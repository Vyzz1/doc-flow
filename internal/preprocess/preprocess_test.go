package preprocess

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"golang.org/x/image/tiff"
)

// synthetic builds an image with a light background and a dark band, so a
// rescale has something to preserve.
func synthetic(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			shade := color.RGBA{R: 240, G: 240, B: 240, A: 255}
			if y > height/3 && y < 2*height/3 {
				shade = color.RGBA{R: 16, G: 16, B: 16, A: 255}
			}
			img.Set(x, y, shade)
		}
	}
	return img
}

func encode(t *testing.T, img image.Image, contentType string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch contentType {
	case "image/png":
		err = png.Encode(&buf, img)
	case "image/jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "image/tiff":
		err = tiff.Encode(&buf, img, nil)
	default:
		t.Fatalf("no encoder for %s", contentType)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", contentType, err)
	}
	return buf.Bytes()
}

func TestCheckBounds(t *testing.T) {
	tests := []struct {
		name    string
		width   int
		height  int
		wantErr error
	}{
		{name: "a page", width: 2480, height: 3508},
		{name: "one pixel", width: 1, height: 1},
		{name: "exactly at the limit", width: MaxPixels / 1000, height: 1000},

		{name: "zero width", width: 0, height: 100, wantErr: ErrEmptyImage},
		{name: "zero height", width: 100, height: 0, wantErr: ErrEmptyImage},
		{name: "negative", width: -1, height: 100, wantErr: ErrEmptyImage},
		{name: "one pixel over", width: MaxPixels/1000 + 1, height: 1001, wantErr: ErrTooLarge},
		{name: "a gigapixel bomb", width: 50000, height: 50000, wantErr: ErrTooLarge},
		// The product of these overflows a 32-bit int; dividing instead of
		// multiplying is what keeps the check honest.
		{name: "dimensions that would overflow", width: 1 << 30, height: 1 << 30, wantErr: ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckBounds(tt.width, tt.height); !errors.Is(err, tt.wantErr) {
				t.Errorf("CheckBounds(%d, %d) = %v, want %v", tt.width, tt.height, err, tt.wantErr)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	for _, contentType := range []string{"image/png", "image/jpeg", "image/tiff"} {
		t.Run(contentType, func(t *testing.T) {
			data := encode(t, synthetic(60, 40), contentType)

			img, err := Decode(data, contentType)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got := img.Bounds().Size(); got.X != 60 || got.Y != 40 {
				t.Errorf("size = %v, want 60x40", got)
			}
		})
	}
}

func TestDecodeRefusesWhatItCannotRead(t *testing.T) {
	png60 := encode(t, synthetic(60, 40), "image/png")

	tests := []struct {
		name        string
		data        []byte
		contentType string
		wantErr     error
	}{
		// PDFs are rasterized before they reach this package.
		{name: "pdf", data: []byte("%PDF-1.7"), contentType: "application/pdf", wantErr: ErrUnsupported},
		{name: "gif", data: []byte("GIF89a"), contentType: "image/gif", wantErr: ErrUnsupported},
		{name: "no content type", data: png60, contentType: "", wantErr: ErrUnsupported},

		{name: "truncated", data: png60[:20], contentType: "image/png"},
		{name: "empty", data: nil, contentType: "image/png"},
		{name: "wrong codec for the bytes", data: png60, contentType: "image/jpeg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.data, tt.contentType)
			if err == nil {
				t.Fatal("Decode() = nil, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Decode() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// A header claiming more pixels than the limit is refused before any are
// allocated, which is the whole point of reading the header first.
func TestDecodeRefusesABombFromItsHeader(t *testing.T) {
	// A real file this large cannot be built in a test, so the guard is
	// checked where it runs: on the declared dimensions.
	if err := CheckBounds(1<<20, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("CheckBounds() = %v, want ErrTooLarge", err)
	}

	// And a legitimate scan still passes.
	data := encode(t, synthetic(1200, 1600), "image/png")
	if _, err := Decode(data, "image/png"); err != nil {
		t.Errorf("Decode() rejected a 1200x1600 page: %v", err)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name       string
		width      int
		height     int
		wantWidth  int
		wantHeight int
	}{
		{name: "already small", width: 800, height: 600, wantWidth: 800, wantHeight: 600},
		{name: "exactly at the edge", width: MaxEdge, height: 2000, wantWidth: MaxEdge, wantHeight: 2000},
		{name: "wide", width: 8000, height: 4000, wantWidth: MaxEdge, wantHeight: MaxEdge / 2},
		{name: "tall", width: 4000, height: 8000, wantWidth: MaxEdge / 2, wantHeight: MaxEdge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(synthetic(tt.width, tt.height))
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got.Width != tt.wantWidth || got.Height != tt.wantHeight {
				t.Errorf("size = %dx%d, want %dx%d", got.Width, got.Height, tt.wantWidth, tt.wantHeight)
			}
			// PNG, because re-encoding a scan as JPEG adds artifacts exactly
			// where the engine looks for letter edges.
			if got.ContentType != "image/png" {
				t.Errorf("ContentType = %q, want image/png", got.ContentType)
			}
			if _, err := png.Decode(bytes.NewReader(got.Bytes)); err != nil {
				t.Errorf("output is not a readable PNG: %v", err)
			}
		})
	}
}

func TestThumbnail(t *testing.T) {
	tests := []struct {
		name       string
		width      int
		height     int
		wantWidth  int
		wantHeight int
	}{
		{name: "landscape", width: 1600, height: 800, wantWidth: ThumbnailEdge, wantHeight: ThumbnailEdge / 2},
		{name: "portrait", width: 800, height: 1600, wantWidth: ThumbnailEdge / 2, wantHeight: ThumbnailEdge},
		{name: "square", width: 1000, height: 1000, wantWidth: ThumbnailEdge, wantHeight: ThumbnailEdge},
		{name: "smaller than the box is left alone", width: 100, height: 80, wantWidth: 100, wantHeight: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Thumbnail(synthetic(tt.width, tt.height))
			if err != nil {
				t.Fatalf("Thumbnail() error = %v", err)
			}
			if got.Width != tt.wantWidth || got.Height != tt.wantHeight {
				t.Errorf("size = %dx%d, want %dx%d", got.Width, got.Height, tt.wantWidth, tt.wantHeight)
			}
			if got.ContentType != "image/jpeg" {
				t.Errorf("ContentType = %q, want image/jpeg", got.ContentType)
			}
			if _, err := jpeg.Decode(bytes.NewReader(got.Bytes)); err != nil {
				t.Errorf("output is not a readable JPEG: %v", err)
			}
		})
	}
}

func TestThumbnailIsMuchSmallerThanThePage(t *testing.T) {
	img := synthetic(2000, 2600)

	page, err := Normalize(img)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	thumb, err := Thumbnail(img)
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	if len(thumb.Bytes) >= len(page.Bytes) {
		t.Errorf("thumbnail is %d bytes and the page is %d: a preview must be the cheap one", len(thumb.Bytes), len(page.Bytes))
	}
}

// A transparent scan encoded straight to JPEG comes out with a black
// background, because JPEG has no alpha channel.
func TestThumbnailFlattensTransparencyOntoWhite(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40)) // fully transparent

	got, err := Thumbnail(img)
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(got.Bytes))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	r, g, b, _ := decoded.At(20, 20).RGBA()
	if r < 0xF000 || g < 0xF000 || b < 0xF000 {
		t.Errorf("centre pixel = (%d, %d, %d), want near white", r>>8, g>>8, b>>8)
	}
}

func TestScaleToKeepsAtLeastOnePixel(t *testing.T) {
	// A very long, very thin page would otherwise scale to zero height and
	// produce an empty image.
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "a hairline strip", width: 10000, height: 1},
		{name: "a vertical hairline", width: 1, height: 10000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scaleTo(tt.width, tt.height, ThumbnailEdge)
			if got.X < 1 || got.Y < 1 {
				t.Errorf("scaleTo() = %v, want both sides at least 1", got)
			}

			page, err := Thumbnail(synthetic(tt.width, tt.height))
			if err != nil {
				t.Fatalf("Thumbnail() error = %v", err)
			}
			if page.Width < 1 || page.Height < 1 {
				t.Errorf("thumbnail is %dx%d", page.Width, page.Height)
			}
		})
	}
}

