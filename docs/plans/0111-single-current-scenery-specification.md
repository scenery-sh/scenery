# Single Current Scenery Specification

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current while the flag-day change
is implemented.

## Purpose / Big Picture

Make edition 2027 the last migration codename, then remove the product mechanism
for selecting Scenery editions, profiles, first-party schema versions, package
compatibility ranges, or historical release targets. The current Scenery binary
implements one evolving source language, one current specification catalog, one
compiler/runtime path, and one machine protocol.

The permanent invariant is:

> Revisions identify exact artifacts and fail closed on mismatch; they never
> select an older parser, catalog, decoder, generator, runtime adapter, or
> release.

Source should start with the application instead of a language selector:

```hcl
application "hello" {}

workspace {
  implementation_root "application" {
    path             = "."
    revision_include = ["**/*.go", "go.mod", "go.sum"]
  }
}

go_module "application" {
  root        = "."
  import_path = "example.com/hello"
}
```

Package source should declare only its current contract identity:

```hcl
package "service" {
  go_contract {
    import_path = "example.com/hello/service"
  }
}
```

Machine output should use an unversioned logical kind and immutable content
revisions rather than a selectable API version:

```json
{
  "kind": "scenery.cli",
  "schema_revision": "sha256:...",
  "spec_revision": "sha256:...",
  "producer": {
    "version": "v0.3.2...",
    "commit": "..."
  },
  "ok": true,
  "data": {},
  "diagnostics": []
}
```

Remove these product selectors and parallel identities:

- `language { edition = "2027" ... }` and authored `require_profiles`;
- `Edition`, manifest `Profiles`, profile dependency maps, and profile gating;
- first-party logical kinds such as `scenery.record/v1` and machine identifiers
  such as `scenery.cli.v1`;
- `package.scenery_version`, application/package semantic-version identity,
  registry version ranges, and provider edition/profile negotiation;
- `scenery upgrade --version` and tag-specific install lookup;
- active `vnext` / `consolenext` architecture and route names;
- the temporary `onlv_refresh` request and logout compatibility path.

Retain these exact identities because they make builds and state safe rather
than creating selectable product versions:

- source, workspace, contract, implementation, deployment, HTTP-surface,
  OpenAPI, schema, generator, toolchain, descriptor, and runtime ABI revisions;
- integrity hashes and lockfile content identities;
- producer version/commit/build metadata reported by `scenery version`;
- semantic evolution analysis, migration consequences, immutable plans,
  approval binding, rename receipts, and explicit state migrations;
- external standard and tool versions such as Go, OpenAPI, PostgreSQL, Caddy,
  Victoria, and third-party module versions.

There is no compatibility parser or automatic old-source translator. Checked-in
fixtures and current client applications move in the same flag-day milestone.
Disposable caches, generated artifacts, pending plans, and receipts produced by
the old specification are rejected with a regenerate or re-plan instruction.
Persistent user data is never silently discarded: each durable state format
that changes gets an explicit, idempotent migration with backup and completion
marker, but no runtime dispatch to an older implementation.

## Progress

- [x] 2026-07-13 - Read root instructions, `PLANS.md`, current contracts, active plans, completed edition/v0 cleanup plans, source schemas, compiler/profile code, lock/provider code, CLI envelopes, upgrader, auth compatibility, current specifications, dashboard paths, and documentation status.
- [x] 2026-07-13 - Reconciled the supplied design conversation with current source: verified live edition/profile selectors, versioned kind/schema identities, `internal/vnext`, `apps/consolenext`, `upgrade --version`, package/provider version constraints, and plan-0110 cookie compatibility; rejected the nonexistent `scenery update` command as an implementation assumption.
- [x] 2026-07-13 - Created this ExecPlan and registered it in `docs/plans/active.md` and `docs/knowledge.json`; no product behavior changed.
- [ ] Milestone 1 - Remove live compatibility and historical release selection.
- [ ] Milestone 2 - Remove authored edition/profile selection and infer required behavior from the graph.
- [ ] Milestone 3 - Establish one current catalog and reset first-party logical and machine identities.
- [ ] Milestone 4 - Remove Scenery package/provider semantic-version selection.
- [ ] Milestone 5 - Remove `vnext` / `consolenext` product and specification naming.
- [ ] Milestone 6 - Move the current implementation into stable responsibility packages without an intermediate mega-package.
- [ ] Milestone 7 - Add final residue guardrails and complete generated, client-app, runtime, and documentation acceptance.

