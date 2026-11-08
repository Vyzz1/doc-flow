// Command delete-job is the Lambda behind DELETE /jobs/{id}. It removes a
// finished job, its stored result, and its files.
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

	// Built here, not per invocation, so a warm container reuses the clients.
	jobs, err := store.NewFromEnv(context.Background())
	if err != nil {
		logger.Error("cannot start: job store", slog.String("error", err.Error()))
		os.Exit(1)
	}
	objects, err := storage.NewS3FromEnv(context.Background())
	if err != nil {
		logger.Error("cannot start: object storage", slog.String("error", err.Error()))
		os.Exit(1)
	}

	handler := api.NewDeleteJob(jobs, objects)
	lambda.Start(func(ctx context.Context, r api.Request) (api.Response, error) {
		return handler.Handle(logging.Into(ctx, logger), r)
	})
}
