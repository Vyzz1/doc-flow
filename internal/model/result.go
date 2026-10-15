package model

import (
	"errors"
	"fmt"
)

// OCRProvider names the engine that produced recognized text. Tesseract is the
// default; Textract is a paid fallback behind a per-user daily quota.
type OCRProvider string

// The OCR engines the pipeline can use.
const (
	ProviderTesseract OCRProvider = "tesseract"
	ProviderTextract  OCRProvider = "textract"
)

// Errors reported when a document result is rejected.
var (
	ErrInvalidProvider   = errors.New("unknown OCR provider")
	ErrInvalidConfidence = errors.New("confidence out of range")
)

// Valid reports whether p is one of the known OCR providers.
func (p OCRProvider) Valid() bool {
	return p == ProviderTesseract || p == ProviderTextract
}

// String makes OCRProvider print as its bare value in log fields and errors.
func (p OCRProvider) String() string { return string(p) }

// Field is one value extracted from a document.
type Field struct {
	Name  string
	Value string
	// Confidence is in [0,1], as reported by the extractor.
	Confidence float64
}

// DocumentResult is a job's final output: the recognized text and the fields
// extracted from it. It is stored as the JOB#<id> / RESULT item.
type DocumentResult struct {
	JobID    string
	Provider OCRProvider
	// Confidence is the mean recognition confidence in [0,1]. The OCR step
	// compares it against the fallback threshold.
	Confidence float64
	Pages      int
	Text       string
	Fields     []Field
}

// Validate reports whether r is internally consistent.
func (r DocumentResult) Validate() error {
	if err := validateJobID(r.JobID); err != nil {
		return err
	}
	if !r.Provider.Valid() {
		return fmt.Errorf("provider %q: %w", r.Provider, ErrInvalidProvider)
	}
	if err := validateConfidence("result", r.Confidence); err != nil {
		return err
	}
	if r.Pages < 1 {
		return fmt.Errorf("pages %d: must be at least 1", r.Pages)
	}
	seen := make(map[string]struct{}, len(r.Fields))
	for _, f := range r.Fields {
		if f.Name == "" {
			return errors.New("extracted field has an empty name")
		}
		if _, dup := seen[f.Name]; dup {
			return fmt.Errorf("extracted field %q appears twice", f.Name)
		}
		seen[f.Name] = struct{}{}
		if err := validateConfidence("field "+f.Name, f.Confidence); err != nil {
			return err
		}
	}
	return nil
}

// Field returns the extracted field named name.
func (r DocumentResult) Field(name string) (Field, bool) {
	for _, f := range r.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func validateConfidence(what string, c float64) error {
	if c < 0 || c > 1 {
		return fmt.Errorf("%s confidence %v: %w", what, c, ErrInvalidConfidence)
	}
	return nil
}
