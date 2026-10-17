package ocr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// itoa keeps the default-page-cap assertion readable.
func itoa(n int) string { return strconv.Itoa(n) }

// fakeRasterizer records the command and writes the pages it is told to.
type fakeRasterizer struct {
	args  []string
	pages []string
	err   error
}

func (f *fakeRasterizer) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.args = args
	if f.err != nil {
		return nil, f.err
	}
	// The real tool writes <prefix>-N.png; the prefix is the last argument.
	prefix := args[len(args)-1]
	for _, page := range f.pages {
		if err := os.WriteFile(prefix+"-"+page+".png", []byte("png"), 0o600); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func newRasterizer(f *fakeRasterizer) *Rasterizer {
	return &Rasterizer{Binary: "pdftoppm", MaxPages: 5, run: f.run}
}

func TestRasterize(t *testing.T) {
	f := &fakeRasterizer{pages: []string{"1", "2"}}
	dir := t.TempDir()

	pages, err := newRasterizer(f).Rasterize(context.Background(), filepath.Join(dir, "doc.pdf"), dir)
	if err != nil {
		t.Fatalf("Rasterize() error = %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("Rasterize() returned %d pages, want 2", len(pages))
	}

	joined := strings.Join(f.args, " ")
	for _, want := range []string{"-png", "-r 300", "-f 1", "-l 5"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %v are missing %q", f.args, want)
		}
	}
}

// Glob sorts lexically, which puts page-10 before page-2. Reading order is
// what the recognized text depends on.
func TestRasterizeOrdersPagesNumerically(t *testing.T) {
	f := &fakeRasterizer{pages: []string{"1", "2", "3", "10", "11"}}
	dir := t.TempDir()

	pages, err := newRasterizer(f).Rasterize(context.Background(), filepath.Join(dir, "doc.pdf"), dir)
	if err != nil {
		t.Fatalf("Rasterize() error = %v", err)
	}
	want := []string{"page-1.png", "page-2.png", "page-3.png", "page-10.png", "page-11.png"}
	for i, page := range pages {
		if filepath.Base(page) != want[i] {
			t.Errorf("page %d = %s, want %s (all: %v)", i, filepath.Base(page), want[i], pages)
		}
	}
}

// A hundred-page PDF would spend the whole Lambda budget before the engine
// starts.
func TestRasterizeCapsThePageCount(t *testing.T) {
	f := &fakeRasterizer{pages: []string{"1"}}
	r := newRasterizer(f)
	r.MaxPages = 2
	dir := t.TempDir()

	if _, err := r.Rasterize(context.Background(), filepath.Join(dir, "doc.pdf"), dir); err != nil {
		t.Fatalf("Rasterize() error = %v", err)
	}
	if !strings.Contains(strings.Join(f.args, " "), "-l 2") {
		t.Errorf("args %v do not cap the page range", f.args)
	}
}

func TestRasterizeReportsAnEmptyDocument(t *testing.T) {
	// The tool exited cleanly but produced nothing, which happens for a PDF
	// with no renderable pages.
	f := &fakeRasterizer{}
	dir := t.TempDir()

	_, err := newRasterizer(f).Rasterize(context.Background(), filepath.Join(dir, "doc.pdf"), dir)
	if !errors.Is(err, ErrEmptyPDF) {
		t.Errorf("Rasterize() error = %v, want ErrEmptyPDF", err)
	}
}

func TestRasterizeWrapsToolFailures(t *testing.T) {
	sentinel := errors.New("Syntax Error: Couldn't find trailer dictionary")
	f := &fakeRasterizer{err: sentinel}
	dir := t.TempDir()

	_, err := newRasterizer(f).Rasterize(context.Background(), filepath.Join(dir, "doc.pdf"), dir)
	if !errors.Is(err, sentinel) {
		t.Errorf("Rasterize() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestZeroRasterizerFallsBackToDefaults(t *testing.T) {
	var r Rasterizer
	dir := t.TempDir()
	r.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != DefaultRasterizer {
			t.Errorf("ran %q, want %q", name, DefaultRasterizer)
		}
		if !strings.Contains(strings.Join(args, " "), "-l "+itoa(DefaultMaxPages)) {
			t.Errorf("args %v do not use the default page cap", args)
		}
		return nil, os.WriteFile(args[len(args)-1]+"-1.png", []byte("png"), 0o600)
	}

	if _, err := r.Rasterize(context.Background(), filepath.Join(dir, "doc.pdf"), dir); err != nil {
		t.Fatalf("Rasterize() error = %v", err)
	}
}

func TestNewRasterizerDefaults(t *testing.T) {
	r := NewRasterizer()
	if r.Binary != DefaultRasterizer || r.MaxPages != DefaultMaxPages || r.run == nil {
		t.Errorf("NewRasterizer() = %+v, want the defaults filled in", r)
	}
}

func TestWritePDF(t *testing.T) {
	dir := t.TempDir()
	body := []byte("%PDF-1.7")

	path, err := WritePDF(dir, body)
	if err != nil {
		t.Fatalf("WritePDF() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("file holds %q, want %q", got, body)
	}
}

func TestWritePDFReportsAnUnwritableDirectory(t *testing.T) {
	if _, err := WritePDF(filepath.Join(t.TempDir(), "no-such-dir"), []byte("x")); err == nil {
		t.Error("WritePDF() = nil for a missing directory, want an error")
	}
}
