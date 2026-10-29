// Package pipeline holds the processing workflow's step logic, so the
// Lambda entrypoints under cmd/ stay thin.
//
// Steps exchange a State: identifiers and S3 keys, never file contents. The
// payload stays small enough that Step Functions never has to spill it, and
// a failed execution's input is readable in the console.
//
// Failures come in two kinds, and the difference decides whether the
// workflow backs off or gives up. The two error types here are what the
// state machine matches on: the Lambda runtime reports a returned error's Go
// type name, so these names appear verbatim in the ASL. Returning a bare
// errors.New would report "errorString", which no retry rule can usefully
// match.
package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"docflow/internal/model"
	"docflow/internal/ocr"
	"docflow/internal/preprocess"
	"docflow/internal/storage"
	"docflow/internal/validation"
)

// State is the payload Step Functions carries between states.
type State struct {
	model.WorkflowInput

	// ContentType is what validation found in the bytes, which may differ
	// from what the client declared.
	ContentType string `json:"contentType,omitempty"`
	// SizeBytes is the stored object's real length.
	SizeBytes int64 `json:"sizeBytes,omitempty"`

	// Keys of what the recognition step derived. Keys, not contents: the
	// payload stays small enough that Step Functions never has to spill it.
	PageKey      string `json:"pageKey,omitempty"`
	ThumbnailKey string `json:"thumbnailKey,omitempty"`
	TextKey      string `json:"textKey,omitempty"`

	// What the engine reported about the text behind TextKey.
	Confidence float64 `json:"confidence,omitempty"`
	Pages      int     `json:"pages,omitempty"`
	Provider   string  `json:"provider,omitempty"`

	// Failed and Reason are set by the failure path, so the finish step can
	// record why without a second payload shape.
	Failed bool   `json:"failed,omitempty"`
	Reason string `json:"reason,omitempty"`

	// Error is what the state machine's catch block captured. Turning it
	// into a reason is done here rather than in the ASL, because deciding
	// which failures are safe to repeat to a user is logic worth testing.
	Error *TaskError `json:"error,omitempty"`
}

// TaskError is the shape Step Functions puts in a catch block's result:
// the error name and a JSON-encoded cause.
type TaskError struct {
	Error string `json:"Error"`
	Cause string `json:"Cause"`
}

// lambdaCause is the payload the Lambda runtime encodes into Cause.
type lambdaCause struct {
	ErrorMessage string `json:"errorMessage"`
	ErrorType    string `json:"errorType"`
}

// Reason returns a short, user-safe explanation of the captured failure.
//
// A permanent failure's message starts with a sentence written for the user,
// followed by reasonSeparator and the technical detail, so only the part
// before the separator is repeated. A message without the separator is not
// one this code wrote, and a transient failure carries whatever AWS said,
// which may name a table or a bucket; both report only that processing
// failed.
func (e *TaskError) Reason() string {
	if e == nil {
		return defaultFailureReason
	}
	if e.Error != permanentErrorName {
		return defaultFailureReason
	}
	var cause lambdaCause
	if err := json.Unmarshal([]byte(e.Cause), &cause); err != nil {
		return defaultFailureReason
	}
	reason, _, found := strings.Cut(cause.ErrorMessage, reasonSeparator)
	if !found || reason == "" {
		return defaultFailureReason
	}
	return reason
}

// reasonSeparator divides a PermanentError's message into what a user may be
// told and what only the logs and the execution history should hold. The
// message is all that crosses from a Lambda to the state machine, so this is
// how the two travel together.
const reasonSeparator = " | "

// permanentErrorName is what the Lambda runtime reports for a
// *PermanentError, and what the state machine matches on. A test pins it to
// the type's actual name.
const permanentErrorName = "PermanentError"

// PermanentError is a failure no retry can fix: a file that is not a
// document, a job that does not exist. The state machine catches it and
// fails the job without backing off.
type PermanentError struct {
	Step model.StepName
	Err  error
}

// Error leads with the sentence a user may be shown and keeps the detail
// after it: Reason relies on that order.
func (e *PermanentError) Error() string {
	return fmt.Sprintf("%s%s%s: %v", publicReason(e.Err), reasonSeparator, e.Step, e.Err)
}

func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a permanent failure of step.
func Permanent(step model.StepName, err error) error {
	return &PermanentError{Step: step, Err: err}
}

// TransientError is a failure worth retrying: a throttled table, a refused
// read. The state machine retries it with backoff before giving up.
type TransientError struct {
	Step model.StepName
	Err  error
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("%s: %v", e.Step, e.Err)
}

func (e *TransientError) Unwrap() error { return e.Err }

// Transient wraps err as a retryable failure of step.
func Transient(step model.StepName, err error) error {
	return &TransientError{Step: step, Err: err}
}

// IsPermanent reports whether err is a failure no retry can fix.
func IsPermanent(err error) bool {
	_, ok := errors.AsType[*PermanentError](err)
	return ok
}

// IsTransient reports whether err is worth retrying.
func IsTransient(err error) bool {
	_, ok := errors.AsType[*TransientError](err)
	return ok
}

// errUnreadable marks a file that passed validation but cannot be decoded or
// rendered: damaged, truncated, or not what its first bytes claimed.
var errUnreadable = errors.New("file is damaged or cannot be read")

// publicReasons are the sentences a user is shown, by the failure that
// caused them. They say what is wrong with the document or what to do, never
// how the pipeline found out. Order matters only for errors that wrap more
// than one of these.
var publicReasons = []struct {
	err    error
	reason string
}{
	{validation.ErrEmpty, "The file is empty."},
	{validation.ErrTooLarge, "The file is larger than the size limit."},
	{validation.ErrUnrecognized, "The file is not a supported document type."},
	{validation.ErrTypeMismatch, "The file's contents do not match the type it was uploaded as."},
	{validation.ErrNotAccepted, "This kind of document does not accept that file type."},
	{storage.ErrNotFound, "No file was found for this job. Upload it again."},
	{errUnreadable, "The file is damaged or cannot be read."},
	{preprocess.ErrTooLarge, "The image is too large to process."},
	{preprocess.ErrEmptyImage, "The image is empty."},
	{preprocess.ErrUnsupported, "The file type cannot be processed."},
	{ocr.ErrUnsupported, "The file type cannot be processed."},
	{ocr.ErrNoText, "No text could be read from the document."},
	{ocr.ErrEmptyPDF, "The PDF has no pages."},
}

// defaultPublicReason is what a permanent failure with no entry above says.
const defaultPublicReason = "The document could not be processed."

// publicReason returns the user-safe sentence for a permanent failure's
// cause. An unlisted cause gets the default: an error this code did not
// anticipate may say anything, a bucket name included.
func publicReason(err error) string {
	for _, known := range publicReasons {
		if errors.Is(err, known.err) {
			return known.reason
		}
	}
	return defaultPublicReason
}

// UserMessage returns a short, user-safe reason for a failure.
//
// It is what reaches the job record and the API, so it never carries a
// bucket name, a table name, a file's bytes, or an AWS request id.
func UserMessage(err error) string {
	var permanent *PermanentError
	if errors.As(err, &permanent) {
		return publicReason(permanent.Err)
	}
	var transient *TransientError
	if errors.As(err, &transient) {
		return fmt.Sprintf("%s failed after retrying", transient.Step)
	}
	return "processing failed"
}