## Surprises & Discoveries

- `internal/vnext` is no longer an alternate implementation. It currently owns about 39,000 lines across 142 top-level Go files, including parsing, formatting, source schemas, canonical graphs, revisions, evolution, mutation plans, deployment plans, Go/TypeScript/OpenAPI generation, and implementation verification. A blind directory rename would preserve the wrong ownership boundary.
- The checked-in source migration is bounded: the repository currently has eight `.scn` files, with four authored `language` / edition / profile declarations and four authored `scenery_version` constraints. The larger cost is generated artifacts, tests, schemas, and public machine identities.
- `cmd/scenery/main.go` already rejects every CLI envelope except `scenery.cli.v1`. The `.v1` cleanup is therefore a deliberate schema/identity reset and centralization, not removal of a functioning multi-version decoder.
- `resourceSchemas` is keyed by versioned kind strings and `CoreSchema` reports the kind itself as `schema_revision`; helpers repeatedly trim `/v1` to recover block names. This is the clearest structural evidence that logical kind and schema identity are conflated.
- `internal/vnext/lock.go` requires registry version constraints and carries provider `Version`, `Editions`, `Profiles`, and string ABI versions. There is no implemented `scenery update` command, and registry upgrades are currently reported as a future capability. This plan must remove selection without inventing a package-manager workflow.
- `application.version` feeds implementation revision and OpenAPI display version; generated Go contracts and adapters also carry `PackageVersion`. Removing those fields requires one coordinated artifact identity reset rather than a parser-only edit.
- Plan 0110 is the only intentionally active application compatibility exception found in current contracts. Removing it immediately invalidates browser sessions that only carry `onlv_refresh`; that is an explicit product consequence, not an accidental regression.
- The normative directory still calls itself `docs/specs/vnext`, version `0.5-draft`, target edition 2027, and profile-specific `V1`, even though current docs call `.scn` the singular application model.
- `apps/consolenext` is the active embedded dashboard source and `/consolenext/` is the path-mode dashboard route. Renaming it requires source, embed scripts, toolchain metadata, route manifests, tests, docs, and browser proof together; there is no route alias afterward.

## Decision Log

- Decision: Edition 2027 is the final migration codename, not a permanent selector.
  Rationale: Keeping a hard-coded edition field preserves a dead branching mechanism and gives future incompatible compilers the same meaningless identity.
  Date/Author: 2026-07-13 / user and Codex.
- Decision: Use one `spec_revision` digest of the canonical machine catalog plus per-artifact `schema_revision` digests; do not add separate selectable spec/catalog version registries.
  Rationale: One catalog revision and concrete schema revisions provide exact provenance with less identity machinery. A mismatch means regenerate, recompile, or re-plan.
  Date/Author: 2026-07-13 / Codex.
- Decision: Delete authored profiles without replacing them with a feature-toggle framework.
  Rationale: Resource kinds already reveal which behavior is used. Existing inspection consumers may receive compiler-derived `features_used`, but source cannot select features and unavailable behavior fails directly.
  Date/Author: 2026-07-13 / Codex.
- Decision: Preserve evolution and migration analysis while deleting compatibility execution.
  Rationale: Comparing current canonical graphs and planning safe state/deployment changes is a current product capability. It does not require an old parser, old runtime adapter, or old artifact decoder.
  Date/Author: 2026-07-13 / user and Codex.
- Decision: Disposable artifacts fail closed; durable state migrates explicitly.
  Rationale: Regenerating code, caches, plans, and receipts is safer and smaller than compatibility decoding. Databases, object stores, deploy registries, and other persistent user state cannot be discarded and need idempotent migrations instead.
  Date/Author: 2026-07-13 / Codex.
