package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	"docflow/internal/model"
	"docflow/internal/store"
)

// sqsMessage wraps an S3 event for one key the way the queue delivers it.
func sqsMessage(t *testing.T, id, key string) events.SQSMessage {
	t.Helper()
	event := events.S3Event{Records: []events.S3EventRecord{{
		S3: events.S3Entity{Object: events.S3Object{Key: key}},
	}}}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode event: %v", err)
	}
	return events.SQSMessage{MessageId: id, Body: string(body)}
}

func newStarter(jobs *fakeStore, executions *fakeExecutions) *Starter {
	h := NewStarter(jobs, executions, testARN)
	h.Now = func() time.Time { return testNow }
	return h
}

func TestSplitUploadKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantUser   string
		wantJob    string
		wantErrIs  error
		wantReject bool
	}{
		{name: "an upload", key: "uploads/user-1/" + testJobID, wantUser: "user-1", wantJob: testJobID},

		// Derived artifacts live under other prefixes. Reacting to them
		// would start a workflow for something this pipeline just wrote.
		{name: "a thumbnail", key: "thumbnails/user-1/" + testJobID, wantReject: true, wantErrIs: ErrNotAnUpload},
		{name: "a derived page", key: "derived/user-1/" + testJobID, wantReject: true, wantErrIs: ErrNotAnUpload},

		{name: "no prefix", key: testJobID, wantReject: true, wantErrIs: ErrNotAnUpload},
		{name: "too few segments", key: "uploads/" + testJobID, wantReject: true, wantErrIs: ErrNotAnUpload},
		{name: "too many segments", key: "uploads/user-1/a/" + testJobID, wantReject: true, wantErrIs: ErrNotAnUpload},
		{name: "empty user", key: "uploads//" + testJobID, wantReject: true, wantErrIs: model.ErrInvalidUserID},
		{name: "job id is not a ULID", key: "uploads/user-1/../../etc/passwd", wantReject: true},
		{name: "lowercase job id", key: "uploads/user-1/01j8zqj9x7k3m4n5p6q7r8s9tv", wantReject: true, wantErrIs: model.ErrInvalidJobID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, job, err := splitUploadKey(tt.key)
			if tt.wantReject {
				if err == nil {
					t.Fatalf("splitUploadKey(%q) = %q/%q, want an error", tt.key, user, job)
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Errorf("error = %v, want %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitUploadKey(%q) error = %v", tt.key, err)
			}
			if user != tt.wantUser || job != tt.wantJob {
				t.Errorf("= %q/%q, want %q/%q", user, job, tt.wantUser, tt.wantJob)
			}
		})
	}
}

