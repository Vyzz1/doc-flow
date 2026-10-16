// Command get-job is the Lambda behind GET /jobs/{id}. It reports a job's
// status, its per-step progress, and its result once the job is done.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"docflow/internal/api"
	"docflow/internal/logging"
	"docflow/internal/store"
)

func main() {
	logger := logging.NewFromEnv(os.Stdout)
	slog.SetDefault(logger)

	// Built here, not per invocation, so a warm container reuses the client.
	jobs, err := store.NewFromEnv(context.Background())
	if err != nil {
		logger.Error("cannot start: job store", slog.String("error", err.Error()))
		os.Exit(1)
	}

	handler := api.NewGetJob(jobs)
	lambda.Start(func(ctx context.Context, r api.Request) (api.Response, error) {
		return handler.Handle(logging.Into(ctx, logger), r)
	})
}
