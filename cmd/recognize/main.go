// Command recognize turns an upload into the page the OCR engine reads, a
// thumbnail for the dashboard, and the recognized text.
//
// This is the one function that ships as a container image: it needs the
// Tesseract engine and poppler's pdftoppm, neither of which Lambda's managed
// runtimes carry.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"docflow/internal/logging"
	"docflow/internal/ocr"
	"docflow/internal/pipeline"
	"docflow/internal/storage"
	"docflow/internal/store"
)

func main() {
	logger := logging.NewFromEnv(os.Stdout)
	slog.SetDefault(logger)

	ctx := context.Background()
	jobs, err := store.NewFromEnv(ctx)
	if err != nil {
		logger.Error("cannot start: job store", slog.String("error", err.Error()))
		os.Exit(1)
	}
	documents, err := storage.NewS3FromEnv(ctx)
	if err != nil {
		logger.Error("cannot start: document storage", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// OCR_PROVIDER names the engine. An unreadable value falls back to the
	// free one rather than failing, so a typo cannot start billing.
	provider, err := ocr.ProviderFromEnv()
	if err != nil {
		logger.Warn("unreadable OCR_PROVIDER, using tesseract", slog.String("error", err.Error()))
	}
	if provider != ocr.ProviderTesseract {
		// Textract is a paid fallback and has no implementation yet; the
		// quota that would gate it is in internal/quota, waiting for one.
		logger.Warn("only tesseract is implemented, using it", slog.String("requested", string(provider)))
	}

	handler := pipeline.NewRecognize(jobs, documents, documents, ocr.NewTesseract(), ocr.NewRasterizer())
	lambda.Start(func(ctx context.Context, state pipeline.State) (pipeline.State, error) {
		return handler.Handle(logging.Into(ctx, logger), state)
	})
}
