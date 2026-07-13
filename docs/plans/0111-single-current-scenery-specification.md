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
- [x] 2026-07-13 - Step 17: moved the sole dashboard source to `apps/console`, changed the path-mode route to `/console/` without an alias, and synchronized package, toolchain, embed, harness, agent-instruction, and current documentation references.
- [x] 2026-07-13 - Step 18: renamed root contract files, CLI command and inspection files, and the tightly coupled build bundle/input identifiers by responsibility; behavior and the deferred `internal/vnext` package path remain unchanged.
- [x] 2026-07-13 - Step 9 machine-envelope slice: centralized `scenery.cli` and `scenery.cli.event` under `internal/machine`, added producer identity and exact schema/spec revisions, removed direct CLI constructors, and made decoding strict with no alternate schema path. Envelope schema revisions are `sha256:2f9c241ad368ba2a9495d8b8025bb7de5a0fde893da4b5659dde8774519abe6e` and `sha256:85aeaf3276c078fe745cc7d327447caebd7926d5410a2a3dcbb036b188e61a75`.
- [x] 2026-07-13 - Milestone 1: removed the temporary refresh-cookie compatibility path and tag-specific upgrade selection; `scenery upgrade` follows the current release channel.
- [x] 2026-07-13 - Milestone 2: removed authored language edition/profile selectors from current source, fixtures, parsing, graph projections, and generated artifacts; resource use now determines required behavior.
- [x] 2026-07-13 - Milestone 3: established `internal/spec` as the canonical current catalog, made resource kinds unversioned with digest schema revisions, added exact manifest catalog identity, and centralized strict `scenery.cli` / `scenery.cli.event` envelopes under `internal/machine`.
- [x] 2026-07-13 - Milestone 4: removed Scenery package/provider semantic-version selection and retained exact integrity plus descriptor/ABI revisions for locked content.
- [x] 2026-07-13 - Milestone 5: moved the normative specification to `docs/spec`, the dashboard to `apps/console` at `/console/`, and current public product naming away from `vnext` / `consolenext`; the internal implementation-package extraction remains Milestone 6.
- [x] 2026-07-13 - Step 12 durable-state audit and transition handling: proved application databases, durable execution rows, object stores, dashboard state, global deploy ownership, agent identity, and `.scenery.json` retain their existing persisted shapes; added an atomic, idempotent, identity-only migration for `.scenery/approval-trust.json`; made `scenery upgrade` refuse binary replacement when the app root discoverable from its current directory contains legacy interrupted recovery state; and kept the same refusal at current change/deployment entry points as the safety net for other app roots. Focused tests prove preserved trust-key encodings, backup and completion-marker behavior, crash completion after the trust-store rewrite, pre-replacement refusal, and byte-for-byte preservation of refused journals, locks, and target binary.
- [x] 2026-07-13 - Step 12 durable-state follow-up: migrated deploy ownership, agent registry/session/substrate/route/lease state, agent and edge process state, privileged edge targets, edge DNS state, managed Postgres credentials, and dev port leases to strict current artifact identities. Each migration preserves payload values, writes the exact former bytes to an owner-only `.legacy.bak`, atomically/fsyncs the replacement, records `.legacy.migrated`, and is restart-idempotent; application databases, durable rows, object data, and `.scenery.json` remain unchanged.
- [x] 2026-07-13 - Milestone 6: split source/compiler, graph, evolution, generation, deployment planning, and contract-agent protocol composition into stable responsibility packages; moved package-owned tests with them; migrated command/build consumers; and removed `internal/vnext` rather than retaining a compatibility facade.
- [x] 2026-07-13 - Cross-process artifact identity reset: bound checked compiler/evolution/generation/deployment/build artifacts to static revisions of their complete self-normalized schemas; expanded private journal, lock, provider, cache, and OpenAPI descriptors to complete type shapes with projection tests; and replaced deployment-state `api_version` with strict current artifact identity.
- [x] 2026-07-13 - Milestone 7: added tracked-source residue and dependency-direction guardrails; regenerated current Go, TypeScript, OpenAPI, fixture, and embedded-dashboard artifacts; migrated ONLV to the unversioned generated-client path and package name; and completed repository, schema, runtime, dashboard, storage, PostgreSQL, parallel-worktree, and client-app acceptance.

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
- The machine-identity reset reaches one durable configuration file: `.scenery/approval-trust.json`. Its Ed25519 public keys are user-owned trust roots, but the current strict decoder rejects the former `api_version` shape and suggests recreating the store. Recreating is data loss; the keys need an atomic identity-only migration.
- Change and deployment apply journals are normally transient, but they are non-disposable while an apply is interrupted because they own source backups, prior deployment bytes, and provider rollback progress. The current strict readers reject former journal/lock identities before recovery, so a flag-day upgrade must migrate/recover them or refuse the binary replacement while old recovery state exists.
- Provider descriptor revisions initially included producer provenance, which made the same semantic provider compile to different locks under `go run` and a built binary. Producer metadata remains reported, but descriptor identity now hashes only the semantic provider contract and is proven stable across processes.
- Generated-artifact freshness initially compared producer metadata and therefore treated artifacts as stale after only the binary build identity changed. Freshness now validates exact schema/spec identity and compares the semantic document with producer provenance excluded.
- Runtime built-in providers still registered sources with `@semver` suffixes after source package selection was removed. Registering the plain current source and testing all built-ins exposed and closed that last runtime-only compatibility seam.
- The complete generated-app and generated-package schemas required one final identity reset after their required fields drifted from the actual producer shape. Removing the nonexistent top-level `generator_version`, regenerating descriptors, and validating every harness payload made the checked schemas authoritative again.

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
- Decision: Checked cross-process artifacts carry static exact schema revisions derived from their complete self-normalized JSON Schemas; private artifacts without checked schemas hash complete structural descriptors guarded by type-shape tests.
  Rationale: Partial field summaries can remain unchanged while real wire shapes drift. Exact checked-schema bindings and complete private projections make any structural change an intentional identity reset without adding schema files for every transient cache or lock.
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
- Decision: Keep JSON-RPC contract-agent composition in a thin `internal/contractagent` package instead of placing it in compiler or evolution.
  Rationale: The protocol combines immutable compiler results and graph/schema queries with explicit evolution mutations. Giving it a narrow adapter boundary avoids a compiler-to-evolution dependency while assigning no domain policy or compatibility behavior to the adapter.
  Date/Author: 2026-07-13 / Codex.
