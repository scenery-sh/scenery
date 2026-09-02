# Table-Driven TypeScript HTTP Client Emit

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Generated `typescript_client` HTTP methods currently inline the same request
construction and typed response-matching machine once per binding. A large
public surface such as ONLV's Next client therefore repeats that machine
hundreds of times, producing a multi-megabyte `client.ts`.

This plan replaces the inlined emit with one shared generated runtime helper
driven by a per-binding descriptor table. The public TypeScript API stays one
class and one async method per covered binding with
`(input, options?) => Promise<XxxOutcome>`. The generator does not emit
TanStack hooks. There is one rolling emit shape: the verbose per-method
machine is removed, not kept as a compatibility path.

## Progress

- [x] (2026-09-01) Read generator contracts, current `renderTSClient` /
  `renderTSResponseCases` emit, locked test fragments, and the TypeScript
  client specification.
- [x] (2026-09-01) Chose a single `Runtime.invoke` / `Runtime.matchResponse`
  helper plus a `bindings` descriptor table in `client.ts`.
- [x] (2026-09-01) Implemented descriptor emit, shared runtime matching, and
      thin method wrappers.
- [x] (2026-09-01) Updated tests, specification wording, and committed fixture
      clients.
- [x] (2026-09-01) Ran package tests, fixture regeneration, `go test ./...`,
      TypeScript conformance/typecheck, quick self-harness, and an ONLV size
      check. Full self-harness was skipped because the refreshed
      recommended-command union included it only due to unrelated dirty files
      already in the worktree.
- [x] (2026-09-01) Interned identical response cases and repeated consecutive
      shared runs into `sharedResponses` / `sharedResponseSets`. ONLV
      `client.ts` fell from 668,125 bytes to 300,060 bytes (691 lines, 220
      methods). Combined client+runtime is 400,508 bytes.
- [x] (2026-09-02) Added capability-aware shared-runtime emit. Small targets no
      longer contain unused query, binding header/cookie, multipart, or retry
      declarations, helpers, and invoke branches; the descriptor-table method
      shape remains unchanged.
- [x] (2026-09-02) Re-generated all three committed clients idempotently and passed
      focused Go tests, 24 Bun conformance tests, both generated-client/catalog
      TypeScript checks, `go test ./...`, and the full self-harness.

## Surprises & Discoveries

- Observation: empty `query: string[]` and `cookies: string[]` blocks are
  emitted for every method even when the binding has no such mappings.
  Evidence: `internal/compiler/testdata/native/clients/generated/public_api/client.ts`
  and `internal/generate/generate_typescript.go`.

- Observation: same-status completion selection and the 400 dual-path are
  locked by string fragments such as `completionMatches` inside `client.ts`.
  Those tests must move onto descriptor fields plus the shared runtime helper.
  Evidence: `internal/generate/generate_typescript_response_test.go`.

- Observation: after the table-driven emit, ONLV `client.ts` fell from 25,822
  lines / 1,989,587 bytes to 689 lines / 667,163 bytes while keeping 220
  methods. Combined `client.ts` + `runtime.ts` fell from 2,080,413 to 767,611
  bytes. Remaining client bytes are mostly repeated per-binding transport
  failure descriptors, not request-machine boilerplate.
  Evidence: `wc -c -l` before and after
  `go run ./cmd/scenery generate --target typescript_client.public_api --app-root /Users/petrbrazdil/Repos/onlv -o json`.

- Observation: interning identical cases plus maximal shared runs dropped ONLV
  `client.ts` from 668,125 to 300,060 bytes. `problemCode` appears 7 times
  (once per interned case) and 218 methods spread `sharedResponseSets`.
  Runtime bytes did not change. Remaining size is per-binding path/body data
  and unique completion mappings.
  Evidence: worktree `go build` generate against ONLV; do not use the older
  `.scenery/harness/bin/scenery` for this size check.

- Observation: capability projection reduced the single-binding native fixture
  `runtime.ts` from 100,448 bytes to 79,911 bytes (20.4%) while the richer house
  fixture retains query, binding header/cookie, response metadata, and
  multipart conformance coverage. The assistant-only fixture reaches the same
  79,911-byte minimal runtime without losing its separate assistant surface.
  Evidence: regenerated committed native, house, and assistant TypeScript
  fixtures plus generated-client `tsc` and Bun conformance.

## Decision Log

- Decision: Put the request/response machine in generated `runtime.ts` as
  `invoke` and `matchResponse`, and emit one frozen descriptor table in
  `client.ts` keyed by method name.
  Rationale: the matching rules are identical for every binding; only the
  mapping data differs. `typeRegistry` already proved that shared codec
  descriptors belong in the client module.
  Date/Author: 2026-09-01 / Codex.

