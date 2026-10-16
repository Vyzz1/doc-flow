// Command create-job is the Lambda behind POST /jobs. It records a PENDING
// job and returns a presigned upload for the document.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"docflow/internal/api"
	"docflow/internal/logging"
	"docflow/internal/storage"
	"docflow/internal/store"
)

func main() {
	logger := logging.NewFromEnv(os.Stdout)
	slog.SetDefault(logger)

	// Clients are built here, not per invocation, so a warm container reuses
	// them and their connection pools.
	ctx := context.Background()
	jobs, err := store.NewFromEnv(ctx)
	if err != nil {
		logger.Error("cannot start: job store", slog.String("error", err.Error()))
		os.Exit(1)
	}
	uploads, err := storage.NewS3FromEnv(ctx)
	if err != nil {
		logger.Error("cannot start: upload storage", slog.String("error", err.Error()))
		os.Exit(1)
	}

	handler := api.NewCreateJob(jobs, uploads)
	lambda.Start(func(ctx context.Context, r api.Request) (api.Response, error) {
		return handler.Handle(logging.Into(ctx, logger), r)
	})
}