- Decision: Move each `internal/vnext` file once into its final responsibility package; do not create a temporary `internal/language` mega-package.
  Rationale: A neutral intermediate rename followed by a split doubles churn across roughly 39,000 lines without changing behavior or reducing ownership ambiguity.
  Date/Author: 2026-07-13 / Codex.
- Decision: Activating this plan does not silently change today’s contracts. Plan 0110 remains the current cookie contract until Milestone 1 lands; that milestone then marks 0110 superseded and records the forced-login consequence.
  Rationale: An ExecPlan records intended work. Current implementation and `docs/local-contract.md` remain authoritative until a milestone is actually shipped.
  Date/Author: 2026-07-13 / Codex.
- Decision: Treat approval trust keys and interrupted apply recovery records as durable state, not as disposable old artifacts.
  Rationale: Trust keys cannot be reconstructed, and interrupted journals are the only safe route back to pre-apply source/provider state. Pending plans, detached approvals, generated outputs, and completed receipts can be regenerated or reissued; these two classes cannot.
  Date/Author: 2026-07-13 / Codex.

## Outcomes & Retrospective

Scenery now has one current source language, catalog, compiler/runtime path, and
machine protocol. Source has no edition, profile, application/package version,
Scenery-version range, or provider-version selector. Logical resource and
machine kinds are unversioned; exact digest revisions identify schemas, the
catalog, generated artifacts, plans, locks, and runtime/deployment ABIs. The
final catalog revision is
`sha256:f0169e1b831f438fb57320be3bcf5e80ddf96aaea6fcbd2f23124ecd1cb3b64f`.

The former `internal/vnext`, `docs/specs/vnext`, `apps/consolenext`, and
`/consolenext/` current architecture is gone. Responsibilities now live in
`internal/scn`, `internal/spec`, `internal/graph`, `internal/compiler`,
`internal/evolution`, `internal/generate`, `internal/deployplan`,
`internal/machine`, and the thin `internal/contractagent` adapter. The sole
dashboard is `apps/console` at `/console/`. Architecture and tracked-source
residue checks fail future reintroduction outside explicit durable-migration
and historical-plan allowlists.

Durable identity changes preserve user-owned data through strict, atomic,
restart-idempotent migrations with exact backups and completion markers.
Interrupted legacy change/deployment recovery state instead blocks upgrade and
current mutation with a previous-binary recovery instruction. Disposable
generated artifacts, locks, pending plans, and caches fail closed and are
regenerated or replanned under the current identity.

Acceptance completed on 2026-07-13:

- `go run ./cmd/scenery harness self --summary --write` passed all lanes,
  including full Go tests, Go vet, architecture and contract drift, schema
  validation, parallel worktree runtimes, PostgreSQL, storage, dashboard
  typecheck/build, TypeScript client conformance/typecheck, and the fixture
  matrix. Evidence is `.scenery/harness/self-latest.json`.