- Decision: `scenery upgrade` follows one current channel, while `scenery version`, immutable Git tags, release checksums, and producer metadata remain.
  Rationale: Build provenance and rollback evidence are not user-selectable language versions. Normal installation must not offer arbitrary historical targets.
  Date/Author: 2026-07-13 / user and Codex.
- Decision: Remove Scenery-owned package/provider semver selection and lock exact content identities; do not invent `scenery update` in this plan.
  Rationale: The current CLI has no update workflow. Source ranges and solvers are unnecessary for one-current-artifact semantics; a future registry plan may define how an exact lock is replaced.
  Date/Author: 2026-07-13 / Codex.
- Decision: Remove `application.version` and `PackageVersion` rather than replacing them with speculative release metadata.
  Rationale: OpenAPI can display the contract revision, and generated/runtime compatibility already has content revisions. Optional human release metadata can be added later if a real consumer appears.
  Date/Author: 2026-07-13 / Codex.
- Decision: Move each `internal/vnext` file once into its final responsibility package; do not create a temporary `internal/language` mega-package.
  Rationale: A neutral intermediate rename followed by a split doubles churn across roughly 39,000 lines without changing behavior or reducing ownership ambiguity.
  Date/Author: 2026-07-13 / Codex.
- Decision: Activating this plan does not silently change today’s contracts. Plan 0110 remains the current cookie contract until Milestone 1 lands; that milestone then marks 0110 superseded and records the forced-login consequence.
  Rationale: An ExecPlan records intended work. Current implementation and `docs/local-contract.md` remain authoritative until a milestone is actually shipped.
  Date/Author: 2026-07-13 / Codex.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Scenery source is HCL-like `.scn`. Root `scenery.scn` installs package-local
`scenery.package.scn` modules. Today every authored root starts with exactly one
`language` block. `internal/vnext/compiler.go` validates `edition == "2027"`,
reads `require_profiles`, expands `ProfileDependencies`, validates against
`SupportedProfiles`, and writes edition/profiles into every graph view.
`internal/vnext/model.go` defines those constants and includes edition/profiles
in manifest and contract-revision projections. `internal/vnext/source_schemas.go`
defines the authored `language`, `application.version`, `package.version`, and
`package.scenery_version` fields.

The current canonical graph uses strings such as `scenery.record/v1` as both
logical resource kinds and schema identities. `internal/vnext/schemas.go` owns
`resourceSchemas`, `CoreSchema`, authored-to-core schema projection, and several
helpers that trim `/v1`. `internal/vnext/agent_schemas.go` and
`internal/vnext/agent.go` expose these identities to agent schema/capability
surfaces.

The compiler manifest in `internal/vnext/model.go` currently contains
`APIVersion`, `Edition`, `DiagnosticCatalog`, `Application.Version`, `Profiles`,
`ContractRevision`, resources, source map, and diagnostics. Independent
workspace, implementation, deployment, HTTP-surface, OpenAPI, package ABI,
provider ABI, and artifact revisions are calculated throughout
`internal/vnext/revisions.go`, generation files, change/migration planning, and
deployment planning. Those independent revisions remain, but their projections
must include the current `spec_revision` and use unversioned hash domains.

Machine CLI envelopes live mainly in `cmd/scenery/main.go`, while
`cmd/scenery/vnext.go`, `vnext_helpers.go`, `vnext_deploy.go`, and
`vnext_binding_cli.go` construct parallel envelope values directly. Current
schemas live under `docs/schemas/` and use first-party `.v1` identifiers. The
current binary accepts only `scenery.cli.v1`, so no multi-version dispatcher
needs preservation.

Registry and provider identity lives in `internal/vnext/lock.go`,
`module_compile.go`, source schemas, generated contract metadata, and provider
deployment/runtime checks. Lock entries carry semantic `Version` plus integrity
and descriptor/ABI fields; provider descriptors advertise `Editions` and
`Profiles`. The exact integrity and descriptor/ABI revisions remain while
semantic source selection disappears.

