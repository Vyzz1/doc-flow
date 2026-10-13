// Package ocr will recognize text from document images.
package ocr

// TODO: Define an OCRClient interface and choose the provider via OCR_PROVIDER.
// TODO: Default to Tesseract invoked as a subprocess with context cancellation and no cgo.
// TODO: Allow low-confidence Textract fallback only after a daily quota check.
// TODO: Use fake OCR clients in tests; never call paid services from tests.
