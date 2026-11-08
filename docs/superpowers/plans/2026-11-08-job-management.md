# Job management frontend

Implement the existing list/delete backend in the English Vue workspace. Keep
the current upload route; add `/jobs` as My documents with newest-first rows,
20-item cursor pagination, refresh, loading/error/empty states and detail links.
Add a reusable accessible confirmation dialog from the existing Reka primitives
for deleting one job from either list or detail. Disable deletion for PROCESSING.

Contracts read: `internal/api/listjobs.go`, `internal/api/deletejob.go`, their
tests, and README API documentation. GET `/jobs?limit=20&next=<jobId>` returns
`{jobs, next?}`; DELETE `/jobs/{id}` returns empty 204, 409 for PROCESSING, 404
when absent. A 500 may follow deletion of records with incomplete file cleanup.
Preserve that message, refresh caches after all delete outcomes, and do not
retry deletes automatically. Keep dialogs independent of list rows so an error
remains visible even if a partial deletion makes the row disappear.

Use account-scoped TanStack infinite queries and mutation invalidation. Cancel
detail reads before removing deleted cache entries; guard mutation callbacks
against account changes. Keep list refresh manual to limit backend reads. Extend
the in-memory demo to model ordering, pagination and processing conflicts.

Steps:

- [x] Add HTTP contract tests, then list/delete adapter methods and shared types.
- [x] Test and implement infinite list queries, deletion lifecycle and demo parity.
- [x] Add My documents, confirmation dialog, navigation and detail deletion.
- [x] Test list pagination, failure/empty states, confirmation, processing refusal,
  partial deletion, cache cleanup and account isolation.
- [x] Update docs; run formatting, lint, unit tests, production build and local
  browser checks. Run required `make lint test`, with shared cache access if needed.

No backend changes, new AWS resources, live AWS operations or deployment.
User asked to read and implement the existing APIs; use the established UI style.

## Verification

- Frontend formatting, ESLint, TypeScript and production build passed.
- All 60 frontend tests passed, including 21 new list/delete tests.
- Both expanded Playwright scenarios passed in local Chrome: desktop and mobile,
  list/detail deletion, confirmation cancellation and empty state. No external
  requests in the demo scenario; screenshots inspected.
- A fresh read-only review found no important issues.
- `make test NPM=npm.cmd` passed (Go and frontend), as did `make build`
  for linux/arm64. Root Go lint passed with zero issues. The combined
  `make lint test` reached infra lint but was stopped at the user's request
  to commit; full infra lint is not claimed as passing.
- User authorized committing the completed frontend changes.
