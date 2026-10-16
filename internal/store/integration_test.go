//go:build integration

// These run against DynamoDB Local, started by `make up`. They exist for
// the things a fake cannot show: that a conditional expression is actually
// evaluated by the database, that two writers racing produce one winner,
// and that an item round-trips through real marshalling.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"docflow/internal/model"
)

// newLiveStore returns a store backed by DynamoDB Local, with a table of its
// own so parallel tests cannot collide.
func newLiveStore(t *testing.T) *Store {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("DYNAMODB_ENDPOINT"))
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT is not set; run make up")
	}

	ctx := t.Context()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("ap-southeast-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
		// DynamoDB Local does not return the checksums the SDK now
		// validates by default, and a mismatch makes it discard the
		// connection. Real DynamoDB does return them, so this is a
		// concession to the stand-in and stays out of production code.
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	table := fmt.Sprintf("docflow-%s-%d", strings.ToLower(strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())), time.Now().UnixNano())
	createTable(t, ctx, client, table)
	return New(client, table)
}

func createTable(t *testing.T, ctx context.Context, client *dynamodb.Client, table string) {
	t.Helper()
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String(pkAttr), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String(skAttr), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String(pkAttr), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String(skAttr), KeyType: types.KeyTypeRange},
		},
	})
	if err != nil {
		t.Fatalf("create table %s: %v", table, err)
	}
	t.Cleanup(func() {
		//nolint:errcheck // the container is thrown away after the run
		client.DeleteTable(context.WithoutCancel(ctx), &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	})
}

func liveJob(t *testing.T, jobID string) model.Job {
	t.Helper()
	job, err := model.NewJob("user-1", jobID, model.DocInvoice, time.Now())
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}
	job.ContentType = "application/pdf"
	return job
}

// newID mints a ULID so each test writes its own job.
func newID(t *testing.T) string {
	t.Helper()
	id, err := model.NewJobIDNow()
	if err != nil {
		t.Fatalf("NewJobIDNow: %v", err)
	}
	return id
}

func TestLiveJobRoundTrip(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()
	job := liveJob(t, newID(t))

	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := s.GetJob(ctx, job.UserID, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.JobID != job.JobID || got.DocType != job.DocType || got.ContentType != job.ContentType {
		t.Errorf("GetJob = %+v, want %+v", got, job)
	}
	// CreatedAt is stored as RFC3339 with nanoseconds and comes back
	// exactly.
	if !got.CreatedAt.Equal(job.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, job.CreatedAt)
	}
	// ExpiresAt does not, and must not be expected to: it is the TTL
	// attribute, which DynamoDB defines as whole Unix seconds. Anything
	// finer is dropped on the way in. Nothing reads it back for precision,
	// but a caller comparing the two would otherwise be puzzled.
	if want := job.ExpiresAt.Truncate(time.Second); !got.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v truncated to the second", got.ExpiresAt, want)
	}
}

// The condition is the whole point of CreateJob, and a fake can only report
// that one was sent.
func TestLiveCreateJobRefusesADuplicate(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()
	job := liveJob(t, newID(t))

	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s.CreateJob(ctx, job); !errors.Is(err, ErrJobExists) {
		t.Fatalf("second CreateJob = %v, want ErrJobExists", err)
	}
}

