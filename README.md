# DocFlow

A low-cost, serverless document-processing pipeline on AWS.

Status: early Go scaffold. `internal/model` holds the implemented domain types;
every other package is a package declaration with TODOs, and the commands do not
run Lambda handlers or process documents yet.

## Planned flow

API Gateway -> create job -> presigned S3 upload -> SQS -> starter -> Step Functions
Standard -> validate -> preprocess and thumbnail -> OCR -> extract -> save results.
The client polls job status through the get-job API.

Tesseract is the default OCR provider. Textract fallback requires a per-user daily
quota. See [AGENTS.md](AGENTS.md) for architecture, security, and cost constraints.

## Layout

- `cmd/create-job`, `cmd/get-job`: the API Lambdas. `cmd/starter` and
  `cmd/worker` are still placeholders.
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
- `internal/`: the remaining shared processing packages.
- `.github/workflows/ci.yml`: lint, test, linux/arm64 build, vulnerability
  scan, and the merge step.
- `docs/adr/`: architecture decision records.
- `infra/`: the CDK app, a separate Go module holding the table, bucket, and
  Lambdas.
- `web/` will be added when implementation begins.

The worker placeholder will be split into individual processing Lambda commands
as those steps are implemented. `cmd/` holds executable entrypoints; `internal/`
holds their reusable logic.

## Local checks

Requires Go 1.26.9 or newer, GNU Make, and golangci-lint v2 (verified with
v2.12.2). The floor is 1.26.9 rather than 1.26.2 because govulncheck reports
ten stdlib advisories, in `net/http`, `net/textproto`, `crypto/tls`, and
`net/url`, as reachable once the AWS SDK makes real HTTP calls; they are all
fixed in 1.26.9. If `go build` reports `go.mod requires go >= 1.26.9`, install
it or set `GOTOOLCHAIN=auto` to let Go fetch it. Install the linter using its [official installation instructions](https://golangci-lint.run/docs/welcome/install/local/).
No external application dependencies are installed.

```sh
make lint test       # both Go modules
make build           # linux/arm64 bootstrap binaries
make test-infra      # CDK assertion tests
make synth           # synthesize the stack (builds the assets first)
make fmt
```

`infra/` is a separate module, so `./...` at the root never reaches it;
`make lint` covers both, and `make test` deliberately does not. Synthesizing
runs the CDK's JavaScript in a Node subprocess through jsii, which takes
seconds rather than milliseconds, so those tests have their own target.

`make lint` checks errors, static analysis, unused code, and formatting. CI runs
the same targets, so a green `make lint test build` locally means a green build.
`make fmt` applies gofmt and goimports. `make build` produces Linux/arm64
executables at `bin/<command>/bootstrap` with cgo disabled. These are still
placeholder commands, not functioning Lambda handlers.

`internal/model`, `internal/logging`, and `internal/store` are covered by
table-driven unit tests; the store is tested against a fake of the four
DynamoDB calls it makes. The other packages have no tests yet because no
behavior has been implemented.

Integration tests against DynamoDB Local are **not** wired up yet, so there is
no `make test-int` target despite AGENTS.md mentioning one. The local Docker
environment, frontend lint, and CDK checks remain TODOs for the next stage.

## API

Two endpoints exist. Both sit behind a JWT authorizer, and both read the
caller from the verified `sub` claim rather than from anything the client
sends.

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
| `create-job`, `get-job` Lambdas | arm64, `provided.al2023`, from `bin/<command>` |
| Log groups | one week of retention, set explicitly |

**There is no API Gateway yet.** The handlers read their caller from a JWT
authorizer, and which issuer signs those tokens is still an open decision, so
the HTTP API is the first job of the next phase. Until then the functions
deploy but nothing routes to them.

Two stages, chosen with `-c stage=`:

```sh
make build                          # the stack mounts bin/<command> as assets
cd infra
npx cdk synth                       # dev: DESTROY on teardown, debug logging
npx cdk synth -c stage=prod         # prod: RETAIN, info logging, no auto-delete helper
npx cdk synth -c uploadOrigins=https://app.example   # restrict the upload CORS
```

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

## Configuration

| Variable | Used by | Meaning |
|---|---|---|
| `LOG_LEVEL` | `internal/logging` | `debug`, `info` (default), `warn`, or `error` |
| `TABLE_NAME` | `internal/store` | the single DynamoDB table; required |
| `DYNAMODB_ENDPOINT` | `internal/store` | overrides the endpoint, for DynamoDB Local |
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
| `govulncheck` | `govulncheck ./...` |
| `Merge` | squash-merges the pull request and deletes the branch |

CI runs the same Make targets you run locally, so a green `make lint test build`
means a green build.

### Merge

The merge job `needs` the other two, so it runs only when lint, test, build,
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
