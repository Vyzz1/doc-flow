# 4. A static Vue dashboard with source-owned UI components

Date: 2026-10-30

Status: accepted

## Context

The backend exposes authenticated job creation/lookup but had no frontend.
The earlier plan named React; the user selected Vue, shadcn-vue and TanStack Vue
Query with English copy. The frontend must remain static for the planned
S3/CloudFront hosting and add no AWS service.

## Options considered

- Naive UI: ready-made dashboard components, less source ownership.
- PrimeVue or Vuetify: broad styled suites beyond this MVP's needs.
- shadcn-vue: copied component source, flexible Tailwind styling and accessible
  primitives; requires owning component updates.

## Decision

Use Vue 3 + TypeScript + Vite in `web/`, shadcn-vue + Tailwind for components,
and TanStack Vue Query for authenticated job queries and upload mutation state.
Use hash routes to avoid adding hosting rewrite infrastructure. Use Amplify
Auth only as a client library for existing Cognito, with SRP and sessionStorage
tokens. No Amplify backend or hosted login is provisioned.

Match existing API contracts: signed multipart S3 POST and progress polling.
Queries are keyed by subject/job ID; account changes clear the cache. Only
transient reads retry automatically. Upload failures retain the ticket/job ID
for explicit retry without creating another job.

Provide an explicit, visibly labeled local demo adapter. It does not OCR files
or call AWS, and does not replace checking against deployed services.

## Consequences

- Frontend has its own npm lockfile and local/CI checks.
- Component source is maintained here. `web/**` stays excluded from GitHub
  language statistics, as requested by the user.
- With no listing endpoint, the UI opens jobs by ID instead of server history.
  No DynamoDB access pattern, IAM or resource changes.
- No infrastructure cost added. Real usage still invokes per-request services;
  polling adds backend reads.
- Browser storage is accessible to JavaScript: render document content as text,
  never persist passwords, and include no secrets in the frontend build.
- Live auth, upload and CORS checks require explicit authorization before release.
