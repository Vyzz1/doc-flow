package pipeline

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"docflow/internal/extract"
	"docflow/internal/model"
	"docflow/internal/ocr"
	"docflow/internal/storage"
)

func recognizeState(contentType string) State {
	state := testState()
	state.ContentType = contentType
	state.SizeBytes = 2048
	return state
}

func newRecognize(t *testing.T, jobs *fakeStore, docs *fakeDocuments, art *fakeArtifacts, engine ocr.Client, pdf Rasterizer) *Recognize {
	t.Helper()
	h := NewRecognize(jobs, docs, art, engine, pdf)
	h.Now = func() time.Time { return testNow }
	h.WorkDir = t.TempDir()
	return h
}

func TestRecognizeAnImage(t *testing.T) {
	jobs := newFakeStore(t)
	docs := &fakeDocuments{body: pngBytes(1200, 1600)}
	art := newFakeArtifacts()
	engine := &pagedOCR{results: []ocr.Result{
		{Text: "INVOICE Total 1,250.00", Confidence: 0.93, Pages: 1, Provider: ocr.ProviderTesseract},
	}}

	got, err := newRecognize(t, jobs, docs, art, engine, &fakeRasterizer{}).
		Handle(context.Background(), recognizeState("image/png"))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	// Three artifacts, each under the derived prefix so the pipeline does
	// not react to its own output.
	for _, key := range []string{got.PageKey, got.ThumbnailKey, got.TextKey} {
		if key == "" {
			t.Fatalf("state is missing an artifact key: %+v", got)
		}
		if !strings.HasPrefix(key, "derived/") {
			t.Errorf("key %q is not under derived/", key)
		}
		if len(art.written[key]) == 0 {
			t.Errorf("nothing was stored at %q", key)
		}
	}
	if art.types[got.ThumbnailKey] != "image/jpeg" {
		t.Errorf("thumbnail stored as %q, want image/jpeg", art.types[got.ThumbnailKey])
	}
	if art.types[got.PageKey] != "image/png" {
		t.Errorf("page stored as %q, want image/png", art.types[got.PageKey])
	}
	if string(art.written[got.TextKey]) != "INVOICE Total 1,250.00" {
		t.Errorf("stored text = %q", art.written[got.TextKey])
	}

	if got.Confidence != 0.93 || got.Pages != 1 || got.Provider != string(ocr.ProviderTesseract) {
		t.Errorf("state = %+v, want the engine's report", got)
	}
	if len(jobs.steps) != 1 || jobs.steps[0].Step != model.StepOCR || jobs.steps[0].Status != model.StepSucceeded {
		t.Errorf("step results = %+v, want one succeeded ocr step", jobs.steps)
	}
	// The recorded step points at what it produced.
	if len(jobs.steps[0].Artifacts) != 3 {
		t.Errorf("step recorded %v, want three artifacts", jobs.steps[0].Artifacts)
	}

	// An image is handed to the engine as it arrived; re-encoding before
	// recognition would only lose detail.
	if len(engine.calls) != 1 || engine.calls[0].ContentType != "image/png" {
		t.Errorf("engine calls = %+v, want one on the original image", engine.calls)
	}
}

func TestRecognizeAPDF(t *testing.T) {
	jobs := newFakeStore(t)
	docs := &fakeDocuments{body: []byte("%PDF-1.7 pretend document")}
	art := newFakeArtifacts()
	pdf := &fakeRasterizer{pages: 3}
	engine := &pagedOCR{results: []ocr.Result{
		{Text: "page one", Confidence: 0.9, Provider: ocr.ProviderTesseract},
		{Text: "page two", Confidence: 0.8, Provider: ocr.ProviderTesseract},
		{Text: "page three", Confidence: 0.7, Provider: ocr.ProviderTesseract},
	}}

	got, err := newRecognize(t, jobs, docs, art, engine, pdf).
		Handle(context.Background(), recognizeState("application/pdf"))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if pdf.calls != 1 {
		t.Errorf("the rasterizer ran %d times, want once", pdf.calls)
	}
	// Every page is read, in order, and the text is joined.
	if len(engine.calls) != 3 {
		t.Fatalf("engine read %d pages, want 3", len(engine.calls))
	}
	text := string(art.written[got.TextKey])
	if text != "page one\npage two\npage three" {
		t.Errorf("stored text = %q, want the pages joined in order", text)
	}
	// The mean of 0.9, 0.8 and 0.7.
	if got.Confidence < 0.79 || got.Confidence > 0.81 {
		t.Errorf("Confidence = %v, want the mean of the pages", got.Confidence)
	}
	if got.Pages != 3 {
		t.Errorf("Pages = %d, want 3", got.Pages)
	}
	// A PDF still gets a thumbnail, which is the reason this work is one
	// step rather than three.
	if len(art.written[got.ThumbnailKey]) == 0 {
		t.Error("a PDF produced no thumbnail")
	}
}

