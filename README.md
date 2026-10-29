# DocFlow

A low-cost, serverless document-processing pipeline on AWS.

Status: the backend is implemented and has been deployed to a dev stack, where
`scripts/smoke-dev.bash` runs a document from upload to `DONE`. The dashboard
(`web/`) is not started.

## Flow

API Gateway -> create job -> presigned S3 upload -> SQS -> starter -> Step Functions
Standard -> validate -> recognize (render, thumbnail, OCR) -> extract (fields and
save) -> mark done. The client polls job status through the get-job API.

Tesseract is the default OCR provider. Textract fallback requires a per-user daily
quota. [docs/architecture.md](docs/architecture.md) describes how the pieces fit;
[AGENTS.md](AGENTS.md) holds the constraints they answer to.

## Layout

- `cmd/`: the Lambdas. `create-job` and `get-job` serve the API; `starter`,
  `validate`, `recognize`, `extract`, and `finish` make up the processing
  workflow.
- `internal/model`: domain types: `Job` and its status state machine,
  `StepResult`, `DocumentResult`, and the `WorkflowInput` payload passed between
  Step Functions states.
- `internal/logging`: structured JSON logging carrying `jobId` and `userId`.
- `internal/api`: the HTTP API's request handling, so the entrypoints stay thin.
- `internal/store`: the single-table DynamoDB persistence behind a `JobStore`
  interface.
- `internal/storage`: presigned S3 uploads behind a `Storage` interface.
- `internal/validation`: recognizes documents by their leading bytes.
- `internal/ocr`: the `Client` interface, the Tesseract subprocess, and a fake.
- `internal/quota`: the per-user daily allowance for paid OCR.
- `internal/preprocess`: normalizes pages for OCR and makes thumbnails.
- `internal/extract`: turns recognized text into the document's fields.
- `internal/pipeline`: the workflow's step logic and its two error kinds.
- `internal/`: the remaining shared processing packages.
- `.github/workflows/ci.yml`: lint, test, linux/arm64 build, vulnerability
  scan, and the merge step.
- `docs/architecture.md`: how the pieces fit, what is decided, and what is
  not verified.
- `docs/adr/`: architecture decision records.
- `infra/`: the CDK app, a separate Go module holding the table, bucket,
  queues, Lambdas, workflow, HTTP API, and Cognito user pool.
- `scripts/`: `smoke-dev.bash`, the end-to-end check against a deployed stack.
- `web/` will be added when the dashboard starts.

`cmd/` holds executable entrypoints; `internal/` holds their reusable logic.

## Local checks

