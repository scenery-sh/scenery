# Engineering Constraints

Keep dependencies minimal. Prefer the Go standard library unless an external dependency has a clear, concrete payoff that justifies the added maintenance and upgrade surface.

# Workflow

- After every repository change, rebuild any binaries from this repo that are expected to be available in `PATH`. For onlava, run `go install ./cmd/onlava` before finishing the task.
- For substantial repo changes, run `onlava harness self --json --write` when practical so agents and humans have one stable validation snapshot at `.onlava/harness/self-latest.json`.
- For substantial target-app changes, run `onlava harness --json --write` from that app root when practical.

# Execution Plans

For complex features, multi-hour tasks, migrations, or significant refactors, create or update an ExecPlan as described in `PLANS.md`.

- Store active ExecPlans under `docs/plans/<0000-short-slug>.md` using a permanent historical sequence ID.
- Link active ExecPlans from `docs/plans/active.md`.
- Keep their Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective sections current as you work.
- `PLAN.md` is the strategic roadmap; do not treat it as an executable task plan.

# Summary

Build an onlava-native Go-only local runtime that makes `onlava run` start a single HTTP server for onlava services, with strict onlava naming and no compatibility layer for non-onlava syntax. Use `onlava dev` for the full local development platform with dashboard, proxy, DB Studio, and live reload.

# Public Surface

- CLI:
  - `onlava dev [--port <n>] [--listen <addr>]` for local development.
  - `onlava run [--port <n>] [--listen <addr>]` for headless production-like execution.
  - Default listen address is `127.0.0.1:4000`.
- App root marker:
  - `.onlava.json`, JSON, required for `onlava run`.
  - Phase 1 only needs `"name"`; no cloud/linking fields.
  - Standard auth can be enabled with `"auth": {"enabled": true}`. It registers the built-in auth handler plus `/auth/*` and `/users/dev-bootstrap` endpoints, stores DB-backed state in PostgreSQL schema `onlava_auth`, and exposes request data as `*auth.AuthData`.
- Source directives:

  ```go
  //onlava:api public|auth|private [raw] [path=/...] [method=GET,POST]
  //onlava:service
  //onlava:authhandler
  ```

- Public Go packages:
  - `github.com/pbrazdil/onlava` with `Meta()` and `CurrentRequest()`.
  - `github.com/pbrazdil/onlava/auth` with `UserID()`, `Data()`, `CurrentAuthData()`, and the standard auth module.
  - `github.com/pbrazdil/onlava/errs` with coded errors and HTTP status mapping.
- Struct tag surface for typed endpoints/auth params:
  - Request decoding: `json`, `header`, `query`, `qs`, `cookie`.
  - onlava tags: `onlava:"optional"` and `onlava:"httpstatus"`.

# Implementation Changes

Create a new Go module rooted at `github.com/pbrazdil/onlava` with three main areas:

- `cmd/onlava` for the CLI.
- Internal parser/build pipeline for service discovery, directive parsing, codegen, and app launch.
- Runtime/public packages under `github.com/pbrazdil/onlava/...`.

## App Discovery And Service Model

- `onlava run` walks upward to `.onlava.json`, then loads the Go module from that root.
- Service discovery follows onlava rules:
  - A service is defined by onlava APIs, an onlava service struct, or an onlava auth handler.
  - Nested services are invalid.
  - Service names come from the root package name and must be unique.

## Parser Behavior

- Support typed handlers with onlava endpoint signatures: `func(context.Context, [path params...], [payload]) ([resp], error)`.
- Support raw handlers: `func(http.ResponseWriter, *http.Request)`.
- Support `//onlava:service` methods plus optional `init<Type>() (*Type, error)` service initialization.
- Support `//onlava:authhandler` as either a package function or service method.
- Allow both token-string auth and structured auth params from `header`, `query`, `qs`, and `cookie` tags.
- Allow optional auth-data return struct.

## Preserve onlava Route Defaults

- Default path: `/<service>.<Endpoint>`.
- Typed endpoint default methods:
  - `GET,POST` when no payload exists.
  - `POST` when a payload exists.
- Raw endpoint default method wildcard.

## Build/Codegen Strategy

- `onlava run` parses the app with `go/packages` and AST, builds an app model, generates a transient build workspace, compiles it, and runs the resulting binary.
- Generated files include:
  - Endpoint descriptors and registration.
  - Service-struct wrappers.
  - Auth-handler registration.
  - Internal API-call helpers.
  - A synthetic main.
- Rewriting is part of phase 1: endpoint-to-endpoint calls are rewritten to generated internal call helpers so same-service and cross-service calls honor routing, auth context, and private access rules instead of bypassing the runtime through direct Go calls.

## Runtime Behavior

- Start one local HTTP server with separate public and private routing tables.
- Mount public and auth endpoints on external HTTP.
- Keep private endpoints internal-only and callable via generated in-process service calls.
- Do not support service-to-service calls to raw endpoints in phase 1; fail at generation time with a clear error.
- Decode typed requests from path params, headers, query strings, cookies, and JSON body.
- Encode typed responses as JSON.
- Honor `onlava:"httpstatus"` on response structs.
- Run the auth handler for external auth requests, then expose auth state through `github.com/pbrazdil/onlava/auth`.
- Expose enough request metadata through `onlava.CurrentRequest()` for migrated common cases, especially raw endpoint path params, method, path, service, endpoint, and payload metadata.
- Map `github.com/pbrazdil/onlava/errs` codes to HTTP responses and return full structured errors for in-process internal calls.

# Test Plan

Maintain a small onlava-named fixture set as the acceptance suite for phase 1.

Add parser/unit tests for:

- Directive parsing and validation.
- Service discovery.
- Typed/raw handler signatures.
- Auth-handler signatures.
- Route defaulting and path-param validation.

Add golden/codegen tests for:

- Service-struct wrappers.
- Internal API-call helper generation.
- Auth-handler registration.
- Synthetic main generation.

Add integration tests that run `onlava run` against sample apps and verify:

- Public typed endpoints on default and custom paths.
- Auth endpoints with string-token and structured auth params.
- `auth.UserID()` and `auth.Data()`.
- Service struct initialization and internal private calls.
- Raw endpoint routing plus `onlava.CurrentRequest().PathParams`.
- `onlava:"httpstatus"` and coded error responses.

Gate phase 1 on `go test ./...` and a lint/format pass for the new onlava code.

# Assumptions And Defaults

- Go only in phase 1. TypeScript is out of scope.
- Strict onlava naming only. No non-onlava app markers, imports, or directives in this milestone.
- No infra generation, DB management/migrations, Pub/Sub, cron, middleware, dashboard, cloud features, namespaces, or live-reload/watch mode.
- No automatic source migration command in phase 1; migrated apps are expected to adopt onlava syntax directly.
- Single-process local runtime only. No remote service hosting or distributed local orchestration in this phase.
