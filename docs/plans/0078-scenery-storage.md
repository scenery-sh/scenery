# Scenery Storage

> **Historical note (2026-07-02).** This plan built the storage capability on managed ZeroFS. ZeroFS was later removed in [plan 0091](0094-local-storage-and-zerofs-removal.md); the current storage backend is the local filesystem and the current contract lives in [docs/local-contract.md](../local-contract.md).

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Make storage a Scenery-owned capability instead of an app-local service pattern. After this plan, a Scenery app can declare durable file/object storage in `.scenery.json`, run `scenery up`, and receive a typed `scenery.sh/storage` capability that stores, lists, streams, and deletes objects without the target app owning filesystem roots, S3 clients, ad hoc `/drive` endpoints, or substrate lifecycle.

The first production backend is ZeroFS. Scenery starts and supervises a shared ZeroFS storage cell for the app, writes its config under the agent storage root rather than an individual worktree or session state root, exposes 9P over a Unix socket for file operations, and hides S3-compatible backend details from the app. The same app storage cell is shared across Git worktrees and live Scenery sessions by default so files uploaded or generated in one worktree are visible from the others. Scenery does not enable NFS, NBD, or public ZeroFS TCP listeners by default. Scenery includes the ZeroFS Web UI as a Scenery-owned operator/debug surface, bound only to a private loopback listener or equivalent private upstream, and exposed only through a Scenery-controlled route with explicit access policy. Target apps consume Scenery storage through Go APIs and generated TypeScript/HTTP surfaces owned by Scenery.

This is a platform rewrite, not a compatibility shim for an app-specific drive package. A target app such as ONLV should delete its local `drive` implementation and call `scenery.sh/storage` or generated Scenery storage routes. Scenery owns the storage substrate, config schema, app env injection, CLI inspection, harness probes, docs, and safety diagnostics.

## Progress

- [x] 2026-06-22: ExecPlan drafted and registered as `docs/plans/0078-scenery-storage.md`.
- [x] 2026-06-22: Added `storage` to `internal/app.Config`, `.scenery.json` schema, and `scenery inspect storage --json` foundation. Full docs updates remain in later milestones.
- [x] 2026-06-22: Added `scenery.sh/storage` public app package with stream-first object API, key validation, HTTP object serving helper, and an internal memory test backend.
- [x] 2026-06-22: Added managed ZeroFS planning/config foundation with shared agent storage-cell paths, 9P/RPC Unix sockets, private loopback Web UI listener config, and tests that reject NFS/NBD/TCP listener drift.
- [x] 2026-06-22: Added `scenery storage status|webui|ls|stat|put|get|rm --json` CLI foundation, backed by configured storage inspect data and a Scenery-owned shared storage-cell local backend for object command validation.
- [x] 2026-06-22: Added `testdata/apps/storage-basic` and validated `scenery check`, `scenery inspect storage`, and a put/list/stat/get/rm CLI loop with isolated `SCENERY_AGENT_HOME`.
- [x] 2026-06-22: Updated storage docs, env registry entries, config/CLI JSON schema references, agent guide, app cookbook, and installable skill guidance for the implemented storage foundation.
- [x] 2026-06-22: Wired `scenery.sh/storage` to Scenery-injected `SCENERY_STORAGE_CONFIG`, inject `SCENERY_STORAGE_CONFIG`/`SCENERY_STORAGE_CELL_ID` into app, worker, setup, and code-task processes, and added a fixture code task proving `storage.Default(ctx)` writes through the shared storage cell.
- [x] 2026-06-22: Added self-harness storage fixture probe coverage that runs the fixture code task with an isolated agent home and verifies the object lands in the Scenery-owned shared storage-cell path.
- [x] 2026-06-22: Added beta reserved runtime HTTP storage routes under `/__scenery/storage/<store>/...` for auth-gated upload, list, HEAD/ranged download, delete, and private-store denial backed by the current Scenery storage runtime env.
- [x] 2026-06-22: Added explicit-binary managed ZeroFS process launcher foundation: shared storage-cell TOML under the agent storage root, `zerofs run -c`, 9P/RPC Unix socket readiness, private loopback Web UI readiness, agent session route/backend registration, normalized `zerofs-<cell-id>` substrate metadata, protected `SCENERY_ZEROFS_WEBUI_URL` env, and fake-binary tests.
- [x] 2026-06-22: Added shared-cell ZeroFS attach/reuse foundation: dev supervisors first probe and attach a healthy existing `zerofs-<cell-id>` substrate, register the current session's protected Web UI route/backend to that upstream, discard stale substrate records when probes fail, and avoid stopping attached shared processes on session shutdown.
- [x] 2026-06-22: Added generated TypeScript storage helpers on `client.storage` for reserved auth storage routes: store-scoped list, put, get, getText, getBlob, head, delete, and recursive deletePrefix, with generator tests and a fixture client TypeScript compile smoke check.
- [x] 2026-06-22: Added a shared-cell shutdown guard: a ZeroFS-owning dev supervisor now scans live agent sessions before shutdown and detaches instead of interrupting the process when another live session has the same storage route backend attached.
- [x] 2026-06-22: Extended self-harness storage proof with two copied fixture roots sharing one `SCENERY_AGENT_HOME`: one root writes an object through `scenery storage put`, the other reads it through `scenery storage get`, and the harness verifies the shared storage-cell object path.
- [x] 2026-06-22: Made `scenery inspect storage --json` and `scenery storage status --json` agent-aware: when a matching ZeroFS substrate/session exists, storage readiness reports `ready` with normalized substrate status, route, protected Web UI URL, and attachment metadata while staying configured-only offline.
- [x] 2026-06-22: Aligned the CLI/local storage backend with runtime store limits so `scenery storage put` enforces configured `max_object_bytes` in the same shared-cell local validation path used by fixture probes.
- [x] 2026-06-22: Added durable ZeroFS storage-cell lease accounting on agent substrate records, surfaced lease ownership in `scenery inspect storage --json`/`scenery storage status --json`, and made both normal supervisor shutdown and `scenery down` release only the current session's lease while preserving shared storage-cell data.
- [x] 2026-06-22: Added a session-local Scenery storage proxy over a Unix socket for agent-backed dev runtimes. `SCENERY_STORAGE_CONFIG` now gives app code a proxy-backed store when a session state root exists, while CLI/non-session fixture paths keep the local shared-cell backend until the real ZeroFS 9P backend is available.
- [x] 2026-06-22: Added `scenery doctor --json` diagnostics for managed ZeroFS storage apps: doctor warns when `SCENERY_DEV_ZEROFS_BIN` is missing or non-executable so agents see the local substrate gap before `scenery up`.
- [x] 2026-06-22: Added private/internal runtime storage route integration: reserved storage routes are registered on the private route table for internal use, while `private` stores still return permission denied on the external/public HTTP surface.
- [x] 2026-06-22: Replaced the session storage proxy's local shared-cell backend with a pure-Go 9P2000.L data-plane adapter. Agent-backed dev app storage now goes through the managed ZeroFS 9P Unix socket and is tested against a protocol-compatible p9 localfs server; CLI/non-session fixture validation remains local.
- [x] 2026-06-22: Replaced process-only ZeroFS readiness with a real ZeroFS 1.2.5 proof: `scenery up --app-root testdata/apps/storage-basic --json --detach` starts the managed ZeroFS process, registers `zerofs-storage-basic`, exposes the protected storage Web UI route, starts the session-local storage proxy, and a public fixture endpoint writes and reads `probe/public.txt` through `storage.Default(ctx)` over the real ZeroFS 9P socket.
- [x] 2026-06-22: Added ZeroFS-available self-harness proof. When `SCENERY_DEV_ZEROFS_BIN` points to an executable binary, `scenery harness self --summary --write` starts the fixture app with real ZeroFS, calls the storage probe over the app API Unix socket, asserts `inspect storage` readiness `ready`, and cleans up the throwaway agent home; without the binary the proof records a skipped diagnostic.
- [x] 2026-06-22: Validated the Scenery storage replacement path in `/Users/petrbrazdil/Repos/onlv` on branch `feat/scenery-storage-onlv-migration`: ONLV now declares storage cell `onlv`, backend file helpers write/list/read/delete through `scenery.sh/storage`, Pulse Drive/viewer/contact/annotation writes use generated `client.storage`, and validation passed with `scenery check --json`, `go test ./...`, `bun run typecheck`, `bun run lint`, `scenery harness --json --write`, and an isolated `scenery storage put|get` byte-for-byte smoke.
- [x] 2026-06-24: Current contract update: managed ZeroFS now resolves from the pinned `zerofs` artifact in `scenery.toolchain.json`; previous `SCENERY_DEV_ZEROFS_BIN` notes in this plan are historical implementation evidence, not supported setup instructions.