Requires Go 1.26.10 or newer, GNU Make, and golangci-lint v2 (verified with
v2.12.2). The floor is 1.26.10 rather than 1.26.2 because govulncheck reports
eleven stdlib advisories, in `net/http`, `net/textproto`, `crypto/tls`, `crypto/x509`, and
`net/url`, as reachable once the AWS SDK makes real HTTP calls; they are all
fixed in 1.26.10. If `go build` reports `go.mod requires go >= 1.26.10`, install
it or set `GOTOOLCHAIN=auto` to let Go fetch it. Install the linter using its [official installation instructions](https://golangci-lint.run/docs/welcome/install/local/).
No external application dependencies are installed.

```sh
make lint test       # both Go modules
make build           # linux/arm64 bootstrap binaries
make test-int        # integration tests; starts the local services first
make test-infra      # CDK assertion tests
make synth           # synthesize the stack (builds the assets first; needs Docker)
make up / make down  # DynamoDB Local and S3Mock
make fmt
```

`make synth` needs Docker too: the recognition function is a container
image, and synthesizing a Docker asset builds it. On an amd64 machine that
means QEMU, which Docker Desktop provides and CI sets up explicitly.

`make test-int` needs Docker. It runs the tests behind the `integration`
build tag against DynamoDB Local and S3Mock, which is where the conditional
writes and the ranged reads are shown to work rather than merely to be
sent. The endpoints are passed in rather than defaulted in code, so a run
can never reach a real account by forgetting to set something.

`infra/` is a separate module, so `./...` at the root never reaches it;
`make lint` covers both, and `make test` deliberately does not. Synthesizing
runs the CDK's JavaScript in a Node subprocess through jsii, which takes
seconds rather than milliseconds, so those tests have their own target.

`make lint` checks errors, static analysis, unused code, and formatting. CI runs
the same targets, so a green `make lint test build` locally means a green build.
`make fmt` applies gofmt and goimports. `make build` produces Linux/arm64
executables at `bin/<command>/bootstrap` with cgo disabled; these are the
Lambda handlers the stack deploys.

Every package under `internal/` has table-driven unit tests against fakes,
and `make test-infra` asserts on the synthesized template. Frontend lint
waits for `web/`.

## API

Two endpoints, both behind an HTTP API with a Cognito JWT authorizer. Each
reads its caller from the verified `sub` claim rather than from anything the
client sends, and that claim is the storage key: a job lives under
`USER#<sub>`, so one user's id cannot reach another's data.

Every route names the authorizer explicitly. The API sets no default one, so
a route that forgot it would be open rather than quietly protected, and a
test asserts none is.

Getting a token in dev, where the app client allows plain passwords:

```sh
aws cognito-idp admin-create-user --user-pool-id "$POOL" --username you@example.test
aws cognito-idp admin-set-user-password --user-pool-id "$POOL"   --username you@example.test --password '...' --permanent
aws cognito-idp initiate-auth --auth-flow USER_PASSWORD_AUTH   --client-id "$CLIENT" --auth-parameters USERNAME=you@example.test,PASSWORD='...'
```

Production allows SRP only, and self sign-up is off everywhere: anyone who
could create an account could choose their own `sub`.

### `POST /jobs`

```json
{ "docType": "invoice", "contentType": "application/pdf" }
```

Returns `201` with the job id and a presigned upload:

```json
{
  "jobId": "01J8ZQJ9X7K3M4N5P6Q7R8S9TV",
  "status": "PENDING",
  "uploadUrl": "https://<bucket>.s3.amazonaws.com",
  "uploadFields": { "key": "uploads/<user>/<job>", "policy": "..." },
  "maxBytes": 10485760,
  "expiresAt": "2026-10-16T08:45:00Z"
}
```

The client then sends `multipart/form-data` to `uploadUrl`: every pair in
`uploadFields` first, then the file. It is a POST rather than a PUT because
only a POST policy can bound the body size, which S3 then enforces itself.

### `GET /jobs/{id}`

Returns the job, its per-step progress, and, once the job is `DONE`, its
result. Ownership needs no separate check: a job lives under its owner's
partition key, so another user's id simply does not find it, and a caller
cannot learn whether someone else's job exists.

A `FAILED` job carries a `failureReason`, a sentence written for the person
who uploaded the document, such as `The file is not a supported document type.`
It never holds the pipeline's own wording, a file's bytes, or a resource name.
The technical detail stays in the function logs and in the failed execution's
history, under the same `jobId`. A failure with no known cause reads `The
document could not be processed.`, and one that exhausted its retries reads
`processing failed`.

A malformed id is rejected before any read, so a bad path costs no capacity.

## Infrastructure

`infra/` is a CDK app written in Go, in its own module so the CDK's large,
build-time-only dependency tree stays out of the Lambda module's graph.

**It still needs Node.** The Go API is a jsii binding: it drives the CDK's
JavaScript in a Node subprocess, and the `cdk` CLI is itself a Node program.
Writing the app in Go removes the npm dependencies from the repository, not
the Node requirement.

It currently creates:

| Resource | Notes |
|---|---|
| DynamoDB table | single table, on-demand, TTL on `expiresAt` |
| S3 upload bucket | private, SSE, TLS enforced, 7-day expiry, CORS for the presigned POST |
| HTTP API | `POST /jobs` and `GET /jobs/{id}`, each naming the Cognito JWT authorizer |
| Cognito user pool and app client | Lite plan, self sign-up off |
| Lambdas | arm64, `provided.al2023`, from `bin/<command>` |
| `recognize` | arm64 container image from `build/ocr`, carrying Tesseract and poppler: 81 MB |
| Upload queue and its dead letters | three attempts, then the message stops being redelivered |
| S3 notification | filtered to `uploads/`, so derived artifacts do not start a workflow |
| Step Functions workflow | Standard; `Validate`, `Recognize`, `Extract`, `MarkDone`, each with a catch routing to `MarkFailed` |
| Log groups | one week of retention, set explicitly |

The handlers read their caller from the JWT authorizer's verified `sub`;
[ADR 0002](docs/adr/0002-cognito-for-authentication.md) records why Cognito
issues the tokens.

Two stages, chosen with `-c stage=`:

```sh
make build                          # the stack mounts bin/<command> as assets
cd infra
npx cdk synth                       # dev: DESTROY on teardown, debug logging
npx cdk synth -c stage=prod         # prod: RETAIN, info logging, no auto-delete helper
npx cdk synth -c uploadOrigins=https://app.example   # restrict the upload CORS
```

Deploying is a manual step and needs your own AWS account: `make synth`,
then `cd infra && npx cdk diff` and `npx cdk deploy`, after a one-time
`npx cdk bootstrap`. The stack outputs the API URL, user pool and client ids,
the state machine, the table, the bucket, and the dead-letter queue. Check a
deployment with the [smoke test](#smoke-test). `npx cdk destroy` removes the
dev stack, upload bucket included.

Nothing in the stack runs when idle. The table bills per request, Lambda bills
per invocation, and the log groups expire, which is the one cost that
otherwise accrues on its own.

IAM is written out statement by statement rather than taken from the CDK's
`Grant*` helpers, which hand over more verbs than either function uses:
`create-job` gets `dynamodb:PutItem` and `s3:PutObject` under `uploads/*`,
`get-job` gets `dynamodb:GetItem` and `dynamodb:Query`. A test asserts the
production template contains no wildcard action or resource, and another
asserts the stack creates nothing that bills while idle.

`govulncheck` runs over the root module only. The CDK tree is build-time
tooling that never reaches a deployed artifact, and scanning it would report
on code that does not ship.

## Smoke test

`scripts/smoke-dev.bash` runs documents from upload to a terminal status
against a deployed stack. It is the only check that touches real AWS, so it is manual
and not part of CI.

```sh
aws configure                       # once, region ap-southeast-1
bash scripts/smoke-dev.bash         # draws its own documents with Pillow
bash scripts/smoke-dev.bash scan.pdf   # or send one png, jpg or pdf of yours
```

Needs `curl`, `jq`, and Python with Pillow when no file is given. It reads the
API URL and Cognito ids from the stack outputs (`STACK`, default
`DocFlow-dev`), creates a throwaway user and deletes it on exit. It checks that:

- `POST /jobs` without a token returns 401;
- S3 refuses an upload of the wrong content type (403) and one over
  `maxBytes` (400 or 403), which S3Mock cannot prove;
- a PNG invoice reaches `DONE` with extracted fields within `TIMEOUT`
  seconds (default 180);
- a two page PDF reaches `DONE` and reports two pages;
- a file that is not a PNG ends `FAILED` in `validate`, and a PNG signature
  with nothing behind it ends `FAILED` in `ocr`, each with a failure reason.

With a file argument it runs only the happy path on that file.

It creates a few DynamoDB items and S3 objects, which expire after seven
days, and uses Lambda and Step Functions within the free tier.

## Configuration

| Variable | Used by | Meaning |
|---|---|---|
| `LOG_LEVEL` | `internal/logging` | `debug`, `info` (default), `warn`, or `error` |
| `TABLE_NAME` | `internal/store` | the single DynamoDB table; required |
| `DYNAMODB_ENDPOINT` | `internal/store` | overrides the endpoint, for DynamoDB Local |
| `STATE_MACHINE_ARN` | `cmd/starter` | the workflow to launch; required |
| `OCR_PROVIDER` | `internal/ocr` | `tesseract` (default) or `textract` |
| `TEXTRACT_DAILY_PAGES` | `internal/quota` | paid pages per user per UTC day, 20 by default; `0` refuses every fallback |
| `UPLOAD_BUCKET` | `internal/storage` | the private upload bucket; required |
| `S3_ENDPOINT` | `internal/storage` | overrides the endpoint and uses path style, for MinIO |

The table's TTL must be configured on the `expiresAt` attribute, which every
item carries as Unix seconds seven days out.

## CI

One workflow, `.github/workflows/ci.yml`, on pushes to the trunk, on pull
requests, Mondays at 02:00 UTC, and on demand:

| Job | Does |
|---|---|
| `Go lint, test, build` | `make lint`, `go test -race -shuffle=on`, `make build`, tidy and clean-tree check |
| `CDK lint, test, synth` | the infra module's lint and assertion tests, then a synth with the container image |
| `Integration tests` | the `integration` build tag against DynamoDB Local and S3Mock |
| `govulncheck` | `govulncheck ./...` |
| `Merge` | squash-merges the pull request and deletes the branch |

CI runs the same Make targets you run locally, so a green `make lint test build`
means a green build.

### Merge

The merge job `needs` the other four, so it runs only when lint, test, build,
and the vulnerability scan have all passed. It merges pull requests opened by
the repository owner, an organization member, or a collaborator, from a branch
in this repository, that are not drafts. Bots, outside contributors, and forks
are excluded, which is why the plain `pull_request` trigger is safe: GitHub
withholds secrets from fork runs.

Setup is one step: add a repository secret named `AUTO_MERGE_TOKEN` holding a
fine-grained personal access token scoped to this repository only, with
`Contents: read and write` and `Pull requests: read and write`, and nothing
else. Give it an expiry and renew it. No branch protection and no "Allow
auto-merge" setting are needed, because the job merges directly rather than
queueing.

The flip side is that the gate is this workflow alone. A check added elsewhere
later will not hold the merge back unless it becomes a job here, and a
collaborator's pull request merges with no human review once the jobs are
green.

### Dependency and vulnerability updates

`govulncheck` fails the build only when vulnerable code is actually reachable.
With no dependencies yet it audits the Go version in `go.mod` against the
stdlib advisories; fixing one of those means bumping the `go` directive by
hand, since Dependabot does not do it.

`.github/dependabot.yml` opens weekly update pull requests for Go modules and
for the actions used here, grouped to keep the noise down. They are **not**
merged automatically: the merge job excludes bots, so a bump lands only after
you approve it. Enable "Dependabot security updates" in the repository settings
to get advisory patches outside the weekly schedule.
