# Directory groups become HTTP route prefixes

This ExecPlan is a living document. Maintain Progress, Surprises & Discoveries,
Decision Log, and Outcomes as work proceeds, according to [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Services may live beneath organizational folders such as `group1/maps` and
`group1/geometry`. Their HTTP routes must include the parent directories while
retaining the existing gateway and endpoint responsibilities. The public rule
is `gateway.base_path + directory group + authored endpoint path`; the external
runtime mount, such as `/api`, remains outside that contract path.

## Progress

- [x] (2026-09-09 19:59Z) Confirm the requested composition and inspect source,
  effective, expanded, generated-server and client route ownership.
- [x] (2026-09-09 20:12Z) Implement compiler-owned derivation, source/effective
  provenance, CRUD/collision/revision tests and Go/TypeScript/OpenAPI parity.
- [x] (2026-09-09 20:12Z) Update current specification, agent/app guidance and
  regenerate all three committed TypeScript fixtures.
- [x] (2026-09-09 20:16Z) Pass Scenery validation, commit/push main `38189f96`,
  build the dashboard embed, and install/verify that exact producer.
- [x] (2026-09-09 20:30Z) Update ONLV consumers in place, regenerate with the
  same Scenery revision, validate source/builds and commit only migration-owned
  changes on local ONLV main as `c402478c`.
- [x] (2026-09-09 21:27Z) Verify the independently completed ONLV merge:
  main and origin match `805ecc63`, including all local migration commits.
- [x] (2026-09-09 22:29Z) Apply the explicitly approved same-schema upgrade
  from plan 0173 and prove the running ONLV app, grouped requests and unchanged
  database/object data at the original root and browser origin.

## Surprises & Discoveries

Scenery starts at main `31863c57`; README simplification and its knowledge entry
are already task-owned uncommitted changes. ONLV starts at main `d354266c` and
contains substantial concurrent frontend work. Preserve it and stage only exact
task-owned paths or hunks. Its existing runtime is at `http://localhost:4920`.

The first default verifier run rejected the plan's missing literal living-document
statement; implementation, schema, drift, Go and vet checks passed. Add the required
statement and rerun. ONLV's before/after expanded inventories contain 211 HTTP
bindings: exactly 118 solar bindings acquire `/solar`, and 93 remain unchanged.
Maps is a root package and does not change; `pkg/maps3d` exposes no HTTP bindings.

The ONLV stop and client regeneration succeed, but the new installed binary
cannot reopen retained `scenery.worktree` state from the preceding specification
(SCN8003). The same rejection blocks storage inspection; storage and toolchain
records also require exact current identities. The diagnostic suggests explicit
migration, but no retained-worktree migration command exists. Do not relabel
identities, delete ownership, or recreate credentials to bypass this guard.
Owner: Scenery runtime/agent. Resolution requires an explicitly reviewed,
data-preserving in-place state upgrade before ONLV live acceptance; that is an
additional public lifecycle contract outside the implemented HTTP transformation.

ONLV push is independently blocked by remote main `3f67f2ea`, which changes 107
files and overlaps 13 currently dirty paths. The non-mutating `git merge-tree`
preview finds a `BUGS.md` conflict. No force-push, stash, merge, rebase or alternate
checkout was used; source commit `c402478c` remains local until safe integration.

That publication snapshot was superseded at 21:27Z: ONLV main and origin both
resolve to `805ecc63` and include `c402478c` and the independent remote work.
The developer approved a supported in-place upgrade; implementation and safety
acceptance now live in [plan 0173](0173-retained-state-spec-upgrade.md).

## Decision Log

- Decision: Keep gateway `base_path` outermost and exclude the service package's
  final directory from the group. Nested parent directories accumulate. Do not
  deduplicate repeated segments or retain aliases for old routes.
  Rationale: Explicit user agreement; one deterministic current HTTP contract.
  Date/author: 2026-09-09, Petr and Codex.
- Decision: Derive the grouped path once in the effective graph, after patches
  and before CRUD expansion. Preserve the authored path in the source view and
  attach directory-group provenance to the effective path.
  Rationale: Runtime, clients, collision checks and revisions must consume the
  same compiler result, not independently inspect filesystem paths.
  Date/author: 2026-09-09, Codex.
- Decision: Registry package cache locations and framework-owned routes do not
  create application directory groups. Local group segments must be literal
  URL-safe names, never parameters or percent-encoded path controls.
  Rationale: Machine cache paths and transport syntax are not application groups.
  Date/author: 2026-09-09, Codex.

## Outcomes & Retrospective

Completed on 2026-09-10 local time. Scenery HTTP implementation `38189f96`,
generated/native parity, main publication and installation are complete. ONLV
source/client migration `c402478c` is integrated on main and origin, with final
handoff `66386c29`; concurrent work was preserved.

The explicitly approved [retained-state upgrade](0173-retained-state-spec-upgrade.md)
ships in `758cbf79` and resolves the runtime blocker without replacing ONLV.
The original port 4920 serves real AHJ/tariff list, filter and detail requests
through `/api/solar/...`; old catalog paths return 404 and root-package Maps
reads remain unchanged. Exact pre-start SQL inventories (116 tables / 67,684
rows) and all 551 object hashes/totals match. Go, generation, native build,
repo/app harness and doctor checks pass; Chrome reports no app console errors.
No write-capable solar operation, production deployment or full release was
claimed by this read-only application acceptance.

## Context and Orientation

`internal/compiler/compiler.go` builds source/effective/expanded graph snapshots.
Local module resources already record `workspace_package_root` and module
ancestry. `internal/compiler/http.go` validates the resulting route namespace;
`data_expand.go` turns CRUD HTTP declarations into ordinary bindings.
Generators in `internal/generate` already join gateway base paths with binding
paths. `internal/spec/catalog.go` owns semantic revision review gates.

ONLV uses this sibling Scenery checkout through its Go module replacement.
`app.scn` installs many modules under `solar/`. Generated frontend clients and
hard-coded HTTP consumers must change together; Go package and database identities
must remain unchanged. Existing diagnostic/UI work is not part of URL migration.

## Milestones

1. One canonical grouped path with source provenance and collision coverage.
2. Matching generated Go routes, TypeScript descriptors and OpenAPI output.
3. Validated and installed Scenery main, then a working in-place ONLV cutover.

## Plan of Work

Add a compiler transformation for local binding and CRUD HTTP paths using each
owning module's normalized workspace package root. Reject nonliteral group
segments with the existing invalid-HTTP-path diagnostic. Exclude locked registry
module ancestry. Bump the owning semantic digest and document both views.

Exercise flat and nested directories, nested module declarations, nonroot gateway
prefixes, explicit repeated segments, CRUD expansion, invalid groups, collisions,
and change-sensitive revisions. Retain an external native application probe for
the generated HTTP server boundary. Regenerate the three committed client fixtures.

Only after Scenery validation, commit/push and install it. Stop ONLV's old runtime
without data-deletion flags before regenerating clients and restarting with the
new binary. Update raw URL consumers using a before/after endpoint inventory.

## Concrete Steps

Commands run from the Scenery root unless another directory is named:

    go test ./internal/spec ./internal/compiler ./internal/parse ./internal/generate ./internal/contractagent ./cmd/scenery ./scripts/verify
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write
    go run ./scripts/verify --probe native-contract --summary --write

After inspecting/staging exact diffs, commit and push main. Prepare the dashboard
with `./scripts/build-dashboard-ui-embed.sh`, then perform the explicitly requested
`go install ./cmd/scenery` and verify `scenery version -o json` against HEAD.

From `/Users/petrbrazdil/Repos/onlv`, use the coherent CLI for generation/check,
`go test ./...`, `just repo-harness`, and `scenery harness -o json --write`.
Run lint, typecheck and build in each frontend whose generated client/raw URLs
change. Exercise affected catalog requests at the Scenery root in the browser.

## Validation and Acceptance

Expected classes are compiler/generator, CLI JSON/current specification and
runtime. The full self-harness supersedes quick; inspect its changed-area union
and run any additional required commands. The `native-contract` probe must prove
grouped generated routes and reject the old unprefixed routes, then clean up its
owned processes. No data or tenancy behavior changes.

ONLV acceptance requires successful new grouped requests, no remaining authored
old-path consumers in the selected route inventory, coherent generated clients,
and a running runtime using the installed source revision. Record independent
failures from concurrent changes without repairing or staging unrelated work.
Full release certification, benchmarks and all-root timing audits are unselected
because the human requested delivery, not those additional measurement workflows.

## Idempotence and Recovery

Regeneration is explicit and ownership checked. Never manually edit generated
files. Do not create another ONLV checkout or change its origin, SQL ownership,
storage namespace, or data. An interrupted cutover resumes by completing
generation with the selected binary before `scenery up --detach --wait ready`.
Never add old-route compatibility aliases or rewrite receipts to force readiness.

## Artifacts and Notes

Keep transient endpoint inventories and verification artifacts outside authored
source. Record final commands, revisions, limitations and live-route evidence
here before closing the plan.

Scenery validation on 2026-09-09: affected Go packages and `go test ./...` pass;
`golangci-lint run ./...` reports zero issues; all three fixture generations pass;
TypeScript conformance reports 27 tests / 101 assertions; both generated-client
and catalog `tsc` projects pass. Default self-verification passes with 41 existing
knowledge-freshness and 23 architecture warnings. The selected `native-contract`
probe passes, including `/api/group1/nested/house/process` returning 200, the old
`/api/house/process` returning 404, generated TypeScript invocation, public restart
reuse and owned-process cleanup. No release, benchmark or all-root timing lane
was selected.

ONLV validation passes generation/freshness, contract and implementation checks,
`go test ./...`, `just repo-harness`, native development build, all nine
`scenery harness` checks, lint/typecheck/build in both generated frontends,
47 catalog unit tests and 22 standalone fixture-browser cases. Each client's
213 bindings have exactly 118 grouped paths and 95 unchanged paths, with no
other transport code change. These checks cover the shared worktree; concurrent
frontend work was not swept into the migration commit. The live `up` and storage
inspection commands remain blocked by SCN8003. A compatible pre-upgrade CLI
confirms the stopped root retains the same 551 objects, 1,618,434,235 bytes,
worktree incarnation and storage generation. No ownership or data repair was
attempted to bypass the guard.

## Interfaces and Dependencies

No new dependency, environment knob, CLI command, group DSL or JSON field is
required. The existing effective `http.path` carries the group, and its field
provenance identifies the transformation. Source `http.path`, service identities,
Go imports and durable names remain authored and unchanged.
