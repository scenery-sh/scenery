# Bounded Development Runtime RPC Execution

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

The development runtime RPC (`/runtime`, documented in
[the local contract](../local-contract.md#development-runtime-rpc)) is the
JSON-RPC 2.0 WebSocket that app-owned developer tooling such as ONLV's bottom
panel uses to read runtime status, browse PostgreSQL, run SQL and maintain
local storage. An external review dated 2026-09-24 (improvement 1) found that
its server has no execution bounds:

- `handleWebSocket` in `cmd/scenery/dashboard.go` starts one goroutine per
  request through `calls.Go` without a concurrency limit, admission bound or
  per-request deadline. The serialized response writes and
  `dashboardClientWriteTimeout` bound only writing, not execution.
- `db/query` (`queryDB` and `scanRuntimeRows` in the same file) reads every row
  of an unrestricted statement into memory.
- A slow statement cannot be told apart from a stuck one, and nothing keeps
  `status` responsive while slow statements occupy the runtime.

After this plan, the runtime bounds the work any client can start and keeps
cheap control calls responsive:

- Each connection runs at most 6 database or storage calls ("work calls") and,
  in a separate reserved allowance, at most 4 control calls (`status`,
  `traces/clear`). Each app runs at most 12 work calls across all connections.
  A call beyond a limit is refused at once with a documented JSON-RPC error
  (`SCN8011`, `capacity_exhausted`); nothing waits in a queue.
- Every call has a deadline (5 s for control calls, 30 s for work calls);
  expiry cancels the call and answers `SCN8012` (`deadline_exceeded`).
- `db/query` answers at most 5,000 rows and 4 MiB of JSON-encoded rows;
  `postgres/rows` keeps its 500-row page cap and gains the same 4 MiB budget.
  A larger result stops reading, cancels the statement and answers `SCN8013`
  (`result_too_large`) instead of accumulating it.
- Closing the WebSocket cancels every call of that connection, and cancellation
  reaches PostgreSQL: the server stops the running statement instead of
  finishing it for a caller that has gone. A result stopped at its budget is
  cancelled rather than drained from the server.

Observable result: against a running `scenery up`, a tool that floods
`db/query` with `select pg_sleep(20)` sees at most 6 statements start per
connection and immediate `SCN8011` refusals beyond them, `status` keeps
answering within milliseconds, `select generate_series(1, 100000000)` fails
fast with `SCN8013`, and disconnecting removes the sleeping statements from
`pg_stat_activity` within seconds.

## Progress

- [x] (2026-09-24 14:06 CEST) Mapped the RPC loop (`cmd/scenery/dashboard.go`),
  dispatch (`cmd/scenery/dashboard_rpc.go`), PostgreSQL inspection
  (`cmd/scenery/dashboard_postgres.go`), storage RPC
  (`cmd/scenery/dashboard_storage.go`), the `/runtime` tunnel
  (`cmd/scenery/local_path_router.go`), pgx cancellation behavior, the
  diagnostic catalog, the generated client and its consumer in ONLV.
- [x] (2026-09-24 14:06 CEST) ExecPlan written and indexed.
- [x] (2026-09-24 14:16 CEST) Milestone 1: call classes, per-connection and
  per-app admission, deadlines, disconnect cleanup, in-process acceptance test
  (`go test ./cmd/scenery -run 'TestRuntimeRPC|TestDashboard'` passes, also
  with `-race -count=3`).
- [x] (2026-09-24 14:16 CEST) Milestone 2: result budgets for `db/query` and
  `postgres/rows`; an exceeded budget cancels the statement before its rows
  close. Server-side cancellation verified against PostgreSQL 18 (see
  Surprises); no driver change was needed.
- [x] (2026-09-24 14:18 CEST) Milestone 3: catalog diagnostics
  `SCN8011`–`SCN8013`, failure objects in `error.data`, the four committed
  generated clients regenerated for the new spec revision, client
  documentation, and a `DevRuntimeClient runtime failures` test, now in
  `internal/generate/testdata/dev_runtime_client.test.ts` (see the merge
  entry below).
- [x] (2026-09-24 14:21 CEST) Milestone 4: `docs/local-contract.md`
  (execution bounds, failure table, request-failure range),
  `docs/agent-guide.md`, `ARCHITECTURE.md`, `docs/harness-engineering.md` and
  the worktree fixture's `AGENTS.md` and `README.md`.
- [x] (2026-09-24 14:21 CEST) Milestone 5 implemented: worktree probe row A19
  in `scripts/verify/harness_self_worktree_rpc_bounds.go`, run after A4;
  `acceptance_rows` is 18.
- [x] (2026-09-24 14:31 CEST) A19 passed against real PostgreSQL in the second
  probe run (evidence under Artifacts); that run failed A9 on the pruned
  fixture module (see Surprises). The third run failed A9 on go-sdk v1.8.0;
  the fixture module was reworked and checked by an offline-cache emulation.
- [x] (2026-09-24 14:23 CEST) Milestone 6, first part: full verifier, lint and
  the `ui` probe pass (see Artifacts).
- [x] (2026-09-24 14:55 CEST) Merged `main` (environment configuration,
  telemetry report, dev runtime client lifecycle): renumbered this plan 0206,
  kept `main`'s client and contract changes, reapplied the client
  documentation, moved the client test into `dev_runtime_client.test.ts`,
  regenerated the four committed clients for the combined spec revision.
- [x] (2026-09-24 15:02 CEST) The fourth `--probe worktree` run (started
  14:43 CEST) stopped when the machine's data volume filled up (296 MiB free,
  OrbStack's Docker daemon gone) and could not write its report; it had not
  reached A9. After Docker returned, its eight retained PostgreSQL clusters
  were removed with `scenery down` and `scenery prune --db --older-than 1ns`
  under the probe's agent home; the merged binary refused the records
  (`SCN8003`, unexpected `scenery.worktree` spec revision), so a binary built
  from the pre-merge commit `6acf618c` did it. Containers, volumes and the
  retained root are gone.
- [ ] Milestone 6, remaining: a complete `go run ./scripts/verify --probe
  worktree --summary --write` run on the merged branch with the final fixture
  module (A9 included).

## Surprises & Discoveries

- A first reading of pgx v5.11.0 suggested that a cancelled context leaves the
  statement running on the server: the default `DeadlineContextWatcherHandler`
  (`pgconn/config.go`) only sets a deadline on the client socket. A scratch
  test against a disposable `postgres:18-alpine` container disproved it: with
  plain `postgresdb.Open`, `select pg_sleep(5)` under a 300 ms context returned
  after 301 ms and `pg_stat_activity` showed no active statement 200 ms later.
  The interrupted read makes `ResultReader.receiveMessage` call
  `PgConn.asyncClose` (`pgconn/pgconn.go`), which sends PostgreSQL a cancel
  request before closing the socket. The planned `postgresdb.OpenCancelable`
  (a `CancelRequestContextWatcherHandler` pool) returned 100 ms later with the
  same server outcome, so it was dropped. The real-process proof stays in
  worktree probe row A19.
- pgx `Rows.Close` drains the rest of an unfinished result
  (`pgconn.ResultReader.Close` "consumes any remaining result data"). Measured
  against the same container with `select generate_series(1, 100000000)`:
  after 5,001 rows, closing without cancelling ran until the 3 s context
  deadline, while cancelling first closed at once. The budget path therefore
  cancels the statement's context before closing its rows.
- `select g from generate_series(1, 100000000) g` materializes the function
  result before the first row (about 4.5 s), so probes use the target-list form
  `select generate_series(…)`, which streams.
- `http.Server.Close` and `Shutdown` do not close hijacked connections, so the
  RPC WebSockets of a closed runtime backend stay open until their clients
  leave. Recorded as a follow-up; see the Decision Log.
- The review named `internal/generate/testdata/dev_runtime_client.test.ts` as
  the generated client's behavior tests. The file did not exist when this plan
  started (`git log --all` over that path was empty); `main` added it in
  parallel (client lifecycle work, merged before this branch) and runs it in
  the `ui` probe, so the runtime-failure test moved there during the merge.
- `main` allocated plan number 0205 (environment configuration) while this
  plan was written, so this plan was renumbered 0206 when the branch merged
  `main`; the spawned follow-up tasks still name it 0205.
- `docs/local-contract.md` says request failures use `SCN8001` through
  `SCN8005`, stale since the storage diagnostics `SCN8006`–`SCN8010`.
- The first `go run ./scripts/verify --probe ui --probe worktree --summary
  --write` (2026-09-24 14:23 CEST) failed in row A1, before A19: `scenery up`
  of the fixture answered `SCN9000` because its database setup command
  (`go run ./cmd/schema`, `.scenery.json`) printed `go: updates to go.mod
  needed; to update it: go mod tidy`. The dependency bump `ad1998b1`
  (2026-09-21) raised the framework module's requirements (pgx 5.11.0,
  golang.org/x/net 0.58.0, …) while `testdata/apps/worktree-postgres/go.mod`
  still listed pgx 5.10.0 and indirect modules that plan 0204's smaller SDK no
  longer needs. The probe is therefore broken on `main` independently of this
  plan. `testdata/apps/basic`, `multiservice`, `storage-basic` and
  `testdata/assistant` pin the same stale versions; they fail only where the
  app itself runs `go` read-only.
- The second run (14:25 CEST), with a `go mod tidy`-pruned fixture module,
  passed A1–A8, A10–A17 and A19 but failed A9: the historical producer
  (`c56e3e96`) in the nested sandbox runs an offline `go mod tidy`
  (`GOPROXY=off`) on `/app-old`, whose `go.mod` is the same fixture with
  `replace scenery.sh => /baseline`. Its runtime still imports
  `mcpfederation` → `go-sdk` → `segmentio/*` → `golang.org/x/sys`, and the
  lookup failed (`legacy-sandbox/failure-legacy-state/agent/dev/app-old-*.log`:
  "module lookup disabled by GOPROXY=off") for x/sys v0.47.0, which the listed
  `golang.org/x/net` v0.58.0 selects and nothing had cached.
- The third run (14:37 CEST) listed the historical module set at the current
  framework's versions and failed A9 again, now on `go-sdk` v1.8.0: the
  sandbox's offline module cache holds only what `go mod download all` of the
  historical source and the current CLI build fetch (go-sdk v1.6.1, x/sys
  v0.46.0 and v0.48.0, …), and the current CLI no longer imports go-sdk. An
  emulation with a fresh `GOMODCACHE` filled the same two ways reproduced that
  failure exactly and accepted the final module (the tidy set plus
  `golang.org/x/sys v0.48.0`): offline `go mod tidy` and `go build` against the
  `c56e3e96` source, with its runtime imported, both exit 0.

## Decision Log

- Decision: classify calls. Work calls are `postgres/tables`,
  `postgres/schema`, `postgres/rows`, `db/query` and every `storage/` method
  (they open the app's database or storage and can be slow). Every other
  method is a control call: `status`, `traces/clear`, and unknown methods, which
  fail at once. Rationale: the resources that need protection are the app's
  database and storage; control calls read runtime state only. Date:
  2026-09-24. Author: Claude.
- Decision: admission never waits. The read loop admits or refuses each request
  before reading the next one; a refusal is written at once on the connection
  (bounded by the existing 1 s write deadline) and never spawns a goroutine.
  Rationale: the review asks for bounded admission, not an unbounded queue; a
  refusal is retryable and TanStack Query in ONLV already retries failed
  queries. Date: 2026-09-24. Author: Claude.
- Decision: the control allowance is a separate per-connection pool of 4 that
  work calls can never occupy. Rationale: "reserved allowance" for `status`;
  separate pools are simpler to state and to test than a shared pool with a
  reserve. Date: 2026-09-24. Author: Claude.
- Decision: limits are 6 work calls per connection, 12 work calls per app and 4
  control calls per connection. Rationale: ONLV shares one client across its
  runtime, database and storage tabs and can issue about five work calls when
  they mount (tables, columns, rows, storage inspection, listing); six keeps
  that normal burst admitted while bounding runaway loops, and twelve admits
  two such clients (two browser tabs) per app. Twelve concurrent calls keep the
  runtime's PostgreSQL connections well below the server's default 100. Date:
  2026-09-24. Author: Claude.
- Decision: the per-app key is the request's trimmed `app_id`, or the
  backend's default app when it is empty. Rationale: `app_id` is how the
  contract addresses an app; resolving a canonical identity would add a status
  lookup before admission. Two spellings of one app (session route id and base
  app id) receive separate budgets; the limit guards against runaway tooling,
  not an adversary. Date: 2026-09-24. Author: Claude.
- Decision: deadlines are 5 s for control calls and 30 s for work calls,
  fixed by the server. Rationale: the RPC is inspection-grade tooling; long
  statements belong in `scenery db shell` or migrations. A client-chosen
  timeout adds contract surface without a current need. Date: 2026-09-24.
  Author: Claude.
- Decision: an expired deadline answers `SCN8012` unless a storage failure more
  specific than cancellation describes the outcome (for example `SCN8010`
  partial completion with progress details). Rationale: a partially completed
  mutation must keep its recovery information. Date: 2026-09-24. Author: Claude.
- Decision: result budgets fail instead of truncating. `db/query` answers at
  most 5,000 rows and 4 MiB (4,194,304 bytes) of JSON-encoded rows;
  `postgres/rows` keeps its page limit and the same byte budget. On overflow the
  runtime cancels the statement and answers `SCN8013` with `max_rows`,
  `max_bytes` and `rows_within_budget` details. Rationale: `db/query` runs
  arbitrary statements outside a transaction, so a truncated "success" of an
  `UPDATE … RETURNING` would be misleading, and agents must not mistake a
  partial answer for a complete one ("branch on stable diagnostic codes").
  `rows_within_budget` lets a table browser retry `postgres/rows` with a
  smaller page. Cancellation cannot undo a statement PostgreSQL already
  finished; the contract says so. Date: 2026-09-24. Author: Claude.
- Decision: add catalog diagnostics `SCN8011 capacity_exhausted`,
  `SCN8012 deadline_exceeded` and `SCN8013 result_too_large`, carried in
  `error.data` as the same failure object storage failures use (`code`,
  `diagnostic`, `message`, `details`); the JSON-RPC `error.code` stays
  `-32000`. Rationale: the root instructions keep request failures in the
  checked catalog and clients branch on diagnostics; one failure-object shape
  per contract. The spec revision changes, so every committed generated client
  that pins it is regenerated (as in plan 0202). Date: 2026-09-24. Author:
  Claude.
- Decision: keep pgx's default context handling for the runtime's pools; drop
  the planned `postgresdb.OpenCancelable`. Rationale: the default already
  sends a cancel request when a context interrupts a statement (see Surprises)
  and returns sooner; a second pool constructor would add surface without a
  behavior difference. Date: 2026-09-24. Author: Claude.
- Decision: `testdata/apps/worktree-postgres/go.mod` lists what `go mod tidy`
  keeps for the current framework (pgx v5.11.0, x/net v0.58.0, x/sync v0.23.0,
  x/text v0.42.0 and the pgx helpers) plus `golang.org/x/sys v0.48.0`, and
  `go.sum` is that tidy result with the x/sys hashes. The current framework
  selects exactly these versions (`go build -mod=readonly ./...` passes and
  `go run ./cmd/schema` fails only on the missing database URL). Modules only
  the historical A9 producer needs (go-sdk, segmentio, oauth2, …) stay
  unlisted, so its offline tidy selects the versions its own source requires,
  which its `go mod download all` cached; listing x/sys pins the one module
  whose selection would otherwise move to an uncached version. Rationale: the
  requested real-PostgreSQL proof runs through this fixture and cannot start
  without it; the change touches only the fixture's module requirements. The
  other stale fixture modules are left to a separate task. Date: 2026-09-24.
  Author: Claude.
- Decision: out of scope, recorded as follow-ups: a WebSocket frame size limit
  (today a request frame is read without a size limit), a cap on concurrent
  RPC connections, closing RPC WebSockets when the backend closes, write
  deadlines that scale with large results, budgets for `postgres/tables` and
  `postgres/schema` (bounded by the catalog size), and the separate HTTP
  storage transfers (`/runtime/storage`). Rationale: the review item names
  concurrency, admission errors, deadlines, SQL result budgets, the control
  allowance and disconnect cancellation; the rest would silently widen the
  assignment. Date: 2026-09-24. Author: Claude.

## Outcomes & Retrospective

Not yet completed; open for one complete `--probe worktree` run with the final
fixture module. Implemented and verified: the development runtime RPC admits
at most 6 work and 4 control calls per connection and 12 work calls per app,
refusing the excess at once with `SCN8011`; calls stop at their deadline with
`SCN8012`; `db/query` and `postgres/rows` fail with `SCN8013` instead of
accumulating oversized results and cancel the statement rather than draining
it; closing a connection cancels its statements in PostgreSQL. Against real
PostgreSQL, `status` stayed below 30 ms while every work slot held a sleeping
statement, and abandoned statements left the server about 50 ms after the
disconnect. The scope grew by one fixture repair: the `worktree` probe had
been unable to start on `main` since the 2026-09-21 dependency bump.

## Context and Orientation

Terms used here:

- **Runtime backend**: the worktree runtime owner's HTTP listener
  (`cmd/scenery/worktree_runtime_owner.go` starts it through
  `startAgentDashboardListener` in `cmd/scenery/agent_dashboard.go`; the
  in-process supervisor starts its own in `cmd/scenery/dev_supervisor.go` when
  no agent owns the session). Internally it is `dashboardServer`
  (`cmd/scenery/dashboard.go`). One backend serves every session of its
  worktree.
- **Development runtime RPC**: the JSON-RPC 2.0 WebSocket the backend serves at
  `/__scenery`; the app origin's `/runtime` path tunnels to it
  (`tunnelLocalBackendUpgrade` in `cmd/scenery/local_path_router.go` hijacks
  the client connection and copies bytes both ways, so a client disconnect
  closes the backend connection).
- **Connection loop**: `dashboardServer.handleWebSocket` reads one request at a
  time, runs it in a goroutine and writes responses through
  `dashboardClient.writeJSON` (mutex plus 1 s write deadline). Its context is
  cancelled and every call awaited when the loop ends.
- **Dispatch**: `dashboardServer.handleRPC` and `dispatchRPC` in
  `cmd/scenery/dashboard_rpc.go`. Failures are `-32000` errors; storage failures
  (`cmd/scenery/dashboard_storage.go`, `dashboardStorageFailure`) add
  `error.data`, the `storagefs.Failure` object.
- **PostgreSQL access**: `openPostgresDashboardDB` (in `dashboard.go`) resolves
  the app's development database and opens a fresh `*sql.DB` per call through
  `postgresdb.Open` (`internal/postgresdb/postgresdb.go`, pgx stdlib driver).
  `postgres/rows` and `db/query` scan with `scanRuntimeRows`.
- **Diagnostic catalog**: `internal/spec/diagnostics_catalog.go`; `SCN8000`–
  `SCN8099` is the `request_protocol` category. The catalog is part of the spec
  revision pinned by committed generated clients.
- **Generated client**: `internal/generate/dev_runtime_client.ts`, embedded and
  written as `dev-runtime.ts` when a `typescript_client` sets
  `dev_runtime = true` (committed in the `house` fixture under
  `internal/compiler/testdata/house/clients/generated/public_api/`). Its
  `DevRuntimeError` exposes `diagnostic` and `details` from `error.data`.
  ONLV (`apps/nextnext/src/features/devRuntime`) shares one client across its
  bottom-panel tabs, polls `status` every 2 s and loads data with TanStack
  Query.
- **Worktree probe**: `go run ./scripts/verify --probe worktree`, the explicit
  real-process proof with managed PostgreSQL (`scripts/verify/
  harness_self_worktree_*.go`, fixture `testdata/apps/worktree-postgres`).

## Milestones

1. Admission, deadlines and cleanup. New `cmd/scenery/dashboard_rpc_limits.go`
   owns call classes, limits, the per-app admission table and runtime failure
   objects; `handleWebSocket` admits before spawning. Proof:
   `go test ./cmd/scenery -run 'TestRuntimeRPC'` including the in-process
   acceptance test described under Validation and Acceptance.
2. Result budgets and statement cancellation. `scanRuntimeRows` enforces row
   and byte budgets and returns JSON-encoded rows; `queryDB` and `postgresRows`
   cancel the statement before closing rows on overflow. Proof:
   `go test ./cmd/scenery`, plus the scratch PostgreSQL measurements recorded
   in Surprises.
3. Diagnostics and clients. Catalog rows `SCN8011`–`SCN8013`, regenerated
   committed clients, client documentation, and a test in
   `internal/generate/testdata/dev_runtime_client.test.ts` proving the
   failure objects reach `DevRuntimeError`. Proof: `go test ./internal/spec
   ./internal/compiler ./internal/contractagent ./internal/generate`, fixture
   regeneration, `--probe ui`.
4. Documentation: `docs/local-contract.md` (Development Runtime RPC and the
   request-failure sentence), `docs/spec/typescript-client.md` only if the
   client's exported surface changes, `ARCHITECTURE.md`, `docs/harness-
   engineering.md` and the fixture's `AGENTS.md` for the new probe row. Proof:
   the verifier's knowledge checks.
5. Real PostgreSQL proof: worktree probe row A19. Proof: `go run
   ./scripts/verify --probe worktree --summary --write`.
6. Validation union and outcomes.

## Plan of Work

Milestone 1 adds `runtimeRPCLimits` (fields for the three concurrency limits,
two deadlines, the query row budget and the result byte budget) with documented
defaults, and a `runtimeRPC` value on `dashboardServer` holding the limits and
a mutex-guarded map of active work calls per app key. `handleWebSocket` keeps
two buffered-channel semaphores per connection. For each request it classifies
the method, tries the connection semaphore of that class without blocking, and
for work calls also tries the app table; a refusal releases whatever it took
and writes the `SCN8011` response synchronously (notifications are dropped).
An admitted call runs in a goroutine under
`context.WithTimeoutCause(connectionContext, deadline, errRuntimeCallDeadline)`
and releases its slots when it returns. `handleRPC` maps a cause of
`errRuntimeCallDeadline` to `SCN8012` unless the error is a runtime failure or a
storage failure whose code is not `canceled`, and puts any runtime failure
object in `error.data`. A server hook `openDatabase` (in
`dashboardServerHooks`, defaulting to `openPostgresDashboardDB`) lets in-process
tests substitute a `database/sql` driver built with `sql.OpenDB`, so admission,
deadlines and cancellation are proven without PostgreSQL. `queryDB` switches to
`openDashboardPostgres` so both PostgreSQL paths share the hook and the app-root
check.

Milestone 2 changes `scanRuntimeRows(rows, budget)` to encode each row with
`encoding/json` as it is read (`[]byte` values still become strings first),
count the exact encoded size of the `rows` array, and fail with `SCN8013` when
the row or byte budget is exceeded; results carry `[]json.RawMessage` rows, so
the wire shape is unchanged and rows are encoded once. A value that JSON cannot
encode (a PostgreSQL `NaN` float) now fails the call instead of breaking the
connection's response write. `queryDB` and `postgresRows` run the statement
under a child context and cancel it before closing the rows, so the driver
does not drain the rest. PostgreSQL pools keep pgx's default context handling,
which already cancels an interrupted statement on the server.

Milestone 3 adds the three catalog rows, regenerates every committed generated
client whose descriptor pins the spec revision (house, native,
`testdata/apps/worktree-postgres`, `testdata/assistant`), documents the runtime
failure diagnostics on `DevRuntimeError` and the budgets on `query` and
`postgresRows` in `internal/generate/dev_runtime_client.ts`, and adds a test
to `internal/generate/testdata/dev_runtime_client.test.ts` with its fake
WebSocket: a `SCN8011` failure rejects only its own call with code `rpc`,
diagnostic and details while the connection keeps serving the next call.

Milestone 4 documents the classes, limits, deadlines, budgets, error shapes and
cancellation in the Development Runtime RPC section, and fixes the stale
request-failure range sentence.

Milestone 5 adds scenario A19 to the worktree probe after A4, while worktree A
serves managed SQL. Through the app origin's `/runtime` it: sends 8 `db/query`
`select pg_sleep(20)` statements tagged with a unique comment on one connection
and requires 6 to start (counted in `pg_stat_activity` through a direct
connection to the worktree's PostgreSQL) and 2 immediate `SCN8011` refusals;
fills the app's remaining 6 work calls from a second connection and requires a
third connection's work call to be refused with `details.scope = "app"` while
its `status` succeeds; measures 20 `status` calls during the load and requires
each below 250 ms; requires `select generate_series(1, 100000000)` to answer
`SCN8013` within 5 s; closes every connection and requires the tagged
statements to disappear from `pg_stat_activity` within 5 s (far below their
20 s sleep); and verifies the runtime still answers `status` afterwards.

## Concrete Steps

Working directory: the repository root of this worktree.

    go test ./cmd/scenery -run 'TestRuntimeRPC|TestDashboard'
    go test ./internal/spec
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/apps/worktree-postgres -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
    go test ./internal/compiler ./internal/contractagent ./internal/generate
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write
    go run ./scripts/verify --probe ui --probe worktree --summary --write

## Validation and Acceptance

Changed-area classes from the root validation matrix: Go packages
(`cmd/scenery`, `internal/spec`, `scripts/verify`),
compiler or generator (the catalog changes the spec revision and the generated
client template changes), and release-sensitive or runtime (runtime RPC
behavior). The full verifier is therefore selected, not quick:
`go run ./scripts/verify --summary --write` from the repository root, which
refreshes `.scenery/harness/agent-context.json`; the union in its
`changed_area.recommended_commands` is fulfilled in addition to the Concrete
Steps. Both committed fixture regenerations are required. Changed external
boundaries and their probes: the runtime RPC with real PostgreSQL
(`go run ./scripts/verify --probe worktree --summary --write`, row A19 with its
assertions and the probe's verified cleanup) and the generated TypeScript
client (`go run ./scripts/verify --probe ui --summary --write`: client
conformance including the dev runtime block, generated-client and catalog
typechecks). Release certification (`scripts/release-gate.sh`) and timing
audits are not selected without an explicit request.

In-process acceptance (Milestone 1, `cmd/scenery`, below 100 ms isolated): one
test drives the real WebSocket handler over loopback with a `database/sql`
driver whose statements block until their context ends. It proves:

- bounded active work: 8 `db/query` calls on one connection start exactly 6
  statements and answer 2 `SCN8011` refusals with `scope` `connection`; a second
  connection starts 6 more; a third connection's work call is refused with
  `scope` `app`, while a call for another `app_id` is admitted;
- status latency stays low: while every admitted statement is still blocked,
  repeated `status` calls on every connection answer, each within 250 ms, and
  the blocked statements are still running afterwards;
- cancellation cleanup: closing a connection cancels each of its statements'
  contexts, closes their databases, returns the handler and releases the app's
  slots; after all connections close, no statement is active and the app table
  is empty.

Further in-process tests cover the deadline (`SCN8012` with `deadline_ms`
after a shortened work deadline, statement context cancelled), the row and byte
budgets for `db/query` and `postgres/rows` (`SCN8013` details, the statement
cancelled before its rows closed, no rows read past the overflow), storage
partial-completion failures surviving an expired deadline, and the documented
default limits.

Real PostgreSQL acceptance is worktree probe row A19 as described in the Plan
of Work; the probe records its measured status latencies, admission counts and
cleanup time as evidence.

## Idempotence and Recovery

Source edits and generation are repeatable; regeneration rewrites identical
bytes. The worktree probe creates disposable Git worktrees, agent home and
PostgreSQL containers and removes them through its verified cleanup; a failed
run keeps its private probe root for diagnosis and prints it in the summary.
The A19 statements sleep at most 20 s, so even a failed cancellation ends on
its own. If `tools/typescript/node_modules` is missing, the `ui` probe
provisions it from the frozen lockfile.

## Artifacts and Notes

Validation (repository root of this worktree, 2026-09-24):

- `go test ./cmd/scenery -run 'TestRuntimeRPC|TestDashboard' -count=1`: pass;
  the acceptance test runs in under 10 ms, the deadline test in 20 ms;
  `-race -count=3`: pass.
- `go test ./cmd/scenery ./scripts/verify ./internal/spec ./internal/generate
  ./internal/compiler ./internal/contractagent ./internal/graph`: pass.
- Fixture regenerations (house, native, `testdata/apps/worktree-postgres`,
  `testdata/assistant`): `ok: true`, no diagnostics; rerunning house and
  native changed nothing.
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts
  internal/generate/testdata/dev_runtime_client.test.ts` (after merging
  `main`): 48 pass.
- `golangci-lint run ./...`: 0 issues.
- `go run ./scripts/verify --summary --write`: `pass_with_warnings`; the
  warnings (review-due knowledge entries, architecture hotspots, cached Go
  suite over its advisory 5 s budget) predate this plan. Changed-area classes:
  `cli-json-contract`, `compiler-or-generator`, `go-package`,
  `release-sensitive-or-runtime`; recommended commands all run above.
- `go run ./scripts/verify --probe ui --summary --write`: TypeScript
  dependencies, client conformance, generated-client and catalog typechecks
  pass.
- `go run ./scripts/verify --probe worktree --summary --write`, run 2 and
  run 3: A1–A8, A10–A17 and A19 pass, A9 fails on the fixture module (see
  Surprises); verified cleanup of owned clusters both times. A19 evidence,
  run 2 / run 3: 6 statements admitted per connection and 12 per app, 12
  sleeping statements in `pg_stat_activity`, 20 `status` calls under
  saturation with p50 17 / 17 ms and max 26 / 28 ms, the 100,000,000-row
  `db/query` answered `SCN8013` after 667 / 1,981 ms with
  `rows_within_budget` 5,000, and all sleeping statements left
  `pg_stat_activity` 53 / 52 ms after the connections closed.
- Scratch PostgreSQL measurements (disposable `postgres:18-alpine`, removed):
  see Surprises.

## Interfaces and Dependencies

- `cmd/scenery/dashboard_rpc_limits.go`: `runtimeRPCLimits`,
  `defaultRuntimeRPCLimits`, `runtimeCallClass`, the per-app admission table,
  `runtimeRPCFailure` (`code`, `diagnostic`, `message`, `details`) and the
  constructors for `SCN8011`–`SCN8013`.
- `cmd/scenery/dashboard.go`: `handleWebSocket` (admission, per-call
  deadline), `dashboardServerHooks.openDatabase`, `queryDB`,
  `scanRuntimeRows(rows, runtimeResultBudget)`.
- `cmd/scenery/dashboard_postgres.go`: `postgresRows` budget and cancellation.
- `cmd/scenery/dashboard.go`: `queryRuntimeRows` cancels a statement's
  context before closing its rows; PostgreSQL pools still come from
  `postgresdb.Open` (pgx stdlib, unchanged).
- `internal/spec/diagnostics_catalog.go`: `SCN8011`, `SCN8012`, `SCN8013`.
- `internal/generate/dev_runtime_client.ts`: documentation only; the wire and
  exported types are unchanged.
- `scripts/verify/harness_self_worktree_rpc_bounds.go` (new): scenario A19.