func TestLiveStatusTransitions(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()
	job := liveJob(t, newID(t))
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	now := time.Now()
	if err := s.UpdateStatus(ctx, job.UserID, job.JobID, model.StatusPending, model.StatusProcessing, now); err != nil {
		t.Fatalf("start processing: %v", err)
	}
	// The same move again must fail: the stored status is no longer PENDING.
	err := s.UpdateStatus(ctx, job.UserID, job.JobID, model.StatusPending, model.StatusProcessing, now)
	if !errors.Is(err, ErrStatusConflict) {
		t.Fatalf("repeated transition = %v, want ErrStatusConflict", err)
	}
	if err := s.UpdateStatus(ctx, job.UserID, job.JobID, model.StatusProcessing, model.StatusDone, now); err != nil {
		t.Fatalf("finish: %v", err)
	}

	got, err := s.GetJob(ctx, job.UserID, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != model.StatusDone {
		t.Errorf("Status = %q, want DONE", got.Status)
	}
}

// FailJob's condition is an IN over the statuses model.FailableFrom lists.
// Whether DynamoDB evaluates that the way the unit tests assume is exactly
// what a fake cannot answer.
func TestLiveFailJobFromEitherStatus(t *testing.T) {
	tests := []struct {
		name    string
		advance bool
	}{
		{name: "from pending", advance: false},
		{name: "from processing", advance: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newLiveStore(t)
			ctx := t.Context()
			job := liveJob(t, newID(t))
			if err := s.CreateJob(ctx, job); err != nil {
				t.Fatalf("CreateJob: %v", err)
			}
			now := time.Now()
			if tt.advance {
				if err := s.UpdateStatus(ctx, job.UserID, job.JobID, model.StatusPending, model.StatusProcessing, now); err != nil {
					t.Fatalf("start processing: %v", err)
				}
			}

			if err := s.FailJob(ctx, job.UserID, job.JobID, "unsupported file type", now); err != nil {
				t.Fatalf("FailJob: %v", err)
			}
			got, err := s.GetJob(ctx, job.UserID, job.JobID)
			if err != nil {
				t.Fatalf("GetJob: %v", err)
			}
			if got.Status != model.StatusFailed || got.FailureReason == "" {
				t.Errorf("job = %+v, want FAILED with a reason", got)
			}

			// A finished job cannot be failed again.
			if err := s.FailJob(ctx, job.UserID, job.JobID, "again", now); !errors.Is(err, ErrStatusConflict) {
				t.Errorf("second FailJob = %v, want ErrStatusConflict", err)
			}
		})
	}
}

// DynamoDB reports a missing item and a failed condition the same way, and
// the store tells them apart by asking for the old item. That behaviour is
// a property of the service, not of the code.
func TestLiveUpdateDistinguishesMissingFromConflict(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()

	missing := newID(t)
	err := s.UpdateStatus(ctx, "user-1", missing, model.StatusPending, model.StatusProcessing, time.Now())
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("update of a missing job = %v, want ErrJobNotFound", err)
	}

	job := liveJob(t, newID(t))
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	err = s.UpdateStatus(ctx, job.UserID, job.JobID, model.StatusProcessing, model.StatusDone, time.Now())
	if !errors.Is(err, ErrStatusConflict) {
		t.Fatalf("update from the wrong status = %v, want ErrStatusConflict", err)
	}
}

// Only one of several workers racing to claim a job may win. This is the
// property the whole idempotency story rests on, and no fake can show it.
func TestLiveOnlyOneWorkerClaimsAJob(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()
	job := liveJob(t, newID(t))
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	const workers = 12
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		won       int
		conflicts int
		other     []error
	)
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.UpdateStatus(ctx, job.UserID, job.JobID, model.StatusPending, model.StatusProcessing, time.Now())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, ErrStatusConflict):
				conflicts++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if won != 1 {
		t.Errorf("%d workers claimed the job, want exactly 1", won)
	}
	if conflicts != workers-1 {
		t.Errorf("%d workers saw a conflict, want %d", conflicts, workers-1)
	}
}

func TestLiveListJobsIsNewestFirst(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()

	var ids []string
	for range 5 {
		job := liveJob(t, newID(t))
		if err := s.CreateJob(ctx, job); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		ids = append(ids, job.JobID)
		// ULIDs carry a millisecond timestamp, so space the writes out
		// enough that their order is not down to the random suffix.
		time.Sleep(2 * time.Millisecond)
	}

	jobs, _, err := s.ListJobs(ctx, "user-1", 10, "")
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != len(ids) {
		t.Fatalf("ListJobs returned %d jobs, want %d", len(jobs), len(ids))
	}
	for i, job := range jobs {
		want := ids[len(ids)-1-i]
		if job.JobID != want {
			t.Errorf("job %d = %s, want %s: the page is not newest first", i, job.JobID, want)
		}
	}
}

