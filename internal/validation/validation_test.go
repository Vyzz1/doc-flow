package validation

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"docflow/internal/model"
)

const testJobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TV"

var testNow = time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

// Leading bytes of each format, padded out to a realistic header.
var (
	pngHeader  = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0}, 8)...)
	jpegHeader = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 12)...)
	tiffLE     = append([]byte{'I', 'I', 0x2A, 0x00}, bytes.Repeat([]byte{0}, 12)...)
	tiffBE     = append([]byte{'M', 'M', 0x00, 0x2A}, bytes.Repeat([]byte{0}, 12)...)
	pdfHeader  = append([]byte("%PDF-1.7"), bytes.Repeat([]byte{0}, 8)...)
)

func testJob(t *testing.T, docType model.DocType, contentType string) model.Job {
	t.Helper()
	job, err := model.NewJob("user-1", testJobID, docType, testNow)
	if err != nil {
		t.Fatalf("NewJob() error = %v", err)
	}
	job.ContentType = contentType
	return job
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name   string
		header []byte
		want   Kind
	}{
		{name: "png", header: pngHeader, want: KindPNG},
		{name: "jpeg", header: jpegHeader, want: KindJPEG},
		{name: "tiff little endian", header: tiffLE, want: KindTIFF},
		{name: "tiff big endian", header: tiffBE, want: KindTIFF},
		{name: "pdf", header: pdfHeader, want: KindPDF},

		{name: "empty", header: nil, want: KindUnknown},
		{name: "text", header: []byte("hello there, this is not a document"), want: KindUnknown},
		{name: "zip", header: []byte{'P', 'K', 0x03, 0x04, 0, 0, 0, 0}, want: KindUnknown},
		{name: "gif is not accepted anywhere", header: []byte("GIF89a--------"), want: KindUnknown},
		{name: "elf", header: []byte{0x7F, 'E', 'L', 'F', 0, 0, 0, 0}, want: KindUnknown},

		// A truncated signature is not a match.
		{name: "half a png", header: []byte{0x89, 'P', 'N'}, want: KindUnknown},
		{name: "two jpeg bytes", header: []byte{0xFF, 0xD8}, want: KindUnknown},

		// The header has to start the file, so a signature hidden behind
		// other bytes does not count.
		{name: "pdf after padding", header: append([]byte("junk"), pdfHeader...), want: KindUnknown},
		{name: "png after a space", header: append([]byte{' '}, pngHeader...), want: KindUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(tt.header); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectReadsOnlyTheHeader(t *testing.T) {
	// A whole document is never needed, so HeaderSize bytes must be enough.
	full := append(append([]byte{}, pngHeader...), bytes.Repeat([]byte("payload"), 1000)...)
	if got := Detect(full[:HeaderSize]); got != KindPNG {
		t.Errorf("Detect() on the first %d bytes = %q, want %q", HeaderSize, got, KindPNG)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		docType     model.DocType
		contentType string
		header      []byte
		size        int64
		wantKind    Kind
		wantErr     error
	}{
		{
			name: "pdf invoice", docType: model.DocInvoice, contentType: "application/pdf",
			header: pdfHeader, size: 2048, wantKind: KindPDF,
		},
		{
			name: "scanned invoice", docType: model.DocInvoice, contentType: "image/tiff",
			header: tiffBE, size: 2048, wantKind: KindTIFF,
		},
		{
			name: "id photo", docType: model.DocID, contentType: "image/jpeg",
			header: jpegHeader, size: 2048, wantKind: KindJPEG,
		},
		{
			name: "declared type with parameters", docType: model.DocID, contentType: "IMAGE/PNG; charset=binary",
			header: pngHeader, size: 2048, wantKind: KindPNG,
		},
		{
			// create-job always records one, but a job read back from an
			// older item might not have.
			name: "no declared type", docType: model.DocImage, contentType: "",
			header: pngHeader, size: 2048, wantKind: KindPNG,
		},
		{
			name: "exactly at the limit", docType: model.DocImage, contentType: "image/png",
			header: pngHeader, size: model.MaxUploadBytes, wantKind: KindPNG,
		},

		{
			name: "empty", docType: model.DocImage, contentType: "image/png",
			header: pngHeader, size: 0, wantErr: ErrEmpty,
		},
		{
			name: "negative size", docType: model.DocImage, contentType: "image/png",
			header: pngHeader, size: -1, wantErr: ErrEmpty,
		},
		{
			name: "one byte over the limit", docType: model.DocImage, contentType: "image/png",
			header: pngHeader, size: model.MaxUploadBytes + 1, wantErr: ErrTooLarge,
		},
		{
			name: "not a document", docType: model.DocImage, contentType: "image/png",
			header: []byte("<html><body>hi</body>"), size: 2048, wantErr: ErrUnrecognized,
		},
		{
			// The whole point of reading the bytes: a client that says PNG
			// and sends a PDF is rejected.
			name: "declared png, actually pdf", docType: model.DocInvoice, contentType: "image/png",
			header: pdfHeader, size: 2048, wantKind: KindPDF, wantErr: ErrTypeMismatch,
		},
		{
			name: "declared jpeg, actually png", docType: model.DocID, contentType: "image/jpeg",
			header: pngHeader, size: 2048, wantKind: KindPNG, wantErr: ErrTypeMismatch,
		},
		{
			// A real PDF, declared honestly, but an ID document may not be
			// one.
			name: "pdf as an id document", docType: model.DocID, contentType: "application/pdf",
			header: pdfHeader, size: 2048, wantKind: KindPDF, wantErr: ErrNotAccepted,
		},
		{
			name: "tiff as an id document", docType: model.DocID, contentType: "image/tiff",
			header: tiffLE, size: 2048, wantKind: KindTIFF, wantErr: ErrNotAccepted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := testJob(t, tt.docType, tt.contentType)

			kind, err := Validate(job, tt.header, tt.size)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
			if kind != tt.wantKind {
				t.Errorf("Validate() kind = %q, want %q", kind, tt.wantKind)
			}
		})
	}
}

// Every rejection has to be permanent, or the workflow will retry a file that
// can never become valid.
func TestEveryRejectionIsPermanent(t *testing.T) {
	tests := []struct {
		name        string
		docType     model.DocType
		contentType string
		header      []byte
		size        int64
	}{
		{name: "empty", docType: model.DocImage, contentType: "image/png", header: pngHeader, size: 0},
		{name: "too large", docType: model.DocImage, contentType: "image/png", header: pngHeader, size: model.MaxUploadBytes + 1},
		{name: "unrecognized", docType: model.DocImage, contentType: "image/png", header: []byte("nope"), size: 10},
		{name: "mismatch", docType: model.DocInvoice, contentType: "image/png", header: pdfHeader, size: 10},
		{name: "not accepted", docType: model.DocID, contentType: "application/pdf", header: pdfHeader, size: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := testJob(t, tt.docType, tt.contentType)

			_, err := Validate(job, tt.header, tt.size)
			if err == nil {
				t.Fatal("Validate() = nil, want a rejection")
			}
			if !errors.Is(err, ErrPermanent) {
				t.Errorf("Validate() error = %v, which does not wrap ErrPermanent, so it would be retried", err)
			}
		})
	}
}