Current normative prose is the seven Markdown files under `docs/specs/vnext/`,
including its child `AGENTS.md`. Active dashboard source is
`apps/consolenext/`; `cmd/scenery/dashboard_ui_build.go` owns the root constant,
and route, harness, toolchain, embed, docs, and browser paths refer to
`/consolenext/`.

The only live first-party compatibility exception named by this plan is the
fixed `onlv_refresh` read/clear path in `auth/standard_config.go`,
`auth/standard.go`, `auth/standard_sessions.go`, and plan 0110. Historical
ExecPlans may keep historical words and identifiers, but current source, current
docs, schemas, generated artifacts, and tests must converge on the new singular
contract.

Definitions used by this plan:

- **Logical kind**: an unversioned stable name such as `scenery.record` or
  `scenery.cli`.
- **Specification revision**: a SHA-256 digest of the canonical current machine
  catalog. It identifies semantics and never chooses an implementation.
- **Schema revision**: a SHA-256 digest of one concrete artifact/envelope shape.
- **Producer identity**: the Scenery build version, commit, build time, and
  toolchain identity that created an artifact.
- **Evolution analysis**: comparison of current canonical graphs to identify
  API, security, state, deployment, and generated-client consequences.
- **Compatibility execution**: parsing, translating, dispatching, or running an
  older Scenery language/schema/runtime. This is forbidden.

## Milestones

1. **Remove live compatibility and release selection.** Delete the
   `onlv_refresh` read/clear branch and its transition docs/tests, mark plan 0110
   superseded with the intentional forced-login consequence, remove
   `upgradeOptions.Version`, `--version`, and tag-specific GitHub release lookup,
   and keep only latest/current-channel upgrade behavior.
2. **Remove edition and profile selection.** Delete the root `language` block,
   `Edition`, kernel/supported/dependency profile maps, profile validation and
   feature gating, edition/profile manifest fields, and source-schema support.
   Rewrite all current `.scn` sources in the same milestone. Retain behavior such
   as HTTP path tails directly in the one current grammar; unavailable resource
   kinds fail without a “missing profile” diagnostic.
3. **Create one current catalog and reset first-party identities.** Move current
   resource/source schema and diagnostic definitions into `internal/spec`, make
   logical kinds unversioned, calculate one canonical `spec_revision`, calculate
   concrete schema digests, centralize envelopes under `internal/machine`, and
   replace `.v1` / `/v1` first-party identifiers across manifests, lockfiles,
   plans, generated metadata, schemas, CLI events, fixtures, and harness samples.
   Old disposable artifacts are rejected; no alternate decoder is retained.
4. **Remove Scenery package/provider semantic-version selection.** Delete
   `package.scenery_version`, application/package version fields, registry
   version constraints, lock-entry semantic versions, provider
   edition/profile/version negotiation, generated `PackageVersion`, and semver
   helpers that become unused. Keep exact source, integrity, descriptor,
   capability, contract, and ABI revision digests. Use contract revision as the
   OpenAPI display version.
5. **Remove “next” product/spec naming.** Move normative prose to `docs/spec/`
   with unversioned filenames, rewrite it as the evolving current specification,
   rename compatibility prose/identifiers to evolution, move
   `apps/consolenext` to `apps/console`, and change the dashboard route to
   `/console/` without an alias. Rename public root and `cmd/scenery` files and
   identifiers whose only remaining meaning is `vnext`.
6. **Split stable implementation responsibilities.** Move files directly out of
   `internal/vnext` into `internal/scn`, `internal/spec`, `internal/graph`,
   `internal/compiler`, `internal/evolution`, `internal/generate`, and
   `internal/deployplan`, retaining `internal/machine` from Milestone 3. Keep
   parser/source/schema packages below compiler, runtime independent of compiler,
   and CLI dependent on narrow facades. Delete `internal/vnext` only when empty.
7. **Enforce and prove the singular contract.** Regenerate all checked-in
   artifacts and schemas, migrate current client apps in lockstep, add one bounded
   current-source residue guardrail, update all current docs/AGENTS/indexes, and
   complete cached Go, schema, generated-client, dashboard, fixture, ONLV, and
   self-harness acceptance.

