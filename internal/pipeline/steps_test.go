package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"docflow/internal/model"
	"docflow/internal/ocr"
	"docflow/internal/storage"
	"docflow/internal/store"
	"docflow/internal/validation"
)

// pdfBody is a stored object that validation will recognize.
var pdfBody = append([]byte("%PDF-1.7"), bytes.Repeat([]byte{0}, 2048)...)

func testState() State {
	return State{WorkflowInput: model.WorkflowInput{
		JobID:     testJobID,
		UserID:    testUser,
		DocType:   model.DocInvoice,
		UploadKey: testUploadKey(),
	}}
}

func newValidate(jobs *fakeStore, documents *fakeDocuments) *Validate {
	h := NewValidate(jobs, documents)
	h.Now = func() time.Time { return testNow }
	return h
}

func newFinish(jobs *fakeStore) *Finish {
	h := NewFinish(jobs)
	h.Now = func() time.Time { return testNow }
	return h
}

// The state machine matches these names in its Retry and Catch rules, and
// the Lambda runtime reports a returned error's bare Go type name. Renaming
// either type would silently stop the rules matching, so the names are
// pinned here.
func TestErrorTypeNamesMatchTheStateMachine(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: Permanent(model.StepValidate, errors.New("bad")), want: "PermanentError"},
		{err: Transient(model.StepValidate, errors.New("slow")), want: "TransientError"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := reflect.TypeOf(tt.err).Elem().Name()
			if got != tt.want {
				t.Errorf("Lambda would report errorType %q, but the state machine matches %q", got, tt.want)
			}
		})
	}
}

func TestErrorClassification(t *testing.T) {
	cause := errors.New("because")
	permanent := Permanent(model.StepOCR, cause)
	transient := Transient(model.StepOCR, cause)

	if !IsPermanent(permanent) || IsTransient(permanent) {
		t.Error("a permanent error is misclassified")
	}
	if !IsTransient(transient) || IsPermanent(transient) {
		t.Error("a transient error is misclassified")
	}
	for _, err := range []error{permanent, transient} {
		if !errors.Is(err, cause) {
			t.Errorf("%v does not unwrap to its cause", err)
		}
	}
	if IsPermanent(cause) || IsTransient(cause) {
		t.Error("a plain error was classified")
	}
}

func TestUserMessageKeepsInternalsOut(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "permanent says what is wrong with the document",
			err:  Permanent(model.StepValidate, validation.ErrUnrecognized),
			want: "The file is not a supported document type.",
		},
		{
			// An error this code did not anticipate may say anything, so
			// it is not repeated.
			name: "an unlisted permanent cause gets the default",
			err:  Permanent(model.StepOCR, errors.New("open s3://docflow-prod/uploads/u/j: denied")),
			want: defaultPublicReason,
		},
		{
			// A transient failure's cause is an AWS message that may name a
			// table or a bucket, so only the fact of it travels.
			name: "transient says only that it was retried",
			err:  Transient(model.StepOCR, errors.New("table docflow-prod: throttled")),
			want: "ocr failed after retrying",
		},
		{name: "anything else", err: errors.New("boom"), want: "processing failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UserMessage(tt.err); got != tt.want {
				t.Errorf("UserMessage() = %q, want %q", got, tt.want)
			}
		})
	}

	message := UserMessage(Transient(model.StepOCR, errors.New("table docflow-prod: throttled")))
	if bytes.Contains([]byte(message), []byte("docflow-prod")) {
		t.Errorf("UserMessage leaked an internal name: %q", message)
	}
}

func TestValidateAcceptsADocument(t *testing.T) {
	jobs, documents := newFakeStore(t), &fakeDocuments{body: pdfBody}

	got, err := newValidate(jobs, documents).Handle(context.Background(), testState())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", got.ContentType)
	}
	if got.SizeBytes != int64(len(pdfBody)) {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, len(pdfBody))
	}

	// Only the header is read: a ten megabyte scan has no business crossing
	// the network for a format check.
	if documents.headerBytes != 16 {
		t.Errorf("read %d header bytes, want 16", documents.headerBytes)
	}

	if len(jobs.steps) != 1 {
		t.Fatalf("recorded %d step results, want 1", len(jobs.steps))
	}
	if jobs.steps[0].Status != model.StepSucceeded || jobs.steps[0].Step != model.StepValidate {
		t.Errorf("step result = %+v, want a succeeded validate", jobs.steps[0])
	}
}

