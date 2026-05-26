Yes. The better solution is **not “allocate more ports better.”** The better solution is to **remove almost all public per-worktree ports from the model**.

I would change the architecture from:

```text
each worktree owns:
  API port
  dashboard port
  proxy ports
  frontend ports
  Postgres port
  Electric port
  Temporal port
  Temporal UI port
  Victoria ports
  Grafana port
```

to:

```text
one machine-local onlava daemon/router owns:
  one control socket
  one HTTPS/HTTP ingress
  one dashboard
  one observability substrate
  one Temporal substrate
  optional shared Postgres substrate

each worktree owns:
  private app process
  private runtime identity
  private DB/schema or DB container
  private task queues/namespaces
  private build/cache/log state
```

That is materially better than my prior plan because it turns the port problem from **N worktrees × M services** into **one local ingress plus private session backends**.

---

# Better solution: `onlava agent`

Introduce a long-running local daemon:

```sh
onlava agent
```

But the user normally never runs it directly. `onlava dev` auto-starts it if missing.

The daemon owns:

```text
~/.onlava/run/agent.sock             # local control socket
https://console.onlava.localhost     # global dashboard
https://*.onlava.localhost           # routed session URLs
optional :80/:443 or high-port fallback
```

Then every worktree registers a session with the daemon:

```sh
cd ~/Repos/onlv-fr-123
onlava dev

cd ~/Repos/onlv-fr-456
onlava dev
```

The daemon creates:

```text
session: onlv-fr-123-a81c7d
api URL:      https://api.onlv-fr-123-a81c7d.onlava.localhost
pulse URL:    https://pulse.onlv-fr-123-a81c7d.onlava.localhost
console URL:  https://console.onlava.localhost/s/onlv-fr-123-a81c7d
mcp URL:      https://mcp.onlv-fr-123-a81c7d.onlava.localhost/sse
```

There is one trusted local CA, one router, one dashboard, one owner of process/session state.

---

# Core principle

**Worktrees should not bind stable host ports.**

They should bind one of:

```text
Unix domain sockets, preferred
daemon-allocated private loopback ports, acceptable fallback
Docker internal network ports, preferred for containers
```

The only stable external surface is the daemon/router.

So instead of saying:

```text
this worktree's API is on 127.0.0.1:4000
```

say:

```text
this worktree's API is registered as session api backend
```

The daemon knows whether that backend is:

```text
unix:///.../.onlava/sessions/<id>/run/api.sock
http://127.0.0.1:48231
http://container-name:4000 on a Docker network
```

The human and browser never care.

---

# Why this is better than pure per-session port allocation

My previous answer isolated every service with its own generated ports. That works, but it still leaves you with dozens of moving pieces per worktree.

The better design makes most ports disappear.

## Old improved design

```text
worktree A:
  api 4317
  dashboard 9562
  temporal 17487
  temporal ui 18487
  postgres 15487
  electric 13487
  grafana 20487
  victoria metrics 21487
  victoria logs 22487
  victoria traces 23487
  pulse 24487
  blog 25487

worktree B:
  same list, different ports
```

That is safe, but noisy.

## Better daemon design

```text
machine:
  onlava router: 443 or 10443
  onlava control socket: ~/.onlava/run/agent.sock
  optional central dashboard
  optional central observability
  optional central Temporal
  optional central Postgres

worktree A:
  app backend: private socket/hidden port
  frontends: hidden ports or container network
  DB: isolated database/schema or private container
  queues: isolated namespace/task queue

worktree B:
  same, but no public port conflict possible
```

You still may have hidden ports for tools that cannot speak Unix sockets, but those ports are daemon-internal implementation details. They are not configured by humans, not in `.env`, and not stable API.

---

# Concrete architecture

## 1. `onlava agent` as the only global port owner

Add package:

```text
internal/agent
internal/agent/router
internal/agent/session
internal/agent/supervisor
internal/agent/substrate
```

The daemon owns:

```text
control API over Unix socket:
  ~/.onlava/run/agent.sock

session registry:
  ~/.onlava/agent/sessions.db

single dashboard:
  https://console.onlava.localhost

single reverse proxy:
  https://<route>.<session>.onlava.localhost
```

The daemon should be authenticated by filesystem permissions on the socket plus a local token for browser/MCP access.

This directly fixes the current issue where dashboard binding is global and cleanup can stop an existing onlava dev process that merely “looks like” an onlava dashboard process. The current dashboard default is `127.0.0.1:9401`, and the existing reaping logic is tied to the default dashboard address/process inspection.  

---

## 2. `onlava dev` becomes an agent client

Current `onlava dev` starts the local development platform itself: app process, dashboard, Temporal, Victoria, Grafana, DB Studio, proxy, and watcher.  

Change that.

New flow:

```text
onlava dev
  1. discover app root
  2. ensure agent is running
  3. ask agent to create/update session
  4. compile app
  5. start app backend privately
  6. attach terminal to session logs
  7. print stable routed URLs
```

The CLI remains interactive, but global resource ownership moves to the daemon.

Ctrl-C can either stop the session or detach, depending on mode:

```sh
onlava dev              # attached; Ctrl-C stops session
onlava dev --detach     # start session and return
onlava attach           # attach to logs
onlava down             # stop current session
```

---

## 3. Apps should support Unix socket listeners

Right now the app child gets:

```sh
ONLAVA_LISTEN_ADDR=127.0.0.1:<port>
```

from the dev supervisor. 

Add:

```sh
ONLAVA_LISTEN_NETWORK=unix
ONLAVA_LISTEN_ADDR=.onlava/sessions/<id>/run/api.sock
```

Fallback:

```sh
ONLAVA_LISTEN_NETWORK=tcp
ONLAVA_LISTEN_ADDR=127.0.0.1:<agent-private-port>
```

This is the single most important technical change. Once app servers can listen on Unix sockets, the API port problem disappears.

The local router can reverse-proxy HTTP to Unix sockets cleanly.

---

## 4. One global dashboard, not one dashboard per worktree

Do not start a dashboard server per worktree.

Use one dashboard:

```text
https://console.onlava.localhost
```

It shows all live sessions:

```text
onlv-fr-123-a81c7d
onlv-fr-456-b44f20
onlava-own-change-9f021a
```

Session-specific dashboard URL:

```text
https://console.onlava.localhost/s/onlv-fr-123-a81c7d
```

The dashboard store becomes daemon-owned and keyed by:

```text
session_id
runtime_app_id
app_root
branch
pid
started_at
```

This also eliminates the current global dashboard port fight.

---

## 5. One global observability substrate

Do **not** start Victoria and Grafana per worktree by default.

Current onlava dev starts Victoria sidecars and Grafana by default when available, with fixed default Victoria ports and Grafana default port unless overridden.  

Better:

```text
one VictoriaMetrics
one VictoriaLogs
one VictoriaTraces
one Grafana
```

Every emitted metric/log/trace includes:

```text
onlava.session_id
onlava.app_id
onlava.app_root_hash
onlava.branch
onlava.worktree
```

Then:

```sh
onlava inspect traces --session current --json
onlava inspect metrics --session current --json
onlava logs --session current
```

The Grafana dashboards get a session variable.

This is better than per-session Grafana/Victoria because:

```text
less CPU
less RAM
fewer downloads
no Grafana port collision
no Victoria port collision
faster startup
easier historical comparison between sessions
```

For hard isolation, add:

```sh
onlava dev --isolated-observability
```

but default should be shared substrate with session labels.

---

## 6. One Temporal dev server, isolated by namespace/task queue

Current onlv enables Temporal local auto-start and uses a fixed task queue prefix.  Current onlava Temporal dev logic reuses an already reachable Temporal address, otherwise starts a local Temporal server on the configured port and UI port `port + 1000`. 

Better:

```text
one daemon-owned Temporal dev server
session-specific namespace or task queue prefix
```

For each session:

```sh
TEMPORAL_ADDRESS=127.0.0.1:<daemon-temporal-port>
TEMPORAL_NAMESPACE=onlv-fr-123-a81c7d
ONLAVA_TEMPORAL_TASK_QUEUE=onlava.onlvnext-o5o2.onlv-fr-123-a81c7d
ONLAVA_TEMPORAL_DEPLOYMENT_NAME=onlava.onlvnext-o5o2.onlv-fr-123-a81c7d
ONLAVA_BUILD_ID=onlv-fr-123-a81c7d
```

This avoids running multiple Temporal servers, but still prevents workers from consuming each other’s tasks.

If Temporal namespaces are annoying in local dev, use one namespace plus strict session-prefixed task queues. Namespace isolation is cleaner; task-queue isolation is easier.

---

## 7. One Postgres substrate, isolated database per session

For onlv, the current Compose file publishes fixed Postgres host port `5433` and Electric host port `3000`, and uses a named volume.  That creates collisions and state bleed.

Better default:

```text
one daemon-managed Postgres cluster per Postgres major version
one database per session
```

Example:

```text
cluster: onlava-postgres-18
session DB: onlv_fr_123_a81c7d
```

Generated env:

```sh
DatabaseURL=postgresql://postgres:postgres@127.0.0.1:<daemon-pg-port>/onlv_fr_123_a81c7d?sslmode=disable
```

This is a good tradeoff:

```text
database state isolated
migrations isolated
only one Postgres process/container
no per-worktree Postgres port
fast reset/drop/recreate
```

Commands:

```sh
onlava db reset
onlava db psql
onlava db snapshot create before-refactor
onlava db snapshot restore before-refactor
```

