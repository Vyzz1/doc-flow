package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/store"
)

func storedJob(t *testing.T, status model.JobStatus) model.Job {
	t.Helper()
	job, err := model.NewJob(testUser, testJobID, model.DocInvoice, testNow)
	if err != nil {
		t.Fatalf("NewJob() error = %v", err)
	}
	job.Status = status
	if status == model.StatusFailed {
		job.FailureReason = "unsupported file type"
	}
	return job
}

func TestGetJob(t *testing.T) {
	jobs := newFakeStore()
	jobs.job = storedJob(t, model.StatusProcessing)
	validate, err := model.NewStepResult(testJobID, model.StepValidate, testNow, testNow.Add(300*time.Millisecond))
	if err != nil {
		t.Fatalf("NewStepResult() error = %v", err)
	}
	jobs.steps = []model.StepResult{validate}

	resp, err := NewGetJob(jobs).Handle(context.Background(), getRequest(testUser, testJobID))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", resp.StatusCode, resp.Body)
	}

	body := decode[GetJobResponse](t, resp.Body)
	if body.JobID != testJobID || body.Status != string(model.StatusProcessing) {
		t.Errorf("body = %+v, want job %s PROCESSING", body, testJobID)
	}
	if len(body.Steps) != 1 {
		t.Fatalf("steps = %v, want one", body.Steps)
	}
	if body.Steps[0].Step != string(model.StepValidate) || body.Steps[0].DurationMS != 300 {
		t.Errorf("step = %+v, want validate taking 300ms", body.Steps[0])
	}
	// A job still running has no result to report.
	if body.Result != nil {
		t.Errorf("result = %+v for a PROCESSING job, want none", body.Result)
	}
}

func TestGetJobIncludesResultOnlyWhenDone(t *testing.T) {
	result := model.DocumentResult{
		JobID:      testJobID,
		Provider:   model.ProviderTesseract,
		Confidence: 0.92,
		Pages:      1,
		Text:       "INVOICE",
		Fields:     []model.Field{{Name: "total", Value: "1250000", Confidence: 0.88}},
	}

	tests := []struct {
		name       string
		status     model.JobStatus
		wantResult bool
	}{
		{name: "pending", status: model.StatusPending},
		{name: "processing", status: model.StatusProcessing},
		{name: "failed", status: model.StatusFailed},
		{name: "done", status: model.StatusDone, wantResult: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore()
			jobs.job = storedJob(t, tt.status)
			jobs.result = result

			resp, err := NewGetJob(jobs).Handle(context.Background(), getRequest(testUser, testJobID))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			body := decode[GetJobResponse](t, resp.Body)

			if tt.wantResult {
				if body.Result == nil {
					t.Fatal("a DONE job reported no result")
				}
				if body.Result.Text != "INVOICE" || len(body.Result.Fields) != 1 {
					t.Errorf("result = %+v, want the stored one", body.Result)
				}
			} else if body.Result != nil {
				t.Errorf("a %s job reported a result: %+v", tt.status, body.Result)
			}
		})
	}
}

func TestGetJobReportsFailureReason(t *testing.T) {
	jobs := newFakeStore()
	jobs.job = storedJob(t, model.StatusFailed)

	resp, err := NewGetJob(jobs).Handle(context.Background(), getRequest(testUser, testJobID))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	body := decode[GetJobResponse](t, resp.Body)
	if body.FailureReason != "unsupported file type" {
		t.Errorf("failureReason = %q, want the stored reason", body.FailureReason)
	}
}

func TestGetJobRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name       string
		sub        string
		jobID      string
		wantStatus int
	}{
		{name: "no token", sub: "", jobID: testJobID, wantStatus: http.StatusUnauthorized},
		{name: "not a ULID", sub: testUser, jobID: "../../etc/passwd", wantStatus: http.StatusBadRequest},
		{name: "empty id", sub: testUser, jobID: "", wantStatus: http.StatusBadRequest},
		{name: "lowercase ULID", sub: testUser, jobID: strings.ToLower(testJobID), wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore()
			jobs.job = storedJob(t, model.StatusDone)

			resp, err := NewGetJob(jobs).Handle(context.Background(), getRequest(tt.sub, tt.jobID))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d\nbody: %s", resp.StatusCode, tt.wantStatus, resp.Body)
			}
		})
	}
}

func TestGetJobNotFound(t *testing.T) {
	jobs := newFakeStore().fail("GetJob", store.ErrJobNotFound)

	resp, err := NewGetJob(jobs).Handle(context.Background(), getRequest(testUser, testJobID))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetJobReportsReadFailures(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "job read", method: "GetJob"},
		{name: "step read", method: "ListStepResults"},
		{name: "result read", method: "GetResult"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore().fail(tt.method, errors.New("throttled"))
			jobs.job = storedJob(t, model.StatusDone)

			resp, err := NewGetJob(jobs).Handle(context.Background(), getRequest(testUser, testJobID))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", resp.StatusCode)
			}
			if strings.Contains(resp.Body, "throttled") {
				t.Errorf("response leaked the underlying error: %s", resp.Body)
			}
		})
	}
}

// A DONE job whose result is missing means the save step and the status
// write disagree. The status is still true, so report the job and leave the
// gap in the logs rather than turning it into a 404.
func TestGetJobWithDoneButNoResult(t *testing.T) {
	jobs := newFakeStore().fail("GetResult", store.ErrResultNotFound)
	jobs.job = storedJob(t, model.StatusDone)

	var logged bytes.Buffer
	ctx := logging.Into(context.Background(), logging.New(&logged, slog.LevelInfo))

	resp, err := NewGetJob(jobs).Handle(ctx, getRequest(testUser, testJobID))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", resp.StatusCode, resp.Body)
	}
	body := decode[GetJobResponse](t, resp.Body)
	if body.Status != string(model.StatusDone) || body.Result != nil {
		t.Errorf("body = %+v, want a DONE job with no result", body)
	}
	if !strings.Contains(logged.String(), "no stored result") {
		t.Errorf("the gap was not logged: %q", logged.String())
	}
}

func TestGetJobLogsCarryIdentifiers(t *testing.T) {
	jobs := newFakeStore().fail("GetJob", errors.New("throttled"))

	var logged bytes.Buffer
	ctx := logging.Into(context.Background(), logging.New(&logged, slog.LevelInfo))

	if _, err := NewGetJob(jobs).Handle(ctx, getRequest(testUser, testJobID)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	for _, want := range []string{testUser, testJobID} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log line does not carry %q: %s", want, logged.String())
		}
	}
}
