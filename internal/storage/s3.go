package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"docflow/internal/model"
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

// PostPresigner signs an upload. Keeping it an interface means the handler
// tests never reach the network.
type PostPresigner interface {
	PresignPostObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.PresignPostOptions)) (*s3.PresignedPostRequest, error)
}

// API is the slice of the S3 client this package reads with.
type API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3 presigns uploads into one bucket and reads objects back out of it.
type S3 struct {
	presigner PostPresigner
	client    API
	bucket    string
	now       func() time.Time
}

// WithClient gives the storage a reader. NewS3FromEnv sets it; NewS3 takes
// only a presigner, so a test that signs uploads needs nothing more.
func WithClient(client API) S3Option {
	return func(s *S3) { s.client = client }
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
	return NewS3(s3.NewPresignClient(client), bucket, append([]S3Option{WithClient(client)}, opts...)...), nil
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

// ErrNotFound means the key holds no object.
var ErrNotFound = errors.New("object not found")

var _ Documents = (*S3)(nil)

// Size returns an object's length.
func (s *S3) Size(ctx context.Context, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		return 0, fmt.Errorf("head %s: %w", key, translateNotFound(err))
	}
	if out.ContentLength == nil {
		return 0, fmt.Errorf("head %s: no content length", key)
	}
	return *out.ContentLength, nil
}

// ReadHeader returns an object's first n bytes, using a ranged read so the
// rest never crosses the network.
func (s *S3) ReadHeader(ctx context.Context, key string, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("read header of %s: %d bytes requested", key, n)
	}
	// HTTP ranges are inclusive on both ends.
	return s.read(ctx, key, aws.String(fmt.Sprintf("bytes=0-%d", n-1)))
}

// Read returns a whole object.
func (s *S3) Read(ctx context.Context, key string) ([]byte, error) {
	return s.read(ctx, key, nil)
}

func (s *S3) read(ctx context.Context, key string, byteRange *string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
		Range:  byteRange,
	})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, translateNotFound(err))
	}
	defer out.Body.Close() //nolint:errcheck // nothing useful to do with a close failure on a read

	// A ranged read is already bounded; a whole-object read is bounded by
	// the upload policy, and this is the belt to that policy's braces.
	body, err := io.ReadAll(io.LimitReader(out.Body, model.MaxUploadBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return body, nil
}

// translateNotFound turns S3's two different missing-object errors into one
// sentinel, so callers do not have to know which API they came from.
func translateNotFound(err error) error {
	if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
		return ErrNotFound
	}
	if _, ok := errors.AsType[*types.NotFound](err); ok {
		return ErrNotFound
	}
	return err
}

var _ Artifacts = (*S3)(nil)

// Write stores a derived artifact.
func (s *S3) Write(ctx context.Context, key, contentType string, body []byte) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("write artifact: %w", ErrEmptyKey)
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(body),
		ContentType: &contentType,
		// The object's length is known, so send it rather than letting the
		// SDK buffer to discover it.
		ContentLength: aws.Int64(int64(len(body))),
	}); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}
