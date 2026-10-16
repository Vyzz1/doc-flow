//go:build integration

// These run against S3Mock, started by `make up`.
//
// They exist for the parts of the S3 contract this code leans on and a fake
// can only echo back: that a ranged read returns the bytes the range names,
// that a missing object reports as missing, and that a presigned POST can
// actually be posted to.
package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// liveBucket is created by the tests themselves.
const liveBucket = "docflow-test"

func newLiveStorage(t *testing.T) (*S3, *s3.Client) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	if endpoint == "" {
		t.Skip("S3_ENDPOINT is not set; run make up")
	}

	cfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("ap-southeast-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
		// S3Mock answers a ranged read with the checksum of the whole
		// object, which the SDK then rejects. Real S3 omits the checksum
		// for a partial range instead, so this is a concession to the
		// stand-in; production keeps validation on, and whether that holds
		// against S3 is one more thing only a deployment shows.
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	createBucket(t, client)
	return NewS3(s3.NewPresignClient(client), liveBucket, WithClient(client)), client
}

// createBucket makes the bucket if it is not there. Doing it here rather
// than in the container's configuration keeps the tests from depending on
// an image setting that could change under them.
func createBucket(t *testing.T, client *s3.Client) {
	t.Helper()
	_, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(liveBucket)})
	if err == nil {
		return
	}
	var owned *types.BucketAlreadyOwnedByYou
	var exists *types.BucketAlreadyExists
	if errors.As(err, &owned) || errors.As(err, &exists) {
		return
	}
	t.Fatalf("create bucket %s: %v", liveBucket, err)
}

// putObject stores an object the tests then read back.
func putObject(t *testing.T, client *s3.Client, key string, body []byte) {
	t.Helper()
	if _, err := client.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(liveBucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func liveKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("uploads/user-1/%d", time.Now().UnixNano())
}

func TestLiveSize(t *testing.T) {
	storage, client := newLiveStorage(t)
	key := liveKey(t)
	body := bytes.Repeat([]byte("x"), 4096)
	putObject(t, client, key, body)

	got, err := storage.Size(t.Context(), key)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != int64(len(body)) {
		t.Errorf("Size = %d, want %d", got, len(body))
	}
}

// The ranged read is the one place an off-by-one would be silent: HTTP
// ranges are inclusive at both ends, so sixteen bytes is bytes=0-15.
func TestLiveReadHeaderReturnsExactlyTheRange(t *testing.T) {
	storage, client := newLiveStorage(t)
	key := liveKey(t)
	body := []byte("%PDF-1.7 followed by a great deal of content nobody needs here")
	putObject(t, client, key, body)

	for _, n := range []int64{1, 8, 16, int64(len(body))} {
		got, err := storage.ReadHeader(t.Context(), key, n)
		if err != nil {
			t.Fatalf("ReadHeader(%d): %v", n, err)
		}
		if int64(len(got)) != n {
			t.Errorf("ReadHeader(%d) returned %d bytes", n, len(got))
		}
		if !bytes.Equal(got, body[:n]) {
			t.Errorf("ReadHeader(%d) = %q, want %q", n, got, body[:n])
		}
	}
}

func TestLiveReadHeaderOfAShortObject(t *testing.T) {
	storage, client := newLiveStorage(t)
	key := liveKey(t)
	putObject(t, client, key, []byte("tiny"))

	got, err := storage.ReadHeader(t.Context(), key, 16)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if string(got) != "tiny" {
		t.Errorf("ReadHeader = %q, want the whole short object", got)
	}
}

func TestLiveRead(t *testing.T) {
	storage, client := newLiveStorage(t)
	key := liveKey(t)
	body := bytes.Repeat([]byte("content"), 1000)
	putObject(t, client, key, body)

	got, err := storage.Read(t.Context(), key)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("Read returned %d bytes, want %d", len(got), len(body))
	}
}

func TestLiveMissingObject(t *testing.T) {
	storage, _ := newLiveStorage(t)
	key := liveKey(t)

	if _, err := storage.Size(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Size of a missing object = %v, want ErrNotFound", err)
	}
	if _, err := storage.Read(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Read of a missing object = %v, want ErrNotFound", err)
	}
	if _, err := storage.ReadHeader(t.Context(), key, 16); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReadHeader of a missing object = %v, want ErrNotFound", err)
	}
}

// post sends a document the way a browser would: the signed fields first,
// then the file.
func post(t *testing.T, upload Upload, body []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for name, value := range upload.Fields {
		if err := form.WriteField(name, value); err != nil {
			t.Fatalf("write field %s: %v", name, err)
		}
	}
	file, err := form.CreateFormFile("file", "document.pdf")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := file.Write(body); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upload.URL, &buf)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() {
		//nolint:errcheck // nothing useful to do with a close failure here
		response.Body.Close()
	})
	return response
}

// A presigned POST has to be something a client can actually send. This
// checks the request is well formed and accepted; whether the policy's
// size condition is *enforced* is a property of the storage service, and
// S3Mock is not S3 — see the skip below.
func TestLivePresignedPostIsUsable(t *testing.T) {
	storage, client := newLiveStorage(t)
	key := liveKey(t)

	upload, err := storage.PresignUpload(t.Context(), key, "application/pdf", 1024, time.Minute)
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	if upload.URL == "" || len(upload.Fields) == 0 {
		t.Fatalf("Upload = %+v, want a URL and form fields", upload)
	}

	body := []byte("%PDF-1.7 a small document")
	response := post(t, upload, body)
	if response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		t.Fatalf("post returned %d: %s", response.StatusCode, detail)
	}

	stored, err := client.GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String(liveBucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("the posted object is not there: %v", err)
	}
	defer stored.Body.Close() //nolint:errcheck // test cleanup
	got, err := io.ReadAll(stored.Body)
	if err != nil {
		t.Fatalf("read the posted object: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("stored %q, want %q", got, body)
	}
}

// The size limit lives in the signed policy, and only the storage service
// can enforce it. S3Mock does not evaluate POST policies, so this records
// what it does rather than asserting what S3 would: the limit is covered by
// a unit test on the signed conditions, and only a real deployment proves
// it is applied.
func TestLivePolicyEnforcementIsNotProvenHere(t *testing.T) {
	storage, _ := newLiveStorage(t)
	key := liveKey(t)

	upload, err := storage.PresignUpload(t.Context(), key, "application/pdf", 16, time.Minute)
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}

	oversized := bytes.Repeat([]byte("x"), 4096)
	response := post(t, upload, oversized)
	switch {
	case response.StatusCode < 300:
		t.Logf("S3Mock accepted %d bytes against a 16 byte policy: it does not evaluate POST "+
			"conditions, so this records the gap rather than closing it", len(oversized))
	case response.StatusCode == http.StatusBadRequest, response.StatusCode == http.StatusForbidden:
		t.Logf("S3Mock rejected the oversized body with %d, which is what S3 would do", response.StatusCode)
	default:
		// Anything else means the request failed for a reason that has
		// nothing to do with the policy, and reading it as enforcement
		// would be exactly the wrong conclusion.
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		t.Fatalf("post returned %d, which says nothing about the policy: %s", response.StatusCode, detail)
	}
}
