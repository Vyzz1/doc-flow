package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// defaultTTL is how long a presigned upload stays valid when a caller does
// not say. Long enough for a slow mobile upload, short enough that a leaked
// URL stops working the same hour.
const defaultTTL = 15 * time.Minute

// Errors reported when an upload cannot be presigned.
var (
	ErrEmptyKey     = errors.New("object key is empty")
	ErrInvalidLimit = errors.New("upload size limit must be positive")
)

// PostPresigner is the one S3 call this package makes. Keeping it an
// interface means the handler tests never reach the network.
type PostPresigner interface {
	PresignPostObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.PresignPostOptions)) (*s3.PresignedPostRequest, error)
}

// S3 presigns uploads into one bucket.
type S3 struct {
	presigner PostPresigner
	bucket    string
	now       func() time.Time
}

// S3Option configures an S3.
type S3Option func(*S3)

// WithClock replaces the clock used to compute ExpiresAt, so tests do not
// depend on the wall clock.
func WithClock(now func() time.Time) S3Option {
	return func(s *S3) { s.now = now }
}

// NewS3 returns storage backed by bucket.
func NewS3(presigner PostPresigner, bucket string, opts ...S3Option) *S3 {
	s := &S3{presigner: presigner, bucket: bucket, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewS3FromEnv builds storage from UPLOAD_BUCKET and the ambient AWS
// configuration. Call it during Lambda initialization, not inside a handler,
// so the client is reused across warm invocations.
func NewS3FromEnv(ctx context.Context, opts ...S3Option) (*S3, error) {
	bucket := strings.TrimSpace(os.Getenv("UPLOAD_BUCKET"))
	if bucket == "" {
		return nil, errors.New("UPLOAD_BUCKET is not set")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT")); endpoint != "" {
			o.BaseEndpoint = &endpoint
			// MinIO and other local S3s do not serve virtual-hosted buckets.
			o.UsePathStyle = true
		}
	})
	return NewS3(s3.NewPresignClient(client), bucket, opts...), nil
}

var _ Storage = (*S3)(nil)

// PresignUpload returns a presigned POST limited to contentType and maxBytes.
//
// Both limits travel inside the signed policy, so S3 rejects a mismatched
// type or an oversized body itself. The validate step still inspects the
// stored object's magic bytes: a content type the client declared proves
// nothing about what it sent.
func (s *S3) PresignUpload(ctx context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (Upload, error) {
	if strings.TrimSpace(key) == "" {
		return Upload{}, fmt.Errorf("presign upload: %w", ErrEmptyKey)
	}
	if maxBytes <= 0 {
		return Upload{}, fmt.Errorf("presign upload for %s: %w", key, ErrInvalidLimit)
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}

	out, err := s.presigner.PresignPostObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		ContentType: &contentType,
	}, func(o *s3.PresignPostOptions) {
		o.Expires = ttl
		o.Conditions = []any{
			// An empty object is never a document, and the upper bound is
			// what keeps one caller from spending the project's budget.
			[]any{"content-length-range", 1, maxBytes},
		}
	})
	if err != nil {
		return Upload{}, fmt.Errorf("presign upload for %s: %w", key, err)
	}

	return Upload{
		URL:       out.URL,
		Fields:    out.Values,
		Key:       key,
		MaxBytes:  maxBytes,
		ExpiresAt: s.now().UTC().Add(ttl),
	}, nil
}
