// Command extract reads the recognized text back, pulls the document's
// fields out of it, and stores the job's result.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"docflow/internal/logging"
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

	handler := pipeline.NewExtract(jobs, documents)
	lambda.Start(func(ctx context.Context, state pipeline.State) (pipeline.State, error) {
		return handler.Handle(logging.Into(ctx, logger), state)
	})
}
