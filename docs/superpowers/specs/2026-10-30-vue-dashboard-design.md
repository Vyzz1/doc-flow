# DocFlow Vue dashboard

Status: approved by the user on 2026-10-30.

## Intent and decisions

Build the first DocFlow frontend inside `web/`. The user selected Vue,
shadcn-vue and TanStack Vue Query, with English throughout the interface.
The dashboard lets an existing user upload a document, follow processing,
and read extracted fields and recognized text using the existing backend.

## Approaches

- Naive UI: ready-made dashboard controls with less component ownership.
- PrimeVue or Vuetify: broad component suites, with more prescribed styling.
- shadcn-vue: source-owned components with Tailwind; more integration work,
  but direct control over appearance. Selected by the user.

Use Vue 3, TypeScript, Vite, Vue Router, Tailwind and shadcn-vue. Use
`@tanstack/vue-query` for remote state. No Pinia is needed for this scope.
The exact compatible dependency versions are an implementation decision.

## Proposed visual design

Builder proposal: a light, restrained workspace with slate text, blue accents,
subtle borders and clear status badges. Desktop uses a compact left navigation
and a main workspace; mobile collapses navigation into a simple header.
Typography uses the system font stack, with no external font requests.

- Sign in: product name, concise explanation, email and password fields,
  accessible errors and a pending button state.
- New document: document-type selector, drag-and-drop file input with a normal
  keyboard-accessible picker, selected filename/size, and an Upload button.
  A separate Open job input accepts a known job ID.
- Job details: copyable job ID, document type, creation time and status;
  step outcomes; tabs for extracted fields and recognized text when available.
  Fields show name, value and confidence. Result summary shows page count,
  OCR provider and overall confidence. Copy text is available with feedback.

Do not show invented throughput charts, account totals or a server-side job
history. The backend has no listing endpoint. Do not claim an exact processing
percentage: it exposes completed step outcomes, not continuous progress.

## Routes and authentication

Use hash-based Vue Router routes so the static build works on S3 without
requiring additional rewrite infrastructure: `/sign-in`, `/`, `/jobs/:id`
inside the hash. Protected routes preserve a safe internal return destination.

Use Cognito SRP through Amplify Auth, configured only with the existing pool
and app client identifiers. No hosted login, new pool, sign-up or external
identity service is required. Production enables SRP, not password-only auth.
Support the new-password challenge for administrator-created accounts.
For an unexpected challenge, show a clear unsupported-step message and allow
the user to restart sign-in. Account recovery UI is outside the MVP.

Builder proposal: use sessionStorage for the auth library's token storage,
refresh sessions through that library, never persist passwords, and never log
credentials or tokens. Send the Cognito ID token in the API Authorization
header to match the configured JWT audience. On sign-out, clear the Query
cache and cancel requests. Query keys include the authenticated subject and
job ID; changing users cannot reuse another user's cached data.

## Existing API integration

POST `/jobs` accepts `{ docType, contentType }`, returning jobId, status,
uploadUrl, uploadFields, maxBytes and expiresAt. Use one mutation for the
explicit create-and-upload action; disable automatic mutation retries because
creating a job is not an idempotent client operation. Prevent duplicate submits.

Check file size before creation (10 MiB) and again against maxBytes. Supported
types follow `internal/model/content_type.go`: invoice accepts PDF, JPEG, PNG,
TIFF; ID accepts JPEG and PNG; image accepts JPEG, PNG, TIFF. Resolve absent
browser MIME types from recognized extensions, reject unsupported files and
leave authoritative content validation to the backend.

Upload using multipart FormData, adding all uploadFields before the file.
Do not manually set the multipart Content-Type or send the API token to S3.
Treat any successful HTTP status as success; the S3 response may be empty.
After creating a job, retain its ID in the UI even if upload fails. Offer an
explicit retry of the same file and signed fields while they remain valid;
once expired, explain that a new submission is required. Never automatically
create another job after a partial failure. Processing continues remotely if
the browser leaves after upload.

GET `/jobs/{id}` returns status, docType, timestamps, steps, failureReason and
an optional result. Use a reactive query with cancellation via AbortSignal.
Poll every 3 seconds only while PENDING or PROCESSING, with background polling
disabled; stop on DONE or FAILED. Retry only transient read failures with a
small bound and backoff. Do not retry 400, 401, 403 or 404. Suspend polling on
permanent errors and provide manual retry for recoverable network failures.
401 triggers session refresh once, then sign-in if still unauthorized.

Validate canonical ULIDs before making a lookup. Preserve displayed data on
transient polling errors and distinguish connection problems from a FAILED
job. Show backend-safe failureReason for failed jobs. Steps are outcomes, not
proof of which operation is currently executing, and can change after retry.
Handle missing/null result, steps or fields without crashing; DONE without a
result gets a clear unavailable-result message. Render OCR and extracted
values as text, never HTML. There is no artifact download URL in the API,
so this version does not fetch private thumbnails or original documents.

## Configuration and local development

Document public Vite configuration: API base URL, Cognito user pool ID and
app client ID. These are public identifiers; no AWS keys or client secrets.
Check configuration at startup and show a useful setup screen when incomplete.
Provide a checked-in example configuration with placeholders, never real
secrets. Existing `.env` ignore rules remain in force.

Include an explicitly enabled local demo mode using fake data and an in-memory
adapter behind the same API interface. It must work without AWS credentials,
show a visible Demo mode label and never send fake uploads to AWS. Default
behavior remains the configured real API; demo is never a silent fallback.
Document that demonstration fixtures do not prove real Cognito or AWS access.

The existing API supports browser CORS and the bucket supports POST CORS.
Document the existing uploadOrigins setting for the eventual hosted origin.
No infrastructure deployment, account writes or new services are in scope.
Keep the existing `web/** linguist-detectable=false` rule.

## Verification and documentation

Use Vitest and Vue Testing Library with fakes for meaningful auth, upload,
polling, terminal/error-state and account-isolation coverage. No tests contact
AWS. Include TypeScript checking, ESLint, tests and a production Vite build.
Wire frontend lint into `make lint` and document frontend commands plus setup
in README. Update architecture docs and add an ADR for the frontend choices.
Run repository `make lint test`, the linux/arm64 Go build and relevant frontend
checks. Report unavailable tools or failed checks honestly. No DynamoDB access
patterns, IAM, workflow or CDK resources change, so no integration-test or
infrastructure synthesis scope is added by this frontend-only implementation.

## Non-goals and cost

No job-list API, admin console, OCR editing, paid OCR enablement, hosting
provisioning or deployment. The output is a local static frontend ready for
the existing planned S3/CloudFront hosting. No new AWS resource or idle cost;
real usage still invokes the existing backend and polling increases reads.

## Builder discretion

Choose component boundaries, icons, exact spacing and compatible package
versions. Keep dependencies focused. This document's proposed visual style,
session-scoped auth storage and demo mode are builder recommendations, not
preferences already stated by the user.
