package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"docflow/internal/model"
)

// API is the slice of the DynamoDB client this package uses. Keeping it
// narrow makes the store fakeable, and leaves Scan out of reach by
// construction rather than by review.
type API interface {
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// Store is the DynamoDB-backed JobStore.
type Store struct {
	client API
	table  string
	now    func() time.Time
}

// Option configures a Store.
type Option func(*Store)

// WithClock replaces the clock used for the TTL on step and result items, so
// tests do not depend on the wall clock.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// New returns a Store writing to table.
func New(client API, table string, opts ...Option) *Store {
	s := &Store{client: client, table: table, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewFromEnv builds a Store from TABLE_NAME and the ambient AWS
// configuration. Set DYNAMODB_ENDPOINT to point at DynamoDB Local.
//
// Call it during Lambda initialization, not inside a handler, so the client
// is reused across warm invocations.
func NewFromEnv(ctx context.Context, opts ...Option) (*Store, error) {
	table := strings.TrimSpace(os.Getenv("TABLE_NAME"))
	if table == "" {
		return nil, errors.New("TABLE_NAME is not set")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint := strings.TrimSpace(os.Getenv("DYNAMODB_ENDPOINT")); endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return New(client, table, opts...), nil
}

var _ JobStore = (*Store)(nil)

// CreateJob stores a new job, refusing to overwrite an existing one.
func (s *Store) CreateJob(ctx context.Context, job model.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	item, err := attributevalue.MarshalMap(newJobItem(job))
	if err != nil {
		return fmt.Errorf("marshal job %s: %w", job.JobID, err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(#pk)"),
		ExpressionAttributeNames: map[string]string{
			"#pk": pkAttr,
		},
	})
	if err != nil {
		if _, ok := errors.AsType[*types.ConditionalCheckFailedException](err); ok {
			return fmt.Errorf("create job %s: %w", job.JobID, ErrJobExists)
		}
		return fmt.Errorf("create job %s: %w", job.JobID, err)
	}
	return nil
}

// GetJob reads one job.
func (s *Store) GetJob(ctx context.Context, userID, jobID string) (model.Job, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			pkAttr: &types.AttributeValueMemberS{Value: userPK(userID)},
			skAttr: &types.AttributeValueMemberS{Value: jobSK(jobID)},
		},
	})
	if err != nil {
		return model.Job{}, fmt.Errorf("get job %s: %w", jobID, err)
	}
	if len(out.Item) == 0 {
		return model.Job{}, fmt.Errorf("get job %s: %w", jobID, ErrJobNotFound)
	}
	var item jobItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return model.Job{}, fmt.Errorf("unmarshal job %s: %w", jobID, err)
	}
	return item.toModel()
}

// ListJobs returns a page of a user's jobs, newest first.
func (s *Store) ListJobs(ctx context.Context, userID string, limit int32, token string) ([]model.Job, string, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("#pk = :pk AND begins_with(#sk, :prefix)"),
		ExpressionAttributeNames: map[string]string{
			"#pk": pkAttr,
			"#sk": skAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: userPK(userID)},
			":prefix": &types.AttributeValueMemberS{Value: jobPrefix},
		},
		// Job ids are ULIDs, so descending sort-key order is newest first.
		ScanIndexForward: aws.Bool(false),
	}
	if limit > 0 {
		in.Limit = aws.Int32(limit)
	}
	// The token is the last job id of the previous page. Rebuilding the key
	// from it avoids handing callers an opaque blob of DynamoDB internals.
	if token != "" {
		in.ExclusiveStartKey = map[string]types.AttributeValue{
			pkAttr: &types.AttributeValueMemberS{Value: userPK(userID)},
			skAttr: &types.AttributeValueMemberS{Value: jobSK(token)},
		}
	}

	out, err := s.client.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("list jobs of user %s: %w", userID, err)
	}
	var items []jobItem
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &items); err != nil {
		return nil, "", fmt.Errorf("unmarshal jobs of user %s: %w", userID, err)
	}
	jobs := make([]model.Job, 0, len(items))
	for _, item := range items {
		job, err := item.toModel()
		if err != nil {
			return nil, "", err
		}
		jobs = append(jobs, job)
	}

	var next string
	if len(out.LastEvaluatedKey) > 0 && len(jobs) > 0 {
		next = jobs[len(jobs)-1].JobID
	}
	return jobs, next, nil
}

// UpdateStatus performs a status change as a conditional write.
func (s *Store) UpdateStatus(ctx context.Context, userID, jobID string, from, to model.JobStatus, now time.Time) error {
	if to == model.StatusFailed {
		return fmt.Errorf("update job %s to FAILED: use FailJob", jobID)
	}
	if err := from.ValidateTransition(to); err != nil {
		return fmt.Errorf("update job %s: %w", jobID, err)
	}
	return s.conditionalStatusWrite(ctx, userID, jobID, []model.JobStatus{from}, to, "", now)
}

// FailJob moves a job to FAILED from any status the model allows failing
// from, which is PENDING or PROCESSING. Failing an already finished job is a
// conflict, not a no-op.
func (s *Store) FailJob(ctx context.Context, userID, jobID, reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("fail job %s: %w", jobID, model.ErrMissingReason)
	}
	return s.conditionalStatusWrite(ctx, userID, jobID, model.FailableFrom(), model.StatusFailed, reason, now)
}

