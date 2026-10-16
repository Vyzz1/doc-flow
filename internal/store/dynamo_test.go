package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"docflow/internal/model"
)

const (
	testTable = "docflow-test"
	testJobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TV"
	testUser  = "user-1"
)

var testNow = time.Date(2026, 10, 15, 8, 30, 0, 0, time.UTC)

// fakeAPI records what the store sent and replays what the test set up. The
// narrow API interface is what makes this possible without a DynamoDB.
type fakeAPI struct {
	put    *dynamodb.PutItemInput
	get    *dynamodb.GetItemInput
	update *dynamodb.UpdateItemInput
	query  *dynamodb.QueryInput

	getOut   *dynamodb.GetItemOutput
	queryOut *dynamodb.QueryOutput

	err error
}

func (f *fakeAPI) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.put = in
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeAPI) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.get = in
	if f.err != nil {
		return nil, f.err
	}
	if f.getOut != nil {
		return f.getOut, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (f *fakeAPI) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.update = in
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeAPI) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.query = in
	if f.err != nil {
		return nil, f.err
	}
	if f.queryOut != nil {
		return f.queryOut, nil
	}
	return &dynamodb.QueryOutput{}, nil
}

func newTestStore(f *fakeAPI) *Store {
	return New(f, testTable, WithClock(func() time.Time { return testNow }))
}

func testJob(t *testing.T) model.Job {
	t.Helper()
	job, err := model.NewJob(testUser, testJobID, model.DocInvoice, testNow)
	if err != nil {
		t.Fatalf("NewJob() error = %v", err)
	}
	return job
}

// stringAttr reads a string attribute, failing the test if it is missing.
func stringAttr(t *testing.T, item map[string]types.AttributeValue, key string) string {
	t.Helper()
	v, ok := item[key].(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("attribute %q = %#v, want a string", key, item[key])
	}
	return v.Value
}

