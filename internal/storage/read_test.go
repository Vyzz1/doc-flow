package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"docflow/internal/model"
)

// fakeAPI serves one object and records what was asked for.
type fakeAPI struct {
	body []byte

	get  *s3.GetObjectInput
	head *s3.HeadObjectInput

	getErr        error
	headErr       error
	noContentSize bool
}

func (f *fakeAPI) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.get = in
	if f.getErr != nil {
		return nil, f.getErr
	}
	body := f.body
	// Honour the range the caller asked for, the way S3 would.
	if in.Range != nil {
		var first, last int
		if _, err := fmt.Sscanf(*in.Range, "bytes=%d-%d", &first, &last); err == nil {
			if last >= len(body) {
				last = len(body) - 1
			}
			if first <= last {
				body = body[first : last+1]
			}
		}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (f *fakeAPI) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.head = in
	if f.headErr != nil {
		return nil, f.headErr
	}
	if f.noContentSize {
		return &s3.HeadObjectOutput{}, nil
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(int64(len(f.body)))}, nil
}

func (f *fakeAPI) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return &s3.PutObjectOutput{}, nil
}

func newReader(f *fakeAPI) *S3 {
	return NewS3(&fakePresigner{}, testBucket, WithClient(f))
}

func TestSize(t *testing.T) {
	f := &fakeAPI{body: bytes.Repeat([]byte("x"), 1234)}

	got, err := newReader(f).Size(context.Background(), testKey)
	if err != nil {
		t.Fatalf("Size() error = %v", err)
	}
	if got != 1234 {
		t.Errorf("Size() = %d, want 1234", got)
	}
	if *f.head.Key != testKey || *f.head.Bucket != testBucket {
		t.Errorf("head asked for %s/%s", *f.head.Bucket, *f.head.Key)
	}
}

func TestSizeWithoutAContentLength(t *testing.T) {
	f := &fakeAPI{noContentSize: true}

	if _, err := newReader(f).Size(context.Background(), testKey); err == nil {
		t.Error("Size() = nil for a response with no length, want an error")
	}
}

