// Package validation decides whether an uploaded object is a document this
// pipeline will process.
//
// It reads the object's leading bytes rather than trusting the content type
// the client declared at create time. A declared type is a hint the client
// chose; the bytes are what the OCR step will actually have to read.
//
// Every error here wraps ErrPermanent. A file that is not a PNG will not
// become one on the second attempt, so the workflow fails the job instead of
// retrying. Transient failures, such as S3 refusing the read, come from the
// caller and are retried there.
package validation

import (
	"bytes"
	"errors"
	"fmt"

	"docflow/internal/model"
)

// Kind is a format recognized by its leading bytes. The values match the
// content types the model accepts, so the two can be compared directly.
type Kind string

// The formats the pipeline recognizes.
const (
	KindUnknown Kind = ""
	KindJPEG    Kind = "image/jpeg"
	KindPNG     Kind = "image/png"
	KindTIFF    Kind = "image/tiff"
	KindPDF     Kind = "application/pdf"
)

// String makes Kind print as its bare value in log fields and errors.
func (k Kind) String() string { return string(k) }

// HeaderSize is how many leading bytes Detect needs. Reading exactly this
// many keeps the validate step from pulling a whole document into memory.
const HeaderSize = 16

// ErrPermanent marks a failure no retry can fix. The workflow matches it to
// decide between failing fast and backing off.
var ErrPermanent = errors.New("permanent")

// The reasons an object is rejected. Each one also wraps ErrPermanent.
var (
	ErrEmpty        = errors.New("file is empty")
	ErrTooLarge     = errors.New("file exceeds the size limit")
	ErrUnrecognized = errors.New("file format not recognized")
	ErrTypeMismatch = errors.New("file contents do not match the declared content type")
	ErrNotAccepted  = errors.New("file format not accepted for this document type")
)

// signature is one format's leading bytes.
type signature struct {
	kind   Kind
	prefix []byte
}

// signatures are checked in order. They are written out rather than taken
// from net/http's sniffer, which guesses a type for anything and falls back
// to text/plain or application/octet-stream; a validator has to be able to
// say no.
var signatures = []signature{
	{KindPNG, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}},
	{KindJPEG, []byte{0xFF, 0xD8, 0xFF}},
	// TIFF carries its byte order in the header: II for little-endian, MM for
	// big-endian, each followed by the number 42.
	{KindTIFF, []byte{'I', 'I', 0x2A, 0x00}},
	{KindTIFF, []byte{'M', 'M', 0x00, 0x2A}},
	// The PDF specification allows the header to sit anywhere in the first
	// kilobyte. Requiring it at the start rejects files that hide one format
	// behind another, which is worth more here than accepting an unusual PDF.
	{KindPDF, []byte("%PDF-")},
}

// Detect returns the format implied by the leading bytes of a file, or
// KindUnknown. Pass at least HeaderSize bytes, or the whole file when it is
// shorter.
func Detect(header []byte) Kind {
	for _, sig := range signatures {
		if bytes.HasPrefix(header, sig.prefix) {
			return sig.kind
		}
	}
	return KindUnknown
}

// Validate checks a stored object against the job that expects it, returning
// the format it really is.
//
// size is the object's full length, which the caller reads from S3 rather
// than from the client, and header is its first HeaderSize bytes.
func Validate(job model.Job, header []byte, size int64) (Kind, error) {
	switch {
	case size <= 0:
		return KindUnknown, permanent(ErrEmpty, "size %d", size)
	case size > model.MaxUploadBytes:
		return KindUnknown, permanent(ErrTooLarge, "size %d bytes, limit %d", size, model.MaxUploadBytes)
	}

	kind := Detect(header)
	if kind == KindUnknown {
		return KindUnknown, permanent(ErrUnrecognized, "leading bytes %x", clip(header))
	}

	// The declared type is only a hint, but a hint that disagrees with the
	// bytes means the client is confused or trying something.
	if declared := model.NormalizeContentType(job.ContentType); declared != "" && declared != string(kind) {
		return kind, permanent(ErrTypeMismatch, "declared %s, found %s", declared, kind)
	}

	if !job.DocType.AcceptsContentType(string(kind)) {
		return kind, permanent(ErrNotAccepted, "%s is not accepted for %s", kind, job.DocType)
	}

	return kind, nil
}

// permanent wraps a reason so callers can match both the specific cause and
// the fact that retrying is pointless.
func permanent(reason error, format string, args ...any) error {
	return fmt.Errorf("%s: %w: %w", fmt.Sprintf(format, args...), reason, ErrPermanent)
}

// clip keeps an error message short when the header is long.
func clip(header []byte) []byte {
	if len(header) > 8 {
		return header[:8]
	}
	return header
}