func numberAttr(t *testing.T, item map[string]types.AttributeValue, key string) string {
	t.Helper()
	v, ok := item[key].(*types.AttributeValueMemberN)
	if !ok {
		t.Fatalf("attribute %q = %#v, want a number", key, item[key])
	}
	return v.Value
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func conditionFailed(item map[string]types.AttributeValue) error {
	msg := "The conditional request failed"
	return &types.ConditionalCheckFailedException{Message: &msg, Item: item}
}

func TestCreateJob(t *testing.T) {
	f := &fakeAPI{}
	job := testJob(t)

	if err := newTestStore(f).CreateJob(context.Background(), job); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if f.put == nil {
		t.Fatal("CreateJob() sent no PutItem")
	}
	if got := *f.put.TableName; got != testTable {
		t.Errorf("TableName = %q, want %q", got, testTable)
	}
	if got := *f.put.ConditionExpression; got != "attribute_not_exists(#pk)" {
		t.Errorf("ConditionExpression = %q, want an attribute_not_exists guard", got)
	}
	if got := stringAttr(t, f.put.Item, pkAttr); got != "USER#"+testUser {
		t.Errorf("pk = %q, want USER#%s", got, testUser)
	}
	if got := stringAttr(t, f.put.Item, skAttr); got != "JOB#"+testJobID {
		t.Errorf("sk = %q, want JOB#%s", got, testJobID)
	}
	if got := stringAttr(t, f.put.Item, statusAttr); got != string(model.StatusPending) {
		t.Errorf("status = %q, want PENDING", got)
	}
	if got, want := numberAttr(t, f.put.Item, ttlAttr), job.ExpiresAt.Unix(); got != itoa(want) {
		t.Errorf("%s = %s, want %d", ttlAttr, got, want)
	}
}

func TestCreateJobRejectsDuplicate(t *testing.T) {
	f := &fakeAPI{err: conditionFailed(nil)}

	err := newTestStore(f).CreateJob(context.Background(), testJob(t))
	if !errors.Is(err, ErrJobExists) {
		t.Fatalf("CreateJob() error = %v, want ErrJobExists", err)
	}
}

func TestCreateJobValidatesBeforeCalling(t *testing.T) {
	f := &fakeAPI{}
	job := testJob(t)
	job.UserID = ""

	if err := newTestStore(f).CreateJob(context.Background(), job); err == nil {
		t.Fatal("CreateJob() = nil for an invalid job, want an error")
	}
	if f.put != nil {
		t.Error("CreateJob() called DynamoDB with an invalid job")
	}
}

func TestCreateJobPropagatesOtherErrors(t *testing.T) {
	sentinel := errors.New("throttled")
	f := &fakeAPI{err: sentinel}

	err := newTestStore(f).CreateJob(context.Background(), testJob(t))
	if !errors.Is(err, sentinel) {
		t.Fatalf("CreateJob() error = %v, want it to wrap %v", err, sentinel)
	}
	if errors.Is(err, ErrJobExists) {
		t.Error("a throttling error was reported as ErrJobExists")
	}
}

func TestGetJob(t *testing.T) {
	want := testJob(t)
	item, err := attributevalue.MarshalMap(newJobItem(want))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	f := &fakeAPI{getOut: &dynamodb.GetItemOutput{Item: item}}

	got, err := newTestStore(f).GetJob(context.Background(), testUser, testJobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.JobID != want.JobID || got.UserID != want.UserID || got.Status != want.Status {
		t.Errorf("GetJob() = %+v, want %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("timestamps did not round-trip: %+v", got)
	}
	if got := stringAttr(t, f.get.Key, pkAttr); got != "USER#"+testUser {
		t.Errorf("key pk = %q, want USER#%s", got, testUser)
	}
}

func TestGetJobNotFound(t *testing.T) {
	f := &fakeAPI{getOut: &dynamodb.GetItemOutput{}}

	_, err := newTestStore(f).GetJob(context.Background(), testUser, testJobID)
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("GetJob() error = %v, want ErrJobNotFound", err)
	}
}

func TestGetJobRejectsCorruptItem(t *testing.T) {
	job := testJob(t)
	item, err := attributevalue.MarshalMap(newJobItem(job))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	tests := []struct {
		name    string
		corrupt func(map[string]types.AttributeValue)
	}{
		{
			name: "unparseable timestamp",
			corrupt: func(m map[string]types.AttributeValue) {
				m["createdAt"] = &types.AttributeValueMemberS{Value: "yesterday"}
			},
		},
		{
			name: "status outside the state machine",
			corrupt: func(m map[string]types.AttributeValue) {
				m[statusAttr] = &types.AttributeValueMemberS{Value: "QUEUED"}
			},
		},
		{
			name: "failed without a reason",
			corrupt: func(m map[string]types.AttributeValue) {
				m[statusAttr] = &types.AttributeValueMemberS{Value: string(model.StatusFailed)}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			corrupted := make(map[string]types.AttributeValue, len(item))
			for k, v := range item {
				corrupted[k] = v
			}
			tt.corrupt(corrupted)
			f := &fakeAPI{getOut: &dynamodb.GetItemOutput{Item: corrupted}}

			if _, err := newTestStore(f).GetJob(context.Background(), testUser, testJobID); err == nil {
				t.Error("GetJob() = nil for a corrupt item, want an error")
			}
		})
	}
}

func TestListJobs(t *testing.T) {
	older := testJob(t)
	newer := testJob(t)
	newer.JobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TW"
	newer.UploadKey = model.UploadKey(newer.UserID, newer.JobID)

	items := make([]map[string]types.AttributeValue, 0, 2)
	for _, job := range []model.Job{newer, older} {
		item, err := attributevalue.MarshalMap(newJobItem(job))
		if err != nil {
			t.Fatalf("MarshalMap() error = %v", err)
		}
		items = append(items, item)
	}
	f := &fakeAPI{queryOut: &dynamodb.QueryOutput{
		Items:            items,
		LastEvaluatedKey: map[string]types.AttributeValue{pkAttr: &types.AttributeValueMemberS{Value: "x"}},
	}}

	jobs, token, err := newTestStore(f).ListJobs(context.Background(), testUser, 2, "")
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("ListJobs() returned %d jobs, want 2", len(jobs))
	}
	if jobs[0].JobID != newer.JobID {
		t.Errorf("first job = %s, want the newest %s", jobs[0].JobID, newer.JobID)
	}
	if token != older.JobID {
		t.Errorf("token = %q, want the last job id %q", token, older.JobID)
	}
	if f.query.ScanIndexForward == nil || *f.query.ScanIndexForward {
		t.Error("query did not ask for descending order, so jobs are not newest first")
	}
	if f.query.Limit == nil || *f.query.Limit != 2 {
		t.Errorf("Limit = %v, want 2", f.query.Limit)
	}
	if f.query.ExclusiveStartKey != nil {
		t.Error("first page sent an ExclusiveStartKey")
	}
}

func TestListJobsContinuesFromToken(t *testing.T) {
	f := &fakeAPI{queryOut: &dynamodb.QueryOutput{}}

	jobs, token, err := newTestStore(f).ListJobs(context.Background(), testUser, 0, testJobID)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 0 || token != "" {
		t.Errorf("empty page returned jobs=%v token=%q", jobs, token)
	}
	if got := stringAttr(t, f.query.ExclusiveStartKey, skAttr); got != "JOB#"+testJobID {
		t.Errorf("ExclusiveStartKey sk = %q, want JOB#%s", got, testJobID)
	}
	if f.query.Limit != nil {
		t.Errorf("Limit = %v for limit 0, want it unset", *f.query.Limit)
	}
}

func TestListJobsStopsPagingWithoutLastEvaluatedKey(t *testing.T) {
	item, err := attributevalue.MarshalMap(newJobItem(testJob(t)))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	f := &fakeAPI{queryOut: &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{item}}}

	_, token, err := newTestStore(f).ListJobs(context.Background(), testUser, 10, "")
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if token != "" {
		t.Errorf("token = %q on the last page, want empty", token)
	}
}