// A blank page in a multi-page document is ordinary and must not fail the
// job; a document blank on every page has nothing to extract from.
func TestRecognizeSkipsBlankPages(t *testing.T) {
	t.Run("one blank page among three", func(t *testing.T) {
		jobs := newFakeStore(t)
		docs := &fakeDocuments{body: []byte("%PDF-1.7")}
		art := newFakeArtifacts()
		engine := &pagedOCR{
			results: []ocr.Result{
				{Text: "first", Confidence: 0.9, Provider: ocr.ProviderTesseract},
				{},
				{Text: "third", Confidence: 0.7, Provider: ocr.ProviderTesseract},
			},
			errs: []error{nil, ocr.ErrNoText, nil},
		}

		got, err := newRecognize(t, jobs, docs, art, engine, &fakeRasterizer{pages: 3}).
			Handle(context.Background(), recognizeState("application/pdf"))
		if err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if text := string(art.written[got.TextKey]); text != "first\nthird" {
			t.Errorf("stored text = %q, want the two readable pages", text)
		}
		// The mean is over the pages that had text, not over all of them.
		if got.Confidence < 0.79 || got.Confidence > 0.81 {
			t.Errorf("Confidence = %v, want the mean of the readable pages", got.Confidence)
		}
	})

	t.Run("every page blank", func(t *testing.T) {
		jobs := newFakeStore(t)
		docs := &fakeDocuments{body: []byte("%PDF-1.7")}
		engine := &pagedOCR{errs: []error{ocr.ErrNoText, ocr.ErrNoText}}

		_, err := newRecognize(t, jobs, docs, newFakeArtifacts(), engine, &fakeRasterizer{pages: 2}).
			Handle(context.Background(), recognizeState("application/pdf"))
		if !IsPermanent(err) {
			t.Errorf("error = %v, want a permanent failure", err)
		}
		if !errors.Is(err, ocr.ErrNoText) {
			t.Errorf("error = %v, want it to wrap ErrNoText", err)
		}
	})
}

func TestRecognizeRejectsPermanently(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		readErr     error
		engineErr   error
		pdfErr      error
	}{
		{name: "no content type", contentType: "", body: pngBytes(10, 10)},
		{name: "the upload is gone", contentType: "image/png", readErr: storage.ErrNotFound},
		{name: "bytes that will not decode", contentType: "image/png", body: []byte("not a png at all")},
		{name: "the engine cannot read the format", contentType: "image/png", body: pngBytes(10, 10), engineErr: ocr.ErrUnsupported},
		{name: "a pdf the tool cannot open", contentType: "application/pdf", body: []byte("%PDF-1.7"), pdfErr: errors.New("Syntax Error: Couldn't read trailer")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			docs := &fakeDocuments{body: tt.body, readErr: tt.readErr}
			engine := &pagedOCR{errs: []error{tt.engineErr}}
			pdf := &fakeRasterizer{pages: 1, err: tt.pdfErr}

			_, err := newRecognize(t, jobs, docs, newFakeArtifacts(), engine, pdf).
				Handle(context.Background(), recognizeState(tt.contentType))
			if !IsPermanent(err) {
				t.Errorf("error = %v, which the state machine would retry", err)
			}
		})
	}
}

func TestRecognizeRetriesTransientFailures(t *testing.T) {
	tests := []struct {
		name      string
		readErr   error
		writeErr  error
		failOn    string
		engineErr error
	}{
		{name: "the upload read is throttled", readErr: errTransient},
		{name: "the page cannot be stored", writeErr: errTransient, failOn: model.ArtifactPage},
		{name: "the thumbnail cannot be stored", writeErr: errTransient, failOn: model.ArtifactThumbnail},
		{name: "the text cannot be stored", writeErr: errTransient, failOn: model.ArtifactText},
		{name: "the engine is unavailable", engineErr: errTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			docs := &fakeDocuments{body: pngBytes(100, 120), readErr: tt.readErr}
			art := newFakeArtifacts()
			art.err, art.failOn = tt.writeErr, tt.failOn
			engine := &pagedOCR{errs: []error{tt.engineErr}}

			_, err := newRecognize(t, jobs, docs, art, engine, &fakeRasterizer{}).
				Handle(context.Background(), recognizeState("image/png"))
			if !IsTransient(err) {
				t.Errorf("error = %v, want a transient failure the state machine will retry", err)
			}
		})
	}
}

func TestRecognizeRecordsAFailure(t *testing.T) {
	jobs := newFakeStore(t)
	docs := &fakeDocuments{body: []byte("not a png")}

	if _, err := newRecognize(t, jobs, docs, newFakeArtifacts(), &pagedOCR{}, &fakeRasterizer{}).
		Handle(context.Background(), recognizeState("image/png")); err == nil {
		t.Fatal("Handle() = nil, want a failure")
	}
	if len(jobs.steps) != 1 {
		t.Fatalf("recorded %d step results, want the failure recorded", len(jobs.steps))
	}
	if jobs.steps[0].Status != model.StepFailed || jobs.steps[0].Error == "" {
		t.Errorf("step result = %+v, want a failed step with a reason", jobs.steps[0])
	}
	if jobs.steps[0].Retryable {
		t.Error("the recorded step says it is retryable, but an undecodable file is not")
	}
}

