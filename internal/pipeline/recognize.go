package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"docflow/internal/extract"
	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/ocr"
	"docflow/internal/preprocess"
	"docflow/internal/storage"
	"docflow/internal/store"
)

// Rasterizer renders a PDF into images an OCR engine can read.
type Rasterizer interface {
	Rasterize(ctx context.Context, pdfPath, dir string) ([]string, error)
}

// Recognize is the workflow's middle step. It turns an upload into the page
// the engine reads, a thumbnail for the dashboard, and the recognized text.
//
// All of that is one state rather than the three the original design had.
// Tesseract cannot open a PDF, and poppler lives only in this step's
// container image, so splitting the work would mean PDFs and images taking
// different paths through the workflow, with nothing to produce a PDF's
// thumbnail. Doing it here costs one read of the upload and one decode
// instead of three of each, and saves two of the four thousand state
// transitions the free tier allows each month. See
// docs/adr/0003-recognition-is-one-state.md.
type Recognize struct {
	Jobs      store.JobStore
	Documents storage.Documents
	Artifacts storage.Artifacts
	OCR       ocr.Client
	PDF       Rasterizer

	// WorkDir is where the engine's input files are written. Lambda gives
	// /tmp; a test gives its own directory.
	WorkDir string
	Now     func() time.Time
}

// NewRecognize returns the step using the production clock and /tmp.
func NewRecognize(jobs store.JobStore, documents storage.Documents, artifacts storage.Artifacts, engine ocr.Client, pdf Rasterizer) *Recognize {
	return &Recognize{
		Jobs:      jobs,
		Documents: documents,
		Artifacts: artifacts,
		OCR:       engine,
		PDF:       pdf,
		Now:       time.Now,
	}
}

// Handle produces the renditions and the text.
func (h *Recognize) Handle(ctx context.Context, state State) (State, error) {
	ctx = logging.WithJob(ctx, state.JobID, state.UserID)
	started := h.now()

	if err := state.Validate(); err != nil {
		return state, Permanent(model.StepOCR, err)
	}
	if state.ContentType == "" {
		// Validation sets this. Reaching here without it means the workflow
		// was started from a payload nobody produced.
		return state, Permanent(model.StepOCR, errors.New("content type is missing"))
	}

	work, cleanup, err := h.workDir(state.JobID)
	if err != nil {
		return state, Transient(model.StepOCR, err)
	}
	defer cleanup()

	body, err := h.Documents.Read(ctx, state.UploadKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return state, Permanent(model.StepOCR, errors.New("the uploaded document is gone"))
		}
		return state, Transient(model.StepOCR, fmt.Errorf("read upload: %w", err))
	}

	pages, err := h.pages(ctx, state, work, body)
	if err != nil {
		h.record(ctx, state, started, err)
		return state, err
	}

	if err := h.writeRenditions(ctx, &state, pages[0]); err != nil {
		h.record(ctx, state, started, err)
		return state, err
	}

	result, err := h.recognize(ctx, state, pages)
	if err != nil {
		h.record(ctx, state, started, err)
		return state, err
	}

	textKey := model.DerivedKey(state.UserID, state.JobID, model.ArtifactText)
	if err := h.Artifacts.Write(ctx, textKey, "text/plain; charset=utf-8", []byte(result.Text)); err != nil {
		wrapped := Transient(model.StepOCR, fmt.Errorf("write text: %w", err))
		h.record(ctx, state, started, wrapped)
		return state, wrapped
	}

	state.TextKey = textKey
	state.Confidence = result.Confidence
	state.Pages = result.Pages
	state.Provider = string(result.Provider)

	h.record(ctx, state, started, nil)
	logging.FromContext(ctx).InfoContext(ctx, "document recognized",
		slog.String("provider", state.Provider),
		slog.Float64("confidence", state.Confidence),
		slog.Int("pages", state.Pages),
		slog.Int("textBytes", len(result.Text)),
	)
	return state, nil
}

