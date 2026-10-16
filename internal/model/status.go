package model

import (
	"errors"
	"fmt"
)

// JobStatus is the lifecycle state of a document-processing job.
type JobStatus string

// The only statuses a job may hold.
const (
	StatusPending    JobStatus = "PENDING"
	StatusProcessing JobStatus = "PROCESSING"
	StatusDone       JobStatus = "DONE"
	StatusFailed     JobStatus = "FAILED"
)

// Errors reported when a status or a status change is rejected. Callers match
// them with errors.Is.
var (
	ErrInvalidStatus     = errors.New("unknown job status")
	ErrInvalidTransition = errors.New("job status transition not allowed")
)

// allowedTransitions maps each status to the statuses it may move to. Jobs
// only move forward:
//
//	PENDING ---> PROCESSING ---> DONE
//	   |               \-------> FAILED
//	   \-----------------------> FAILED
//
// A job fails straight from PENDING when the starter rejects the upload
// before any work begins, which saves a write that exists only to satisfy the
// state machine. See docs/adr/0001-job-status-transitions.md.
//
// A job nobody ever uploads for stays PENDING until its TTL removes it;
// nothing fails it.
var allowedTransitions = map[JobStatus][]JobStatus{
	StatusPending:    {StatusProcessing, StatusFailed},
	StatusProcessing: {StatusDone, StatusFailed},
	StatusDone:       nil,
	StatusFailed:     nil,
}

// FailableFrom lists the statuses a job may move to FAILED from. The store
// builds its conditional write from this, so the rule lives in one place.
func FailableFrom() []JobStatus {
	return []JobStatus{StatusPending, StatusProcessing}
}

// Valid reports whether s is one of the known statuses.
func (s JobStatus) Valid() bool {
	_, ok := allowedTransitions[s]
	return ok
}

// IsTerminal reports whether s allows no further transition.
func (s JobStatus) IsTerminal() bool {
	return len(allowedTransitions[s]) == 0
}

// CanTransitionTo reports whether a job may move from s to next.
func (s JobStatus) CanTransitionTo(next JobStatus) bool {
	for _, allowed := range allowedTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// ValidateTransition returns nil when a job may move from s to next. It is a
// guard in front of the DynamoDB conditional write that performs the move, not
// a replacement for it: concurrent writers still race, and only the condition
// keeps the table consistent.
func (s JobStatus) ValidateTransition(next JobStatus) error {
	if !s.Valid() {
		return fmt.Errorf("transition from %q: %w", s, ErrInvalidStatus)
	}
	if !next.Valid() {
		return fmt.Errorf("transition to %q: %w", next, ErrInvalidStatus)
	}
	if !s.CanTransitionTo(next) {
		return fmt.Errorf("transition %s -> %s: %w", s, next, ErrInvalidTransition)
	}
	return nil
}

// String makes JobStatus print as its bare value in log fields and errors.
func (s JobStatus) String() string { return string(s) }
