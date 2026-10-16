// Command starter consumes the queued S3 upload events and launches a
// processing workflow for each one.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sfn"

	"docflow/internal/logging"
	"docflow/internal/pipeline"
	"docflow/internal/store"
)

func main() {
	logger := logging.NewFromEnv(os.Stdout)
	slog.SetDefault(logger)

	ctx := context.Background()
	stateMachine := strings.TrimSpace(os.Getenv("STATE_MACHINE_ARN"))
	if stateMachine == "" {
		logger.Error("cannot start: STATE_MACHINE_ARN is not set")
		os.Exit(1)
	}
	jobs, err := store.NewFromEnv(ctx)
	if err != nil {
		logger.Error("cannot start: job store", slog.String("error", err.Error()))
		os.Exit(1)
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		logger.Error("cannot start: aws config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	handler := pipeline.NewStarter(jobs, sfn.NewFromConfig(cfg), stateMachine)
	lambda.Start(func(ctx context.Context, batch events.SQSEvent) (events.SQSEventResponse, error) {
		return handler.Handle(logging.Into(ctx, logger), batch)
	})
}
