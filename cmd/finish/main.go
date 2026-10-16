// Command finish records a job's terminal status, on both the success path
// and the state machine's catch path.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"docflow/internal/logging"
	"docflow/internal/pipeline"
	"docflow/internal/store"
)

func main() {
	logger := logging.NewFromEnv(os.Stdout)
	slog.SetDefault(logger)

	jobs, err := store.NewFromEnv(context.Background())
	if err != nil {
		logger.Error("cannot start: job store", slog.String("error", err.Error()))
		os.Exit(1)
	}

	handler := pipeline.NewFinish(jobs)
	lambda.Start(func(ctx context.Context, state pipeline.State) (pipeline.State, error) {
		return handler.Handle(logging.Into(ctx, logger), state)
	})
}
