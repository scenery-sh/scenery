# Worktree-Owned Development Runtime and PostgreSQL

This ExecPlan is a living document and must be updated as work proceeds. Assigned for implementation on 2026-09-07 in `/Users/petrbrazdil/Repos/scenery`. Preserve the existing dirty implementation and removed-feature cleanup. Execution includes repository changes and explicitly owned disposable test resources. The user additionally requires an equivalent clone of the existing main database if needed; preserve the source and identify the exact application/database before any real migration. No shared CLI installation, commit, push, or unrelated runtime/database mutation is authorized.

Prepared: 2026-09-07. Inspected repository: `scenery-sh/scenery`, baseline `c56e3e9614e58914e27a8536b171e4d979f32bbb`. Reconcile this baseline with the executing checkout before editing. The original lending experiment used `86790aea52073f26fdfffc6108a6ac382581f5f5`.

Repository destination: `docs/plans/0167-worktree-runtime-postgres.md`; the permanent ID was allocated after checking current tracked/untracked plans and prior allocations. Registered in the active and knowledge indexes.

Relationship to the preceding plan: this is the implementation plan for the runtime-ownership work previously deferred by **Ordinary Go Contracts Without Generated Git Noise**. It does not reopen that plan's decision: application-imported generated Go belongs inside the application's Go module and is ignored by Git by default. Coordinate overlapping startup/build hooks without maintaining two implementations. This plan must not be closed after merely producing another ownership decision brief.

## Purpose / Big Picture

Make an ordinary Git worktree the ownership boundary for local application execution and managed PostgreSQL. From a newly created checkout, `scenery up` should prepare the application, provision its required local database, and publish working localhost routes without requiring a private agent home, a router address, Docker resource names, or knowledge of another application's installed Scenery version.

The target is one development supervisor per application root, with worktree-local control and routing, plus one dedicated PostgreSQL instance for each worktree that actually requires managed SQL. Within that instance, retain the current application database and service-schema model. Stop operations preserve data; deletion is explicit. Independent worktrees can run incompatible Scenery revisions without replacing each other's control plane or sharing an application database.

This changes both the control-plane dependency and PostgreSQL ownership. Namespacing a container while retaining a mandatory connection to the incompatible shared agent is not acceptance. Conversely, starting a private control process while reusing machine-global PostgreSQL names is not acceptance.

A worktree here means an application checkout at a particular absolute root, whether created by `git worktree add`, `git clone`, a source-only copy, or without Git. It is not a Git branch, commit, PID, or CLI version. Each root has at most one live development environment; `--env` selects its configuration, not a second concurrently writable environment at that root.

The public model is deliberately small:

    git worktree add ../library-search -b feature/search
    cd ../library-search
    scenery up

Scenery provides the capabilities; developers use the printed application URL and ordinary `ps`, `logs`, `down`, `db`, and `snapshot` commands. No additional isolation mode, public namespace selector, PostgreSQL sharing policy, broker, or compatibility protocol is introduced.

The guarantee is lifecycle and managed-SQL isolation, not an OS security sandbox. Docker and physical host resources remain shared. Explicit external databases and intentionally shared storage retain their declared sharing semantics and must be identified as such. Do not claim isolation for them merely because their callers live in different worktrees.

## Progress

- [x] 2026-09-07: Prepared the design after reading current repository contracts and ownership/startup code. No implementation or runtime validation was performed during preparation.
- [x] 2026-09-07: Confirmed the inspected baseline still uses agent-home-local PostgreSQL state with globally named Docker resources. Confirmed the detached-startup and truthful-diagnostic corrections are already shipped baseline work.
- [x] 2026-09-07: M0 — Reconciled the dirty baseline, registered the plan, inventoried owners/consumers, and established exact-resource disposable acceptance. The baseline and candidate now build inside a nested-daemon lane; no developer global database or shared CLI has been modified.
- [x] 2026-09-07: M1 — Implemented durable worktree/resource identity and one PostgreSQL lifecycle resolver. Deterministic coverage and real interruption/recovery, conflict, retained-volume and explicit deletion acceptance passed.
- [x] 2026-09-07: M1 foundation — Added canonical root/state-home paths, private short socket directories, separate live/operation locks, strict retained worktree/PostgreSQL records, and fail-closed ownership updates. Added resumable create/start reconciliation with injected Docker and state seams. Package tests pass.
- [x] 2026-09-07: M2/M3 implementation in progress — Ordinary startup now embeds private worktree control and routing in its owner process; managed SQL and optional Victoria use worktree-owned resources. Database server lifecycle, read-only SQL resolution, and snapshot Docker execution use retained ownership with authenticated cluster identity. Focused and CLI package tests pass; this is not real-process acceptance.
- [x] 2026-09-07: Preliminary real-process proof — A private no-SQL fixture served HTTP and its Chrome console without a PostgreSQL allocation; down removed its live processes/listener. Two disposable lending roots used distinct containers, volumes, credentials and ports; each passed ten two-borrower races and typed 404/409/400 checks via the generated fetch client. An inert snapshot restore preserved the first book in the second root without starting workers or generating Go. During down of A, B served 78 sentinel requests with zero failures. These manual copies are not the required real Git-worktree A1–A18 release matrix. Candidate SHA256: `e6cf1ca3f0ab0e9137b67de3345ec8a128e71f4e8813590bf0e6616c9a851fb5`; both SQL runtimes are now stopped with their data retained.
- [x] 2026-09-07: M1 remaining — Stop/delete/restore transitions, legacy provenance guard and Docker inspection coverage are complete. The fresh release timing audit confirmed 169 selected roots across 3,380 isolated serial samples, with maximum p95 94ms and no 100ms violations.
- [x] 2026-09-07: M3 consumer hardening — Standalone apply/seed now holds lifecycle ownership through completion; status and dashboard-cache inspection are read-only. Removed unused global PostgreSQL allocation/decoder paths and old shared-agent down/prune/status dispatch. Current status/server/prune schemas describe worktree ownership and retained restore archives. Restore intent precedes target provisioning, with explicit verified overwrite as migration authority; unresolved legacy server authority blocks implicit fresh allocation. Focused package tests passed during these changes; final integrated validation remains pending.
- [x] 2026-09-07: A17 verified explicit machine-edge proxy registration and domain conflict isolation. The worktree proxy publishes only API routes; console/control remain localhost-only. Unavailable domain service falls back to localhost with diagnostics. The real Caddy static exposure probe also passed its 16 HTTP checks and raw traversal checks without host trust/DNS changes.
- [x] 2026-09-07: Added the named release worktree acceptance runner, which creates source-only real Git worktrees and records per-row results and actual subprocess argv. A1/A2/A3/A4/A5/A6 passed in the focused execution of that same runner: distinct retained clusters and credentials, ten typed lending races per root, no-SQL console and explicit missing auxiliary capability, 178 uninterrupted sibling requests during A's outage, and data retained through rebuild/down-up/supervisor crash/PostgreSQL restart. This is partial acceptance, not completion of A1–A18.
- [x] 2026-09-07: Fixed canonical-root SQL naming after the full harness exposed `/var` versus `/private/var` drift. Added observation-only PostgreSQL endpoint monitoring: authenticated port changes wake the existing rebuild loop without starting or allocating a database. Removed the unused machine-agent watchdog and obsolete disable control. Standalone `db setup` now holds ownership across apply and seed.
- [x] 2026-09-07: A7/A8 passed real concurrent acquisition and mixed-protocol binaries. A7 originally found a loser reading before the winner persisted its record; the verified winner now resolves both launch races. A8 changes an actual health/state wire field and descriptor, records source/binary SHA evidence, and rejects same-root incompatible access without replacing the owner.
- [x] 2026-09-07: A10/A12/A13/A14/A16 passed in the named release runner: conflicting retained credentials/daemon/mount/container/state remain untouched; container recreation preserves the volume and authenticated cluster while changing the published endpoint; Git-removed data remains discoverable and exact cleanup is retryable; equal external DSNs stay shared and reject managed destruction; inert snapshots preserve every fixture row and failed SQL restore blocks readiness; unsupported engine/state compatibility fails without replacement.
- [x] 2026-09-07: A11 passed real SIGKILL at five durable checkpoints in a source-overlay fault binary: pending intent, volume, container, endpoint, and persisted restore SQL intent. The shipped binary has no fault flags or environment controls. Retrying uses the same credentials/resource identity; interrupted overwrite requires explicit completion with the same archive.
- [x] 2026-09-07: A15 passed with the real pinned Victoria binaries. Serving readiness was reached while all three optional binaries were unavailable, diagnostics reported the failure, and both delayed component availability and a verified metrics-process kill recovered with zero sentinel HTTP failures. No PostgreSQL capability was allocated for the no-SQL app.
- [x] 2026-09-07: Closed the missing-authority allocation hole: a read-only exact-canonical-root Docker inventory now blocks new credentials when retained resources have lost their matching local record. Existing-cluster authentication precedes retained-state reconciliation, so a credential/system-identity conflict cannot rewrite authority.
- [x] 2026-09-07: A18 completed three repetitions at each of 1/5/10 SQL-backed worktrees with real Victoria, cold/warm startup, native and Docker resource samples, zero HTTP load failures, disk/hardware/daemon evidence and verified cleanup. This preliminary run overlapped source editing, so final cost reporting must use a frozen-source release rerun rather than treating its varying warm timings as a fixed-source benchmark.
- [x] 2026-09-07: A9 passed real immutable-baseline/current coexistence using one state home and an isolated nested Docker daemon. Native migration preserved fixture rows, exact seed ledger, owner, reader grant and pgcrypto extension; source data and old server authority remained unchanged. The old sibling served 252 requests with zero failures. Verified sandbox deletion completed. Evidence: `.scenery/harness/worktree-runtime/legacy-probe-latest.json`; final integrated release and frozen-source cost rerun remain required.
- [x] 2026-09-07: Read-only consumer audit removed unused global-agent client/dashboard-restart helpers and the unused provisioning URL resolver. Seed dry-run no longer starts a database or creates a ledger; focused tests passed. Release-gate CLI proof now uses disposable `go build -o`, including new authored files in its source-only snapshot, without installing a CLI or staging the user's work.
- [x] 2026-09-07: M2 — Normal development control/routing runs in the worktree supervisor without the mandatory shared-agent startup path. A1/A2/A7/A8/A9/A17 passed.
- [x] 2026-09-07: M3 — Database, status, shutdown, snapshot and cleanup consumers use the selected worktree authority; unused global allocation/port/client paths are removed. Real lifecycle and read-only checks passed.
- [x] 2026-09-07: M4 — All A1–A18 passed in one release invocation, including a frozen-source nine-run cost matrix and verified owned-resource cleanup. Its only repository-level failure was the environment registry scanner rejecting a dynamically constructed existing variable name in the probe; explicit registered literals now pass drift checks.
- [x] 2026-09-07: Fixed UI harness readiness to use the runtime-published dashboard URL rather than a parent-selected backend port. Chrome passed all six journeys without console/network failures. The complete release gate passed, including full Go/race/lint and source-snapshot build. Optional external-app smoke was not configured. Final full release rerun after the probe-only variable-literal correction is pending.
- [x] 2026-09-07: M5 — Delivered and exercised the native migration runbook, updated contracts/schemas, and completed all 47 release checks plus the release gate. Final A9 served 246 sibling requests without failure; A18 completed nine frozen-source runs with 28,913 load requests and zero failures. Chrome journeys and direct worktree logs also passed. Developer data, the installed CLI and unrelated applications were not migrated or modified.

