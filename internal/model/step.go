package model

import (
	"errors"
	"fmt"
	"time"
)

// StepName identifies a state in the processing workflow. The names double as
// the SK suffix of the per-step DynamoDB item (JOB#<id> / STEP#<name>).
type StepName string

// The workflow steps, in the order the state machine runs them. Preprocess and
// Thumbnail run in parallel.
const (
	StepValidate   StepName = "validate"
	StepPreprocess StepName = "preprocess"
	StepThumbnail  StepName = "thumbnail"
	StepOCR        StepName = "ocr"
	StepExtract    StepName = "extract"
	StepSave       StepName = "save"
)

// StepStatus is the outcome of a single step.
type StepStatus string

// The outcomes a finished step may report.
const (
	StepSucceeded StepStatus = "SUCCEEDED"
	StepFailed    StepStatus = "FAILED"
)

// Errors reported when a step result is rejected.
var (
	ErrInvalidStepName   = errors.New("unknown workflow step")
	ErrInvalidStepStatus = errors.New("unknown step status")
)

// Valid reports whether n is one of the known workflow steps.
func (n StepName) Valid() bool {
	switch n {
	case StepValidate, StepPreprocess, StepThumbnail, StepOCR, StepExtract, StepSave:
		return true
	default:
		return false
	}
}

// String makes StepName print as its bare value in log fields and errors.
func (n StepName) String() string { return string(n) }

// Valid reports whether s is one of the known step outcomes.
func (s StepStatus) Valid() bool {
	return s == StepSucceeded || s == StepFailed
}

// String makes StepStatus print as its bare value in log fields and errors.
func (s StepStatus) String() string { return string(s) }

// StepResult records how one workflow step went for one job. It is written once
// per step and read back by the get-job API to report progress.
type StepResult struct {
	JobID  string
	Step   StepName
	Status StepStatus

	StartedAt time.Time
	Duration  time.Duration

	// Artifacts are the S3 keys the step produced, such as a normalized page or
	// a thumbnail. Contents stay in S3.
	Artifacts []string
	// Error is a short, user-safe explanation set only on StepFailed.
	Error string
	// Retryable distinguishes a transient failure, which the state machine
	// retries with backoff, from a permanent one, which fails fast.
	Retryable bool
}

// NewStepResult returns a succeeded result for step, covering the window from
// startedAt to finishedAt.
func NewStepResult(jobID string, step StepName, startedAt, finishedAt time.Time, artifacts ...string) (StepResult, error) {
	r := StepResult{
		JobID:     jobID,
		Step:      step,
		Status:    StepSucceeded,
		StartedAt: startedAt.UTC(),
		Duration:  finishedAt.Sub(startedAt),
		Artifacts: artifacts,
	}
	if err := r.Validate(); err != nil {
		return StepResult{}, err
	}
	return r, nil
}

// NewFailedStepResult returns a failed result for step. Set retryable for
// transient failures such as a throttled dependency.
func NewFailedStepResult(jobID string, step StepName, startedAt, finishedAt time.Time, reason string, retryable bool) (StepResult, error) {
	r := StepResult{
		JobID:     jobID,
		Step:      step,
		Status:    StepFailed,
		StartedAt: startedAt.UTC(),
		Duration:  finishedAt.Sub(startedAt),
		Error:     reason,
		Retryable: retryable,
	}
	if err := r.Validate(); err != nil {
		return StepResult{}, err
	}
	return r, nil
}

// Validate reports whether r is internally consistent.
func (r StepResult) Validate() error {
	if err := validateJobID(r.JobID); err != nil {
		return err
	}
	if !r.Step.Valid() {
		return fmt.Errorf("step %q: %w", r.Step, ErrInvalidStepName)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("step %s status %q: %w", r.Step, r.Status, ErrInvalidStepStatus)
	}
	if r.Duration < 0 {
		return fmt.Errorf("step %s duration %s: negative", r.Step, r.Duration)
	}
	if r.Status == StepFailed && r.Error == "" {
		return fmt.Errorf("step %s failed: %w", r.Step, ErrMissingReason)
	}
	if r.Status == StepSucceeded && r.Error != "" {
		return fmt.Errorf("step %s succeeded but carries error %q", r.Step, r.Error)
	}
	return nil
}