func TestAcceptedFilesAreNotPermanentFailures(t *testing.T) {
	job := testJob(t, model.DocInvoice, "application/pdf")
	if _, err := Validate(job, pdfHeader, 1024); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

// Every content type the model accepts must be something this package can
// recognize, or an upload create-job allowed could never pass validation.
func TestEveryAcceptedContentTypeIsDetectable(t *testing.T) {
	headers := map[string][]byte{
		"image/jpeg":      jpegHeader,
		"image/png":       pngHeader,
		"image/tiff":      tiffLE,
		"application/pdf": pdfHeader,
	}
	for _, docType := range []model.DocType{model.DocInvoice, model.DocID, model.DocImage} {
		for _, contentType := range docType.ContentTypes() {
			t.Run(string(docType)+"/"+contentType, func(t *testing.T) {
				header, found := headers[contentType]
				if !found {
					t.Fatalf("%s accepts %s, but no test header exists for it", docType, contentType)
				}
				if got := Detect(header); string(got) != contentType {
					t.Fatalf("Detect() = %q for a %s header, want %q", got, contentType, contentType)
				}

				job := testJob(t, docType, contentType)
				if _, err := Validate(job, header, 1024); err != nil {
					t.Errorf("a %s upload of %s is rejected: %v", docType, contentType, err)
				}
			})
		}
	}
}