## Plan of Work

Implement one milestone at a time and leave the repository green after each
milestone. A milestone that changes a public JSON shape, source grammar,
generated artifact, route, or durable state format must update its schemas,
fixtures, docs, and migration behavior in the same change. Do not postpone
contract sync to the last milestone.

Start with the two small live selectors. Removing `onlv_refresh` is a direct
deletion across auth and transition docs; removing `upgrade --version` collapses
release lookup onto the existing latest-release path. Keep version/build fields
in upgrade output because they report producer identity; only target selection
is removed.

Then remove the authored language block and profiles before changing every
machine identity. Compile resource behavior directly from the graph. Do not
replace profiles with authored booleans, capability flags, or a second registry.
If an existing inspection surface needs a summary, derive `features_used` from
resource kinds after compilation and keep it informational.

Perform the machine identity reset as one coordinated flag day. Introduce the
canonical catalog and digest functions first behind focused tests, then switch
manifest/resource/envelope types and every schema/golden/fixture consumer in the
same milestone. Use one unversioned cryptographic domain per revision family and
include `spec_revision` in contract, implementation, deployment, generator, and
plan projections. A spec mismatch must produce a stable diagnostic that says to
regenerate or re-plan; it must not dispatch by revision.

Remove package/provider semver only after the catalog reset so lock and descriptor
shapes need one rewrite, not two. Registry source blocks name a source; lock
entries pin exact integrity and descriptor/ABI revisions. Local modules remain
local paths. Go module and managed-tool versions remain because they belong to
external ecosystems and reproducible toolchains. Do not add a package solver or
`scenery update`; a future registry plan may define replacement of one exact
lock entry with another.

Once semantic names are stable, remove “next” naming and move implementation
files directly into final packages. Avoid an intermediate `internal/language`
directory. Move one coherent responsibility at a time, update imports, run its
focused tests, and only then move the next responsibility. Package extraction
must reduce coupling rather than wrap the old package behind one-implementation
interfaces.

Finish with a small residue rule over current product/source/docs. Historical
plans are allowed to preserve history. External standard versions, Go/toolchain
versions, database migrations, producer build versions, and content revisions
are explicitly allowlisted. The rule should reject reintroduction of edition or
profile selectors, first-party versioned kind/schema spellings, active `vnext`
or `consolenext` names, `onlv_refresh`, and `upgrade --version`.

## Concrete Steps