- Decision: Do not export `invoke`, `matchResponse`, or binding descriptor
  types from `index.ts`.
  Rationale: Section 5 of `docs/spec/typescript-client.md` forbids exporting
  internal implementation helpers. Callers keep using the typed class methods.
  Date/Author: 2026-09-01 / Codex.

- Decision: Omit empty query, header, cookie, path-parameter, and body arrays
  from descriptors instead of emitting empty blocks.
  Rationale: the empty arrays are noise, not contract data.
  Date/Author: 2026-09-01 / Codex.

- Decision: Replace the inlined emit entirely. Do not keep a verbose mode.
  Rationale: Scenery has one rolling specification and one generated client
  shape.
  Date/Author: 2026-09-01 / Codex.

- Decision: Intern identical response-case JSON (count >= 2) into
  `sharedResponses` and intern repeated maximal consecutive shared runs into
  `sharedResponseSets`, then reference them from each binding `responses`
  array. Matching stays on a flat `BindingResponseCase[]`; no runtime helper
  change.
  Rationale: ONLV's remaining client fat was the same transport/admission/
  system failure objects copied onto every binding. Sharing the objects keeps
  failure-first order, the 400 dual-path, and clone-per-candidate semantics.
  Date/Author: 2026-09-01 / Codex.

- Decision: Derive one runtime-capability set from the same binding descriptors
  and target retry configuration, then filter marked sections from the shared
  runtime source. Keep one table-driven invoke implementation and emit a direct
  fetch branch when retry is absent.
  Rationale: Small clients should not pay for unused transport capabilities,
  but capability specialization must not return to per-method request/response
  machines or duplicate runtime implementations.
  Date/Author: 2026-09-02 / Codex.

## Outcomes & Retrospective

Implementation is complete in this worktree. Generated HTTP methods are thin
typed wrappers over `Runtime.invoke` and a `bindings` descriptor table.
Identical response cases and repeated consecutive shared runs are interned
as `sharedResponses` / `sharedResponseSets`. `Runtime.matchResponse`
preserves failure-first `isProblemCode` matching, same-status exact-one
completion selection, `response.clone()`, contract violation swallowing,
`system.*` throws, and the 400 dual-path. Empty query and cookie mapping
collections are omitted. The shared runtime now projects only capabilities used
by covered descriptors and target retry configuration. Public
class/method/outcome shape is unchanged, a retry-disabled target no longer
exports the inapplicable `RetryRuntime` type, and `index.ts` still does not
export the table-driven helper.

Scenery validation for this change passed: `go test ./internal/generate`,
both committed fixture regeneration commands, bun conformance, generated
client `tsc`, and `go test ./...`. ONLV was regenerated only as an
uncommitted size check. From the original 25,822-line / 1.99 MB
`client.ts`, the table-driven pass reached 689 lines / 668 KB and interning
reached 691 lines / 300 KB. Combined client+runtime is 401 KB. The plan
stays active until the scenery change lands.

The 2026-09-02 capability follow-up kept that validation surface green. All
fixture regeneration commands returned `changed: []` on the verification pass;
the native runtime is 79,911 bytes, the rich house fixture exercises retained
capabilities, and the full self-harness passed its Go, vet, fixture, schema,
TypeScript, UI, and real-process lanes.

## Context and Orientation

`internal/generate/generate_typescript.go` (`renderTSClient`) emits one method
body per public HTTP binding: cancel check, path/query/header/cookie/body
encoding, `fetch` or `fetchWithRetry`, then `renderTSResponseCases` from
`internal/generate/generate_typescript_response.go`. That helper groups
responses by status, tries transport/admission/`system.*` failures first with
`isProblemCode`, then tries `result.*` / `error.*` / `dispatch.enqueued`
completions on `response.clone()`, accepts a completion only when exactly one
candidate matches, swallows only `SceneryClientError` with
`code === "contract_violation"`, and otherwise throws `SceneryClientError`.

`internal/generate/generate_typescript_runtime.go` already owns codecs,
`typeRegistry` consumers, and header/cookie/body helpers. Committed fixtures
live at `internal/compiler/testdata/native/clients/generated/public_api/` and
`internal/compiler/testdata/house/clients/generated/public_api/`. The public
contract is `docs/spec/typescript-client.md`; Appendix A still excludes React
hooks.

## Milestones

1. Specify the binding descriptor and shared invoke/match helpers.
2. Replace inlined method bodies with table-driven thin wrappers.
3. Relock tests on descriptor data and runtime matching behavior.
4. Regenerate committed TypeScript fixtures and prove the suite.

## Plan of Work

Add generated TypeScript types for a binding call descriptor: method, path
template, optional path/query/header/cookie/body mappings, response byte
limit, and response cases `{status, role, kind, name, codec, content types,
type ref, problemCode?}`. Implement `Runtime.invoke(transport, binding, input,
options, typeRegistry)` to build the request and `Runtime.matchResponse` to
apply the existing failure-then-completion rules.