func (s *Store) conditionalStatusWrite(ctx context.Context, userID, jobID string, from []model.JobStatus, to model.JobStatus, reason string, now time.Time) error {
	if len(from) == 0 {
		return fmt.Errorf("update job %s to %s: no source status to condition on", jobID, to)
	}
	set := []string{"#status = :to", "#updatedAt = :updatedAt"}
	names := map[string]string{
		"#pk":        pkAttr,
		"#status":    statusAttr,
		"#updatedAt": "updatedAt",
	}
	values := map[string]types.AttributeValue{
		":to":        &types.AttributeValueMemberS{Value: string(to)},
		":updatedAt": &types.AttributeValueMemberS{Value: formatTime(now)},
	}
	// One source status compares directly; several become an IN list, which
	// is how FailJob accepts both PENDING and PROCESSING in one write.
	placeholders := make([]string, 0, len(from))
	for i, status := range from {
		name := fmt.Sprintf(":from%d", i)
		placeholders = append(placeholders, name)
		values[name] = &types.AttributeValueMemberS{Value: string(status)}
	}
	statusCondition := "#status = " + placeholders[0]
	if len(placeholders) > 1 {
		statusCondition = "#status IN (" + strings.Join(placeholders, ", ") + ")"
	}
	if reason != "" {
		set = append(set, "#failureReason = :failureReason")
		names["#failureReason"] = "failureReason"
		values[":failureReason"] = &types.AttributeValueMemberS{Value: reason}
	}

	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			pkAttr: &types.AttributeValueMemberS{Value: userPK(userID)},
			skAttr: &types.AttributeValueMemberS{Value: jobSK(jobID)},
		},
		UpdateExpression:          aws.String("SET " + strings.Join(set, ", ")),
		ConditionExpression:       aws.String("attribute_exists(#pk) AND " + statusCondition),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		// Asking for the old item on a failed condition is what lets this
		// tell "no such job" apart from "wrong status"; DynamoDB reports both
		// as the same exception.
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err != nil {
		if failed, ok := errors.AsType[*types.ConditionalCheckFailedException](err); ok {
			if len(failed.Item) == 0 {
				return fmt.Errorf("update job %s to %s: %w", jobID, to, ErrJobNotFound)
			}
			return fmt.Errorf("update job %s to %s: %w", jobID, to, ErrStatusConflict)
		}
		return fmt.Errorf("update job %s to %s: %w", jobID, to, err)
	}
	return nil
}

// PutStepResult records one step's outcome.
func (s *Store) PutStepResult(ctx context.Context, result model.StepResult) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("put step result: %w", err)
	}
	item, err := attributevalue.MarshalMap(newStepItem(result, expiryFrom(s.now())))
	if err != nil {
		return fmt.Errorf("marshal step %s of job %s: %w", result.Step, result.JobID, err)
	}
	// No condition: a retried step overwrites its own earlier attempt, which
	// is what keeps repeated delivery idempotent.
	if _, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      item,
	}); err != nil {
		return fmt.Errorf("put step %s of job %s: %w", result.Step, result.JobID, err)
	}
	return nil
}

// ListStepResults returns every recorded step for a job.
func (s *Store) ListStepResults(ctx context.Context, jobID string) ([]model.StepResult, error) {
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("#pk = :pk AND begins_with(#sk, :prefix)"),
		ExpressionAttributeNames: map[string]string{
			"#pk": pkAttr,
			"#sk": skAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: jobPK(jobID)},
			":prefix": &types.AttributeValueMemberS{Value: stepPrefix},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list steps of job %s: %w", jobID, err)
	}
	var items []stepItem
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &items); err != nil {
		return nil, fmt.Errorf("unmarshal steps of job %s: %w", jobID, err)
	}
	results := make([]model.StepResult, 0, len(items))
	for _, item := range items {
		result, err := item.toModel()
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	sortSteps(results)
	return results, nil
}

// PutResult stores a job's recognized text and extracted fields.
func (s *Store) PutResult(ctx context.Context, result model.DocumentResult) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("put result: %w", err)
	}
	item, err := attributevalue.MarshalMap(newResultItem(result, expiryFrom(s.now())))
	if err != nil {
		return fmt.Errorf("marshal result of job %s: %w", result.JobID, err)
	}
	if _, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      item,
	}); err != nil {
		return fmt.Errorf("put result of job %s: %w", result.JobID, err)
	}
	return nil
}

// GetResult reads a job's stored result.
func (s *Store) GetResult(ctx context.Context, jobID string) (model.DocumentResult, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			pkAttr: &types.AttributeValueMemberS{Value: jobPK(jobID)},
			skAttr: &types.AttributeValueMemberS{Value: resultSK},
		},
	})
	if err != nil {
		return model.DocumentResult{}, fmt.Errorf("get result of job %s: %w", jobID, err)
	}
	if len(out.Item) == 0 {
		return model.DocumentResult{}, fmt.Errorf("get result of job %s: %w", jobID, ErrResultNotFound)
	}
	var item resultItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return model.DocumentResult{}, fmt.Errorf("unmarshal result of job %s: %w", jobID, err)
	}
	return item.toModel()
}
