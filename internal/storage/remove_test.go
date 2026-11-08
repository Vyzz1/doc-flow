package storage

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// removeAPI serves listing pages in order and records every delete.
type removeAPI struct {
	fakeAPI

	pages [][]string // keys per listing page; every page but the last is truncated

	deleteKey     *s3.DeleteObjectInput
	listed        []*s3.ListObjectsV2Input
	deletedByPage [][]string

	deleteErr  error
	listErr    error
	batchErr   error
	refuseKeys []types.Error
}

func (r *removeAPI) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	r.deleteKey = in
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	return &s3.DeleteObjectOutput{}, nil
}

func (r *removeAPI) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	r.listed = append(r.listed, in)
	if r.listErr != nil {
		return nil, r.listErr
	}
	if len(r.pages) == 0 {
		return &s3.ListObjectsV2Output{}, nil
	}
	index := len(r.listed) - 1
	out := &s3.ListObjectsV2Output{}
	for _, key := range r.pages[index] {
		out.Contents = append(out.Contents, types.Object{Key: aws.String(key)})
	}
	if index < len(r.pages)-1 {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String("page-token")
	}
	return out, nil
}

func (r *removeAPI) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if r.batchErr != nil {
		return nil, r.batchErr
	}
	var keys []string
	for _, object := range in.Delete.Objects {
		keys = append(keys, *object.Key)
	}
	r.deletedByPage = append(r.deletedByPage, keys)
	return &s3.DeleteObjectsOutput{Errors: r.refuseKeys}, nil
}

func newRemover(f *removeAPI) *S3 {
	return NewS3(&fakePresigner{}, testBucket, WithClient(f))
}

func TestDelete(t *testing.T) {
	f := &removeAPI{}

	if err := newRemover(f).Delete(context.Background(), testKey); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if f.deleteKey == nil || *f.deleteKey.Key != testKey || *f.deleteKey.Bucket != testBucket {
		t.Errorf("DeleteObject got %+v, want %s in %s", f.deleteKey, testKey, testBucket)
	}
}

func TestDeleteRefusesAnEmptyKey(t *testing.T) {
	f := &removeAPI{}

	if err := newRemover(f).Delete(context.Background(), "  "); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Delete() error = %v, want ErrEmptyKey", err)
	}
	if f.deleteKey != nil {
		t.Error("DeleteObject was called for an empty key")
	}
}

func TestDeleteWrapsTheClientError(t *testing.T) {
	sentinel := errors.New("access denied")
	f := &removeAPI{deleteErr: sentinel}

	if err := newRemover(f).Delete(context.Background(), testKey); !errors.Is(err, sentinel) {
		t.Errorf("Delete() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestDeletePrefix(t *testing.T) {
	const prefix = "derived/user-1/job-1/"
	f := &removeAPI{pages: [][]string{
		{prefix + "page.png", prefix + "thumbnail.jpg"},
		{prefix + "text.txt"},
	}}

	if err := newRemover(f).DeletePrefix(context.Background(), prefix); err != nil {
		t.Fatalf("DeletePrefix() error = %v", err)
	}

	if len(f.listed) != 2 {
		t.Fatalf("listed %d pages, want 2", len(f.listed))
	}
	for _, in := range f.listed {
		if *in.Prefix != prefix {
			t.Errorf("listed prefix %q, want %q", *in.Prefix, prefix)
		}
	}
	if f.listed[0].ContinuationToken != nil || *f.listed[1].ContinuationToken != "page-token" {
		t.Errorf("continuation tokens = %v, %v, want none then page-token",
			f.listed[0].ContinuationToken, f.listed[1].ContinuationToken)
	}
	want := [][]string{
		{prefix + "page.png", prefix + "thumbnail.jpg"},
		{prefix + "text.txt"},
	}
	if !slices.EqualFunc(f.deletedByPage, want, slices.Equal) {
		t.Errorf("deleted %v, want %v", f.deletedByPage, want)
	}
}

func TestDeletePrefixWithNothingThereDeletesNothing(t *testing.T) {
	f := &removeAPI{}

	if err := newRemover(f).DeletePrefix(context.Background(), "derived/user-1/job-1/"); err != nil {
		t.Fatalf("DeletePrefix() error = %v", err)
	}
	if len(f.deletedByPage) != 0 {
		t.Errorf("DeleteObjects was called %d times for an empty listing", len(f.deletedByPage))
	}
}

func TestDeletePrefixRefusesAPrefixThatCouldEmptyTheBucket(t *testing.T) {
	// Each of these would match far more than one job's files.
	for _, prefix := range []string{"", "/", "//", "derived", "derived/user-1/job-1"} {
		t.Run(prefix, func(t *testing.T) {
			f := &removeAPI{}

			err := newRemover(f).DeletePrefix(context.Background(), prefix)
			if !errors.Is(err, ErrInvalidPrefix) {
				t.Errorf("DeletePrefix(%q) error = %v, want ErrInvalidPrefix", prefix, err)
			}
			if len(f.listed) != 0 {
				t.Errorf("DeletePrefix(%q) listed the bucket anyway", prefix)
			}
		})
	}
}

func TestDeletePrefixFailures(t *testing.T) {
	const prefix = "derived/user-1/job-1/"
	sentinel := errors.New("throttled")

	tests := []struct {
		name string
		api  *removeAPI
		want error
	}{
		{name: "listing fails", api: &removeAPI{listErr: sentinel}, want: sentinel},
		{name: "batch delete fails", api: &removeAPI{pages: [][]string{{prefix + "a"}}, batchErr: sentinel}, want: sentinel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := newRemover(tt.api).DeletePrefix(context.Background(), prefix); !errors.Is(err, tt.want) {
				t.Errorf("DeletePrefix() error = %v, want it to wrap %v", err, tt.want)
			}
		})
	}

	t.Run("a refused key fails the call even when S3 answered", func(t *testing.T) {
		f := &removeAPI{
			pages:      [][]string{{prefix + "a"}},
			refuseKeys: []types.Error{{Key: aws.String(prefix + "a"), Code: aws.String("AccessDenied")}},
		}
		if err := newRemover(f).DeletePrefix(context.Background(), prefix); err == nil {
			t.Error("DeletePrefix() = nil, want an error for the refused key")
		}
	})
}