- Both canonical app fixtures passed current-source generation, generation
  freshness, and contract checks after regeneration.
- Dashboard lint, typecheck, build, embedded-bundle generation, generated
  TypeScript conformance, and generated-client typecheck passed.
- ONLV was migrated in place to `apps/scenery-client`, package
  `@onlv/scenery-client`, and `apps/next/src/generated/scenery`; its contract
  check, generated-client freshness, Go tests, application lint/typecheck/build,
  repository harness, `/console/` route, and live current-source runtime path
  passed without using the shared installed Scenery binary.
- `scenery upgrade --help` and focused tests prove there is no historical
  `--version` selection; focused auth tests prove only `scenery_refresh` is
  read, issued, and cleared.

The largest implementation risk was not parsing; it was identity consistency
across built binaries, generated documents, provider locks, and durable state.
Making schema/catalog/ABI hashes semantic and producer-independent, then
running the same artifacts through source and built binaries, was the useful
acceptance boundary. No compatibility dispatcher, selector replacement,
feature-flag framework, or package-manager update workflow was added.

## Context and Orientation

Scenery source is HCL-like `.scn`. Root `scenery.scn` installs package-local
`scenery.package.scn` modules. Current source starts with application/workspace
declarations and has no language, edition, profile, application/package version,
or Scenery-version selector. `internal/scn` owns parsing and formatting,
`internal/spec` owns the exact current catalog, and `internal/graph` owns the
canonical resource graph and general revisions.

The current canonical graph uses unversioned logical resource kinds such as
`scenery.record` and independent digest-valued schema revisions. The canonical
catalog and diagnostic catalog share one exact `spec_revision`; agent schema and
capability surfaces expose the same identities.

The compiler manifest contains `kind`, `schema_revision`, `spec_revision`, the
diagnostic-catalog revision, application identity, `contract_revision`,
resources, source map, and diagnostics. Independent workspace, implementation,
deployment, HTTP-surface, OpenAPI, package ABI, provider ABI, and artifact
revisions are calculated by `internal/compiler`, `internal/graph`,
`internal/generate`, `internal/evolution`, and `internal/deployplan`. Their
projections include the current `spec_revision` and use unversioned hash domains.

Machine CLI envelopes are centralized under `internal/machine`. The current
schemas expose `scenery.cli` and `scenery.cli.event` logical kinds, exact
schema/spec revisions, producer identity, and strict current decoding without a
multi-version dispatcher.

Registry and provider identity lives in `internal/compiler/lock.go`, module
compilation, source schemas, generated contract metadata, and provider
deployment/runtime checks. Lock entries pin exact source integrity plus
descriptor, package-contract, capability, and runtime/deployment/migration ABI
identities; no semantic version, edition, or profile selection remains.

Current normative prose is under `docs/spec/`, including its child `AGENTS.md`.
The sole dashboard source is `apps/console/`; route, harness, toolchain, embed,
docs, and browser paths use `/console/` with no former route alias.

The former fixed-cookie exception from plan 0110 is removed: current auth reads,
issues, and clears only `scenery_refresh`. Historical ExecPlans may keep
historical words and identifiers, but current source, docs, schemas, generated
artifacts, and tests converge on the singular current contract.

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
apps/console/node_modules/.bin/tsc -p internal/vnext/testdata/tsconfig.generated-clients.json
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

### Step 12 durable-state inventory (2026-07-13)

Read-only inspection of the current worktree produced this classification:

| Surface and location | Classification | Evidence and required handling |
|---|---|---|
| CLI/event envelopes; manifests; OpenAPI; generated Go/TypeScript descriptors; runtime bundles under `.scenery/build/runtime/`; generated fixture SQL under `.scenery/fixtures/`; harness output; immutable provider cache entries | Disposable process output, generated artifact, or cache | Their logical/schema identities changed intentionally. Regenerate, rebuild, rerun, or reinstall the exact provider cache entry; no durable migration or compatibility decoder is required. |
| Issued plans under `.scenery/plans/issued/`, detached approval tokens, unapplied deployment/change plans, and completed plan/receipt files under `.scenery/changes/applied/` and `.scenery/deployments/applied/` | Revision-bound transaction artifacts | Re-plan or reissue after the flag day. Applied rename receipts remain read-only evidence: the loader extracts `rename_receipts` and validates their base/target revisions and digest, so unknown outer identity fields do not mutate user state. |
| Auth PostgreSQL tables, including `scenery_auth_refresh_sessions` | Durable user state; shape unchanged | No files under `auth/db` changed. Cookie selection is transport-only; existing canonical `scenery_refresh` sessions continue and former-cookie-only browsers intentionally log in again. |
| Durable tasks/jobs/events/steps/signals/schedules/worker tokens in the app database | Durable user state; shape unchanged | No files under `internal/durable/store` changed. Generated descriptor/contract revisions changed, but persisted task `external_name` and revision semantics did not receive a storage migration in this worktree. |
| Application Postgres schemas/data and storage-cell/object-store trees | Durable user state; shape unchanged | No files under `internal/postgresdb`, `internal/postgresname`, `storage`, or `internal/storage` changed. The object/datasource edits are package-comment naming only. |
| Dashboard/devdash persisted state | Durable local user state; shape unchanged | No files under `internal/devdash` changed. `apps/console` and `/console/` rename source/routes/RPC presentation without rewriting the backing store. |
| Global deploy ownership registry at `Paths.DeployPath` (`.../scenery/agent/deploy.json`) | Durable deployment ownership; identity-only migration implemented | The current registry carries strict artifact identity. Loading the former registry preserves targets, domains, app roots, ownership, and ACME email; writes exact former bytes to `.legacy.bak`; atomically installs the current bytes; and records `.legacy.migrated`. |
| Agent registry/session/substrate/route/lease records, agent/edge process state, privileged edge target, edge DNS state, managed Postgres state, and dev port leases | Durable local operational state; identity-only migrations implemented | The shared migration helper validates the former identity, preserves payload fields and credentials, writes an owner-only exact backup, atomically/fsyncs the current artifact, and records an idempotent completion marker. Focused migration tests cover nested registry records and each standalone state family. |
| `.scenery.json` app configuration | Durable user configuration; shape unchanged | Application configuration does not use the retired selectable specification identities and is not rewritten. |
| `scenery.scn`, package source, and optional `scenery.lock.scn` | Tracked source input, not runtime state | Current source is edited in the flag day. Lock entries drop semantic `version` while preserving exact source, integrity, descriptor, package-contract, capability, and runtime/deployment/migration ABI identities. This checkout has no `scenery.lock.scn`; external apps must update the tracked lock source rather than rely on a runtime translator. |
| `.scenery/deployments/<name>.json` applied-state snapshot | App-local deployment evidence; prior bytes remain safe | New snapshots use the strict current `scenery.deployment-state` artifact identity. Existing prior bytes are read opaquely, copied into a recovery journal, restored byte-for-byte on rollback, and replaced only after a successful new apply; the global deploy registry remains authoritative for routing ownership. No eager rewrite is required. |
| `.scenery/approval-trust.json` | Durable user trust configuration; identity-only migration implemented | On first load of the former `{api_version, keys}` shape, Scenery validates every key, preserves the encoded key values and exact legacy bytes, writes the owner-only `.legacy-v1.bak` backup, atomically/fsync-renames the current identity, and atomically writes `.legacy-v1.migrated`. A restart after the current-file rewrite completes the marker only when the strict current identity and all keys match the backup. Once marked, only the strict current decoder runs. Recreating or deleting the store is forbidden. |
| `.scenery/transactions/change.lock`, `change-apply.json`, and transaction backup directory | Non-disposable while interrupted; safe refusal implemented | Before installing a downloaded binary, `scenery upgrade` checks the app root discoverable from its current directory and refuses a former lock or journal identity with an explicit instruction to recover using the previous Scenery binary. Current change recovery repeats that check before decoding or mutation, covering roots not visible to the upgrade process. The legacy bytes and backup directory remain untouched. |
| `.scenery/deployments/apply.lock` and `.scenery/deployments/journal/*.json` | Non-disposable while interrupted; safe refusal implemented | The same bounded upgrade preflight checks the discoverable app root before replacement. Current deploy apply remains the safety net for other app roots: it detects the former lock or recovery-journal identity before stale-lock removal, provider rollback, state restoration, or other mutation and returns the same previous-binary recovery instruction. The legacy bytes remain untouched. No global app-root walk or registry scan is attempted. |

The local checkout contained no approval trust store, issued/applied plans,
lockfile, change transaction, or deployment journal/state at audit time. That
absence made this checkout safe but was not sufficient product handling. The
trust-store migration and legacy recovery-state refusal now cover the two
non-disposable transition hazards. Focused current-shape, migration, refusal,
and applied-rename tests passed with:

```sh
go test ./internal/agent ./internal/evolution ./internal/deployplan ./cmd/scenery -run 'Test(Approval|ArtifactMigration|DeployRegistryMigration|DeploymentRecovery|ChangeTransaction|AppliedRename|Legacy)'
go test ./cmd/scenery -run 'TestRunUpgradeRefusesLegacyRecoveryStateBeforeReplacingBinary'
```

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
