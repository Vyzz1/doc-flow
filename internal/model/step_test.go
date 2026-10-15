package model

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestStepNameValid(t *testing.T) {
	tests := []struct {
		step StepName
		want bool
	}{
		{StepValidate, true},
		{StepPreprocess, true},
		{StepThumbnail, true},
		{StepOCR, true},
		{StepExtract, true},
		{StepSave, true},
		{StepName("OCR"), false},
		{StepName("classify"), false},
		{StepName(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.step), func(t *testing.T) {
			if got := tt.step.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStepStatusValid(t *testing.T) {
	tests := []struct {
		status StepStatus
		want   bool
	}{
		{StepSucceeded, true},
		{StepFailed, true},
		{StepStatus("succeeded"), false},
		{StepStatus("RUNNING"), false},
		{StepStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewStepResult(t *testing.T) {
	started := testNow
	finished := started.Add(1500 * time.Millisecond)

	got, err := NewStepResult(testJobID, StepThumbnail, started, finished, "thumbs/"+testJobID+".png")
	if err != nil {
		t.Fatalf("NewStepResult() error = %v", err)
	}
	if got.Status != StepSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, StepSucceeded)
	}
	if want := 1500 * time.Millisecond; got.Duration != want {
		t.Errorf("Duration = %s, want %s", got.Duration, want)
	}
	if len(got.Artifacts) != 1 {
		t.Errorf("Artifacts = %v, want one key", got.Artifacts)
	}
	if got.Error != "" || got.Retryable {
		t.Errorf("a succeeded step carries error %q / retryable %v", got.Error, got.Retryable)
	}
}

func TestNewStepResultRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		jobID   string
		step    StepName
		started time.Time
		ended   time.Time
		wantErr error
	}{
		{name: "bad job id", jobID: "nope", step: StepOCR, started: testNow, ended: testNow, wantErr: ErrInvalidJobID},
		{name: "unknown step", jobID: testJobID, step: StepName("classify"), started: testNow, ended: testNow, wantErr: ErrInvalidStepName},
		{name: "negative duration", jobID: testJobID, step: StepOCR, started: testNow, ended: testNow.Add(-time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewStepResult(tt.jobID, tt.step, tt.started, tt.ended)
			if err == nil {
				t.Fatal("NewStepResult() = nil error, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("NewStepResult() error = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, StepResult{}) {
				t.Errorf("NewStepResult() returned %+v on error, want the zero StepResult", got)
			}
		})
	}
}

func TestNewFailedStepResult(t *testing.T) {
	started := testNow
	finished := started.Add(2 * time.Second)

	tests := []struct {
		name      string
		reason    string
		retryable bool
		wantErr   error
	}{
		{name: "permanent failure", reason: "magic bytes do not match a supported format"},
		{name: "transient failure", reason: "DynamoDB throttled the write", retryable: true},
		{name: "missing reason", reason: "", wantErr: ErrMissingReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewFailedStepResult(testJobID, StepValidate, started, finished, tt.reason, tt.retryable)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewFailedStepResult() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if got.Status != StepFailed {
				t.Errorf("Status = %q, want %q", got.Status, StepFailed)
			}
			if got.Error != tt.reason {
				t.Errorf("Error = %q, want %q", got.Error, tt.reason)
			}
			if got.Retryable != tt.retryable {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tt.retryable)
			}
			if want := 2 * time.Second; got.Duration != want {
				t.Errorf("Duration = %s, want %s", got.Duration, want)
			}
		})
	}
}

func TestStepResultValidate(t *testing.T) {
	valid := StepResult{
		JobID:     testJobID,
		Step:      StepExtract,
		Status:    StepSucceeded,
		StartedAt: testNow,
		Duration:  time.Second,
	}

	tests := []struct {
		name    string
		mutate  func(*StepResult)
		wantErr error
	}{
		{name: "succeeded", mutate: func(*StepResult) {}},
		{name: "failed with a reason", mutate: func(r *StepResult) {
			r.Status = StepFailed
			r.Error = "no fields matched"
		}},

		{name: "bad job id", mutate: func(r *StepResult) { r.JobID = "" }, wantErr: ErrInvalidJobID},
		{name: "unknown step", mutate: func(r *StepResult) { r.Step = StepName("ocr2") }, wantErr: ErrInvalidStepName},
		{name: "unknown status", mutate: func(r *StepResult) { r.Status = StepStatus("RUNNING") }, wantErr: ErrInvalidStepStatus},
		{name: "failed without a reason", mutate: func(r *StepResult) { r.Status = StepFailed }, wantErr: ErrMissingReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid
			tt.mutate(&r)
			if err := r.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStepResultValidateRejectsContradictions(t *testing.T) {
	tests := []struct {
		name   string
		result StepResult
	}{
		{
			name:   "succeeded but carries an error",
			result: StepResult{JobID: testJobID, Step: StepOCR, Status: StepSucceeded, Error: "timeout"},
		},
		{
			name:   "negative duration",
			result: StepResult{JobID: testJobID, Step: StepOCR, Status: StepSucceeded, Duration: -time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.result.Validate(); err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}
