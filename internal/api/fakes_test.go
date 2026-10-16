package api

import (
	"context"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"docflow/internal/model"
	"docflow/internal/storage"
	"docflow/internal/store"
)

const (
	testUser  = "user-1"
	testJobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TV"
)

var testNow = time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

// fakeStore is a JobStore whose every method can be told what to return.
type fakeStore struct {
	created  []model.Job
	job      model.Job
	steps    []model.StepResult
	result   model.DocumentResult
	failures map[string]error
}

func newFakeStore() *fakeStore {
	return &fakeStore{failures: map[string]error{}}
}

func (f *fakeStore) fail(method string, err error) *fakeStore {
	f.failures[method] = err
	return f
}

func (f *fakeStore) CreateJob(_ context.Context, job model.Job) error {
	if err := f.failures["CreateJob"]; err != nil {
		return err
	}
	f.created = append(f.created, job)
	return nil
}

func (f *fakeStore) GetJob(_ context.Context, _, _ string) (model.Job, error) {
	if err := f.failures["GetJob"]; err != nil {
		return model.Job{}, err
	}
	return f.job, nil
}

func (f *fakeStore) ListJobs(context.Context, string, int32, string) ([]model.Job, string, error) {
	return nil, "", f.failures["ListJobs"]
}

func (f *fakeStore) UpdateStatus(context.Context, string, string, model.JobStatus, model.JobStatus, time.Time) error {
	return f.failures["UpdateStatus"]
}

func (f *fakeStore) FailJob(context.Context, string, string, string, time.Time) error {
	return f.failures["FailJob"]
}

func (f *fakeStore) PutStepResult(context.Context, model.StepResult) error {
	return f.failures["PutStepResult"]
}

func (f *fakeStore) ListStepResults(context.Context, string) ([]model.StepResult, error) {
	if err := f.failures["ListStepResults"]; err != nil {
		return nil, err
	}
	return f.steps, nil
}

func (f *fakeStore) PutResult(context.Context, model.DocumentResult) error {
	return f.failures["PutResult"]
}

func (f *fakeStore) GetResult(context.Context, string) (model.DocumentResult, error) {
	if err := f.failures["GetResult"]; err != nil {
		return model.DocumentResult{}, err
	}
	return f.result, nil
}

var _ store.JobStore = (*fakeStore)(nil)

// fakeUploads records what was presigned and replays a canned upload.
type fakeUploads struct {
	key         string
	contentType string
	maxBytes    int64
	ttl         time.Duration

	err error
}

func (f *fakeUploads) PresignUpload(_ context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (storage.Upload, error) {
	f.key, f.contentType, f.maxBytes, f.ttl = key, contentType, maxBytes, ttl
	if f.err != nil {
		return storage.Upload{}, f.err
	}
	return storage.Upload{
		URL:       "https://bucket.s3.amazonaws.com",
		Fields:    map[string]string{"key": key, "policy": "signed"},
		Key:       key,
		MaxBytes:  maxBytes,
		ExpiresAt: testNow.Add(ttl),
	}, nil
}

var _ storage.Storage = (*fakeUploads)(nil)

// request builds an authenticated request carrying body.
func request(sub, body string) Request {
	r := Request{Body: body}
	if sub != "" {
		r.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
			JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{
				Claims: map[string]string{"sub": sub},
			},
		}
	}
	return r
}

// getRequest builds an authenticated GET /jobs/{id} request.
func getRequest(sub, jobID string) Request {
	r := request(sub, "")
	r.PathParameters = map[string]string{"id": jobID}
	return r
}