func TestValidateRejectsPermanently(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		storeErr  error
		sizeErr   error
		headerErr error
	}{
		{name: "not a document", body: []byte("<html>this is a web page</html>")},
		{name: "empty object", body: nil},
		{name: "job is gone", body: pdfBody, storeErr: store.ErrJobNotFound},
		{name: "nothing was uploaded", body: pdfBody, sizeErr: storage.ErrNotFound},
		{name: "header is gone", body: pdfBody, headerErr: storage.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			if tt.storeErr != nil {
				jobs.fail("GetJob", tt.storeErr)
			}
			documents := &fakeDocuments{body: tt.body, sizeErr: tt.sizeErr, headerErr: tt.headerErr}

			_, err := newValidate(jobs, documents).Handle(context.Background(), testState())
			if err == nil {
				t.Fatal("Handle() = nil, want a failure")
			}
			// Retrying any of these produces the same answer, and the state
			// machine must not back off before giving up.
			if !IsPermanent(err) {
				t.Errorf("error = %v, which the state machine would retry", err)
			}
		})
	}
}

func TestValidateRetriesTransientFailures(t *testing.T) {
	tests := []struct {
		name      string
		storeErr  error
		sizeErr   error
		headerErr error
	}{
		{name: "table throttled", storeErr: errTransient},
		{name: "head refused", sizeErr: errTransient},
		{name: "read refused", headerErr: errTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			if tt.storeErr != nil {
				jobs.fail("GetJob", tt.storeErr)
			}
			documents := &fakeDocuments{body: pdfBody, sizeErr: tt.sizeErr, headerErr: tt.headerErr}

			_, err := newValidate(jobs, documents).Handle(context.Background(), testState())
			if !IsTransient(err) {
				t.Errorf("error = %v, want a transient failure the state machine will retry", err)
			}
		})
	}
}

func TestValidateRejectsAMalformedState(t *testing.T) {
	jobs, documents := newFakeStore(t), &fakeDocuments{body: pdfBody}
	state := testState()
	state.JobID = "not-a-ulid"

	_, err := newValidate(jobs, documents).Handle(context.Background(), state)
	if !IsPermanent(err) {
		t.Errorf("error = %v, want a permanent failure", err)
	}
}

// Losing the audit trail must not turn a valid document into a failed job.
func TestValidateSurvivesAnUnrecordableStep(t *testing.T) {
	jobs := newFakeStore(t).fail("PutStepResult", errTransient)
	documents := &fakeDocuments{body: pdfBody}

	got, err := newValidate(jobs, documents).Handle(context.Background(), testState())
	if err != nil {
		t.Fatalf("Handle() error = %v, want the document still accepted", err)
	}
	if got.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want the document still validated", got.ContentType)
	}
}

func TestValidateRecordsARejection(t *testing.T) {
	jobs := newFakeStore(t)
	documents := &fakeDocuments{body: []byte("<html>not a document</html>")}

	if _, err := newValidate(jobs, documents).Handle(context.Background(), testState()); err == nil {
		t.Fatal("Handle() = nil, want a failure")
	}
	if len(jobs.steps) != 1 {
		t.Fatalf("recorded %d step results, want the rejection recorded", len(jobs.steps))
	}
	step := jobs.steps[0]
	if step.Status != model.StepFailed || step.Error == "" {
		t.Errorf("step result = %+v, want a failed validate with a reason", step)
	}
	if step.Retryable {
		t.Error("the recorded step says it is retryable, but validation never is")
	}
}

func TestFinishMarksDone(t *testing.T) {
	jobs := newFakeStore(t)

	if _, err := newFinish(jobs).Handle(context.Background(), testState()); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(jobs.statusCalls) != 1 || jobs.statusCalls[0] != (statusCall{from: model.StatusProcessing, to: model.StatusDone}) {
		t.Errorf("status calls = %v, want one PROCESSING to DONE", jobs.statusCalls)
	}
	if len(jobs.failCalls) != 0 {
		t.Error("the success path failed the job")
	}
}

func TestFinishMarksFailed(t *testing.T) {
	jobs := newFakeStore(t)
	state := testState()
	state.Failed = true
	state.Reason = "validate failed: file format not recognized"

	if _, err := newFinish(jobs).Handle(context.Background(), state); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(jobs.failCalls) != 1 || jobs.failCalls[0].reason != state.Reason {
		t.Errorf("fail calls = %v, want one carrying the reason", jobs.failCalls)
	}
	if len(jobs.statusCalls) != 0 {
		t.Error("the failure path also moved the status some other way")
	}
}