For cases needing full cluster isolation:

```sh
onlava dev --isolated-postgres
```

But the default should be **shared cluster, isolated DB**.

---

## 8. Electric probably stays per session, but hidden behind the router

Electric is more likely to need per-session process/container because it is tied to one database URL.

Run it as:

```text
container: onlava-electric-<session_id>
network: onlava-agent
host port: none, or Docker-assigned ephemeral only
route: https://electric.<session>.onlava.localhost
```

The frontend gets:

```sh
ELECTRIC_URL=https://electric.onlv-fr-123-a81c7d.onlava.localhost
```

No fixed `3000`.

---

## 9. Frontends route through the daemon

The current local proxy already supports frontend upstream overrides through `ONLAVA_FRONTEND_<NAME>_ADDR`.  Use that internally, but hide it from humans.

For tools like Vite/Astro/Bun that need TCP:

```text
daemon allocates hidden loopback port
frontend binds there
router exposes stable hostname
```

Example:

```text
Pulse internal: 127.0.0.1:49231
Pulse external: https://pulse.onlv-fr-123-a81c7d.onlava.localhost
```

For frontends that can listen on Unix sockets later, switch them.

The checked-in onlv config has fixed hosts and a fixed blog upstream `127.0.0.1:4321`; the daemon should override these in effective dev config, not require changing `.onlava.json` per worktree. 

---

# What onlv becomes

`onlv` should stop being responsible for port orchestration.

Current `Justfile` hard-codes:

```text
app_root := "/Users/petrbrazdil/Repos/onlv"
onlava := "go -C ../pulse run ./cmd/onlava"
```

That is inherently hostile to worktrees. 

The better version is boring:

```make
set shell := ["zsh", "-cu"]

app_root := justfile_directory()
onlava := env_var_or_default("ONLAVA_BIN", "onlava")

@dev:
  {{onlava}} dev --app-root {{app_root}}

@down:
  {{onlava}} down --app-root {{app_root}}

@urls:
  {{onlava}} status --app-root {{app_root}} --json

@psql:
  {{onlava}} db psql --app-root {{app_root}}
```

No Compose port choices. No Overmind port choices. No absolute paths. No `lsof`. No manual cleanup.

If you keep Overmind, make Overmind a child detail of the onlava session, not the owner of the world.

---

# What `.onlava.json` should and should not do

Do not mutate `.onlava.json` per session.

Keep checked-in identity stable:

```json
{
  "name": "onlvnext-o5o2"
}
```

Then in dev mode, the daemon derives:

```text
base_app_id: onlvnext-o5o2
runtime_app_id: onlvnext-o5o2--onlv-fr-123-a81c7d
session_id: onlv-fr-123-a81c7d
```

Current onlava already treats app identity as `id` when present, otherwise `name`; onlv currently only has `name`.  

So the dev runtime id should be an **effective runtime override**, not a source config mutation.

---

# Route model

I would use host-based routing, not path-based routing, for apps/frontends:

```text
https://api.<session>.onlava.localhost
https://pulse.<session>.onlava.localhost
https://blog.<session>.onlava.localhost
https://electric.<session>.onlava.localhost
https://mcp.<session>.onlava.localhost
```

Dashboard can be path-based because it is the daemon UI:

```text
https://console.onlava.localhost/s/<session>
```

Host-based routing is better for:

```text
cookies
CORS
OAuth redirect URLs
frontend origin behavior
service workers
WebSockets/SSE
```

For onlv auth, generate these per session:

```sh
PublicAppURL=https://pulse.<session>.onlava.localhost
APIBaseURL=https://api.<session>.onlava.localhost
AuthCookieDomain=
```

I would usually leave `AuthCookieDomain` empty in local session mode so cookies are host-only and cannot bleed between sessions.

---

# Session manifest still exists, but it is no longer a port spreadsheet

Still write:

```text
.onlava/sessions/<session_id>/manifest.json
```

But the manifest focuses on identity and routes:

```json
{
  "schema_version": "onlava.dev.session.v1",
  "session_id": "onlv-fr-123-a81c7d",
  "base_app_id": "onlvnext-o5o2",
  "runtime_app_id": "onlvnext-o5o2--onlv-fr-123-a81c7d",
  "app_root": "/Users/petrbrazdil/Repos/onlv-fr-123",
  "state_root": "/Users/petrbrazdil/Repos/onlv-fr-123/.onlava/sessions/onlv-fr-123-a81c7d",
  "routes": {
    "api": "https://api.onlv-fr-123-a81c7d.onlava.localhost",
    "pulse": "https://pulse.onlv-fr-123-a81c7d.onlava.localhost",
    "blog": "https://blog.onlv-fr-123-a81c7d.onlava.localhost",
    "mcp": "https://mcp.onlv-fr-123-a81c7d.onlava.localhost/sse",
    "dashboard": "https://console.onlava.localhost/s/onlv-fr-123-a81c7d",
    "electric": "https://electric.onlv-fr-123-a81c7d.onlava.localhost",
    "grafana": "https://grafana.onlava.localhost/d/onlava-dev-overview?var-session=onlv-fr-123-a81c7d",
    "temporal": "https://temporal.onlava.localhost/namespaces/onlv-fr-123-a81c7d"
  },
  "backends": {
    "api": {
      "network": "unix",
      "addr": ".onlava/sessions/onlv-fr-123-a81c7d/run/api.sock"
    }
  }
}
```