// S3 reports a missing object differently depending on the call, and the
// caller should not have to know which.
func TestMissingObjectIsOneError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "NoSuchKey", err: &types.NoSuchKey{}},
		{name: "NotFound", err: &types.NotFound{}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" on head", func(t *testing.T) {
			f := &fakeAPI{headErr: tt.err}
			if _, err := newReader(f).Size(context.Background(), testKey); !errors.Is(err, ErrNotFound) {
				t.Errorf("Size() error = %v, want ErrNotFound", err)
			}
		})
		t.Run(tt.name+" on get", func(t *testing.T) {
			f := &fakeAPI{getErr: tt.err}
			if _, err := newReader(f).Read(context.Background(), testKey); !errors.Is(err, ErrNotFound) {
				t.Errorf("Read() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestOtherFailuresAreNotMistakenForMissing(t *testing.T) {
	sentinel := errors.New("access denied")

	f := &fakeAPI{headErr: sentinel}
	_, err := newReader(f).Size(context.Background(), testKey)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Size() error = %v, want it to wrap %v", err, sentinel)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("a permission failure was reported as a missing object")
	}
}

func TestReadHeaderAsksForARange(t *testing.T) {
	f := &fakeAPI{body: []byte("%PDF-1.7 and then a great deal more content")}

	got, err := newReader(f).ReadHeader(context.Background(), testKey, 8)
	if err != nil {
		t.Fatalf("ReadHeader() error = %v", err)
	}
	if string(got) != "%PDF-1.7" {
		t.Errorf("ReadHeader() = %q, want the first eight bytes", got)
	}
	// Ranges are inclusive on both ends, so eight bytes is 0-7. Getting
	// this wrong would read one byte too many on every validation.
	if f.get.Range == nil || *f.get.Range != "bytes=0-7" {
		t.Errorf("Range = %v, want bytes=0-7", f.get.Range)
	}
}

func TestReadHeaderOfAShortObject(t *testing.T) {
	f := &fakeAPI{body: []byte("tiny")}

	got, err := newReader(f).ReadHeader(context.Background(), testKey, 16)
	if err != nil {
		t.Fatalf("ReadHeader() error = %v", err)
	}
	if string(got) != "tiny" {
		t.Errorf("ReadHeader() = %q, want the whole short object", got)
	}
}

func TestReadHeaderRejectsAnEmptyRange(t *testing.T) {
	for _, n := range []int64{0, -1} {
		f := &fakeAPI{body: []byte("content")}
		if _, err := newReader(f).ReadHeader(context.Background(), testKey, n); err == nil {
			t.Errorf("ReadHeader(%d) = nil, want an error", n)
		}
		if f.get != nil {
			t.Errorf("ReadHeader(%d) called S3 anyway", n)
		}
	}
}

func TestRead(t *testing.T) {
	f := &fakeAPI{body: []byte("the whole document")}

	got, err := newReader(f).Read(context.Background(), testKey)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(got) != "the whole document" {
		t.Errorf("Read() = %q", got)
	}
	if f.get.Range != nil {
		t.Errorf("Read() asked for a range: %v", *f.get.Range)
	}
}

// The upload policy caps an object, but this is the belt to that policy's
// braces: an object that somehow grew larger must not exhaust the Lambda.
func TestReadStopsAtTheUploadLimit(t *testing.T) {
	f := &fakeAPI{body: bytes.Repeat([]byte("x"), int(model.MaxUploadBytes)+4096)}

	got, err := newReader(f).Read(context.Background(), testKey)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if int64(len(got)) != model.MaxUploadBytes {
		t.Errorf("Read() returned %d bytes, want it capped at %d", len(got), model.MaxUploadBytes)
	}
}

func TestReadReportsABrokenBody(t *testing.T) {
	f := &brokenBodyAPI{}
	s := NewS3(&fakePresigner{}, testBucket, WithClient(f))

	if _, err := s.Read(context.Background(), testKey); err == nil {
		t.Error("Read() = nil for a body that fails mid-stream, want an error")
	}
}

// brokenBodyAPI returns a body that fails partway through.
type brokenBodyAPI struct{ fakeAPI }

func (f *brokenBodyAPI) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(io.MultiReader(
		strings.NewReader("partial"),
		&failingReader{},
	))}, nil
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestWrite(t *testing.T) {
	f := &writeAPI{}
	s := NewS3(&fakePresigner{}, testBucket, WithClient(f))

	body := []byte("page bytes")
	if err := s.Write(context.Background(), "derived/u/j/page.png", "image/png", body); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if f.put == nil {
		t.Fatal("Write() sent no PutObject")
	}
	if *f.put.Key != "derived/u/j/page.png" || *f.put.Bucket != testBucket {
		t.Errorf("wrote to %s/%s", *f.put.Bucket, *f.put.Key)
	}
	// The content type is recorded so a browser fetching a thumbnail
	// renders it rather than downloading it.
	if f.put.ContentType == nil || *f.put.ContentType != "image/png" {
		t.Errorf("ContentType = %v, want image/png", f.put.ContentType)
	}
	// The length is known, so it is sent rather than letting the SDK buffer
	// the body to discover it.
	if f.put.ContentLength == nil || *f.put.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %v, want %d", f.put.ContentLength, len(body))
	}
	if string(f.body) != string(body) {
		t.Errorf("stored %q, want %q", f.body, body)
	}
}

func TestWriteRejectsAnEmptyKey(t *testing.T) {
	for _, key := range []string{"", "   "} {
		f := &writeAPI{}
		s := NewS3(&fakePresigner{}, testBucket, WithClient(f))

		if err := s.Write(context.Background(), key, "image/png", []byte("x")); !errors.Is(err, ErrEmptyKey) {
			t.Errorf("Write(%q) error = %v, want ErrEmptyKey", key, err)
		}
		if f.put != nil {
			t.Errorf("Write(%q) called S3 anyway", key)
		}
	}
}

func TestWriteWrapsFailures(t *testing.T) {
	sentinel := errors.New("access denied")
	f := &writeAPI{err: sentinel}
	s := NewS3(&fakePresigner{}, testBucket, WithClient(f))

	if err := s.Write(context.Background(), "derived/u/j/page.png", "image/png", []byte("x")); !errors.Is(err, sentinel) {
		t.Errorf("Write() error = %v, want it to wrap %v", err, sentinel)
	}
}

// writeAPI captures a PutObject and its body.
type writeAPI struct {
	fakeAPI

	put  *s3.PutObjectInput
	body []byte
	err  error
}

func (w *writeAPI) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	w.put = in
	if w.err != nil {
		return nil, w.err
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	w.body = body
	return &s3.PutObjectOutput{}, nil
}
