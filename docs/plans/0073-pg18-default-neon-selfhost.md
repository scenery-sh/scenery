# PostgreSQL 18 Default Neon Selfhost

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Onlava apps that use `dev.services.postgres.kind: "neon"` should run the same
way they do today, but the default self-hosted Neon database should be
PostgreSQL 18 instead of PostgreSQL 16. The app-facing surface must stay the
same: app authors still configure Neon through the existing `.onlava.json`
shape, use the same `onlava up`, `onlava db neon ...`, `onlava db branch ...`,
`onlava db psql`, worktree, Electric, and harness commands, and receive the
same JSON schemas except for additive version/image metadata where needed.

The implementation work is internal plumbing. Onlava must move the local Neon
selfhost storage cell and branch compute stack to a coherent PostgreSQL 18
baseline, pinned to the latest stable upstream Neon commits that support that
baseline. The storage cell means pageserver, safekeepers, and storage broker.
Branch compute means the compute container that runs Postgres for a local Neon
branch. These components must be built and pinned together because their
protocols, timeline metadata, Postgres distribution layout, and Neon extension
code are version-coupled.

The end state is boring by design: a normal Onlava app starts, applies DB setup,
Electric runs, agents inspect status, and app SQL can use native PostgreSQL 18
behavior such as `uuidv7()` without changing Onlava's public workflow.

## Progress

- [x] 2026-06-10: Created this ExecPlan as `docs/plans/0073-pg18-default-neon-selfhost.md`.
- [x] 2026-06-10: Linked this plan from `docs/plans/active.md`.
- [x] 2026-06-10: Indexed this plan in `docs/knowledge.json`.
- [ ] Identify and record the latest stable upstream Neon storage, compute, and Postgres refs that can support PG18.
- [ ] Build reproducible local PG18 storage and compute images from those refs.
- [ ] Make Onlava's default selfhost Neon plumbing use PG18 without changing app-facing commands or config.
- [ ] Make the fresh-install/reset path PG18 by default; legacy PG16 state migration is explicitly out of scope.
- [ ] Prove a real Onlava app runs on PG18 with native `uuidv7()`.
- [ ] Update docs, schemas, and generated toolchain metadata affected by the internal default.

## Surprises & Discoveries

- 2026-06-10: `ghcr.io/neondatabase/compute-node-v18` is not publicly pullable, so the first PG18 proof required a local compute image build rather than a simple digest bump. Evidence: local Docker pull/search attempts returned no usable public v18 image.
- 2026-06-10: A local PG18 compute smoke image was built at `local/compute-node-v18:dev`, image ID `sha256:a2b88679bd24ffbd3ec18dd6721a70904b47a11ac9f400390ca500da7af25561`, using a temporary workspace under `/tmp/onlv-neon-compute-v18`. `postgres --version` reported `PostgreSQL 18beta2`, and a standalone container smoke returned `server_version_num = 180000` plus `uuidv7() is not null = t`.
- 2026-06-10: The current Onlava runtime pins storage and compute separately. Evidence: `cmd/onlava/db_neon_generated.go` uses `ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f` for pageserver, safekeepers, and storage broker; `internal/neonselfhost/compute.go` uses `ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249` and sets `PG_VERSION=16`.
- 2026-06-10: The storage image is not version-neutral. Evidence: upstream Neon `Dockerfile` copies Postgres distributions into `/usr/local/v14`, `/usr/local/v15`, `/usr/local/v16`, and `/usr/local/v17`, creates a `postgres_install.tar.gz` containing those directories, and omits `/usr/local/v18`.
- 2026-06-10: The current Neon pageserver source caps WAL ingest rate-limit indexing at PG17. Evidence: `/tmp/onlv-neon-compute-v18/neon/pageserver/src/walingest.rs` defines `MAX_PG_VERSION` as `PgMajorVersion::PG17.major_version_num()`.
- 2026-06-10: The current Onlava compute template preloads `neon,pg_cron,timescaledb,pg_stat_statements`. A compute image built with `EXTENSIONS=none` is enough to prove `uuidv7()` but is not enough to preserve current app behavior. Evidence: `cmd/onlava/db_neon_generated.go` sets `shared_preload_libraries` to `neon,pg_cron,timescaledb,pg_stat_statements`.
- 2026-06-10: `onlava inspect docs --json` reported `review_due_count: 0` and `stale_count: 0`, so this plan does not need unrelated doc gardening.

