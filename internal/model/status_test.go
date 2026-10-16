package model

import (
	"errors"
	"testing"
)

func TestJobStatusValid(t *testing.T) {
	tests := []struct {
		status JobStatus
		want   bool
	}{
		{StatusPending, true},
		{StatusProcessing, true},
		{StatusDone, true},
		{StatusFailed, true},
		{JobStatus("pending"), false},
		{JobStatus("CANCELLED"), false},
		{JobStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJobStatusIsTerminal(t *testing.T) {
	tests := []struct {
		status JobStatus
		want   bool
	}{
		{StatusPending, false},
		{StatusProcessing, false},
		{StatusDone, true},
		{StatusFailed, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.want {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJobStatusValidateTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    JobStatus
		to      JobStatus
		wantErr error
	}{
		{name: "start processing", from: StatusPending, to: StatusProcessing},
		{name: "finish", from: StatusProcessing, to: StatusDone},
		{name: "fail while processing", from: StatusProcessing, to: StatusFailed},
		{name: "fail before processing", from: StatusPending, to: StatusFailed},

		{name: "skip processing", from: StatusPending, to: StatusDone, wantErr: ErrInvalidTransition},
		{name: "backwards", from: StatusProcessing, to: StatusPending, wantErr: ErrInvalidTransition},
		{name: "revive done", from: StatusDone, to: StatusProcessing, wantErr: ErrInvalidTransition},
		{name: "retry failed", from: StatusFailed, to: StatusProcessing, wantErr: ErrInvalidTransition},
		{name: "self", from: StatusProcessing, to: StatusProcessing, wantErr: ErrInvalidTransition},

		{name: "unknown source", from: JobStatus("QUEUED"), to: StatusProcessing, wantErr: ErrInvalidStatus},
		{name: "unknown target", from: StatusProcessing, to: JobStatus("CANCELLED"), wantErr: ErrInvalidStatus},
		{name: "empty target", from: StatusPending, to: JobStatus(""), wantErr: ErrInvalidStatus},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.from.ValidateTransition(tt.to)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateTransition(%q -> %q) = %v, want %v", tt.from, tt.to, err, tt.wantErr)
			}
			if got := tt.from.CanTransitionTo(tt.to); got != (tt.wantErr == nil) {
				t.Errorf("CanTransitionTo(%q) = %v, want %v", tt.to, got, tt.wantErr == nil)
			}
		})
	}
}

func TestFailableFrom(t *testing.T) {
	// The store builds its conditional write from this list, so every status
	// in it must really allow the move, and every status that allows the move
	// must be in it.
	failable := map[JobStatus]bool{}
	for _, s := range FailableFrom() {
		failable[s] = true
	}
	for _, status := range []JobStatus{StatusPending, StatusProcessing, StatusDone, StatusFailed} {
		if got, want := failable[status], status.CanTransitionTo(StatusFailed); got != want {
			t.Errorf("FailableFrom contains %s = %v, but CanTransitionTo(FAILED) = %v", status, got, want)
		}
	}
}