// A FAILED job with no reason is rejected by the model, so losing the status
// change would be worse than a vague message.
func TestFinishSubstitutesAMissingReason(t *testing.T) {
	jobs := newFakeStore(t)
	state := testState()
	state.Failed = true

	if _, err := newFinish(jobs).Handle(context.Background(), state); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(jobs.failCalls) != 1 || jobs.failCalls[0].reason != defaultFailureReason {
		t.Errorf("fail calls = %v, want the default reason", jobs.failCalls)
	}
}

func TestFinishAcceptsAnAlreadyFinishedJob(t *testing.T) {
	tests := []struct {
		name   string
		method string
		err    error
		failed bool
	}{
		{name: "done raced", method: "UpdateStatus", err: store.ErrStatusConflict},
		{name: "done, job gone", method: "UpdateStatus", err: store.ErrJobNotFound},
		{name: "failed raced", method: "FailJob", err: store.ErrStatusConflict, failed: true},
		{name: "failed, job gone", method: "FailJob", err: store.ErrJobNotFound, failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t).fail(tt.method, tt.err)
			state := testState()
			state.Failed = tt.failed

			// The job is terminal either way, so looping would achieve
			// nothing.
			if _, err := newFinish(jobs).Handle(context.Background(), state); err != nil {
				t.Errorf("Handle() error = %v, want the step to accept it", err)
			}
		})
	}
}

func TestFinishRetriesTransientFailures(t *testing.T) {
	tests := []struct {
		name   string
		method string
		failed bool
	}{
		{name: "marking done", method: "UpdateStatus"},
		{name: "marking failed", method: "FailJob", failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t).fail(tt.method, errTransient)
			state := testState()
			state.Failed = tt.failed

			_, err := newFinish(jobs).Handle(context.Background(), state)
			if !IsTransient(err) {
				t.Errorf("error = %v, want a transient failure", err)
			}
		})
	}
}

func TestFinishRejectsAMalformedState(t *testing.T) {
	jobs := newFakeStore(t)
	state := testState()
	state.UserID = ""

	_, err := newFinish(jobs).Handle(context.Background(), state)
	if !IsPermanent(err) {
		t.Errorf("error = %v, want a permanent failure", err)
	}
}

