# Automatic Operation Tracing

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

Make every framework-owned execution boundary observable without application instrumentation. Complete six approved areas: durable/event/MCP/CLI entrypoints; internal service bindings; a bounded, scoped trace-list/detail development RPC and generated client; outgoing HTTP spans and propagation; storage operations; and TypeScript client correlation and request/retry lifecycle. Preserve the SQL and contextual logging fixes from completed plan 0208.

## Progress

- [x] 2026-10-02: Audited runtime and generated clients and confirmed the six gaps against current code.
- [x] 2026-10-02: Implemented shared span lifecycle and local/remote durable, event, MCP, CLI and internal boundaries; focused Go coverage passes.
- [x] 2026-10-02: Implemented HTTP propagation, streamed I/O lifecycle and storage adapters; focused coverage passes.
- [x] 2026-10-02: Implemented scoped, bounded trace reads and generated dev-runtime methods; isolation and budget tests pass.
- [x] 2026-10-02: Generated client observation and trace-ID errors implemented; 53 Bun conformance tests and both TypeScript checks pass.
- [x] 2026-10-02: Regenerated all three clients; focused and full Go verification, race tests, lint, TypeScript checks and the real observability probe pass. The post-edit classification union is fulfilled.

## Surprises & Discoveries

- 2026-10-02: Telemetry is exported under the stable base application ID and an independent session ID; the runtime process ID is not the telemetry application ID. The new RPC resolves this pair from registered status and tests retain distinct route/base/runtime IDs.
- 2026-10-02: Victoria can expose a trace by ID before its search index lists it. The real probe waits for both independently. Unknown trace lookup returns HTTP 404 from the backend and is normalized to the documented empty detail.

- 2026-10-02: The real combined SQL/storage fixture exposed a startup race: early retained-PostgreSQL startup failed on the worktree operation lock held by first storage allocation. The supervisor now joins only storage allocation before this optional PostgreSQL start; compilation and Victoria remain concurrent. Covered by a focused cancellation/failure test and the combined observability probe.

- Durable, event, MCP and CLI request states enable tracing but never create a span; DB instrumentation consequently has no parent and emits nothing.
- Internal process links carry the caller span intact; instrumenting the receiving binding once covers both local and remote dispatch without duplicate operation spans.
- The CLI returns child summaries when a trace ID is selected, but not events or a parent tree. The development RPC has no trace read methods.
- The host Go 1.27 installation fails standard-library compilation. The existing `/Users/petrbrazdil/sdk/go1.27.0/bin/go` works; validation uses it through a command-local PATH.

## Decision Log

- 2026-10-02, agent: Use existing framework runtime adapters and generated identities, not per-application hooks or a new telemetry dependency. The user authorized all six areas.
- 2026-10-02, agent: Keep existing bounded telemetry export and RPC admission. Trace reads must enforce application and session scope, payload limits and cancellation.
- 2026-10-02, agent: Instrument client lifecycle in shared generated Runtime.invoke and fetchWithRetry. Provide typed observation and trace correlation without collecting request bodies, credentials or SQL parameter values.

## Outcomes & Retrospective

Completed on 2026-10-02. All six approved areas are implemented and validated. The generated probe returned 28 spans with nested SQL under internal and durable operations, storage I/O and a propagated outbound HTTP child. The public runtime RPC returned the same span tree and SQL events, listed the request and returned an empty detail for an unknown trace. The generated TypeScript client made two retry attempts, cancelled the discarded response and correlated the final backend trace. Contextual logs, SQL metrics and literal/argument redaction remained intact. Owned runtime, Victoria and PostgreSQL resources were removed.

No application instrumentation is needed at generated execution or storage boundaries. Custom HTTP transports require one `scenery.TraceHTTPTransport` wrapper; client-side lifecycle observations require an `onTrace` consumer and are not automatically exported. No installation, commit, push or client application UI change was performed.

## Context and Orientation

`runtime/devreport.go` exports trace events and summaries. `runtime/span.go` and `dbtrace.go` own application and SQL spans. `runtime/contract_internal.go`, `durable.go`, `contract_events.go`, `mcp_dispatch.go` and `contract_cli.go` are execution adapters. Storage reaches the runtime through the small `internal/appsdk` bridge. `internal/victoria/query.go` translates backend trace data. `cmd/scenery/dashboard_rpc.go` and its admission code own the documented development RPC; `internal/generate/dev_runtime_client.ts` generates its typed client. The generated HTTP client uses `generate_typescript_runtime_invoke.go` and shared runtime helpers. No dashboard UI belongs to Scenery.

## Milestones

First establish shared, context-aware lifecycle and tests for every execution kind. Then instrument I/O, expose trace data and extend generated client observation. Finally prove a generated application emits nested operation/SQL/HTTP/storage traces and that the supported RPC returns them, while unit tests exercise background handlers, failures, cancellation and retry behavior.

## Plan of Work