Hidden ports can be present for debugging, but they should not be the main interface.

---

# Ownership model

The daemon is the only process allowed to kill or reuse resources.

Each session process has:

```text
session_id
owner_pid
process_start_time
command_fingerprint
app_root
backend socket path
```

`onlava down` sends a request to the daemon:

```text
stop session <id>
```

The daemon stops only processes it started and owns.

No more “port 9401 is occupied by something that looks like onlava; kill it.” That class of cleanup is exactly what breaks parallel worktrees.

---

# Phased execution plan

## Phase 1: daemon + router, hidden TCP backend

Do this first. Do not wait for Unix sockets everywhere.

Deliver:

```text
onlava agent auto-start
onlava dev registers session
global dashboard
global router
session URLs
daemon-owned hidden loopback ports for app/frontends
onlava down
onlava status --json
```

Use daemon-allocated hidden ports internally.

Result:

```text
humans stop seeing ports
worktrees stop colliding externally
dashboard becomes global
proxy becomes global
```

## Phase 2: session identity everywhere

Add:

```text
runtime_app_id
session_id labels
session-specific logs
session-specific traces
session-specific metrics
session-specific auth/local URLs
session-specific Temporal task queue/build id
```

Result:

```text
no cross-session dashboard/log/trace/task collision
```

## Phase 3: shared substrates

Move these out of per-session startup:

```text
Grafana
VictoriaMetrics
VictoriaLogs
VictoriaTraces
Temporal dev server
Postgres cluster
```

Use session labels/namespaces/databases.

Result:

```text
faster startup
lower RAM
almost no fixed service ports
```

## Phase 4: Unix sockets

Add:

```text
ONLAVA_LISTEN_NETWORK=unix
Unix socket reverse proxy
optional Postgres Unix socket support
```

Result:

```text
API port fully disappears
fewer race windows
cleaner ownership
```

## Phase 5: onlava-owned dev services

Instead of `onlv` hand-running Compose, let onlava own dev services.

Add a dev services section, perhaps:

```json
{
  "dev": {
    "services": {
      "postgres": {
        "kind": "postgres",
        "version": "18",
        "isolation": "database"
      },
      "electric": {
        "kind": "container",
        "image": "electricsql/electric:canary",
        "env": {
          "DATABASE_URL": "$DatabaseURL",
          "ELECTRIC_INSECURE": "true",
          "ELECTRIC_USAGE_REPORTING": "false"
        },
        "route": "electric"
      }
    }
  }
}
```

Result:

```text
Justfile becomes tiny
Compose port publishing disappears
agents can start/stop/reset reliably
```

---

# What I would build first, specifically

The first serious PR should not be “port allocator.” It should be:

```text
PR: onlava agent MVP
```

Scope:

```text
1. Agent process with Unix control socket.
2. Auto-start from `onlava dev`.
3. Session registry keyed by app_root + branch + hash.
4. Global router on one configurable HTTPS port.
5. Per-session route registration.
6. Existing app still listens on daemon-allocated hidden TCP port.
7. Existing dashboard converted to daemon/global dashboard.
8. `onlava status --json`.
9. `onlava down`.
```

This avoids the hardest socket/runtime changes at first but immediately changes the mental model.

Then second PR:

```text
PR: session labels + runtime app id
```

Third PR:

```text
PR: global observability + Temporal substrates
```

Fourth PR:

```text
PR: Unix socket app backend
```

---

# What this buys you

With the daemon model, this becomes true:

```sh
git worktree add ../onlv-a -b feature/a
git worktree add ../onlv-b -b feature/b
git worktree add ../onlv-c -b feature/c

(cd ../onlv-a && just dev)
(cd ../onlv-b && just dev)
(cd ../onlv-c && just dev)
```

And you get:

```text
no user-selected ports
no per-worktree dashboard port
no per-worktree Grafana port
no per-worktree Victoria ports
no per-worktree Temporal port
no fixed Postgres/Electric host ports
no cross-worktree task queue pollution
no cross-worktree auth cookie pollution
no killing sibling worktrees
one place to inspect everything
```

That is the version I would aim for.

The earlier “session manifest with allocated ports” plan is a good fallback. The daemon/router/substrate plan is the cleaner end state.
