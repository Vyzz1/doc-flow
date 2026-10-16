# 1. A job may fail straight from PENDING

Date: 2026-10-16

Status: accepted

## Context

A job is created `PENDING` by the create-job Lambda, which hands the client a
presigned URL. The client uploads, S3 emits an event, SQS delivers it, and the
starter begins the workflow.

`AGENTS.md` wrote the lifecycle as `PENDING -> PROCESSING -> DONE|FAILED`.
Read strictly, that forbids `PENDING -> FAILED`, and the first implementation
of `internal/model` enforced it that way.

That reading costs a write in the case the starter is most likely to hit
first. When the starter finds the uploaded object is not a supported document
— wrong magic bytes, oversized, truncated — nothing has been processed, but
the job cannot be marked `FAILED` until it has first been marked
`PROCESSING`. The intermediate write exists only to satisfy the state
machine. It is a second conditional write against DynamoDB, and a window in
which a reader sees a job as `PROCESSING` when no processing will ever occur.

## Options considered

1. **Keep `PENDING -> FAILED` forbidden.** Matches the sentence in
   `AGENTS.md` exactly. The starter writes `PROCESSING` and then `FAILED`.
   Two writes, and a misleading intermediate state.

2. **Allow `PENDING -> FAILED`.** The starter fails the job in one write. The
   lifecycle gains an edge, but keeps the property that matters: a job never
   moves backwards, and the terminal states stay terminal.

3. **Add a distinct `REJECTED` status for pre-processing failures.** Honest
   about the difference, but it splits every "did this job fail" check in the
   API, the dashboard, and the metrics into two, for a distinction callers
   have not asked for. The failure reason already carries that detail.

## Decision

Allow `PENDING -> FAILED`.

```
PENDING ---> PROCESSING ---> DONE
   |               \-------> FAILED
   \-----------------------> FAILED
```

`model.FailableFrom` is the single list of statuses a job may fail from, and
`internal/store` builds its conditional write from it, so the rule cannot
drift between the domain and the table.

`UpdateStatus` still refuses `FAILED` and defers to `FailJob`, because a
failure must carry a reason.

## Consequences

- The starter fails an invalid upload in one conditional write, and no reader
  ever sees a job as `PROCESSING` that was never processed.
- Failing a `DONE` or `FAILED` job is still a conflict, not a no-op. The
  conditional write reports `ErrStatusConflict`, and the caller decides
  whether a redelivery made it harmless.
- A job nobody ever uploads for still expires as `PENDING`. Nothing fails it;
  the TTL removes it after seven days.
- `AGENTS.md` has been updated to match, with a pointer here. The sentence it
  used to carry is the one this record overrules.
- Anything that treats `FAILED` as implying work was attempted is now wrong.
  Today nothing does: the per-step items under `JOB#<id> / STEP#<name>` are
  what say which steps ran, and a job failed from `PENDING` simply has none.
