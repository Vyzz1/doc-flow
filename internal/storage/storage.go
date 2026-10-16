// Package storage manages document uploads and artifacts in S3.
//
// Clients never talk to the pipeline's bucket with credentials. They get a
// presigned upload that expires, names one key, and carries the size and
// content-type limits as part of the signed policy, so S3 enforces them
// before a byte reaches the pipeline.
package storage

import (
	"context"
	"time"
)

// Upload is everything a client needs to send one document.
//
// It describes a presigned HTTP POST rather than a PUT, because only POST
// carries a policy, and only a policy can bound the body size. The client
// sends multipart/form-data: every pair in Fields first, then the file.
type Upload struct {
	// URL is the bucket endpoint the client posts to.
	URL string
	// Fields are the form fields that must precede the file, including the
	// signed policy. Treat them as opaque.
	Fields map[string]string
	// Key is where the object will land, for the job record.
	Key string
	// MaxBytes is the size S3 will refuse to exceed.
	MaxBytes int64
	// ExpiresAt is when the signature stops working.
	ExpiresAt time.Time
}

// Storage hands out presigned uploads. Later steps will extend this with
// artifact reads and writes; a handler takes the interface so tests can fake
// it without an S3.
type Storage interface {
	// PresignUpload returns an upload for key, limited to contentType and
	// maxBytes, valid for ttl.
	PresignUpload(ctx context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (Upload, error)
}