Update this section at each meaningful stopping point. Split partially completed items. An implemented path without its required real-process evidence remains incomplete.

## Surprises & Discoveries

At the inspected baseline, `cmd/scenery/dev_services_postgres.go` allocates server state and credentials beneath the selected agent home but defaults the Docker objects to `scenery-postgres` and `scenery-postgres-data`. The latest check rejects conflicting port bindings before starting an existing container. Matching ports still do not establish ownership. `internal/postgresname/name.go` already distinguishes application databases by app identity and absolute root. Preserve useful database/schema semantics while changing physical server ownership. [R1][R2]

`cmd/scenery/dev_session_controller.go` does substantially more than register an API process: it resolves routes, allocates ports, prepares frontends, connects to the shared agent, and supplies dashboard/control state. `internal/agent/server.go` already provides an in-process `Server` with `NewServer`, `Run`, and `Close`; its constructor also performs machine-edge and registry work. Reuse and separate those responsibilities deliberately rather than spawning another global daemon or transplanting the constructor unchanged. [R3][R4]

`worktree.go` wraps Git, and its `--db` removal path operates on `.scenery` filesystem state rather than serving as PostgreSQL ownership authority. Its callers require an explicit audit.

Completed plans 0163 and 0164 already preserve structured detached failures through a private inherited pipe, observe supervisor exit, make dotenv optional, classify expected runtime failures, and qualify diagnostic summaries. Their recorded results are prior evidence, not validation of this plan. Preserve those changes. The exact Victoria failure in the original experiment remains unestablished. [R7][R8]

The original experiment report and source are available at the provided path, verified read-only during M0. Its earlier HTTP/client behavior, ten two-borrower races and persistence are historical standalone-binary evidence, not acceptance of this implementation. Adapt its authored lending inputs to the managed-runtime release fixture.

Record new findings here with paths, exact commands, or evidence files. In particular, record any hidden shared database, process-wide environment mutation, stale-data adoption, cross-worktree shutdown, or protocol dependency uncovered during M0–M4.

The initial resolver tests used real fsync-backed state for every simulated
transition; one recreation test took 120ms. Moved orchestration tests to an
in-memory injected state seam while keeping private-file/strict-decoding coverage
in `internal/agent`. Production writes remain fsync-backed. Repeated isolated
timing has not yet been measured, and real-process recovery remains an M4 lane.

## Decision Log

- 2026-09-07, plan author, following the developer's requested direction: use a dedicated managed PostgreSQL instance per SQL-requiring application worktree. This prioritizes independent lifecycle and upgrade boundaries over minimum process count. Measure the cost; do not implement alternative sharing modes speculatively.
- 2026-09-07, plan author: retain one application database with service schemas and the framework `scenery` schema. Do not create a container per service, branch, session, Scenery revision, or process.
- 2026-09-07, plan author: make the existing per-app supervisor the sole live development owner. Host local control/routing in that owner using existing agent/router components; do not automatically start a second standalone agent per worktree or fall back to a global agent on failure.
- 2026-09-07, plan author: durable capability identity and credentials outlive supervisors and checkout deletion. Keep their authoritative record in owner-only user-local Scenery state partitioned by worktree. `.scenery/` in the checkout is not the only recovery record.
- 2026-09-07, plan author: scope identity by normalized absolute root within the user-local state namespace; store and verify the complete identity. Short names are display/resource labels, not proof. A move requires explicit transfer; a branch switch retains the existing database.
- 2026-09-07, plan author: PostgreSQL endpoints are observed after ownership verification. Bind only to loopback, let Docker allocate the host port, and verify authentication and database identity before advertising readiness. A changed endpoint is never permission to adopt an unrelated server.
- 2026-09-07, plan author: `down` stops this worktree's owned development processes and preserves data. PostgreSQL engine image identity is durable; a Scenery update must not implicitly perform a PostgreSQL major upgrade.
- 2026-09-07, plan author: new worktrees receive fresh declared schema/setup/seed data. Data transfer uses explicit existing snapshots; no automatic branch database, live-template clone, or inferred reverse migration.
- 2026-09-07, plan author: external `DATABASE_URL` is operator-owned. Preserve it exactly, provide no implicit managed isolation, and never create/drop external databases merely to simulate worktree isolation.
- 2026-09-07, plan author: retain one current wire protocol and strict mutation decoding. Separate binaries may own separate roots. A newer CLI cannot control an incompatible live owner at the same root, and incompatible durable metadata cannot justify silently creating new data.
- 2026-09-07, plan author: no shared machine migration is authorized. Existing applications, global PostgreSQL data, installed agents, launchd/systemd units, public routes, and installed CLI remain untouched during implementation validation.
- 2026-09-07, implementation audit: machine-global server credentials alone must not claim every future root. The migration guard keeps credentials opaque, requires a readable exact ownership registry when old server authority exists, and checks canonical root bindings and checkout execution provenance. It can inspect the exact pre-cutover registry schema through the existing strict artifact decoder solely for migration claims; unknown schema/spec identity remains a precondition. This does not enable old live control, mutate the old registry, or adopt its data. A9 is required to prove same-home coexistence.

## Outcomes & Retrospective

Completed on 2026-09-07 in the existing dirty checkout at baseline `c56e3e9`.
Ordinary `up` now owns local control/routing, optional observability and required
managed PostgreSQL per canonical app root. `down` preserves retained data;
destructive cleanup requires one explicit stopped worktree. Different current
protocols coexist on separate roots; same-root incompatibility fails without
replacement. The unused global PostgreSQL allocator/decoder, port-lease
allocator, watchdog and development shared-agent dispatch are removed. Explicit
machine edge/deploy remain operator capabilities. Object-storage cells and
external database URLs retain their declared sharing semantics.

