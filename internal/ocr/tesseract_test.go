package ocr

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// tsvHeader is the row the engine emits first.
const tsvHeader = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext"

// tsvRow builds one output row. Level 5 is a word; anything else is
// structure.
func tsvRow(level, page, conf, text string) string {
	return strings.Join([]string{level, page, "1", "1", "1", "1", "0", "0", "10", "10", conf, text}, "\t")
}

func tsv(rows ...string) string {
	return tsvHeader + "\n" + strings.Join(rows, "\n") + "\n"
}

// fakeRunner replays canned engine output and records the command.
type fakeRunner struct {
	name string
	args []string

	output string
	err    error
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.name, f.args = name, args
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.output), nil
}

func newTesseract(r *fakeRunner) *Tesseract {
	return &Tesseract{Binary: "tesseract", Languages: "eng", run: r.run}
}

func TestRecognize(t *testing.T) {
	r := &fakeRunner{output: tsv(
		tsvRow("1", "1", "-1", ""),
		tsvRow("5", "1", "96", "INVOICE"),
		tsvRow("5", "1", "90", "Total"),
		tsvRow("5", "1", "84", "1250000"),
	)}

	got, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/doc.png", ContentType: "image/png"})
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if got.Text != "INVOICE Total 1250000" {
		t.Errorf("Text = %q, want the words joined", got.Text)
	}
	// 96, 90, and 84 average to 90 percent, which the rest of the pipeline
	// wants as 0.9.
	if math.Abs(got.Confidence-0.9) > 1e-9 {
		t.Errorf("Confidence = %v, want 0.9", got.Confidence)
	}
	if got.Confidence > 1 {
		t.Error("Confidence is a percentage; model.DocumentResult would reject it")
	}
	if got.Pages != 1 {
		t.Errorf("Pages = %d, want 1", got.Pages)
	}
	if got.Provider != ProviderTesseract {
		t.Errorf("Provider = %q, want tesseract", got.Provider)
	}
}

func TestRecognizeAsksForTSVOnce(t *testing.T) {
	r := &fakeRunner{output: tsv(tsvRow("5", "1", "90", "hello"))}

	if _, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.png", ContentType: "image/png"}); err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if r.name != "tesseract" {
		t.Errorf("ran %q, want tesseract", r.name)
	}
	// One run has to yield both the text and the confidences: asking twice
	// would double the cost of the slowest step in the pipeline.
	want := []string{"/tmp/a.png", "stdout", "-l", "eng", "tsv"}
	if strings.Join(r.args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", r.args, want)
	}
}

func TestRecognizeCountsPages(t *testing.T) {
	r := &fakeRunner{output: tsv(
		tsvRow("5", "1", "90", "one"),
		tsvRow("5", "2", "90", "two"),
		tsvRow("5", "3", "90", "three"),
	)}

	got, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.tiff", ContentType: "image/tiff"})
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if got.Pages != 3 {
		t.Errorf("Pages = %d, want 3", got.Pages)
	}
}

func TestRecognizeIgnoresStructureAndGaps(t *testing.T) {
	r := &fakeRunner{output: tsv(
		tsvRow("1", "1", "-1", ""),     // page
		tsvRow("2", "1", "-1", ""),     // block
		tsvRow("3", "1", "-1", ""),     // paragraph
		tsvRow("4", "1", "-1", ""),     // line
		tsvRow("5", "1", "-1", ""),     // a word row with no confidence
		tsvRow("5", "1", "95", "   "),  // whitespace the engine reports
		tsvRow("5", "1", "95", "real"), // the only real word
	)}

	got, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.png", ContentType: "image/png"})
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if got.Text != "real" {
		t.Errorf("Text = %q, want only the recognized word", got.Text)
	}
	if math.Abs(got.Confidence-0.95) > 1e-9 {
		t.Errorf("Confidence = %v, want 0.95: the skipped rows must not drag the mean", got.Confidence)
	}
}

func TestRecognizeReportsNoText(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "header only", output: tsvHeader + "\n"},
		{name: "empty", output: ""},
		{name: "structure only", output: tsv(tsvRow("4", "1", "-1", ""))},
		{name: "blank words", output: tsv(tsvRow("5", "1", "90", ""))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRunner{output: tt.output}
			_, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.png", ContentType: "image/png"})
			if !errors.Is(err, ErrNoText) {
				t.Errorf("Recognize() error = %v, want ErrNoText", err)
			}
		})
	}
}

func TestRecognizeRefusesFormatsItCannotRead(t *testing.T) {
	tests := []string{"application/pdf", "image/gif", "text/plain", ""}
	for _, contentType := range tests {
		t.Run(contentType, func(t *testing.T) {
			r := &fakeRunner{}
			_, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a", ContentType: contentType})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Recognize() error = %v, want ErrUnsupported", err)
			}
			if r.name != "" {
				t.Error("Recognize() ran the engine on a format it cannot read")
			}
		})
	}
}

// PDFs are rasterized before they reach the engine, so refusing one here is
// the correct behaviour rather than a gap.
func TestRecognizeRefusesPDF(t *testing.T) {
	r := &fakeRunner{}
	_, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.pdf", ContentType: "application/pdf"})
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Recognize() error = %v, want ErrUnsupported", err)
	}
}

func TestRecognizeWrapsEngineFailures(t *testing.T) {
	sentinel := errors.New("exit status 1")
	r := &fakeRunner{err: sentinel}

	_, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.png", ContentType: "image/png"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recognize() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestRecognizeRejectsUnreadableOutput(t *testing.T) {
	// A row that ends mid-record is corruption, not an empty document.
	r := &fakeRunner{output: tsvHeader + "\n" + "5\t1\t1\n"}

	got, err := newTesseract(r).Recognize(context.Background(), Document{Path: "/tmp/a.png", ContentType: "image/png"})
	if err == nil {
		t.Fatalf("Recognize() = %+v, want an error", got)
	}
}

func TestNewTesseractDefaults(t *testing.T) {
	client := NewTesseract()
	if client.Binary != DefaultBinary {
		t.Errorf("Binary = %q, want %q", client.Binary, DefaultBinary)
	}
	if client.Languages != "eng" {
		t.Errorf("Languages = %q, want eng", client.Languages)
	}
	if client.run == nil {
		t.Error("NewTesseract() left no way to run the engine")
	}
}

func TestZeroTesseractFallsBackToDefaults(t *testing.T) {
	// A Tesseract built with a struct literal must still work; the zero
	// value naming no binary would otherwise try to run "".
	var client Tesseract
	client.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != DefaultBinary {
			t.Errorf("ran %q, want %q", name, DefaultBinary)
		}
		if strings.Join(args, " ") != "/tmp/a.png stdout -l eng tsv" {
			t.Errorf("args = %v, want the defaults", args)
		}
		return []byte(tsv(tsvRow("5", "1", "90", "hi"))), nil
	}

	if _, err := client.Recognize(context.Background(), Document{Path: "/tmp/a.png", ContentType: "image/png"}); err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
}
