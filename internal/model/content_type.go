package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MaxUploadBytes caps an upload at 10 MiB. It is a policy choice, not a
// technical limit: documents this pipeline handles are far smaller, and a cap
// keeps one caller from spending the project's S3 and OCR budget. The
// presigned upload carries it as a condition, so S3 rejects an oversized body
// before it costs anything.
const MaxUploadBytes int64 = 10 << 20

// ErrUnsupportedContentType means the declared type is not one the document
// kind accepts.
var ErrUnsupportedContentType = errors.New("unsupported content type")

// contentTypes lists what each document kind accepts. This is the type the
// client declares at create time; the validate step still checks the stored
// object's magic bytes, because a declared type proves nothing.
var contentTypes = map[DocType][]string{
	DocInvoice: {"application/pdf", "image/jpeg", "image/png", "image/tiff"},
	DocID:      {"image/jpeg", "image/png"},
	DocImage:   {"image/jpeg", "image/png", "image/tiff"},
}

// ContentTypes returns the content types d accepts, for an error message or a
// client-facing capability list.
func (d DocType) ContentTypes() []string {
	return slices.Clone(contentTypes[d])
}

// NormalizeContentType lowercases a content type and drops any parameters, so
// "IMAGE/JPEG; charset=binary" compares equal to "image/jpeg".
func NormalizeContentType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// AcceptsContentType reports whether d accepts contentType.
func (d DocType) AcceptsContentType(contentType string) bool {
	return slices.Contains(contentTypes[d], NormalizeContentType(contentType))
}

// ValidateContentType returns nil when d accepts contentType, and an error
// wrapping ErrUnsupportedContentType otherwise.
func (d DocType) ValidateContentType(contentType string) error {
	if !d.Valid() {
		return fmt.Errorf("document type %q: %w", d, ErrInvalidDocType)
	}
	if !d.AcceptsContentType(contentType) {
		return fmt.Errorf("content type %q for %s, want one of %s: %w",
			contentType, d, strings.Join(d.ContentTypes(), ", "), ErrUnsupportedContentType)
	}
	return nil
}
