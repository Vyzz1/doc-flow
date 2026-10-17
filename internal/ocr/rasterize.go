package ocr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// DefaultRasterizer is poppler's PDF-to-image tool, as it is named on PATH
// inside the OCR container image.
const DefaultRasterizer = "pdftoppm"

// rasterDPI is the resolution a page is rendered at. Tesseract wants around
// 300 for body text; higher costs time and memory without recognizing more.
const rasterDPI = 300

// ErrEmptyPDF means the file produced no pages.
var ErrEmptyPDF = errors.New("pdf produced no pages")

// Rasterizer turns a PDF into images Tesseract can read.
//
// The engine cannot open a PDF at all, so this runs first. It is a separate
// binary rather than a library: a PDF parser in this process would be a
// large attack surface for a file a stranger uploaded, while a subprocess
// can be given a deadline and nothing else.
type Rasterizer struct {
	// Binary is the executable to run, "pdftoppm" by default.
	Binary string
	// MaxPages caps how much of a document is rendered. A hundred-page PDF
	// would otherwise spend the whole Lambda budget before the engine
	// starts.
	MaxPages int
	// run executes the tool. Tests replace it.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultMaxPages is how many pages are rendered when a caller does not say.
const DefaultMaxPages = 5

// NewRasterizer returns a rasterizer running poppler from PATH.
func NewRasterizer() *Rasterizer {
	return &Rasterizer{Binary: DefaultRasterizer, MaxPages: DefaultMaxPages, run: runCommand}
}

// Rasterize renders the PDF at pdfPath into PNG files in dir, returning
// their paths in page order.
func (r *Rasterizer) Rasterize(ctx context.Context, pdfPath, dir string) ([]string, error) {
	binary := r.Binary
	if binary == "" {
		binary = DefaultRasterizer
	}
	maxPages := r.MaxPages
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	run := r.run
	if run == nil {
		run = runCommand
	}

	prefix := filepath.Join(dir, "page")
	// -r sets the resolution, -f and -l bound the page range, and the
	// prefix is where the tool writes page-1.png, page-2.png and so on.
	if _, err := run(ctx, binary,
		"-png",
		"-r", strconv.Itoa(rasterDPI),
		"-f", "1",
		"-l", strconv.Itoa(maxPages),
		pdfPath, prefix,
	); err != nil {
		return nil, fmt.Errorf("run %s on %s: %w", binary, pdfPath, err)
	}

	pages, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, fmt.Errorf("list rendered pages: %w", err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("render %s: %w", pdfPath, ErrEmptyPDF)
	}
	// Glob sorts lexically, which puts page-10 before page-2. Ordering by
	// the number the tool wrote is what keeps the text in reading order.
	sortPages(pages, prefix)
	return pages, nil
}

// sortPages orders rendered pages by their page number rather than by name.
func sortPages(pages []string, prefix string) {
	number := func(path string) int {
		name := path[min(len(prefix)+1, len(path)):]
		name = name[:max(len(name)-len(".png"), 0)]
		n, err := strconv.Atoi(name)
		if err != nil {
			return 0
		}
		return n
	}
	// Insertion sort: a page cap of a handful makes anything cleverer
	// pointless.
	for i := 1; i < len(pages); i++ {
		for j := i; j > 0 && number(pages[j]) < number(pages[j-1]); j-- {
			pages[j], pages[j-1] = pages[j-1], pages[j]
		}
	}
}

// WritePDF stores body in dir as a file the rasterizer can open, and
// returns its path. The engine and the tool both want a file, not a stream.
func WritePDF(dir string, body []byte) (string, error) {
	path := filepath.Join(dir, "document.pdf")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}
