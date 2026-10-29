package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/storage"
	"docflow/internal/store"
	"docflow/internal/validation"
)

// Validate is the workflow's first step: it decides whether the uploaded
// object is a document the rest of the pipeline can read.
type Validate struct {
	Jobs      store.JobStore
	Documents storage.Documents

	Now func() time.Time
}

// NewValidate returns the step using the production clock.
func NewValidate(jobs store.JobStore, documents storage.Documents) *Validate {
	return &Validate{Jobs: jobs, Documents: documents, Now: time.Now}
}

// Handle checks the stored object and records the outcome.
//
// It reads the object's length and its first few bytes, never the whole
// file: a ranged read is enough to recognize a format, and the rest would
// cost transfer and memory for nothing.
func (h *Validate) Handle(ctx context.Context, state State) (State, error) {
	ctx = logging.WithJob(ctx, state.JobID, state.UserID)
	started := h.now()

	if err := state.Validate(); err != nil {
		return state, Permanent(model.StepValidate, err)
	}

	job, err := h.Jobs.GetJob(ctx, state.UserID, state.JobID)
	if err != nil {
		if errors.Is(err, store.ErrJobNotFound) {
			return state, Permanent(model.StepValidate, err)
		}
		return state, Transient(model.StepValidate, fmt.Errorf("read job: %w", err))
	}

	size, err := h.Documents.Size(ctx, state.UploadKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// The client never uploaded, or the object has already expired.
			// Neither improves with a retry.
			return state, Permanent(model.StepValidate, fmt.Errorf("no object was uploaded: %w", err))
		}
		return state, Transient(model.StepValidate, fmt.Errorf("read object size: %w", err))
	}

	header, err := h.Documents.ReadHeader(ctx, state.UploadKey, validation.HeaderSize)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return state, Permanent(model.StepValidate, fmt.Errorf("no object was uploaded: %w", err))
		}
		return state, Transient(model.StepValidate, fmt.Errorf("read object header: %w", err))
	}

	kind, err := validation.Validate(job, header, size)
	if err != nil {
		h.record(ctx, state, started, err)
		// validation only ever reports permanent failures; treating one as
		// transient would retry a file that cannot become valid.
		return state, Permanent(model.StepValidate, err)
	}

	state.ContentType = string(kind)
	state.SizeBytes = size

	h.record(ctx, state, started, nil)
	logging.FromContext(ctx).InfoContext(ctx, "document accepted",
		slog.String("contentType", state.ContentType),
		slog.Int64("sizeBytes", size),
	)
	return state, nil
}

// record stores the step's outcome. A failure to record is logged rather
// than returned: the step's own verdict matters more than the audit trail,
// and losing the trail must not turn a valid document into a failed job.
func (h *Validate) record(ctx context.Context, state State, started time.Time, cause error) {
	var (
		result model.StepResult
		err    error
	)
	if cause == nil {
		result, err = model.NewStepResult(state.JobID, model.StepValidate, started, h.now())
	} else {
		result, err = model.NewFailedStepResult(state.JobID, model.StepValidate, started, h.now(),
			UserMessage(Permanent(model.StepValidate, cause)), false)
	}
	if err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "cannot build the step result",
			slog.String("error", err.Error()))
		return
	}
	if err := h.Jobs.PutStepResult(ctx, result); err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "cannot record the step result",
			slog.String("error", err.Error()))
	}
}

func (h *Validate) now() time.Time {
	if h.Now == nil {
		return time.Now()
	}
	return h.Now()
}
