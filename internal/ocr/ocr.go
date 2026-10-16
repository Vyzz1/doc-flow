// Package ocr recognizes text in document images.
//
// Tesseract is the default and runs as a subprocess, so the Lambda stays
// cgo-free and ships the engine as a binary in its container image. Textract
// is a paid fallback for low-confidence results, gated by internal/quota.
//
// Everything is behind Client, so the steps above can be tested against Fake
// without an engine, a container, or an AWS account.
package ocr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Provider names an engine.
type Provider string

// The engines this package knows about.
const (
	ProviderTesseract Provider = "tesseract"
	ProviderTextract  Provider = "textract"
)

// Valid reports whether p is a known engine.
func (p Provider) Valid() bool {
	return p == ProviderTesseract || p == ProviderTextract
}

// String makes Provider print as its bare value in log fields and errors.
func (p Provider) String() string { return string(p) }

// Errors reported when recognition cannot proceed.
var (
	// ErrUnsupported means the engine cannot read that format.
	ErrUnsupported = errors.New("format not supported by this engine")

	// ErrNoText means the engine ran but found nothing. It is a permanent
	// outcome for this document, not a reason to retry.
	ErrNoText = errors.New("no text recognized")

	// ErrUnknownProvider means OCR_PROVIDER named an engine that does not
	// exist.
	ErrUnknownProvider = errors.New("unknown OCR provider")
)

// Document is one file to recognize, already downloaded next to the process.
//
// Only a path travels, never the bytes: a workflow payload stays small, and
// the engine wants a file anyway.
type Document struct {
	// Path is a local file, typically under the Lambda's /tmp.
	Path string
	// ContentType is the format validation found in the bytes, not what the
	// client declared.
	ContentType string
}

// Result is what an engine recognized.
type Result struct {
	Text string
	// Confidence is the mean over recognized words, in [0,1]. Engines report
	// percentages; converting here keeps that unit out of the rest of the
	// pipeline.
	Confidence float64
	Pages      int
	Provider   Provider
}

// Client recognizes text in a document.
type Client interface {
	Recognize(ctx context.Context, doc Document) (Result, error)
}

// ProviderFromEnv reads OCR_PROVIDER, defaulting to Tesseract.
func ProviderFromEnv() (Provider, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("OCR_PROVIDER")))
	if raw == "" {
		return ProviderTesseract, nil
	}
	provider := Provider(raw)
	if !provider.Valid() {
		return ProviderTesseract, fmt.Errorf("OCR_PROVIDER %q: %w", raw, ErrUnknownProvider)
	}
	return provider, nil
}

// Fake is a Client for tests and local runs. Tests never call a real engine,
// and nothing in this repository may call a paid one.
type Fake struct {
	Result Result
	Err    error

	// Calls records what was asked of it.
	Calls []Document
}

// Recognize returns the canned result.
func (f *Fake) Recognize(_ context.Context, doc Document) (Result, error) {
	f.Calls = append(f.Calls, doc)
	if f.Err != nil {
		return Result{}, f.Err
	}
	result := f.Result
	if result.Provider == "" {
		result.Provider = ProviderTesseract
	}
	return result, nil
}

var _ Client = (*Fake)(nil)
