# Neutral End-User HTTP Surfaces

## Purpose / Big Picture

An application's users must not see the framework name in its HTTP surface or
browser-delivered code. The name remains useful to developers in CLI output,
source packages, machine descriptors, private process links and diagnostics.
The invariant is case-insensitive and covers paths, query parameters, headers,
cookies, bodies, errors and delivered code/assets, including source comments.

The first PR changes public configuration, object storage, refresh cookies and
the core generated TypeScript transport/assistant client. Optional React pages
and the UI catalog need a separate coordinated cutover; this first PR does not
claim complete compliance for applications using those outputs.

## Progress

- [x] 2026-10-27: Inspect current HTTP producers and consumers; add the repo rule.
- [x] 2026-10-27: Implement the first public transport and core-client cutover.
- [x] 2026-10-27: Regenerate five committed clients; core browser sources contain no framework name. Full verifier and lint pass; auth, configuration, native-runtime and UI probes pass. The named storage probe has a reproduced baseline blocker; separate native HTTP proof passes all 10 cases.
- [x] 2026-10-27: Prepare the first PR with its explicit migration and remaining scope.
- [ ] Remove branding from optional React adapters/catalog output and verify an
  actual production frontend bundle, served URLs, assets and source maps.

### Resume here

2026-10-27: The first PR on `feat/public-http-neutrality` covers transport and
core-client naming, with the independently reproduced storage-probe blocker
recorded below. Next, migrate optional React/catalog output and verify a
production bundle. No application deployment or data migration is authorized
by this plan. The user requested a first PR; the broader UI cutover remains
follow-up scope.

## Surprises & Discoveries

- `runtime/deployment_config.go` exposed branding in both the public route and
  its JSON `kind`; changing only the route would still reveal it.
- `runtime/response_identity.go` already omits development identity headers in
  production. Private protocols and developer-only tools can retain their names.
- `runtime/gzip.go` removed its compression-control header only for gzip-capable
  requests. Giving that marker a neutral name also covers identity responses.
- The full-core scan also found the multipart boundary and private TypeScript
  failure aliases. Both now use neutral names.
- The named storage probe fails before storage assertions: `unallocated read:
  unexpected state in unallocated agent home: telemetry.jsonl.lock`. Its
  `verifyHarnessStorageProbeNoState` guard permits the telemetry file but not
  the existing telemetry lock. A clean base checkout reproduces the same failure;
  this PR leaves the independent verifier issue unchanged.
- `internal/uigen` emits branded identifiers, query keys and `scenery-ui` imports.
  It needs an atomic adapter/catalog/client-consumer migration beyond this PR.

## Decision Log

- 2026-10-27: Use `/_app/public-config`, `app.public-config`, `/_app/storage`,
  `X-Storage-Metadata`, `X-Storage-Error` and `app_refresh`. Preserve method,
  authentication, tenant isolation, caching and token semantics.
- 2026-10-27: Use `ClientError`, `clientMetadata`, neutral generation comments
  and neutral literal generator/catalog metadata in core browser client code.
  Keep developer JSON descriptor identities and source package paths unchanged.
- 2026-10-27: Remove old public spellings without aliases. Regenerate and redeploy
  clients with the runtime. Existing browser sessions require login again; no
  database sessions, user data or token formats are deleted or migrated.
- 2026-10-27: Keep the first PR focused on transport/core clients. The remaining
  React/catalog work stays explicit rather than claiming an unverified global
  ban is already implemented.

## Outcomes & Retrospective

First-PR implementation and regeneration are complete. All changed behavior has
in-process coverage and live proof; the change is ready for review with the
independent blocker disclosed. The named storage
probe remains blocked by an unchanged precondition that also fails on clean
base `0c1f7fcdc2293d4907ab5720fc5ac4a744eb32b4`.
Evidence from 2026-10-27 (local immutable archives, intentionally uncommitted):

