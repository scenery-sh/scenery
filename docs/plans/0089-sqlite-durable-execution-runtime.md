# 0089 - SQLite Durable Execution Runtime Per Service

> Current contract note (2026-07-06): 0097 supersedes this plan's storage model. Durable execution now uses the app Postgres database's `scenery` schema and a single shared store; the current contract lives in `0097-postgres-only-data-platform.md` and `docs/local-contract.md`.

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Replace the current legacy async runtime-backed worker and durable-execution path with a Scenery-owned durable execution runtime backed by one separate SQLite database per service that declares durable work.

The target model is deliberately smaller than legacy async runtime: durable enqueue, worker leasing, retry, timeouts, status, events, schedules, remote workers, inspection, idempotent starts, and optional durable step results. It intentionally does not add NATS, Redis, Kafka, distributed SQLite, direct remote SQLite access, or legacy async runtime-compatible deterministic replay.

The core invariant is:

```text
A job exists because a row exists in the service's durable SQLite database, not because a broker message exists.
```

Durable database files must be named `<service-name>.durable.sqlite`, must live alongside the service's normal SQLite database state, and must not use table names containing `scenery`. Remote workers must use authenticated HTTPS long-polling against the owning runtime; they never open SQLite directly.

## Progress

* [x] 2026-06-27: Created this ExecPlan from the supplied durable-runtime draft, linked it from `docs/plans/active.md`, and indexed it in `docs/knowledge.json`.
* [x] 2026-06-27: Re-ran the current legacy async runtime, worker, cron, service database, dashboard, and CLI inventory before implementation began.
* [x] 2026-06-27: Added the first durable SQLite store foundation slice: service-name path normalization, `<service>.durable.sqlite` resolution, SQLite open/migrate pragmas, table-name invariant tests, task reconciliation, and idempotent job start.
* [x] 2026-06-27: Added the initial public `scenery.sh/durable` typed task declaration API and current-contract docs noting that runtime execution remains pending.
* [x] 2026-06-27: Slice validation passed with `go test ./durable ./internal/durable/store`, `go test ./...`, `go run ./cmd/scenery inspect docs --json`, and `git diff --check`.
* [x] 2026-06-27: Wired durable task declarations into parser/model/codegen import registration: `durable.NewTask` is discovered as `durable_task`, static literal configs require `TaskConfig.Service`, and generated main imports durable declaration packages. Focused validation passed with `go test ./internal/parse ./internal/codegen ./durable ./internal/durable/store`.
* [x] 2026-06-27: Full validation after parser/model/codegen wiring passed with `go test ./...`, `go run ./cmd/scenery inspect docs --json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`; self-harness status was `pass_with_warnings` for existing large-file and slow-test warnings.
* [x] 2026-06-27: Wired `durable.NewTask` into runtime registration and startup DB reconciliation. Runtime startup now opens one `<app-root>/.scenery/state/db/<service>.durable.sqlite` per declaring service and reconciles task rows; worker execution remains pending. Focused validation passed with `go test ./durable ./runtime ./internal/parse ./internal/codegen ./internal/durable/store`, `python3 -m json.tool docs/knowledge.json`, and `git diff --check`.
* [x] 2026-06-27: Fixed `scenery test` generated-workspace execution to retry `go mod tidy` when `go test` reports stale module metadata after durable dependencies changed the app graph. Regression validation passed with `go test ./cmd/scenery -run TestSceneryTestRunsGoTestInGeneratedWorkspace` and `go test ./...`.
* [x] 2026-06-27: Full validation after runtime registration and generated-workspace tidy retry passed with `go test ./...`, `python3 -m json.tool docs/knowledge.json`, `go run ./cmd/scenery inspect docs --json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`; self-harness status was `pass_with_warnings` for existing large-file and slow-test warnings.
* [x] 2026-06-27: Added `durable.Start` and runtime queued-job enqueue against the active service durable store, with generated job IDs and optional dedupe keys. Focused validation passed with `go test ./durable ./runtime ./internal/durable/store`.
* [x] 2026-06-27: Added first local durable worker execution for generated `all` and `worker` roles: workers lease queued jobs from the service durable DB, run the registered Go handler with JSON input, and mark jobs succeeded or failed. Focused validation passed with `go test ./internal/durable/store ./durable ./runtime`.
* [x] 2026-06-27: Full validation after local durable execution passed with `go test ./...`, `python3 -m json.tool docs/knowledge.json`, `go run ./cmd/scenery inspect docs --json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`; self-harness status was `pass_with_warnings` for existing large-file and slow-test warnings.
* [x] 2026-06-27: Added retry scheduling for failed local durable attempts. Failed jobs requeue until `max_attempts` using the reconciled retry policy, then become final `failed`. Focused validation passed with `go test ./internal/durable/store ./durable ./runtime`.
* [x] 2026-06-27: Full validation after retry scheduling passed with `go test ./...`, `python3 -m json.tool docs/knowledge.json`, `go run ./cmd/scenery inspect docs --json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`; self-harness status was `pass_with_warnings` for existing large-file and slow-test warnings.
* [x] 2026-06-27: Added `scenery inspect durable --json` with `scenery.inspect.durable.v1` schema output for durable declarations, service names, durable DB paths, and DB existence. The parser now accepts apps whose only Scenery surface is a runtime declaration. Focused validation passed with `go test ./internal/parse -run 'TestParseRejectsAppsWithoutSceneryDirectives|TestParseRejectsNonSceneryDirectives|TestRuntime'`, `go test ./cmd/scenery -run 'TestRunSceneryInspectDurable|TestParseInspectArgs'`, and `go test ./cmd/scenery`.
* [x] 2026-06-27: Full validation after durable inspect passed with `go test ./...`, `go run ./cmd/scenery inspect docs --json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`; self-harness status was `pass_with_warnings` for existing large-file and slow-test warnings.
* [x] 2026-06-27: Added authenticated durable worker HTTP endpoints for lease, heartbeat, complete, and fail under `/__scenery/durable/v1/<service>/...`. Worker tokens are stored as hashes in the service durable DB; leased-job heartbeat/complete/fail is fenced by `worker_id` and `lease_id`; focused validation passed with `go test ./internal/durable/store ./runtime`.
* [x] 2026-06-27: Added `scenery worker durable token create --service <name> --json`, which creates or rotates a service durable worker bearer token, stores only the hash in `<service>.durable.sqlite`, and prints the raw secret once in `scenery.durable.worker_token.create.v1`. Focused validation passed with `go test ./cmd/scenery -run 'TestWorkerDurableTokenCreate|TestCommandHelpAndJSONManifest|TestCanonicalCommandParsers'`.
* [x] 2026-06-27: Added `scenery worker durable --endpoint <url> --token <token>` long-running remote handler execution. The CLI starts the generated app binary with durable remote worker env, the runtime polls remote lease endpoints, executes registered Go handlers, heartbeats once per lease, and completes or fails over HTTP. Focused validation passed with `go test ./cmd/scenery ./runtime ./internal/durable/store -run 'TestParseWorkerDurableArgs|TestWorkerDurableTokenCreate|TestCommandHelpAndJSONManifest|TestDurableRemoteWorkerExecutesJobOverHTTP|TestDurableWorkerHTTPLeaseHeartbeatAndComplete|TestWorkerTokenAndLeasedJobFencing'`.
* [x] 2026-06-27: Added job-admin store and CLI surfaces: `scenery worker durable jobs list|inspect|cancel|retry ... --json` emits `scenery.durable.jobs.v1`, inspect includes job events, and retry/cancel append durable events. Focused validation passed with `go test ./cmd/scenery ./internal/durable/store -run 'TestWorkerDurableTokenCreate|TestParseWorkerDurableArgs|TestJobAdminListEventsCancelAndRetry|TestCommandHelpAndJSONManifest'`.
* [x] 2026-06-27: Added durable step and signal primitives. `durable.Step` persists/reuses local handler step results by job/key and falls back to direct execution outside a durable job context; `durable.Signal` appends JSON signal rows and events for a run. Focused validation passed with `go test ./durable ./runtime ./internal/durable/store -run 'TestStepRunsWithoutDurableJobContext|TestStepsAndSignals|TestDurableLocalWorkerExecutesQueuedJob'`.
* [x] 2026-06-27: Added interval schedule primitives. `durable.Schedule` records schedules in the service durable DB, API/all roles materialize due schedules into queued jobs, and existing local or remote workers execute them. Focused validation passed with `go test ./durable ./runtime ./internal/durable/store -run 'TestStepRunsWithoutDurableJobContext|TestStepsAndSignals|TestRunDueSchedulesStartsJobs|TestDurableScheduleEnqueuesAndRuns'`.
* [x] 2026-06-27: Updated public docs, environment registry, JSON schemas, CLI help manifests, and legacy async runtime migration guidance for the durable runtime surface.
* [x] 2026-06-27: Final validation passed with `go test ./...`, `go run ./cmd/scenery inspect docs --json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`; self-harness status was `pass_with_warnings` for existing large-file and slow-test warnings.

Update this section at every meaningful stopping point. Every update must include the date, what changed, and whether validation was run.

## Surprises & Discoveries

* `go run ./cmd/scenery inspect docs --json` reported `review_due_count: 0`, `stale_count: 0`, and one AGENTS scope before this plan was created.
* The supplied draft is intentionally explicit that no NATS dependency should be added, remote workers should long-poll over HTTPS, table names in durable SQLite databases must not contain `scenery`, and the durable database filename must contain the service name.
* The repo had no SQLite driver dependency before this slice. The store uses `modernc.org/sqlite` through `database/sql`, keeping the rest of the durable store driver-neutral.
* Runtime declarations were already resource-bearing for codegen, but `parse.App` still rejected an app with no `//scenery:` directives. Durable inspect exposed that mismatch, so runtime declarations now satisfy the "app has Scenery content" check.
* The store schema's job state is `succeeded`, while lease state is `completed`. Remote completion now maps the successful job state to the lease vocabulary when releasing a lease.

Add new surprises here with the command, test, or file that exposed them.

## Decision Log

Decision: Create this as `0089-sqlite-durable-execution-runtime.md`.

Rationale: `0088` is already allocated to SQLite service databases and Postgres removal; this plan is the next historical sequence ID and covers durable execution on top of that service database model.

Date/Author: 2026-06-27 / Codex.

Decision: Use `modernc.org/sqlite` for the first durable store slice while keeping store code on `database/sql`.

Rationale: Go has no standard-library SQLite driver. A pure-Go driver avoids CGO setup while letting the store keep ordinary `database/sql` boundaries.

Date/Author: 2026-06-27 / Codex.

Decision: Keep the pasted draft as a detailed source section inside this ExecPlan instead of replacing it with a short summary.

Rationale: The draft contains concrete schema, protocol, CLI, validation, and PR breakdown details that future agents need to execute the work without chat history.