1. Record a tracked baseline inventory in this plan’s `Artifacts and Notes`: current `.scn` files, language/profile/scenery-version declarations, first-party versioned identifiers, `internal/vnext` file/line count, dashboard references, schemas, generated artifacts, and active client apps.
2. Delete `legacyRefreshCookieName`, `refreshCookieReadOrder`, legacy request selection, private legacy logout state, second clearing header, focused legacy tests, plan-0110 transition docs, and environment/runbook wording. Mark plan 0110 superseded only when this deletion lands; move its active index entry accordingly.
3. Remove `upgradeOptions.Version`, `--version` parsing/help/completion, tag-specific release URL selection, and explicit-version tests. Fetch only the current release channel. Retain current/target producer identities, checksums, dry-run, target path, toolchain sync, and deploy-helper drift reporting.
4. Delete the authored `language` schema and root-singleton requirement. Remove edition parsing and diagnostics, `Edition`, `KernelProfiles`, `SupportedProfiles`, `ProfileDependencies`, `profilesFromLanguage`, `normalizeProfiles`, `validateProfiles`, and profile-gated resource/security behavior.
5. Remove edition/profiles from `Manifest`, `PartialGraph`, graph views, contract revision input, agent capabilities, inspection output, schemas, and tests. Rewrite every current `.scn` file and current client application without a compatibility parser.
6. Preserve all currently implemented behavior directly. Replace “missing profile” checks with ordinary current-spec resource/field validation or `feature_unavailable` only when the installed binary truly lacks an advertised resource kind.
7. Create `internal/spec` from the existing resource schemas, authored source schemas, diagnostic catalog, and deterministic canonical encoder. Define unversioned logical kinds and calculate `spec_revision` from the canonical catalog. Do not add a second JSON catalog source unless implementation proves the Go catalog cannot generate required machine output.
8. Change resource kinds from `scenery.<kind>/v1` to `scenery.<kind>`. Remove every helper that trims `/v1` to recover a block type. Make `CoreSchema` return an unversioned kind plus a content `schema_revision` digest.
9. Create `internal/machine` for the one CLI envelope, event envelope, producer identity, schema-revision constants/digests, and strict current-schema decoding. Replace direct envelope construction in `cmd/scenery/main.go`, `vnext*.go`, harness code, detached children, generated CLI bindings, and tests.
10. Reset manifests, lockfiles, plans, receipts, provider descriptors/plans, generated metadata, dashboard RPC payloads, CLI command payloads, docs schemas, and harness examples from `api_version` / `schema_version` semantic labels to unversioned `kind` plus digest revisions where the shape crosses a process or durable boundary.
11. Update revision projections and hash domains. Include `spec_revision`; retain exact dependency integrity and ABI identities; invalidate old plans, receipts, and generated artifacts. Add focused mismatch tests proving no old decoder/dispatcher is called.
12. Inventory durable non-disposable state formats affected by Step 10. For each one, either prove its internal format is unchanged or add an atomic backup, idempotent migration, and completion marker. Never delete databases, object stores, deploy registry ownership, agent identity, or user configuration as a shortcut.
13. Delete `package.scenery_version`, `application.version`, `package.version`, module/provider source constraints, lock `Version`, provider `Version` / `Editions` / `Profiles`, generated `PackageVersion`, semantic version recommendation output, and semver helpers/dependencies left without callers.
14. Make lock entries exact: kind, source, integrity, descriptor revision, package contract revision, capabilities, and runtime/deployment/migration ABI revisions. Keep one descriptor per source in the current compiler. Do not introduce range resolution.
15. Use a short contract revision in OpenAPI `info.version`. Use package source/import identity plus package contract revision for generated Go identity checks. Keep Go toolchain version and external module versions untouched.
16. Move the six normative specifications and their child `AGENTS.md` from `docs/specs/vnext/` to `docs/spec/`; rename the umbrella to `SPEC.md` and companions to `go-implementation.md`, `http.md`, `http-path-tail.md`, `typescript-client.md`, and `evolution.md`. Remove draft/edition/profile language and state that this is the evolving current specification.
17. Move `apps/consolenext` to `apps/console`, update its child `AGENTS.md`, package/toolchain/embed paths, `dashboardUIRootRel`, route manifests, browser journeys, screenshots/artifacts, docs, and the canonical path route from `/consolenext/` to `/console/`. Do not retain the old route as an alias.
18. Rename public root files such as `vnext.go` and `vnext_*.go`, and `cmd/scenery/vnext*.go`, by responsibility. Update generator names such as `scenery.vnext.*` to unversioned current names.
19. Extract `internal/scn` for source/CST/positions/formatter, `internal/graph` for resources/canonicalization/provenance/revisions, `internal/compiler` for loading/defaults/expansion/validation/implementation verification, `internal/evolution` for diff/changes/migrations/rename receipts, `internal/generate` for Go/TypeScript/OpenAPI generation, and `internal/deployplan` for provider/deployment planning. Delete `internal/vnext` when empty.
20. Enforce dependency direction with existing architecture checks: `scn` and `spec` are foundational; compiler builds graph; evolution/generate/deployplan consume graph/spec; `cmd/scenery` consumes narrow facades; runtime and generated contracts never import parser/compiler.
21. Update root and child `AGENTS.md`, `SKILL.md`, README, local contract, agent guide, cookbook, environment/docs indexes, active/completed plans, all affected JSON schemas, generated fixtures, examples, and client-app guidance in every milestone that changes their contract.
22. Add one final current-surface denylist with a tiny explicit allowlist for historical ExecPlans and external standards/toolchains. Do not add separate regression tests for every deleted alias.