All A1–A18 passed in the final integrated release, with verified cleanup of every
probe-owned cluster. A9 used the immutable pre-cutover baseline on a separate
nested Docker daemon and the same state home as the candidate. The original
agent/container/volume authority remained unchanged and its sibling served 246
requests without failure. The inert native clone preserved all fixture rows,
the exact seed ledger, owner, reader grant and pgcrypto extension before and
after startup. Archive SHA256:
`9460a969e700254f306d51438f02cd2d2adbc869ab91ae6f7ea5feb690757dd3`.
The [operator runbook](../runbooks/worktree-postgres-migration.md) contains the
verified commands, quiescence requirements and post-write rollback limits.

The developer's actual main database was **not cloned or switched**: its exact
application/root was not selected. No shared CLI installation, commit, push,
trust/DNS change, or unrelated runtime mutation occurred. A real clone requires
that explicit source selection; it is separate from completed implementation
and disposable migration acceptance.

### Validation results

Commands ran from the repository root unless stated otherwise. Raw final release
evidence is `.scenery/harness/worktree-runtime/final-release.json`: 47 passing
steps, 18 individually identified worktree rows and recorded subprocess argv.
The initial fresh timing invocation passed runtime/race checks but failed only
dynamic environment-name discovery in the probe. Explicit registered literals
fixed that check; the complete release rerun passed. The original is preserved
as `final-release-with-drift.json`, not relabeled as a passing aggregate.

| Validation | Result |
|---|---|
| Affected-package tests through the quick harness: root, `cmd/scenery`, `internal/{agent,build,compiler,devdash,doctor,evolution,generate,generate/api,postgresdb,redact,victoria,workspacetx}` | Passed. Exact package argv is recorded in the harness evidence. |
| `go test ./...`, `go test -race ./...`, `go vet ./...` | Passed, including final release/gate executions. |
| `golangci-lint run ./...` | Passed, zero issues. |
| `.scenery/harness/bin/scenery harness self --release --fresh-tests --summary --write` | Fresh timing and runtime/race checks passed; aggregate originally failed the subsequently fixed environment-discovery check. 169 selected roots, 3,380 isolated serial samples, maximum p95 94ms, zero 100ms violations. |
| `.scenery/harness/bin/scenery harness self --release --summary --write` | Passed all 47 steps; supersedes required default/quick runtime proof. |
| `scripts/release-gate.sh` with worktree-local `SCENERY_BIN` | Passed full Go/race/lint, UI/embed, private CLI build, default self-harness, source-snapshot build, fixture smoke, router safety and artifact hygiene. Logs: `.scenery/harness/worktree-runtime/release-gate/`. No `go install`. |
| `cd apps/console && bun run lint && bun run typecheck && bun run build` and `./scripts/build-dashboard-ui-embed.sh` | Passed; one 504.83KB bundle-size warning. |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json` and the same command for `internal/compiler/testdata/house` | Passed, no generated fixture drift. The assistant fixture was also regenerated successfully. |
| `bun test internal/generate/testdata/typescript_client_conformance.test.ts` | Passed 26 tests / 98 assertions. |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json` and `-p internal/generate/testdata/tsconfig.catalog.json` | Passed. |
| `.scenery/harness/bin/scenery harness ui --app-root <disposable-no-SQL-fixture> -o json --write` with its private state home | Passed six Chrome journeys, zero console/network errors; screenshot inspected. Root and results: `.scenery/harness/worktree-runtime/final-ui.json`. The unqualified command cannot discover an app at the Scenery repository root. |
| `.scenery/harness/bin/scenery logs --app-root .scenery/harness/worktree-runtime/basic-a --limit 500 -o jsonl` with its private state home | Passed, seven events and successful terminal summary. Matching ordinary `up`/`down` passed, no SQL allocation. Evidence: `final-logs.jsonl`, `logs-up.json`, `logs-down.json` in the worktree-runtime artifact directory. |
| `git diff --check` | Passed. |

Optional release-gate external-app smoke was skipped because no
`SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` was selected. It is not a substitute
for a real developer-app migration. The harness retains 43 documentation-review
and 27 architecture warnings (nine intersect changed paths), with no hard
failure. Earlier disposable SQL leftovers were removed through matching CLIs and
exact retained identities; private diagnostic/build artifacts remain.

### Measured worktree costs

Final A18 used three repetitions at each size with unchanged authored source
SHA256 `9ba53236528235e2e4ea549d572d46211e0fc7e4c7dc054c6dcfc30d0e08ba6f`.
Hardware: Mac14,14, 24 logical CPUs, 64GiB RAM, Darwin/arm64, Go 1.27.0;
Docker 29.4.0 on OrbStack exposes 24 CPUs / 58.8GiB. Each API-only lending
worktree had one supervisor, one app, three real Victoria processes and one
PostgreSQL container. Load requests: 28,913; failures: zero.

Cold means a fresh authored Git worktree, new cluster/generated artifacts and
fresh cohort-private Go build cache; module/toolchain/OS caches may be warm.
Warm means ordinary down/up at the same roots with retained data/cache. Ranges
span all repetitions: startup is per application, memory is a cohort aggregate.

| Worktrees | Cold serving (s) | Warm serving (s) | Native idle RSS (MiB) | Native load RSS (MiB) | PostgreSQL idle/load memory (MiB) |
|---|---:|---:|---:|---:|---:|
| 1 | 18.66–21.46 | 1.68–1.88 | 173–181 | 260–329 | 25.5–26.3 / 25.6–26.3 |
| 5 | 24.67–26.76 | 2.19–3.29 | 876–901 | 1,328–1,682 | 129.3–131.5 / 129.1–131.3 |
| 10 | 40.57–50.38 | 3.00–5.20 | 1,748–1,809 | 2,628–3,352 | 252.1–255.0 / 253.1–254.7 |

CPU percentages use **one core = 100%**, so cohort values can exceed 100%.

| Worktrees | Native idle/load CPU (%) | PostgreSQL idle/load CPU (%) | Load requests/s | PostgreSQL disk (MiB) | State + Victoria disk (MiB) | Cohort Go cache (MiB) |
|---|---:|---:|---:|---:|---:|---:|
| 1 | 0.3–3.9 / 33.1–51.1 | 0.1–1.0 / 0.7–1.9 | 42.7–43.5 | 69.9 | 2.7–2.8 | 232.4 |
| 5 | 5.1–28.0 / 292.5–311.6 | 1.0–4.0 / 3.3–6.3 | 213.9–214.9 | 349.3 | 14.6–14.9 | 297.0 |
| 10 | 17.4–73.9 / 450.7–518.9 | 4.6–7.2 / 9.7–11.8 | 425.1–431.3 | 697.7 | 29.3–29.7 | 377.7–378.1 |

Authored checkouts occupied 0.30 / 1.48 / 2.97MiB for 1/5/10 roots. Shared
toolchain/module caches are excluded. Native RSS counts shared pages per process;
Docker memory is container accounting, not host RSS. Do not add these figures to
VM memory or treat them as PSS. Background developer workloads were not stopped.
Short fixed idle/load windows are not a long-duration soak test. This fixture
has no frontend dev server and establishes no capacity ceiling or SLA.

## Context and Orientation

The developer's repository path is `/Users/petrbrazdil/Repos/scenery`. The original app/report is `/Users/petrbrazdil/Temp/scenery-library-desk.K8zIXY/README.md`. Treat these as provided locations, not permission to reset a dirty tree or operate the app's existing database. Read available app-local instructions and copy only authored inputs for disposable experiments.

Read `AGENTS.md`, `PLANS.md`, `ARCHITECTURE.md`, active plans, and the task-relevant current local-contract/agent-guide sections before implementation. Read owning child instructions for `internal/agent`, `internal/edge`, `internal/machine`, and any additionally changed compiler/generator/dashboard subtree. `docs/tech-debt.md` is context for avoiding known hazards, not authorization to absorb unrelated cleanup.

Important owners and starting points:

| Area | Current paths and responsibilities |
|---|---|
| Supervisor and startup | `cmd/scenery/watch.go`, `dev_supervisor.go`, `dev_build_pipeline.go`, `dev_session_controller.go`, `dev_detach.go`, `dev_detach_startup.go`, `cli_diagnostic_error.go` |
| Agent and process ownership | `internal/agent/server.go`, `client.go`, `paths.go`; process locks, owner fingerprints, registry/session state, launchd/systemd boundaries |
| PostgreSQL lifecycle | `cmd/scenery/dev_services_postgres.go`, `dev_named_lock.go`, `db_cli.go`, `postgres_dumptool.go`; `internal/postgresname`, `internal/postgresdb` |
| Other PostgreSQL consumers | Database setup/seed commands, snapshot paths, dashboard database inspection, durable workers |
| Routing and status | `cmd/scenery/dev_session_controller.go`, status/log/console handlers, dashboard control-plane handlers; `internal/agent`, `internal/localproxy` |
| Optional capabilities | Victoria supervision/recovery, storage-cell/proxy code, assistant and frontend supervisors, desktop lifecycle |
| Git and cleanup | `cmd/scenery/worktree.go`, down/prune command handlers; resolve actual files and callers in M0 |
| Validation | Existing release probes under `cmd/scenery/harness_self_*`, `harness_parallel.go`, `scripts/release-gate.sh`, `docs/schemas/` |

A durable resource record identifies what the worktree owns even when no process runs. A live owner record identifies a fingerprint-verified process currently authorized to manage it. A resource operation lock serializes creation, inspection-plus-mutation, restore, and deletion. An OS lock is not a database credential, and a matching container name or port is not any of these identities.

### Target ownership and lifecycle contract

Store durable records outside the checkout, under the resolved user-local Scenery state root, in a worktree-keyed directory such as `worktrees/<key>/`. Keep this a private layout, not a new configuration surface. Use existing explicit home injection for tests; ordinary users do not select homes to obtain isolation. Different local state stores must not accidentally address the same Docker objects: allocate a random resource-instance identifier once under the worktree lock and persist it before provisioning. Reuse that identifier on retry and container recreation with the retained volume.

The full binding includes normalized app root, configured app identity, local owner, resource-instance identifier, selected Docker daemon identity, container ID, volume identity, pinned engine image/major, and credential authority. Container/volume names can use the persisted opaque identifier. Labels contain nonsecret identity only. Match full recorded ownership and actual mounts; do not trust a truncated name, arbitrary labels alone, or a password-looking container environment.

Keep the application's existing root-derived database naming where possible to avoid an unrelated public naming change. Short-hash collisions must resolve by complete identity comparison and fail closed, not by sharing resources. Reject unsafe/symlinked state paths using the current filesystem rules. App-ID changes at an existing root require explicit reconciliation; they must not silently hide or replace its data.

| User action | Required behavior |
|---|---|
| Ordinary Git worktree creation | Source only; no database creation or server startup. |
| First `up` with managed SQL | One owned cluster, app database, declared schemas/setup, working localhost app routes. |
| `up` without any SQL consumer | No managed PostgreSQL provisioning merely for an unused dashboard feature. |
| Go/contract edit | Rebuild necessary app pieces; retain PostgreSQL instance and data. |
| Concurrent same-root `up` | One verified owner; the loser joins/reports it, or reports an incompatible owner without mutation. |
| Same root with a different selected environment | Do not report an already-running different environment as successful acquisition; require stopping the existing owner first. |
| `down` | Stop owned app/helpers/control/observability and managed PostgreSQL in dependency order; retain durable data and credentials. |
| `up` after `down` or crash | Reconcile and reuse verified resources; no fresh database just because a session disappeared. |
| Branch/commit switch at the same root | Same database; run existing forward setup rules or report incompatibility. No inferred rollback/reset. |
| Different worktree | Independent runtime, container, volume, credentials, database, endpoints, and shutdown authority. |
| Worktree removed with Git | Data retained and discoverable as orphaned; no implicit destructive cleanup. |
| Root reused with retained data | Make reuse explicit in status, verify complete identity, and never describe it as a fresh database. |
| Explicit external URL | Preserve URL semantics; report external, with managed isolation not provided. |
| Scenery/engine incompatibility | Typed precondition, retained data, no automatic empty replacement or destructive upgrade. |

Schema migrations and seeds remain the application's declared lifecycle. This plan does not promise backward-compatible migrations across branches or automatic data sanitization.

## Milestones

### M0 — Establish a safe baseline and close the ownership inventory

Record HEAD, dirty paths, available toolchains, and active-plan overlap. Register the plan with a fresh permanent number. Preserve completed plans 0163/0164 and the Go-generation decision. Read the original report when present without running that app.

Inventory every read/write consumer of agent state, session state, PostgreSQL credentials/container names, port leases, snapshots, storage, Victoria, and dashboard. Produce a concise table in `Artifacts and Notes`: current authority, target authority, call sites, and proof. An empty app root in a database-provisioning call is a blocking ownership gap, not a default to retain.

Before any real-process lane, review its setup and cleanup for installed-CLI writes, global container names, default-agent startup, trust/DNS changes, and broad Docker cleanup. Use a private filesystem root and uniquely identified probe objects. A private agent home is not a private Docker daemon. Run legacy global-name scenarios only on a truly disposable Docker daemon/dedicated test machine. If this is unavailable, record the blocked legacy lane; do not experiment on the developer's shared container.

Resolve two concrete implementation boundaries in this plan before coding: which existing agent handlers become embedded worktree services, and which machine/operator handlers remain intentional machine operations. This is a call-site mapping, not a new product decision or another plan. Preserve the selected worktree-owned architecture.

### M1 — Implement one durable worktree PostgreSQL owner

Use `internal/agent` for durable/process ownership primitives, `internal/postgresname` for deterministic naming, `internal/postgresdb` for database I/O, and `cmd/scenery` for orchestration. Do not put driver imports into `internal/app` or introduce a generic provisioning framework.

Implement a single resolver with explicit app identity and injected state/runtime dependencies. Separate read-only lookup from ensure/start/stop/delete operations so `ps`, `doctor`, and inspection do not provision infrastructure. Write the pending resource identity and credentials atomically before the first external mutation. Create/label the named volume explicitly, then create the container with the recorded image and loopback-only dynamic publication; inspect and persist actual IDs/mounts/endpoints.

Every retry uses the same pending identity. A name collision, wrong volume mount, different Docker daemon, unreadable record, credential mismatch, or unsupported engine must fail before mutation. Absence of a container with a verified existing volume permits recreation using retained credentials and image, not volume initialization with new credentials. Absence of previously recorded data is a lost-resource precondition; do not treat it as first use.

Verify ownership before updating a cached endpoint, then authenticate against the recorded cluster/application database. Readiness requires successful SQL access and declared setup, not just TCP or container state. Preserve existing SCN8003/SCN8004 classification and redaction. Never expose raw Docker output or DSNs containing credentials.

Prove all state transitions with injected Docker/database/process/filesystem seams. These tests perform no actual container or network work. This milestone alone is not the public cutover and does not fix shared-agent compatibility.

### M2 — Make the worktree supervisor own control and localhost routing

Acquire the worktree live-owner lock before provisioning anything. Resolve the exact worktree control socket and inspect only that owner for normal acquisition. The detached launcher starts the matching supervisor without first requiring `localagent.Ensure` against the machine-global agent. Retain the current inherited startup-result pipe and direct child-exit observation from 0163.

Host the required current agent control handlers, single-app registry, application dashboard, and local routing in the supervisor process. Reuse `internal/agent.Server`/router components, separating machine-edge/deploy startup from local serving. Avoid a new standalone per-worktree daemon and avoid separate JSON representations for local versus detached status. The supervisor may use the existing current client protocol over its private socket internally; the same binary owns both ends.

Inject explicit paths, listeners, and invocation-owned environment values. Remove process-wide temporary environment publication used to redirect unrelated consumers. Control sockets must remain short enough for supported Unix platforms and live in owner-only directories; no shared predictable `/tmp` socket is sufficient ownership. Reuse current bounded socket derivation and verify full identity.

For browser routes, preserve the app's localhost path-layout contract and configured port/range constraints. Hold bound listeners rather than reserving a port and releasing it before startup. Persist/reuse the preferred localhost endpoint when free; if an automatically assigned port is occupied, allocate and report the actual new endpoint. An explicitly requested fixed port fails instead of silently moving. PostgreSQL publication remains separate and internal.

One owner computes serving readiness from required capabilities, application listener, configured frontend readiness, and existing end-to-end route/asset probes. Foreground, detached output, status, and startup telemetry refer to that result. Preserve existing `--wait registered` behavior in this scoped change, but label it strictly as registration, never serving readiness; do not mix an unrelated command-removal migration into this work. Known failure must survive session cleanup and remain distinct from timeout.

Do not leave the old agent-backed/default-disabled fallback as a second development runtime path. Explicit machine `system agent`/edge/deploy commands may remain for their operator responsibilities, but normal local `up` does not create, replace, or require them.

