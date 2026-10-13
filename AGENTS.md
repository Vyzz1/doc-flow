# AGENTS.md

Guidance for AI coding agents working in this repository.

## Project

**DocFlow**: a serverless, asynchronous document-processing pipeline on AWS.
Users upload invoices / ID documents / images; the system validates, preprocesses, runs OCR,
extracts fields, and reports job progress.

Flow:

```
Client -> API Gateway (HTTP API) -> Lambda create-job -> presigned S3 URL + job PENDING in DynamoDB
Client -> S3 (uploads/) -> S3 Event -> SQS -> Lambda starter -> Step Functions (Standard)
  Validate -> [Preprocess + Thumbnail] -> OCR (Tesseract; fallback Textract if low confidence)
           -> Extract fields -> Save results
  Catch -> mark FAILED -> SQS DLQ
Progress: GET /jobs/{id} polling (WebSocket API is a stretch goal)
Dashboard: static React app on S3 + CloudFront
```

## Hard constraints (read first)

- **Free-tier / low-cost project.** Never add NAT Gateway, Express Workflows, OpenSearch, RDS,
  or any always-on resource. Ask before adding any service not listed above.
- **Textract and Rekognition cost money.** Default OCR is Tesseract. Textract is only a fallback
  behind a per-user daily quota. Rekognition is optional, behind a feature flag, off by default.
- **Never run `cdk deploy`, `aws ...` write commands, or anything touching a real AWS account
  unless the user explicitly asks.** Prefer `cdk synth` / `cdk diff` and local tests.
- Never commit secrets, AWS keys, or `.env` files.

## Tech stack

| Area | Choice |
|---|---|
| Lambda code | Go, `arm64`, runtime `provided.al2023` (OCR Lambda: container image) |
| Infrastructure | AWS CDK in TypeScript (`infra/`) |
| Data | DynamoDB single-table design |
| Orchestration | Step Functions Standard, SQS (+ DLQ) |
| CI/CD | GitHub Actions (dev / prod environments) |

## Repository layout

```
infra/        CDK stacks (TypeScript)
functions/    one directory per Lambda: create-job, starter, validate, thumbnail, ocr, extract, ...
internal/     shared Go packages: model, store (DynamoDB repo), ocr, quota, logging
docs/         architecture.md, ADRs, cost-analysis.md
.github/      workflows
```

## Commands

> Adjust if the Makefile differs; the Makefile is the source of truth.

```bash
make build        # build all Go Lambdas (GOOS=linux GOARCH=arm64, CGO_ENABLED=0)
make test         # unit tests: go test ./...
make test-int     # integration tests (needs Docker: DynamoDB Local, MinIO)
make lint         # golangci-lint run + eslint for web/ and infra/
make synth        # cd infra && npx cdk synth
make up           # docker compose up -d (DynamoDB Local, MinIO)
make down         # docker compose down -v
```

Run `make lint test` before declaring a task done. Run `make test-int` when touching
`internal/store` or any DynamoDB access pattern.

## Go conventions

- Go version per `go.mod`. Format with `gofmt`/`goimports`. Lint must pass.
- Wrap errors with context: `fmt.Errorf("get job %s: %w", id, err)`. Do not swallow errors.
- Pass `context.Context` as the first argument everywhere; respect Lambda deadlines.
- Put external services behind small interfaces (`OCRClient`, `JobStore`, `Storage`) so they can be faked.
  Choose OCR implementation by `OCR_PROVIDER=tesseract|textract`.
- Structured JSON logging with `jobId` and `userId` as fields on every log line. No `fmt.Println`.
- Keep Lambda handlers thin: parse event -> call a function in `internal/` -> return.
- Initialize AWS SDK clients outside the handler (reused across warm invocations).
- Table-driven tests. No test may call real AWS.

## DynamoDB rules

Single table. Key design (do not change without an ADR):

| PK | SK | Content |
|---|---|---|
| `USER#<id>` | `JOB#<ulid>` | job status, file key, timestamps |
| `JOB#<id>` | `STEP#<name>` | per-step result / error / duration |
| `JOB#<id>` | `RESULT` | OCR text, extracted fields |
| `USER#<id>` | `QUOTA#<yyyy-mm-dd>` | daily page counter (atomic `ADD`) |

- Status transitions use **conditional writes**: `PENDING -> PROCESSING -> DONE|FAILED`. Never move backwards.
- All processing is **idempotent**: S3 events and SQS deliveries can repeat. Key idempotency on `jobId`.
- Items carry a TTL (7 days) so data and uploads expire automatically.
- Never `Scan` in application code paths. Add a GSI instead (and document it).

## Step Functions / error handling

- Distinguish transient errors (retry with exponential backoff + jitter) from permanent errors (fail fast, no retry).
- Every state that can fail has a `Catch` that marks the job `FAILED` and routes to the DLQ.
- Keep state payloads small; pass S3 keys and IDs, not file contents.
- Minimize state transitions per job (free tier allows ~4,000/month).

## Security

- S3 buckets are private, SSE enabled, no public ACLs. Uploads only via presigned URLs with
  size and content-type limits.
- IAM: least privilege, one role per Lambda, scoped to specific resources. No `*` actions or resources.
- Validate uploads by magic bytes, not just file extension or the declared content-type.
- Secrets live in SSM Parameter Store / Secrets Manager, never in code or environment defaults.

## Testing strategy

| Layer | Tooling | When |
|---|---|---|
| Unit | `go test` + fakes | every change |
| Integration | testcontainers-go (DynamoDB Local, MinIO) | store / data-access changes |
| State machine | Step Functions Local + mock config | workflow changes |
| E2E (real AWS, dev only) | `//go:build e2e` tag, low quota | manual, before release |

## Definition of done

- [ ] Code builds for `linux/arm64`; `make lint test` passes
- [ ] New/changed behavior covered by tests (including failure paths)
- [ ] No new always-on or paid resource; cost impact noted in the PR if any
- [ ] IAM permissions minimal; `cdk synth` succeeds
- [ ] Docs updated (`docs/` or an ADR) if architecture or data model changed

## How to work here

- Make small, focused changes. Do not refactor unrelated code.
- If a request conflicts with the hard constraints above, say so and propose an alternative instead of proceeding.
- Record significant design decisions as ADRs in `docs/adr/` (context, options considered, decision, consequences).
- When unsure about intent, ask one concise question rather than guessing.