## Validation and Acceptance

Use Go’s native test result cache for ordinary, focused, and substantial final
validation. Use `-count=1` only for an explicit fresh performance measurement or
nondeterminism investigation. Do not install the shared CLI unless the human
explicitly asks; use the worktree-local self-harness binary.

At every milestone, run the focused packages that own the changed boundary,
then from `/Users/petrbrazdil/Repos/scenery` run:

```sh
gofmt -w <changed Go files>
go test ./...
go test ./cmd/scenery
go vet ./...
go run ./cmd/scenery inspect docs -o json
go run ./cmd/scenery harness self --summary --write
git diff --check
```

For source/catalog/generator milestones, also run the current equivalents of:

```sh
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery generate --check -o json
bun test internal/vnext/testdata/typescript_client_conformance.test.ts
apps/consolenext/node_modules/.bin/tsc -p internal/vnext/testdata/tsconfig.generated-clients.json
```

Update those paths as files move; the final commands must use the new stable
package and `apps/console` paths. For the dashboard rename, run from
`apps/console`:

```sh
bun run lint
bun run typecheck
bun run build
cd ../..
./scripts/build-dashboard-ui-embed.sh
scenery harness ui -o json --write
```

Final machine-contract acceptance must prove:

- `scenery check` and `scenery compile` accept current source with no `language`,
  edition, profile, application version, or `scenery_version` selector;
- current source, effective, and expanded graphs retain deterministic resources,
  provenance, and independent contract/implementation/deployment revisions;
- all first-party logical kinds are unversioned and every cross-process artifact
  carries digest `spec_revision` / `schema_revision` plus producer identity;
- an artifact, plan, receipt, or generated bundle with the old or wrong spec
  revision fails with a regenerate/re-plan diagnostic and never chooses another
  decoder;
- persistent state either keeps the same proven shape or migrates atomically
  without data loss;
- `scenery upgrade --help` exposes no `--version`, and upgrade fetches only the
  current channel;
- `scenery version` still reports exact build/commit/toolchain provenance;
- standard auth reads/issues/clears only `scenery_refresh` and current docs contain
  no `onlv_refresh` exception;
- the dashboard source is `apps/console`, `/console/` works, and
  `/consolenext/` is not routed;
- `go list ./...` contains no `internal/vnext` package;
- current docs expose one evolving specification under `docs/spec/` and no
  active edition/profile/version-selection language.

Run a final residue search over tracked current surfaces. Refine the allowlist
to external standards, tools, build provenance, and historical plans; do not
weaken the patterns merely to make the check green:

```sh
git grep -nE 'internal/vnext|runVNext|vnext_|docs/specs/vnext|apps/consolenext|/consolenext/|edition[[:space:]]*=[[:space:]]*"2027"|require_profiles|scenery_version|onlv_refresh|upgrade --version' -- ':!docs/plans/*'
git grep -nE 'scenery\.[a-z0-9_.-]+(/v[0-9]+|\.v[0-9]+)' -- ':!docs/plans/*'
```

The first command must have no current-product matches. The second must contain
only reviewed external/provenance exceptions; first-party logical kind and
machine schema matches fail acceptance.

When `/Users/petrbrazdil/Repos/onlv` is available, migrate its current `.scn`
source in the same source flag day and prove the real loop with the worktree-local
binary:

```sh
SCENERY=/Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery
"$SCENERY" check --app-root /Users/petrbrazdil/Repos/onlv -o json
"$SCENERY" generate --check --app-root /Users/petrbrazdil/Repos/onlv -o json
(cd /Users/petrbrazdil/Repos/onlv && go test ./...)
"$SCENERY" harness --app-root /Users/petrbrazdil/Repos/onlv -o json --write
"$SCENERY" up --detach --app-root /Users/petrbrazdil/Repos/onlv --wait ready
"$SCENERY" logs --app-root /Users/petrbrazdil/Repos/onlv --limit 1
```

