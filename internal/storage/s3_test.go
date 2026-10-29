package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	testBucket = "docflow-uploads"
	testKey    = "uploads/user-1/01J8ZQJ9X7K3M4N5P6Q7R8S9TV"
)

var testNow = time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

// fakePresigner records the request and replays a canned response.
type fakePresigner struct {
	params  *s3.PutObjectInput
	options *s3.PresignPostOptions

	out *s3.PresignedPostRequest
	err error
}

func (f *fakePresigner) PresignPostObject(_ context.Context, params *s3.PutObjectInput, optFns ...func(*s3.PresignPostOptions)) (*s3.PresignedPostRequest, error) {
	f.params = params
	f.options = &s3.PresignPostOptions{}
	for _, fn := range optFns {
		fn(f.options)
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.out != nil {
		return f.out, nil
	}
	return &s3.PresignedPostRequest{
		URL:    "https://" + testBucket + ".s3.amazonaws.com",
		Values: map[string]string{"key": testKey, "policy": "signed", "x-amz-signature": "deadbeef"},
	}, nil
}

func newTestS3(f *fakePresigner) *S3 {
	return NewS3(f, testBucket, WithClock(func() time.Time { return testNow }))
}

// contentLengthRange pulls the size condition out of the signed policy.
func contentLengthRange(t *testing.T, opts *s3.PresignPostOptions) []any {
	t.Helper()
	for _, condition := range opts.Conditions {
		list, ok := condition.([]any)
		if ok && len(list) == 3 && list[0] == "content-length-range" {
			return list
		}
	}
	t.Fatalf("no content-length-range in %v", opts.Conditions)
	return nil
}

func TestPresignUpload(t *testing.T) {
	f := &fakePresigner{}

	got, err := newTestS3(f).PresignUpload(context.Background(), testKey, "image/jpeg", 1024, 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignUpload() error = %v", err)
	}

	if *f.params.Bucket != testBucket {
		t.Errorf("Bucket = %q, want %q", *f.params.Bucket, testBucket)
	}
	if *f.params.Key != testKey {
		t.Errorf("Key = %q, want %q", *f.params.Key, testKey)
	}
	if *f.params.ContentType != "image/jpeg" {
		t.Errorf("ContentType = %q, want image/jpeg", *f.params.ContentType)
	}
	if f.options.Expires != 10*time.Minute {
		t.Errorf("Expires = %s, want 10m", f.options.Expires)
	}

	// S3 must refuse an empty body and anything over the cap, rather than
	// the pipeline discovering it after paying to store the object.
	limits := contentLengthRange(t, f.options)
	if limits[1] != 1 {
		t.Errorf("minimum size = %v, want 1", limits[1])
	}
	if limits[2] != int64(1024) {
		t.Errorf("maximum size = %v, want 1024", limits[2])
	}

	if got.Key != testKey || got.MaxBytes != 1024 {
		t.Errorf("Upload = %+v, want key %q and 1024 bytes", got, testKey)
	}
	if want := testNow.Add(10 * time.Minute); !got.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}
	if got.Fields["policy"] == "" {
		t.Error("Upload carries no policy field, so a client cannot post")
	}

	// The SDK does not sign the content type itself. The policy has to say
	// so, and the form has to carry the field the policy compares against.
	var pinned bool
	for _, condition := range f.options.Conditions {
		if list, ok := condition.([]any); ok && len(list) == 3 &&
			list[0] == "eq" && list[1] == "$Content-Type" && list[2] == "image/jpeg" {
			pinned = true
		}
	}
	if !pinned {
		t.Errorf("conditions %v do not pin the content type to image/jpeg", f.options.Conditions)
	}
	if got.Fields["Content-Type"] != "image/jpeg" {
		t.Errorf("Fields[Content-Type] = %q, want image/jpeg", got.Fields["Content-Type"])
	}
}

func TestPresignUploadDefaultsTTL(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
	}{
		{name: "zero", ttl: 0},
		{name: "negative", ttl: -time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakePresigner{}
			got, err := newTestS3(f).PresignUpload(context.Background(), testKey, "image/png", 1024, tt.ttl)
			if err != nil {
				t.Fatalf("PresignUpload() error = %v", err)
			}
			if f.options.Expires != defaultTTL {
				t.Errorf("Expires = %s, want the %s default", f.options.Expires, defaultTTL)
			}
			if want := testNow.Add(defaultTTL); !got.ExpiresAt.Equal(want) {
				t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
			}
		})
	}
}

func TestPresignUploadRejectsBadInput(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		maxBytes int64
		wantErr  error
	}{
		{name: "empty key", key: "", maxBytes: 1024, wantErr: ErrEmptyKey},
		{name: "blank key", key: "   ", maxBytes: 1024, wantErr: ErrEmptyKey},
		{name: "zero limit", key: testKey, maxBytes: 0, wantErr: ErrInvalidLimit},
		{name: "negative limit", key: testKey, maxBytes: -1, wantErr: ErrInvalidLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakePresigner{}
			_, err := newTestS3(f).PresignUpload(context.Background(), tt.key, "image/png", tt.maxBytes, time.Minute)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("PresignUpload() error = %v, want %v", err, tt.wantErr)
			}
			if f.params != nil {
				t.Error("PresignUpload() called S3 with invalid input")
			}
		})
	}
}

func TestPresignUploadWrapsFailures(t *testing.T) {
	sentinel := errors.New("no credentials")
	f := &fakePresigner{err: sentinel}

	_, err := newTestS3(f).PresignUpload(context.Background(), testKey, "image/png", 1024, time.Minute)
	if !errors.Is(err, sentinel) {
		t.Fatalf("PresignUpload() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestNewS3FromEnvNeedsABucket(t *testing.T) {
	t.Setenv("UPLOAD_BUCKET", "  ")
	if _, err := NewS3FromEnv(context.Background()); err == nil {
		t.Fatal("NewS3FromEnv() = nil without UPLOAD_BUCKET, want an error")
	}
}

func TestNewS3FromEnv(t *testing.T) {
	t.Setenv("UPLOAD_BUCKET", testBucket)
	t.Setenv("AWS_REGION", "ap-southeast-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:9000")

	s, err := NewS3FromEnv(context.Background())
	if err != nil {
		t.Fatalf("NewS3FromEnv() error = %v", err)
	}
	if s.bucket != testBucket {
		t.Errorf("bucket = %q, want %q", s.bucket, testBucket)
	}
	if s.presigner == nil {
		t.Error("NewS3FromEnv() built storage with no presigner")
	}
}
