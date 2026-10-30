# Vue Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the English DocFlow dashboard approved by the user.

**Architecture:** A static Vue app in `web/` uses an injectable API/auth boundary,
TanStack Vue Query for remote state, and shadcn-vue source components. Real mode
uses existing Cognito and HTTP endpoints; explicit demo mode stays local.

**Tech Stack:** Vue 3, TypeScript, Vite, Tailwind, shadcn-vue, TanStack Vue Query,
Amplify Auth, Vitest, Vue Testing Library, MSW.

**Spec:** `docs/superpowers/specs/2026-10-30-vue-dashboard-design.md`

## Global Constraints

- English throughout the interface; source in `web/`.
- Existing Go API shapes and document MIME allowlist remain authoritative.
- No AWS account operations, infrastructure deployment or new services.
- Poll every 3 seconds only while PENDING or PROCESSING; no background polling.
- No automatic create-job mutation retries; retain partial upload state.
- Use session-scoped auth tokens and clear query data on account changes.
- Verify repository lint/tests and linux/arm64 build, plus frontend checks.
- Implement inline in the authorized workspace; preserve existing edits and
  leave changes uncommitted for review. Do not introduce a worktree/merge flow.

## Review Focus

1. A failed S3 upload must retain job ID and retry the same signed upload.
2. ID documents must reject PDF despite invoices accepting PDF.
3. Terminal and permanent-error jobs must stop automatic polling.
4. Signing out during an in-flight request must not leak data into a new account.
5. DONE without result and nullable collections must render safe empty states.

## Task 1: Foundation and contract boundary

**Files:** `web/package.json`, lockfile, Vite/TS/ESLint/Vitest configuration,
`web/components.json`, `web/src/components/ui/`, `web/src/lib/{types,config,files,api,demo}.ts`,
`web/src/lib/*.test.ts`, `web/src/test/{setup,fixtures}.ts`.

**Interfaces:** `DocumentType`, `Job`, `UploadTicket`, `DocFlowApi` with
`createJob(docType, contentType, signal?)`, `upload(ticket, file, signal?)`,
`getJob(id, signal?)`; `AuthService` with session and SRP sign-in methods.

- [ ] Set up focused Vue/Vite tooling and generate the required shadcn-vue controls.
- [ ] Write failing tests for MIME/size checks, configuration, API serialization,
  S3 multipart ordering/no auth leakage, HTTP errors and one-time 401 refresh.
- [ ] Run `npm test -- --run` from `web/`; confirm failures before implementation.
- [ ] Implement the contracts, validation and fetch adapter with request timeout
  and cancellation; use local HTTP interception for contract tests.
- [ ] Implement a local-only stateful demo adapter matching the same contract.
- [ ] Run the Task 1 tests and TypeScript check; expect all pass.

## Task 2: Auth, upload and reactive job state

**Files:** `web/src/lib/auth.ts`, `web/src/composables/{useUpload,useJob}.ts`,
associated tests, `web/src/{main,router,runtime}.ts`.

**Interfaces:** Runtime provides API, auth, configuration and QueryClient.
`useUpload` exposes file/type state, pending ticket, errors and explicit submit;
`useJob` scopes queries by subject and valid job ID.

- [ ] Write failing tests for partial upload retries, expired tickets, duplicate
  submissions, query cancellation/account keys and stopping terminal polling.
- [ ] Run the tests; confirm the missing behavior fails.
- [ ] Implement SRP auth, refresh, new-password challenge and session storage;
  implement bounded retry policy, reactive job queries and upload composable.
- [ ] Implement hash routes, authentication guard and cache clearing on logout.
- [ ] Run all tests and TypeScript check; expect pass.

## Task 3: Accessible English workspace

**Files:** `web/src/App.vue`, `web/src/views/{SignIn,Upload,Job}View.vue`,
`web/src/components/{AppShell,StatusBadge,StepTimeline,JobResult,SetupNotice}.vue`,
`web/src/style.css`, component tests, `web/index.html`.

**Interfaces:** Views consume the Task 2 runtime/composables. Components take
typed API data and never render OCR via HTML.

- [ ] Write failing component tests for failed and missing-result jobs, nullable
  fields/steps, literal OCR markup, login challenge and invalid upload feedback.
- [ ] Run tests to observe the missing UI behavior.
- [ ] Build the responsive workspace, forms, upload drop area, status timeline,
  results tabs, clipboard feedback, empty/loading/error and configuration states.
- [ ] Run all frontend tests, lint and production build; expect pass.

## Task 4: Integration, docs and final verification

**Files:** `Makefile`, `.github/workflows/ci.yml`, `.gitignore`, `README.md`,
`AGENTS.md`, `docs/architecture.md`, `docs/adr/0004-vue-dashboard.md`,
`web/.env.example`, `web/README.md`.

- [ ] Wire frontend lint/test/build targets and a required frontend CI job.
- [ ] Document Node setup, explicit demo, real public configuration, S3 origin
  setup, auth limitations, static output and the absence of server job history.
- [ ] Run `make lint test build`, frontend tests/build and `git diff --check`.
- [ ] Serve the production demo build locally and verify the HTTP entrypoint;
  perform browser QA if a supported local browser capability is available.
- [ ] Request a fresh read-only review against the approved spec; fix important
  findings with regression tests and rerun affected checks.
- [ ] Record completed tasks, verification evidence and any limitations below.

## Execution record

- Design approved by the user on 2026-10-30.
- Native execution selected to continue the already-authorized implementation.
- Preflight: Tasks 2 and 3 consume Task 1 API types; Task 3 consumes Task 2
  composables. No contract conflict found.
- Existing `.gitattributes` edit is preserved. No commits, worktree creation,
  publication or AWS access will be performed.
- User subsequently requested a commit and will try the UI themselves.
- Implemented Tasks 1–3 and the Task 4 tooling/documentation. Verified 39
  frontend tests, frontend lint/typecheck, real and demo production builds,
  two headless Chrome desktop/mobile checks, and the baseline Go unit suite.
- The combined `make lint test build` run passed root Go lint and reached infra
  lint, but had not completed when the user requested handoff. No claim is made
  that this full command or linux/arm64 build passed for this change.
- Independent review was interrupted at the user's request; it is incomplete.
- No AWS account actions or deployment were performed.
