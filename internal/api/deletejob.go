package api

import (
	"context"
	"errors"
	"net/http"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/storage"
	"docflow/internal/store"
)

// DeleteJob removes one of the caller's jobs and what it left behind.
type DeleteJob struct {
	Jobs    store.JobStore
	Objects storage.Remover
}

// NewDeleteJob returns a handler deleting from jobs and objects.
func NewDeleteJob(jobs store.JobStore, objects storage.Remover) *DeleteJob {
	return &DeleteJob{Jobs: jobs, Objects: objects}
}

// Handle deletes the job, then its objects.
//
// Ownership needs no separate check, as in GetJob: the delete is keyed on the
// caller's own partition, so another user's id finds nothing.
//
// The job's records go first because that write is also the guard against a
// job still being processed. If the objects then cannot be removed, the job is
// already gone from the API and the bucket's expiry rule collects the files;
// the caller is told, so the failure is not silent.
func (h *DeleteJob) Handle(ctx context.Context, r Request) (Response, error) {
	userID, err := UserID(r)
	if err != nil {
		return Fail(ctx, http.StatusUnauthorized, "unauthenticated", err)
	}
	ctx = logging.WithUserID(ctx, userID)

	jobID := r.PathParameters["id"]
	if !model.ValidJobID(jobID) {
		return Fail(ctx, http.StatusBadRequest, "job id is not a ULID", nil)
	}
	ctx = logging.WithJobID(ctx, jobID)

	if err := h.Jobs.DeleteJob(ctx, userID, jobID); err != nil {
		switch {
		case errors.Is(err, store.ErrJobNotFound):
			return Fail(ctx, http.StatusNotFound, "job not found", err)
		case errors.Is(err, store.ErrStatusConflict):
			return Fail(ctx, http.StatusConflict, "the job is still being processed", err)
		default:
			return Fail(ctx, StatusFor(err), "could not delete the job", err)
		}
	}

	// Both are attempted even if the first fails, so one error does not leave
	// the other's files behind.
	removed := errors.Join(
		h.Objects.Delete(ctx, model.UploadKey(userID, jobID)),
		h.Objects.DeletePrefix(ctx, model.DerivedPrefix(userID, jobID)),
	)
	if removed != nil {
		return Fail(ctx, http.StatusInternalServerError,
			"the job was deleted, but its files could not be removed; they expire on their own", removed)
	}

	return Response{StatusCode: http.StatusNoContent}, nil
}
