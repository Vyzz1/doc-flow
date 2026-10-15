// Package model defines the document pipeline's shared domain types: jobs,
// their status state machine, per-step results, and extracted document data.
//
// Types here hold identifiers and S3 keys only. Document contents never travel
// with them, which keeps Step Functions payloads and DynamoDB items small.
package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// DocType is the kind of document a job carries. It selects the extraction
// rules applied after OCR.
type DocType string

// The document kinds the pipeline accepts.
const (
	DocInvoice DocType = "invoice"
	DocID      DocType = "id_document"
	DocImage   DocType = "image"
)

// Retention is how long jobs, uploads, and results live before TTL removes
// them. It matches the S3 lifecycle rule on the upload bucket.
const Retention = 7 * 24 * time.Hour

// maxIDLen bounds identifiers so a single key cannot grow unbounded.
const maxIDLen = 128

// Errors reported by Validate and the job constructors.
var (
	ErrInvalidUserID  = errors.New("invalid user id")
	ErrInvalidJobID   = errors.New("invalid job id")
	ErrInvalidDocType = errors.New("invalid document type")
	ErrMissingReason  = errors.New("failure reason required")
)

// Valid reports whether d is one of the accepted document kinds.
func (d DocType) Valid() bool {
	switch d {
	case DocInvoice, DocID, DocImage:
		return true
	default:
		return false
	}
}

// String makes DocType print as its bare value in log fields and errors.
func (d DocType) String() string { return string(d) }

// Job is one document-processing request, keyed by its owner and a ULID that
// sorts by creation time.
type Job struct {
	UserID string
	JobID  string
	Status JobStatus

	DocType DocType
	// UploadKey is where the client uploads the original document.
	UploadKey string
	// ContentType and SizeBytes are what the client declared at create time.
	// Validation re-checks both against the stored object's magic bytes and
	// actual length before processing starts.
	ContentType string
	SizeBytes   int64

	CreatedAt time.Time
	UpdatedAt time.Time
	// ExpiresAt drives the DynamoDB TTL attribute.
	ExpiresAt time.Time

	// FailureReason is a short, user-safe explanation set only on StatusFailed.
	// It never carries document contents or internal error chains.
	FailureReason string
}

// NewJob returns a PENDING job owned by userID. The caller supplies jobID so
// that ULID generation stays with the handler that can also log it, and now so
// that tests control time.
func NewJob(userID, jobID string, docType DocType, now time.Time) (Job, error) {
	if err := validateUserID(userID); err != nil {
		return Job{}, err
	}
	if err := validateJobID(jobID); err != nil {
		return Job{}, err
	}
	if !docType.Valid() {
		return Job{}, fmt.Errorf("document type %q: %w", docType, ErrInvalidDocType)
	}
	now = now.UTC()
	return Job{
		UserID:    userID,
		JobID:     jobID,
		Status:    StatusPending,
		DocType:   docType,
		UploadKey: UploadKey(userID, jobID),
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(Retention),
	}, nil
}

// UploadKey returns the S3 key holding a job's original document.
func UploadKey(userID, jobID string) string {
	return "uploads/" + userID + "/" + jobID
}

// Validate reports whether j is internally consistent. Use it before writing a
// job and after reading one back, so a malformed item fails at the boundary
// rather than deeper in the workflow.
func (j Job) Validate() error {
	if err := validateUserID(j.UserID); err != nil {
		return err
	}
	if err := validateJobID(j.JobID); err != nil {
		return err
	}
	if !j.Status.Valid() {
		return fmt.Errorf("status %q: %w", j.Status, ErrInvalidStatus)
	}
	if !j.DocType.Valid() {
		return fmt.Errorf("document type %q: %w", j.DocType, ErrInvalidDocType)
	}
	if j.SizeBytes < 0 {
		return fmt.Errorf("size %d bytes: negative", j.SizeBytes)
	}
	if j.Status == StatusFailed && j.FailureReason == "" {
		return fmt.Errorf("job %s is FAILED: %w", j.JobID, ErrMissingReason)
	}
	return nil
}

// Transition moves j to next and stamps UpdatedAt. Use Fail for StatusFailed,
// which also records why.
func (j *Job) Transition(next JobStatus, now time.Time) error {
	if next == StatusFailed {
		return fmt.Errorf("transition job %s to FAILED: use Fail", j.JobID)
	}
	if err := j.Status.ValidateTransition(next); err != nil {
		return fmt.Errorf("job %s: %w", j.JobID, err)
	}
	j.Status = next
	j.UpdatedAt = now.UTC()
	return nil
}

// Fail moves j to FAILED with a short, user-safe reason.
func (j *Job) Fail(reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("fail job %s: %w", j.JobID, ErrMissingReason)
	}
	if err := j.Status.ValidateTransition(StatusFailed); err != nil {
		return fmt.Errorf("job %s: %w", j.JobID, err)
	}
	j.Status = StatusFailed
	j.FailureReason = reason
	j.UpdatedAt = now.UTC()
	return nil
}

// WorkflowInput returns the payload passed to the state machine for j.
func (j Job) WorkflowInput() WorkflowInput {
	return WorkflowInput{
		JobID:     j.JobID,
		UserID:    j.UserID,
		DocType:   j.DocType,
		UploadKey: j.UploadKey,
	}
}

// WorkflowInput is the payload Step Functions carries between states: IDs and
// S3 keys, never file contents.
type WorkflowInput struct {
	JobID     string  `json:"jobId"`
	UserID    string  `json:"userId"`
	DocType   DocType `json:"docType"`
	UploadKey string  `json:"uploadKey"`
}

// Validate reports whether w carries everything a workflow state needs.
func (w WorkflowInput) Validate() error {
	if err := validateUserID(w.UserID); err != nil {
		return err
	}
	if err := validateJobID(w.JobID); err != nil {
		return err
	}
	if !w.DocType.Valid() {
		return fmt.Errorf("document type %q: %w", w.DocType, ErrInvalidDocType)
	}
	if w.UploadKey == "" {
		return errors.New("upload key is empty")
	}
	return nil
}

// validateUserID rejects identifiers that are empty, oversized, or that carry
// the separators used by the single-table key layout.
func validateUserID(userID string) error {
	switch {
	case userID == "":
		return fmt.Errorf("user id is empty: %w", ErrInvalidUserID)
	case len(userID) > maxIDLen:
		return fmt.Errorf("user id is %d bytes, limit %d: %w", len(userID), maxIDLen, ErrInvalidUserID)
	case strings.ContainsAny(userID, "#/"):
		return fmt.Errorf("user id %q contains a key separator: %w", userID, ErrInvalidUserID)
	default:
		return nil
	}
}

func validateJobID(jobID string) error {
	if !ValidJobID(jobID) {
		return fmt.Errorf("job id %q is not a ULID: %w", jobID, ErrInvalidJobID)
	}
	return nil
}

// ulidLen is the length of a ULID in Crockford base32.
const ulidLen = 26

// crockfordAlphabet is Crockford base32: the digits and the uppercase letters
// except I, L, O, and U.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ValidJobID reports whether id is a canonical uppercase ULID. Job IDs sort by
// creation time, so the store relies on their shape for range queries.
func ValidJobID(id string) bool {
	if len(id) != ulidLen {
		return false
	}
	// A ULID's first character encodes the top 3 bits of a 48-bit timestamp, so
	// anything above '7' overflows.
	if id[0] > '7' {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(crockfordAlphabet, rune(id[i])) {
			return false
		}
	}
	return true
}
