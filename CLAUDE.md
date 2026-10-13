@AGENTS.md

# CLAUDE.md

Claude Code-specific notes. Shared project rules live in `AGENTS.md` (imported above); keep them there, not here.

## Workflow

- For any non-trivial change (new Lambda, data-model change, new AWS resource), **plan first**: list the files
  you will touch and the cost/IAM impact, then wait for confirmation before editing.
- Prefer small diffs. After editing Go code, run `make lint test`; after editing `infra/`, run `make synth`.
- Use `make test-int` (Docker required) for anything under `internal/store`.
- Do not run deploy or AWS write commands. Read-only inspection (`cdk diff`, `cdk synth`) is fine.

## Explore before writing

- Check `internal/` for an existing helper before creating a new one.
- Check `docs/adr/` before changing architecture; a decision may already exist.
- Match existing patterns for handlers, logging fields (`jobId`, `userId`), and error wrapping.

## Things to avoid

- Adding dependencies without asking. Prefer the standard library and the AWS SDK for Go v2.
- Using cgo in Lambda builds (keep `CGO_ENABLED=0`; the OCR Lambda shells out to the `tesseract` binary instead).
- Changing DynamoDB key schema, GSIs, or the job status state machine without an ADR.
- Calling Textract / Rekognition from tests or local runs; use the fake `OCRClient`.