func TestLiveListJobsPages(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()

	for range 5 {
		if err := s.CreateJob(ctx, liveJob(t, newID(t))); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	seen := map[string]bool{}
	token := ""
	for page := range 10 {
		jobs, next, err := s.ListJobs(ctx, "user-1", 2, token)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, job := range jobs {
			if seen[job.JobID] {
				t.Errorf("job %s appeared on two pages", job.JobID)
			}
			seen[job.JobID] = true
		}
		if next == "" {
			break
		}
		token = next
	}
	if len(seen) != 5 {
		t.Errorf("paging saw %d jobs, want 5", len(seen))
	}
}

func TestLiveStepResultsAndResult(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()
	job := liveJob(t, newID(t))
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	started := time.Now()
	for _, step := range []model.StepName{model.StepValidate, model.StepOCR} {
		result, err := model.NewStepResult(job.JobID, step, started, started.Add(time.Second))
		if err != nil {
			t.Fatalf("NewStepResult: %v", err)
		}
		if err := s.PutStepResult(ctx, result); err != nil {
			t.Fatalf("PutStepResult: %v", err)
		}
	}
	// A retried step overwrites its own earlier attempt rather than adding
	// a second item.
	retried, err := model.NewStepResult(job.JobID, model.StepValidate, started, started.Add(2*time.Second))
	if err != nil {
		t.Fatalf("NewStepResult: %v", err)
	}
	if err := s.PutStepResult(ctx, retried); err != nil {
		t.Fatalf("PutStepResult: %v", err)
	}

	steps, err := s.ListStepResults(ctx, job.JobID)
	if err != nil {
		t.Fatalf("ListStepResults: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("ListStepResults returned %d steps, want 2", len(steps))
	}
	// Workflow order, not the alphabetical order DynamoDB stores them in.
	if steps[0].Step != model.StepValidate || steps[1].Step != model.StepOCR {
		t.Errorf("steps = %s then %s, want validate then ocr", steps[0].Step, steps[1].Step)
	}

	want := model.DocumentResult{
		JobID: job.JobID, Provider: model.ProviderTesseract, Confidence: 0.91, Pages: 2,
		Text:   "INVOICE",
		Fields: []model.Field{{Name: "total", Value: "1250000", Confidence: 0.88}},
	}
	if err := s.PutResult(ctx, want); err != nil {
		t.Fatalf("PutResult: %v", err)
	}
	got, err := s.GetResult(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if got.Text != want.Text || got.Pages != want.Pages || len(got.Fields) != 1 {
		t.Errorf("GetResult = %+v, want %+v", got, want)
	}
	if got.Fields[0] != want.Fields[0] {
		t.Errorf("field = %+v, want %+v", got.Fields[0], want.Fields[0])
	}
}

func TestLiveMissingThingsAreNotFound(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()

	if _, err := s.GetJob(ctx, "user-1", newID(t)); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("GetJob = %v, want ErrJobNotFound", err)
	}
	if _, err := s.GetResult(ctx, newID(t)); !errors.Is(err, ErrResultNotFound) {
		t.Errorf("GetResult = %v, want ErrResultNotFound", err)
	}
}

// Every item needs the attribute the table's TTL is configured on, or
// nothing ever expires and the free tier fills up quietly.
func TestLiveEveryItemCarriesTheTTLAttribute(t *testing.T) {
	s := newLiveStore(t)
	ctx := t.Context()
	job := liveJob(t, newID(t))
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	step, err := model.NewStepResult(job.JobID, model.StepValidate, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("NewStepResult: %v", err)
	}
	if err := s.PutStepResult(ctx, step); err != nil {
		t.Fatalf("PutStepResult: %v", err)
	}
	if err := s.PutResult(ctx, model.DocumentResult{
		JobID: job.JobID, Provider: model.ProviderTesseract, Confidence: 0.5, Pages: 1,
	}); err != nil {
		t.Fatalf("PutResult: %v", err)
	}

	client, ok := s.client.(*dynamodb.Client)
	if !ok {
		t.Fatalf("store is not backed by a real client")
	}
	for _, key := range []map[string]types.AttributeValue{
		{pkAttr: &types.AttributeValueMemberS{Value: UserPK(job.UserID)}, skAttr: &types.AttributeValueMemberS{Value: "JOB#" + job.JobID}},
		{pkAttr: &types.AttributeValueMemberS{Value: "JOB#" + job.JobID}, skAttr: &types.AttributeValueMemberS{Value: "STEP#validate"}},
		{pkAttr: &types.AttributeValueMemberS{Value: "JOB#" + job.JobID}, skAttr: &types.AttributeValueMemberS{Value: resultSK}},
	} {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.table), Key: key})
		if err != nil {
			t.Fatalf("GetItem: %v", err)
		}
		if len(out.Item) == 0 {
			t.Fatalf("item %v was not stored", key)
		}
		ttl, found := out.Item[TTLAttr]
		if !found {
			t.Errorf("item %v has no %s, so it will never expire", key, TTLAttr)
			continue
		}
		if _, isNumber := ttl.(*types.AttributeValueMemberN); !isNumber {
			t.Errorf("item %v has a non-numeric %s, which DynamoDB ignores", key, TTLAttr)
		}
	}
}