### M3 — Cut over consumers and remove competing authority

Route `up`, `down`, `db`, snapshots, status/logs/console, application dashboard DB inspection, setup/seed execution, and explicit cleanup through the same worktree identity and PostgreSQL resolver. Pass the resolved database capability into application/setup children instead of re-deriving global state. Keep one app database with service schemas and the existing external-URL precedence.

Worktree-scoped `db server status|start|stop|logs` must resolve an app root rather than act on a machine-global default. Add `--app-root` support to that existing subcommand family if needed; do not add a new command family. Status/log reads do not start a server. Explicit start may provision a stopped worktree database without starting the app. Explicit stop refuses to undermine a different active owner; coordinated `down` stops the app first under the same lock.

Keep the meaning of app-database versus whole-resource deletion precise. `db reset`/`db drop` and `down --db` operate on the selected app database as documented, not arbitrary other databases on a cluster. Full orphaned container/volume removal belongs to explicit database cleanup through the existing prune surface. Destructive database pruning must resolve one exact worktree/resource selection, verify no active runtime/restore owner, and show the selected data scope. A missing app directory does not prevent resolving its retained durable record from an explicit absolute `--app-root`.

Use the existing explicit form `scenery prune --older-than <duration> --app-root <absolute-root> --db` for selected, eligible stopped/orphaned resource cleanup; its plan-specific release test uses `--older-than 1s` after establishing eligibility. Under this form, report the whole selected managed cluster/container/volume scope separately from app-only `db drop`. Refuse a live runtime or restore owner, even when an age threshold is satisfied. Do not expand an omitted app-root into deletion of every worktree.

Default prune only reports/removes eligible ephemeral records; it does not delete retained SQL data because leases expired. `--state` removes only disposable execution state, not the sole credentials/ownership record for a surviving volume. Retirement of the last durable record happens only after confirmed resource deletion. If deletion fails, retain recoverable intent and identity. No broad `docker prune`, name-prefix deletion, automatic age-based data destruction, or cross-daemon mutation.

Keep Git worktree commands thin. Remove the misleading `worktree remove --db` filesystem-state behavior rather than teaching the Git wrapper a second PostgreSQL lifecycle. Direct users to the exact existing data-deletion command. Git worktree removal requires a stopped owner but never implies permission to delete its persistent data; direct Git usage remains supported and orphan detection does not depend on the wrapper.

For `ps` without a root, reuse the current discovery/status surface to enumerate worktree owners read-only. Treat discovery as an index, not a lifecycle authority. Query compatible live owners; represent incompatible entries as inspectable limited/unknown state without decoding their mutation protocol or issuing signals. `ps`, logs, and down with an app root resolve that exact root. No process is replaced based on executable age.

Complete the auxiliary-capability audit described below before declaring the default path cut over. Delete old development shared-server constants/lookup paths/lease plumbing where no intentional operator consumer remains. Do not delete unrelated machine-edge ownership safeguards or tests.

### M4 — Prove the actual development workflow and its cost

Extend the existing release integration harness with a named `worktree runtime isolation` probe, using a focused owner such as `cmd/scenery/harness_self_worktree_runtime.go`. This is internal release evidence, not a new user smoke-test command. Reuse existing fixture-copy, child cleanup, HTTP/client, PostgreSQL, and snapshot helpers. Do not hide expensive subprocess tests in normal Go test roots.

Create a checked-in authored fixture under `testdata/apps/worktree-postgres/`, with app-local instructions and no committed generated application Go/cache/secrets. Reproduce the lending contract: create/list, borrow/return, typed 404/409, input validation, SQL injection, and atomic conditional updates. Prefer adapting the original source when available; otherwise author this self-contained fixture. Its report must distinguish the reconstructed fixture from the unavailable original experiment.

Create real Git worktrees in a disposable repository outside the Scenery source tree. Start them with normal candidate `up`, not a manually launched app binary or an externally supplied database. Test independent resources, real HTTP/generated TypeScript clients, ten two-borrower races per worktree, persistence, process restart, and failure isolation. Record actual package/CLI provenance and exact invocations.

Build two real candidate binaries with incompatible current specification/protocol identities, both containing this ownership design. Use successive candidate revisions or a deliberately recorded temporary spec-fixture revision with correctly recomputed identities. Merely changing the displayed version string is insufficient. Run A and B in different worktrees while a third sentinel continues serving; an incompatible same-root acquisition must fail without mutation. Separately test coexistence with the pre-cutover baseline in the disposable-daemon lane. Never make production code accept multiple current decoders to make the test pass.

Measure 1, 5, and 10 active SQL-backed worktrees. Use identical fixtures and fixed request/load/sample windows; record cold versus warm definitions, exact hardware/runtime versions, startup distribution, idle/load container memory and CPU, disk usage, and any Docker VM/host measurements separately. Show source measurements, not invented estimates or double-counted shared memory. Resource cost is reported for product review; no unsupported performance guarantee or alternative sharing mode is inferred. If capacity prevents a required level, report that level incomplete rather than silently reducing it.

### M5 — Deliver safe migration, current contracts, and release evidence

Provide an operator runbook for explicit migration from the old shared server, tested entirely on disposable source/destination resources. Inventory source app/root/database/engine, export and verify a faithful database backup, provision the target without starting application workers, restore, validate data, then perform the explicit switch. Final lossless cutover must quiesce writes for the selected application or provide an equally demonstrated capture of later writes. Other applications on the old shared server remain running.

Do not permanently retain the old shared-server runtime as a fallback in the new `up`. Same-current worktree transfers use existing Scenery snapshots. For a pre-cutover export whose Scenery snapshot identity is incompatible, use an explicit operator-run native PostgreSQL dump/restore with tools compatible with source and destination, rather than adding an old snapshot decoder or relabeling a signed/digest-bound archive as current. The old matching CLI may inspect/verify its own artifacts. The new binary never needs to understand the old control protocol. The runbook must give the exact verified export/import commands, archive checksum, selected database identities, and reviewed treatment of database ownership, grants, extensions, and setup ledgers; do not silently discard those semantics with blanket restore flags. Never decode arbitrary old state heuristically, rebind the global volume, or adopt credentials based on a container name.

Before any automatic fresh managed allocation at a root, require no evidence of an existing legacy runtime/data claim. M0 must identify the current durable provenance that can reveal such a claim. Ambiguous/unreadable legacy evidence produces a migration-required precondition without modifying the old state; absence of readable old state is not permission to call retained data nonexistent. Use the existing narrow durable identity/migration primitives for this guard, not a permanent old runtime decoder. The new workflow must not silently switch an existing user's app to an empty database.

Keep the old source data and export until the operator explicitly retires them. Rollback before target writes can switch back to the untouched source. After target writes, switching back loses those writes unless they are transferred; state this explicitly and do not advertise an automatic rollback.

Update normative source/runtime/protocol docs and checked schemas with the same change. Run the final validation matrix and focused proof, remove temporary candidate-only scaffolding, and record resource tradeoffs and any blocked evidence. Actual migration of the developer's shared machine remains outside execution scope.

## Plan of Work

### Keep the implementation singular and resumable

First implement deterministic identity/ownership operations behind explicit dependencies; then embed the owner/control path; then switch all callers. Keep each milestone testable, but do not ship a public half-cutover that offers two equal managed modes. Reuse the current diagnostic carrier, machine envelope, OS process locks/fingerprints, atomic durable writes, and lifecycle cleanup. No general resource broker, database pool, new package umbrella, or distributed lock service is justified.

Make every external mutation resumable. Persist intent before creating Docker objects, record returned immutable IDs, and inspect those objects after interrupted calls. A timeout may mean Docker created the resource; retry inspection before any second allocation. Authenticate against the retained database without regenerating secrets. Mark pending setup/restore separately from readiness and never advance a successful setup fingerprint after failure.

A stopped record is not a cache miss. A corrupt record, deleted credential file, wrong daemon, missing volume, inaccessible engine, or unsupported format must stop at an actionable precondition with original data intact. Distinguish a verified never-provisioned pending record from a record that previously held data.

### Control-plane and auxiliary boundaries

Normal development startup must preserve the application console, route manifest, frontend routing, structured logs, and capability status without needing the machine-global dashboard. Build the smallest worktree-hosted portion of existing handlers; do not remove these features and call the reduced process equivalent `up`.