- Full verifier: `.scenery/harness/runs/20261027T000840.890561000Z/`; no errors,
  five existing knowledge-freshness warnings and one advisory suite-wall warning.
- Auth, configuration and native-runtime probes:
  `.scenery/harness/runs/20261027T001007.233590000Z/`; each passes, including
  all 25 auth cases and owned cleanup. The same run records the storage blocker.
- Corrected UI conformance and both managed TypeScript checks:
  `.scenery/harness/runs/20261027T001132.640120000Z/`; 58 Bun tests pass.
- Storage blocker on clean base:
  `.scenery/harness/public-http-proof/storage-baseline/`; same exact error,
  cleanup passed, base revision recorded above.
- Supplemental actual production-mode runtime:
  `.scenery/harness/public-http-proof/result.json`; 10 HTTP cases pass over a
  Unix socket: configuration, retired paths, unauthenticated storage, upload,
  GET/HEAD metadata, GET/HEAD errors and deletion. Every response header/body
  excludes the framework name. The record pins the executed binary SHA-256;
  its owned process exited and its temporary storage/socket root was removed.
- `golangci-lint run ./...`: `0 issues.`

The overall plan remains active for optional React/catalog output and served
production bundle verification.

## Plan of Work

`runtime` owns the public configuration response, object routes and compression
coordination. `storage` owns encoded metadata and error headers. `auth` owns the
canonical refresh cookie. `internal/generate` owns core browser source and its
ownership marker; `internal/uigen` uses the renamed transport error class.
Update direct consumers, living contract docs, migration guidance and focused
boundary tests together. Regenerate committed clients through the local CLI;
never hand-edit generated fixture bytes.

Follow-up work must rename browser-visible React identifiers, query keys,
materialized catalog paths and bundled catalog strings together with their
consumers. Add production build/served-asset evidence and inspect source maps;
do not solve it with a blanket response-body rewrite or a compatibility layer.

## Validation and Acceptance

Run commands from the repository root. Expected cumulative classes are Go,
generator, runtime, auth, storage, configuration and TypeScript UI verification.
Select full directly: `go run ./scripts/verify --summary --write`. Read its
`agent-context-summary.json` first, then fulfill the complete changed-area
command union in `agent-context.json`; successful same-input verifier steps
satisfy duplicate commands. Always run `golangci-lint run ./...`.

Focused coverage is `go test ./runtime ./storage ./auth ./internal/generate
./internal/uigen ./scripts/verify`. Regenerate with the local
`.scenery/harness/bin/scenery generate --target typescript_client.public_api
--app-root <fixture> -o json` for `internal/compiler/testdata/native`,
`internal/compiler/testdata/house`, `testdata/assistant`,
`examples/webhook-inbox` and `testdata/apps/worktree-postgres`.

Run `go run ./scripts/verify --probe ui --probe configuration --probe storage
--probe auth --probe native-contract --summary --write`. These named probes
own dependency preparation, TypeScript conformance/typechecks, served source
identity, actual public configuration, storage behavior and auth-cookie
lifecycle. Each must report actual assertions and owned resource cleanup.
The configuration response must use the neutral route and kind; storage must
retain metadata/error decoding, auth and tenant behavior; login/refresh/logout
must use the neutral cookie. The generated core-client scan must reject the
framework name, including all optional transport sections and assistant source.

The selected `ui` probe runs both managed TypeScript checks and Bun conformance
plus generated dev-runtime tests. Release certification and benchmarks are not
selected, because the user requested implementation and a first PR.

## Idempotence and Recovery

Generation is transactional and repeatable. Preserve old marked Go artifacts
and recognize the new neutral marker for core TypeScript source. Do not reset
application data or adopt mismatched resources. Probes create disposable homes,
apps and databases and must clean their own resources. Retry failed checks using
their archived commands. Runtime and generated browser clients must be deployed
together; rolling back requires the matching prior clients and runtime.
