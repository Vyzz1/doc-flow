# 2. Cognito signs the API's tokens

Date: 2026-10-16

Status: accepted

## Context

The API handlers have read their caller from a JWT authorizer's verified
`sub` claim since they were written. That claim is not decoration: it
becomes the DynamoDB partition key (`USER#<sub>`) and the S3 prefix
(`uploads/<sub>/<job>`).

Ownership is enforced entirely by that key. `get-job` performs no permission
check; it reads `USER#<sub> / JOB#<id>`, so another user's id simply does not
find a job, and a caller cannot learn whether someone else's exists. That is
a property worth having — there is no check anybody can forget to write —
but it holds only while `sub` is something the client cannot choose.

Nothing was deciding `sub`, so no HTTP API existed and the two handlers were
deployed with nothing routed to them. This was the one thing between the
code and a usable service.

`AGENTS.md` lists the services this project may use, and no identity
provider is among them. Adding one needs a record.

## Options considered

1. **A development stub.** A Lambda authorizer returning a fixed `sub`,
   enabled only in dev. Shortest path to exercising the pipeline end to end,
   and it commits to nothing. But it is not authentication, and a stub that
   works is a stub that survives.

2. **An external provider** — Auth0, Clerk, a Google project. API Gateway
   needs only an issuer URL and an audience, so this creates no AWS resource
   and costs nothing on this bill. It requires an account somewhere else,
   and configuration that lives outside this repository.

3. **Cognito.** An AWS-native user directory the CDK creates alongside
   everything else, so a deployment produces a working system with no
   external setup.

## Decision

Cognito, on the **Lite** feature plan.

Lite is set explicitly rather than left to default. AWS defaults a new pool
to Essentials, and what Essentials adds — managed login, passkeys, email
one-time passwords — is not used here: the API needs a signed token and
nothing more. Both plans are free below ten thousand monthly active users;
beyond that Lite is $0.0055 per user against Essentials' $0.015.

The app client holds no secret, because its clients are browsers and command
lines that cannot keep one. Plain username-and-password authentication is
enabled in dev only, where it is the difference between fetching a token
with one CLI call and standing up a login page first; production allows SRP
alone.

Self sign-up is off. Anyone who could create an account could choose their
own `sub`, and `sub` is a storage key.

The authorizer pins both the issuer and the audience. Without the audience,
a token minted for any other client of the same pool would be accepted.

## Consequences

- A deployment now produces something callable. The two handlers have
  routes, and every route requires a token.
- **No route may be added without an authorizer.** The API sets no default
  authorizer on purpose, so a route that forgets one is open rather than
  silently inheriting protection it did not ask for. A test asserts every
  route carries `AuthorizationType: JWT`, because that mistake is invisible
  in review and total in effect.
- The cost is zero at any size this project will reach. Step Functions
  transitions remain the binding free-tier limit.
- `AGENTS.md` gains Cognito in its service list and its flow. The list
  existed to make additions deliberate, so an addition updates it.
- Moving to an external provider later changes the authorizer's issuer and
  audience and nothing else. The handlers read a claim; they do not know who
  signed it.
- Users are created by an administrator, which suits a project with no
  sign-up flow and would not suit one with customers.
