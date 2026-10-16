package ocr

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultBinary is the engine's name on PATH inside the OCR container image.
const DefaultBinary = "tesseract"

// Tesseract recognizes text by running the engine as a subprocess.
//
// A subprocess rather than a binding: cgo is not an option for these Lambdas,
// and the container image already has to carry the engine.
type Tesseract struct {
	// Binary is the executable to run, "tesseract" by default.
	Binary string
	// Languages is passed as -l, "eng" by default.
	Languages string
	// run executes the engine. Tests replace it, so none of them needs the
	// engine installed.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NewTesseract returns a client running the engine from PATH.
func NewTesseract() *Tesseract {
	return &Tesseract{Binary: DefaultBinary, Languages: "eng", run: runCommand}
}

var _ Client = (*Tesseract)(nil)

// tesseractFormats are what the engine reads directly. PDFs are rasterized
// before they reach it, so they are not here.
var tesseractFormats = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/tiff": true,
}

// Recognize runs the engine and returns its text and mean confidence.
//
// The engine is asked for TSV rather than plain text: the same run then
// carries a per-word confidence, which the OCR step compares against the
// fallback threshold. Asking twice would double the cost of the slowest step
// in the pipeline.
func (t *Tesseract) Recognize(ctx context.Context, doc Document) (Result, error) {
	if !tesseractFormats[doc.ContentType] {
		return Result{}, fmt.Errorf("tesseract cannot read %s: %w", doc.ContentType, ErrUnsupported)
	}

	binary := t.Binary
	if binary == "" {
		binary = DefaultBinary
	}
	languages := t.Languages
	if languages == "" {
		languages = "eng"
	}
	run := t.run
	if run == nil {
		run = runCommand
	}

	// "stdout tsv" writes the TSV to standard output instead of a file.
	output, err := run(ctx, binary, doc.Path, "stdout", "-l", languages, "tsv")
	if err != nil {
		return Result{}, fmt.Errorf("run %s on %s: %w", binary, doc.Path, err)
	}

	result, err := parseTSV(string(output))
	if err != nil {
		return Result{}, fmt.Errorf("read %s output for %s: %w", binary, doc.Path, err)
	}
	result.Provider = ProviderTesseract
	return result, nil
}

// runCommand executes the engine, respecting the context's deadline so a
// slow document cannot outlive the Lambda.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

// TSV column indexes, from the engine's own header row.
const (
	colLevel = 0
	colPage  = 1
	colConf  = 10
	colText  = 11
	colCount = 12
)

// levelWord is the row level carrying a recognized word; the rest describe
// pages, blocks, paragraphs, and lines.
const levelWord = "5"

// parseTSV turns the engine's TSV into text and a mean confidence.
//
// Rows with a confidence of -1 are structure, not words, and rows whose text
// is blank are gaps the engine reports but nobody wants in the output.
func parseTSV(output string) (Result, error) {
	reader := csv.NewReader(strings.NewReader(output))
	reader.Comma = '\t'
	// The engine does not quote its text column, and a stray quote would
	// otherwise swallow the rest of the file.
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1

	var (
		words      []string
		confidence float64
		counted    int
		pages      int
	)
	for row := 0; ; row++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("row %d: %w", row, err)
		}
		if len(record) < colCount || record[colLevel] == "level" {
			continue // the header, or a row too short to mean anything
		}
		if page, err := strconv.Atoi(record[colPage]); err == nil && page > pages {
			pages = page
		}
		if record[colLevel] != levelWord {
			continue
		}

		text := strings.TrimSpace(record[colText])
		conf, err := strconv.ParseFloat(record[colConf], 64)
		if err != nil || conf < 0 || text == "" {
			continue
		}
		words = append(words, text)
		confidence += conf
		counted++
	}

	if counted == 0 {
		return Result{}, ErrNoText
	}
	return Result{
		Text: strings.Join(words, " "),
		// The engine reports percentages; the rest of the pipeline works in
		// [0,1], and model.DocumentResult rejects anything else.
		Confidence: confidence / float64(counted) / 100,
		Pages:      max(pages, 1),
	}, nil
}