## Surprises & Discoveries

- 2026-06-22: ZeroFS official client libraries speak 9P2000.L directly to the server and can avoid kernel mounts, but the Go client requires cgo plus a separately built native `libzerofs_ffi` dynamic library. This must not leak into target app builds. Scenery should isolate any ZeroFS-native binding inside a Scenery-owned storage proxy or optional managed tool, not inside app packages.
- 2026-06-22: The initial docs reading suggested ZeroFS could run 9P with only Unix sockets when `addresses` is omitted, but real ZeroFS 1.2.5 did not create the 9P Unix socket until a private loopback `addresses` listener was configured. Scenery still keeps the app data path on the Unix socket and treats the loopback listener as hidden substrate.
- 2026-06-22: ZeroFS Web UI is useful enough to include, but it has to be treated as an operator/debug surface rather than an app data-plane API. It should bind to loopback/private infrastructure only and be exposed through Scenery policy, not directly.
- 2026-06-22: The first Web UI draft still described ZeroFS paths under `session.StateRoot`, which would isolate storage per worktree/session. That is wrong for ONLV's intended product behavior. Storage must be keyed by a stable app storage cell and shared across worktrees by default; only route/proxy attachment is session-local.
- 2026-06-22: ZeroFS is dual licensed under AGPLv3/commercial terms. Scenery must treat bundling/linking and distribution as a release/legal gate. The first implementation should support an external `zerofs` binary/container and avoid statically or dynamically linking ZeroFS into the Scenery app runtime until licensing is resolved.
- 2026-06-22: The first browser-facing storage route implementation can be backed by `SCENERY_STORAGE_CONFIG` without adding ZeroFS bindings to the app runtime. That gives browser upload/download coverage now, while the ZeroFS process/proxy boundary remains the next substrate milestone.
- 2026-06-22: This machine does not currently have a `zerofs` binary in `PATH`, so real ZeroFS readiness remains unproved. The managed process foundation is tested with a fake explicit binary and keeps real startup behind `SCENERY_DEV_ZEROFS_BIN`.
- 2026-06-22: Agent substrate kinds and endpoint keys are normalized as labels, so ZeroFS cell records use `zerofs-<cell-id>` with endpoint keys such as `cell-id`, `ninep-socket`, `rpc-socket`, and `webui-addr`. Reuse code must tolerate stale or legacy records and delete unhealthy ones before attempting a fresh start.
- 2026-06-22: Empty lease maps must survive the agent client/server JSON path; `UpsertSubstrateRequest.leases` intentionally does not use `omitempty` so `{}` means "clear all leases" while `null` means "preserve current leases".
- 2026-06-22: Unix socket paths can exceed macOS limits when a repo path is deep, so the storage proxy falls back from `.scenery/sessions/<id>/run/storage/proxy.sock` to a short temporary socket keyed by session/app hash and removes it on shutdown.
- 2026-06-22: Storage private/internal routing belongs in the runtime route table, not in generated public browser helpers. Public `/__scenery/storage/...` requests still enforce auth/private policy, while Scenery-internal calls can use the private route table for non-external storage work.
- 2026-06-22: `github.com/hugelgupf/p9` gives Scenery a pure-Go 9P2000.L client/server surface originally from gVisor. It lets the storage proxy speak ZeroFS's 9P Unix socket without pulling ZeroFS FFI, cgo, Rust, or loader env into target app builds.
- 2026-06-22: Real ZeroFS 1.2.5 rejected the draft TOML shape in several useful ways: `[cache]` uses `dir` rather than `path` and requires a cache size, `[storage]` requires `encryption_password`, and `[servers.webui]` uses `addresses` rather than `address`.
- 2026-06-22: A tiny `disk_size_gb = 1` cache can produce a zero-block warning and never create the 9P socket on this macOS machine. The real-binary proof succeeded with `disk_size_gb = 10` and `memory_size_gb = 1`, matching ZeroFS examples.
- 2026-06-22: ZeroFS 1.2.5 did not create the 9P Unix socket when `[servers.ninep]` had only `unix_socket`. Adding a private loopback `addresses` listener activates 9P while the Scenery storage proxy still uses the Unix socket data path. This is a substrate detail, not an app integration surface.
- 2026-06-22: The shared agent-root socket path can exceed macOS Unix-domain socket path limits. Managed ZeroFS now keeps durable cache/object/config/log files under the shared agent storage root, but places 9P/RPC Unix sockets under a short temp path keyed by the storage cell identity.
- 2026-06-24: ZeroFS binary selection moved out of app/runtime env and into the Scenery managed toolchain. `docs/local-contract.md`, `docs/environment.md`, and `scenery.toolchain.json` are the current contract for storage substrate resolution.