## Decision Log

- Decision: Keep Onlava's app-facing Neon surface unchanged while changing the default selfhost Postgres major version internally.
  Rationale: The goal is compatibility from the app author's point of view. Any new knobs for image overrides or PG major selection must be internal, diagnostic, or additive, not required for normal app use.
  Date/Author: 2026-06-10 / pbrazdil + Codex

- Decision: Treat storage and compute images as one compatibility set.
  Rationale: Compute speaks to safekeepers and pageserver through Neon-specific protocols, and pageserver uses a Postgres distribution directory for basebackup/timeline operations. Mixing a new PG18 compute image with an old storage image may appear to start but can fail later in WAL ingest, basebackup, reset, restore, or branch diff paths.
  Date/Author: 2026-06-10 / Codex

- Decision: Do not use the `EXTENSIONS=none` PG18 compute smoke image as the final default.
  Rationale: Current Onlava-generated config preloads `pg_cron`, `timescaledb`, and `pg_stat_statements`. Final PG18 behavior must either build those extensions for PG18 or explicitly change the generated config with a documented compatibility decision. Since the stated goal is "apps running like they would now", extension parity is required unless an extension is proven unused and removed through a separate accepted product decision.
  Date/Author: 2026-06-10 / Codex

- Decision: Target a fresh PG18 selfhost cell; do not build PG16 migration support.
  Rationale: The accepted implementation target is a fresh local selfhost cell, not migration of old dev data. Existing PG16 cells, branch leases, and backend metadata may be treated as incompatible and require explicit teardown/reinstall. Do not spend implementation effort on preserving or migrating those old cells.
  Date/Author: 2026-06-10 / pbrazdil + Codex

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Start with these files:

- `cmd/onlava/db_neon_generated.go`: generates the selfhost storage-cell Compose file, pageserver config, compute config, and compute wrapper script. It currently hardcodes the old `ghcr.io/neondatabase/neon` digest and writes PG16-era compute defaults.
- `internal/neonselfhost/compute.go`: starts branch compute containers. It currently hardcodes `computeImageRef` to a PG16 image and passes `PG_VERSION=16`.
- `cmd/onlava/db_neon.go`: reports Neon status, expected images, and components.
- `onlava.toolchain.json`: declares managed image refs for `neon`, `neon-compute-node-v16`, `minio`, and the combined `neon-selfhost` image set.
- `internal/toolchain/manifest_gen.go`: generated embedded copy of `onlava.toolchain.json`; update it with the existing generator or documented repo command when the manifest changes.
- `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, and `docs/app-development-cookbook.md`: describe the Neon selfhost contract and must stay synchronized if defaults, image refs, status metadata, or reset guidance change.
- `docs/schemas/onlava.db.neon.status.v1.schema.json`, `docs/schemas/onlava.db.neon.selfhost.backend.v*.schema.json`, and related branch schemas: update only if JSON changes are not already allowed as additive metadata.
- `cmd/onlava/harness_neon.go`: contains the real Docker-backed Neon selfhost proof used by non-quick `onlava harness self`.
- `internal/neonselfhost/*_test.go` and `cmd/onlava/db_neon_*_test.go`: focused unit tests around generated cell state, image inspection, branch checkout, compute startup, reset, restore, delete, and schema diff.

The temporary PG18 compute proof created during diagnosis is informative but not
the final implementation. Its script lives at `/tmp/onlv-neon-build-script/build-compute-node-v18.sh`,
and its workspace lives at `/tmp/onlv-neon-compute-v18`. Do not depend on those
paths for production behavior. Recreate any useful patches as committed,
reviewable build inputs or documented toolchain build scripts.

Definitions:

- Storage image: the image currently referenced as `ghcr.io/neondatabase/neon`. It supplies `pageserver`, `safekeeper`, `storage_broker`, and embedded Postgres distributions under `/usr/local/v*`.
- Compute image: the image currently referenced as `ghcr.io/neondatabase/compute-node-v16`. It supplies Postgres, Neon Postgres extensions, `compute_ctl`, and the runtime entrypoint used by branch compute.
- Latest stable upstream Neon commit: the newest upstream Neon release, release-candidate, or stable branch/tag that is acceptable for local selfhost storage and compute after validation. The implementation must record exact commit SHAs and image digests in this plan's `Decision Log` before switching Onlava defaults.
- Surface compatibility: no required `.onlava.json` changes and no required new user-facing command. Diagnostic output may mention PG18/image metadata, and errors may tell the user to destroy/reinstall incompatible existing PG16 cells.

## Milestones

Milestone 1 locks the upstream source baseline. Inspect Neon upstream refs and choose exact storage, compute, and Postgres commits. Prefer published stable or release-candidate refs over `main`. If PG18 support is available only through a combination of stable storage plus targeted PG18 Postgres branch fixes, record the rationale and the smallest patch set. The output is a short source-lock note in this plan plus reproducible local build commands.

Milestone 2 produces a matching image set. Build `local/neon:pg18-dev` and `local/compute-node-v18:dev` from the locked source baseline. The storage image must include `/usr/local/v18` and any metadata pageserver needs for PG18 timelines. The compute image must include the extensions Onlava currently preloads, or the plan must be updated with an explicit decision to alter preload defaults. Verify image binaries and versions before wiring them into Onlava.

Milestone 3 moves Onlava's internal default to PG18 without changing the app surface. Replace hardcoded image refs and `PG_VERSION=16` plumbing with PG18-aware constants or manifest-driven values. The generated storage cell and compute templates must still be produced by the same commands and in the same locations, but they should target PG18. Status output may expose image and PG major metadata additively.

Milestone 4 keeps the fresh-start path simple. Existing PG16 selfhost cells,
branch leases, and backend state do not need migration or compatibility support.
If stale state blocks startup, fail with precise reset instructions such as
`onlava db neon uninstall --destroy-data --json`, `onlava db neon install --json`,
and `onlava db neon start --json`. It is acceptable for the implementation and
tests to assume a clean selfhost state after explicit teardown.

Milestone 5 proves runtime parity with a real app. A normal Onlava app should start with the PG18 selfhost Neon provider, create or reuse a branch, run setup/apply/seed as it does today, expose a SQL-ready endpoint, and allow `select current_setting('server_version_num'), uuidv7() is not null;` through `onlava db psql` or an equivalent harness query. Electric behavior must still work for a Neon-backed app that uses it.

Milestone 6 updates docs, schemas, and generated metadata. The documentation should describe PG18 as the default without adding new user steps. Machine-readable manifests and generated files should remain deterministic. The active plan should be updated with outcomes and any remaining debt.

## Plan of Work

Begin by auditing upstream Neon. Use primary Git sources, not memory, because image publication and PG18 readiness are moving targets. Capture:

- the chosen Neon storage source ref and commit SHA,
- the chosen Neon compute source ref and commit SHA,
- the chosen Neon Postgres PG18 ref and commit SHA,
- whether upstream already publishes matching GHCR images,
- whether storage and compute images can be built from one Neon commit, and
- any local PG18 patches needed for storage, pageserver, safekeeper, compute extensions, or the Dockerfiles.

Then make the image build reproducible. Do not leave important build logic only
in `/tmp`. Either add a repo-local script under an appropriate onlava-owned
tooling directory or encode the image refs in the managed toolchain workflow
that already owns images. Keep the build artifacts out of the repo. The script
should build into local Docker tags first and print the exact source refs it
used.

Next, wire Onlava to the new defaults. Avoid scattering image refs and PG major
constants. Prefer a small internal source of truth for the Neon selfhost image
set and default PG major, then use it from generated Compose, status reporting,
toolchain manifest checks, and compute startup. If env overrides already exist
for selfhost drivers or toolchain images, preserve them. Do not add production
environment variables unless this plan is amended with a reason and
`docs/environment.registry.json` is updated.

After wiring, harden the fresh PG18 lifecycle. Add tests for fresh PG18 install,
missing PG18 images, stale local containers after an explicit reset, and status
diagnostics. Do not add a migration layer for existing PG16 backend state; clear
reset guidance is enough.

Finally, run the real runtime proof. The proof should use the same app-facing
commands an agent or human uses today. If the default self-harness is too slow
for every edit, run focused tests while developing and finish with the full
non-quick Neon proof before marking this plan complete.

## Concrete Steps

1. Run the usual orientation commands:

   ```sh
   onlava inspect docs --json
   git status --short
   ```

2. Discover upstream refs and record the chosen locks in `Decision Log`:

   ```sh
   git ls-remote --heads https://github.com/neondatabase/neon.git 'release*' 'releases/*' 'rc/*' 'main'
   git ls-remote --tags --sort='v:refname' https://github.com/neondatabase/neon.git
   git ls-remote --heads https://github.com/neondatabase/postgres.git '*18*' '*neon*'
   ```

3. Build local candidate images from a clean temporary source checkout. Candidate tags:

   ```text
   local/neon:pg18-dev
   local/compute-node-v18:dev
   ```

   The compute image must verify:

   ```sh
   docker run --rm --entrypoint /usr/local/bin/postgres local/compute-node-v18:dev --version
   docker run --rm --user postgres --entrypoint /bin/sh local/compute-node-v18:dev -lc '<standalone initdb/postgres smoke that selects current_setting('\''server_version_num'\''), uuidv7() is not null>'
   ```

   The storage image must verify:

   ```sh
   docker run --rm --entrypoint /usr/local/bin/pageserver local/neon:pg18-dev --version
   docker run --rm --entrypoint /usr/local/bin/safekeeper local/neon:pg18-dev --version
   docker run --rm --entrypoint /bin/sh local/neon:pg18-dev -lc 'test -x /usr/local/v18/bin/initdb && /usr/local/v18/bin/postgres --version'
   ```

4. Update Onlava's internal constants and generated output:

   - replace PG16 compute image refs with the chosen PG18 compute image ref,
   - replace storage image refs with the chosen matching storage image ref,
   - change branch compute `PG_VERSION=16` to `PG_VERSION=18`,
   - ensure generated pageserver config can find `/usr/local/v18`,
   - preserve container names, labels, ports, state paths, branch IDs, and public JSON shapes,
   - update `onlava.toolchain.json` and regenerate `internal/toolchain/manifest_gen.go`, and
   - update tests that assert old image refs or `PG_VERSION=16`.

5. Add or update fresh-start checks:

   - fresh install writes PG18 cell metadata,
   - status reports PG18 image/major metadata additively,
   - missing local/managed PG18 images produce actionable messages, and
   - explicit teardown/reinstall leaves no stale PG16 compute containers reused for PG18 branches.

6. Update docs and schemas if behavior or JSON metadata changed:

   - `docs/local-contract.md`,
   - `docs/agent-guide.md`,
   - `SKILL.md`,
   - `docs/app-development-cookbook.md`,
   - `docs/knowledge.json`,
   - related schemas under `docs/schemas/`, and
   - this plan's `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective`.

7. Validate with focused tests, then with the full repo/runtime proof.

## Validation and Acceptance

Development validation:

```sh
go test ./internal/neonselfhost ./cmd/onlava
go test ./...
```

Toolchain and generated metadata validation:

```sh
onlava inspect docs --json
onlava doctor --json
onlava check --json
```

Runtime acceptance for the PG18 default:

```sh
onlava db neon install --json
onlava db neon start --json
onlava db neon status --json
onlava db branch checkout pg18-default-smoke --json
onlava db psql -- -Atc "select current_setting('server_version_num'), uuidv7() is not null;"
onlava up --json --detach
onlava check --json
onlava down --json
```

The final selfhost proof must include:

- storage cell containers running from the chosen PG18-compatible storage image,
- branch compute running from the chosen PG18-compatible compute image,
- SQL readiness through existing Onlava branch lease metadata,
- `server_version_num` starting with `18`,
- `uuidv7()` available without app schema workarounds,
- app DB setup applying through the existing command path, and
- Electric still starting for an app that uses the Neon-backed database.

Before completion, run:

```sh
onlava harness self --summary --write
```

If full harness runtime is impractical during intermediate work, record the
reason in `Surprises & Discoveries` and run the focused Neon selfhost proof plus
`go test ./...`; the full harness remains required before the plan can be marked
complete.

## Idempotence and Recovery

Image builds should be repeatable from source locks and local tags. Re-running
the build should refresh the same local tags without requiring manual cleanup.
If a build fails, keep artifacts under `/tmp` or Docker's normal cache, record
the failing command and error in `Surprises & Discoveries`, and rerun after
patching the source lock or build script.

Onlava state migration is out of scope. Existing PG16 selfhost cells and branch
leases may be treated as incompatible with the PG18 default. If old state blocks
startup, fail with a message that names the exact inspection command and the
exact destructive reset command. A user must be able to recover by stopping
containers, uninstalling with `--destroy-data`, reinstalling, and starting the
PG18 cell.

Docker containers should remain labeled with `onlava.substrate=neon` and
component labels so cleanup and status continue to find them. After explicit
teardown/reinstall, do not reuse a running PG16 compute container for a PG18
branch.

Generated files must be deterministic. If `onlava.toolchain.json` changes,
regenerate the embedded manifest through the repo's existing generation path and
avoid hand-editing generated byte blobs.

## Artifacts and Notes

Temporary artifacts from the initial investigation:

- `/tmp/onlv-neon-build-script/build-compute-node-v18.sh`: local script that built a smoke PG18 compute image.
- `/tmp/onlv-neon-compute-v18`: temporary Neon checkout and build workspace.
- `local/compute-node-v18:dev`: local smoke image proving PostgreSQL 18beta2 and native `uuidv7()`.

These artifacts are not source of truth. They can guide implementation, but the
final work must live in the repository or in the managed toolchain contract.

Current pinned runtime images before this plan:

```text
ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f
ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249
quay.io/minio/minio:RELEASE.2022-10-20T00-55-09Z
minio/mc@sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727
```

MinIO images are not Neon/Postgres protocol-coupled. They may be updated for
age/security as part of a separate toolchain refresh, but PG18 compatibility
should not depend on changing them unless a validation failure proves otherwise.

## Interfaces and Dependencies

Public Onlava interfaces that should remain stable:

- `.onlava.json` `dev.services.postgres.kind: "neon"` and related branch/project config,
- `onlava db neon install|start|stop|restart|status --json`,
- `onlava db branch checkout|status|list|reset|restore|delete|diff --json`,
- `onlava up`, `onlava db psql`, DB setup, Electric startup, and worktree branch pins,
- existing JSON schemas, with only additive metadata allowed unless this plan is amended.

External dependencies:

- Neon upstream repository: `https://github.com/neondatabase/neon.git`.
- Neon Postgres fork: `https://github.com/neondatabase/postgres.git`.
- Docker Buildx for local image builds.
- GHCR image availability if final defaults use published images instead of locally built tags.
- MinIO and `minio/mc` for object storage substrate; expected to remain unchanged for PG18.

Internal dependencies:

- Toolchain manifest generation.
- Neon selfhost backend state and branch lease registry.
- Docker network `onlava-neon_default`.
- Generated compute templates and pageserver config.
- Real Docker-backed Neon selfhost harness.