Date/Author: 2026-06-27 / Codex.

Decision: Do not update `AGENTS.md`, `SKILL.md`, `docs/local-contract.md`, or schemas while only creating the plan.

Rationale: The current behavior has not changed yet. Those files should be updated in the implementation PRs that change durable runtime contracts.

Date/Author: 2026-06-27 / Codex.

Decision: Keep `scenery inspect durable --json` as declaration and service-DB metadata for this slice.

Rationale: Job listing, retry, cancel, repair, leases, worker tokens, schedules, steps, and signals need store/protocol behavior that is not complete yet. The first inspect surface gives agents stable discovery of durable task declarations and DB files without inventing admin semantics early.

Date/Author: 2026-06-27 / Codex.

Decision: Use bearer-token auth plus `worker_id` and `lease_id` fencing for the first remote durable worker endpoints.

Rationale: The durable DB already owns token and lease tables. Hashing the bearer token in the service durable DB and requiring the active lease identity on heartbeat/complete/fail gives the protocol stale-completion protection without adding a separate broker or remote SQLite access.

Date/Author: 2026-06-27 / Codex.

## Outcomes & Retrospective

Implemented on 2026-06-27.

Shipped behavior:

* `scenery.sh/durable` has typed task declarations, `durable.Start`, `durable.Schedule`, `durable.Step`, and `durable.Signal`.
* Durable declarations reconcile into one SQLite DB per service at `<app-root>/.scenery/state/db/<service>.durable.sqlite`.
* Durable DB table names do not contain `scenery`.
* Local `all` and `worker` roles lease queued jobs, execute registered Go handlers, complete successes, fail or retry errors, and honor task retry policy up to `MaxAttempts`.
* API/all roles materialize due interval schedules into queued jobs.
* Remote workers use authenticated HTTP endpoints under `/__scenery/durable/v1/<service>/...` for lease, heartbeat, complete, and fail. Bearer tokens are stored only as hashes in the service durable DB, and heartbeat/complete/fail are fenced by `worker_id` plus `lease_id`.
* `scenery worker durable --endpoint <url> --token <token>` runs generated app handlers as a remote durable worker.
* `scenery worker durable token create --service <name> --json` creates remote worker tokens and prints the raw secret once.
* `scenery worker durable jobs list|inspect|cancel|retry ... --json` provides job admin and event inspection through `scenery.durable.jobs.v1`.
* `scenery inspect durable --json` reports declarations, services, durable DB paths, and DB existence through `scenery.inspect.durable.v1`.

legacy async runtime migration note: this introduces the replacement durable path but does not remove existing legacy async runtime workflows, cron, or worker deployment commands. Existing legacy async runtime-based apps can migrate durable work by declaring `durable.NewTask`, using deterministic `durable.Start` IDs or dedupe keys, and replacing legacy async runtime schedules with `durable.Schedule` where interval scheduling is sufficient.

Final validation is recorded in Progress.

## Context and Orientation

Read these files before implementation:

```text
AGENTS.md
PLANS.md
docs/plans/active.md
docs/plans/0047-typescript-legacy-async-runtime-workers.md
docs/plans/0035-legacy-async-runtime-worker-production-hardening.md
docs/local-contract.md
docs/agent-guide.md
docs/app-development-cookbook.md
docs/tech-debt.md
DSL.md
ARCHITECTURE.md
SKILL.md
```

Then inventory the current implementation before editing:

```sh
rg -n "legacy-async-runtime|legacy async runtime|worker|cron|schedule|task queue|task_queue|TaskQueue|Activity|Workflow" legacy-async-runtime cmd internal docs testdata durable
rg -n "scenery worker|inspect legacy-async-runtime|SCENERY_ROLE|worker typescript|cron" cmd internal docs testdata
rg -n "sqlite|DatabaseURL|dev.services|db branch|db snapshot" cmd internal db auth docs testdata
```

The root `AGENTS.md` says not to spawn subagents or background agent tasks in this repo. Do all exploration, implementation, and validation in the main session. Re-check for child `AGENTS.md` files before touching any path.

This plan depends on `0088` for the per-service SQLite database model. If `0088` changes the service DB state root, URL shape, CLI grammar, or database branch lifecycle, update this plan before implementing durable execution.

## Milestones

M1 designs and validates contracts: no NATS, per-service durable SQLite files, no `scenery` table names, authenticated HTTPS long-polling, and public `durable` API shape.

M2 adds the durable store foundation under an internal package: open, pragmas, migrations, task reconciliation, job start, status, events, and stale lease recovery.

M3 adds the public `scenery.sh/durable` package and local in-process worker runtime.

M4 wires durable declarations into parser/model/codegen and runtime roles so API, worker, and local-dev `all` modes behave predictably.

M5 adds worker HTTP protocol endpoints and worker token authentication.

M6 adds `scenery worker durable`, remote worker config, subscriptions, labels, long-polling, heartbeat, completion, failure, and missing-handler behavior.

M7 adds schedules, steps, signals, CLI inspection/admin, dashboard panels, docs, schemas, and self-harness checks.

M8 documents and implements the legacy async runtime migration/compatibility bridge without preserving legacy async runtime's heavy operational model as the preferred path.

## Plan of Work

Build the smallest durable runtime that replaces the useful legacy async runtime-backed path without importing legacy async runtime's complexity. The runtime should create durable SQLite databases only for services that declare durable work, expose HTTPS worker leasing only through the owning app/runtime, and keep all state changes as SQLite transactions owned by that runtime.

Implementation should be staged. Start with store and invariants, then local API and local worker execution, then remote worker protocol and token auth, then schedules/steps/signals, then inspection/dashboard/admin. Each slice must leave the repo testable and must update the relevant docs/schema/test surface when behavior changes.

Delete or mark legacy legacy async runtime guidance only when the replacement behavior exists. Until then, this plan records the target direction; it does not by itself change current public behavior.

## Concrete Steps

1. Re-inventory legacy async runtime, cron, worker, service database, CLI, dashboard, docs, schemas, and fixture code paths.
2. Add an internal durable store package with service-name path normalization, `<service>.durable.sqlite` resolution, SQLite pragmas, migrations, and tests that reject table names containing `scenery`.
3. Implement task reconciliation, job start with deterministic IDs/dedupe keys, status, events, and stale lease recovery.
4. Implement atomic claim, lease token fencing, heartbeat, complete, fail, retry/backoff, and race tests.
5. Add `scenery.sh/durable` task/start/status/cancel/events/signal/step APIs and a local handler registry.
6. Wire durable declarations into app model, generated registration, runtime roles, and local dev startup.
7. Add reserved worker HTTP endpoints under `/__scenery/durable/v1` with thin handlers over the store.
8. Add worker token storage, token CLI, scope enforcement, raw-secret redaction, and auth tests.
9. Add `scenery worker durable` with config file support, endpoint/token-env flags, service/task subscriptions, labels, concurrency, long-polling, heartbeat, and completion/failure handling.
10. Add schedules, overlap policies, catchup windows, steps, signals, and repair helpers.
11. Add CLI inspection/admin commands and JSON schemas for durable status, jobs, job inspect, retry, cancel, token, DB check, and repair outputs.
12. Add dashboard panels after the CLI JSON surfaces are stable.
13. Update README, ARCHITECTURE, docs/local-contract.md, docs/app-development-cookbook.md, docs/environment.md, SKILL.md, AGENTS.md when behavior changes, and add durable-specific docs.
14. Add self-harness checks for no NATS, filename constraints, table-name constraints, fixture durable execution, and CLI JSON schemas.

## Validation and Acceptance

Per-slice validation should include the smallest focused package tests plus the repo gates affected by the change. Final validation for the full plan should include:

```sh
go test ./internal/durable/...
go test ./durable/...
go test ./runtime/...
go test ./cmd/scenery/...
go test ./... -run Durable
go test ./...
go run ./cmd/scenery inspect docs --json
go run ./cmd/scenery harness self --summary --write
git diff --check
```

Acceptance criteria:

* An app with no durable tasks creates no durable DB.
* A service with durable tasks creates `<service>.durable.sqlite`.
* Durable DB is separate from `<service>.sqlite`.
* Durable DB filename contains the service name.
* Durable DB table names do not contain `scenery`.
* No NATS dependency is added.
* Local worker can execute jobs.
* Remote worker can long-poll over HTTPS and execute jobs.
* Worker auth uses high-entropy bearer tokens, stored only as hashes.
* Token scopes can allow all services or selected services/task kinds.
* Deployment config controls worker subscriptions.
* Worker leases have fencing tokens.
* Stale completion is rejected.
* Expired leases are retried or failed.
* Retry/backoff works.
* Status/events are inspectable.
* Schedules can enqueue jobs.
* CLI can list, inspect, retry, cancel, and repair jobs.
* Dashboard can show basic durable state.

## Idempotence and Recovery

Durable DB migrations must be idempotent and run under an exclusive migration transaction. Store open should be safe to rerun after partial setup. Job start must support deterministic job IDs or dedupe keys and return the existing run for duplicate starts.

Because the durable DB is separate from the service DB, same-transaction enqueue with service data is not available. Production flows that need repairability should use deterministic job IDs/dedupe keys, service DB pending markers, or app-specific repair hooks. Do not claim same-file transaction guarantees across service and durable databases.

If implementation fails halfway, rerun focused durable tests first, then full package tests for touched runtime/CLI areas. Generated `.scenery/` output from validation is cache and must not be committed.

## Artifacts and Notes

The supplied draft is preserved below as the detailed architecture and implementation source. Its headings are nested under this section so the required ExecPlan headings above stay intact.

### ExecPlan: SQLite Durable Execution Runtime Per Service

This plan replaces the current legacy async runtime-backed worker/durable-execution path with a Scenery-owned durable execution runtime backed by **one separate SQLite database per service that needs durable work**.

This plan intentionally does **not** use NATS. Remote workers subscribe by authenticated HTTPS long-polling against the owning app/runtime. SQLite remains local to the service runtime and is never opened remotely.

Table names in the durable SQLite database **must not contain `scenery`**. The durable database filename **must contain the service name**, so related databases are visually grouped by service name.

---

#### 1. Purpose / Big Picture

Scenery currently supports legacy async runtime workflows and activities, but legacy async runtime is too heavy as an operational dependency for the desired product shape. The goal is to keep the useful parts:

```text
durable enqueue
worker polling
retry
timeouts
status
events
cron/schedules
remote workers
inspection
idempotent job starts
durable step results where needed
```

while dropping the heavy parts:

```text
legacy async runtime server
legacy async runtime worker deployment machinery
legacy async runtime task queues
legacy async runtime schedule service
legacy async runtime deterministic workflow replay constraints
legacy async runtime-specific query/update/signal semantics
legacy async runtime dev-server supervision
```

The new runtime is:

```text
service runtime
  owns
    service-name durable SQLite database
  exposes
    authenticated worker lease API
  records
    jobs, leases, attempts, events, schedules, steps, signals
  dispatches
    local and remote workers through HTTPS long-polling
```

The core invariant:

> A job exists because a row exists in the service's durable SQLite database, not because a broker message exists.

No NATS, no Redis, no Kafka, no broker dependency.

---

#### 2. Explicit Decisions

##### Decision 1: No NATS

Use HTTPS long-polling from workers to the app/runtime. The server internally polls the relevant durable SQLite databases every 100ms while a long-poll request is open.

Rationale:

```text
- 100ms latency is enough.
- Durable state already belongs in SQLite.
- NATS would not remove the need for SQLite job/status/result tables.
- Avoid a second operational dependency.
- Avoid dual source-of-truth issues.
```

##### Decision 2: One durable SQLite database per service that needs it

Do not create a global durable database for the whole app unless a service actually declares durable jobs, cron, or remote worker needs.

Database files are separate from the service's main SQLite database.

Example layout:

```text
.scenery/state/apps/<app-id>/db/
  auth.sqlite
  auth.durable.sqlite

  maps.sqlite
  maps.durable.sqlite

  codex.sqlite
  codex.durable.sqlite

  billing.sqlite
  billing.durable.sqlite
```

Alternative if the app already groups by environment:

```text
.scenery/state/<env>/db/
  maps.sqlite
  maps.durable.sqlite
  codex.sqlite
  codex.durable.sqlite
```

The important naming rule:

```text
<service-name>.sqlite
<service-name>.durable.sqlite
```

This satisfies:

```text
- main and durable DBs are separate;
- service name appears in both filenames;
- related files group together naturally by sorting;
- no table name needs to carry a service or Scenery prefix.
```

##### Decision 3: Durable DB table names do not contain `scenery`

Allowed table names:

```text
meta
tasks
jobs
job_events
job_attempts
job_steps
job_signals
schedules
leases
workers
worker_tokens
locks
schema_migrations
```

Disallowed table names:

```text
scenery_jobs
scenery_durable_jobs
scenery_worker_tokens
scenery_job_events
```

The database file already identifies the service and purpose.

##### Decision 4: Remote workers never open SQLite

Workers in another region or continent must not mount, sync, or directly open the durable SQLite database.

They use:

```text
HTTPS lease API
Bearer worker token
long-polling
lease + heartbeat + completion protocol
```

The service runtime owns all SQLite transactions.

##### Decision 5: Long-polling over tight remote polling

Remote workers should not do 100ms HTTP polling.

Instead:

```text
worker sends lease request with wait_ms=30000
server checks local durable DB every 100ms while request is open
server returns immediately when work is available
worker repeats after job/empty/error
```

This gives near-100ms dispatch latency without generating 10 empty network round-trips per second per worker.

##### Decision 6: At-least-once execution

The system guarantees at-least-once execution.

It does not guarantee exactly-once side effects.

All external side-effecting handlers must be idempotent or receive an idempotency key.

##### Decision 7: Separate durable DB means no same-transaction enqueue with service DB

Because the durable DB is separate from the service DB, enqueueing durable work cannot be in the exact same SQLite transaction as a mutation in the service DB.

This is accepted.

Mitigation:

```text
- deterministic job IDs or dedupe keys;
- repair/reconciliation command;
- optional "pending durable work" marker in service DB for critical flows;
- idempotent Start semantics;
- startup repair that compares service state with durable jobs.
```

Do not pretend this has same-file transactional guarantees. It does not.

---

#### 3. Target Runtime Model

```text
client/API request
  |
  v
service handler
  |
  | writes business state to <service>.sqlite
  |
  | starts durable job in <service>.durable.sqlite
  v
durable DB
  |
  v
remote/local worker long-polls runtime
  |
  v
runtime leases job from durable DB
  |
  v
worker executes job
  |
  | heartbeat, events, complete/fail
  v
runtime updates durable DB
```

For a remote worker:

```text
worker process in another continent
  |
  | HTTPS POST /__scenery/durable/v1/lease
  | Authorization: Bearer scn_wk_<token-id>_<secret>
  v
public app/runtime endpoint
  |
  | local SQLite transaction
  v
<service>.durable.sqlite
```

The worker subscribes to services and task kinds through deployment config:

```json
{
  "worker": {
    "targets": [
      {
        "name": "prod-main",
        "endpoint": "https://api.example.com",
        "token_env": "SCENERY_WORKER_TOKEN",
        "subscriptions": [
          {
            "services": ["*"],
            "task_kinds": ["*"],
            "concurrency": 4
          },
          {
            "services": ["maps"],
            "task_kinds": ["roof.detect.v1", "tile.render.v1"],
            "concurrency": 1,
            "required_labels": {
              "gpu": "true"
            }
          }
        ]
      }
    ]
  }
}
```

---

#### 4. Non-Goals

This plan does not implement:

```text
- NATS;
- Redis;
- Kafka;
- legacy async runtime-compatible replay;
- deterministic workflow interpreter;
- exactly-once side effects;
- distributed SQLite;
- direct remote SQLite access;
- cross-service distributed transactions;
- full legacy async runtime query/update/signal parity;
- global job ordering across services;
- global worker scheduler across all apps;
- high-availability failover for the SQLite-owning service.
```

The first version should be intentionally smaller than legacy async runtime.

---

#### 5. User-Facing Concepts

##### Task

A registered durable unit of work.

```go
var DetectRoof = durable.NewTask[DetectRoofInput, DetectRoofResult](
    "roof.detect.v1",
    durable.TaskConfig{
        Service:     "maps",
        MaxAttempts: 3,
        Timeout:     10 * time.Minute,
        Retry: durable.RetryPolicy{
            InitialInterval: 5 * time.Second,
            BackoffFactor:  2.0,
            MaxInterval:    2 * time.Minute,
        },
        Requirements: durable.Requirements{
            Labels: map[string]string{
                "gpu": "true",
            },
        },
    },
    detectRoofHandler,
)
```

##### Job

A started task instance.

```go
run, err := durable.Start(ctx, DetectRoof, input, durable.StartOptions{
    ID:        "roof-detect:" + submissionID,
    DedupeKey: "roof-detect:" + submissionID,
})
```

##### Worker

A process authorized to lease jobs from one or more services.

```sh
scenery worker durable \
  --endpoint https://api.example.com \
  --token-env SCENERY_WORKER_TOKEN \
  --services maps,codex \
  --concurrency 4
```

##### Lease

A temporary right to execute a job.

```text
job_id
lease_id
lease_token
lease_until
worker_id
attempt
```

Only the current lease holder may heartbeat, complete, or fail the job.

##### Event

A durable append-only job event.

Examples:

```text
job.created
job.leased
job.heartbeat
job.progress
job.completed
job.failed
job.retry_scheduled
job.canceled
step.started
step.completed
step.failed
signal.received
signal.consumed
```

##### Step

Optional memoized durable execution step inside a job.

Use only when a multi-stage job should not repeat completed expensive operations after retry.

---

#### 6. File Layout

##### Runtime durable DB location

Use app state root, not source root, for runtime DBs.

Suggested default:

```text
<state-root>/db/<service-name>.durable.sqlite
```

Examples:

```text
.scenery/state/db/maps.durable.sqlite
.scenery/state/db/codex.durable.sqlite
.scenery/state/db/auth.durable.sqlite
```

If Scenery already has an agent/app/session state layout, place durable DBs alongside the service DBs:

```text
<agent-home>/apps/<app-id>/<env>/db/
  maps.sqlite
  maps.durable.sqlite
  codex.sqlite
  codex.durable.sqlite
```

##### Naming constraints

Service name must be normalized for filenames.

Example normalization:

```text
Maps              -> maps.durable.sqlite
maps-service      -> maps-service.durable.sqlite
maps/service      -> maps-service.durable.sqlite
maps_service      -> maps_service.durable.sqlite
```

Reject unsafe names:

```text
../maps
/absolute/path
maps\windows
maps:bad
empty string
```

##### Sidecar files

SQLite may create:

```text
maps.durable.sqlite
maps.durable.sqlite-wal
maps.durable.sqlite-shm
```

These also group naturally by service name.

---

#### 7. SQLite Pragmas

On opening every durable DB:

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA temp_store = MEMORY;
```

Durability mode should be configurable:

```json
{
  "durable": {
    "sqlite": {
      "synchronous": "full"
    }
  }
}
```

Supported values:

```text
full      -> PRAGMA synchronous = FULL
normal    -> PRAGMA synchronous = NORMAL
off       -> only for tests/dev throwaway runtimes
```

Recommended default:

```text
production: FULL
local dev: NORMAL is acceptable if explicitly selected
tests: NORMAL or OFF depending on fixture
```

Rationale:

```text
- workload is not write-heavy enough to justify risky defaults;
- user explicitly does not need ultra-low latency;
- accepted jobs should usually survive a hard crash;
- config can relax durability when desired.
```

---

#### 8. Database Schema

Each service durable DB has the same schema.

Table names intentionally do not contain `scenery`.

##### 8.1 `meta`

Stores DB identity and runtime metadata.

```sql
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Initial rows:

```text
schema_version = "1"
app_id         = "<app-id>"
service_name   = "<service-name>"
created_by     = "scenery"
created_at     = "<timestamp>"
```

Table name is generic. Value may mention Scenery.

##### 8.2 `schema_migrations`

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

##### 8.3 `tasks`

Registered task declarations known to this service.

```sql
CREATE TABLE IF NOT EXISTS tasks (
  name TEXT PRIMARY KEY,
  version INTEGER NOT NULL DEFAULT 1,

  handler_ref TEXT NOT NULL,
  input_codec TEXT NOT NULL DEFAULT 'json',
  result_codec TEXT NOT NULL DEFAULT 'json',

  default_timeout_ms INTEGER NOT NULL DEFAULT 60000,
  default_lease_ms INTEGER NOT NULL DEFAULT 60000,

  max_attempts INTEGER NOT NULL DEFAULT 1,
  retry_initial_ms INTEGER NOT NULL DEFAULT 1000,
  retry_max_ms INTEGER NOT NULL DEFAULT 60000,
  retry_backoff REAL NOT NULL DEFAULT 2.0,
  retry_jitter REAL NOT NULL DEFAULT 0.1,

  requirements_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,

  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Notes:

```text
- task names are stable API identifiers, e.g. roof.detect.v1;
- handler_ref can identify generated Go registration metadata;
- requirements_json stores label requirements such as gpu=true;
- tasks table is useful for inspection and worker compatibility checks.
```

##### 8.4 `jobs`

The main queue/state table.

```sql
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,

  task_name TEXT NOT NULL,
  task_version INTEGER NOT NULL DEFAULT 1,

  state TEXT NOT NULL CHECK (
    state IN (
      'queued',
      'running',
      'sleeping',
      'waiting',
      'succeeded',
      'failed',
      'canceled'
    )
  ),

  dedupe_key TEXT UNIQUE,

  priority INTEGER NOT NULL DEFAULT 0,
  run_after TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 1,

  timeout_at TEXT,
  deadline_at TEXT,

  lease_id TEXT,
  lease_owner TEXT,
  lease_token_hash TEXT,
  lease_until TEXT,

  input_codec TEXT NOT NULL DEFAULT 'json',
  input_blob BLOB NOT NULL,

  result_codec TEXT,
  result_blob BLOB,

  error_codec TEXT,
  error_blob BLOB,

  requirements_json TEXT NOT NULL DEFAULT '{}',
  labels_json TEXT NOT NULL DEFAULT '{}',
  memo_json TEXT NOT NULL DEFAULT '{}',

  created_by TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TEXT,

  FOREIGN KEY (task_name) REFERENCES tasks(name)
);
```

Indexes:

```sql
CREATE INDEX IF NOT EXISTS jobs_ready_idx
ON jobs(state, run_after, priority, created_at);