func TestDecodeKey(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain", raw: "uploads/u/j", want: "uploads/u/j"},
		// S3 encodes spaces as "+", which only QueryUnescape undoes.
		{name: "space as plus", raw: "uploads/u+1/j", want: "uploads/u 1/j"},
		{name: "percent encoded", raw: "uploads/u%2F1/j", want: "uploads/u/1/j"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeKey(tt.raw)
			if err != nil {
				t.Fatalf("decodeKey() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("decodeKey(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}

	if _, err := decodeKey("uploads/%zz/j"); err == nil {
		t.Error("decodeKey() = nil for a malformed escape, want an error")
	}
}

func TestStarterStartsAWorkflow(t *testing.T) {
	jobs, executions := newFakeStore(t), &fakeExecutions{}

	response, err := newStarter(jobs, executions).Handle(context.Background(),
		events.SQSEvent{Records: []events.SQSMessage{sqsMessage(t, "m1", testUploadKey())}})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(response.BatchItemFailures) != 0 {
		t.Fatalf("BatchItemFailures = %v, want none", response.BatchItemFailures)
	}

	if len(jobs.statusCalls) != 1 || jobs.statusCalls[0] != (statusCall{from: model.StatusPending, to: model.StatusProcessing}) {
		t.Errorf("status calls = %v, want one PENDING to PROCESSING", jobs.statusCalls)
	}
	if len(executions.inputs) != 1 {
		t.Fatalf("started %d executions, want 1", len(executions.inputs))
	}

	started := executions.inputs[0]
	// Naming the execution after the job is what makes a redelivery
	// harmless: Step Functions refuses a duplicate name.
	if *started.Name != testJobID {
		t.Errorf("execution name = %q, want the job id", *started.Name)
	}
	if *started.StateMachineArn != testARN {
		t.Errorf("state machine = %q, want %q", *started.StateMachineArn, testARN)
	}

	var state State
	if err := json.Unmarshal([]byte(*started.Input), &state); err != nil {
		t.Fatalf("execution input is not a State: %v", err)
	}
	if state.JobID != testJobID || state.UserID != testUser || state.UploadKey != testUploadKey() {
		t.Errorf("input = %+v, want the job's identifiers and key", state)
	}
}

func TestStarterIsIdempotent(t *testing.T) {
	tests := []struct {
		name       string
		storeErr   error
		executeErr error
	}{
		{
			// A redelivery of the same event: the job already moved on.
			name:     "job already processing",
			storeErr: store.ErrStatusConflict,
		},
		{
			// Two events for one object, racing.
			name:       "execution already running",
			executeErr: &sfntypes.ExecutionAlreadyExists{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			if tt.storeErr != nil {
				jobs.fail("UpdateStatus", tt.storeErr)
			}
			executions := &fakeExecutions{err: tt.executeErr}

			response, err := newStarter(jobs, executions).Handle(context.Background(),
				events.SQSEvent{Records: []events.SQSMessage{sqsMessage(t, "m1", testUploadKey())}})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if len(response.BatchItemFailures) != 0 {
				t.Errorf("a duplicate was reported as a failure: %v", response.BatchItemFailures)
			}
		})
	}
}

func TestStarterDiscardsWhatItCannotUse(t *testing.T) {
	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "not JSON", body: "not json"},
		{name: "empty body", body: ""},
		{name: "not an upload key", key: "thumbnails/user-1/" + testJobID},
		{name: "unreadable key", key: "uploads/%zz/" + testJobID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, executions := newFakeStore(t), &fakeExecutions{}

			message := events.SQSMessage{MessageId: "m1", Body: tt.body}
			if tt.key != "" {
				message = sqsMessage(t, "m1", tt.key)
			}

			response, err := newStarter(jobs, executions).Handle(context.Background(),
				events.SQSEvent{Records: []events.SQSMessage{message}})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			// Retrying would produce the same result until the queue gives
			// up, filling the dead-letter queue with noise.
			if len(response.BatchItemFailures) != 0 {
				t.Errorf("BatchItemFailures = %v, want the message discarded", response.BatchItemFailures)
			}
			if len(executions.inputs) != 0 {
				t.Error("a workflow was started for an unusable message")
			}
		})
	}
}

func TestStarterDiscardsAnUploadWithNoJob(t *testing.T) {
	jobs := newFakeStore(t).fail("GetJob", store.ErrJobNotFound)
	executions := &fakeExecutions{}

	response, err := newStarter(jobs, executions).Handle(context.Background(),
		events.SQSEvent{Records: []events.SQSMessage{sqsMessage(t, "m1", testUploadKey())}})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(response.BatchItemFailures) != 0 {
		t.Errorf("BatchItemFailures = %v, want the message discarded", response.BatchItemFailures)
	}
	if len(executions.inputs) != 0 {
		t.Error("a workflow was started for a job that does not exist")
	}
}

func TestStarterReportsOnlyTheFailedMessage(t *testing.T) {
	// One bad message must not force SQS to redeliver the good ones beside
	// it, which is the whole point of reporting partial batch failures.
	jobs := newFakeStore(t)
	executions := &fakeExecutions{}
	starter := newStarter(jobs, executions)

	good := sqsMessage(t, "good", testUploadKey())
	bad := sqsMessage(t, "bad", testUploadKey())

	// Fail only while the second message is handled.
	calls := 0
	failing := &failingExecutions{inner: executions, failOn: 2, calls: &calls}
	starter.Executions = failing

	response, err := starter.Handle(context.Background(),
		events.SQSEvent{Records: []events.SQSMessage{good, bad}})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(response.BatchItemFailures) != 1 || response.BatchItemFailures[0].ItemIdentifier != "bad" {
		t.Errorf("BatchItemFailures = %v, want only the bad message", response.BatchItemFailures)
	}
}

func TestStarterReportsTransientStoreFailures(t *testing.T) {
	jobs := newFakeStore(t).fail("UpdateStatus", errors.New("throttled"))
	executions := &fakeExecutions{}

	response, err := newStarter(jobs, executions).Handle(context.Background(),
		events.SQSEvent{Records: []events.SQSMessage{sqsMessage(t, "m1", testUploadKey())}})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(response.BatchItemFailures) != 1 {
		t.Errorf("BatchItemFailures = %v, want the message retried", response.BatchItemFailures)
	}
	if len(executions.inputs) != 0 {
		t.Error("a workflow was started despite the job not being marked PROCESSING")
	}
}