Victoria is optional serving support. Scope its processes, mutable files, endpoints, and recovery to the worktree owner; reusable immutable downloads may remain shared cache. Start/recover it without delaying required application readiness. Unavailable observability is visible as degraded, not a silent success or startup failure. This is lifecycle refactoring, not replacement of its backend or a claim to have found the original experiment's Victoria cause.

Keep assistant/frontend/desktop children under the same supervisor with existing capability/approval semantics. Standalone operator `worker` behavior retains explicit runtime/database inputs; do not silently provision local infrastructure for production/headless workers. If a managed local child requests SQL, its requirement participates in the same pre-start capability plan.

Preserve intentional storage-cell sharing and tenant isolation. Scope transient proxy/control sockets to the worktree owner but do not relocate or split object data as collateral work. Status must identify explicitly shared/external capabilities so SQL isolation is not misrepresented as total data isolation.

Machine-wide domains/edge/deploy remain intentional features with existing supervision and privileged-helper contracts. Publish a worktree's routes through a compatible explicit edge integration using the existing current protocol; edge absence/incompatibility leaves the local development URL usable and reports domain unavailability. Do not install trust/DNS, restart launchd/systemd services, or silently claim a domain during ordinary local setup. Deploy/operator commands must still enforce their existing availability requirements and fail closed; local fallback is not proof of public deployment readiness. Do not change the privileged helper's frozen handoff merely to reuse it for worktree discovery. [R9]

### Data transfer and engine versions

Use existing `snapshot save`, `verify`, and `load` semantics. New worktrees start from declared setup/seed inputs, not copied sessions or databases. `pg_dump` supports a consistent database export, but it is not a transaction across separate object storage. Preserve that scope distinction. Do not implement transparent live cloning with `CREATE DATABASE ... TEMPLATE`: the source cannot have other active sessions. [R10][R11]

Restored data can contain queued jobs, schedules, authentication records, and provider credentials. Snapshot restoration stays faithful and leaves the app/worker stopped. Starting restored workers is an explicit subsequent action. Development-safe sanitization belongs to the app's declared importer/fixtures; Scenery must not guess which rows to remove. The release fixture uses inert local test data and no external side effects.

Persist the PostgreSQL image digest/major used for a resource. New worktrees use the currently bundled pinned image. Existing resources do not change engines on a Go edit or CLI replacement. Reuse compatible recorded images; unsupported requirements return an explicit migration diagnostic. Major engine upgrades need a validated export/restore or PostgreSQL-supported upgrade procedure, not a new image pointed at the old volume. Existing current-schema metadata can be migrated with narrow one-way, backup-preserving tooling; no failed decode creates new credentials or a fresh database. [R12]

### What to delete or consolidate

Remove machine-global PostgreSQL names from the ordinary development path, independent agent-home/server-state authorities for the same Docker object, per-session shared-PostgreSQL lease accounting no longer needed for worktree instances, startup fallback to global agents/manual default ports, and the worktree wrapper's misleading `--db` state deletion. Consolidate database resolution, endpoint reconciliation, startup readiness, and cleanup around their one owner.

Retain current request/error identities, exact live compatibility checks, non-destructive defaults, service schemas, external URL semantics, source generation decisions, strict snapshot validation, and operator edge process protections. Do not remove real cross-app operator features without an explicit replacement and acceptance proof.

## Concrete Steps

All repository commands below run from the designated Scenery implementation checkout. Use an existing assigned worktree or create an isolated implementation worktree with explicit developer approval if required. These instructions do not authorize `git reset --hard`, blanket `git clean`, `go install`, commit, push, shared-agent restart, or destructive shared-container commands.

Record baseline and locate actual plan numbers:

```sh
pwd -P
git rev-parse HEAD
git status --short
git ls-files 'docs/plans/[0-9][0-9][0-9][0-9]-*.md'
```

Read the supplied experiment only when its file exists; record the exact absence otherwise:

```sh
if test -f /Users/petrbrazdil/Temp/scenery-library-desk.K8zIXY/README.md; then
  cat /Users/petrbrazdil/Temp/scenery-library-desk.K8zIXY/README.md
else
  printf '%s\n' 'Original experiment report unavailable; use the authored release fixture and do not claim an original-app rerun.'
fi
```

Inventory current callers from the repository root:

```sh
rg -n 'ensureSharedPostgresServer|postgresServerStatePath|postgresServerContainer|postgresServerVolume' cmd internal
rg -n 'EnsureWith|localagent\.Ensure|commandAgentPaths|SCENERY_AGENT_HOME|SCENERY_AGENT_DISABLE' cmd/scenery internal/agent
rg -n 'postgresServer|DatabaseNameFor|DATABASE_URL|SCENERY_DATABASE_JSON' cmd/scenery internal/postgresdb internal/postgresname
rg -n 'DeleteOwnedSession|SubstrateLease|PortLease|startupReady|waitForStartupReady' cmd/scenery internal/agent
rg -n 'snapshot|storage.cell|storageCell|Victoria' ARCHITECTURE.md cmd/scenery internal/agent docs/local-contract.md
```

Treat search results as a starting inventory; inspect the actual current call paths before assigning ownership.

Provision the worktree-local embedded dashboard and candidate CLI before full validation:

```sh
./scripts/build-dashboard-ui-embed.sh
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
```

Read `.scenery/harness/agent-context.json`, record `changed_area.validation_classes`, and execute the exact union in `changed_area.recommended_commands` plus this plan's commands. Inspect setup/cleanup before running a lane that could reach global state; move it to the disposable test daemon rather than weakening its assertions.

Focused package validation, from the repository root:

```sh
go test ./internal/agent
go test ./internal/postgresdb
go test ./internal/localproxy
go test ./internal/doctor
go test ./internal/edge
go test ./cmd/scenery
go test ./...
golangci-lint run ./...
```

Run the plan-specific real-process probe through the existing release command after registering its named step:

```sh
.scenery/harness/bin/scenery harness self --release --summary --write
```

The probe must record the exact candidate path, app root, selected Docker endpoint, owned resource IDs, and redacted output for every command it runs. Its normal application operations include the following invocations, executed by the runner with absolute `$SCENERY` and `$APP` values pointing only to its candidate and disposable fixture:

```sh
"$SCENERY" generate --app-root "$APP" -o json
"$SCENERY" check --app-root "$APP" -o json
"$SCENERY" up --detach --app-root "$APP" -o json
"$SCENERY" ps --app-root "$APP" -o json
"$SCENERY" db list --app-root "$APP" -o json
"$SCENERY" logs --app-root "$APP" --limit 100 -o json
"$SCENERY" snapshot save --db --app-root "$APP" --output "$PROBE_ROOT/data.zip" -o json
"$SCENERY" snapshot verify --input "$PROBE_ROOT/data.zip" -o json
"$SCENERY" down --app-root "$APP" -o json
```

These shell variables are local runner variables, not added Scenery configuration knobs. The runner creates `$PROBE_ROOT` with `os.MkdirTemp`, creates Git worktrees itself, and refuses paths outside it for destructive fixture operations. Snapshot restore into a stopped disposable destination uses the existing explicit command:

```sh
"$SCENERY" snapshot load --db --app-root "$DEST_APP" --input "$PROBE_ROOT/data.zip" --mode overwrite --yes -o json
```

Managed database ownership must be established before restore. Provision it via the existing `db server start --app-root` family after its M3 cutover, not by starting application workers against partially restored data. Resolve all package imports and generated clients using the currently approved Go-generation workflow; never materialize/commit generated Go as a hidden acceptance workaround.

Final repository validation, after rebuilding the local binary from the final tree:

```sh
git diff --check
.scenery/harness/bin/scenery harness self --summary --write
.scenery/harness/bin/scenery harness self --release --summary --write
scripts/release-gate.sh
```

Release mode may supersede the separate default/quick lanes according to the root matrix; the quick refresh remains useful for discovering the changed-area union. Record actual executions and supersession rather than claiming unexecuted commands passed.

## Validation and Acceptance

Expected root validation classes are `go-package`, `cli-json-contract`, and `release-sensitive-or-runtime`; dashboard changes and any compiler/generator changes add their respective cumulative commands. Nearest child instructions also apply. Compilation alone is not sufficient.

### Required deterministic tests

Use injected dependencies for ownership transitions, pending intent/retry, full identity mismatches, missing records/volumes, wrong daemon, port changes, redaction, state migration rejection, and cleanup decisions. Test changed environment/same-root acquisition, exact current identity decoding, and no shared-agent calls from ordinary local startup.

Retain detached pipe/exit/error ordering tests from 0163 and scope/redaction tests from 0164. Assert that a port mismatch is reconciled only for a fully verified owned resource; all unverified conflicts stay non-mutating. Test that a container missing after successful provisioning does not imply a missing volume is disposable.