CREATE INDEX IF NOT EXISTS jobs_task_state_idx
ON jobs(task_name, state, created_at);

CREATE INDEX IF NOT EXISTS jobs_lease_idx
ON jobs(lease_owner, lease_until);

CREATE INDEX IF NOT EXISTS jobs_dedupe_idx
ON jobs(dedupe_key);
```

State meaning:

```text
queued
  ready or future-ready work not currently leased

running
  leased by a worker

sleeping
  retry/backoff or timer delay

waiting
  waiting for signal/external completion

succeeded
  terminal success

failed
  terminal failure

canceled
  terminal cancellation
```

##### 8.5 `job_attempts`

One row per execution attempt.

```sql
CREATE TABLE IF NOT EXISTS job_attempts (
  job_id TEXT NOT NULL,
  attempt INTEGER NOT NULL,

  worker_id TEXT,
  lease_id TEXT,

  started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  heartbeat_at TEXT,
  finished_at TEXT,

  state TEXT NOT NULL CHECK (
    state IN ('running', 'succeeded', 'failed', 'expired', 'canceled')
  ),

  error_codec TEXT,
  error_blob BLOB,

  PRIMARY KEY (job_id, attempt),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
```

##### 8.6 `job_events`

Append-only history.

```sql
CREATE TABLE IF NOT EXISTS job_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,

  job_id TEXT NOT NULL,
  attempt INTEGER,

  event_type TEXT NOT NULL,
  payload_codec TEXT NOT NULL DEFAULT 'json',
  payload_blob BLOB,

  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
```

Indexes:

```sql
CREATE INDEX IF NOT EXISTS job_events_job_seq_idx
ON job_events(job_id, seq);

CREATE INDEX IF NOT EXISTS job_events_type_idx
ON job_events(event_type, created_at);
```

##### 8.7 `job_steps`

Memoized step results for multi-step jobs.

```sql
CREATE TABLE IF NOT EXISTS job_steps (
  job_id TEXT NOT NULL,

  step_key TEXT NOT NULL,
  step_version INTEGER NOT NULL DEFAULT 1,

  state TEXT NOT NULL CHECK (
    state IN ('started', 'succeeded', 'failed', 'skipped')
  ),

  attempt INTEGER NOT NULL DEFAULT 0,

  input_hash TEXT,
  idempotency_key TEXT NOT NULL,

  result_codec TEXT,
  result_blob BLOB,

  error_codec TEXT,
  error_blob BLOB,

  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

  PRIMARY KEY (job_id, step_key),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
```

##### 8.8 `job_signals`

Durable inbox for external events.

```sql
CREATE TABLE IF NOT EXISTS job_signals (
  job_id TEXT NOT NULL,

  name TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,

  payload_codec TEXT NOT NULL DEFAULT 'json',
  payload_blob BLOB NOT NULL,

  consumed_at TEXT,
  received_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

  PRIMARY KEY (job_id, name, dedupe_key),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
```

##### 8.9 `schedules`

Cron/interval schedules.

```sql
CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,

  task_name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,

  spec_codec TEXT NOT NULL DEFAULT 'json',
  spec_blob BLOB NOT NULL,

  overlap_policy TEXT NOT NULL CHECK (
    overlap_policy IN (
      'skip',
      'buffer_one',
      'buffer_all',
      'allow_all'
    )
  ) DEFAULT 'skip',

  catchup_window_ms INTEGER NOT NULL DEFAULT 60000,

  next_fire_at TEXT,
  last_fire_at TEXT,

  input_codec TEXT NOT NULL DEFAULT 'json',
  input_blob BLOB NOT NULL DEFAULT x'7b7d',

  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

  FOREIGN KEY (task_name) REFERENCES tasks(name)
);
```

First version should support:

```text
interval schedules
simple cron strings if already parsed elsewhere
skip overlap
allow_all overlap
buffer_one overlap
```

Defer complex calendar behavior if not needed immediately.

##### 8.10 `workers`

Observed worker processes.

```sql
CREATE TABLE IF NOT EXISTS workers (
  id TEXT PRIMARY KEY,

  token_id TEXT,
  deployment_id TEXT,
  build_id TEXT,

  hostname TEXT,
  pid INTEGER,
  region TEXT,

  labels_json TEXT NOT NULL DEFAULT '{}',
  subscriptions_json TEXT NOT NULL DEFAULT '[]',

  first_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

  disabled_at TEXT
);
```

##### 8.11 `worker_tokens`

Long-lived worker credentials.

```sql
CREATE TABLE IF NOT EXISTS worker_tokens (
  id TEXT PRIMARY KEY,

  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,

  scopes_json TEXT NOT NULL DEFAULT '{}',

  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TEXT,
  disabled_at TEXT,

  last_used_at TEXT
);
```

Token format:

```text
scn_wk_<token-id>_<secret>
```

Example:

```text
scn_wk_01J2ABCD9K8R9MX3Z8H7QF5QVG_K7p5uF0R...256-bit-secret...
```

Store only:

```text
token_id
hash(secret)
```

Never store or log the raw secret.

##### 8.12 `leases`

Optional normalized lease table.

The current lease is already stored on `jobs`, but a lease table is useful for diagnostics.

```sql
CREATE TABLE IF NOT EXISTS leases (
  id TEXT PRIMARY KEY,

  job_id TEXT NOT NULL,
  worker_id TEXT NOT NULL,

  token_hash TEXT NOT NULL,

  acquired_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TEXT NOT NULL,
  released_at TEXT,

  state TEXT NOT NULL CHECK (
    state IN ('active', 'completed', 'failed', 'expired', 'canceled')
  ),

  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
```

The first version may skip this table if `jobs` + `job_attempts` is enough. If included, keep `jobs.lease_id` as the fast current-lease pointer.

##### 8.13 `locks`

Small service-local coordination locks.

```sql
CREATE TABLE IF NOT EXISTS locks (
  name TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Used for:

```text
schedule reconciler
migration lock
cleanup compactor
stale lease scanner
```

---

#### 9. Public Go API

Introduce a new package:

```text
scenery.sh/durable
```

Do not put this in `scenery.sh/legacy-async-runtime`.

legacy async runtime can later become an adapter or legacy package.

##### 9.1 Task declaration

```go
package durable

type Task[I, O any] struct {
    name string
    cfg  TaskConfig
}

type TaskConfig struct {
    Service string

    Timeout time.Duration

    LeaseDuration time.Duration

    MaxAttempts int

    Retry RetryPolicy

    Requirements Requirements

    MaxConcurrency int
}

type RetryPolicy struct {
    InitialInterval time.Duration
    MaxInterval     time.Duration
    BackoffFactor   float64
    Jitter          float64
}

type Requirements struct {
    Labels map[string]string
}

func NewTask[I, O any](
    name string,
    cfg TaskConfig,
    handler func(context.Context, I) (O, error),
) *Task[I, O]
```

##### 9.2 Start

```go
type StartOptions struct {
    ID        string
    DedupeKey string

    Priority int
    RunAfter time.Time

    Memo map[string]any
    Labels map[string]string

    MaxAttempts *int
    Timeout     time.Duration
}

type Run[O any] struct {
    Service string
    JobID   string
}

func Start[I, O any](
    ctx context.Context,
    task *Task[I, O],
    input I,
    opts StartOptions,
) (Run[O], error)
```

Rules:

```text
- ID or DedupeKey required for production validation.
- If ID omitted, runtime may generate one only for local/dev.
- DedupeKey prevents duplicate starts.
- Start writes into <service>.durable.sqlite.
```

##### 9.3 Result

```go
func Get[O any](ctx context.Context, run Run[O]) (O, error)

func Status(ctx context.Context, service string, jobID string) (JobStatus, error)

func Cancel(ctx context.Context, service string, jobID string, reason string) error
```

##### 9.4 Events

```go
func Events(
    ctx context.Context,
    service string,
    jobID string,
    afterSeq int64,
    limit int,
) ([]Event, error)
```

##### 9.5 Signals

```go
func Signal[I any](
    ctx context.Context,
    service string,
    jobID string,
    name string,
    input I,
    opts SignalOptions,
) error

type SignalOptions struct {
    DedupeKey string
}
```

##### 9.6 Steps

```go
type Context interface {
    context.Context

    JobID() string
    Attempt() int
    IdempotencyKey(stepKey string) string
}

func Step[I, O any](
    ctx Context,
    key string,
    input I,
    fn func(context.Context, I) (O, error),
) (O, error)
```

Semantics:

```text
- If step succeeded before, return stored result.
- If step failed before and job is retrying, execute again unless configured otherwise.
- Step key must be stable and versioned, e.g. "detect-roof.v1".
```

---

#### 10. Worker HTTP API

The runtime exposes reserved internal worker endpoints.

Suggested route prefix:

```text
/__scenery/durable/v1
```

The table-name constraint does not apply to HTTP routes.

##### 10.1 `POST /lease`

Long-poll and claim available jobs.

Request:

```json
{
  "worker_id": "wk-us-east-1-gpu-01",
  "deployment_id": "prod",
  "build_id": "2026-06-27T120000Z-abcd1234",

  "services": ["maps", "codex"],
  "task_kinds": ["*"],

  "labels": {
    "region": "us-east-1",
    "gpu": "true",
    "arch": "arm64"
  },

  "capacity": 4,

  "wait_ms": 30000,
  "lease_ms": 60000
}
```

Response with job:

```json
{
  "job": {
    "id": "job_01J...",
    "service": "maps",
    "task_name": "roof.detect.v1",
    "attempt": 2,
    "timeout_ms": 600000,
    "input_codec": "json",
    "input": {
      "submission_id": "sub_123",
      "source_object": "storage://private/uploads/source.tif"
    }
  },
  "lease": {
    "id": "lease_01J...",
    "token": "lease-secret",
    "expires_at": "2026-06-27T12:01:00Z"
  }
}
```

Response without job:

```json
{
  "job": null,
  "retry_after_ms": 100
}
```

##### 10.2 `POST /heartbeat`

Request:

```json
{
  "service": "maps",
  "job_id": "job_01J...",
  "lease_id": "lease_01J...",
  "lease_token": "lease-secret",
  "extend_ms": 60000,
  "progress": {
    "phase": "detecting",
    "percent": 42
  }
}
```

Response:

```json
{
  "ok": true,
  "cancel_requested": false,
  "lease_expires_at": "2026-06-27T12:02:00Z"
}
```

##### 10.3 `POST /complete`

Request:

```json
{
  "service": "maps",
  "job_id": "job_01J...",
  "lease_id": "lease_01J...",
  "lease_token": "lease-secret",
  "result_codec": "json",
  "result": {
    "roof_polygon_object": "storage://private/results/roof.json"
  }
}
```

Response:

```json
{
  "ok": true
}
```

##### 10.4 `POST /fail`

Request:

```json
{
  "service": "maps",
  "job_id": "job_01J...",
  "lease_id": "lease_01J...",
  "lease_token": "lease-secret",
  "error_codec": "json",
  "error": {
    "type": "temporary",
    "message": "upstream timeout"
  },
  "retryable": true
}
```

Response if retry scheduled:

```json
{
  "ok": true,
  "state": "sleeping",
  "next_run_after": "2026-06-27T12:05:00Z"
}
```

Response if terminal failure:

```json
{
  "ok": true,
  "state": "failed"
}
```

##### 10.5 `POST /event`

Append progress events.

```json
{
  "service": "maps",
  "job_id": "job_01J...",
  "lease_id": "lease_01J...",
  "lease_token": "lease-secret",
  "event_type": "progress",
  "payload": {
    "phase": "downloaded source",
    "bytes": 123456
  }
}
```

##### 10.6 `POST /step/start`

Optional, only for remote worker step memoization.

For in-process Go workers, step state can be managed by the runtime directly. For external workers, expose step APIs.

Request:

```json
{
  "service": "maps",
  "job_id": "job_01J...",
  "lease_id": "lease_01J...",
  "lease_token": "lease-secret",
  "step_key": "download-source.v1",
  "input_hash": "sha256:..."
}
```

Response when already completed:

```json
{
  "state": "succeeded",
  "result_codec": "json",
  "result": {
    "local_ref": "storage://..."
  }
}
```

Response when should execute:

```json
{
  "state": "started",
  "idempotency_key": "job_01J...:download-source.v1"
}
```

##### 10.7 `POST /step/complete`

```json
{
  "service": "maps",
  "job_id": "job_01J...",
  "lease_id": "lease_01J...",
  "lease_token": "lease-secret",
  "step_key": "download-source.v1",
  "result_codec": "json",
  "result": {}
}
```

---

#### 11. Authentication and Authorization

##### 11.1 Token format

Worker token:

```text
scn_wk_<token-id>_<secret>
```

Example:

```text
scn_wk_01J2K7K6BTZV28PQYF1R7P9N2R_ly72lKZk9u5qf...
```

`token-id` is public-ish identifier.

`secret` is high-entropy bearer secret.

Minimum:

```text
256 bits random secret
base64url or base62 encoded
```

##### 11.2 Storage

In `worker_tokens`:

```text
id
name
hash(secret)
scopes_json
expires_at
disabled_at
```

Hash:

```text
HMAC-SHA256(server-secret, token-secret)
```

or:

```text
Argon2id/bcrypt if token verification volume is low
```

Pragmatic recommendation:

```text
HMAC-SHA256 with a server-side pepper
```

Reason:

```text
- tokens are already high entropy;
- fast verification is useful for every long-poll request;
- database leak alone should not reveal usable tokens.
```

##### 11.3 Scopes

Example `scopes_json`:

```json
{
  "services": ["maps", "codex"],
  "task_kinds": ["*"],
  "deployments": ["prod"],
  "max_concurrency": 8,
  "max_lease_ms": 300000,
  "labels": {
    "gpu": "true"
  }
}
```

Authorization rules:

```text
- requested service must be allowed by token;
- requested task kind must be allowed by token;
- requested deployment id must be allowed by token;
- requested lease duration must be <= token max_lease_ms;
- advertised labels may be restricted by token;
- token disabled_at or expires_at denies access.
```

##### 11.4 Worker identity

Worker request includes:

```text
worker_id
deployment_id
build_id
hostname
pid
labels
subscriptions
```

`worker_id` is not trusted as authentication. It is an operational label.

Authentication is only the bearer token.

##### 11.5 Token commands

Add CLI:

```sh
scenery durable token create \
  --service maps \
  --name gpu-worker-prod \
  --services maps,codex \
  --task-kinds '*' \
  --deployment prod \
  --max-concurrency 8 \
  --expires 365d \
  --json

scenery durable token list --service maps --json

scenery durable token revoke --service maps <token-id> --json
```

For all-service tokens, either:

```sh
scenery durable token create --all-services ...
```

or create the token record in each service durable DB.

Preferred first implementation:

```text
Tokens are stored per service DB.
An all-services token is materialized into each service durable DB.
```

Reason:

```text
- each service DB remains standalone;
- worker auth can be checked while leasing from that service DB;
- no global durable DB is required.
```

Later improvement:

```text
global app worker-token DB
```

but not in the first version.

---

#### 12. Lease Algorithm

##### 12.1 Long-poll loop

Server-side pseudocode:

```go
func Lease(ctx context.Context, req LeaseRequest) (*LeaseResponse, error) {
    if err := authenticateAndAuthorize(req); err != nil {
        return nil, err
    }

    wait := clamp(req.Wait, 0, 30*time.Second)
    deadline := time.Now().Add(wait)

    for {
        job, err := tryClaimAcrossServices(ctx, req)
        if err != nil {
            return nil, err
        }
        if job != nil {
            return job, nil
        }

        if wait == 0 || time.Now().After(deadline) {
            return &LeaseResponse{
                RetryAfter: 100 * time.Millisecond,
            }, nil
        }

        select {
        case <-localDurableNotify:
        case <-time.After(100 * time.Millisecond):
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }
}
```

##### 12.2 Claim across services

If worker subscribes to many services:

```text
services = ["maps", "codex", "auth"]
```

The runtime should:

```text
- open each service durable DB lazily;
- skip services without durable DB;
- choose ready jobs fairly;
- avoid always checking services in same order;
- claim at most req.capacity jobs, or one job in v1.
```

Fairness strategy v1:

```text
rotate starting service index per lease request
```

Example:

```go
start := atomic.AddUint64(&leaseRoundRobin, 1) % len(services)
for i := 0; i < len(services); i++ {
    service := services[(start+i)%len(services)]
    job := tryClaimService(service)
    if job != nil { return job }
}
```

##### 12.3 Claim SQL

Read candidate:

```sql
SELECT
  id,
  task_name,
  attempt,
  max_attempts,
  input_codec,
  input_blob,
  requirements_json
FROM jobs
WHERE state IN ('queued', 'sleeping')
  AND run_after <= CURRENT_TIMESTAMP
  AND (lease_until IS NULL OR lease_until < CURRENT_TIMESTAMP)
ORDER BY priority DESC, run_after ASC, created_at ASC
LIMIT 1;
```

Claim candidate:

```sql
UPDATE jobs
SET
  state = 'running',
  lease_id = ?,
  lease_owner = ?,
  lease_token_hash = ?,
  lease_until = ?,
  attempt = attempt + 1,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state IN ('queued', 'sleeping')
  AND run_after <= CURRENT_TIMESTAMP
  AND (lease_until IS NULL OR lease_until < CURRENT_TIMESTAMP)
RETURNING
  id,
  task_name,
  task_version,
  attempt,
  max_attempts,
  input_codec,
  input_blob,
  timeout_at,
  deadline_at,
  requirements_json,
  labels_json,
  memo_json;
```

Then insert attempt/event in same transaction:

```sql
INSERT INTO job_attempts (
  job_id,
  attempt,
  worker_id,
  lease_id,
  state
)
VALUES (?, ?, ?, ?, 'running');

INSERT INTO job_events (
  job_id,
  attempt,
  event_type,
  payload_blob
)
VALUES (?, ?, 'job.leased', ?);
```

##### 12.4 Lease token

Generate per lease:

```text
lease_id = ULID/UUIDv7
lease_token = 256-bit random
lease_token_hash = HMAC-SHA256(server-secret, lease_token)
```

Only return raw `lease_token` once.

Completion/heartbeat/failure requires it.

---

#### 13. Completion Algorithm

##### 13.1 Complete SQL

Within transaction:

```sql
UPDATE jobs
SET
  state = 'succeeded',
  result_codec = ?,
  result_blob = ?,
  lease_id = NULL,
  lease_owner = NULL,
  lease_token_hash = NULL,
  lease_until = NULL,
  completed_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state = 'running'
  AND lease_id = ?
  AND lease_token_hash = ?
  AND lease_until > CURRENT_TIMESTAMP;
```

Check rows affected.

If zero:

```text
- stale lease;
- wrong token;
- expired lease;
- already completed/canceled;
- reject with 409 Conflict.
```

Then:

```sql
UPDATE job_attempts
SET
  state = 'succeeded',
  finished_at = CURRENT_TIMESTAMP
WHERE job_id = ?
  AND attempt = ?;

INSERT INTO job_events (
  job_id,
  attempt,
  event_type,
  payload_blob
)
VALUES (?, ?, 'job.completed', ?);
```

##### 13.2 Failure SQL

If retryable and attempts remain:

```sql
UPDATE jobs
SET
  state = 'sleeping',
  run_after = ?,
  error_codec = ?,
  error_blob = ?,
  lease_id = NULL,
  lease_owner = NULL,
  lease_token_hash = NULL,
  lease_until = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state = 'running'
  AND lease_id = ?
  AND lease_token_hash = ?;
```

If terminal:

```sql
UPDATE jobs
SET
  state = 'failed',
  error_codec = ?,
  error_blob = ?,
  lease_id = NULL,
  lease_owner = NULL,
  lease_token_hash = NULL,
  lease_until = NULL,
  completed_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state = 'running'
  AND lease_id = ?
  AND lease_token_hash = ?;
```

##### 13.3 Backoff

Formula:

```text
delay = min(max_interval, initial_interval * backoff_factor^(attempt - 1))
delay = jitter(delay, jitter_ratio)
```

Example:

```text
initial = 5s
factor = 2
max = 2m
jitter = 0.1

attempt 1 failure -> 5s +/-10%
attempt 2 failure -> 10s +/-10%
attempt 3 failure -> 20s +/-10%
...
```

---

#### 14. Heartbeat Algorithm

Heartbeat extends lease if valid.

```sql
UPDATE jobs
SET
  lease_until = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state = 'running'
  AND lease_id = ?
  AND lease_token_hash = ?
  AND lease_until > CURRENT_TIMESTAMP;
```

Also:

```sql
UPDATE job_attempts
SET heartbeat_at = CURRENT_TIMESTAMP
WHERE job_id = ?
  AND attempt = ?;
```

Optionally append event only every N seconds to avoid noisy event logs:

```text
job.heartbeat every 30s max
job.progress whenever payload changes materially
```

Cancellation check:

```sql
SELECT state FROM jobs WHERE id = ?;
```

If state is `canceled`, heartbeat response tells worker to stop.

---

#### 15. Stale Lease Recovery

A background loop per service checks expired running jobs.

Frequency:

```text
every 5s
```

SQL:

```sql
SELECT id, attempt, max_attempts
FROM jobs
WHERE state = 'running'
  AND lease_until <= CURRENT_TIMESTAMP
LIMIT 100;
```

For each:

If attempts remain:

```sql
UPDATE jobs
SET
  state = 'sleeping',
  run_after = ?,
  lease_id = NULL,
  lease_owner = NULL,
  lease_token_hash = NULL,
  lease_until = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state = 'running'
  AND lease_until <= CURRENT_TIMESTAMP;
```

If attempts exhausted:

```sql
UPDATE jobs
SET
  state = 'failed',
  lease_id = NULL,
  lease_owner = NULL,
  lease_token_hash = NULL,
  lease_until = NULL,
  completed_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND state = 'running'
  AND lease_until <= CURRENT_TIMESTAMP;
```

Append event:

```text
job.lease_expired
job.retry_scheduled
job.failed
```

---

#### 16. Schedules / Cron

##### 16.1 Schedule reconciler

Each service with schedules runs a local schedule reconciler.

Use `locks` table:

```sql
INSERT INTO locks (name, owner, expires_at)
VALUES ('schedule-reconciler', ?, ?)
ON CONFLICT(name) DO UPDATE SET
  owner = excluded.owner,
  expires_at = excluded.expires_at,
  updated_at = CURRENT_TIMESTAMP
WHERE locks.expires_at <= CURRENT_TIMESTAMP;
```

Only lock owner fires schedules.

##### 16.2 Fire schedule

For each due schedule:

```text
schedule id
task name
scheduled_at
```

Create deterministic job id:

```text
schedule:<schedule-id>:<scheduled-at-rfc3339>
```

or:

```text
sched_<hash(schedule_id + scheduled_at)>
```

Insert job with dedupe key:

```text
schedule:<schedule-id>:<scheduled-at>
```

This makes schedule firing idempotent.

##### 16.3 Overlap policies

Implement first:

```text
skip
  if previous schedule job for same schedule is running/waiting/sleeping, do not enqueue

allow_all
  enqueue every due occurrence

buffer_one
  if one pending/running exists, do not enqueue another; otherwise enqueue next
```

Defer:

```text
buffer_all
  can be implemented later if needed
```

##### 16.4 Catchup

If service was down:

```text
now - next_fire_at <= catchup_window -> fire
otherwise skip and advance
```

Append events:

```text
schedule.fired
schedule.skipped_overlap
schedule.skipped_catchup_window
schedule.updated_next_fire
```

---

#### 17. Remote Worker Runtime

##### 17.1 Worker process lifecycle

Worker starts:

```text
1. Load deployment config.
2. Resolve token from env.
3. Generate stable worker_id if not configured.
4. Start N local goroutines equal to concurrency.
5. Each goroutine long-polls lease endpoint.
6. Execute returned job by task_name.
7. Heartbeat while executing.
8. Complete/fail.
9. Repeat.
```

##### 17.2 Worker config

Example file:

```json
{
  "worker_id": "gpu-worker-us-east-1-a",
  "targets": [
    {
      "name": "prod",
      "endpoint": "https://api.example.com",
      "token_env": "SCENERY_WORKER_TOKEN",
      "wait_ms": 30000,
      "empty_backoff_ms": 100,
      "lease_ms": 60000,
      "heartbeat_ms": 20000,
      "subscriptions": [
        {
          "services": ["*"],
          "task_kinds": ["*"],
          "concurrency": 2
        },
        {
          "services": ["maps"],
          "task_kinds": ["roof.detect.v1"],
          "concurrency": 1,
          "labels": {
            "gpu": "true"
          }
        }
      ],
      "labels": {
        "region": "us-east-1",
        "gpu": "true",
        "arch": "arm64"
      }
    }
  ]
}
```

##### 17.3 CLI

```sh
scenery worker durable \
  --config worker.durable.json

scenery worker durable \
  --endpoint https://api.example.com \
  --token-env SCENERY_WORKER_TOKEN \
  --services maps,codex \
  --task-kinds '*' \
  --concurrency 4 \
  --label region=us-east-1 \
  --label gpu=true
```

##### 17.4 Worker task registration

Generated app worker binary must know available task handlers.

At startup:

```go
durable.RegisterTask(...)
```

Worker receives `task_name`.

If handler missing:

```text
fail job with non-retryable error:
  worker_missing_handler
```

But avoid leasing incompatible jobs by including task kinds in request.

##### 17.5 Build compatibility

First version:

```text
No legacy async runtime-style worker deployment versioning.
```

But record:

```text
build_id
deployment_id
handler version
```

in:

```text
workers
job_attempts
job_events
```

Later, add task version compatibility:

```text
task_name + task_version
```

---

#### 18. Runtime Integration

##### 18.1 Parser/model/codegen

Add durable declarations to app model.

Conceptually:

```text
internal/parse
  discover durable.NewTask calls if needed

internal/model
  Service.DurableTasks []DurableTask

internal/codegen
  imports packages that register durable tasks
  emits runtime task registration
```

Scenery already prefers source-derived app semantics captured in `internal/model` before codegen, so keep durable declarations as model facts rather than runtime-only reflection.

##### 18.2 App runtime

At app startup:

```text
- discover services with durable tasks/schedules;
- compute durable DB path per service;
- open DB lazily or at startup;
- apply migrations;
- reconcile task declarations into tasks table;
- start schedule loops for services with schedules;
- expose worker endpoints if durable enabled;
- expose inspect/admin endpoints.
```

##### 18.3 Roles

Current Scenery has role concepts around API/worker/all for legacy async runtime. Durable runtime should keep the useful shape:

```text
SCENERY_ROLE=api
  serves API
  owns durable HTTP endpoints
  may run schedule reconciler
  does not execute local jobs unless configured

SCENERY_ROLE=worker
  executes durable jobs
  may talk to local or remote endpoint
  does not serve public API

SCENERY_ROLE=all
  local dev: API + worker in one process
```

This preserves existing operational intent without legacy async runtime-specific behavior.

##### 18.4 Local dev

For local dev:

```text
scenery up
  starts app runtime
  creates <service>.durable.sqlite only if needed
  starts in-process durable worker if role all
  dashboard shows durable jobs
```

No external durable service starts.

This is a major simplification versus legacy async runtime dev-server supervision.

---

#### 19. CLI Commands

##### 19.1 Status

```sh
scenery durable status --json
scenery durable status --service maps --json
```

Output:

```json
{
  "services": [
    {
      "service": "maps",
      "db_path": ".../maps.durable.sqlite",
      "exists": true,
      "schema_version": 1,
      "counts": {
        "queued": 3,
        "running": 1,
        "sleeping": 2,
        "waiting": 0,
        "succeeded": 120,
        "failed": 4,
        "canceled": 1
      }
    }
  ]
}
```

##### 19.2 List jobs

```sh
scenery durable jobs --service maps --state queued --json
```

##### 19.3 Inspect job

```sh
scenery durable job inspect --service maps job_01J... --json
```

Include:

```text
job row
attempts
events
steps
signals
current lease
```

##### 19.4 Retry

```sh
scenery durable job retry --service maps job_01J... --json
```

Only terminal failed/canceled jobs.

##### 19.5 Cancel

```sh
scenery durable job cancel --service maps job_01J... --reason "operator requested" --json
```

##### 19.6 Tokens

```sh
scenery durable token create --service maps --name gpu-prod --json
scenery durable token list --service maps --json
scenery durable token revoke --service maps <token-id> --json
```

##### 19.7 DB maintenance

```sh
scenery durable db migrate --service maps --json
scenery durable db vacuum --service maps --json
scenery durable db check --service maps --json
```

##### 19.8 Repair

```sh
scenery durable repair --service maps --json
```

Repair should:

```text
- expire stale leases;
- reconcile schedules;
- detect jobs whose task_name is no longer registered;
- detect invalid states;
- optionally recreate missing DB from declarations.
```

---

#### 20. Dashboard / Inspection

Initial dashboard panels:

```text
Durable Overview
  service selector
  counts by state
  oldest queued
  oldest running
  failed last 24h
  workers last seen

Jobs
  filter by service/state/task
  job detail drawer
  attempts/events/steps

Workers
  connected/last-seen workers
  token name
  deployment/build
  labels
  active leases

Schedules
  next fire
  last fire
  overlap policy
  enabled/disabled
```

Avoid real-time push initially.

Use polling:

```text
dashboard polls every 1s or 2s
```

No NATS, no websocket required for v1.

---

#### 21. Error Model

Use structured errors.

Example terminal failure payload:

```json
{
  "type": "handler_error",
  "retryable": false,
  "message": "invalid input",
  "details": {
    "field": "source_object"
  }
}
```

Example retryable failure payload:

```json
{
  "type": "temporary",
  "retryable": true,
  "message": "storage timeout",
  "next_retry_at": "2026-06-27T12:05:00Z"
}
```

Worker protocol errors:

```text
401 unauthorized
403 token scope denied
404 service/job not found
409 stale lease or job state conflict
422 malformed request
429 concurrency/scope limit exceeded
500 runtime error
```

---

#### 22. Idempotency

##### 22.1 Job start idempotency

Every production start should provide:

```text
id
or dedupe_key
```

If `dedupe_key` already exists:

```text
return existing Run
```

Do not enqueue duplicate.

##### 22.2 External side effects

Every job context provides:

```go
ctx.IdempotencyKey()
ctx.StepIdempotencyKey("step-name.v1")
```

Format:

```text
<service>:<job-id>:<attempt-independent-step-key>
```

Example:

```text
maps:job_01J...:charge-card.v1
```

For remote API calls:

```go
stripe.Charge(..., IdempotencyKey(ctx.StepIdempotencyKey("charge.v1")))
```

##### 22.3 Step idempotency

Step key must be stable.

Good:

```text
download-source.v1
detect-roof.v1
write-result.v1
```

Bad:

```text
step-1
loop-index-3
current timestamp
random UUID
```

---

#### 23. Separate DB Dual-Write Recovery

Because the durable DB is separate from the service DB, certain flows can fail between:

```text
service DB commit
durable job insert
```

or:

```text
durable job insert
service DB commit
```

The plan should standardize patterns.

##### Pattern A: Service DB first, durable enqueue second

Use when job derives from committed service state.

```text
1. Commit service mutation.
2. Start durable job with deterministic dedupe_key.
3. If enqueue fails, return warning or mark repair-needed.
4. Repair command can scan service DB for missing jobs.
```

Example:

```text
submission row has status = pending_roof_detection
durable job dedupe_key = roof-detect:<submission-id>
repair finds pending submissions without durable job
```

##### Pattern B: Durable job first, service DB references job

Use when the durable job is the primary entity.

```text
1. Create durable job with deterministic id.
2. Write service DB row referencing job id.
3. If service DB write fails, cancel durable job.
4. Repair finds orphan jobs.
```

##### Pattern C: Service DB pending outbox marker

For critical flows:

```text
service DB transaction:
  insert business row
  insert pending_durable_start row

background repair:
  reads pending_durable_start
  starts durable job idempotently
  marks pending row completed
```

This is the most robust option when separate DBs are required.

This plan should provide helper APIs for Pattern C later, but v1 can document and implement app-specific repair hooks.

---

#### 24. Migrations

##### 24.1 Durable DB creation

Create DB only when:

```text
- service declares at least one durable task;
- service declares at least one durable schedule;
- operator creates token for service;
- command explicitly initializes it.
```

##### 24.2 Migration runner

On open:

```text
BEGIN IMMEDIATE
  create schema_migrations if needed
  read current version
  apply missing migrations
COMMIT
```

Use `locks` table only after base schema exists.

##### 24.3 Version 1 migrations

Migration 1:

```text
meta
schema_migrations
tasks
jobs
job_attempts
job_events
job_steps
job_signals
schedules
workers
worker_tokens
locks
```

##### 24.4 Backward compatibility

Not needed for v1.

After release, every schema change must include:

```text
- forward migration
- compatibility note
- test opening existing v1 DB
```

---

#### 25. Milestones

##### Milestone 1: Design and contracts

Deliver:

```text
- ExecPlan accepted.
- Durable DB filename convention documented.
- Table naming constraint documented.
- No-NATS decision documented.
- Worker protocol JSON schemas drafted.
- Public Go API drafted.
```

Acceptance:

```text
- Plan explicitly says no NATS.
- All table names avoid scenery prefix.
- DB filenames include service name.
- Remote workers use HTTPS long-poll, not DB access.
```

##### Milestone 2: SQLite storage package

Add internal package:

```text
internal/durable/store
```

Responsibilities:

```text
- open DB by service name;
- apply pragmas;
- migrate schema;
- register tasks;
- start jobs;
- claim jobs;
- heartbeat;
- complete;
- fail;
- append events;
- query status/events;
- expire stale leases.
```

Tests:

```text
- creates maps.durable.sqlite;
- no table name contains scenery;
- migrations idempotent;
- WAL mode configured;
- foreign keys enabled;
- busy timeout configured;
- job start idempotency;
- claim race only gives job to one worker;
- stale lease recovery.
```

##### Milestone 3: Public durable API

Add:

```text
durable/
  durable.go
  task.go
  start.go
  context.go
  step.go
```

Implement in-process runtime adapter.

Tests:

```text
- declare task;
- start task;
- execute task locally;
- get result;
- retry failure;
- max attempts terminal failure;
- stable dedupe key returns same job.
```

##### Milestone 4: Runtime integration

Modify runtime:

```text
- app startup opens durable DBs for services with durable tasks;
- generated task registration;
- role handling;
- local worker loop for role all/worker;
- durable HTTP endpoint registration.
```

Tests:

```text
- app without durable tasks creates no durable DB;
- app with maps durable task creates maps.durable.sqlite;
- service DB and durable DB are separate files;
- role api does not execute jobs;
- role worker executes jobs;
- role all executes jobs in local dev.
```

##### Milestone 5: Worker HTTP protocol

Add endpoints:

```text
POST /__scenery/durable/v1/lease
POST /__scenery/durable/v1/heartbeat
POST /__scenery/durable/v1/complete
POST /__scenery/durable/v1/fail
POST /__scenery/durable/v1/event
```

Tests:

```text
- unauthorized request rejected;
- token scope enforced;
- long-poll returns when job appears;
- stale lease completion rejected;
- heartbeat extends lease;
- failure schedules retry;
- terminal completion records result.
```

##### Milestone 6: Worker tokens

Implement:

```text
worker_tokens table use;
token create/list/revoke CLI;
token auth middleware;
scope enforcement.
```

Tests:

```text
- token shown once on create;
- raw token not stored;
- revoked token rejected;
- expired token rejected;
- service scope enforced;
- task kind scope enforced;
- max lease enforced.
```

##### Milestone 7: Remote worker CLI

Implement:

```text
scenery worker durable
```

Capabilities:

```text
- endpoint config;
- token env;
- service subscriptions;
- labels;
- concurrency;
- long-poll;
- heartbeat;
- complete/fail.
```

Tests:

```text
- worker leases remote job;
- worker honors concurrency;
- worker recovers after empty poll;
- worker exits cleanly on SIGINT;
- worker reports missing handler as non-retryable.
```

##### Milestone 8: Schedules

Implement:

```text
schedules table;
schedule reconciler;
interval schedules;
overlap skip;
overlap allow_all;
overlap buffer_one;
catchup window.
```

Tests:

```text
- schedule creates deterministic job id;
- duplicate fire is idempotent;
- skip overlap works;
- catchup window works;
- disabled schedule does not fire.
```

##### Milestone 9: Steps and signals

Implement:

```text
job_steps;
durable.Step;
job_signals;
durable.Signal;
waiting state.
```

Tests:

```text
- completed step result reused after retry;
- failed step can retry;
- signal inserted idempotently;
- waiting job resumes when signal arrives;
- duplicate signal ignored by dedupe key.
```

##### Milestone 10: CLI inspection/admin

Implement:

```text
scenery durable status
scenery durable jobs
scenery durable job inspect
scenery durable job retry
scenery durable job cancel
scenery durable db check
scenery durable repair
```

Tests:

```text
- JSON schemas stable;
- inspect includes attempts/events/steps;
- retry failed job;
- cancel running job;
- repair expires stale lease.
```

##### Milestone 11: Dashboard

Implement basic dashboard:

```text
- service durable status;
- jobs table;
- job detail;
- workers;
- schedules.
```

No push transport required.

##### Milestone 12: legacy async runtime compatibility bridge

Do not reimplement legacy async runtime.

Add migration/compatibility path:

```text
- existing scenery.sh/legacy-async-runtime can remain for now;
- new durable package is preferred;
- selected simple one-activity legacy async runtime workflows can be ported to durable tasks;
- CLI/docs mark legacy async runtime as legacy/heavy backend.
```

---

#### 26. Concrete Implementation Steps

##### Step 1: Add durable config structs

Internal app config:

```go
type DurableConfig struct {
    Enabled *bool `json:"enabled,omitempty"`

    SQLite DurableSQLiteConfig `json:"sqlite,omitempty"`

    Worker DurableWorkerConfig `json:"worker,omitempty"`
}

type DurableSQLiteConfig struct {
    Directory   string `json:"directory,omitempty"`
    Synchronous string `json:"synchronous,omitempty"`
}

type DurableWorkerConfig struct {
    ExposeEndpoint bool `json:"expose_endpoint,omitempty"`
}
```

App config example:

```json
{
  "name": "myapp",
  "durable": {
    "sqlite": {
      "synchronous": "full"
    }
  }
}
```

##### Step 2: Add service durable path resolver

Function:

```go
func DurableDBPath(appStateRoot, serviceName string) (string, error)
```

Returns:

```text
<appStateRoot>/db/<serviceName>.durable.sqlite
```

Tests:

```text
maps -> maps.durable.sqlite
codex -> codex.durable.sqlite
bad/../name rejected
empty rejected
```

##### Step 3: Add schema migration files

Either embedded SQL files:

```text
internal/durable/store/migrations/001_init.sql
```

or Go string constants.

Acceptance:

```text
No CREATE TABLE statement includes scenery in table name.
```

Add test:

```go
func TestDurableSchemaTableNamesDoNotContainScenery(t *testing.T)
```

##### Step 4: Implement store open

```go
type Store struct {
    service string
    path    string
    db      *sql.DB
}

func Open(ctx context.Context, service string, path string, opts Options) (*Store, error)
```

Apply pragmas.

Run migrations.

##### Step 5: Implement task reconciliation

At startup:

```go
store.ReconcileTasks(ctx, []TaskDeclaration)
```

Upsert into `tasks`.

Disable missing tasks?

First version:

```text
do not disable missing tasks automatically;
mark missing in inspect;
avoid surprising production behavior.
```

##### Step 6: Implement Start

```go
func (s *Store) Start(ctx context.Context, req StartRequest) (Job, error)
```

Rules:

```text
- if dedupe key exists, return existing job;
- otherwise insert queued job;
- append job.created event.
```

##### Step 7: Implement claim

```go
func (s *Store) Claim(ctx context.Context, req ClaimRequest) (*ClaimedJob, error)
```

Rules:

```text
- requirements match worker labels;
- task enabled;
- job ready;
- lease assigned atomically;
- attempt inserted;
- event appended.
```

##### Step 8: Implement worker loop

```go
type Worker struct {
    Client WorkerClient
    Registry HandlerRegistry
}
```

Support local and remote clients:

```text
LocalClient -> calls store directly
HTTPClient  -> calls remote endpoint
```

##### Step 9: Implement HTTP endpoints

Keep handler thin:

```text
decode JSON
authenticate
authorize
call durable dispatcher
encode JSON
```

##### Step 10: Implement token auth

Use middleware:

```go
func AuthenticateWorkerToken(r *http.Request) (TokenClaims, error)
```

Lookup token in relevant service DB.

For lease across multiple services:

```text
authenticate token against each candidate service DB;
filter services to those authorized;
claim only authorized services.
```

Optimization later:

```text
token cache with short TTL
```

##### Step 11: Implement scheduler

Per service:

```go
type Scheduler struct {
    store *Store
}
```

Loop:

```text
acquire lock
fire due schedules
sleep min(next_fire_at, 1s)
```

##### Step 12: Implement inspection CLI

Use store read methods.

Do not make CLI parse SQLite directly in many places. Keep access through `internal/durable/store`.

##### Step 13: Add fixture app

Test fixture:

```text
testdata/apps/durable-basic
```

Services:

```text
maps
codex
```

Durable tasks:

```text
maps.echo.v1
maps.fail-then-succeed.v1
codex.echo.v1
```

Acceptance:

```text
running fixture creates:
  maps.durable.sqlite
  codex.durable.sqlite
not:
  durable.sqlite
  scenery.durable.sqlite
```

---

#### 27. Validation and Acceptance

##### 27.1 Unit tests

Required:

```sh
go test ./internal/durable/...
go test ./durable/...
go test ./runtime/...
go test ./cmd/scenery/...
```

##### 27.2 Integration tests

Required:

```sh
go test ./... -run Durable
```

Fixture flows:

```text
- start job from API, worker completes it;
- remote HTTP worker completes job;
- two workers race for one job;
- stale lease expires and another worker completes;
- retryable failure retries;
- non-retryable failure terminal;
- cancellation interrupts heartbeat;
- schedule fires job;
- token scope denies service;
- service DB separate from durable DB.
```

##### 27.3 Self-harness

Add to Scenery self-harness:

```text
- durable fixture check;
- durable CLI JSON schema check;
- no-NATS dependency check;
- table-name constraint check;
- filename constraint check.
```

##### 27.4 No-NATS validation

Add test or harness check:

```text
rg "nats|NATS|JetStream" durable runtime cmd internal
```

Allowed only in:

```text
historical docs/plans
comments explicitly saying not used
```

This plan says do not add NATS. Enforce it.

##### 27.5 Table-name validation

Open generated DB and inspect:

```sql
SELECT name
FROM sqlite_master
WHERE type = 'table';
```

Assert:

```text
no lower(name) contains 'scenery'
```

##### 27.6 Filename validation

Assert durable DB paths match:

```text
<service>.durable.sqlite
```

For service `maps`:

```text
maps.durable.sqlite
```

For service `codex`:

```text
codex.durable.sqlite
```

---

#### 28. Operational Behavior

##### 28.1 Worker in another continent

Recommended settings:

```json
{
  "wait_ms": 30000,
  "lease_ms": 120000,
  "heartbeat_ms": 30000,
  "empty_backoff_ms": 100
}
```

Why:

```text
- long-poll avoids noisy empty requests;
- 120s lease tolerates cross-continent latency and brief network pauses;
- 30s heartbeat detects failure reasonably quickly;
- stale worker cannot complete after lease expiry because lease token is fenced.
```

##### 28.2 Payloads

Job inputs/results should be small.

Use storage references for large data:

```json
{
  "source_object": "storage://private/uploads/source.tif"
}
```

Do not put large files in `input_blob` or `result_blob`.

##### 28.3 Cleanup

Add retention config:

```json
{
  "durable": {
    "retention": {
      "succeeded_for": "7d",
      "failed_for": "30d",
      "events_for": "30d"
    }
  }
}
```

Cleanup command:

```sh
scenery durable prune --service maps --older-than 30d --json
```

First version should not auto-delete unless configured.

##### 28.4 Backup

Durable DB backup is file-level:

```text
maps.durable.sqlite
maps.durable.sqlite-wal
maps.durable.sqlite-shm
```

Use SQLite backup API or stop runtime before copying raw files.

Document:

```text
Do not copy only the .sqlite file while WAL is active unless using SQLite backup/checkpoint correctly.
```

---

#### 29. Compatibility with Current legacy async runtime Surface

Current repo has substantial legacy async runtime integration: config resolution, runtime starter hooks, workers, cron, typed workflows/activities, start options, signals, queries, updates, and worker deployment logic.

This plan should not port all of that 1:1.

Map only what is needed:

| legacy async runtime concept    | Durable replacement                       |
| ------------------- | ----------------------------------------- |
| Workflow            | Task or multi-step task                   |
| Activity            | Task handler or Step                      |
| Task queue          | Service + task kind + labels              |
| Workflow ID         | Job ID                                    |
| Conflict policy     | Dedupe key / existing job return          |
| Retry policy        | Retry fields on task/job                  |
| Signal              | `job_signals`                             |
| Query               | `Status` / `Events` read APIs             |
| Update              | explicit API endpoint or signal + handler |
| Schedule            | `schedules` table                         |
| Worker deployment   | deployment_id/build_id metadata only      |
| legacy async runtime UI         | Scenery durable dashboard                 |
| legacy async runtime dev server | none                                      |

The completed legacy async runtime hardening plan shows complexity around deterministic IDs, task queues, process-local waiters, deployment promotion, cron policy, TLS/API keys, and worker manifests. That complexity is precisely what this plan avoids by owning a narrower runtime.

---

#### 30. Risks and Mitigations

##### Risk: Separate durable DB creates dual-write gaps

Mitigation:

```text
- deterministic job IDs;
- dedupe keys;
- repair command;
- documented patterns;
- optional pending_durable_start table in service DB for critical flows.
```

##### Risk: Remote worker completes stale job

Mitigation:

```text
- lease token;
- lease expiry check;
- lease_id check;
- token hash check;
- reject stale completion with 409.
```

##### Risk: Two workers claim same job

Mitigation:

```text
- atomic UPDATE ... RETURNING;
- lease fields updated only from ready states;
- only one update wins.
```

##### Risk: Worker token leaked

Mitigation:

```text
- scopes;
- expiration;
- revocation;
- no raw token storage;
- HTTPS only;
- token last_used_at;
- dashboard visibility.
```

##### Risk: High empty-poll traffic

Mitigation:

```text
- long-poll;
- 100ms server-side DB scan;
- 30s wait;
- local notify channel optimization.
```

##### Risk: SQLite writer contention

Mitigation:

```text
- one durable DB per service;
- short transactions;
- WAL;
- busy_timeout;
- read-before-write in claim path;
- avoid heartbeat every second;
- batch event writes only if needed later.
```

##### Risk: Job event table grows forever

Mitigation:

```text
- retention config;
- prune command;
- archive/export later if needed.
```

##### Risk: Worker cannot reach service endpoint

Mitigation:

```text
- require public HTTPS endpoint or tunnel;
- no broker in v1;
- operator must deploy reachable app/runtime endpoint.
```

---

#### 31. Documentation Updates

Update:

```text
README.md
ARCHITECTURE.md
docs/local-contract.md
docs/app-development-cookbook.md
docs/environment.md
SKILL.md
AGENTS.md
docs/schemas/*.json
```

Add new docs:

```text
docs/durable.md
docs/durable-worker-protocol.md
docs/durable-sqlite-schema.md
```

Document:

```text
- no NATS;
- DB filename convention;
- no scenery in table names;
- worker token model;
- long-poll subscription model;
- at-least-once semantics;
- separate DB dual-write caveat;
- repair patterns;
- remote worker deployment examples.
```

---

#### 32. Acceptance Criteria

The feature is acceptable when all are true:

```text
- An app with no durable tasks creates no durable DB.
- A service with durable tasks creates <service>.durable.sqlite.
- Durable DB is separate from <service>.sqlite.
- Durable DB filename contains service name.
- Durable DB table names do not contain scenery.
- No NATS dependency is added.
- Local worker can execute jobs.
- Remote worker can long-poll over HTTPS and execute jobs.
- Worker auth uses long high-entropy bearer token.
- Token scopes can allow all services or only selected services.
- Deployment config controls worker subscriptions.
- Worker lease has fencing token.
- Stale completion is rejected.
- Expired leases are retried or failed.
- Retry/backoff works.
- Status/events are inspectable.
- Schedules can enqueue jobs.
- CLI can list, inspect, retry, cancel, and repair jobs.
- Dashboard can show basic durable state.
```

---

#### 33. Suggested Initial PR Breakdown

##### PR 1: Durable schema/store foundation

```text
- DB path resolver
- migrations
- store open
- task reconciliation
- Start
- basic Status
- schema tests
```

##### PR 2: Claim/lease/attempts

```text
- Claim
- heartbeat
- complete
- fail
- stale lease scanner
- race tests
```

##### PR 3: Public `durable` package

```text
- NewTask
- Start
- Run
- Get
- local handler registry
- in-process worker loop
```

##### PR 4: Runtime/codegen integration

```text
- service task discovery
- generated registration
- role handling
- DB creation only when needed
```

##### PR 5: Worker HTTP API and token auth

```text
- lease endpoint
- heartbeat endpoint
- complete/fail endpoints
- worker_tokens table use
- token CLI
```

##### PR 6: Remote worker CLI

```text
- scenery worker durable
- config file support
- subscriptions
- labels
- concurrency
```

##### PR 7: Schedules

```text
- schedules table
- reconciler
- interval schedule
- overlap skip/allow_all/buffer_one
```

##### PR 8: Steps/signals

```text
- job_steps
- durable.Step
- job_signals
- durable.Signal
- waiting/resume
```

##### PR 9: CLI inspect/admin/dashboard

```text
- durable status/jobs/inspect/retry/cancel/repair
- dashboard panels
```

##### PR 10: legacy async runtime migration docs

```text
- port cookbook examples
- mark legacy async runtime as legacy/heavy backend where appropriate
- document durable-first guidance
```

---

#### 34. Final Architecture Summary

```text
per service:
  <service>.sqlite
    normal service data

  <service>.durable.sqlite
    tasks
    jobs
    attempts
    events
    steps
    signals
    schedules
    workers
    tokens
    locks

runtime:
  opens durable DBs only for services that need them
  owns SQLite writes
  exposes HTTPS worker API
  long-polls with 100ms internal scan
  leases jobs with fencing tokens
  records all state durably

worker:
  authenticates with long bearer token
  subscribes to all or selected services/task kinds
  gets leased jobs
  heartbeats
  completes/fails
  never opens SQLite
  never talks to NATS
```

This is the pragmatic replacement for legacy async runtime in Scenery: small, inspectable, local-first, remote-worker capable, and operationally boring.

## Interfaces and Dependencies

New public package:

```text
scenery.sh/durable
```

New likely internal packages and surfaces:

```text
internal/durable/store
internal/durable/runtime
internal/durable/protocol
cmd/scenery durable ...
scenery worker durable
POST /__scenery/durable/v1/lease
POST /__scenery/durable/v1/heartbeat
POST /__scenery/durable/v1/complete
POST /__scenery/durable/v1/fail
POST /__scenery/durable/v1/event
POST /__scenery/durable/v1/step/start
POST /__scenery/durable/v1/step/complete
```

Dependencies and constraints:

* Build on `0088`'s per-service SQLite database model.
* Prefer Go standard library and existing SQLite choices from `0088`; do not add a broker dependency.
* Do not add NATS, Redis, Kafka, or a legacy async runtime-compatible replay engine.
* Remote workers use HTTPS long-polling and bearer tokens.
* SQLite is local to the owning runtime and never opened remotely.
* Durable table names must not contain `scenery`.
* Durable DB filenames must contain the service name and use `<service>.durable.sqlite`.
* Large job payloads should use storage references instead of storing large blobs in job inputs/results.