// pages returns the image files the engine will read, in reading order.
//
// A PDF is rendered first, because the engine cannot open one. An image is
// written out as it arrived: re-encoding it before recognition would only
// lose detail.
func (h *Recognize) pages(ctx context.Context, state State, work string, body []byte) ([]page, error) {
	if state.ContentType == "application/pdf" {
		pdfPath, err := ocr.WritePDF(work, body)
		if err != nil {
			return nil, Transient(model.StepOCR, err)
		}
		paths, err := h.PDF.Rasterize(ctx, pdfPath, work)
		if err != nil {
			// A PDF the tool cannot open will not open on the second
			// attempt either.
			return nil, Permanent(model.StepOCR, fmt.Errorf("render pdf: %w", err))
		}
		rendered := make([]page, 0, len(paths))
		for _, path := range paths {
			rendered = append(rendered, page{path: path, contentType: "image/png"})
		}
		return rendered, nil
	}

	path := filepath.Join(work, "document")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return nil, Transient(model.StepOCR, fmt.Errorf("write document: %w", err))
	}
	return []page{{path: path, contentType: state.ContentType, body: body}}, nil
}

// page is one image the engine will read.
type page struct {
	path        string
	contentType string
	// body is the bytes, when they are already in hand. A rendered PDF page
	// is read from disk instead.
	body []byte
}

// writeRenditions stores the normalized page and the thumbnail, both
// derived from the document's first page.
func (h *Recognize) writeRenditions(ctx context.Context, state *State, first page) error {
	body := first.body
	if body == nil {
		read, err := os.ReadFile(first.path)
		if err != nil {
			return Transient(model.StepOCR, fmt.Errorf("read rendered page: %w", err))
		}
		body = read
	}

	image, err := preprocess.Decode(body, first.contentType)
	if err != nil {
		// Validation recognized the format from its magic bytes, so a file
		// that will not decode is damaged rather than unsupported. Either
		// way a retry changes nothing.
		return Permanent(model.StepOCR, fmt.Errorf("decode page: %w", err))
	}

	normalized, err := preprocess.Normalize(image)
	if err != nil {
		return Transient(model.StepOCR, fmt.Errorf("normalize page: %w", err))
	}
	thumbnail, err := preprocess.Thumbnail(image)
	if err != nil {
		return Transient(model.StepOCR, fmt.Errorf("make thumbnail: %w", err))
	}

	pageKey := model.DerivedKey(state.UserID, state.JobID, model.ArtifactPage)
	if err := h.Artifacts.Write(ctx, pageKey, normalized.ContentType, normalized.Bytes); err != nil {
		return Transient(model.StepOCR, fmt.Errorf("write page: %w", err))
	}
	thumbnailKey := model.DerivedKey(state.UserID, state.JobID, model.ArtifactThumbnail)
	if err := h.Artifacts.Write(ctx, thumbnailKey, thumbnail.ContentType, thumbnail.Bytes); err != nil {
		return Transient(model.StepOCR, fmt.Errorf("write thumbnail: %w", err))
	}

	state.PageKey = pageKey
	state.ThumbnailKey = thumbnailKey
	return nil
}

// recognize runs the engine over every page and merges the results.
func (h *Recognize) recognize(ctx context.Context, state State, pages []page) (ocr.Result, error) {
	var (
		texts      []string
		confidence float64
		recognized int
		provider   ocr.Provider
	)
	for i, p := range pages {
		result, err := h.OCR.Recognize(ctx, ocr.Document{Path: p.path, ContentType: p.contentType})
		switch {
		case errors.Is(err, ocr.ErrNoText):
			// A blank page in a multi-page document is ordinary. A
			// document with nothing on any page is not, and is caught
			// below.
			logging.FromContext(ctx).InfoContext(ctx, "page has no text", slog.Int("page", i+1))
			continue
		case errors.Is(err, ocr.ErrUnsupported):
			return ocr.Result{}, Permanent(model.StepOCR, err)
		case err != nil:
			return ocr.Result{}, Transient(model.StepOCR, fmt.Errorf("recognize page %d: %w", i+1, err))
		}
		texts = append(texts, result.Text)
		confidence += result.Confidence
		recognized++
		provider = result.Provider
	}

	if recognized == 0 {
		// Nothing to extract from, and nothing a retry would change.
		return ocr.Result{}, Permanent(model.StepOCR, ocr.ErrNoText)
	}
	return ocr.Result{
		Text:       strings.Join(texts, "\n"),
		Confidence: confidence / float64(recognized),
		Pages:      len(pages),
		Provider:   provider,
	}, nil
}

