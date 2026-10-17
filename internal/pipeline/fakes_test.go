package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sfn"

	"docflow/internal/model"
	"docflow/internal/ocr"
	"docflow/internal/storage"
	"docflow/internal/store"
)

const (
	testUser  = "user-1"
	testJobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TV"
	testARN   = "arn:aws:states:ap-southeast-1:123456789012:stateMachine:DocFlow"
)

var testNow = time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

func testUploadKey() string { return model.UploadKey(testUser, testJobID) }

// fakeStore is a JobStore whose every method can be told what to return.
type fakeStore struct {
	job    model.Job
	steps  []model.StepResult
	result model.DocumentResult

	statusCalls []statusCall
	failCalls   []failCall

	failures map[string]error
}

type statusCall struct {
	from model.JobStatus
	to   model.JobStatus
}

type failCall struct {
	reason string
}

func newFakeStore(t interface{ Helper() }) *fakeStore {
	t.Helper()
	job, err := model.NewJob(testUser, testJobID, model.DocInvoice, testNow)
	if err != nil {
		panic(err)
	}
	job.ContentType = "application/pdf"
	return &fakeStore{job: job, failures: map[string]error{}}
}

func (f *fakeStore) fail(method string, err error) *fakeStore {
	f.failures[method] = err
	return f
}

func (f *fakeStore) CreateJob(context.Context, model.Job) error { return f.failures["CreateJob"] }

func (f *fakeStore) GetJob(context.Context, string, string) (model.Job, error) {
	if err := f.failures["GetJob"]; err != nil {
		return model.Job{}, err
	}
	return f.job, nil
}

func (f *fakeStore) ListJobs(context.Context, string, int32, string) ([]model.Job, string, error) {
	return nil, "", f.failures["ListJobs"]
}

func (f *fakeStore) UpdateStatus(_ context.Context, _, _ string, from, to model.JobStatus, _ time.Time) error {
	f.statusCalls = append(f.statusCalls, statusCall{from: from, to: to})
	return f.failures["UpdateStatus"]
}

func (f *fakeStore) FailJob(_ context.Context, _, _, reason string, _ time.Time) error {
	f.failCalls = append(f.failCalls, failCall{reason: reason})
	return f.failures["FailJob"]
}

func (f *fakeStore) PutStepResult(_ context.Context, result model.StepResult) error {
	if err := f.failures["PutStepResult"]; err != nil {
		return err
	}
	f.steps = append(f.steps, result)
	return nil
}

func (f *fakeStore) ListStepResults(context.Context, string) ([]model.StepResult, error) {
	return f.steps, f.failures["ListStepResults"]
}

func (f *fakeStore) PutResult(_ context.Context, result model.DocumentResult) error {
	if err := f.failures["PutResult"]; err != nil {
		return err
	}
	f.result = result
	return nil
}

func (f *fakeStore) GetResult(context.Context, string) (model.DocumentResult, error) {
	return model.DocumentResult{}, f.failures["GetResult"]
}

var _ store.JobStore = (*fakeStore)(nil)

// fakeDocuments serves one object's bytes.
type fakeDocuments struct {
	body []byte

	sizeErr   error
	headerErr error
	readErr   error

	headerBytes int64
}

func (f *fakeDocuments) Size(context.Context, string) (int64, error) {
	if f.sizeErr != nil {
		return 0, f.sizeErr
	}
	return int64(len(f.body)), nil
}

func (f *fakeDocuments) ReadHeader(_ context.Context, _ string, n int64) ([]byte, error) {
	f.headerBytes = n
	if f.headerErr != nil {
		return nil, f.headerErr
	}
	if int64(len(f.body)) < n {
		return f.body, nil
	}
	return f.body[:n], nil
}

func (f *fakeDocuments) Read(context.Context, string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.body, nil
}

var _ storage.Documents = (*fakeDocuments)(nil)

// fakeExecutions records what the starter launched.
type fakeExecutions struct {
	inputs []*sfn.StartExecutionInput
	err    error
}

func (f *fakeExecutions) StartExecution(_ context.Context, in *sfn.StartExecutionInput, _ ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error) {
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	return &sfn.StartExecutionOutput{}, nil
}

var _ Executions = (*fakeExecutions)(nil)

// failingExecutions fails one call out of a batch, so a test can check that
// the others still succeed.
type failingExecutions struct {
	inner  *fakeExecutions
	failOn int
	calls  *int
}

func (f *failingExecutions) StartExecution(ctx context.Context, in *sfn.StartExecutionInput, optFns ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error) {
	*f.calls++
	if *f.calls == f.failOn {
		return nil, errTransient
	}
	return f.inner.StartExecution(ctx, in, optFns...)
}

var _ Executions = (*failingExecutions)(nil)

// errTransient stands in for a throttled dependency.
var errTransient = errors.New("throttled")

// fakeArtifacts records what the recognition step stored.
type fakeArtifacts struct {
	written map[string][]byte
	types   map[string]string
	err     error
	failOn  string
}

func newFakeArtifacts() *fakeArtifacts {
	return &fakeArtifacts{written: map[string][]byte{}, types: map[string]string{}}
}

func (f *fakeArtifacts) Write(_ context.Context, key, contentType string, body []byte) error {
	if f.err != nil && (f.failOn == "" || strings.HasSuffix(key, f.failOn)) {
		return f.err
	}
	f.written[key] = body
	f.types[key] = contentType
	return nil
}

var _ storage.Artifacts = (*fakeArtifacts)(nil)

// fakeRasterizer writes one PNG per page it is told to produce.
type fakeRasterizer struct {
	pages int
	err   error

	calls int
}

func (f *fakeRasterizer) Rasterize(_ context.Context, _, dir string) ([]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var paths []string
	for i := 1; i <= f.pages; i++ {
		path := filepath.Join(dir, fmt.Sprintf("page-%d.png", i))
		if err := os.WriteFile(path, pngBytes(200, 260), 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

var _ Rasterizer = (*fakeRasterizer)(nil)

// pngBytes builds a small PNG the preprocess package can decode.
func pngBytes(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			shade := uint8(240)
			if y > height/3 && y < 2*height/3 {
				shade = 16
			}
			img.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// pagedOCR returns a different result per page, so a test can tell whether
// every page was read.
type pagedOCR struct {
	results []ocr.Result
	errs    []error

	calls []ocr.Document
}

func (p *pagedOCR) Recognize(_ context.Context, doc ocr.Document) (ocr.Result, error) {
	index := len(p.calls)
	p.calls = append(p.calls, doc)
	if index < len(p.errs) && p.errs[index] != nil {
		return ocr.Result{}, p.errs[index]
	}
	if index < len(p.results) {
		return p.results[index], nil
	}
	return ocr.Result{Text: "page", Confidence: 0.9, Pages: 1, Provider: ocr.ProviderTesseract}, nil
}

var _ ocr.Client = (*pagedOCR)(nil)