Every exact top-level Go test root must satisfy the repository's repeated isolated p95 below 100ms rule. Keep subprocesses, real sockets, Docker, compiler invocations, and timed service proof in release integration. Use the existing fresh-test/timing lane for measurements and record per-root results; do not create exceptions or hide work in `TestMain`.

### Required real-process acceptance matrix

Every row below runs under the named release probe invoked by `.scenery/harness/bin/scenery harness self --release --summary --write`, from the repository root. The runner's output must identify each row separately and include its actual subprocess argv. Do not infer multiple results from one generic `ok`.

| ID | Scenario and required evidence |
|---|---|
| A1 | Fresh Git worktree, managed SQL, no `.env`, no pre-created generated Go, no external DSN, no user-selected agent/router addresses. `up` returns serving readiness and the printed URL serves the fixture. |
| A2 | Non-SQL basic app starts without provisioning PostgreSQL, including opening its ordinary console. A deliberately invoked SQL-backed auxiliary feature reports/provisions its own required capability explicitly. |
| A3 | Two real worktrees have distinct full ownership records, container IDs, volumes, credentials, app databases, and control/browser/database endpoints. Secrets are compared privately and never emitted. |
| A4 | Ten two-borrower races per worktree produce exactly one successful borrow and one typed conflict each; typed 404, return/reborrow, input validation, and generated TypeScript fetch-client behavior pass. |
| A5 | Inject an outage by stopping/restarting only A's fingerprint/ID-verified probe container through the test runner while B receives continuous requests. B has zero failed sentinel requests in the recorded window and unchanged supervisor/container/volume identities. Global resources are not signaled or mutated. |
| A6 | App rebuild, `down`/`up`, supervisor failure/recovery, and PostgreSQL restart preserve exact fixture records and retained resource identity. Record endpoint reconciliation independently of data identity. |
| A7 | Concurrent same-root launches yield one owner and one cluster. A different selected environment is not mistaken for idempotent acquisition. Same-root incompatible CLI access fails without restarting or rewriting the owner. |
| A8 | Distinct real candidate spec/protocol identities run simultaneously on different roots; the sentinel keeps serving. Record actual revision digests, binary provenance, uninterrupted request counts, and resource ownership. |
| A9 | A real pre-cutover shared-agent/server sentinel coexists without mutation on a disposable daemon/dedicated machine. New ordinary `up` does not use or repair it. Never run its global container names on the developer's shared daemon. |
| A10 | Restart/container-recreation tests verify published loopback endpoints and retained volumes. Inject unrelated name/mount/daemon/credential/state conflicts and prove no adopt, reset, deletion, or new credentials. |
| A11 | Process interruption after pending-state write, volume creation, container creation, endpoint persistence, and during restore is recoverable. Retry uses the same intent and never publishes incomplete setup as ready. |
| A12 | Removing a stopped worktree through ordinary Git leaves data discoverable. Explicit orphan cleanup using the retained exact root/resource deletes only selected inactive data; a failed cleanup retains enough state to retry. |
| A13 | Two explicit equal external DSNs remain external/shared; inspection states managed isolation is not provided. Managed drop/reset/prune refuse external targets. |
| A14 | Inert snapshot export/verify/restore into another stopped worktree preserves expected rows and does not start workers or sanitize data silently. Failed restore cannot be reported as ready. |
| A15 | Optional Victoria failure/recovery does not delay required serving readiness and is visible as degraded. No optional dashboard path accidentally provisions the global PostgreSQL server. |
| A16 | Compatible restart/upgrade retains data. Unsupported engine or durable-record compatibility produces an actionable precondition and leaves data intact. It never silently allocates a fresh database. |
| A17 | Developer/operator route separation is verified: local localhost works without edge; explicit compatible domain/deploy flow preserves exposure and ownership checks; unavailable public edge is not reported as public readiness. |
| A18 | 1/5/10-worktree cost report includes cold/warm startup, repeated samples, steady idle/load resource measurements, storage, hardware/daemon details, and stated limitations. No invented resource ceiling. |

For A6/A10, include both a normal engine restart and a controlled recreation of only a probe-owned container on the same retained volume. Do not claim Docker necessarily changes an explicitly assigned port on every restart. Deliberately exercise a changed dynamically published endpoint rather than relying on chance.

For A9, build the old baseline in a temporary source copy and execute against a disposable Docker endpoint selected at the test runner boundary. A separate `SCENERY_AGENT_HOME` alone is insufficient. No old source tree, user installation, or shared database is modified. A8 is not a substitute for A9, and a forged display version is not a substitute for either.

### Additional exact validation commands and conditions

If any dashboard source, dashboard API contract, or embedded console behavior changes, run from `apps/console`:

```sh
bun run lint
bun run typecheck
bun run build
```

Then rebuild the embedded UI/local CLI from the repository root and run:

```sh
.scenery/harness/bin/scenery harness ui -o json --write
```

Route that browser lane to the disposable runtime through its existing supported options or test injection; it must not attach to a developer application. These commands may be omitted only when the recorded final diff changes no dashboard source/API/console behavior and the changed-area union selects no dashboard class. Local-control relocation is expected to require console acceptance even without visual changes.

If the final diff touches `internal/compiler` or `internal/generate`, run from the repository root:

