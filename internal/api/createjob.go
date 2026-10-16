package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/storage"
	"docflow/internal/store"
)

// uploadTTL is how long the client has to start the upload.
const uploadTTL = 15 * time.Minute

// CreateJobRequest is the body a client posts to create a job.
type CreateJobRequest struct {
	DocType     string `json:"docType"`
	ContentType string `json:"contentType"`
}

// CreateJobResponse tells the client where to put the document.
type CreateJobResponse struct {
	JobID  string `json:"jobId"`
	Status string `json:"status"`

	UploadURL    string            `json:"uploadUrl"`
	UploadFields map[string]string `json:"uploadFields"`
	MaxBytes     int64             `json:"maxBytes"`
	ExpiresAt    time.Time         `json:"expiresAt"`
}

// CreateJob creates jobs and hands out presigned uploads.
type CreateJob struct {
	Jobs    store.JobStore
	Uploads storage.Storage

	// NewID and Now are injected so tests get deterministic jobs.
	NewID func() (string, error)
	Now   func() time.Time
}

// NewCreateJob returns a handler with the production clock and id source.
func NewCreateJob(jobs store.JobStore, uploads storage.Storage) *CreateJob {
	return &CreateJob{
		Jobs:    jobs,
		Uploads: uploads,
		NewID:   model.NewJobIDNow,
		Now:     time.Now,
	}
}

// Handle creates a PENDING job and returns the upload for it.
func (h *CreateJob) Handle(ctx context.Context, r Request) (Response, error) {
	userID, err := UserID(r)
	if err != nil {
		return Fail(ctx, http.StatusUnauthorized, "unauthenticated", err)
	}
	ctx = logging.WithUserID(ctx, userID)

	var body CreateJobRequest
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		return Fail(ctx, http.StatusBadRequest, "request body is not valid JSON", err)
	}

	docType := model.DocType(body.DocType)
	if !docType.Valid() {
		return Fail(ctx, http.StatusBadRequest, "unsupported document type", nil)
	}
	// Checked here rather than at upload time so the client learns about a
	// bad content type before it sends ten megabytes.
	if err := docType.ValidateContentType(body.ContentType); err != nil {
		return Fail(ctx, http.StatusBadRequest, "unsupported content type for this document type", err)
	}
	contentType := model.NormalizeContentType(body.ContentType)

	jobID, err := h.NewID()
	if err != nil {
		return Fail(ctx, http.StatusInternalServerError, "could not create a job", err)
	}
	ctx = logging.WithJobID(ctx, jobID)

	job, err := model.NewJob(userID, jobID, docType, h.Now())
	if err != nil {
		return Fail(ctx, StatusFor(err), "could not create a job", err)
	}
	job.ContentType = contentType

	// The job is stored before the upload is signed. The other order risks
	// handing out a URL for a job that was never recorded, which would make
	// the S3 event arrive for an unknown job; this order risks an orphan
	// PENDING job, which its TTL removes.
	if err := h.Jobs.CreateJob(ctx, job); err != nil {
		if errors.Is(err, store.ErrJobExists) {
			// Two ULIDs collided, or a retry reused an id. Either way the
			// client should ask again rather than receive someone's job.
			return Fail(ctx, http.StatusConflict, "job id already in use, retry", err)
		}
		return Fail(ctx, StatusFor(err), "could not create a job", err)
	}

	upload, err := h.Uploads.PresignUpload(ctx, job.UploadKey, contentType, model.MaxUploadBytes, uploadTTL)
	if err != nil {
		// The job stays PENDING and expires. Returning an error here is
		// better than returning a job the client cannot upload to.
		return Fail(ctx, http.StatusInternalServerError, "could not prepare the upload", err)
	}

	logging.FromContext(ctx).InfoContext(ctx, "job created",
		slog.String("docType", string(docType)),
		slog.String("contentType", contentType),
	)

	return JSON(http.StatusCreated, CreateJobResponse{
		JobID:        job.JobID,
		Status:       string(job.Status),
		UploadURL:    upload.URL,
		UploadFields: upload.Fields,
		MaxBytes:     upload.MaxBytes,
		ExpiresAt:    upload.ExpiresAt,
	})
}
