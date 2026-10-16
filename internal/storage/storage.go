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

// Storage hands out presigned uploads. A handler takes the interface so
// tests can fake it without an S3.
type Storage interface {
	// PresignUpload returns an upload for key, limited to contentType and
	// maxBytes, valid for ttl.
	PresignUpload(ctx context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (Upload, error)
}

// Documents reads what clients uploaded. It is separate from Storage because
// the steps that read an object never hand out an upload, and the two have
// different permissions.
type Documents interface {
	// Size returns an object's length without transferring it.
	Size(ctx context.Context, key string) (int64, error)

	// ReadHeader returns an object's first n bytes. The validate step needs
	// only a handful, and a ranged read keeps a ten megabyte scan out of a
	// Lambda that has no use for it.
	ReadHeader(ctx context.Context, key string, n int64) ([]byte, error)

	// Read returns a whole object. Callers are responsible for knowing it
	// fits: the upload policy caps one at model.MaxUploadBytes.
	Read(ctx context.Context, key string) ([]byte, error)
}