func TestUpdateStatus(t *testing.T) {
	f := &fakeAPI{}
	later := testNow.Add(time.Minute)

	err := newTestStore(f).UpdateStatus(context.Background(), testUser, testJobID, model.StatusPending, model.StatusProcessing, later)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if got := *f.update.ConditionExpression; got != "attribute_exists(#pk) AND #status = :from0" {
		t.Errorf("ConditionExpression = %q, want it to pin the current status", got)
	}
	from, ok := f.update.ExpressionAttributeValues[":from0"].(*types.AttributeValueMemberS)
	if !ok || from.Value != string(model.StatusPending) {
		t.Errorf(":from0 = %#v, want PENDING", f.update.ExpressionAttributeValues[":from0"])
	}
	to, ok := f.update.ExpressionAttributeValues[":to"].(*types.AttributeValueMemberS)
	if !ok || to.Value != string(model.StatusProcessing) {
		t.Errorf(":to = %#v, want PROCESSING", f.update.ExpressionAttributeValues[":to"])
	}
	if f.update.ReturnValuesOnConditionCheckFailure != types.ReturnValuesOnConditionCheckFailureAllOld {
		t.Error("update did not ask for the old item, so a failed condition cannot be diagnosed")
	}
	if _, found := f.update.ExpressionAttributeNames["#failureReason"]; found {
		t.Error("a non-failing transition touched failureReason")
	}
}