func TestTaskErrorReason(t *testing.T) {
	tests := []struct {
		name string
		err  *TaskError
		want string
	}{
		{
			// Only the sentence before the separator is for the user.
			name: "permanent",
			err:  &TaskError{Error: "PermanentError", Cause: `{"errorMessage":"The file is not a supported document type. | validate: file format not recognized","errorType":"PermanentError"}`},
			want: "The file is not a supported document type.",
		},
		{
			// A message with no separator was not written by this code, so
			// none of it is shown.
			name: "permanent without a user sentence",
			err:  &TaskError{Error: "PermanentError", Cause: `{"errorMessage":"validate: leading bytes 7468: file format not recognized: permanent","errorType":"PermanentError"}`},
			want: defaultFailureReason,
		},
		{
			name: "permanent with an empty user sentence",
			err:  &TaskError{Error: "PermanentError", Cause: `{"errorMessage":" | validate: detail","errorType":"PermanentError"}`},
			want: defaultFailureReason,
		},
		{
			// This one carries whatever AWS said, which may name a table.
			name: "transient",
			err:  &TaskError{Error: "TransientError", Cause: `{"errorMessage":"ocr: table docflow-prod: throttled","errorType":"TransientError"}`},
			want: defaultFailureReason,
		},
		{name: "a Lambda crash", err: &TaskError{Error: "Runtime.ExitError", Cause: "signal: killed"}, want: defaultFailureReason},
		{name: "a timeout", err: &TaskError{Error: "States.Timeout"}, want: defaultFailureReason},
		{name: "unparseable cause", err: &TaskError{Error: "PermanentError", Cause: "not json"}, want: defaultFailureReason},
		{name: "empty message", err: &TaskError{Error: "PermanentError", Cause: `{"errorMessage":""}`}, want: defaultFailureReason},
		{name: "nothing captured", err: nil, want: defaultFailureReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Reason(); got != tt.want {
				t.Errorf("Reason() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A job's failure reason is what the API shows a user. This follows a real
// failure the way it travels: the error a step returns, the message the
// Lambda runtime encodes into the catch block, and the reason Finish stores.
func TestPermanentFailuresReachTheJobAsUserSentences(t *testing.T) {
	tests := []struct {
		name string
		step model.StepName
		err  error
		want string
	}{
		{
			name: "not a document",
			step: model.StepValidate,
			err:  fmt.Errorf("leading bytes 7468697320697320: %w: %w", validation.ErrUnrecognized, validation.ErrPermanent),
			want: "The file is not a supported document type.",
		},
		{
			name: "too large",
			step: model.StepValidate,
			err:  fmt.Errorf("%d bytes: %w: %w", 11<<20, validation.ErrTooLarge, validation.ErrPermanent),
			want: "The file is larger than the size limit.",
		},
		{
			name: "never uploaded",
			step: model.StepValidate,
			err:  fmt.Errorf("no object was uploaded: %w", storage.ErrNotFound),
			want: "No file was found for this job. Upload it again.",
		},
		{
			name: "does not decode",
			step: model.StepOCR,
			err:  fmt.Errorf("decode page: %w: %w", errUnreadable, errors.New("read image/png header: unexpected EOF")),
			want: "The file is damaged or cannot be read.",
		},
		{
			name: "blank document",
			step: model.StepOCR,
			err:  ocr.ErrNoText,
			want: "No text could be read from the document.",
		},
		{
			name: "unlisted cause",
			step: model.StepExtract,
			err:  errors.New("provider \"ocrspace\": unknown OCR provider"),
			want: defaultPublicReason,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			returned := Permanent(tt.step, tt.err)

			// The runtime puts the returned error's text in errorMessage.
			cause, err := json.Marshal(lambdaCause{ErrorMessage: returned.Error(), ErrorType: permanentErrorName})
			if err != nil {
				t.Fatalf("marshal cause: %v", err)
			}
			reason := (&TaskError{Error: permanentErrorName, Cause: string(cause)}).Reason()

			if reason != tt.want {
				t.Errorf("Reason() = %q, want %q", reason, tt.want)
			}
			// What the user sees must not be how the pipeline found out.
			for _, internal := range []string{"permanent", "leading bytes", "unexpected EOF", "ocrspace", string(tt.step) + ":"} {
				if strings.Contains(reason, internal) {
					t.Errorf("reason %q leaks %q", reason, internal)
				}
			}
			// The detail is not lost: logs and the execution history keep it.
			if !strings.Contains(returned.Error(), tt.err.Error()) {
				t.Errorf("error text %q dropped the detail %q", returned.Error(), tt.err)
			}
		})
	}
}

func TestTaskErrorNameMatchesTheType(t *testing.T) {
	want := reflect.TypeOf(Permanent(model.StepValidate, errors.New("x"))).Elem().Name()
	if permanentErrorName != want {
		t.Errorf("permanentErrorName = %q, but the type is named %q", permanentErrorName, want)
	}
}

func TestFinishUsesTheCapturedError(t *testing.T) {
	jobs := newFakeStore(t)
	state := testState()
	state.Failed = true
	state.Error = &TaskError{
		Error: "PermanentError",
		Cause: `{"errorMessage":"The file is not a supported document type. | validate: file format not recognized","errorType":"PermanentError"}`,
	}

	if _, err := newFinish(jobs).Handle(context.Background(), state); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(jobs.failCalls) != 1 || jobs.failCalls[0].reason != "The file is not a supported document type." {
		t.Errorf("fail calls = %v, want the captured reason", jobs.failCalls)
	}
}

func TestFinishKeepsTransientInternalsOutOfTheJob(t *testing.T) {
	jobs := newFakeStore(t)
	state := testState()
	state.Failed = true
	state.Error = &TaskError{
		Error: "TransientError",
		Cause: `{"errorMessage":"ocr: table docflow-prod: throttled","errorType":"TransientError"}`,
	}

	if _, err := newFinish(jobs).Handle(context.Background(), state); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(jobs.failCalls) != 1 {
		t.Fatalf("fail calls = %v, want one", jobs.failCalls)
	}
	if strings.Contains(jobs.failCalls[0].reason, "docflow-prod") {
		t.Errorf("the job's reason leaked an internal name: %q", jobs.failCalls[0].reason)
	}
}