Use one reusable runtime span helper with explicit identities and bounded metadata. Preserve invocation authorization tokens and goroutine state restoration. Async dispatch records causality; attempts get unique span IDs. Incoming HTTP accepts valid W3C trace context, outgoing HTTP injects context into a cloned request, and body closure completes the client span. Storage operations finish on return or streamed-body completion. New RPC reads share existing work-call budgets and return normalized public data instead of backend URLs or backend schemas. Generated TypeScript observation remains optional and cannot change the request result when an observer fails.

## Concrete Steps

Work in `/Users/petrbrazdil/.codex/worktrees/0c6b/scenery`. Use `PATH=/Users/petrbrazdil/sdk/go1.27.0/bin:$PATH` for each Go or lint command. Read scoped instructions before edits. Run affected package tests after each milestone. Regenerate the native, house and assistant TypeScript fixtures through the product generator. Extend the existing named `observability` probe with assertions and cleanup; use the worktree-local binary built by the verifier, never install globally.

## Validation and Acceptance

Expected classes are go-package, compiler-or-generator, cli-json-contract and release-sensitive-or-runtime. All commands run at the repository root with the PATH above for Go tools:

- `go test ./runtime ./storage ./internal/appsdk ./internal/victoria ./cmd/scenery ./internal/generate ./scripts/verify`
- `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`
- `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json`
- `go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json`
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts`
- `tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json`
- `tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
- `go run ./scripts/verify --summary --write`, then inspect `.scenery/harness/agent-context.json` and fulfill the union of recommended commands. This includes the full repository Go suite.
- `golangci-lint run ./...`
- `go test -race ./runtime ./storage ./internal/appsdk ./internal/victoria ./cmd/scenery`
- `go run ./scripts/verify --probe observability --summary --write`: a disposable generated app must prove current served process/build identity, nested SQL, internal, HTTP and storage signals and scoped trace detail readback. Record trace IDs and cleanup of owned processes and PostgreSQL.
- `git diff --check`

In-process tests must demonstrate non-HTTP span creation, nested SQL parentage, terminal failures/cancellation, unique attempts, cross-app/session isolation and bounded results. No browser UI is changed, so browser acceptance is unselected. Benchmarks, all-root timing audits and release certification are unselected because the user requested functionality, not performance measurement or release certification.

## Idempotence and Recovery

Preserve all existing work from plan 0208. Generation is the existing artifact transaction. Probes own temporary roots and cleanup only matching owned resources; retain their root on failure for diagnosis. Do not modify installed Scenery, running user apps, unmanaged databases or completed numbered plans.

## Artifacts and Notes

All validation logs are under ignored `.scenery/harness/`:

| Command / acceptance | Result and evidence |
|---|---|
| Focused Go packages from the validation list | PASS; `0209-focused.log`, `0209-identities-tests.log`, `0209-scope-tests.log`, `0209-readback-tests.log`, `0209-http-tests.log` |
| Three `go run ./cmd/scenery generate` commands above | PASS; native, house and assistant `0209-*-generation.json`; committed client descriptors match generated output |
| Bun conformance and dev-runtime tests | PASS: 53 tests, 238 expectations; `0209-bun.log` |
| Both TypeScript compiler commands above | PASS; `0209-types.log`, `0209-catalog-types.log` |
| Full verifier, including `go test ./...`, affected packages, `go vet` and schemas | PASS; `0209-full-verifier.log`, `self-latest.json`; fulfills all four post-edit validation classes in `agent-context.json` |
| `golangci-lint run ./...` | PASS: 0 issues; `0209-lint.log` |
| Race checks from the validation list | PASS; `0209-race.log`, with subsequent Victoria/CLI updates in `0209-final-race.log` and the final HTTP guard in `0209-http-race.log` |
| `go run ./scripts/verify --probe observability --summary --write` | PASS; `0209-observability-proof.json`, trace `5315968b5d89d824ee08f41a247653ca`, 28 spans; generated retry trace `63aab49800f711a49eb7f616ed377f41`; current host/service/build identities and owned cleanup recorded |
| `.scenery/harness/bin/scenery inspect docs --for-path docs/spec/typescript-client.md -o json` | PASS; `0209-spec-docs.json` |
| `git diff --check` | PASS |

The full verifier reports advisory documentation freshness and source-size warnings; the first final Go suite took 6.907 seconds against the advisory 5-second cached-suite budget. No timing benchmark or release certification was selected. The combined observability probe covers the changed PostgreSQL/storage startup ordering as well as telemetry export and query boundaries. In-process tests cover event, MCP and CLI entrypoints, panic/error handling, streamed cancellation, cross-process parentage and cross-app/session isolation.

Updated public behavior in `ARCHITECTURE.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `docs/spec/typescript-client.md` and the probe catalog. No instruction-doc change was needed: ownership and verification rules remain the same.

## Interfaces and Dependencies

Keep application SDK imports independent of the runtime implementation. Reuse current report envelopes and RPC failure contracts. Add no external dependencies or environment-variable knobs. Framework-owned operation identities come from generated binding registrations, not application payloads.