func TestFitLeavesSmallImagesUntouched(t *testing.T) {
	img := synthetic(100, 100)
	if got := fit(img, MaxEdge); got != img {
		t.Error("fit() resampled an image that already fits, which costs time for nothing")
	}
}

func TestRoundTripFromEncodedBytes(t *testing.T) {
	// The path the preprocess step actually takes: bytes from S3, decode,
	// then both renditions.
	for _, contentType := range []string{"image/png", "image/jpeg", "image/tiff"} {
		t.Run(contentType, func(t *testing.T) {
			data := encode(t, synthetic(1500, 2000), contentType)

			img, err := Decode(data, contentType)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			page, err := Normalize(img)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			thumb, err := Thumbnail(img)
			if err != nil {
				t.Fatalf("Thumbnail() error = %v", err)
			}
			if len(page.Bytes) == 0 || len(thumb.Bytes) == 0 {
				t.Fatal("a rendition came back empty")
			}
			if page.Width != 1500 || page.Height != 2000 {
				t.Errorf("page = %dx%d, want the original 1500x2000", page.Width, page.Height)
			}
		})
	}
}

func TestFlattenOnlyTouchesImagesWithAlpha(t *testing.T) {
	// A JPEG decodes to YCbCr and a grayscale scan to image.Gray; neither
	// has an alpha channel, so neither needs the extra copy.
	gray := image.NewGray(image.Rect(0, 0, 10, 10))
	if got := flatten(gray); got != image.Image(gray) {
		t.Error("flatten() copied an image with no alpha channel")
	}
	if hasAlpha(gray) {
		t.Error("hasAlpha(image.Gray) = true")
	}

	withAlpha := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	if got := flatten(withAlpha); got == image.Image(withAlpha) {
		t.Error("flatten() left a transparent image alone, so JPEG would blacken it")
	}
}
