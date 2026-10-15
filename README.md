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

- `cmd/`: future Lambda entrypoints: create-job, get-job, starter, and worker.
- `internal/model`: implemented domain types: `Job` and its status state machine,
  `StepResult`, `DocumentResult`, and the `WorkflowInput` payload passed between
  Step Functions states.
- `internal/`: the remaining shared storage, validation, processing, quota, and
  logging packages.
- `.github/workflows/ci.yml`: lint, test, linux/arm64 build, vulnerability
  scan, and the merge step.
- `infra/` and `web/` will be added when implementation begins.

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

`make lint` checks errors, static analysis, unused code, and formatting. CI runs
the same targets, so a green `make lint test build` locally means a green build.
`make fmt` applies gofmt and goimports. `make build` produces Linux/arm64
executables at `bin/<command>/bootstrap` with cgo disabled. These are still
placeholder commands, not functioning Lambda handlers.

`internal/model` is covered by table-driven unit tests. The other packages have
no tests yet because no behavior has been implemented. Integration tests, the
local Docker environment, frontend lint, and CDK checks remain TODOs for the next
stage; their Make targets will be added alongside those components.

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
