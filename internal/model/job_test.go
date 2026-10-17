package model

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// testJobID is a canonical ULID used across the model tests.
const testJobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TV"

var testNow = time.Date(2026, 10, 15, 8, 30, 0, 0, time.UTC)

func TestValidJobID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "canonical ulid", id: testJobID, want: true},
		{name: "all zeroes", id: "00000000000000000000000000", want: true},
		{name: "max timestamp", id: "7ZZZZZZZZZZZZZZZZZZZZZZZZZ", want: true},

		{name: "empty", id: "", want: false},
		{name: "too short", id: "01J8ZQJ9X7K3M4N5P6Q7R8S9T", want: false},
		{name: "too long", id: testJobID + "V", want: false},
		{name: "lowercase", id: "01j8zqj9x7k3m4n5p6q7r8s9tv", want: false},
		{name: "timestamp overflow", id: "8ZZZZZZZZZZZZZZZZZZZZZZZZZ", want: false},
		{name: "excluded letter I", id: "01J8ZQJ9X7K3M4N5P6Q7R8S9TI", want: false},
		{name: "excluded letter U", id: "01J8ZQJ9X7K3M4N5P6Q7R8S9TU", want: false},
		{name: "hyphen", id: "01J8ZQJ9X7K3M4N5P6Q7R8S9T-", want: false},
		{name: "uuid", id: "0f8fad5b-d9cb-469f-a165-70867728950e", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidJobID(tt.id); got != tt.want {
				t.Errorf("ValidJobID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestNewJob(t *testing.T) {
	tests := []struct {
		name    string
		userID  string
		jobID   string
		docType DocType
		wantErr error
	}{
		{name: "invoice", userID: "user-1", jobID: testJobID, docType: DocInvoice},
		{name: "id document", userID: "user-1", jobID: testJobID, docType: DocID},
		{name: "image", userID: "user-1", jobID: testJobID, docType: DocImage},

		{name: "empty user", userID: "", jobID: testJobID, docType: DocInvoice, wantErr: ErrInvalidUserID},
		{name: "user with key separator", userID: "USER#1", jobID: testJobID, docType: DocInvoice, wantErr: ErrInvalidUserID},
		{name: "user with slash", userID: "a/b", jobID: testJobID, docType: DocInvoice, wantErr: ErrInvalidUserID},
		{name: "oversized user", userID: strings.Repeat("u", maxIDLen+1), jobID: testJobID, docType: DocInvoice, wantErr: ErrInvalidUserID},
		{name: "bad job id", userID: "user-1", jobID: "not-a-ulid", docType: DocInvoice, wantErr: ErrInvalidJobID},
		{name: "unknown doc type", userID: "user-1", jobID: testJobID, docType: DocType("spreadsheet"), wantErr: ErrInvalidDocType},
		{name: "empty doc type", userID: "user-1", jobID: testJobID, docType: DocType(""), wantErr: ErrInvalidDocType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := NewJob(tt.userID, tt.jobID, tt.docType, testNow)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewJob() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if job != (Job{}) {
					t.Errorf("NewJob() returned %+v on error, want the zero Job", job)
				}
				return
			}
			if job.Status != StatusPending {
				t.Errorf("Status = %q, want %q", job.Status, StatusPending)
			}
			if want := UploadKey(tt.userID, tt.jobID); job.UploadKey != want {
				t.Errorf("UploadKey = %q, want %q", job.UploadKey, want)
			}
			if !job.CreatedAt.Equal(testNow) || !job.UpdatedAt.Equal(testNow) {
				t.Errorf("CreatedAt/UpdatedAt = %v/%v, want %v", job.CreatedAt, job.UpdatedAt, testNow)
			}
			if want := testNow.Add(Retention); !job.ExpiresAt.Equal(want) {
				t.Errorf("ExpiresAt = %v, want %v", job.ExpiresAt, want)
			}
			if err := job.Validate(); err != nil {
				t.Errorf("Validate() on a new job = %v, want nil", err)
			}
		})
	}
}

