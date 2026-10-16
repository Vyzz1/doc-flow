package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/store"
)

// defaultFailureReason is what a job records when the state machine routes
// it to the failure path without saying why. It should not happen, but a
// FAILED job with no reason is rejected by the model, and losing the status
// change would be worse than a vague message.
const defaultFailureReason = "processing failed"

// Finish is the workflow's last step. It moves the job out of PROCESSING,
// to DONE on the success path and to FAILED on the catch path.
//
// One step rather than two: the two differ by a flag on the payload, and a
// second Lambda would cost another cold start and another state transition
// out of the four thousand the free tier allows each month.
type Finish struct {
	Jobs store.JobStore

	Now func() time.Time
}

// NewFinish returns the step using the production clock.
func NewFinish(jobs store.JobStore) *Finish {
	return &Finish{Jobs: jobs, Now: time.Now}
}

// Handle records the job's terminal status.
//
// A conflict means something else already finished the job: a retried
// catch, or a duplicate execution. The job is in a terminal state either
// way, so the step reports success rather than looping.
func (h *Finish) Handle(ctx context.Context, state State) (State, error) {
	ctx = logging.WithJob(ctx, state.JobID, state.UserID)
	logger := logging.FromContext(ctx)

	if err := state.Validate(); err != nil {
		return state, Permanent(model.StepSave, err)
	}

	if state.Failed {
		reason := state.Reason
		if reason == "" {
			reason = state.Error.Reason()
		}
		switch err := h.Jobs.FailJob(ctx, state.UserID, state.JobID, reason, h.now()); {
		case err == nil:
			logger.InfoContext(ctx, "job failed", slog.String("reason", reason))
		case errors.Is(err, store.ErrStatusConflict), errors.Is(err, store.ErrJobNotFound):
			logger.InfoContext(ctx, "job was already finished", slog.String("reason", reason))
		default:
			return state, Transient(model.StepSave, fmt.Errorf("mark job failed: %w", err))
		}
		return state, nil
	}

	switch err := h.Jobs.UpdateStatus(ctx, state.UserID, state.JobID, model.StatusProcessing, model.StatusDone, h.now()); {
	case err == nil:
		logger.InfoContext(ctx, "job done")
	case errors.Is(err, store.ErrStatusConflict), errors.Is(err, store.ErrJobNotFound):
		logger.InfoContext(ctx, "job was already finished")
	default:
		return state, Transient(model.StepSave, fmt.Errorf("mark job done: %w", err))
	}
	return state, nil
}

func (h *Finish) now() time.Time {
	if h.Now == nil {
		return time.Now()
	}
	return h.Now()
}