## Decision Log

- Decision: Make the app-facing Scenery surface `scenery.sh/storage`, not `scenery.sh/zerofs`.
  Rationale: Apps should depend on a Scenery capability. ZeroFS is the first backend and dev substrate, but Scenery should keep the public API stable if the backend changes or if production uses a different storage service later.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

- Decision: Use 9P over Unix sockets as the only managed ZeroFS app storage operation transport for this plan.
  Rationale: 9P gives Scenery programmatic filesystem semantics without NFS privileged ports, NBD block-device lifecycle, kernel mounts, or public TCP listeners. ZeroFS explicitly supports 9P over `unix_socket`. The ZeroFS Web UI is included separately as an operator/debug surface, not as the app storage data plane.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

- Decision: Include ZeroFS Web UI in Scenery storage v1 as a Scenery-controlled operator/debug route; continue to exclude NFS and NBD.
  Rationale: The Web UI is valuable for inspecting files, live stats, and operational behavior while migrating apps such as ONLV. It is not an app data-plane API and must not be exposed directly because ZeroFS Web UI does not provide app-auth semantics. Scenery should bind it privately and put Scenery access policy in front of it. NFS and NBD remain excluded because they add platform-specific privileges and cleanup risks.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

- Decision: Keep an internal memory/local test backend even though user-facing storage v1 is ZeroFS-backed.
  Rationale: Unit tests, schema tests, generated-route tests, and self-harness should not require a real ZeroFS binary or S3 credentials. This is a test fixture and fallback diagnostic tool, not an app-level alternative storage product.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

- Decision: Hide ZeroFS client/linking complexity behind a Scenery-owned runtime boundary.
  Rationale: The Go ZeroFS binding currently needs cgo, a native Rust-built cdylib, and loader env. Target apps should not inherit those build constraints merely by importing `scenery.sh/storage`.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

- Decision: A ZeroFS storage cell is shared across worktrees and Scenery sessions by default.
  Rationale: Storage is durable app data, not ephemeral dev-session state. ONLV should be able to run multiple worktrees while seeing the same user files, maps, and job artifacts. The storage cell identity must not include the Git worktree path, current branch, or Scenery session ID. Session-local processes may attach routes and proxies to the shared cell, but object data, ZeroFS cache, ZeroFS sockets, and Web UI upstream live under a shared agent storage root.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

- Decision: Use `github.com/hugelgupf/p9` for the Scenery-owned ZeroFS 9P data-plane adapter.
  Rationale: The package implements 9P2000.L in pure Go and accepts a generic `net.Conn`, so Scenery can connect to ZeroFS over a Unix socket from the storage proxy while keeping `scenery.sh/storage` and target app builds pure Go. The dependency remains internal to Scenery's storage backend, and tests use its local filesystem server as a protocol-compatible stand-in until this machine has a real `zerofs` binary.
  Date/Author: 2026-06-22 / GPT-5.5 Pro xhigh-effort

## Outcomes & Retrospective

Completed on 2026-06-22. Scenery now owns a beta storage capability backed by managed ZeroFS for live dev sessions, exposes `scenery.sh/storage`, reserved runtime HTTP routes, generated TypeScript storage helpers, CLI inspect/object commands, self-harness probes, and docs/schema coverage. The real ZeroFS 1.2.5 proof required correcting the generated TOML and using short temp Unix sockets for 9P/RPC on macOS. ONLV was migrated far enough to prove the replacement path: durable app config declares the shared storage cell, app writes and lists move through Scenery storage APIs, generated Pulse storage helpers compile, backend and frontend validation pass, and direct public `/drive` GET remains only as an ONLV viewer/download bridge until direct asset consumers become token-aware.

## Context and Orientation

Scenery's repo-local model says Scenery runs the app runtime, gives apps capabilities, lets agents inspect and act safely, and hides substrates unless the user intentionally debugs them. Storage belongs in that model: it is a capability surface, not an app-local directory convention.

Current related files and packages:

```text
AGENTS.md                                  repo-level agent rules and Scenery capability model
PLANS.md                                   ExecPlan structure and validation requirements
docs/plans/active.md                       active ExecPlan index
docs/knowledge.json                        machine-readable docs index
docs/local-contract.md                     CLI/config/JSON contract
docs/agent-guide.md                        agent workflow and app integration docs
docs/environment.md                        Scenery-owned environment variables
docs/environment.registry.json             machine-readable env registry
docs/schemas/scenery.config.v1.schema.json app config schema
internal/app/root.go                       app.Config and DevServiceConfig definitions
internal/agent/types.go                    session/backend/substrate JSON records
cmd/scenery/dev_services.go                managed Postgres/sync lifecycle pattern
cmd/scenery/dev_services_test.go           managed service plan/env tests
cmd/scenery/inspect.go                     inspect command family
cmd/scenery/help.go                        CLI help JSON registry
cmd/scenery/harness_self*.go               self-harness evidence and diagnostics
```

Existing managed-dev-service behavior to follow:

- `dev.services` is a beta local-development config surface.
- Unsupported service kinds fail closed instead of silently falling back.
- Managed Postgres and sync are resolved from `internal/app.DevServiceConfig`, started by `scenery up`, registered with the agent session, and exposed to the app through Scenery-owned env.
- Public contract changes must update docs, JSON schemas, tests, and harness expectations together.

New terms for this plan:

- "Storage store" means one named Scenery storage namespace declared by app config, for example `app`, `user-files`, `job-artifacts`, or `maps`.
- "Object key" means a slash-separated UTF-8 path inside one store. It is an object/file key, not a host filesystem path.
- "Storage cell" means the shared Scenery-owned runtime/data unit for one app storage declaration. Its ID is stable across Git worktrees and sessions.
- "ZeroFS substrate" means the supervised `zerofs run --config <file>` process and its shared cache/storage/run paths under the agent storage root.
- "9P socket" means the Unix socket configured in `[servers.ninep].unix_socket`.
- "ZeroFS Web UI" means the ZeroFS web server configured in `[servers.webui]`; Scenery treats it as a private operator/debug upstream and exposes it only through a Scenery-controlled route.
- "Storage proxy" means the Scenery-owned runtime boundary that speaks to the ZeroFS 9P endpoint and exposes stream-first object operations to the app runtime without forcing app packages to link ZeroFS FFI.
- "Test backend" means an internal memory or temp-directory implementation used by Go tests and fixture apps when no ZeroFS binary is available.

ZeroFS behavior this plan relies on:

- ZeroFS can serve file operations over 9P using a Unix socket.
- ZeroFS storage can target S3-compatible services through `s3://...` plus `[aws].endpoint`, and local dev can use `file://...`.
- ZeroFS requires conditional-put semantics for fencing; for S3-compatible stores that lack native conditional put, its config uses a Redis `conditional_put` URL.
- ZeroFS client endpoints have no application authentication; Scenery must keep the 9P socket local/private and enforce auth at Scenery storage APIs instead.
- ZeroFS Web UI must be bound privately, must not be exposed as a raw public backend, and must sit behind Scenery operator access policy.

## Milestones

1. Storage config and contract are parsed, schema-validated, inspected, and documented without starting ZeroFS.
2. `scenery.sh/storage` exists with a stream-first API, in-memory/local test backend, object-key validation, metadata types, and Go tests.
3. Scenery can start or attach to one shared managed ZeroFS process for a stable storage cell ID, using 9P and RPC Unix sockets for app operations plus a private loopback Web UI upstream for operator/debug access.
4. The app runtime can perform `Put`, `PutFile`, `Get`, `Head`, `List`, `Delete`, and `DeletePrefix` through the Scenery storage boundary without importing ZeroFS FFI in target app packages.
5. `scenery inspect storage --json`, `scenery storage ls|put|get|rm|stat`, and dashboard/dev-event status show storage readiness and failures.
6. Generated HTTP/client surfaces let browser apps upload, list, download, and delete objects through Scenery-authenticated routes; Scenery also exposes a separate protected operator route to the ZeroFS Web UI.
7. Self-harness proves the capability on a fixture app, including a two-worktree visibility test; an ONLV migration branch can delete app-local drive code and use Scenery storage.

## Plan of Work

Begin by defining the public contract without ZeroFS. Add `storage` to app config as a top-level beta capability, separate from `dev.services` lifecycle details. The app config declares logical stores and policy; `dev.services` declares the local substrate. Keep schema changes strict and explicit so unknown fields remain errors.

Proposed app config shape:

```json
{
  "storage": {
    "cell_id": "onlv",
    "share": "worktree",
    "default": "app",
    "stores": {
      "app": {
        "kind": "zerofs",
        "access": "auth",
        "tenant_scoped": true,
        "max_object_bytes": 1073741824
      }
    }
  },
  "dev": {
    "services": {
      "storage": {
        "kind": "zerofs",
        "mode": "local",
        "route": "storage",
        "env": {
          "ZEROFS_STORAGE_URL": "file://${SCENERY_STORAGE_CELL_ROOT}/objects",
          "ZEROFS_CACHE_DIR": "${SCENERY_STORAGE_CELL_ROOT}/cache",
          "ZEROFS_WEBUI": "true"
        }
      }
    }
  }
}
```


Storage cell identity rules:

- `storage.cell_id` is optional but recommended for production apps. If omitted, Scenery derives a stable cell ID from the app identity and route namespace, not from the Git worktree path, branch, session ID, or process ID.
- `storage.share` defaults to `"worktree"`, meaning every worktree and live session for the same app/storage cell attaches to one ZeroFS substrate and object namespace.
- A future explicit `share: "session"` may exist for destructive experiments, but it is not part of this plan and must not be the default.
- Store names are names inside the cell. The physical ZeroFS/S3 keys must include Scenery-owned store and tenant prefixes so two stores do not collide inside the same cell.

Do not reuse `dev.services.<name>.route` as the app-facing storage API. The route is for agent/debug metadata and, when enabled, the protected ZeroFS Web UI operator surface. The app-facing storage API is generated or served by Scenery under app auth, and the 9P socket stays private.

Add `internal/app.StorageConfig`, `StorageStoreConfig`, schema definitions, inspect JSON, and docs. `storage.kind` initially accepts only `"zerofs"` for app config. A hidden/internal `"memory"` or `"local"` backend may exist for tests but is not accepted in user config unless an explicit follow-up changes the contract.

Then add the Go package surface. The initial package should be small, stream-first, and not ZeroFS-specific:

```go
package storage

type Store interface {
    Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error)
    PutFile(ctx context.Context, key, localPath string, opts PutOptions) (*Object, error)
    Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error)
    Head(ctx context.Context, key string) (*Object, error)
    List(ctx context.Context, opts ListOptions) (*ListPage, error)
    Delete(ctx context.Context, key string) error
    DeletePrefix(ctx context.Context, prefix string) error
}

func Default(ctx context.Context) (Store, error)
func Named(ctx context.Context, name string) (Store, error)
func ServeObject(w http.ResponseWriter, req *http.Request, body io.ReadCloser, obj *Object)
```

Types:

```go
type Object struct {
    Store       string
    Key         string
    SizeBytes   int64
    ContentType string
    ETag        string
    SHA256      string
    ModifiedAt  time.Time
    Metadata    map[string]string
}

type PutOptions struct {
    ContentType string
    Metadata    map[string]string
    IfNoneMatch bool
}

type GetOptions struct {
    Offset *int64
    Length *int64
}

type ListOptions struct {
    Prefix    string
    Delimiter string
    Cursor    string
    Limit     int
}

type ListPage struct {
    Objects    []Object
    Prefixes   []string
    NextCursor string
}
```

Validation rules:

- Keys are relative slash paths.
- Reject empty keys for object operations.
- Reject backslashes, `.` and `..` path traversal, duplicate slashes after normalization, ASCII control characters, and invalid UTF-8.
- Preserve caller-visible slash keys as object keys, not OS paths.
- Bound `ListOptions.Limit` with a default and hard max.
- Tenant scoping, when enabled, prefixes physical keys with a Scenery-owned tenant namespace derived from auth state. The caller-visible key must not include tenant IDs.

Next implement internal backends and the storage boundary. Use a memory backend for unit tests and a temp-directory backend only where a real directory is useful for fixture validation. These backends must exercise the same key validation, list pagination, metadata, and streaming behavior as the ZeroFS backend. They must be clearly internal and must not become a compatibility promise.

For ZeroFS, prefer this architecture:

```text
app package: scenery.sh/storage
    -> private Scenery storage transport
        -> Scenery storage proxy/backend
            -> ZeroFS 9P Unix socket
                -> ZeroFS core
                    -> file:// dev store or s3:// production store
```

The storage proxy may initially live inside the generated app runtime if it can use a pure-Go 9P client. If the implementation uses ZeroFS's Go FFI, keep it in a separately built/managed Scenery helper process so target apps do not need cgo, Rust, `CGO_LDFLAGS`, or `LD_LIBRARY_PATH`. The ExecPlan implementer must record the selected 9P client strategy in the Decision Log before coding beyond a prototype.

If no pure-Go 9P2000.L client can cover the required operations cleanly, implement the first ZeroFS backend as a Scenery-owned helper binary behind an internal HTTP or Unix-socket RPC protocol. The helper can carry the ZeroFS FFI dependency while the app-facing `scenery.sh/storage` package remains pure Go. This preserves the 9P-only ZeroFS transport while isolating native dependency risk.

Managed ZeroFS lifecycle follows the `dev_services.go` sync pattern, but with a shared storage-cell owner instead of a session-local owner:

- Detect `dev.services.<name>.kind == "zerofs"`.
- Resolve a `managedZeroFSPlan` and a stable `storageCellID`.
- Acquire an agent-level storage-cell lock before starting or attaching to ZeroFS. The lock key is the storage cell ID, not the worktree path or session ID.
- Allocate shared storage-cell paths under the agent storage root, for example:
  - `<agentStateRoot>/storage/<cellID>/cache`
  - `<agentStateRoot>/storage/<cellID>/objects`
  - `<agentStateRoot>/storage/<cellID>/run/zerofs.toml`
  - `<agentStateRoot>/storage/<cellID>/run/zerofs.9p.sock`
  - `<agentStateRoot>/storage/<cellID>/run/zerofs.rpc.sock`
  - `<agentStateRoot>/storage/<cellID>/run/zerofs-webui.addr`
  - `<agentStateRoot>/storage/<cellID>/run/zerofs.log`
- Allocate only per-session route/proxy metadata under `session.StateRoot`, for example a session-local storage proxy socket or route registration file. Do not store ZeroFS objects, cache, or substrate sockets under the worktree/session state root.
- Write a ZeroFS TOML config with `[cache]`, `[storage]`, `[servers]`, `[servers.ninep]`, `[servers.webui]`, and optionally `[servers.rpc]`.
- Configure `[servers.ninep]` with a Scenery-owned private loopback `addresses` listener plus a short temp `unix_socket`; app storage operations use the Unix socket through the Scenery storage proxy, not the TCP listener.
- Omit RPC TCP `addresses` unless the user has an explicit debug flag in a follow-up.
- Configure `[servers.webui]` with Scenery-owned private loopback `addresses`, `uid`, and `gid`. Do not bind it to `0.0.0.0` or expose it directly as a raw session backend.
- Never enable `[servers.nfs]` or `[servers.nbd]` in v1.
- Start from one of:
  - explicit `SCENERY_DEV_ZEROFS_BIN`,
  - managed toolchain binary if ExecPlan 0059 provides one,
  - explicit container image in `dev.services.<name>.image` when Docker is available.
- Wait until the 9P socket exists, the Web UI private listener responds, and a storage health probe succeeds.
- Register a storage/zerofs substrate with the agent state and dashboard event stream, including a protected Web UI route URL when available.
- Register every attached worktree/session as a lease on the shared storage cell so `scenery ps`, `scenery inspect storage --json`, and cleanup can show which sessions are using it.
- Keep the shared ZeroFS process alive while at least one attached session lease is active; do not start duplicate ZeroFS processes for each worktree.
- Inject app env containing only Scenery capability metadata, not raw S3 secrets.

Proposed app env:

```text
SCENERY_STORAGE_CONFIG=<json or path to generated storage capability config>
SCENERY_STORAGE_DEFAULT=<default store name>
SCENERY_STORAGE_CELL_ID=<stable shared storage cell ID>
SCENERY_STORAGE_PROXY_SOCKET=<session-local Scenery storage proxy socket attached to the shared cell>
SCENERY_STORAGE_ZEROFS_9P_SOCKET=<debug metadata; only if needed by Scenery internals>
SCENERY_ZEROFS_WEBUI_URL=<protected Scenery route URL for operator/debug UI>
```

The Web UI URL is a Scenery route, not the raw ZeroFS listener. The raw listener is private and should not appear in generated browser clients except as redacted debug metadata.

Avoid injecting S3 credentials into app env if only ZeroFS needs them. The ZeroFS process may receive credential env through `dev.services.storage.env` or production secret management, but the target app should only see Scenery storage capability config.

Add CLI support:

```text
scenery inspect storage --json
scenery storage status --json
scenery storage webui [--json]
scenery storage ls <store> [--prefix <prefix>] [--cursor <cursor>] [--limit <n>] [--json]
scenery storage stat <store> <key> --json
scenery storage put <store> <key> <file>
scenery storage get <store> <key> [--output <file>]
scenery storage rm <store> <key> [--recursive]
```

`scenery inspect storage --json` reports the declared stores, active backend kind, readiness, protected Web UI URL, socket paths only when debug metadata is enabled, and schema version `scenery.storage.inspect.v1`. The CLI must avoid printing secret values or raw object-store credentials. `scenery storage webui --json` prints the protected URL and readiness state; without `--json` it may open or print the URL according to existing Scenery CLI conventions.

Generated/browser API:

- Add generated Scenery-authenticated storage routes only when storage is configured.
- Route base should be reserved and collision-checked, for example `/__scenery/storage/...` for platform routes or `/storage/...` only if reserved in `docs/local-contract.md`.
- Browser uploads use streaming `PUT` to `/__scenery/storage/<store>/<key>` or future signed upload tickets generated by Scenery.
- Downloads support `HEAD`, `GET`, content length, content type, ETag, and range requests.
- List supports `prefix`, `delimiter`, `cursor`, and bounded `limit`.
- Mutating routes default to `auth`; public object access requires explicit per-store or per-object policy, not the default.
- The ZeroFS Web UI route is separate from generated object routes. It should be visible in Scenery dashboard/session metadata as an operator/debug tool and should be gated by Scenery's local dev/operator access policy.

Docs and schemas:

- Update `docs/local-contract.md` with storage config, CLI grammar, JSON schema names, and beta/stable classification.
- Add `docs/storage.md` or `docs/app-development-cookbook.md` section showing app usage.
- Update `docs/environment.md` and `docs/environment.registry.json` for every new Scenery-owned env var.
- Update `docs/agent-guide.md` and `SKILL.md` so agents inspect storage with JSON commands instead of discovering substrate paths.
- Add `docs/schemas/scenery.storage.inspect.v1.schema.json` and any CLI output schemas.
- Update `docs/knowledge.json` and `docs/plans/active.md`.

## Concrete Steps

Run from the Scenery repo root.

1. Create this plan and register it.

```sh
cp /tmp/0078-scenery-storage.md docs/plans/0078-scenery-storage.md
# Add a bullet to docs/plans/active.md.
# Add a docs/knowledge.json entry for the new ExecPlan.
```

2. Add storage config parsing.

Files:

```text
internal/app/root.go
docs/schemas/scenery.config.v1.schema.json
docs/local-contract.md
cmd/scenery/inspect.go
cmd/scenery/inspect_test.go
```

Expected work:

- Add `Storage StorageConfig` to `app.Config`.
- Add store structs with strict JSON tags.
- Update schema to accept top-level `storage`.
- Add config tests that unknown storage fields fail, missing required store names fail, unsupported storage kind fails, minimal ZeroFS storage config parses, and default storage cell identity is stable across worktree paths/sessions.
- Add inspect output for configured storage even before runtime startup.

Focused validation:

```sh
go test ./internal/app ./cmd/scenery -run 'Storage|Config|Inspect'
```

3. Add the public storage package and internal test backend.

Files:

```text
storage/storage.go
storage/errors.go
storage/keys.go
storage/http.go
internal/storage/memory.go
internal/storage/local.go
internal/storage/contract_test.go
```

Expected work:

- Implement key validation and list pagination once.
- Add stream-first memory backend tests.
- Add `ServeObject` tests for content type, content length, ETag, range, and missing metadata.
- Keep package free of ZeroFS imports.

Focused validation:

```sh
go test ./storage ./internal/storage
```

4. Add managed ZeroFS planning and config generation.

Files:

```text
cmd/scenery/dev_services.go
cmd/scenery/dev_services_test.go
internal/agent/types.go
docs/schemas/scenery.config.v1.schema.json
docs/local-contract.md
```

Expected work:

- Extend dev service kind enum with `zerofs`.
- Add `SubstrateZeroFS` or `SubstrateStorage` constant.
- Add `managedZeroFSPlan` and resolver tests, including two app roots/worktrees resolving to the same storage cell ID and shared agent storage root.
- Generate TOML that contains 9P and RPC Unix sockets plus a private loopback Web UI listener.
- Validate that NFS/NBD fields are absent and Web UI binds only to the private Scenery-managed upstream.
- Add env expansion rules and secret redaction tests.

Focused validation:

```sh
go test ./cmd/scenery -run 'ZeroFS|Storage|DevService'
go test ./internal/agent
```

5. Add runtime startup and health.

Files:

```text
cmd/scenery/dev_services.go
cmd/scenery/dev_supervisor.go
cmd/scenery/dev_supervisor_test.go
cmd/scenery/doctor.go
cmd/scenery/doctor_test.go
```

Expected work:

- Start explicit ZeroFS binary/container when configured, or attach to the existing shared storage-cell process when another worktree already started it.
- Wait for sockets and health probe.
- Register shared substrate process metadata and per-session/worktree leases.
- Emit dev dashboard events for ready/error states.
- Add `scenery doctor --json` warning when storage is configured but no usable ZeroFS binary/container/toolchain is available.
- Keep failure clear and actionable: if storage is configured, `scenery up` should fail before app start when ZeroFS cannot start.

Focused validation:

```sh
go test ./cmd/scenery -run 'ZeroFS|Storage|Doctor|Supervisor'
```

6. Implement the Scenery storage runtime boundary.

Files depend on the chosen 9P strategy and must be recorded in Decision Log before coding. Expected candidate files:

```text
internal/storage/proxy.go
internal/storage/proxy_client.go
internal/storage/zerofs9p.go
cmd/scenery/storage_proxy.go
cmd/scenery/storage_proxy_test.go
```

Expected work:

- Keep app package pure Go.
- If using a helper process, define a small internal protocol over Unix socket with streaming request/response bodies.
- Implement `Put`, `PutFile`, `Get`, `Head`, `List`, `Delete`, and `DeletePrefix`.
- Ensure `PutFile` streams from the local path without reading the whole file into memory.
- Add cancellation/deadline behavior; cancellation may abandon client wait but must not corrupt object state.
- Add idempotence notes for reconnects and non-idempotent operations.

Focused validation:

```sh
go test ./storage ./internal/storage ./cmd/scenery -run 'Storage|Proxy|ZeroFS'
```

7. Add CLI and inspect surfaces.

Files:

```text
cmd/scenery/storage.go
cmd/scenery/storage_test.go
cmd/scenery/inspect.go
cmd/scenery/help.go
docs/schemas/scenery.storage.inspect.v1.schema.json
docs/local-contract.md
```

Expected work:

- Implement `scenery inspect storage --json`.
- Implement `scenery storage status|webui|ls|stat|put|get|rm`.
- Update `scenery help --json` tests.
- Add schema validation tests for CLI JSON.

Focused validation:

```sh
go test ./cmd/scenery -run 'Storage|Inspect|Help'
```

8. Add generated HTTP/client surfaces.

Files likely include:

```text
internal/codegen/*
internal/parse/*
runtime/*
docs/local-contract.md
docs/app-development-cookbook.md
```

Expected work:

- Add reserved generated storage endpoints when storage is configured.
- Default routes to `auth`.
- Add TypeScript client generation if these routes are not already covered by normal endpoint generation.
- Add browser upload/download examples and tests.

Focused validation:

```sh
go test ./internal/codegen ./cmd/scenery -run 'Storage|Client|Route'
scenery inspect endpoints --json
scenery inspect routes --json
```

9. Add fixture app and self-harness proof.

Files:

```text
testdata/apps/storage-basic/.scenery.json
testdata/apps/storage-basic/files/*.go
cmd/scenery/harness_self*.go
docs/schemas/scenery.harness.self.v1.schema.json
```

Expected work:

- Fixture app declares one ZeroFS storage store and one endpoint/task that writes, lists, reads, and deletes an object.
- Self-harness or a focused integration test starts/attaches two worktree-like app roots with the same storage cell ID, writes from one, reads/lists from the other, and verifies only one ZeroFS substrate process owns the cell.
- Self-harness can run a storage probe when ZeroFS is available and records a skipped diagnostic when it is not.
- Unit tests still pass with the internal memory backend when ZeroFS is unavailable.

Focused validation:

```sh
go test ./cmd/scenery ./storage ./internal/storage
scenery harness self --summary --write
```

10. ONLV proof branch.

Expected work in ONLV after Scenery support lands:

- Replace the app-owned filesystem implementation behind `drive`; the remaining package is now an ONLV compatibility/domain adapter over `scenery.sh/storage`.
- Replace job artifact persistence/download with `scenery.sh/storage`.
- Replace maps artifact persistence with Scenery storage writes.
- Replace Pulse Drive list/write/delete calls to `/drive-index` and `/drive/*` with generated Scenery storage client calls.
- Run ONLV `scenery check --json`, `go test ./...`, frontend type/lint, and app harness.

Validated ONLV evidence on 2026-06-22:

- `/Users/petrbrazdil/Repos/onlv` branch `feat/scenery-storage-onlv-migration` declares storage cell `onlv`, default store `app`, and managed `dev.services.storage` with `kind: "zerofs"` in `.scenery.json`.
- `/Users/petrbrazdil/Repos/onlv/drive` no longer owns `var/storage`; helpers use `scenery.sh/storage` and tests configure the Scenery local runtime backend.
- ONLV job artifact downloads stream from Scenery storage; job and maps artifact writes call `SaveFilesContext`.
- Pulse Drive, viewer lists, contact upload, annotation upload, and scene delete use generated `client.storage` helpers. Public `/drive/*` GET remains for direct asset URLs that cannot attach in-memory auth headers.
- Validation passed: `go run ./cmd/scenery check --json --app-root /Users/petrbrazdil/Repos/onlv`, `go test ./drive ./maps ./jobs -count=1`, `go test ./...`, `(cd apps/pulse && bun run typecheck)`, `(cd apps/pulse && bun run lint)`, `go run ./cmd/scenery inspect storage --json --app-root /Users/petrbrazdil/Repos/onlv`, `go run ./cmd/scenery harness --json --write --app-root /Users/petrbrazdil/Repos/onlv`, an isolated `scenery storage put|get` smoke comparing uploaded/downloaded bytes, and `just repo-harness` in a temporary clean ONLV worktree with the tracked migration diff applied. ONLV `just repo-harness` in the original worktree was also attempted but failed on pre-existing untracked `docs/agent/rooftopology-*.md` conversation dumps with broken markdown links; those files were not part of this migration and were intentionally left untouched.

## Validation and Acceptance

Minimum acceptance for the Scenery PR series:

- `go test ./...` passes.
- `go test ./cmd/scenery ./storage ./internal/storage` passes with storage-specific tests.
- `scenery check --json` passes against `testdata/apps/storage-basic`.
- `scenery inspect storage --json --app-root testdata/apps/storage-basic` emits `scenery.storage.inspect.v1` and validates against `docs/schemas/scenery.storage.inspect.v1.schema.json`.
- `scenery up --app-root testdata/apps/storage-basic --json --detach` starts ZeroFS when a configured ZeroFS binary/container is available; the app can write, list, read, range-read, and delete an object.
- `scenery storage put|get|ls|stat|rm` works against the fixture app's active session.
- Two different worktrees/sessions for the same app storage cell share one ZeroFS substrate and object namespace: an object written from worktree A is visible from worktree B without copy/sync, and inspect output reports the same `storage_cell_id` for both.
- `scenery harness self --summary --write` records storage capability evidence or a clear skipped diagnostic when the ZeroFS binary/container is unavailable.
- No target app package needs to import ZeroFS packages, set `CGO_CFLAGS`, set `CGO_LDFLAGS`, set `LD_LIBRARY_PATH`, or know the S3 endpoint.
- The default ZeroFS config has no NFS server, no NBD server, no RPC TCP listener, no wildcard bind, and no public 9P listener. It may have a private loopback 9P activation listener and a Web UI listener only on private loopback or equivalent private upstream, and Scenery must expose the Web UI only behind a protected operator route.
- Storage routes default to authenticated access and support tenant scoping when standard auth has tenant data.
- Large `PutFile` and HTTP upload paths stream without reading the whole object into memory; add a regression test or benchmark that fails if a 256 MiB fixture is buffered all at once.
- Docs and schemas are updated: `docs/local-contract.md`, `docs/agent-guide.md`, `docs/app-development-cookbook.md`, `docs/environment.md`, `docs/environment.registry.json`, `docs/knowledge.json`, and `SKILL.md`.

Production-readiness gates before promoting beyond beta:

- Legal/licensing decision for bundling, linking, or distributing ZeroFS is recorded.
- S3-compatible provider behavior is tested with a store that either supports conditional put or uses configured Redis `conditional_put`.
- Crash/restart test proves fsync/sync behavior and object visibility after Scenery/ZeroFS restart.
- Concurrency test covers parallel writes/lists/deletes under one tenant and two tenants.
- Secret redaction tests prove credentials never appear in inspect JSON, logs, dashboard events, or harness artifacts.

## Idempotence and Recovery

All config/schema/docs changes are re-runnable. Unknown fields remain rejected, so partial config experiments fail early.

Managed ZeroFS startup must be idempotent and worktree-shared:

