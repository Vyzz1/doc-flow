// Package store persists jobs, step results, and extracted data in the
// pipeline's single DynamoDB table.
//
// The key layout is the one AGENTS.md fixes, and changing it needs an ADR:
//
//	USER#<id>  JOB#<ulid>    the job itself
//	JOB#<id>   STEP#<name>   one workflow step's outcome
//	JOB#<id>   RESULT        recognized text and extracted fields
//
// Quota items (USER#<id> / QUOTA#<date>) share the table but belong to
// internal/quota, which owns their atomic counter.
//
// Nothing here scans. Listing a user's jobs is a Query on their partition, so
// one user's volume never affects another's read cost.
package store

import (
	"context"
	"errors"
	"time"

	"docflow/internal/model"
)

// Errors the store reports. Callers match them with errors.Is.
var (
	// ErrJobExists means a job with that key is already stored. A caller
	// retrying after an SQS redelivery should treat it as success.
	ErrJobExists = errors.New("job already exists")

	// ErrJobNotFound means no job matched the user and job id.
	ErrJobNotFound = errors.New("job not found")

	// ErrStatusConflict means the job existed but was not in the status the
	// write required. A redelivered message that tries to start an already
	// running job gets this, and should treat it as success.
	ErrStatusConflict = errors.New("job is not in the expected status")

	// ErrResultNotFound means the job has no stored result yet.
	ErrResultNotFound = errors.New("job result not found")
)

// JobStore is the persistence the pipeline depends on. Handlers take this
// rather than the concrete store so tests can fake it.
type JobStore interface {
	// CreateJob stores a new PENDING job. It fails with ErrJobExists rather
	// than overwriting, which is what makes repeated delivery safe.
	CreateJob(ctx context.Context, job model.Job) error

	// GetJob reads one job. The read is eventually consistent: polling a job
	// status does not justify twice the read cost.
	GetJob(ctx context.Context, userID, jobID string) (model.Job, error)

	// ListJobs returns a user's jobs, newest first, at most limit of them.
	// Pass the returned token back to continue; an empty token means the end.
	ListJobs(ctx context.Context, userID string, limit int32, token string) ([]model.Job, string, error)

	// UpdateStatus moves a job from one status to another as a conditional
	// write, so two workers racing cannot both win. Use FailJob for FAILED,
	// which also records the reason.
	UpdateStatus(ctx context.Context, userID, jobID string, from, to model.JobStatus, now time.Time) error

	// FailJob moves a PROCESSING job to FAILED with a short, user-safe reason.
	FailJob(ctx context.Context, userID, jobID, reason string, now time.Time) error

	// PutStepResult records one step's outcome, overwriting any earlier
	// attempt at the same step so a retried step does not accumulate items.
	PutStepResult(ctx context.Context, result model.StepResult) error

	// ListStepResults returns every recorded step for a job, in step-name
	// order.
	ListStepResults(ctx context.Context, jobID string) ([]model.StepResult, error)

	// PutResult stores a job's recognized text and extracted fields.
	PutResult(ctx context.Context, result model.DocumentResult) error

	// GetResult reads a job's stored result, or ErrResultNotFound.
	GetResult(ctx context.Context, jobID string) (model.DocumentResult, error)
}