func TestNewJobNormalizesTimeToUTC(t *testing.T) {
	zone := time.FixedZone("ICT", 7*60*60)
	job, err := NewJob("user-1", testJobID, DocInvoice, testNow.In(zone))
	if err != nil {
		t.Fatalf("NewJob() error = %v", err)
	}
	if loc := job.CreatedAt.Location(); loc != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", loc)
	}
	if !job.CreatedAt.Equal(testNow) {
		t.Errorf("CreatedAt = %v, want the same instant as %v", job.CreatedAt, testNow)
	}
}

func TestJobValidate(t *testing.T) {
	newValidJob := func(t *testing.T) Job {
		t.Helper()
		job, err := NewJob("user-1", testJobID, DocInvoice, testNow)
		if err != nil {
			t.Fatalf("NewJob() error = %v", err)
		}
		return job
	}

	tests := []struct {
		name    string
		mutate  func(*Job)
		wantErr error
	}{
		{name: "new job", mutate: func(*Job) {}},
		{name: "failed with a reason", mutate: func(j *Job) {
			j.Status = StatusFailed
			j.FailureReason = "unsupported file type"
		}},

		{name: "empty user", mutate: func(j *Job) { j.UserID = "" }, wantErr: ErrInvalidUserID},
		{name: "bad job id", mutate: func(j *Job) { j.JobID = "x" }, wantErr: ErrInvalidJobID},
		{name: "unknown status", mutate: func(j *Job) { j.Status = JobStatus("QUEUED") }, wantErr: ErrInvalidStatus},
		{name: "unknown doc type", mutate: func(j *Job) { j.DocType = DocType("zip") }, wantErr: ErrInvalidDocType},
		{name: "failed without a reason", mutate: func(j *Job) { j.Status = StatusFailed }, wantErr: ErrMissingReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := newValidJob(t)
			tt.mutate(&job)
			if err := job.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestJobValidateRejectsNegativeSize(t *testing.T) {
	job, err := NewJob("user-1", testJobID, DocInvoice, testNow)
	if err != nil {
		t.Fatalf("NewJob() error = %v", err)
	}
	job.SizeBytes = -1
	if err := job.Validate(); err == nil {
		t.Error("Validate() = nil for a negative size, want an error")
	}
}

func TestJobTransition(t *testing.T) {
	later := testNow.Add(time.Minute)

	tests := []struct {
		name    string
		from    JobStatus
		to      JobStatus
		wantErr bool
	}{
		{name: "pending to processing", from: StatusPending, to: StatusProcessing},
		{name: "processing to done", from: StatusProcessing, to: StatusDone},

		{name: "pending to done", from: StatusPending, to: StatusDone, wantErr: true},
		{name: "done to processing", from: StatusDone, to: StatusProcessing, wantErr: true},
		{name: "failed is reserved for Fail", from: StatusProcessing, to: StatusFailed, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := Job{JobID: testJobID, Status: tt.from, UpdatedAt: testNow}
			err := job.Transition(tt.to, later)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Transition(%q) error = %v, wantErr %v", tt.to, err, tt.wantErr)
			}
			if tt.wantErr {
				if job.Status != tt.from {
					t.Errorf("Status = %q after a rejected transition, want %q", job.Status, tt.from)
				}
				if !job.UpdatedAt.Equal(testNow) {
					t.Errorf("UpdatedAt moved to %v after a rejected transition", job.UpdatedAt)
				}
				return
			}
			if job.Status != tt.to {
				t.Errorf("Status = %q, want %q", job.Status, tt.to)
			}
			if !job.UpdatedAt.Equal(later) {
				t.Errorf("UpdatedAt = %v, want %v", job.UpdatedAt, later)
			}
		})
	}
}

func TestJobFail(t *testing.T) {
	later := testNow.Add(time.Minute)

	tests := []struct {
		name       string
		from       JobStatus
		reason     string
		wantReason string
		wantErr    error
	}{
		{name: "fail while processing", from: StatusProcessing, reason: "OCR produced no text", wantReason: "OCR produced no text"},
		{name: "trims the reason", from: StatusProcessing, reason: "  unsupported file type\n", wantReason: "unsupported file type"},

		{name: "empty reason", from: StatusProcessing, reason: "", wantErr: ErrMissingReason},
		{name: "blank reason", from: StatusProcessing, reason: "   ", wantErr: ErrMissingReason},
		{name: "fail twice", from: StatusFailed, reason: "again", wantErr: ErrInvalidTransition},
		{name: "fail after done", from: StatusDone, reason: "too late", wantErr: ErrInvalidTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := Job{JobID: testJobID, Status: tt.from, UpdatedAt: testNow}
			err := job.Fail(tt.reason, later)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Fail() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if job.Status != tt.from || job.FailureReason != "" {
					t.Errorf("job changed on a rejected Fail: %+v", job)
				}
				return
			}
			if job.Status != StatusFailed {
				t.Errorf("Status = %q, want %q", job.Status, StatusFailed)
			}
			if job.FailureReason != tt.wantReason {
				t.Errorf("FailureReason = %q, want %q", job.FailureReason, tt.wantReason)
			}
			if !job.UpdatedAt.Equal(later) {
				t.Errorf("UpdatedAt = %v, want %v", job.UpdatedAt, later)
			}
		})
	}
}

