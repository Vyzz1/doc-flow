# DocFlow

A low-cost, serverless document-processing pipeline on AWS.

Status: initial Go scaffold. The Go files contain package declarations and TODOs;
the commands do not run Lambda handlers or process documents yet.

## Planned flow

API Gateway -> create job -> presigned S3 upload -> SQS -> starter -> Step Functions
Standard -> validate -> preprocess and thumbnail -> OCR -> extract -> save results.
The client polls job status through the get-job API.

Tesseract is the default OCR provider. Textract fallback requires a per-user daily
quota. See [AGENTS.md](AGENTS.md) for architecture, security, and cost constraints.

## Layout

- `cmd/`: future Lambda entrypoints: create-job, get-job, starter, and worker.
- `internal/`: shared domain, storage, validation, processing, quota, and logging packages.
- `infra/`, `web/`, and CI configuration will be added when implementation begins.

The worker placeholder will be split into individual processing Lambda commands
as those steps are implemented. `cmd/` holds executable entrypoints; `internal/`
holds their reusable logic.

## Local checks

Requires Go 1.26.2 or newer, GNU Make, and golangci-lint v2 (verified with
v2.12.2). Install the linter using its [official installation instructions](https://golangci-lint.run/docs/welcome/install/local/).
No external application dependencies are installed.

```sh
make lint test
make build
make fmt
```

`make lint` checks errors, static analysis, unused code, and formatting.
`make fmt` applies gofmt and goimports. `make build` produces Linux/arm64
executables at `bin/<command>/bootstrap` with cgo disabled. These are still
placeholder commands, not functioning Lambda handlers.

There are no tests yet because no behavior has been implemented. Integration
tests, the local Docker environment, frontend lint, and CDK checks remain TODOs
for the next stage; their Make targets will be added alongside those components.