- If the same storage cell already has a live ZeroFS process, any worktree/session reuses it after health probing and records a new lease.
- If shared cell sockets exist but the process is dead, the lock owner removes stale sockets and restarts the shared process.
- If a previous shared ZeroFS config exists, regenerate it deterministically from app config and env; do not edit it in place by hand.
- `scenery down` for one worktree/session removes only that session lease and route/proxy attachment. It must not stop the shared ZeroFS cell while another lease is active.
- `scenery down --state` for one worktree/session removes only session-local route/proxy state by default. Shared storage-cell data under the agent storage root requires an explicit storage-cell cleanup command or an all-leases-gone state cleanup path recorded in docs.
- `scenery storage status --json` and `scenery inspect storage --json` must make lease ownership clear before any destructive cleanup.
- Failed startup leaves shared logs and generated config for inspection; the next `scenery up` from any worktree retries from the shared cell lock.

Object operations must be safe to retry where possible:

- `Put` without `IfNoneMatch` overwrites deterministically.
- `Put` with `IfNoneMatch` must fail if the object exists.
- `Delete` of a missing key returns a typed not-found error; CLI may offer `--ignore-missing` later but not in v1.
- `DeletePrefix` is best-effort over listed children and should report partial failures with keys.
- List cursors are opaque and may expire; clients must retry from prefix if a cursor is rejected.

If the selected 9P implementation proves too large or unsafe, pause after the config/lifecycle/package milestones and update the Decision Log. Acceptable fallback is a Scenery-owned helper process that carries ZeroFS FFI and exposes a small pure-Go storage proxy protocol to apps. Do not fall back to app-local POSIX roots or a revived app-specific drive service.

## Artifacts and Notes

Expected new or changed files:

```text
storage/storage.go
storage/errors.go
storage/keys.go
storage/http.go
internal/storage/*
cmd/scenery/storage.go
cmd/scenery/dev_storage.go
cmd/scenery/dev_storage_test.go
cmd/scenery/storage_test.go
docs/storage.md
docs/schemas/scenery.storage.inspect.v1.schema.json
testdata/apps/storage-basic/*
```

Expected `docs/plans/active.md` entry:

```markdown
- [0078 Scenery Storage](0078-scenery-storage.md)
  - Status: active
  - Owner: scenery runtime / storage / ONLV integration
  - Created: 2026-06-22
  - Focus: add Scenery-owned storage as an app capability, backed first by managed ZeroFS over 9P Unix sockets for app operations plus a protected ZeroFS Web UI operator route, with a worktree-shared storage cell, stream-first Go APIs, generated browser routes, CLI/inspect/harness support, and a migration path for ONLV to delete its app-local drive service.
```

Expected `docs/knowledge.json` entry should mark the plan active, owned by `scenery runtime / storage`, quality `B`, last reviewed `2026-06-22`, review after `2026-07-22`, and tag it with `plans`, `storage`, `zerofs`, `runtime`, and `onlv`.

Keep raw credentials out of every artifact. Store only redacted env names, socket paths, process IDs, readiness state, and object metadata.

## Interfaces and Dependencies

Public app package:

```text
scenery.sh/storage
```

New or extended config:

```text
.storage
.storage.cell_id
.storage.share
.storage.default
.storage.stores.<name>.kind
.storage.stores.<name>.access
.storage.stores.<name>.tenant_scoped
.storage.stores.<name>.max_object_bytes
.dev.services.<name>.kind = "zerofs"
.dev.services.<name>.mode = "local"
.dev.services.<name>.image
.dev.services.<name>.route
.dev.services.<name>.env
```

New CLI:

```text
scenery inspect storage --json
scenery storage status --json
scenery storage webui [--json]
scenery storage ls <store> [--prefix <prefix>] [--cursor <cursor>] [--limit <n>] [--json]
scenery storage stat <store> <key> --json
scenery storage put <store> <key> <file>
scenery storage get <store> <key> [--output <file>]
scenery storage rm <store> <key> [--recursive]
```

New JSON schema candidates:

```text
docs/schemas/scenery.storage.inspect.v1.schema.json
docs/schemas/scenery.storage.list.v1.schema.json
docs/schemas/scenery.storage.object.v1.schema.json
```

Historical candidate names from the initial plan are below. Current supported env vars live in `docs/environment.md` and `docs/environment.registry.json`; `SCENERY_DEV_ZEROFS_BIN` was not retained.

```text
SCENERY_STORAGE_CONFIG
SCENERY_STORAGE_DEFAULT
SCENERY_STORAGE_CELL_ID
SCENERY_STORAGE_CELL_ROOT
SCENERY_STORAGE_PROXY_SOCKET
SCENERY_DEV_ZEROFS_BIN
SCENERY_DEV_ZEROFS_IMAGE
SCENERY_DEV_ZEROFS_EXTERNAL
SCENERY_ZEROFS_WEBUI_URL
SCENERY_ZEROFS_WEBUI_PORT
SCENERY_STORAGE_UID
SCENERY_STORAGE_GID
```

Use a single spelling for ZeroFS env vars. Prefer `ZEROFS` over `ZEROS`; do not ship both.

External dependencies:

- ZeroFS process or container for managed dev/runtime storage.
- ZeroFS 9P Unix socket for file operations.
- ZeroFS Web UI private listener for operator/debug inspection.
- Optional ZeroFS RPC Unix socket for health, checkpoints, flush, and monitoring commands.
- S3-compatible object store only behind ZeroFS, not directly in app code.
- Redis `conditional_put` only when the configured S3-compatible store lacks conditional put support.
- Possible ZeroFS Go FFI/native library only inside a Scenery-owned helper boundary, not in target app packages.

Security boundaries:

- 9P endpoint is local/private and unauthenticated by ZeroFS. Scenery auth is enforced above it.
- ZeroFS Web UI is a private upstream; Scenery access policy is enforced before users or agents can reach it.
- Store keys are tenant-scoped by default when app auth provides tenant data.
- Shared worktree storage means branch/worktree isolation is not a storage security boundary. Apps that need isolated experiment data must use a different explicit `storage.cell_id` or a future non-default sharing mode.
- Secret-bearing env values are passed only to the ZeroFS process/helper, never to generated browser clients or inspect JSON.
- Generated storage routes are `auth` by default; public access is an explicit future policy.