func TestJobWorkflowInput(t *testing.T) {
	job, err := NewJob("user-1", testJobID, DocInvoice, testNow)
	if err != nil {
		t.Fatalf("NewJob() error = %v", err)
	}
	in := job.WorkflowInput()
	if err := in.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if in.JobID != job.JobID || in.UserID != job.UserID || in.UploadKey != job.UploadKey || in.DocType != job.DocType {
		t.Errorf("WorkflowInput() = %+v, does not match job %+v", in, job)
	}
}

func TestWorkflowInputValidate(t *testing.T) {
	valid := WorkflowInput{
		JobID:     testJobID,
		UserID:    "user-1",
		DocType:   DocInvoice,
		UploadKey: UploadKey("user-1", testJobID),
	}

	tests := []struct {
		name    string
		mutate  func(*WorkflowInput)
		wantErr error
	}{
		{name: "complete", mutate: func(*WorkflowInput) {}},
		{name: "empty user", mutate: func(w *WorkflowInput) { w.UserID = "" }, wantErr: ErrInvalidUserID},
		{name: "bad job id", mutate: func(w *WorkflowInput) { w.JobID = "nope" }, wantErr: ErrInvalidJobID},
		{name: "unknown doc type", mutate: func(w *WorkflowInput) { w.DocType = DocType("csv") }, wantErr: ErrInvalidDocType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := valid
			tt.mutate(&in)
			if err := in.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestWorkflowInputValidateRejectsEmptyUploadKey(t *testing.T) {
	in := WorkflowInput{JobID: testJobID, UserID: "user-1", DocType: DocInvoice}
	if err := in.Validate(); err == nil {
		t.Error("Validate() = nil for an empty upload key, want an error")
	}
}

func TestUploadKey(t *testing.T) {
	if got, want := UploadKey("user-1", testJobID), "uploads/user-1/"+testJobID; got != want {
		t.Errorf("UploadKey() = %q, want %q", got, want)
	}
}

func TestDerivedKey(t *testing.T) {
	// Derived artifacts must not share the upload prefix: that prefix is
	// what starts a workflow, so a rendition written beside the original
	// would make the pipeline react to its own output.
	for _, name := range []string{ArtifactPage, ArtifactThumbnail, ArtifactText} {
		key := DerivedKey("user-1", testJobID, name)
		want := "derived/user-1/" + testJobID + "/" + name
		if key != want {
			t.Errorf("DerivedKey(%q) = %q, want %q", name, key, want)
		}
		if strings.HasPrefix(key, "uploads/") {
			t.Errorf("DerivedKey(%q) = %q, which the starter would react to", name, key)
		}
	}
}

func TestArtifactNamesAreDistinct(t *testing.T) {
	names := map[string]bool{}
	for _, name := range []string{ArtifactPage, ArtifactThumbnail, ArtifactText} {
		if names[name] {
			t.Errorf("artifact name %q is used twice, so one would overwrite the other", name)
		}
		names[name] = true
	}
}