Change `renderTSClient` so each method is a typed wrapper that calls
`Runtime.invoke` and casts to `Types.XxxOutcome`. Keep `typeRegistry` as the
shared codec table. Stop emitting empty query and cookie mapping collections.
Update specification prose only to describe the shared helper; do not change
the public method or outcome union. Update generate tests that currently
search for inlined `completionMatches` fragments.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/generate
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    go test ./...

If `.scenery/harness/agent-context.json` `changed_area.recommended_commands`
includes harness after a refresh, run those exact commands. The full
self-harness supersedes the quick self-harness when both would otherwise be
selected.

Optional size check, not committed, from the ONLV app root when present:

    wc -c -l /Users/petrbrazdil/Repos/onlv/apps/nextnext/src/generated/scenery/client.ts
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root /Users/petrbrazdil/Repos/onlv -o json
    wc -c -l /Users/petrbrazdil/Repos/onlv/apps/nextnext/src/generated/scenery/client.ts

Do not commit ONLV output.

## Validation and Acceptance

Acceptance requires all of the following:

- Public generated API remains one class, one async method per covered
  binding, and `Promise<XxxOutcome>` with `kind: "result" | "error" |
  "failure"` plus the existing enqueue variant.
- Business errors are never inferred from HTTP status alone.
- Failures are tried first via `isProblemCode`; completions on the same
  status accept only when exactly one candidate matches.
- Only `SceneryClientError` with `code === "contract_violation"` is swallowed.
- The 400 dual-path remains failure `transport.invalid_request` then
  completion `error.invalid_input`.
- Each response candidate uses `response.clone()`.
- Empty query and cookie mapping collections are omitted.
- A small target's `runtime.ts` omits unused query, binding header/cookie,
  multipart, and retry declarations, helpers, and invoke branches while
  retaining the single table-driven request/response implementation.
- `index.ts` still does not export `invoke` or binding descriptor types.
- Appendix A still excludes React hooks.
- `go test ./internal/generate`, all three committed fixture regeneration
  commands, `go test ./...`, and any recommended harness commands from the
  refreshed validation matrix pass.
- Every exact top-level Go test root remains under the 100ms p95 budget.

Skip the ONLV size check when `/Users/petrbrazdil/Repos/onlv` is absent or
`generate` against that app root cannot run; record the skip reason. Skip
`go install ./cmd/scenery`; use worktree-local
`.scenery/harness/bin/scenery` when a scenery binary is required.

## Idempotence and Recovery

Generation commands are repeatable and replace the descriptor-covered
TypeScript output atomically. A failed fixture regeneration leaves the
previous committed files in place until a successful generate. Re-run the
same generate commands after fixing emit bugs. Do not keep a second emit
mode to recover old bytes.

## Artifacts and Notes

- ExecPlan: `docs/plans/0156-typescript-client-table-emit.md`
- Generator: `internal/generate/generate_typescript.go`,
  `internal/generate/generate_typescript_response.go`,
  `internal/generate/generate_typescript_runtime_capabilities.go`,
  `internal/generate/generate_typescript_runtime.go`,
  `internal/generate/generate_typescript_runtime_invoke.go`
- Runtime helpers: `Runtime.invoke`, `Runtime.matchResponse`,
  `Runtime.BindingCall`, `Runtime.InvokeTransport`
- Spec: `docs/spec/typescript-client.md`
- Fixtures: `internal/compiler/testdata/native/clients/generated/public_api/`,
  `internal/compiler/testdata/house/clients/generated/public_api/`, and
  `testdata/assistant/clients/generated/public_api/`
- Native fixture `client.ts`: 125 lines to 32 lines.
- Native fixture `runtime.ts`: 100,448 bytes to 79,911 bytes after capability
  projection.
- ONLV size check (uncommitted): original `client.ts` 25,822 lines /
  1,989,587 bytes; table-driven pass 689 / 668,125; intern pass 691 /
  300,060. `runtime.ts` 1,852 / 90,826 to 2,097 / 100,448. Combined
  2,080,413 to 400,508 bytes. 220 methods retained.
- Validation: `go test ./internal/generate` pass; fixture regeneration
  `changed: []` on the intern pass (single-binding fixtures have nothing
  to intern); bun conformance 24 pass; generated-client `tsc` pass;
  `go test ./...` pass.
- Capability follow-up validation: all three fixture regenerations `changed: []`;
  Bun conformance 24 pass; generated-client and catalog `tsc` pass; focused Go,
  `go test ./...`, race, and full self-harness pass.

## Interfaces and Dependencies

The implementation uses only the Go standard library. No environment-variable
knob, new public TypeScript export, React hook, or dual emit path is added.
Callers keep importing the generated client class and outcome types.
