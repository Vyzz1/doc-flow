// Package preprocess prepares documents for OCR and makes thumbnails.
//
// It works on bytes already in memory, which is safe because the presigned
// upload caps a document at model.MaxUploadBytes. What a small file decodes
// to is a different matter: a few kilobytes of PNG can describe a gigapixel
// image, so every decode is preceded by a header check.
//
// Callers store the output in S3 and pass the keys along. Nothing here
// touches S3 or the network.
package preprocess

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/tiff"
)

// Limits on what this package will decode or produce.
const (
	// MaxPixels is the largest image it will decode. A 10 MiB upload can
	// declare far more than this, and decoding it would cost roughly four
	// bytes per pixel of memory; 40 megapixels is about 160 MiB, already
	// more than a small Lambda has.
	MaxPixels = 40_000_000

	// MaxEdge is the longest side a normalized page keeps. Beyond this the
	// extra detail costs OCR time without improving recognition.
	MaxEdge = 4000

	// ThumbnailEdge is the longest side of a thumbnail.
	ThumbnailEdge = 320

	// thumbnailQuality trades a little fidelity for a much smaller object;
	// a thumbnail is a preview, not evidence.
	thumbnailQuality = 80
)

// Errors reported when a document cannot be prepared.
var (
	// ErrUnsupported means the format is not one this package decodes.
	ErrUnsupported = errors.New("format not supported for preprocessing")

	// ErrTooLarge means the image declares more pixels than MaxPixels.
	ErrTooLarge = errors.New("image is too large to decode")

	// ErrEmptyImage means the header declares no pixels at all.
	ErrEmptyImage = errors.New("image has no pixels")
)

// Page is a rendered image ready to be stored.
type Page struct {
	Bytes       []byte
	ContentType string
	Width       int
	Height      int
}

// CheckBounds reports whether an image of this size may be decoded.
//
// It runs on the header, before any pixels are allocated, which is the only
// point where a decompression bomb can still be refused cheaply.
func CheckBounds(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("%dx%d: %w", width, height, ErrEmptyImage)
	}
	// Multiplying could overflow on a hostile header, so divide instead.
	if height > MaxPixels/width {
		return fmt.Errorf("%dx%d is over %d pixels: %w", width, height, MaxPixels, ErrTooLarge)
	}
	return nil
}

// codec is one format's header reader and decoder.
type codec struct {
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

// codecs maps a content type to its decoder. Chosen by declared type rather
// than by sniffing: validation has already matched the bytes against this
// type, and an explicit table makes the supported set obvious. It also keeps
// the decoders out of image's global registry, so nothing elsewhere in the
// binary gains the ability to decode by accident.
var codecs = map[string]codec{
	"image/png":  {config: png.DecodeConfig, decode: png.Decode},
	"image/jpeg": {config: jpeg.DecodeConfig, decode: jpeg.Decode},
	"image/tiff": {config: tiff.DecodeConfig, decode: tiff.Decode},
}

// Decode reads an image of the given content type, refusing one too large to
// process.
func Decode(data []byte, contentType string) (image.Image, error) {
	// PDFs arrive rasterized, so they never reach this package.
	c, supported := codecs[contentType]
	if !supported {
		return nil, fmt.Errorf("%s: %w", contentType, ErrUnsupported)
	}

	// The header first: this is the only point where a decompression bomb
	// can still be refused without allocating its pixels.
	config, err := c.config(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read %s header: %w", contentType, err)
	}
	if err := CheckBounds(config.Width, config.Height); err != nil {
		return nil, err
	}

	img, err := c.decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", contentType, err)
	}
	return img, nil
}

// Normalize returns the page OCR should read: PNG, and no larger than
// MaxEdge on its longest side.
//
// PNG because it is lossless, and re-encoding a scan as JPEG would add
// artifacts exactly where the engine is looking for letter edges.
func Normalize(img image.Image) (Page, error) {
	bounded := fit(img, MaxEdge)
	encoded, err := encodePNG(bounded)
	if err != nil {
		return Page{}, fmt.Errorf("encode normalized page: %w", err)
	}
	size := bounded.Bounds().Size()
	return Page{
		Bytes:       encoded,
		ContentType: "image/png",
		Width:       size.X,
		Height:      size.Y,
	}, nil
}

// Thumbnail returns a small JPEG preview.
func Thumbnail(img image.Image) (Page, error) {
	small := fit(img, ThumbnailEdge)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flatten(small), &jpeg.Options{Quality: thumbnailQuality}); err != nil {
		return Page{}, fmt.Errorf("encode thumbnail: %w", err)
	}
	size := small.Bounds().Size()
	return Page{
		Bytes:       buf.Bytes(),
		ContentType: "image/jpeg",
		Width:       size.X,
		Height:      size.Y,
	}, nil
}

// fit scales img down so its longest side is at most maxEdge, keeping the
// aspect ratio. An image already small enough is returned untouched, so
// nothing is resampled for no reason.
func fit(img image.Image, maxEdge int) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxEdge && height <= maxEdge {
		return img
	}

	scaled := scaleTo(width, height, maxEdge)
	dst := image.NewRGBA(image.Rect(0, 0, scaled.X, scaled.Y))
	// CatmullRom keeps text edges legible; the cheaper kernels smear them,
	// and this runs once per page.
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, xdraw.Over, nil)
	return dst
}

// scaleTo returns the size that fits inside maxEdge, never smaller than one
// pixel on either side.
func scaleTo(width, height, maxEdge int) image.Point {
	if width >= height {
		return image.Pt(maxEdge, max(height*maxEdge/width, 1))
	}
	return image.Pt(max(width*maxEdge/height, 1), maxEdge)
}

// flatten draws img onto white. JPEG has no alpha channel, and encoding a
// transparent scan without this turns its background black.
func flatten(img image.Image) image.Image {
	if !hasAlpha(img) {
		return img
	}
	bounds := img.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, image.NewUniform(image.White), image.Point{}, draw.Src)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Over)
	return dst
}

func hasAlpha(img image.Image) bool {
	switch img.(type) {
	case *image.RGBA, *image.NRGBA, *image.RGBA64, *image.NRGBA64:
		return true
	default:
		return false
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