func TestUpdateStatusRejectsBadTransitions(t *testing.T) {
	tests := []struct {
		name string
		from model.JobStatus
		to   model.JobStatus
	}{
		{name: "skips processing", from: model.StatusPending, to: model.StatusDone},
		{name: "backwards", from: model.StatusProcessing, to: model.StatusPending},
		{name: "revives a finished job", from: model.StatusDone, to: model.StatusProcessing},
		{name: "failed belongs to FailJob", from: model.StatusProcessing, to: model.StatusFailed},
		{name: "unknown target", from: model.StatusPending, to: model.JobStatus("QUEUED")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{}
			err := newTestStore(f).UpdateStatus(context.Background(), testUser, testJobID, tt.from, tt.to, testNow)
			if err == nil {
				t.Fatal("UpdateStatus() = nil, want an error")
			}
			if f.update != nil {
				t.Error("UpdateStatus() called DynamoDB for an impossible transition")
			}
		})
	}
}

func TestUpdateStatusConditionFailure(t *testing.T) {
	job := testJob(t)
	stored, err := attributevalue.MarshalMap(newJobItem(job))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	tests := []struct {
		name    string
		item    map[string]types.AttributeValue
		wantErr error
	}{
		{name: "no such job", item: nil, wantErr: ErrJobNotFound},
		{name: "wrong status", item: stored, wantErr: ErrStatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{err: conditionFailed(tt.item)}
			err := newTestStore(f).UpdateStatus(context.Background(), testUser, testJobID, model.StatusPending, model.StatusProcessing, testNow)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("UpdateStatus() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestFailJob(t *testing.T) {
	f := &fakeAPI{}

	err := newTestStore(f).FailJob(context.Background(), testUser, testJobID, "  unsupported file type\n", testNow)
	if err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}
	reason, ok := f.update.ExpressionAttributeValues[":failureReason"].(*types.AttributeValueMemberS)
	if !ok || reason.Value != "unsupported file type" {
		t.Errorf(":failureReason = %#v, want the trimmed reason", f.update.ExpressionAttributeValues[":failureReason"])
	}
	if got := *f.update.ConditionExpression; got != "attribute_exists(#pk) AND #status IN (:from0, :from1)" {
		t.Errorf("ConditionExpression = %q, want an IN over the failable statuses", got)
	}
	// Whatever the model says may fail must appear in the condition, so the
	// two cannot drift apart.
	want := map[string]bool{}
	for _, status := range model.FailableFrom() {
		want[string(status)] = true
	}
	got := map[string]bool{}
	for name, value := range f.update.ExpressionAttributeValues {
		if strings.HasPrefix(name, ":from") {
			got[value.(*types.AttributeValueMemberS).Value] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("condition covers %v, want %v", got, want)
	}
	for status := range want {
		if !got[status] {
			t.Errorf("condition does not cover %s", status)
		}
	}
}

func TestFailJobFromPending(t *testing.T) {
	// The starter rejects a bad upload before any work begins, and fails the
	// job in one write rather than moving it through PROCESSING first.
	f := &fakeAPI{}

	if err := newTestStore(f).FailJob(context.Background(), testUser, testJobID, "magic bytes rejected", testNow); err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}
	if f.update == nil {
		t.Fatal("FailJob() sent no UpdateItem")
	}
	to, ok := f.update.ExpressionAttributeValues[":to"].(*types.AttributeValueMemberS)
	if !ok || to.Value != string(model.StatusFailed) {
		t.Errorf(":to = %#v, want FAILED", f.update.ExpressionAttributeValues[":to"])
	}
}

func TestFailJobOnFinishedJobConflicts(t *testing.T) {
	done := testJob(t)
	done.Status = model.StatusDone
	stored, err := attributevalue.MarshalMap(newJobItem(done))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	f := &fakeAPI{err: conditionFailed(stored)}

	err = newTestStore(f).FailJob(context.Background(), testUser, testJobID, "too late", testNow)
	if !errors.Is(err, ErrStatusConflict) {
		t.Errorf("FailJob() on a DONE job = %v, want ErrStatusConflict", err)
	}
}

func TestFailJobNeedsAReason(t *testing.T) {
	for _, reason := range []string{"", "   "} {
		f := &fakeAPI{}
		err := newTestStore(f).FailJob(context.Background(), testUser, testJobID, reason, testNow)
		if !errors.Is(err, model.ErrMissingReason) {
			t.Errorf("FailJob(%q) error = %v, want ErrMissingReason", reason, err)
		}
		if f.update != nil {
			t.Errorf("FailJob(%q) called DynamoDB without a reason", reason)
		}
	}
}

func TestPutStepResult(t *testing.T) {
	f := &fakeAPI{}
	result, err := model.NewStepResult(testJobID, model.StepOCR, testNow, testNow.Add(2*time.Second), "derived/"+testJobID+".txt")
	if err != nil {
		t.Fatalf("NewStepResult() error = %v", err)
	}

	if err := newTestStore(f).PutStepResult(context.Background(), result); err != nil {
		t.Fatalf("PutStepResult() error = %v", err)
	}
	if got := stringAttr(t, f.put.Item, pkAttr); got != "JOB#"+testJobID {
		t.Errorf("pk = %q, want JOB#%s", got, testJobID)
	}
	if got := stringAttr(t, f.put.Item, skAttr); got != "STEP#ocr" {
		t.Errorf("sk = %q, want STEP#ocr", got)
	}
	if got := numberAttr(t, f.put.Item, "durationMs"); got != "2000" {
		t.Errorf("durationMs = %s, want 2000", got)
	}
	// A retried step must overwrite its own earlier attempt.
	if f.put.ConditionExpression != nil {
		t.Errorf("ConditionExpression = %q, want none so retries are idempotent", *f.put.ConditionExpression)
	}
	if got, want := numberAttr(t, f.put.Item, ttlAttr), testNow.Add(model.Retention).Unix(); got != itoa(want) {
		t.Errorf("%s = %s, want %d", ttlAttr, got, want)
	}
}

func TestPutStepResultValidatesBeforeCalling(t *testing.T) {
	f := &fakeAPI{}
	bad := model.StepResult{JobID: testJobID, Step: model.StepOCR, Status: model.StepFailed}

	if err := newTestStore(f).PutStepResult(context.Background(), bad); err == nil {
		t.Fatal("PutStepResult() = nil for a failed step with no reason, want an error")
	}
	if f.put != nil {
		t.Error("PutStepResult() called DynamoDB with an invalid result")
	}
}

func TestListStepResultsOrdersByWorkflow(t *testing.T) {
	// DynamoDB returns sort keys alphabetically, which is not the order the
	// workflow runs them in.
	alphabetical := []model.StepName{model.StepExtract, model.StepOCR, model.StepPreprocess, model.StepSave, model.StepThumbnail, model.StepValidate}
	items := make([]map[string]types.AttributeValue, 0, len(alphabetical))
	for _, step := range alphabetical {
		result, err := model.NewStepResult(testJobID, step, testNow, testNow.Add(time.Second))
		if err != nil {
			t.Fatalf("NewStepResult(%s) error = %v", step, err)
		}
		item, err := attributevalue.MarshalMap(newStepItem(result, expiryFrom(testNow)))
		if err != nil {
			t.Fatalf("MarshalMap() error = %v", err)
		}
		items = append(items, item)
	}
	f := &fakeAPI{queryOut: &dynamodb.QueryOutput{Items: items}}

	got, err := newTestStore(f).ListStepResults(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("ListStepResults() error = %v", err)
	}
	want := []model.StepName{model.StepValidate, model.StepPreprocess, model.StepThumbnail, model.StepOCR, model.StepExtract, model.StepSave}
	for i, step := range want {
		if got[i].Step != step {
			t.Errorf("step %d = %s, want %s", i, got[i].Step, step)
		}
	}
	if got := stringAttr(t, f.query.ExpressionAttributeValues, ":prefix"); got != stepPrefix {
		t.Errorf("query prefix = %q, want %q", got, stepPrefix)
	}
}

func TestResultRoundTrip(t *testing.T) {
	want := model.DocumentResult{
		JobID:      testJobID,
		Provider:   model.ProviderTesseract,
		Confidence: 0.92,
		Pages:      2,
		Text:       "INVOICE\nTotal 1.250.000 VND",
		Fields: []model.Field{
			{Name: "total", Value: "1250000", Confidence: 0.88},
			{Name: "currency", Value: "VND", Confidence: 0.99},
		},
	}

	writer := &fakeAPI{}
	if err := newTestStore(writer).PutResult(context.Background(), want); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	if got := stringAttr(t, writer.put.Item, skAttr); got != resultSK {
		t.Errorf("sk = %q, want %q", got, resultSK)
	}

	reader := &fakeAPI{getOut: &dynamodb.GetItemOutput{Item: writer.put.Item}}
	got, err := newTestStore(reader).GetResult(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("GetResult() error = %v", err)
	}
	if got.Text != want.Text || got.Pages != want.Pages || got.Provider != want.Provider {
		t.Errorf("GetResult() = %+v, want %+v", got, want)
	}
	if len(got.Fields) != len(want.Fields) {
		t.Fatalf("GetResult() returned %d fields, want %d", len(got.Fields), len(want.Fields))
	}
	for i := range want.Fields {
		if got.Fields[i] != want.Fields[i] {
			t.Errorf("field %d = %+v, want %+v", i, got.Fields[i], want.Fields[i])
		}
	}
}

func TestGetResultNotFound(t *testing.T) {
	f := &fakeAPI{getOut: &dynamodb.GetItemOutput{}}

	_, err := newTestStore(f).GetResult(context.Background(), testJobID)
	if !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("GetResult() error = %v, want ErrResultNotFound", err)
	}
}

func TestPutResultValidatesBeforeCalling(t *testing.T) {
	f := &fakeAPI{}
	bad := model.DocumentResult{JobID: testJobID, Provider: model.ProviderTesseract, Confidence: 92, Pages: 1}

	if err := newTestStore(f).PutResult(context.Background(), bad); err == nil {
		t.Fatal("PutResult() = nil for a confidence of 92, want an error")
	}
	if f.put != nil {
		t.Error("PutResult() called DynamoDB with an invalid result")
	}
}

func TestResultWithoutFields(t *testing.T) {
	want := model.DocumentResult{
		JobID:      testJobID,
		Provider:   model.ProviderTextract,
		Confidence: 0.4,
		Pages:      1,
		Text:       "",
	}
	writer := &fakeAPI{}
	if err := newTestStore(writer).PutResult(context.Background(), want); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	reader := &fakeAPI{getOut: &dynamodb.GetItemOutput{Item: writer.put.Item}}

	got, err := newTestStore(reader).GetResult(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("GetResult() error = %v", err)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Fields = %v, want none", got.Fields)
	}
}

func TestNewFromEnvNeedsATableName(t *testing.T) {
	t.Setenv("TABLE_NAME", "  ")
	if _, err := NewFromEnv(context.Background()); err == nil {
		t.Fatal("NewFromEnv() = nil without TABLE_NAME, want an error")
	}
}

// TestTransportErrorsAreWrapped checks that a failure from DynamoDB reaches
// the caller intact rather than being swallowed or mistaken for a sentinel.
func TestTransportErrorsAreWrapped(t *testing.T) {
	sentinel := errors.New("throttled")
	ctx := context.Background()

	tests := []struct {
		name string
		call func(*Store) error
	}{
		{name: "GetJob", call: func(s *Store) error {
			_, err := s.GetJob(ctx, testUser, testJobID)
			return err
		}},
		{name: "ListJobs", call: func(s *Store) error {
			_, _, err := s.ListJobs(ctx, testUser, 10, "")
			return err
		}},
		{name: "UpdateStatus", call: func(s *Store) error {
			return s.UpdateStatus(ctx, testUser, testJobID, model.StatusPending, model.StatusProcessing, testNow)
		}},
		{name: "FailJob", call: func(s *Store) error {
			return s.FailJob(ctx, testUser, testJobID, "boom", testNow)
		}},
		{name: "PutStepResult", call: func(s *Store) error {
			r, err := model.NewStepResult(testJobID, model.StepOCR, testNow, testNow)
			if err != nil {
				return err
			}
			return s.PutStepResult(ctx, r)
		}},
		{name: "ListStepResults", call: func(s *Store) error {
			_, err := s.ListStepResults(ctx, testJobID)
			return err
		}},
		{name: "PutResult", call: func(s *Store) error {
			return s.PutResult(ctx, model.DocumentResult{
				JobID: testJobID, Provider: model.ProviderTesseract, Confidence: 0.5, Pages: 1,
			})
		}},
		{name: "GetResult", call: func(s *Store) error {
			_, err := s.GetResult(ctx, testJobID)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(newTestStore(&fakeAPI{err: sentinel}))
			if !errors.Is(err, sentinel) {
				t.Errorf("%s error = %v, want it to wrap %v", tt.name, err, sentinel)
			}
			for _, wrong := range []error{ErrJobNotFound, ErrStatusConflict, ErrResultNotFound, ErrJobExists} {
				if errors.Is(err, wrong) {
					t.Errorf("%s reported a transport failure as %v", tt.name, wrong)
				}
			}
		})
	}
}

func TestQueriesRejectCorruptItems(t *testing.T) {
	corruptJob, err := attributevalue.MarshalMap(newJobItem(testJob(t)))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	corruptJob[statusAttr] = &types.AttributeValueMemberS{Value: "QUEUED"}

	step, err := model.NewStepResult(testJobID, model.StepOCR, testNow, testNow)
	if err != nil {
		t.Fatalf("NewStepResult() error = %v", err)
	}
	corruptStep, err := attributevalue.MarshalMap(newStepItem(step, expiryFrom(testNow)))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	corruptStep["startedAt"] = &types.AttributeValueMemberS{Value: "yesterday"}

	corruptResult, err := attributevalue.MarshalMap(newResultItem(model.DocumentResult{
		JobID: testJobID, Provider: model.ProviderTesseract, Confidence: 0.5, Pages: 1,
	}, expiryFrom(testNow)))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	corruptResult["pages"] = &types.AttributeValueMemberN{Value: "0"}

	tests := []struct {
		name string
		call func(*Store) error
		fake *fakeAPI
	}{
		{
			name: "ListJobs",
			fake: &fakeAPI{queryOut: &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{corruptJob}}},
			call: func(s *Store) error {
				_, _, err := s.ListJobs(context.Background(), testUser, 10, "")
				return err
			},
		},
		{
			name: "ListStepResults",
			fake: &fakeAPI{queryOut: &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{corruptStep}}},
			call: func(s *Store) error {
				_, err := s.ListStepResults(context.Background(), testJobID)
				return err
			},
		},
		{
			name: "GetResult",
			fake: &fakeAPI{getOut: &dynamodb.GetItemOutput{Item: corruptResult}},
			call: func(s *Store) error {
				_, err := s.GetResult(context.Background(), testJobID)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(newTestStore(tt.fake)); err == nil {
				t.Error("a corrupt stored item was accepted, want an error")
			}
		})
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("TABLE_NAME", "docflow")
	t.Setenv("AWS_REGION", "ap-southeast-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	t.Setenv("DYNAMODB_ENDPOINT", "http://127.0.0.1:8000")

	s, err := NewFromEnv(context.Background())
	if err != nil {
		t.Fatalf("NewFromEnv() error = %v", err)
	}
	if s.table != "docflow" {
		t.Errorf("table = %q, want docflow", s.table)
	}
	if s.client == nil {
		t.Error("NewFromEnv() built a store with no client")
	}
}
