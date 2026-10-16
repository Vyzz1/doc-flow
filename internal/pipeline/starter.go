package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/store"
)

// uploadPrefix is where clients upload, and the only prefix the starter
// reacts to. Anything else under the bucket is a derived artifact this
// pipeline wrote itself, and reacting to those would loop.
const uploadPrefix = "uploads/"

// ErrNotAnUpload means an event named a key the starter does not handle.
var ErrNotAnUpload = errors.New("object key is not an upload")

// Executions starts workflow executions.
type Executions interface {
	StartExecution(ctx context.Context, in *sfn.StartExecutionInput, optFns ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error)
}

// Starter turns an uploaded object into a running workflow.
type Starter struct {
	Jobs         store.JobStore
	Executions   Executions
	StateMachine string

	Now func() time.Time
}

// NewStarter returns a starter using the production clock.
func NewStarter(jobs store.JobStore, executions Executions, stateMachine string) *Starter {
	return &Starter{Jobs: jobs, Executions: executions, StateMachine: stateMachine, Now: time.Now}
}

// Handle processes one SQS batch of S3 events.
//
// It reports per-message failures rather than failing the batch, so one bad
// upload does not force SQS to redeliver the messages beside it. Everything
// it does is keyed on the job id, so a redelivery that gets through anyway
// changes nothing.
func (h *Starter) Handle(ctx context.Context, batch events.SQSEvent) (events.SQSEventResponse, error) {
	var response events.SQSEventResponse

	for _, message := range batch.Records {
		if err := h.handleMessage(ctx, message); err != nil {
			logging.FromContext(ctx).ErrorContext(ctx, "message failed",
				slog.String("messageId", message.MessageId),
				slog.String("error", err.Error()),
			)
			response.BatchItemFailures = append(response.BatchItemFailures,
				events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
		}
	}
	return response, nil
}

// handleMessage starts a workflow for every upload the message names.
//
// A message that cannot be parsed, or that names something other than an
// upload, is discarded rather than retried: redelivering it would produce
// the same result until the queue gives up and the DLQ fills with noise.
func (h *Starter) handleMessage(ctx context.Context, message events.SQSMessage) error {
	var event events.S3Event
	if err := json.Unmarshal([]byte(message.Body), &event); err != nil {
		logging.FromContext(ctx).WarnContext(ctx, "message is not an S3 event, discarding",
			slog.String("messageId", message.MessageId))
		return nil
	}

	for _, record := range event.Records {
		key, err := decodeKey(record.S3.Object.Key)
		if err != nil {
			logging.FromContext(ctx).WarnContext(ctx, "unreadable object key, discarding",
				slog.String("key", record.S3.Object.Key))
			continue
		}

		userID, jobID, err := splitUploadKey(key)
		if err != nil {
			logging.FromContext(ctx).WarnContext(ctx, "event is not for an upload, discarding",
				slog.String("key", key))
			continue
		}

		if err := h.start(logging.WithJob(ctx, jobID, userID), userID, jobID); err != nil {
			return err
		}
	}
	return nil
}

// start moves the job to PROCESSING and launches its workflow.
func (h *Starter) start(ctx context.Context, userID, jobID string) error {
	logger := logging.FromContext(ctx)

	job, err := h.Jobs.GetJob(ctx, userID, jobID)
	if err != nil {
		if errors.Is(err, store.ErrJobNotFound) {
			// The upload outlived its job, or arrived for one that never
			// existed. Nothing to start and nothing to retry.
			logger.WarnContext(ctx, "upload has no job, discarding")
			return nil
		}
		return fmt.Errorf("read job: %w", err)
	}

	switch err := h.Jobs.UpdateStatus(ctx, userID, jobID, model.StatusPending, model.StatusProcessing, h.Now()); {
	case err == nil:
	case errors.Is(err, store.ErrStatusConflict):
		// A redelivery, or a second event for the same object. The workflow
		// is already running; starting it again is handled below by the
		// execution name, so carry on rather than failing the message.
		logger.InfoContext(ctx, "job already started, continuing to the execution")
	default:
		return fmt.Errorf("mark job processing: %w", err)
	}

	input, err := json.Marshal(State{WorkflowInput: job.WorkflowInput()})
	if err != nil {
		return fmt.Errorf("encode workflow input: %w", err)
	}

	// The execution is named after the job, which is what makes starting it
	// twice harmless: Step Functions refuses a duplicate name rather than
	// running the document through the pipeline again.
	_, err = h.Executions.StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(h.StateMachine),
		Name:            aws.String(jobID),
		Input:           aws.String(string(input)),
	})
	if err != nil {
		if _, exists := errors.AsType[*sfntypes.ExecutionAlreadyExists](err); exists {
			logger.InfoContext(ctx, "workflow already running for this job")
			return nil
		}
		return fmt.Errorf("start execution: %w", err)
	}

	logger.InfoContext(ctx, "workflow started")
	return nil
}

// decodeKey undoes the form encoding S3 applies to keys in its events.
func decodeKey(raw string) (string, error) {
	// S3 encodes spaces as "+", which only QueryUnescape undoes.
	key, err := url.QueryUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("decode key %q: %w", raw, err)
	}
	return key, nil
}

// splitUploadKey pulls the owner and the job out of an upload key.
//
// The layout is uploads/<userID>/<jobID>, and model forbids a slash in a
// user id, so three parts is the only valid shape.
func splitUploadKey(key string) (userID, jobID string, err error) {
	if !strings.HasPrefix(key, uploadPrefix) {
		return "", "", fmt.Errorf("key %q: %w", key, ErrNotAnUpload)
	}
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("key %q has %d segments, want 3: %w", key, len(parts), ErrNotAnUpload)
	}

	userID, jobID = parts[1], parts[2]
	if err := model.ValidateUserID(userID); err != nil {
		return "", "", fmt.Errorf("key %q: %w", key, err)
	}
	if !model.ValidJobID(jobID) {
		return "", "", fmt.Errorf("key %q does not end in a ULID: %w", key, model.ErrInvalidJobID)
	}
	return userID, jobID, nil
}
