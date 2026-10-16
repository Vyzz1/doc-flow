package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"docflow/internal/model"
	"docflow/internal/store"
)

// newCreateJob fixes the clock and the id source so a test can assert on
// exact values.
func newCreateJob(jobs *fakeStore, uploads *fakeUploads) *CreateJob {
	h := NewCreateJob(jobs, uploads)
	h.NewID = func() (string, error) { return testJobID, nil }
	h.Now = func() time.Time { return testNow }
	return h
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, body)
	}
	return out
}

func TestCreateJob(t *testing.T) {
	jobs, uploads := newFakeStore(), &fakeUploads{}
	h := newCreateJob(jobs, uploads)

	resp, err := h.Handle(context.Background(), request(testUser, `{"docType":"invoice","contentType":"application/pdf"}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", resp.StatusCode, resp.Body)
	}
	if got := resp.Headers["content-type"]; got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}

	body := decode[CreateJobResponse](t, resp.Body)
	if body.JobID != testJobID {
		t.Errorf("jobId = %q, want %q", body.JobID, testJobID)
	}
	if body.Status != string(model.StatusPending) {
		t.Errorf("status = %q, want PENDING", body.Status)
	}
	if body.MaxBytes != model.MaxUploadBytes {
		t.Errorf("maxBytes = %d, want %d", body.MaxBytes, model.MaxUploadBytes)
	}
	if body.UploadFields["policy"] == "" {
		t.Error("response carries no upload policy, so the client cannot post")
	}

	if len(jobs.created) != 1 {
		t.Fatalf("stored %d jobs, want 1", len(jobs.created))
	}
	stored := jobs.created[0]
	if stored.UserID != testUser || stored.JobID != testJobID {
		t.Errorf("stored job = %+v, want it owned by %s", stored, testUser)
	}
	if stored.Status != model.StatusPending {
		t.Errorf("stored status = %q, want PENDING", stored.Status)
	}
	if stored.ContentType != "application/pdf" {
		t.Errorf("stored contentType = %q, want application/pdf", stored.ContentType)
	}

	if uploads.key != model.UploadKey(testUser, testJobID) {
		t.Errorf("presigned key = %q, want the job's upload key", uploads.key)
	}
	if uploads.maxBytes != model.MaxUploadBytes {
		t.Errorf("presigned maxBytes = %d, want %d", uploads.maxBytes, model.MaxUploadBytes)
	}
	if uploads.ttl != uploadTTL {
		t.Errorf("presigned ttl = %s, want %s", uploads.ttl, uploadTTL)
	}
}

func TestCreateJobNormalizesContentType(t *testing.T) {
	jobs, uploads := newFakeStore(), &fakeUploads{}
	h := newCreateJob(jobs, uploads)

	resp, err := h.Handle(context.Background(), request(testUser, `{"docType":"id_document","contentType":"IMAGE/PNG; charset=binary"}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", resp.StatusCode, resp.Body)
	}
	// The signed policy must pin the type S3 will compare against, not the
	// client's spelling of it.
	if uploads.contentType != "image/png" {
		t.Errorf("presigned contentType = %q, want image/png", uploads.contentType)
	}
	if jobs.created[0].ContentType != "image/png" {
		t.Errorf("stored contentType = %q, want image/png", jobs.created[0].ContentType)
	}
}

func TestCreateJobRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name       string
		sub        string
		body       string
		wantStatus int
	}{
		{name: "no token", sub: "", body: `{"docType":"invoice","contentType":"application/pdf"}`, wantStatus: http.StatusUnauthorized},
		{name: "blank subject", sub: "   ", body: `{"docType":"invoice","contentType":"application/pdf"}`, wantStatus: http.StatusUnauthorized},
		{name: "subject with a key separator", sub: "USER#1", body: `{"docType":"invoice","contentType":"application/pdf"}`, wantStatus: http.StatusUnauthorized},
		{name: "not JSON", sub: testUser, body: `not json`, wantStatus: http.StatusBadRequest},
		{name: "empty body", sub: testUser, body: ``, wantStatus: http.StatusBadRequest},
		{name: "unknown doc type", sub: testUser, body: `{"docType":"receipt","contentType":"application/pdf"}`, wantStatus: http.StatusBadRequest},
		{name: "missing doc type", sub: testUser, body: `{"contentType":"application/pdf"}`, wantStatus: http.StatusBadRequest},
		{name: "pdf as an id document", sub: testUser, body: `{"docType":"id_document","contentType":"application/pdf"}`, wantStatus: http.StatusBadRequest},
		{name: "missing content type", sub: testUser, body: `{"docType":"invoice"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, uploads := newFakeStore(), &fakeUploads{}
			h := newCreateJob(jobs, uploads)

			resp, err := h.Handle(context.Background(), request(tt.sub, tt.body))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d\nbody: %s", resp.StatusCode, tt.wantStatus, resp.Body)
			}
			if len(jobs.created) != 0 {
				t.Error("a rejected request still stored a job")
			}
			if uploads.key != "" {
				t.Error("a rejected request still presigned an upload")
			}
			if body := decode[errorBody](t, resp.Body); body.Error == "" {
				t.Error("error response carries no message")
			}
		})
	}
}

func TestCreateJobReportsStoreFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "id collision", err: store.ErrJobExists, wantStatus: http.StatusConflict},
		{name: "throttled", err: errors.New("throttled"), wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore().fail("CreateJob", tt.err)
			uploads := &fakeUploads{}
			h := newCreateJob(jobs, uploads)

			resp, err := h.Handle(context.Background(), request(testUser, `{"docType":"invoice","contentType":"application/pdf"}`))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if uploads.key != "" {
				t.Error("an upload was presigned for a job that was not stored")
			}
		})
	}
}

func TestCreateJobReportsPresignFailure(t *testing.T) {
	jobs := newFakeStore()
	uploads := &fakeUploads{err: errors.New("no credentials")}
	h := newCreateJob(jobs, uploads)

	resp, err := h.Handle(context.Background(), request(testUser, `{"docType":"invoice","contentType":"application/pdf"}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	// The job is already stored and will expire; returning it without an
	// upload would be worse than failing.
	if len(jobs.created) != 1 {
		t.Errorf("stored %d jobs, want the orphan to remain", len(jobs.created))
	}
}

func TestCreateJobReportsIDFailure(t *testing.T) {
	jobs, uploads := newFakeStore(), &fakeUploads{}
	h := newCreateJob(jobs, uploads)
	h.NewID = func() (string, error) { return "", errors.New("no entropy") }

	resp, err := h.Handle(context.Background(), request(testUser, `{"docType":"invoice","contentType":"application/pdf"}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if len(jobs.created) != 0 {
		t.Error("a job was stored without an id")
	}
}

func TestCreateJobResponseKeepsNoSecrets(t *testing.T) {
	jobs, uploads := newFakeStore(), &fakeUploads{err: errors.New("secret-bucket-name: access denied")}
	h := newCreateJob(jobs, uploads)

	resp, err := h.Handle(context.Background(), request(testUser, `{"docType":"invoice","contentType":"application/pdf"}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	// The cause goes to the log, not to the client.
	if strings.Contains(resp.Body, "secret-bucket-name") {
		t.Errorf("response leaked the underlying error: %s", resp.Body)
	}
}
