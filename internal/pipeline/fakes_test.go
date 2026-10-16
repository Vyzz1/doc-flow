package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sfn"

	"docflow/internal/model"
	"docflow/internal/storage"
	"docflow/internal/store"
)

const (
	testUser  = "user-1"
	testJobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TV"
	testARN   = "arn:aws:states:ap-southeast-1:123456789012:stateMachine:DocFlow"
)

var testNow = time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

func testUploadKey() string { return model.UploadKey(testUser, testJobID) }

// fakeStore is a JobStore whose every method can be told what to return.
type fakeStore struct {
	job   model.Job
	steps []model.StepResult

	statusCalls []statusCall
	failCalls   []failCall

	failures map[string]error
}

type statusCall struct {
	from model.JobStatus
	to   model.JobStatus
}

type failCall struct {
	reason string
}

func newFakeStore(t interface{ Helper() }) *fakeStore {
	t.Helper()
	job, err := model.NewJob(testUser, testJobID, model.DocInvoice, testNow)
	if err != nil {
		panic(err)
	}
	job.ContentType = "application/pdf"
	return &fakeStore{job: job, failures: map[string]error{}}
}

func (f *fakeStore) fail(method string, err error) *fakeStore {
	f.failures[method] = err
	return f
}

func (f *fakeStore) CreateJob(context.Context, model.Job) error { return f.failures["CreateJob"] }

func (f *fakeStore) GetJob(context.Context, string, string) (model.Job, error) {
	if err := f.failures["GetJob"]; err != nil {
		return model.Job{}, err
	}
	return f.job, nil
}

func (f *fakeStore) ListJobs(context.Context, string, int32, string) ([]model.Job, string, error) {
	return nil, "", f.failures["ListJobs"]
}

func (f *fakeStore) UpdateStatus(_ context.Context, _, _ string, from, to model.JobStatus, _ time.Time) error {
	f.statusCalls = append(f.statusCalls, statusCall{from: from, to: to})
	return f.failures["UpdateStatus"]
}

func (f *fakeStore) FailJob(_ context.Context, _, _, reason string, _ time.Time) error {
	f.failCalls = append(f.failCalls, failCall{reason: reason})
	return f.failures["FailJob"]
}

func (f *fakeStore) PutStepResult(_ context.Context, result model.StepResult) error {
	if err := f.failures["PutStepResult"]; err != nil {
		return err
	}
	f.steps = append(f.steps, result)
	return nil
}

func (f *fakeStore) ListStepResults(context.Context, string) ([]model.StepResult, error) {
	return f.steps, f.failures["ListStepResults"]
}

func (f *fakeStore) PutResult(context.Context, model.DocumentResult) error {
	return f.failures["PutResult"]
}

func (f *fakeStore) GetResult(context.Context, string) (model.DocumentResult, error) {
	return model.DocumentResult{}, f.failures["GetResult"]
}

var _ store.JobStore = (*fakeStore)(nil)

// fakeDocuments serves one object's bytes.
type fakeDocuments struct {
	body []byte

	sizeErr   error
	headerErr error
	readErr   error

	headerBytes int64
}

func (f *fakeDocuments) Size(context.Context, string) (int64, error) {
	if f.sizeErr != nil {
		return 0, f.sizeErr
	}
	return int64(len(f.body)), nil
}

func (f *fakeDocuments) ReadHeader(_ context.Context, _ string, n int64) ([]byte, error) {
	f.headerBytes = n
	if f.headerErr != nil {
		return nil, f.headerErr
	}
	if int64(len(f.body)) < n {
		return f.body, nil
	}
	return f.body[:n], nil
}

func (f *fakeDocuments) Read(context.Context, string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.body, nil
}

var _ storage.Documents = (*fakeDocuments)(nil)

// fakeExecutions records what the starter launched.
type fakeExecutions struct {
	inputs []*sfn.StartExecutionInput
	err    error
}

func (f *fakeExecutions) StartExecution(_ context.Context, in *sfn.StartExecutionInput, _ ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error) {
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	return &sfn.StartExecutionOutput{}, nil
}

var _ Executions = (*fakeExecutions)(nil)

// failingExecutions fails one call out of a batch, so a test can check that
// the others still succeed.
type failingExecutions struct {
	inner  *fakeExecutions
	failOn int
	calls  *int
}

func (f *failingExecutions) StartExecution(ctx context.Context, in *sfn.StartExecutionInput, optFns ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error) {
	*f.calls++
	if *f.calls == f.failOn {
		return nil, errTransient
	}
	return f.inner.StartExecution(ctx, in, optFns...)
}

var _ Executions = (*failingExecutions)(nil)

// errTransient stands in for a throttled dependency.
var errTransient = errors.New("throttled")
