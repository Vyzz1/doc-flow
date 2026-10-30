# DocFlow workspace

English Vue 3 dashboard using shadcn-vue, Tailwind CSS and TanStack Vue Query.
Requires Node >=22.12 (CI uses Node 24), npm and the existing Go backend for real mode.

## Preview without AWS

```sh
cd web
npm ci
npm run dev:demo
```

Open the printed local URL and choose **Explore demo workspace**. Upload any
supported file or choose **View a sample result**. Demo uploads do not leave
the browser and are not actually OCRed: all results are labeled samples.
New demo jobs complete in approximately 9 seconds and exist only in memory;
reloading removes them. The sample job is always available in demo.

## Connect the existing backend

Copy `.env.example` to `.env.local` inside `web/` and replace its placeholders:

| Public setting              | Value                                             |
| --------------------------- | ------------------------------------------------- |
| `VITE_API_BASE_URL`         | Existing HTTP API URL, without a trailing `/jobs` |
| `VITE_COGNITO_USER_POOL_ID` | Existing Cognito user pool ID                     |
| `VITE_COGNITO_CLIENT_ID`    | Existing app client ID (no client secret)         |
| `VITE_DEMO_MODE`            | `false` for real mode                             |

Run `npm run dev`. Restart Vite after configuration changes. Incomplete
configuration shows setup instructions rather than silently enabling demo.
`dev:demo` and `build:demo` explicitly force demo mode even if real settings
exist. Never deploy a demo build as the real application.

Vite embeds these public identifiers in the build. Never put AWS credentials,
passwords or client secrets in Vite settings. No Amplify backend, CLI provisioning
or AWS credentials are required. Amplify is only the browser auth library for
the already-deployed Cognito pool.

Users are provisioned by an administrator. Sign-in uses Cognito SRP and supports
replacing an administrator-issued temporary password. Additional challenges
show a message to contact the administrator. Signup, password recovery and MFA
setup screens are outside this MVP. Tokens live in sessionStorage, scoped to
this origin/tab. Account changes clear the query cache. OCR text and field
values are rendered as plain text, never HTML.

The browser calls `POST /jobs`, uploads signed fields plus the file to S3 via
multipart POST, then polls `GET /jobs/{id}` every 3 seconds while processing.
Polling stops at terminal states or exhausted errors; inactive tabs do not poll.
Transient read errors retry at most twice. Creation and upload have no automatic
retry: a failed upload retains its job ID and permits explicitly retrying the
same file/ticket until expiry. The 10 MiB limit and document MIME rules match
the backend; the server still validates bytes.

There is no job-list API. Save the job ID or hash-route URL to return to a job.
The UI shows no fabricated history or private S3 artifact URLs. Documents and
results expire after 7 days.

## Checks

```sh
npm run lint        # Prettier check, ESLint (zero warnings), TypeScript
npm run lint:fix    # fix ESLint issues and format with Prettier
npm run format     # format frontend source/config/docs
npm run format:check # check formatting without changes
npm test            # Vitest, component tests and MSW-intercepted HTTP tests
npm run build       # real static build, reads public configuration
npm run build:demo  # static demo build
npx playwright install chromium
npm run test:e2e    # build demo, serve, test headless Chromium, then stop
```

To use installed Chrome instead of downloading Chromium, set
`PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` to its absolute executable path. Browser
checks cover desktop/mobile, file selection, job completion, tabs and sign-out,
and assert demo makes no external requests. Screenshots/failure traces go under
ignored `test-results/`. Tests prove local frontend behavior, not actual Cognito
authentication, deployed CORS or real OCR output.

Root `make lint test` includes the frontend; run `npm ci` here first.
Root `make fmt` includes frontend formatting; `make fmt-web` formats only Vue.
Prettier follows the repository's LF/two-space conventions; its ESLint config
disables conflicting style rules. Generated output, lockfile and environment
files are excluded. CI's existing frontend lint step also checks formatting.
`make build-web` builds the real frontend; `make build` still builds Go Lambdas.
CI checks both builds and runs demo browser checks before merge.

## Static hosting

Output is `web/dist/`; preview with `npm run preview`. Hash routes work on
S3/CloudFront without SPA rewrite rules. When eventually deploying infrastructure,
configure the existing bucket's `uploadOrigins` to include the frontend origin.
API CORS already accepts authorization/content-type. No resources are created
and no AWS deployment is part of this change. Real uploads, SRP login and origin
settings still need an explicitly authorized dev-stack check before release.