```sh
go test ./internal/compiler ./internal/parse ./internal/generate
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Also execute every owning child verification command, including its additional client fixture/typechecks. Omit these compiler/generator additions only when neither subtree is changed and no matching validation class is selected; record that condition. Existing committed TypeScript/golden fixtures are not the application's ignored generated Go and keep their current repository policy.

A missing original experiment directory permits only skipping a literal rerun of that original app; A1–A18 still require the authored fixture. A missing Docker daemon, inability to create disposable legacy isolation, unavailable toolchain, insufficient capacity for a requested cost level, or absent browser tooling blocks the corresponding mandatory row. Record the exact command/error and keep acceptance open. Do not count a substitute standalone binary, external DSN, or mock as managed runtime proof.

The release gate's unrelated optional external-app lane may remain skipped only when its existing `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` is unset. Record that explicit skip; this does not waive this plan's mandatory disposable-app tests. Do not point that lane at the original developer app without authorization.

## Idempotence and Recovery

Creation is serialized under the worktree operation lock. Persist pending identity and credentials first, reconcile partially created resources next, and publish ready state last. Record enough exact object identity to resume after a timeout whose external outcome is unknown. Cleanup must not delete a resource another concurrent command successfully acquired.

Retain full process fingerprints and exact Docker object/volume identity for every destructive action. The active supervisor holds its live lock for its lifetime; database CLI operations acquire the appropriate serialized operation lock rather than inventing a second long-lived owner. Stale record cleanup does not authorize signals to a PID whose fingerprint no longer matches.

On graceful `down`, stop app/worker/assistant/frontend children before PostgreSQL; stop local control services after their cleanup/status work completes. Detached parent failure targets only its child and its newly owned disposable execution state. Supervisor death may leave its database container running; it remains a recoverable worktree-owned resource, not grounds for global cleanup or fresh allocation. Do not introduce automatic idle suspension or a machine-wide reaper to solve this first iteration.

Restore and destructive cleanup require a stopped app plus an exclusive operation lock. Failed restore leaves the app unavailable and retains the verified archive and recovery state. Failed resource deletion retains the durable record until absence is confirmed; deleting the record first can orphan data and lose credentials. Rollback procedures distinguish restoring a stopped resource from discarding writes made after a cutover.

The current Docker daemon identity is bound before I/O. Changing Docker context or reconnecting a new daemon at the same socket must not turn absent old objects into permission to provision replacement data. Report the mismatch without exposing credentials. Remote Docker endpoints that cannot satisfy host-loopback application access fail with an explicit precondition; remote Docker networking support is not added here.

A moved/copy-with-local-state checkout must not claim another root's resources. New-root data transfer is explicit. Shared-volume mounts, missing evidence, corrupt records, or unsupported artifact identities stop without mutation. User-local state is owner-only, never committed, and never printed raw.

Implementation and release cleanup use an explicit manifest of probe-owned paths, process fingerprints, Docker daemon/object IDs, and volumes created by the probe. Do not enumerate-and-delete by prefix, run global prune, install over the user's CLI, or stop an existing machine agent. Preserve failure evidence until inspected; any remaining probe resources must be named in the handoff with a narrowly scoped cleanup procedure.

## Artifacts and Notes

Keep machine-local evidence under `.scenery/harness/worktree-runtime/` and the existing release-gate directory. Record exact schemas for any new evidence fields, or use existing harness artifact structures. Never check in credentials, generated application Go, runtime sockets/state, database dumps, container output containing secrets, or benchmark cache data.

Required evidence consists of the baseline/caller-ownership inventory, redacted per-scenario command results, fixture HTTP/client assertions, resource identity/lifecycle observations, sentinel request continuity, crash/restore recovery results, mixed-version binary identities, 1/5/10 cost measurements, tested migration runbook results, and complete validation/skips. Store secret equality tests as boolean assertions, not hashes that unnecessarily become durable correlators.

### Completed caller-ownership inventory

| Consumer | Previous authority | Current authority and owning call sites | Proof |
|---|---|---|---|
| Ordinary `up`, detached acquisition and restart | Machine agent/control and session leases | `acquireWorktreeRuntime`, `DevSessionController.Prepare`, exact root live lock, embedded control/router | A1/A2/A3/A7/A8 |
| Managed SQL provisioning and readiness | Agent-home credentials, global container/volume names | `worktreePostgresResolver`, retained full root/daemon/resource/credential binding; authenticated observation | A1/A3/A6/A10/A11/A16 |
| SQL endpoint change | Cached shared endpoint | Observation-only `worktreePostgresMonitor`; existing rebuild request channel | A5/A6/A10 |
| Apply, seed, setup | Per-call provisioning and process environment | `beginDatabaseLifecycleEnv` owns the whole operation; resolved child environment; dry-run observes only | Focused CLI tests; A1/A6 |
| DB list/shell/server status/logs and doctor | Global server state | `resolvePostgresDatabaseFromEnv`, `runWorktreeDBServer`, exact root observation, no implicit ensure | A2/A10/A13/A16 and focused tests |
| Dashboard SQL and trace mutation | Global dashboard/default capability | Worktree dashboard resolver and verified `commandWorktreeClient` | A2/A15/A17 |
| `down`, app database reset/drop | Agent session/shared substrate leases | Exact root live/operation locks, verified process/resource ownership; app-only SQL deletion semantics | A5/A6/A13 |
| Snapshots | Shared/default server and live session lookup | `worktree_snapshot.go`, inert worktree restore intent, private retained archive, exact Docker target | A11/A14; A9 native migration passed |
| `ps`, logs, retained/orphan discovery | Mutable global session/substrate inspection | `inspectWorktreeOwners`, strict current root health, read-only durable discovery/index | A8/A12/A16 |
| Prune and Git worktree removal | Shared lease age and checkout `.scenery` deletion | `worktree_prune.go` exact retained cluster selection; Git wrapper only owns Git/stopped-root checks | A12; external refusal A13 |
| Victoria | Machine dashboard/shared mutable lifecycle | Worktree owner-local component records, processes, endpoints and restart loop | A2/A15/A18 |
| Storage cells | Machine-home object data and proxy | Object directories intentionally remain shared; live proxy/control endpoints are owner-local | Existing release storage probe; documented shared-data boundary |
| Explicit domain edge/deploy | Machine operator control | Machine service remains intentional; optional loopback worktree proxy lease publishes API only | A17 |
| Legacy provenance | Old registry/session/server authority | Read-only root-scoped migration guard; no credential adoption or old live-control fallback | Focused agent tests; same-home A9 passed |

The final handoff must state whether ordinary `up` actually completed through managed PostgreSQL; whether mixed-version worktrees and a pre-cutover sentinel were proven; which default shared mechanisms were removed; whether object storage/external capabilities are intentionally shared; measured resource costs; and the exact commands an operator must deliberately run for later migration. Do not imply the developer's existing database was migrated.

### Source references

Repository references below are pinned to the inspected baseline. Local relative files are the execution authority; recheck them when HEAD differs. External references explain the constraints, not hidden prerequisites for understanding this plan.

- [R1] PostgreSQL lifecycle: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/cmd/scenery/dev_services_postgres.go
- [R2] Database/schema naming: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/internal/postgresname/name.go
- [R3] Session preparation: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/cmd/scenery/dev_session_controller.go
- [R4] Agent server lifecycle: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/internal/agent/server.go
- [R6] Git wrapper: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/cmd/scenery/worktree.go
- [R7] Completed detached startup correction: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/docs/plans/0163-detached-startup-diagnostics.md
- [R8] Completed diagnostic/safe recovery correction: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/docs/plans/0164-truthful-runtime-diagnostics.md
- [R9] Agent ownership and privileged-helper contracts: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/internal/agent/AGENTS.md
- [R10] PostgreSQL logical export: https://www.postgresql.org/docs/18/app-pgdump.html
- [R11] PostgreSQL template restrictions: https://www.postgresql.org/docs/18/manage-ag-templatedbs.html
- [R12] PostgreSQL engine upgrades: https://www.postgresql.org/docs/18/upgrading.html
- [R13] Docker persistent volumes: https://docs.docker.com/engine/storage/volumes/
- [R14] Docker publication/runtime options: https://docs.docker.com/engine/containers/run/
- [R15] Repository ExecPlan format: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/PLANS.md

## Interfaces and Dependencies

### M0 resolved implementation boundaries

The current caller audit is complete. Normal development will embed `agent.Server`
registration, session updates, status, logs, route manifests, local proxying, and
dashboard control in `devSupervisor`; its constructor must explicitly omit machine
edge/deploy restoration. `dev_session_controller.go` will use that private client.
Machine `system agent`, privileged edge, deploy target restoration, launchd, and
systemd remain explicit operator paths, not prerequisites of local startup.

Database authority will be resolved once from the normalized app root in
`dev_services_postgres.go`, then carried to setup/seed, dashboard inspection,
`db_cli.go`, and `postgres_dumptool.go`. Snapshot restore and explicit prune must
hold the worktree operation lock, independently of the supervisor lifetime lock.
`agent.go`, logs, observability queries, devdash storage, and snapshot live checks
must stop selecting the global socket. Read-only status currently deletes stale
substrate entries; that mutation will be removed from inspection.

Victoria mutable runtime state and storage proxy sockets become owner-local.
Storage-cell object directories intentionally remain in their existing shared
home; passing a private agent home through that resolver would incorrectly split
object data. The Git wrapper remains a stopped-owner check plus Git execution.
Legacy claims include checkout `.scenery` session artifacts and the existing
machine registry; incompatible or unreadable claim evidence blocks fresh SQL
allocation rather than authorizing a new empty database.

Baseline tools are available (Go 1.27.0, Docker 29.4.0 via local OrbStack).
Disposable resources owned by this plan have been created and removed through
verified identities. The developer's global database and installed CLI remain
untouched. A9 now proves legacy-global coexistence on a separate nested daemon.

Use standard Go libraries and current repository packages by default. Keep ownership/state identity in `internal/agent`, deterministic database/schema derivation in `internal/postgresname`, database I/O in `internal/postgresdb`, and orchestration in `cmd/scenery`. Reuse `internal/localproxy`, `internal/machine`, existing compiler graph/capability declarations, artifact transactions, diagnostics, and the release harness. No new service or public abstraction layer is required.

The narrow internal operations must accept explicit worktree identity and dependencies: resolve durable state without mutation; ensure the verified managed resource; observe actual endpoint/readiness; stop the resource; perform explicitly authorized deletion. Carry one resolved database value to consumers instead of having each consumer rediscover the agent home. Whether these are methods or functions is an implementation detail; do not introduce a public provider interface only for this refactor.

Persistent state and machine output use current unversioned artifact kinds, exact schema/spec digests, and producer identity. Add only necessary typed fields to existing status/result payloads: ownership scope, managed/external/shared classification, serving versus registration state, optional degraded capabilities, retained-data reuse, and safe recovery context. Update schemas, help, JSON consumers, and tests together; no alias fields, alternate legacy decoders, or extra protocol versions.

The single live protocol remains exact. Do not confuse a safe read-only indication that an incompatible owner exists with permission to parse its control state or mutate it. Old durable data is preserved through explicit verified migration or a clear blocked precondition; compatibility failure cannot lead to a new empty managed database under the same apparent application.

Current `.scn`, `.scenery.json`, `DATABASE_URL`, service capability injection, declared setup/seeds, and snapshot surfaces remain the developer-facing inputs. Update `AGENTS.md` and applicable children, `ARCHITECTURE.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, `README.md`, the app cookbook, affected checked schemas/specification, and `docs/knowledge.json` in the cutover. Update the environment registry only for changed existing runtime outputs or removed controls; add no user environment knobs. Preserve the independent in-module, Git-ignored generated-Go policy.