Acceptance requires the ONLV app route, dashboard `/console/`, logs, metrics, and
traces to remain available without an old parser, route alias, or compatibility
runtime. Existing `onlv_refresh`-only browser sessions are expected to require
login again after Milestone 1.

## Idempotence and Recovery

Each milestone is a Git-tracked, independently testable flag day. Use `git mv`
for paths so history remains reviewable. Do not mix a responsibility move with
unrelated behavior after Milestone 5; compile and test after each package move.

Generated outputs must continue to use the existing transaction/journal
mechanism. If regeneration fails, rerun after fixing the owning generator; do
not hand-edit generated files or leave a mixed old/new identity set.

Old caches, generated artifacts, pending plans, approvals, and receipts are
disposable. Reject them with the precise command needed to regenerate or
re-plan. Do not add a compatibility decoder, silent translation, or fallback
catalog.

Persistent state is different. Before changing a durable state schema, enumerate
its on-disk/database locations, create a backup, write an idempotent migration,
fsync/commit it, and record a completion marker. Retry after interruption from
the marker and backup. Never delete user databases, object stores, deployment
registries, agent ownership records, or credentials to satisfy the no-legacy
residue search.

Client source has no shipped translator. Edit each current client and fixture
directly. Coordinate the Scenery and ONLV flag day so no released current binary
claims compatibility with old source. Before release, the whole milestone can
be reverted in Git; after release, forward migration is the supported recovery
path.

## Artifacts and Notes

Initial source review on 2026-07-13 found:

- 142 top-level files and about 39,000 Go lines under `internal/vnext`;
- eight checked-in `.scn` files, four authored language/edition/profile blocks,
  and four authored `scenery_version` constraints;
- seven Markdown files under `docs/specs/vnext` including its child `AGENTS.md`;
- active `apps/consolenext` source with toolchain, embed, route, harness, and docs
  consumers;
- one strict `scenery.cli.v1` decoder plus multiple direct envelope constructors;
- active explicit-version upgrade tests and tag-specific GitHub release lookup;
- active plan-0110 `onlv_refresh` compatibility.

Keep this section updated with milestone commits, exact catalog/spec/schema
digests, generated-artifact reset notes, durable migration inventories, residue
searches, self-harness artifact paths, and ONLV runtime proof. The supplied chat
is design input only; this file and the live tree are the resumable source of
truth.

## Interfaces and Dependencies

The target catalog API should remain small. Reuse the current deterministic
canonical JSON and revision helpers rather than adding a registry framework:

```go
package spec

type Kind string
type Revision string

type Catalog struct {
    Resources   map[Kind]ResourceSchema
    Diagnostics map[string]DiagnosticDefinition
}

func Current() Catalog
func RevisionOf(Catalog) Revision
func SchemaRevision(any) Revision
```

The target graph manifest contains one current identity, not selectors:

```go
type Manifest struct {
    Kind               string
    SpecRevision       spec.Revision
    SchemaRevision     spec.Revision
    Producer           machine.Producer
    Application        ApplicationIdentity
    ContractRevision   string
    Resources          []Resource
    SourceMap          map[string]SourceRecord
    Diagnostics        []Diagnostic
}
```

`ApplicationIdentity` contains the stable application name, not a semantic
version. A provider descriptor contains unversioned kind, schema/spec revision,
source, descriptor revision, capabilities, instance kinds, and content-addressed
runtime/deployment/migration ABI revisions. A lock entry contains no range or
selected semantic version.

The target dependency direction is:

```text
internal/scn      internal/spec
       \             /
        internal/graph
              |
      internal/compiler
       /      |       \
evolution  generate  deployplan
       \      |       /
          cmd/scenery

runtime and generated contracts do not import parser/compiler packages.
```

Use the Go standard library and existing HCL/canonicalization machinery. Remove
`golang.org/x/mod/semver` only if the final caller search proves no external Go
module or unrelated patch constraint still needs it. Add no feature-flag
framework, version registry, compatibility decoder, package solver, updater
command, one-implementation interface, route alias, or new environment knob.