// Lambda reuses /tmp across invocations, so files left behind would
// eventually fill it.
func TestRecognizeCleansUpItsWorkDir(t *testing.T) {
	jobs := newFakeStore(t)
	docs := &fakeDocuments{body: pngBytes(100, 120)}
	h := newRecognize(t, jobs, docs, newFakeArtifacts(), &pagedOCR{}, &fakeRasterizer{})

	if _, err := h.Handle(context.Background(), recognizeState("image/png")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	left, err := os.ReadDir(h.WorkDir)
	if err != nil {
		t.Fatalf("read work dir: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("the work directory still holds %d entries", len(left))
	}
}

func newExtract(jobs *fakeStore, docs *fakeDocuments) *Extract {
	h := NewExtract(jobs, docs)
	h.Now = func() time.Time { return testNow }
	return h
}

func extractState() State {
	state := recognizeState("application/pdf")
	state.TextKey = model.DerivedKey(testUser, testJobID, model.ArtifactText)
	state.Confidence = 0.92
	state.Pages = 2
	state.Provider = string(model.ProviderTesseract)
	return state
}

func TestExtractStoresTheResult(t *testing.T) {
	jobs := newFakeStore(t)
	docs := &fakeDocuments{body: []byte("INVOICE\nInvoice No: INV-7\nGrand Total: 1,250.00")}

	if _, err := newExtract(jobs, docs).Handle(context.Background(), extractState()); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if jobs.result.JobID != testJobID {
		t.Fatalf("no result was stored: %+v", jobs.result)
	}
	if jobs.result.Provider != model.ProviderTesseract || jobs.result.Pages != 2 {
		t.Errorf("result = %+v, want the state's provider and page count", jobs.result)
	}
	if jobs.result.Confidence != 0.92 {
		t.Errorf("Confidence = %v, want 0.92", jobs.result.Confidence)
	}

	if value := fieldOf(jobs.result.Fields, extract.FieldTotal); value != "125000" {
		t.Errorf("total = %q, want 125000 (fields: %+v)", value, jobs.result.Fields)
	}
	if value := fieldOf(jobs.result.Fields, extract.FieldInvoiceNumber); value != "INV-7" {
		t.Errorf("invoice number = %q, want INV-7", value)
	}

	if len(jobs.steps) != 1 || jobs.steps[0].Step != model.StepExtract {
		t.Errorf("step results = %+v, want one extract step", jobs.steps)
	}
}

func fieldOf(fields []model.Field, name string) string {
	for _, f := range fields {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func TestExtractRejectsPermanently(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*State)
		readErr error
	}{
		{name: "no text key", mutate: func(s *State) { s.TextKey = "" }},
		{name: "the text is gone", mutate: func(*State) {}, readErr: storage.ErrNotFound},
		{name: "an unknown provider", mutate: func(s *State) { s.Provider = "ocrspace" }},
		{name: "no provider", mutate: func(s *State) { s.Provider = "" }},
		// The model rejects a confidence outside [0,1], and a result it
		// rejects is a bug here rather than a transient fault.
		{name: "a confidence the model rejects", mutate: func(s *State) { s.Confidence = 92 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			docs := &fakeDocuments{body: []byte("some text"), readErr: tt.readErr}
			state := extractState()
			tt.mutate(&state)

			_, err := newExtract(jobs, docs).Handle(context.Background(), state)
			if !IsPermanent(err) {
				t.Errorf("error = %v, which the state machine would retry", err)
			}
		})
	}
}

func TestExtractRetriesTransientFailures(t *testing.T) {
	tests := []struct {
		name    string
		readErr error
		putErr  error
	}{
		{name: "the text read is throttled", readErr: errTransient},
		{name: "the result cannot be stored", putErr: errTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore(t)
			if tt.putErr != nil {
				jobs.fail("PutResult", tt.putErr)
			}
			docs := &fakeDocuments{body: []byte("some text"), readErr: tt.readErr}

			_, err := newExtract(jobs, docs).Handle(context.Background(), extractState())
			if !IsTransient(err) {
				t.Errorf("error = %v, want a transient failure", err)
			}
		})
	}
}

// Text with no recognizable labels yields a result with no fields rather
// than a failed job: the text is still worth storing.
func TestExtractStoresTextWithNoFields(t *testing.T) {
	jobs := newFakeStore(t)
	docs := &fakeDocuments{body: []byte("the quick brown fox")}

	if _, err := newExtract(jobs, docs).Handle(context.Background(), extractState()); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(jobs.result.Fields) != 0 {
		t.Errorf("Fields = %+v, want none", jobs.result.Fields)
	}
	if jobs.result.Text != "the quick brown fox" {
		t.Errorf("Text = %q, want it stored anyway", jobs.result.Text)
	}
}