// workDir returns a directory for this job's intermediate files, and a
// function that removes it. Lambda's /tmp is reused across invocations, so
// leaving files behind would eventually fill it.
func (h *Recognize) workDir(jobID string) (string, func(), error) {
	base := h.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "docflow-"+jobID+"-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create work directory: %w", err)
	}
	return dir, func() {
		//nolint:errcheck // a failed cleanup is not worth failing the job
		os.RemoveAll(dir)
	}, nil
}

// record stores the step's outcome. As in the validate step, a failure to
// record is logged rather than returned.
func (h *Recognize) record(ctx context.Context, state State, started time.Time, cause error) {
	var (
		result model.StepResult
		err    error
	)
	if cause == nil {
		result, err = model.NewStepResult(state.JobID, model.StepOCR, started, h.now(),
			state.PageKey, state.ThumbnailKey, state.TextKey)
	} else {
		result, err = model.NewFailedStepResult(state.JobID, model.StepOCR, started, h.now(),
			UserMessage(cause), IsTransient(cause))
	}
	if err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "cannot build the step result",
			slog.String("error", err.Error()))
		return
	}
	if err := h.Jobs.PutStepResult(ctx, result); err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "cannot record the step result",
			slog.String("error", err.Error()))
	}
}

func (h *Recognize) now() time.Time {
	if h.Now == nil {
		return time.Now()
	}
	return h.Now()
}

// Extract is the workflow's last working step: it reads the recognized text
// back, pulls the document's fields out of it, and stores the result.
type Extract struct {
	Jobs      store.JobStore
	Documents storage.Documents

	Now func() time.Time
}

// NewExtract returns the step using the production clock.
func NewExtract(jobs store.JobStore, documents storage.Documents) *Extract {
	return &Extract{Jobs: jobs, Documents: documents, Now: time.Now}
}

// Handle stores the job's result.
//
// Saving is part of this step rather than a state of its own. The two have
// nothing to decide between them, and a separate state would cost another
// cold start and another transition to pass a value along unchanged.
func (h *Extract) Handle(ctx context.Context, state State) (State, error) {
	ctx = logging.WithJob(ctx, state.JobID, state.UserID)
	started := h.now()

	if err := state.Validate(); err != nil {
		return state, Permanent(model.StepExtract, err)
	}
	if state.TextKey == "" {
		return state, Permanent(model.StepExtract, errors.New("no recognized text to extract from"))
	}

	text, err := h.Documents.Read(ctx, state.TextKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return state, Permanent(model.StepExtract, errors.New("the recognized text is gone"))
		}
		return state, Transient(model.StepExtract, fmt.Errorf("read text: %w", err))
	}

	provider := model.OCRProvider(state.Provider)
	if !provider.Valid() {
		return state, Permanent(model.StepExtract, fmt.Errorf("provider %q: %w", state.Provider, model.ErrInvalidProvider))
	}

	result := model.DocumentResult{
		JobID:      state.JobID,
		Provider:   provider,
		Confidence: state.Confidence,
		Pages:      max(state.Pages, 1),
		Text:       string(text),
		Fields:     extract.For(state.DocType, string(text), state.Confidence),
	}
	if err := result.Validate(); err != nil {
		// A result the model rejects is a bug here, not a transient fault.
		return state, Permanent(model.StepExtract, err)
	}

	if err := h.Jobs.PutResult(ctx, result); err != nil {
		wrapped := Transient(model.StepExtract, fmt.Errorf("store result: %w", err))
		h.record(ctx, state, started, wrapped)
		return state, wrapped
	}

	h.record(ctx, state, started, nil)
	logging.FromContext(ctx).InfoContext(ctx, "fields extracted",
		slog.Int("fields", len(result.Fields)),
		slog.String("docType", string(state.DocType)),
	)
	return state, nil
}

func (h *Extract) record(ctx context.Context, state State, started time.Time, cause error) {
	var (
		result model.StepResult
		err    error
	)
	if cause == nil {
		result, err = model.NewStepResult(state.JobID, model.StepExtract, started, h.now())
	} else {
		result, err = model.NewFailedStepResult(state.JobID, model.StepExtract, started, h.now(),
			UserMessage(cause), IsTransient(cause))
	}
	if err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "cannot build the step result",
			slog.String("error", err.Error()))
		return
	}
	if err := h.Jobs.PutStepResult(ctx, result); err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "cannot record the step result",
			slog.String("error", err.Error()))
	}
}

func (h *Extract) now() time.Time {
	if h.Now == nil {
		return time.Now()
	}
	return h.Now()
}
