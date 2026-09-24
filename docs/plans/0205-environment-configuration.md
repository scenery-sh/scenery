# Environment-Only Application Configuration

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md). The ONLV migration companion lives in that
repository under `docs/agent/exec-plans/active/`.

Prepared 2026-09-24 against `scenery-sh/scenery` `96df6695` and `pbrazdil/onlv`
`d7e2e0ab`. The human authorized implementation on 2026-09-24 ("implement until
done"). Production mutation, operator cutover, publication and merges remain
subject to separate explicit authorization.

## Purpose / Big Picture

Replace application dotenv files and ambient application environment-variable configuration with one Scenery-owned, typed configuration system. The entire user-facing selection model is **application + environment**. Scenery discovers the application from the checkout; the user selects an environment. There are no machine, repository, worktree, or deployment scopes, no configuration profiles layered on profiles, and no globally mutable active environment.

The observable outcome is:

```sh
scenery config set designs.weather_pack_root /Volumes/Drive01/PSM --env local
scenery config set auth.google_client_secret --env local
scenery config show --env local -o json
scenery up
```

The secret-setting command opens a hidden prompt. Every worktree of this application on the same machine uses the same configured local values. Each worktree retains its own database ownership, browser origin, sockets, process identity, and mutable runtime state. Nothing is copied from a main checkout.

Production uses the same commands:

```sh
scenery config set designs.simulation_concurrency 8 --env production
scenery config set auth.google_client_secret --env production --stdin
scenery deploy --env production
```

A production configuration write changes desired configuration on the configured production target. It does not restart services or deploy code. Deployment pins and applies a complete configuration revision. Local values are never uploaded as production values.

These examples describe the target interface, not commands already implemented at the reviewed baselines. In ONLV, invoke Scenery through `./scripts/scenery`.

The final resolution rule is exactly:

```text
Declared defaults → the selected environment's configured values
```

Runtime-managed capabilities are supplied separately. They are not another configuration override layer.

## Progress

- [x] 2026-09-24: Reviewed the two repository baselines and relevant configuration, compiler, deployment, and validation owners.
- [x] 2026-09-24: Recorded the environment-only product model and the execution order below.
- [x] 2026-09-24: M0 — Value-free inventory recorded under Artifacts and Notes; CLI grammar frozen in `cmd/scenery/help.go` and `docs/schemas/scenery.config.{show,change}.schema.json`.
- [x] 2026-09-24: M1 — `host_path` scalar, deferred configurable deployment inputs (`internal/compiler/module_inputs.go`, `go_config.go`), `internal/appconfig` catalog/resolver/store with locks, atomic writes, content-addressed history, pins and pruning.
- [x] 2026-09-24: M2 — `scenery config show|set|unset`, macOS Keychain and systemd-creds backends, private SSH receiver (`scenery config receive`) with operation-id replay protection. Real-target SSH and systemd-creds proof is still open (see Outcomes).
- [x] 2026-09-24: M3 (core) — generated constructors read `sceneryruntime.ResolveDeploymentConfig`; per-service snapshots over an inherited pipe (`SCENERY_CONFIG_SNAPSHOT_FD`); consumer-only restarts; rejected candidates keep the healthy generation; auth, assistant provider key and workers read configuration; all dotenv loaders removed. Completed later the same day: external SQL supply is the typed `sql.database_url` secret (D14); application processes receive a minimal inherited environment (D15); seed and task launchers keep their framework-injected wiring (D16).
- [x] 2026-09-24: M4 — Staged releases under `~/.scenery/deployments/<app>/<env>/`, captured and pinned configuration revision, target-side validation before stopping, activation into a stable root, commit only after the runtime applied the installed revision, rollback of source and configuration, legacy-root refusal with `docs/runbooks/deploy-root-migration.md`. Rehearsed end to end locally (Artifacts); real Linux/systemd target proof remains open.
- [x] 2026-09-24: M5 — ONLV branch `feat/environment-configuration` (commit `c61c0511` on `origin/main` `d7e2e0ab`): every inventory row migrated, NextNext public map configuration, Vite/Bun/Just dotenv loading disabled, importers narrowed, companion plan `docs/agent/exec-plans/active/environment-configuration.md`. Published as scenery-sh/scenery#218 (`93bd4b89d0a5`); ONLV pins it (`8b0d0b19`) and the D11 two-worktree proof passed on the pinned framework.
- [ ] 2026-09-24: M6 — Code, deletion and guards are complete (dotenv loaders, `internal/envfile`, fixture `.env` files, drift guard, probes, `scripts/config-import`). Open: the operator cutovers and the real Linux/SSH/reboot proofs, which need explicit authorization or unavailable platforms.

Update this section at each meaningful stopping point. Replace planning timestamps with actual completion timestamps when work is executed.

## Surprises & Discoveries

- 2026-09-24: concurrent `os.Root.OpenFile(name, O_CREATE)` of one name fails with ENOENT on darwin (Go 1.27); reproduced in a standalone test. The environment lock file is therefore opened by path with `O_NOFOLLOW` (`internal/appconfig/lock_unix.go`).
- 2026-09-24: `F_FULLFSYNC` makes each store write ~10 ms on macOS; unit tests replace the store's flush seam, and real durability belongs to the `configuration` probe.
- 2026-09-24: the three committed fixture apps under `testdata/apps/` carry placeholder `.env` files; they are dead inputs removed in M6.
- 2026-09-24: the `auth` release probe still configured standard auth through the removed `JWT_SECRET`/`GOOGLE_OAUTH_*`/`AUTH_TOKEN_CIPHER_KEY` variables and failed every case after the merge with main. `scripts/verify/testdata/authprobe` now delivers a configuration snapshot on an inherited pipe, as the supervisor does; `--probe auth` passes.

1. Scenery already derives typed service configuration from package inputs in `internal/compiler/go_config.go`. Sensitive Go configuration is required to use `resource_ref("secret")`; inventing a parallel `secret_string` model would duplicate an existing contract. Reuse and complete that contract. [R3]

2. SSH deployment currently synchronizes source into `$HOME/.scenery/apps/<app-id>` with `rsync --delete`. Placing an unprotected environment store in that source tree creates a deletion hazard. The same destination also lacks environment qualification. Separate mutable source trees from durable configuration and qualify deployment state by environment. [R4]

3. The SSH deploy path performs a local check without forwarding the selected deployment environment, then stops the remote runtime before synchronization. The new path must distinguish source validation from target configuration validation, and validate a pinned candidate before stopping a healthy service. [R4]

4. ONLV has `local`, `devtools`, `production`, and `all` environments. Preserve their declared routing/frontend behavior; do not accidentally delete existing environments to simplify configuration. Each non-deployable environment gets its own local values, without implicit inheritance from `local`. [R5]

5. ONLV currently uses the name `clean-tech` without an explicit ID. Preserve its existing effective application identity by adding `id: "clean-tech"`, not by generating a new identity. Runtime/data ownership must not be reset as a side effect of this refactor. [R5]

6. ONLV's weather client already performs a warned fallback to S3 when pack initialization fails. Keep that domain policy in ONLV; Scenery must not erase an unavailable path. [R6]

7. Active Scenery plans 0200, 0202, and 0203 affect process-per-service development, the runtime RPC, and framework-producer handoff. Integrate with those owners instead of adding a second supervisor or dashboard. [R7]

8. Removing the Go dotenv loader is insufficient. Bun and Vite have their own dotenv-loading behavior, and ONLV's harness documentation records a Justfile dotenv setting. Cover those launch paths explicitly. [R9, E1, E2]

## Decision Log

All decisions below were recorded on 2026-09-24 by the plan author from the agreed product direction. Record implementation refinements here, without silently expanding the CLI model.

**D1 — One authority per application/environment.** Non-deployable environments resolve on the invoking machine. Deployable environments resolve on their configured target. There is no fallback from a failed remote read to a local copy.

**D2 — No user-visible scopes or worktree overrides.** Worktrees share configured environment values. Tests that need different values pass typed configuration directly, or use a test-owned application identity. They do not mutate a real user's shared local environment.

**D3 — Reuse the authored contract.** `.scn` owns types, constraints, safe defaults, sensitivity, and dependency identities. `.scenery.json` retains application identity, environment names, deployment targets, routing, and other structural settings. Do not add an environment-value override map there.

**D4 — Keep build and runtime inputs separate.** This configuration surface accepts deployment-phase values. It cannot change service shape, generated types, package imports, wire contracts, build tags, or toolchain selection. Configuration-only changes must not change compiled executable bytes.

**D5 — No second public secret API.** `config set` knows whether an input is secret from the compiled catalog. Secret values go into protected, versioned storage, never ordinary configuration JSON, command arguments, or generated sources. The application receives the existing typed secret abstraction.

**D6 — Immutable runtime snapshots.** A process generation receives a validated snapshot once. Production and rollback use explicitly retained revisions rather than rereading mutable desired configuration.

**D7 — Preserve state, remove compatibility.** Do not retain an application env fallback or dual resolver in the released result. A one-time offline conversion tool is permitted for migration; it is not a runtime configuration source.

**D8 — Small implementation, not a new platform.** Use files, existing locking/atomic-write facilities, existing SSH transport, current process supervision, and current machine envelopes. Do not add SQLite, Postgres tables, Vault, a config daemon, a new network service, or a plugin marketplace for configuration.

**D9 — Platform secret adapters are implementation details.** Use macOS Keychain for the workstation path and encrypted systemd credentials for the existing Linux/systemd deployment path. Verify non-interactive use under the actual runtime owner. Do not silently fall back to plaintext or assume an arbitrary Linux session has a working credential backend. Unsupported secret-storage capability produces a specific readiness failure; schema-only and secret-free workflows remain available.

**D10 — Explicit mutation context.** `config set`, `config unset`, and deployment require an explicit environment. Reads default to `local`. Existing explicitly selected non-deployable environments remain supported. No `env use`, default write target, `--scope`, or per-write `--target` is introduced.

**D12 — Implementation refinements (2026-09-24, Claude).** Keys are `<module instance path with "." separators>.<input>`; framework keys are `auth.*` (from `.scenery.json` auth) and `assistant.openai_api_key` (when assistants exist). The store document carries its own kind/schema identity without the compiler spec revision, because worktrees of one application run different producers and must share it. Revisions are content-addressed (`cfg-` + 128-bit digest), so equal content has equal revision and no-op writes create nothing. A local supervisor polls its single environment document every 500 ms instead of adding a filesystem-notification dependency. Snapshots travel over a pipe allocated by `internal/devprocess` (`SCENERY_CONFIG_SNAPSHOT_FD` names the descriptor); runtime identity covers keys, values and opaque secret versions, never secret bytes. A worktree's applied/rejected observation is its pin on the environment history, which `config show` reads.

**D14 — External SQL supply is configuration (2026-09-24, Claude).** Every application with SQL requirements declares the framework secret `sql.database_url`. When the selected environment configures it, that server is the external SQL supply with the former explicit-`DATABASE_URL` semantics; otherwise supply stays managed. The CLI removes `DATABASE_URL` and `SCENERY_DATABASE_JSON` from its own environment at startup, so an inherited value never selects a database; the SQL-supply commands (`up`, `worker`, `db`, `snapshot`, `inspect`, `doctor`, `down`, `prune`) resolve the environment they select (`--env`, else the deployable environment whose stable root they run in, else the default) from the revision its runtime runs and export the value for the existing supply code. Because a non-deployable environment's configuration is shared by all worktrees, `scenery up` of such an environment refuses a configured external database instead of sharing one mutable database between worktrees. Standalone generated runtimes launched without Scenery still read `DATABASE_URL` as their explicit endpoint (spec 18.4 provider adapter). `scripts/config-import` maps a former `DATABASE_URL` to `sql.database_url`.

**D15 — Minimal inherited environment for application processes (2026-09-24, Claude).** Service processes of `scenery up` and the application process of `scenery worker` inherit only OS/toolchain protocols (paths, user identity, temporary directories, time zone and locale, terminal settings, TLS roots, HTTP proxies, XDG directories, dynamic-loader paths, Go runtime knobs), `SCENERY_*` wiring and the configured `DATABASE_URL` (`cmd/scenery/app_child_env.go`). Frontend dev servers, build toolchains, tests and tasks keep the inherited environment: they are developer tools, and Vite/Bun no longer load dotenv files themselves.

**D16 — Seed and task launchers (2026-09-24, Claude).** No separate capability bundle is introduced. `scenery db seed` already supplies each declared seed command its service-scoped `DATABASE_URL`; importers accept that or an explicit `--database-url`, and secrets such as API tokens are read from standard input, never argv or ambient variables.

**D13 — Deployment layout refinement (2026-09-24, Claude).** The runtime root of a deployable environment is one stable directory, `~/.scenery/deployments/<app-id>/<env>/source`, because worktree data ownership is keyed by the root path; per-release directories hold only staged source and receipts. `active.json` names the release installed in that root (state `activating`, then `active`), so a restart or reboot at any point runs a consistent source/configuration pair; `commit` confirms it only after the root's runtime pinned the installed revision. A target that still has the legacy checkout at `~/.scenery/apps/<app-id>` refuses deploys and configuration writes until the operator runs the migration runbook; nothing moves or allocates data implicitly.

**D11 — Shared configuration, identical starting fixtures, independent working data.** Removing dotenv changes only how configured values reach a worktree; it does not change how demo data reaches it. Configured environment values are shared across worktrees (D2). Demo projects, scenes and catalog records come from the application's versioned fixture bundle at the checked-out commit, restored into each new worktree's own isolated database and object storage. Edits, captures, simulation results and uploads made afterward belong to that worktree and are never synchronized elsewhere. The application owns which records and assets make up its demo (ONLV: `development/presets/small/` and `development/prepare.ts`); Scenery owns only the generic database/storage restore and isolation mechanisms, and does not learn solar projects or scene registration. No fixture scopes, fixture configuration layers, or copying from the main checkout's live data are introduced. A new demo scene reaches other worktrees only through a deliberately reviewed fixture revision that contains its records and every referenced asset; worktrees prepared afterward from that commit receive it, and existing worktrees keep their data. Content-addressed asset caching with copy-on-write materialization may later reduce disk use behind the same command. It is not a prerequisite for this plan, and writable scene directories are never shared between worktrees.

## Outcomes & Retrospective

Implementation is complete in both repositories; the plan stays active for the steps that need authorization or unavailable platforms.

Implemented: `scenery config show|set|unset|receive`, the typed catalog/resolver/store (`internal/appconfig`), Keychain and systemd-creds backends, per-process snapshots with consumer-only restarts, revision-pinned SSH deploys with rollback, public configuration for browsers, `host_path`, `SecretRef.Reveal/Lookup`, `sql.database_url` (D14), the minimal application-process environment (D15), the one-time `scripts/config-import`, and the dotenv drift guard plus `configuration`, `configuration-secrets` and `configuration-deploy` probes. Removed: every dotenv loader and precedence path, `internal/envfile`, `ResolvedEnv.DotEnvFiles`, fixture `.env` files, ambient auth/database/weather/provider/browser variables. ONLV is migrated on its branch.

Pending, each requiring the user's explicit authorization:

1. Done 2026-09-24: merged as scenery-sh/scenery#218 (`93bd4b89d0a5`); ONLV branch `feat/environment-configuration` pins it (`8b0d0b19`, clients regenerated, checks green) and records the D11 proof in its companion plan; merged as pbrazdil/onlv#136 (`42a22ced`).
2. Done for the workstation's `local` environment on 2026-09-24: `scripts/config-import` moved `auth.google_client_id`, `auth.google_client_secret`, `auth.jwt_secret` and `maps.google_maps_api_key` from the ONLV checkout's `.env` and `apps/nextnext/.env.local` (secrets into the login Keychain, none found in store files); the old files are untouched. Remaining: operator cutover of the other `.env` files with `scripts/config-import` (per developer machine and per deployable environment), the one-time production root migration (`docs/runbooks/deploy-root-migration.md`), and any archival or deletion of the old files.

Unverified on this machine: systemd-creds on Linux, a real SSH target, and reboot/resume on a real target; the local deploy rehearsal used a test-double `ssh`. Restored release executables are rebuilt from retained source (equivalent, not byte-identical).

## Context and Orientation

In Scenery, start with:

- `internal/app/root.go`: `Config`, `EnvConfig`, `ResolvedEnv`, `AppID`, environment resolution, and `DotEnvFiles`.
- `internal/compiler/go_config.go`, `internal/compiler/deployment.go`, `internal/spec/`, `internal/scn/`: input metadata, deployment projections, sensitivity, scalar typing, and validation.
- `internal/generate/`, `runtime/`, `internal/build/`: generated constructors, runtime bootstrap, executable reuse, and identity partitioning.
- `cmd/scenery/dev_supervisor.go`, `dev_runtime_environment.go`, `dev_app_start.go`, `dev_app_handoff.go`: dotenv loading, launch preparation, and retained last-known-good generations.
- `cmd/scenery/appenv.go`, `check.go`, `dev_postgres_start.go`, `db_setup.go`, and `worktree_database_*`: command and database configuration paths.
- `auth/standard.go`: direct canonical env reads for standard authentication.
- `cmd/scenery/deploy_ssh.go`, `deploy_systemd.go`, `deploy_publish.go`, `internal/deployplan/`, and the deployment registry/resume owners.
- `cmd/scenery/build_desktop.go`, frontend launch/build adapters, task/worker launchers, storage and assistant handoff code.
- `internal/machine/`, `internal/redact/`, `docs/local-contract.md`, `docs/schemas/`, `docs/environment.registry.json`, and `scripts/verify/harness_probes.go`.

In ONLV, start with:

- `.scenery.json`, `app.scn`, `go.mod`, and `scripts/scenery`.
- `solar/designs/package.scn`, the `NewService` implementation, `designer_common.go`, and `pkg/nsrdb/`.
- `Justfile`, `development/`, `scripts/`, import commands, provider constructors, and frontend configuration files.
- `docs/agent/SCENERY.md`, `HARNESS.md`, `QUALITY.md`, and existing repository harness rules.

These are verified entry points, not an exhaustive file list. Before editing, enumerate actual call sites at the implementing revision. Read `AGENTS.md`, the applicable child instructions, `PLANS.md`, active plans, and architecture/debt guidance. Do not modify the human-owned `VNEXT.md`. Do not spawn subagents. Do not globally install Scenery for validation.

Place the master plan under the next unused historical number in Scenery's `docs/plans/`; update `active.md` and `docs/knowledge.json`. Put an ONLV migration companion under `docs/agent/exec-plans/active/` using that repository's template. The companion must contain its own migration and acceptance instructions and point to the exact framework change. Do not maintain two conflicting copies of the configuration specification. [R1, R2]

## Milestones

M0 produces a classified inventory and failing behavioral tests. M1 produces a typed resolver/store with no runtime changes. M2 makes local and target configuration commands work against test-owned state. M3 makes generated runtimes consume snapshots and reload the appropriate services. M4 makes target deployment and recovery revision-safe. M5 moves ONLV and its tooling onto the new path. M6 deletes legacy runtime support and produces the complete acceptance record.

Keep implementation commits reviewable and both repositories testable. Intermediate development commits may retain old code while the new path is wired, but there must be only one application configuration resolver in the delivered state. Do not release a compatibility toggle.

## Plan of Work

### M0 — Inventory and freeze the behavioral contract

Search both repositories for dotenv loaders and application environment access, including `os.Getenv`, `os.LookupEnv`, `os.Environ`, `envpolicy`, `process.env`, `import.meta.env`, `Bun.env`, SDK default credential discovery, Just dotenv settings, shell `source`, `--env-file`, and environment maps attached to build/seed/validation commands.

Produce a value-free inventory with one row per semantic input: current name, actual consumers, type, requiredness, default, sensitivity, public exposure, target environment restrictions if genuinely needed, and replacement owner. Never paste actual env contents, DSNs, credentials, or full parent process environments into the plan or evidence.

Classify each entry as one of: typed application input; framework-managed runtime capability; structural/build setting; OS/toolchain requirement; or dead input. This classification is internal implementation work, not a new user-facing scope taxonomy. Every discovered application input must have a migration destination or an explicit removal rationale.

Freeze the CLI grammar and examples before implementing storage:

```text
scenery config show [KEY] [--env NAME] [-o json]
scenery config set KEY [VALUE] --env NAME [--stdin | --null] [-o json]
scenery config unset KEY --env NAME [-o json]
```

Retain existing global `--app-root` behavior. Allow optional `--expect-revision REV` on mutations for agents performing read-modify-write operations; this is an optimistic concurrency precondition, not another selection dimension. Reject obsolete scope/profile flags. Do not add export-all-secrets, reveal, sync, push, pull, clone-env, or worktree-override commands.

Typed scalar input parsing must be deterministic. Strings are literal strings, numeric and boolean inputs use their declared types, and `--null` is accepted only for optional inputs. `unset` removes the configured value and restores the declared default; it is not synonymous with `null`. Reject conflicting input modes. For secrets, prohibit a positional value; accept a no-echo prompt or stdin. Stdin preserves exact secret bytes: do not silently trim whitespace or a trailing newline; validate any format-specific constraint explicitly. Terminal prompts exclude the terminal submission newline. Prompt before acquiring store locks. In non-interactive mode without stdin input, fail with an actionable diagnostic rather than hanging.

Define keys from stable module-instance input identities, not Go type names or checkout paths. Preserve the friendly `designs.weather_pack_root` spelling for the ONLV designs instance. Different instances of a reusable package must have distinct keys. Track aliases and all consuming services internally, so one input does not become several independent settings. Framework-owned settings such as `auth.google_client_secret` join the same catalog through checked framework declarations.

**Exit proof:** parser tests reject scope flags, missing write environments, ambiguous values, unknown input names, and secret arguments. The inventory accounts for the weather path, auth, external database supply, storage/provider credentials, frontends, importers, fixtures, and worker/task launch paths.

### M1 — Typed catalog and environment store

#### Contract and pure resolution

Extend the compiler's existing input/schema derivation. Publish a catalog containing canonical key, declaration location, phase, type, constraints, default or absence, sensitivity, and consumer identities. The catalog must be obtainable without starting services, allocating databases, contacting production, or resolving secret values.

Expose only deployment-phase configurable values through this CLI. Resource capability references remain typed wiring; do not turn a database dependency or arbitrary provider resource into a user-supplied string. Source-authored package/application default binding choices may establish the one declared baseline, but do not create per-environment override maps in source.

Permit unresolved required deployment values during schema extraction and ordinary compile/generation. Enforce their presence when validating a runtime candidate. Do not let the current `Go service config has no resolved value` validation force secrets or machine values into compilation. Keep contract/type errors fatal at compile time.

Add `host_path` end to end as a deployment-only path scalar, with `optional(host_path)` for the weather root. Validate absolute path syntax against the execution target's path rules, not the laptop's filesystem when configuring a remote target. Do not expand shell variables, evaluate symlinks during compilation, stat directory trees, or change values because a disk is temporarily absent. Reject this scalar in wire contracts and build/structural positions. Update parser, specification, Go mappings, schema generation, diagnostics, and conformance fixtures together.

Implement one pure operation conceptually equivalent to:

```text
Resolve(catalog, selectedEnvironmentRevision) -> validated configured inputs
```

Missing optional values remain absent; missing required values yield named diagnostics. Missing local store means an empty configured-value set, not an error by itself. Unavailable or malformed storage is not equivalent to missing storage. There is no ambient env fallback and no fallback between environment names.

A key already stored by a newer branch but absent from this branch's catalog is retained and reported as `unused`; it is not injected. A known key with an incompatible type fails candidate validation. CLI writes still reject unknown keys. A stale branch must not remove or rewrite other branches' keys. Unknown entries are not dumped verbatim into public inspection.

#### Authoritative layout

Use the existing Scenery home, resolved by the established bootstrap mechanism. The normal paths are:

```text
~/.scenery/apps/<app-id>/
  environments/<env>.json                    # authoritative desired revision
  environment-history/<env>/<revision>.json  # immutable retained revisions
  environment-locks/<env>.lock               # per-environment serialization
  secrets/<env>/...                         # protected versions/references; backend-owned

~/.scenery/worktrees/<existing-root-id>/
  ... existing runtime/data ownership ...
  # no configuration overrides

~/.scenery/deployments/<app-id>/<env>/
  releases/<deployment-id>/source/            # only this source tree is synchronized
  releases/<deployment-id>/receipt.json
  active.json                                # successfully activated deployment
```

The local environment file lives on the workstation. A deployable environment file lives in the target service owner's Scenery home, never a workstation cache used as authority. Resolve the actual service owner/home through existing target registration; do not accidentally provision one store as the SSH login user and read another as root at reboot.

The document uses a strict, current, versioned schema with app ID, environment, opaque revision, typed non-secret values, and secret-version references. Extend current producer/schema identity conventions rather than inventing a second envelope. Do not put secret values or raw secret-derived hashes into that document.

Require an explicit stable application ID for the new configuration feature. In ONLV set `id` to its current effective `clean-tech` identity. Validate IDs/environment names as path-safe identifiers. An ID is a namespace, not authentication. Preserve existing authorized ownership checks.

Use restrictive parent/file permissions, no-follow/path-containment checks, size limits, duplicate-key rejection, and deterministic encoding. Reuse repository atomic-write and locking primitives where suitable. On a write: read under the environment lock, apply only the requested key change, validate, create the immutable revision, then atomically replace the desired pointer/document. Use durable flush/rename ordering. Never replace the whole map with the caller's stale copy.

Assign a new revision only for a real non-secret value change; setting the same non-secret value or unsetting an absent key is a no-op. Merge concurrent different-key writes. An expected-revision mismatch changes nothing. Serialize same-key writes and report previous/new revision IDs. Use existing operation IDs for remote retries so an acknowledged-late mutation cannot replay over a newer value.

Bound history: keep desired, active, rollback, and live-runtime-pinned revisions, plus the newest 16 unpinned revisions. Collect secret versions only when no retained revision references them. Never delete a live pin based solely on elapsed time. If retained state reaches a configured internal size bound, report it; do not discard referenced state. Do not build a generic event database or unbounded audit log.

**Exit proof:** pure resolution and store tests cover defaults, optional absence, corruption, old-branch unused keys, wrong types, traversal/symlinks, permissions, concurrent edits, interrupted writes, revision preconditions, no-op writes, and bounded retention.

### M2 — CLI, secrets, and remote operations

Implement the three public verbs with the existing CLI parser, help catalog, JSON envelope, diagnostic codes, and bounded stdout/stderr conventions. Read-only `show` must not allocate services, provision secret stores, or modify state.

Human output and JSON must distinguish declared default, desired configured value, and currently applied runtime revision. The minimum information is application ID, selected environment, canonical input key, type, source (`default` or `environment`), configured/missing/unused/invalid state, redacted secret state, desired revision, and available applied-generation observations. For local environments, show the current worktree's application status and a bounded summary of other known consumers. An unavailable observation is not a successful application. Identify the caller catalog/source revision separately from the active target schema: a new checkout's defaults are not evidence that an older deployed build has adopted them.

Do not validate every required input before a single-key write: operators must be able to populate a new environment incrementally. Validate the key/value and applicable invariants, then report remaining missing requirements. Start/check/deploy validate the complete candidate. `show` must still display a useful diagnostic report for an incomplete environment.

Implement versioned secret creation and lookup behind one narrow interface. Reuse the existing typed secret reference at the application boundary. On macOS, use Keychain APIs through a suitable small adapter, not a shell command with the secret in `-w` or another argv position. On the Linux/systemd target, use the platform credential tooling with explicit tested ownership/key availability. Send plaintext only through private in-memory buffers/pipes to that tooling. Do not log subprocess input/output. Do not implement cryptography from scratch or add a secret-server dependency.

Prove locked/unavailable credentials, denial, headless operation, and boot-time access. A missing credential must not trigger automatic production key generation. Locally generated dev-only keys must retain their established owner and lifetime; environment sharing must not accidentally make independent worktree runtime tokens interchangeable.

Secret mutation is ordered: create a new immutable secret version, durably publish a config revision referencing it, then permit later garbage collection of unreferenced versions. A crash between creation and publication leaves an orphan, not a dangling live reference. Replacing/unsetting desired secrets must not invalidate a running or rollback revision. Secret rotation activation is distinct from revocation of an old credential; do not claim rolling back an application can restore an externally revoked credential.

For deployable environments, resolve the existing SSH target from `.scenery.json`. V1 requires exactly one target, matching the current environment-selected SSH deploy restriction. Reject zero/multiple targets rather than choosing the first. Do not add a per-command target choice or a new authority field.

Use existing SSH authentication and host verification. Implement a narrow private, versioned receiver for config reads/mutations; it is an implementation boundary, not a new public configuration model. Send bounded structured requests over stdin. Do not interpolate values, keys, secrets, or untrusted paths into shell scripts. Validate app/environment identities and protocol versions on both sides. Explicitly distinguish source-catalog validation on the caller from target storage/ownership validation. Never execute code sent as part of a config request.

Resolve secret bytes on the target. Remote `show` returns redacted metadata, not all target secrets to the laptop. If the target cannot be reached, fail with an unavailable diagnostic. A configuration write is allowed before the first application deployment once the existing target Scenery runtime and secret backend are ready; it must not need a running application to hold its own configuration.

**Exit proof:** golden help/JSON tests; no-prompt agent tests; no secret in argv/stdout/stderr/diagnostics; valid explicit remote selection; disconnected-target failure without local writes; concurrent remote edits; interrupted/retried secret mutation; no effects on running production processes.

### M3 — Runtime snapshots and framework-owned consumers

Extend generated bootstrap/constructor wiring to accept a validated runtime snapshot rather than bake deployment values into generated Go literals. Keep application `Config` types generated from `.scn`. Service implementations consume their typed constructor inputs and injected dependencies, not a string-keyed configuration accessor.

The snapshot binds application, environment, configuration schema, selected environment revision, current runtime owner, and service consumer identity. It includes only that process's required resolved inputs and capabilities. Deliver it by an inherited private descriptor/pipe for supervised processes; use an explicitly selected owner-protected descriptor file for supported standalone/bootstrap cases. Allocate descriptors through the existing process launcher; do not assume a hard-coded FD is free. Validate, decode once, and close before launching descendants. Never place secret material in a shared build directory, generated Go/TypeScript, a public manifest, or a general debug endpoint.

The framework launcher may read its private bootstrap locator. That locator must not become an alternative user-configured env source. Generated application code must not discover application values from process environment.

Separate identities into: source/contract schema identity; executable/build identity; and per-service runtime configuration identity. Runtime identity includes relevant non-secret values, opaque secret versions, and owned capability identities. Changing one service's configured input or secret version changes that service's runtime identity, not all executable identities. Preserve real source/toolchain/build invalidation rules; do not bypass them to make an acceptance test appear fast.

For local environments, integrate a watcher with each existing worktree supervisor. Watch the environment metadata directory so atomic replacement is observed; coalesce bursts and recheck revision at startup. Do not watch weather-pack contents or poll/rehash the whole repository. Resolve against that worktree's current catalog, compare consumer-specific snapshots, and restart only changed consumers through the existing last-known-good handoff. An invalid candidate leaves the previous generation alive and is reported as unapplied. A newer desired revision arriving during preparation remains pending; do not incorrectly acknowledge it as applied.

Preserve code-generation/lifecycle cancellation, durable job activation, and draining guarantees. A config-only change must not activate two generations of the same durable consumer. Framework-producer handoff must carry revision and secret-version pins and use a compatible decoder. Do not relabel retained producer/spec identity to force compatibility.

Production watchers never auto-adopt the desired document. Reboot and deployment resume reload the active receipt and its pinned configuration. A supervisor must not become a production deployment simply because it sees a newer desired revision.

Migrate Scenery's own application-facing consumers: standard auth, database selection, storage, providers, assistant keys, workers, task runners, validation commands, and seeding. Eliminate direct JWT/OAuth reads in `auth/standard.go`. Preserve key values and encryption ability during migration; do not regenerate production signing or token-cipher keys. Preserve typed SQL/object injection. Database allocation remains worktree-owned and must not be reconstructed from a shared environment file.

Inventory legitimate external database supply explicitly. Remove `DATABASE_URL`/per-service env fallback, but retain approved external-provider functionality through typed capability configuration. A locally configured server is not permission to share one mutable app database between worktrees. If an external supply cannot satisfy the required local isolation, reject that combination rather than silently sharing it.

Import commands should receive a command-specific typed capability bundle from Scenery's existing seed/task launcher. Do not replace env with secrets on `--database-url` arguments. A small framework-owned bootstrap helper may decode the private command descriptor; domain code still receives explicit inputs. Tests construct those inputs without a personal config store.

Keep necessary OS/toolchain and third-party subprocess env inside narrow, reviewed framework adapters. Use a minimum explicit child environment rather than blindly inheriting the parent. Application credentials, `VITE_*` settings, SDK ambient credential chains, and `NODE_OPTIONS`/`BUN_OPTIONS` loaders must not reintroduce application configuration. Do not promise to eliminate `PATH`, `HOME`, locale, or tool-specific protocols from the operating system.

**Exit proof:** same executable runs with two selected config revisions; only affected consumers restart; unrelated PIDs and all build artifacts stay unchanged; old generation survives invalid input; tests prove environment identity and capability isolation; no extra durable activation; restart/resume preserves exact pins.

### M4 — Deployment and activation

Move future SSH source synchronization into the environment-qualified release source directory from M1. Never place config history, secret storage, deployment receipts, or authoritative ownership inside a `--delete` synchronization root. Keep an explicit `.env*` exclusion as defense against accidentally uploading obsolete user files; it does not imply dotenv support.

The old remote checkout may have live data ownership. Before moving it, inventory registry entries, canonical-root ownership, databases, storage roots, public origin, old producer, and active runtime. Use the existing explicit ownership-migration mechanisms and independently verified backups. Do not simply run the new checkout and let it allocate an empty database. Do not move or delete existing target data without authorization.

The required deploy sequence is:

1. Validate source and the explicitly selected environment definition locally without reading local secrets as production inputs.
2. Contact the target; verify compatible receiver/runtime producer, environment authority, credential-backend readiness, and existing ownership.
3. Capture one desired environment revision and pin its secret versions for this deployment attempt.
4. Stage source/build artifacts separately from the active release; validate the complete candidate and required capabilities against the target. Do not stop healthy serving for a validation failure.
5. Generate one activation receipt binding executable/build identity, schema/default identity, selected environment revision, resolved non-secret snapshot, secret versions, and retained data/route ownership.
6. Use the current deployment coordinator and process lifecycle to activate. A single-process target may require a stop/start; do not add a second load balancer or promise zero downtime. Prevent duplicate durable consumers. On readiness failure, restore the retained prior executable and exact prior configuration.
7. Mark `active.json` only after successful activation and required publish/readiness proof. A desired revision written concurrently remains pending for the next deploy.

A plain process restart, boot resume, frontend publish, or inspection command must not implicitly promote desired configuration. Public frontend configuration must be pinned with the same successful deployment, not taken from a fresh independent read after backend activation.

A config-only deploy must reuse the current executable and unchanged frontend assets. Reuse source/build manifests rather than rerunning compilation merely because the CLI entrypoint is `deploy`. Record explicit build-action counts in the receipt/probe so this is testable.

**Exit proof:** disconnected target, missing secret, wrong type, and protocol mismatch leave the old runtime unchanged. An injected activation failure restores the old revision. A newer desired revision does not affect a captured deploy or reboot. `rsync --delete` cannot touch configuration/secret/history state. Two environments on one target do not share a checkout or runtime allocation.

### M5 — ONLV migration and frontend/tooling cutover

Add `id: "clean-tech"` to `.scenery.json`, preserving the existing effective identity and browser-origin ownership. Keep current environment and frontend definitions. Remove repository env-value mechanisms, not structural routing declarations.

In `solar/designs/package.scn`, add optional deployment input `weather_pack_root` with `host_path` type. Wire it through the service's `config` and generated constructor input. Change `newDesignerRuntime` to accept the path explicitly and pass it to `newWeatherClient`. Delete the production `NSRDB_PACK_ROOT` env read and remove any unused import. Preserve `simulation_concurrency` and `ssc_library_dir` behavior through the single declared-default/environment model.

Keep fallback policy in `pkg/nsrdb`/designs. Prove no-path S3 operation, valid packs, invalid pack data, an unavailable configured directory, and loss of access after initialization. Do not treat missing directory as a Scenery rewrite to null. Do not hash pack files during configuration changes or add recursive mount watchers. A deliberately supplied production path is evaluated on the target, not inferred from the workstation.

Migrate every remaining inventory row, including maps/weather/AI/provider clients, mail/auth settings, importers, service constructors, native subprocess configuration, test fixtures, and task scripts. Supply explicit SDK credentials/options where supported; do not leave an SDK's ambient default chain as an undocumented application input.

Remove Just's dotenv setting and all shell sourcing/copying of `.env` from worktree/bootstrap scripts. Worktree setup only prepares the existing runtime fixture and sees shared environment configuration. Do not create config clones or symlinks between checkouts.

Preserve the fixture workflow under D11. `just worktree <name>` remains the complete ONLV setup command: `development/worktree.ts` calls `scenery worktree create`, then runs `development/prepare.ts` in the new checkout. Preparation keeps its current guarantees: it verifies the preset's asset and snapshot checksums, materializes independent filesystem files, restores the coordinated database and object-storage snapshot into the worktree's own resources, starts the runtime, and ensures the development project and scene are registered. It keeps skipping restoration for a `ready` fixture marker, refusing a marker from a different preset revision, and refusing to overwrite filesystem assets or stored objects whose bytes differ from the preset. Migrate only its configuration inputs; do not move fixture content into Scenery configuration or add a fixture-refresh path in this plan. Updating an existing worktree to a newer preset revision stays an explicit, non-destructive operator action outside `prepare.ts`.

For frontends, disable dotenv at every participating layer. Current Vite documents `envDir: false`; current Bun documents `--no-env-file` and `env = false` in `bunfig.toml`. Verify support in the repository's pinned versions before using those forms, and make the narrow required version update if absent. Cover nested `bun run`, direct tests, Vite dev/build, and desktop builds; setting only Vite's option is not sufficient if Bun already populated the process. [E1, E2]

Replace application `VITE_*`/`import.meta.env` settings with a generated typed public configuration projection. Expose only explicitly public, recursively non-sensitive inputs. Serve deployment values through the existing frontend/bootstrap mechanism as a small revision-pinned JSON response or JSON script asset, not compiled string substitutions. This permits config-only deploy without rebuilding bundles. Use safe serialization, existing base paths/CSP behavior, and explicit cache invalidation by revision. Never expose host paths or arbitrary environment entries by default. Built-in compiler constants such as development/production mode may remain toolchain concerns, not application input APIs.

Shared library code accepts config objects rather than adding global `getConfig(key)` calls. Tests pass typed fake values. Update browser/desktop bootstrap tests so frontend deployment config and backend active config cannot accidentally diverge.

Update ONLV to the exact approved Scenery change through its established wrapper and pin workflow. During co-development, use the supported local-source selection and do not commit a local module replacement. If the required framework commit is not published, record the dependency blocker; do not invent a version or claim a released pin was tested. Regenerate contracts and declared TypeScript clients rather than hand-editing generated output.

**Exit proof:** two test-owned ONLV worktrees start without dotenv, read the same weather setting, retain distinct data/runtime identities, and expose no secrets to browser assets. All inventory consumers have migrated; existing local/devtools/all selection still works without cross-environment fallback. The fixture scenario in the ONLV integration loop passes.

### M6 — One-time conversion, deletion, and enforcement

Implement or supply a bounded one-time conversion tool for the operator's existing ignored files. It runs only when explicitly invoked and is not part of runtime startup. It must not evaluate shell code. Handle any supported variable expansion through a bounded parser and reject unsupported expressions rather than invent values.

Preview only names, proposed destinations, types, sensitivity, and conflicts. Apply one explicitly chosen source environment at a time. Do not infer production values from `.env.local` or combine every file in a checkout. Flag contradictions requiring an operator-selected source rather than apply the old precedence stack invisibly. Target production secrets directly over the authenticated receiver; do not stage them as workstation configuration.

Preserve signing/encryption key bytes, database identity, object storage credentials, and existing capability ownership. Validate resolved parity privately without emitting secret values or public password-verification hashes. Stage all migration values before publishing the replacement desired revision. A failed migration must not leave a half-populated activated environment.

Only after new-path proof, remove or securely archive the old user-owned files with explicit authorization. Never auto-delete personal or production `.env` files during `up`, `check`, or worktree pruning. A leftover file is ignored by the runtime and reported by the migration/harness checks without displaying contents.

Delete `ResolvedEnv.DotEnvFiles`, `appEnvWithDotEnv`, their runtime call sites, obsolete app env mappings, dotenv dependencies, generated `.env.example` scaffolding, and worktree copy logic. Rewrite tests to prove the replacement behavior rather than keeping old precedence tests alive. Historical completed plans remain immutable; update current instructions, environment registry, examples, templates, fixtures, diagnostics, and help. The offline migration parser, if retained for operator cutover, must be isolated from the product/runtime import graph with a narrow harness exception; it does not justify a general dotenv library dependency in applications.

Add semantic/static guards to the existing harness. For Go, identify environment reads by resolved package symbols, including aliases and wrapper packages. For TypeScript/JavaScript, check application env reads, dotenv/loadEnv usage, and forbidden loader settings using the repository's parser/lint machinery. Review framework adapter allowlists narrowly; no blanket exception for `internal/`, tests, or CLI code. Scan active recipes and templates too. Deliberate negative-test fixtures and immutable historical docs are classified explicitly rather than rewritten.

The final application code and supported application launch paths must have no application dotenv support and no ambient application-value fallback. Old ambient JWT, database, weather, provider, and browser config variables must not change resolved behavior.

**Exit proof:** the complete acceptance matrix passes, the inventory has no unmigrated application inputs, real operator cutovers are explicitly recorded or left unverified, and no released compatibility path survives.

## Concrete Steps

Run all commands in the repository named below. These are implementation/verification instructions for the executing agent, not claims that they were run while preparing this plan.

### Before edits, in each repository

```sh
git status --short
git rev-parse HEAD
```

Read the owning instructions and compare current source against the reviewed baselines. Preserve unrelated dirty work. Record source drift that affects this plan. Allocate the Scenery plan number using the current historical files, not merely the active index.

Use bounded source searches. Exclude dependencies, generated output, caches, private data, and actual dotenv files; inventory source references rather than printing values. For example:

```sh
rg -l --hidden \
  -g '!.git/**' -g '!**/.env*' -g '!**/.scenery/**' \
  -g '!**/node_modules/**' -g '!**/vendor/**' -g '!var/**' -g '!x/**' \
  'os\.(Getenv|LookupEnv|Environ)|envpolicy|process\.env|import\.meta\.env|Bun\.env|dotenv|loadEnv|env-file|dotenv-load'
```

Inspect the resulting source files selectively. The search is a starting point, not the final guard; it cannot identify SDK ambient discovery or every shell wrapper by itself.

### Scenery implementation loop

Introduce proposed private packages `internal/appconfig` for the catalog/resolver/store and a narrow secret-storage adapter beneath that owner, unless the current architecture already provides an equally precise owner. Keep runtime snapshot decoding with the runtime owner. Do not couple pure resolution to OS/SSH adapters.

Run focused tests after each milestone. Once the proposed package exists:

```sh
go test ./internal/appconfig ./internal/app ./internal/compiler ./internal/generate
go test ./cmd/scenery ./runtime ./auth
go test -race ./internal/appconfig
```

Add the exact remaining changed packages to the plan's validation record from the actual diff. Fake clocks, stores, watchers, SSH transport, and secret backends keep ordinary tests service-free. Real processes and OS stores belong in named probes.

Regenerate both Scenery-owned fixture clients after compiler/generator changes:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Run the required full verifier, not quick followed by full:

```sh
go run ./scripts/verify --summary --write
golangci-lint run ./...
```

Inspect `.scenery/harness/agent-context.json` and fulfill the exact union of `changed_area.recommended_commands`. Expected classes include Go, CLI JSON, compiler/generator, and release/runtime; TypeScript/tooling changes add their owner requirements. Reuse successful evidence for identical inputs/scope rather than rerunning the same broad suite unnecessarily. [R1, R8]

### ONLV integration loop

Use the supported source checkout for co-development:

```sh
just framework-use --source /absolute/path/to/the/modified/scenery/checkout
./scripts/scenery framework inspect -o json
./scripts/scenery generate --target contracts -o json
just gen-typescript-clients
./scripts/scenery check -o json
./scripts/scenery generate --check
go test ./solar/designs/... ./pkg/nsrdb/...
go test ./...
just repo-harness
just check-harness
just check-app nextnext
bun run --cwd apps/nextnext test:unit
bun run --cwd apps/nextnext build
just lint
```

The absolute source path is a local operator substitution, not a committed replacement. Before final release-pin acceptance, remove only the temporary Scenery replacement, select the actual approved published revision, and repeat producer selection and affected integration proof.

Because root tooling/configuration and generated clients affect multiple frontends, run ONLV's current changed-file validation union too:

```sh
./scripts/scenery validate changed --base main --dry-run -o json
```

Run the exact selected profiles from that output on final inputs and record any unmatched/manual obligations; the dry run is not acceptance. Cover every configured frontend's dev/build dotenv behavior, not only NextNext.

Run `just smoke` only inside a prepared test-owned ONLV fixture, after updating that fixture to the new config bootstrap. It must exercise authenticated persistence and restart under the new producer. Do not point it at the personal checkout or production data. [R9]

Prove the D11 fixture rule with the real application, using test-owned worktrees only:

1. Create two fresh worktrees with `just worktree <name>`; neither may receive a copied or symlinked `.env`.
2. In both, load the `small` preset's development project and scene through the served application (browser or authenticated API), not only through database queries.
3. In one worktree, edit and delete fixture data: change a project record, delete a scene object, and add an upload. Verify that the other worktree still serves the unchanged fixture and that `development/presets/small/` is byte-identical to the commit (`git status --short` is clean and `snapshot.json` checksums still verify).
4. Stop and restart the edited worktree's runtime, then rerun `bun development/prepare.ts` there. The rerun must resume without restoring the snapshot or resetting the user's edits, deletions or uploads.
5. Record the sanitized database, storage, origin and runtime identities of both worktrees in the evidence, and clean only resources whose test ownership was verified.

### Test-owned behavioral transcript

Create a dedicated fixture application with a unique stable test app ID and two worktrees using the existing fixture/worktree provisioning helper. Do not introduce a product `--scope` or temporary config-home env knob for this test. Invoke the worktree-local harness binary or ONLV wrapper appropriate to the fixture.

```sh
scenery config set designs.simulation_concurrency 2 --env local
scenery config show --env local -o json
scenery up
```

Start the second worktree through its own root. Change the setting once from either root. Capture desired/applied revision IDs, affected service generations, unchanged executable hashes/build-action counts, and distinct database/socket/origin identities. Then unset the key and prove the default is restored in both. Save sanitized evidence and clean only resources whose test ownership was verified.

## Validation and Acceptance

### Required behavioral matrix

| Area | Required assertion |
|---|---|
| CLI model | Only app/environment chooses authority; no scopes, active-env mutation, worktree overrides, or per-write target selection. |
| Defaults | Schema/default extraction works without secrets; required runtime values fail only at runtime-readiness validation. |
| Types | Wrong types, invalid constraints, unknown writes, illegal nulls, and non-deployment writes are rejected. |
| Paths | Absolute target-aware syntax; missing paths stay configured; no directory hashing or cross-host stat. |
| Sharing | Two worktrees observe one local configured-value authority without copies/symlinks. |
| Isolation | Existing per-worktree database, browser origin, sockets, runtime tokens, and storage ownership remain distinct. |
| Branch differences | Future stored keys become unused on old branches; incompatible known types do not kill a healthy old generation. |
| Atomicity | Concurrent different-key writes survive; expected-revision conflicts do not mutate; partial writes never become authoritative. |
| Secret handling | No plaintext secret in JSON, argv, logs, build output, public APIs, browser assets, or ordinary evidence. |
| Secret lifecycle | Creation/publication interruption, locked store, denied access, rotation, rollback pins, and collection are correct. |
| Runtime changes | Only relevant consumers restart; no compiler/linker/generator invocation for value-only changes with a current catalog/build. |
| Capability ownership | Shared configuration cannot redirect a worktree onto another worktree's retained mutable resources. |
| Fixtures | Fresh worktrees start from the identical pinned preset; edits in one leave the other and the committed preset unchanged; restart plus rerun of preparation resumes without resetting user changes. |
| Legacy env | Poisoned old env names and dotenv files do not change configured values or browser content. |
| Frontend | Bun/Just/Vite/desktop paths cannot load application dotenv; only typed public projection is delivered. |
| Remote authority | Target outage fails explicitly; no local fallback or secret-cache authority. |
| Deploy revision | One captured desired revision is used throughout activation, frontend publication, and resume. |
| Config-only deploy | Reuses executable and unchanged frontend bundle; promotes only the pinned configuration. |
| Deploy failure | Preflight failure leaves serving intact; activation failure restores exact executable/config pair. |
| Target layout | Source synchronization cannot delete config/history/secrets; same-host environments stay distinct. |
| Recovery | Crash/reboot resumes active, not desired; retained pins and data ownership survive. |
| ONLV | Weather fallback, auth, providers, imports, normal frontend launch, durable workers, and persistence work without dotenv. |

### External probes and exact commands

Extend existing probes rather than inventing a parallel acceptance framework. Register three focused new probe IDs in `scripts/verify/harness_probes.go` and the owning catalog: `configuration`, `configuration-secrets`, and `configuration-deploy`. These are **new IDs to implement**, not existing commands at the reviewed baseline.

`configuration` owns two-worktree sharing, real process restart, build-action counts, poisoned dotenv/ambient inputs, and source/config separation. `configuration-secrets` owns OS secret-store integration, redaction, denied/headless access, and interrupted version publication. `configuration-deploy` owns a disposable SSH/systemd target, config RPC, failed activation, reboot/resume, and same-host multi-environment isolation. Use existing fixture provisioning and target lifecycles. Fakes do not satisfy the real OS/target portions.

After registering them, run from Scenery:

```sh
go run ./scripts/verify \
  --probe configuration --probe configuration-secrets --probe configuration-deploy \
  --summary --write
```

Run the existing affected-boundary regression union:

```sh
go run ./scripts/verify \
  --probe generation --probe native-contract --probe build-info \
  --probe cli-process --probe cli-grammar --probe dev-process --probe process-model \
  --probe worktree --probe worktree-git --probe parallel-runtime \
  --probe postgres --probe auth --probe storage --probe snapshot-backup \
  --probe assistant-runtime --probe code-task --probe typescript \
  --probe desktop --probe deploy-ssh --probe agent-restart \
  --summary --write
```

These existing names were verified in the current probe catalog. Update the selection if current owner guidance adds a required boundary; do not substitute a nonexistent guessed probe name. Test-run receipts must state precisely which commands ran and which prerequisites failed. [R8]

Run macOS secret proof on a macOS runner with an isolated test credential namespace, and Linux deployment/credential/reboot proof on a disposable systemd host controlled by the probe. A platform-specific segment may be unexecuted on the other platform only if the report records its absent OS/backend and links to the required separate platform job. It remains unverified until that job passes. Never convert unavailable proof into a pass or a silent test skip.

Each real probe verifies cleanup of its own processes, descriptors, secrets, deployment releases, and fixture data without pruning unrelated state. Keep normal unit suites service-free and cached; do not add sleeps, repeat `-count=1`, or move external work into ordinary test initialization. Do not add a benchmark or an all-root timing audit without explicit measurement authorization. Build-action counts and unchanged artifact hashes are functional acceptance, not a wall-clock performance claim.

Do not run the production deployment or mutate real production keys merely to complete this plan. Real operator migration requires explicit authorization. If unavailable, deliver the tested implementation with that operator cutover visibly outstanding. Full release certification is separately explicit through `scripts/release-gate.sh`; the commands above do not claim release certification.

## Idempotence and Recovery

Configuration mutation operates on one key under a per-environment lock. Preserve untouched entries. Request/revision preconditions protect retries and concurrent agents. Temporary files are never read as authority. Recovery accepts either the old complete document or the new complete document, not a repaired interpretation of partial JSON.

Secret creation precedes publication; orphan cleanup follows reference scanning. A startup/readiness command never deletes credentials or creates missing production keys. Keep secret versions referenced by active and rollback deployments, even after desired configuration changes.

Pin a revision before building/preparing a candidate. If source schema or producer changes during preparation, invalidate that candidate and restart preparation; do not silently mix the old catalog with new values. Rollback uses the retained exact snapshot, not a new resolution against current source/defaults.

Reboot and supervisor/framework handoff use active pinned receipts. Keep candidate and active pointers distinct. If a desired update is invalid, serve the last-known-good generation and show the pending error. An explicit new deploy is required to advance production.

For the old remote checkout relocation, require an ownership/data inventory and recoverable backup before any approved move. Record the previous producer and root identity. Restore with the owning migration/runbook mechanisms; never delete registry state, adopt a different database, or fake revision metadata to make startup pass.

Migration can be previewed repeatedly. Applying an already migrated value is either a no-op or an explicit conflict, never a reason to overwrite a newer environment blindly. Old user files remain untouched until separately authorized archival/deletion. The runtime never falls back to them during recovery.

Fixture preparation is idempotent per worktree: a `ready` marker skips restoration, and an interrupted preparation resumes from its marker without overwriting assets or objects whose bytes differ from the preset. Configuration changes never trigger fixture restoration, and fixture restoration never writes configured values.

## Artifacts and Notes

### M3 live proof (2026-09-24)

Fixture: `testdata/apps/multiservice` copied to the scratchpad with `id:
"cfgprobe"`, `echo` inputs `prefix` (string, default `echo`) and `repeat`
(uint32, default 1, minimum 1), isolated `SCENERY_AGENT_HOME`, worktree-local
`.scenery/harness/bin/scenery` built with framework producer linker flags.

- Baseline `POST /echo` → `{"message":"echo:hi"}`; PIDs greeter 79346, echo
  79347, host 79357.
- `config set echo.prefix shout --env local` → `{"message":"shout:hi"}` and
  `greeter:shout:hello petr`; echo restarted as 79915 with the identical
  executable `scenery-app-a862dcf2…`; greeter and host PIDs unchanged; the
  runtime log's only new event is `config.applied` with
  `restarted_services: ["echo/service/echo"]` (no `build.artifact` or
  `go.command` step after the change). `config show` → `applied`.
- A store write of `"many"` for `echo.repeat` (simulated newer branch) plus an
  unknown `echo.future_flag`: the runtime kept serving `shout:hi` with all PIDs
  unchanged; `config show` reported `runtime rejected` with the typed problem
  and `echo.future_flag` as unused.
- `config set echo.repeat 2` → `shoutshout:hi`; `config unset echo.prefix` →
  `echoecho:hi` (default restored), each restarting only echo.
- Second git worktree of the fixture started with the shared values; one `set`
  from it changed both (`sharedshared:hi` in each); separate API sockets and
  PIDs; `config show` reported `other_runtimes: {total: 1, applied: 1}`.
- `scenery down` in both removed every process and both pins.

### macOS Keychain proof (2026-09-24)

Same fixture with a required environment secret `echo.token` read through
`input.Config.Token.Reveal()`:

- `scenery up` refused to start: `echo.token: required input is not
  configured`, naming the fix.
- `printf … | scenery config set echo.token --env local --stdin` created one
  login-Keychain item (service `sh.scenery.config.cfgprobe.local`, account
  `echo.token/<version>`); the plaintext appeared in no CLI output, no store
  file and no agent or runtime log, and not in any service process's
  environment (`ps eww`); service processes carried only
  `SCENERY_CONFIG_SNAPSHOT_FD=3`.
- The runtime revealed exactly the configured 20 bytes; rotating to a 32-byte
  value restarted only echo, which then revealed 32 bytes; the previous version
  stayed retained for history.
- Cleanup: `scenery down`, then both test Keychain items deleted; none remain.

### M4 deploy rehearsal (2026-09-24)

A test-double `ssh` (scratchpad `fakessh/ssh`) ran every remote command on
this Mac under a separate target `HOME`, so rsync, `scenery config receive`,
`scenery deploy receive` and the target's `scenery up` ran for real. Fixture
as in the M3 proof with `envs.production.deploy.ssh = ["fake-target"]`.

- `config set echo.prefix prod --env production` wrote only
  `target-home/.scenery/apps/cfgprobe/environments/production.json`; the
  workstation store has no production document.
- First `scenery deploy --env production` → target served `prod:hi`;
  `active.json` state `active` with the captured revision.
- `config set echo.prefix prod2 --env production` left the target serving
  `prod:hi`; `config show --env production` reported desired and applied
  revisions separately. A target `down`/`up` (reboot/resume stand-in) still
  served `prod:hi`.
- Discovered and fixed: the remote `down` guard probed
  `$HOME/.scenery/run/agent.sock`, which does not exist when the agent socket
  path is long, so `down` was skipped, `up` reported "already up" and the old
  runtime kept serving while the release was confirmed. `down` now always runs
  for an installed root, and `commit` requires the stable root's runtime pin to
  have applied the installed revision.
- Config-only redeploy (`prod3`): served `prod3:hi` with the identical three
  executables; the target run recorded no `build.artifact` step and
  `process.reuse: linked_identity_unchanged`.
- A Go compile error was rejected by the workstation check before any target
  step. A release whose constructor refuses its configured value failed
  `up --wait ready`; deploy reinstalled the previous release's source and
  configuration revision and reported `restored previous release …`; the
  target served `prod3:hi` and the running echo executable did not contain the
  failed release's code. Limitation: the restored executable is rebuilt from the
  retained previous source (equivalent, not byte-identical), because
  development-process executables are not retained per release.
- Final `down` left no target processes.

### D14/D15 live proof (2026-09-24)

Worktree-local CLI built with producer linker flags; isolated
`SCENERY_AGENT_HOME` per scenario.

- `examples/webhook-inbox` copy (SQL requirement with `lifecycle = "external"`)
  and a throwaway `postgres:18-alpine` container. With
  `DATABASE_URL=postgres://…@203.0.113.9/…` in the shell and nothing
  configured, `db list` refused with the lifecycle diagnostic naming
  `sql.database_url`; the inherited URL was not used. `config show` listed
  `sql.database_url` as an optional framework secret.
- `printf … | scenery config set sql.database_url --env local --stdin` stored
  one Keychain item; the password appeared in no CLI output and no store file.
  `db list` (same bogus shell variable) then reported `source: external`, the
  container's port, a measured database size and the password redacted.
- `scenery up` refused: `environment local configures sql.database_url, one
  external database for every worktree …`, without the URL.
- `scenery worker` could not be exercised: it needs a `worker`-role go target,
  which the compiler rejects (SCN6135), independently of this plan; recorded as
  a separate task. The worker path is covered by unit tests.
- `testdata/apps/multiservice`-based fixture: `scenery up` with planted
  `JWT_SECRET`, `AWS_SECRET_ACCESS_KEY`, `NODE_OPTIONS`, `NSRDB_PACK_ROOT` and
  `DATABASE_URL`. The supervisor carried the planted variables; the three
  service processes carried none of them (only `PATH`, `HOME`, `SCENERY_*` and
  `SCENERY_CONFIG_SNAPSHOT_FD`), and `POST /api/echo` answered from the
  snapshot-delivered 21-byte secret.
- Cleanup: container removed, `scenery down`, every test Keychain item deleted
  (none remain), proof homes removed.

### M5 ONLV validation (2026-09-24)

ONLV worktree `feat/environment-configuration` with the local framework
selected through `framework use --source` (not committed):
`scenery check` and `generate --check` ok; `GOWORK=off go test ./...` ok (the
ignored `utilities/data` CSVs had to be copied from the main checkout);
`go run ./cmd/repoharness` ok; `golangci-lint run --tests=false ./...` 0
issues; NextNext `typecheck`, `lint`, `i18n:check`, `bun test src` (1080
pass) and `vite build` ok; viewer typecheck/lint/knip ok; Playwright
`scene-creation.pw.ts` 2/2 with the public-config fixture. `scene-viewer.pw.ts`
fails the same 6 WebGL render tests on untouched `origin/main`, so those
failures predate this change. A temporary `.env.local` proved Vite's
`envDir: false` ignores it (and loads it without the option); a scratch Bun
project proved `env = false` stops Bun's automatic `.env` loading.

### D11 fixture proof (2026-09-24)

On ONLV `8b0d0b19` pinned to `93bd4b89d0a5`: two `just worktree` checkouts
without `.env` each restored the `small` preset into their own database,
Postgres container and object storage. An API edit of the fixture project, a
deleted scene object and a new upload in one worktree left the other serving
the unchanged fixture; the preset files stayed byte-identical. After `down`
and a rerun of `development/prepare.ts`, the edited worktree resumed without
restoring the snapshot and kept all three changes. Test-owned databases,
state, containers, volumes and branches were removed afterwards.

### M0 value-free input inventory (2026-09-24)

Collected with `rg`/`git grep` over Scenery `96df6695` (framework and runtime
code, excluding tests, `scripts/verify` and fixtures) and ONLV `origin/main`
`d7e2e0ab`; dotenv files were read for variable names only. Classes: **A**
typed application input, **F** framework-managed runtime capability, **S**
structural/build setting, **O** OS/toolchain requirement, **D** dead input.

Scenery framework (application-facing):

| Current name | Consumer | Class | Destination |
|---|---|---|---|
| `JWT_SECRET` | `auth/standard.go` | A (secret) | `auth.jwt_secret`; local development keeps the established dev-only default |
| `GOOGLE_OAUTH_CLIENT_ID` | `auth/standard.go`, `cmd/scenery/check.go` | A | `auth.google_client_id` |
| `GOOGLE_OAUTH_CLIENT_SECRET` | `auth/standard.go`, `cmd/scenery/check.go` | A (secret) | `auth.google_client_secret` |
| `AUTH_TOKEN_CIPHER_KEY` | `auth/standard_google_cipher.go` | A (secret, base64 32 bytes) | `auth.token_cipher_key`; key bytes preserved |
| `AUTH_COOKIE_DOMAIN` | `auth/standard.go` (supervisor injects empty locally) | A | `auth.cookie_domain` |
| `AUTH_EMAIL_FROM` | `auth/standard.go` | A | `auth.email_from` |
| `SCENERY_CORS_ALLOW_ORIGINS` | `runtime/server.go` | S | stays structural runtime wiring (registry `user_input`); reviewed in M6 guard |
| `DATABASE_URL`, `*_DATABASE_URL`, `SCENERY_DATABASE_JSON` | `runtime/sql_bindings.go`, `runtime/durable.go`, `db/db.go`, `auth/standard.go` | F | worktree-owned SQL supply injected by the supervisor; ambient external supply is removed (M3) |
| `SCENERY_ASSISTANT_TOKEN_KEY(_FILE)` | `runtime/assistant_bootstrap.go` | F | supervisor-owned private key file |
| `SCENERY_STORAGE_CONFIG`, assistant, durable, listen, link, report, session vars | runtime | F | unchanged injected wiring |
| `.env`, `.env.<env>`, `.env.local`, `.env.<env>.local` | `appEnvWithDotEnv`, `ResolvedEnv.DotEnvFiles`, `runtime.LoadDotEnvIntoEnv` | removed | deleted in M6 |

ONLV (`d7e2e0ab`):

| Current name | Consumer | Class | Destination |
|---|---|---|---|
| `NSRDB_PACK_ROOT` | `solar/designs/designer_common.go` | A | `designs.weather_pack_root` (`optional(host_path)`) |
| `GOOGLE_OAUTH_CLIENT_ID/SECRET`, `JWT_SECRET` | root `.env` → Scenery auth | A | `auth.*` keys above |
| `UTILITYAPI_TOKENS_JSON` | `solar/consumptionprofiles/api.go` | A (secret) | consumptionprofiles deployment secret input |
| `SOLAR_API_TOKEN`, `DATABASE_URL`, `UTILITIES_DATABASE_URL` | `utilities/cmd/download.go`, `cmd/*_import`, `solar/*/cmd/import` | A / F | importer capability bundle from the task launcher (M5) |
| `MAPS3D_FLYOVER_MANIFEST_URL`, `MAPS3D_FLYOVER_TOKEN_P1` | `pkg/maps3dflyover/meshgetter.go` | A | maps deployment inputs, passed explicitly |
| `MAPS3D_BASISU_PATH`, `MAPS3D_GLTFPACK_PATH` | `pkg/maps3d/meshops` | O | tool locations passed explicitly by the owning service |
| `MAPS3D_DEBUG_*`, `MAPS3D_DUMP_*` | `pkg/maps3d` | D/S | developer debug switches; removed or made explicit options in M5 |
| `SCENERY_APP_ID`, `SCENERY_VICTORIA_TRACES_ENDPOINT` | `pkg/pulsetrace/read.go` | F | framework-injected wiring |
| `SCENERY_BIN` | `internal/repoharness/context.go` | O | harness tool selection |
| `VITE_GOOGLE_MAPS_API_KEY`, `VITE_GOOGLE_MAPS_MAP_ID`, `VITE_APPLE_MAPS_TOKEN` | `apps/nextnext` via `import.meta.env` | A (public) | typed public frontend projection (M5) |
| `import.meta.env.DEV`, `BASE_URL`, `NODE_ENV` | frontends | S | toolchain constants, not application inputs |
| `process.env.*` in Playwright/QA scripts (`CDP_URL`, `PLAYWRIGHT_BASE_URL`, `RESPONSIVE_*`, `STYLE_*`, `BREAKPOINT_*`, `DESIGNER_*`, `ORACLE_*`, `INTERACTION_*`, `SCENE_*`, `OUT`, `DEBUG`) | test/QA tooling | O | test-tool arguments; not application configuration |
| Root `.env` Encore-era names (`ClerkSecretKey`, `DatabaseURL`, `ENCORE_*`, `LightRabbitMQURL`, `House*`, `Maps*`, `POSTGRES_PORT`, `PULSE_PORT`, `VIEWER_PORT`, `COMPOSE_PROJECT_NAME`, `PublicGoogleMapsAPIKey`, `DisableRoofWorker`) | none | D | ignored; archived only with explicit authorization |
| `Justfile` `set dotenv-load := true` | Just recipes | removed | M5 |

The implementation handoff must contain: the updated master/ONLV plans; the value-free input migration inventory; strict current CLI/store/snapshot schemas; source and target protocol tests; regenerated clients; the actual Scenery pin in ONLV; sanitized unit/probe receipts; and an explicit list of pending operator actions.

Use existing ignored `.scenery/harness/` locations for evidence, with bounded reports such as `environment-configuration.json`, `environment-configuration-secrets.json`, and `environment-configuration-deploy.json`. Include exact source/build producer identities, chosen app/environment identities, desired/applied revisions, assertions, and cleanup. Do not include raw secret values, secret verification digests, or full process environments.

Suggested review boundaries are: contract/store; CLI/secrets/SSH; runtime/framework consumers; deployment activation; ONLV migration; and final compatibility deletion/harness documentation. These are implementation milestones, not independently releasable feature modes. ONLV adoption follows the tested framework change. Publication, push, and production operations remain subject to actual user authorization.

The final handoff must explicitly answer: Does a new worktree run without copied files? Can any old application env name still affect behavior? Can config-only changes cause builds? Can local data leak into production configuration? Does reboot use active rather than desired? What remains unverified on real macOS/Linux targets?

## Interfaces and Dependencies

Keep the internal boundaries narrow:

```text
Catalog extraction: source contract → typed input catalog, no secrets/network
Resolver: catalog + one environment revision → validated values, pure
Store: read desired / mutate one key / read revision / pin / release pin
Secret adapter: create immutable version / resolve version / remove unreferenced version
Remote transport: bounded authenticated read/mutation requests, no shell-valued config
Runtime bootstrap: validate one snapshot → generated typed constructor inputs
Supervisor: compare consumer identities → existing generation handoff
Deployment: pin candidate → validate → activate → persist active receipt
```

Use current schema/spec/producer revision enforcement. Register any new diagnostic in the existing checked catalog. One current decoder per new format; an explicit retained-state migration handles older state. Do not weaken producer checks to enable a mixed-version rollout.

Prefer Go's standard library for files, JSON, synchronization, and process I/O, together with existing project helpers. OS credential adapters are the only intended platform-specific secret dependency. Audit any proposed library for secret-in-argv behavior, headless failure semantics, and new build requirements before adoption. Do not add a browser configuration editor or a new public secret-management surface in this plan.

### Source references

Repository references below are pinned to the reviewed snapshots. Implementation must reread changed owners at its actual starting revision.

- [R1 — Scenery agent and validation rules](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/AGENTS.md)
- [R2 — Scenery ExecPlan contract](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/PLANS.md)
- [R3 — Existing typed Go configuration and secret references](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/internal/compiler/go_config.go)
- [R4 — Current SSH deployment and synchronization](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/cmd/scenery/deploy_ssh.go)
- [R5 — ONLV application identity and environments](https://github.com/pbrazdil/onlv/blob/d7e2e0ab63e41f9aa94535811c09b891b1775125/.scenery.json)
- [R6 — ONLV weather client construction and fallback](https://github.com/pbrazdil/onlv/blob/d7e2e0ab63e41f9aa94535811c09b891b1775125/solar/designs/designer_common.go)
- [R7 — Active Scenery runtime plans](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/docs/plans/active.md)
- [R8 — Exact existing external probe catalog](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/scripts/verify/harness_probes.go)
- [R9 — ONLV verification and smoke contract](https://github.com/pbrazdil/onlv/blob/d7e2e0ab63e41f9aa94535811c09b891b1775125/docs/agent/HARNESS.md)
- [R10 — ONLV framework selection and generation](https://github.com/pbrazdil/onlv/blob/d7e2e0ab63e41f9aa94535811c09b891b1775125/docs/agent/SCENERY.md)
- [R11 — ONLV ExecPlan convention](https://github.com/pbrazdil/onlv/blob/d7e2e0ab63e41f9aa94535811c09b891b1775125/PLANS.md)
- [R12 — Current Linux/systemd deployment owner](https://github.com/scenery-sh/scenery/blob/96df669500021fb155c15c3d5b7177abebc79dca/cmd/scenery/deploy_systemd.go)
- [E1 — Vite shared options, including envDir](https://vite.dev/config/shared-options#envdir)
- [E2 — Bun automatic dotenv-loading controls](https://bun.com/docs/runtime/environment-variables)
- [E3 — systemd credential model](https://systemd.io/CREDENTIALS/)
- [E4 — Apple Keychain services](https://developer.apple.com/documentation/security/keychain-services)
