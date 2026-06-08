# onlava

**One CLI for building, running, and inspecting Go services — built for humans and AI agents.**

onlava is a Go-native local runtime and toolchain for building service applications from ordinary Go packages.

Applications mark their root with `.onlava.json`, declare endpoints with `//onlava:` directives, and run as one local HTTP server. onlava handles service discovery, route registration, auth context, request decoding, generated internal calls, local development supervision, inspection, logs, traces, metrics, and TypeScript client generation.

onlava is used in production. The stable v0 surface is intentionally small and Go-first; the local dashboard, observability, Grafana workbench, local HTTPS routing, Temporal worker tooling, and cron UI are development-focused capabilities. Their backing services and files are substrate details unless you intentionally debug them.

## Why onlava?

- **Go source is the app model.** Services, APIs, auth handlers, middleware, Temporal workflows and activities, and cron jobs are discovered from Go code.
- **One local app server.** `onlava serve` builds once and starts a headless, production-like HTTP server.
- **Full local dev loop.** `onlava up` runs the app session with file watching, rebuild/restart supervision, dashboard, API explorer, logs, traces, metrics, Grafana, and optional HTTPS local domains.
- **Typed HTTP by default.** onlava decodes path params, query params, headers, cookies, and JSON bodies into Go structs, then encodes typed responses.
- **Generated internal calls.** Endpoint-to-endpoint calls are rewritten to generated helpers so private access, auth context, and routing semantics are preserved.
- **Inspectable by tools and agents.** `onlava inspect`, `onlava check`, `onlava logs`, and `onlava harness` expose machine-readable JSON contracts.
- **Generated clients.** onlava can generate a TypeScript client with JSON and local wire-format support.

## Status

Available now:

- `.onlava.json` root discovery
- `onlava up`, `onlava serve`, `onlava task`, `onlava build`, `onlava check`
- typed and raw HTTP endpoints
- public, auth, and private endpoints
- auth handlers and request auth helpers
- service struct initialization
- middleware
- private/internal endpoint calls
- secrets from environment and local `.env`
- local logs, traces, and metrics inspection
- local observability and Grafana capabilities
- Temporal workflow/activity and cron local runtime support
- local HTTPS edge and frontend routing with optional trust-store installation
- dashboard and API explorer
- configured generators, SQLC refresh, database lifecycle commands, and repo task commands
- app-local code tasks
- TypeScript client generation
- JSON/wire benchmark fixture

Stable v0 API details live in [docs/local-contract.md](docs/local-contract.md). Agent workflows live in [docs/agent-guide.md](docs/agent-guide.md). Architecture notes live in [ARCHITECTURE.md](ARCHITECTURE.md).

## Requirements

- Go 1.26+
- Bun, only when working on the dashboard UI or the benchmark fixture
- `psql`, only when using `onlava db psql`

Run `onlava doctor --json` after install when you want a read-only readiness report for the host, Go toolchain, disk/memory resources, and optional local-development dependencies.

## Install From Source

```sh
git clone https://github.com/pbrazdil/onlava.git onlava
cd onlava
go install ./cmd/onlava
onlava doctor --json
onlava version --json
```

The module path is `github.com/pbrazdil/onlava`. Source installs are useful when working from a checkout or testing unreleased changes.

## Prebuilt CLI Binaries

Tagged releases publish prebuilt `onlava` archives for macOS, Linux, and Windows on the [GitHub Releases](https://github.com/pbrazdil/onlava/releases) page.

After installing a prebuilt binary, verify it with:

```sh
onlava version --json
```

## Agent Skill

onlava includes an installable agent skill for using onlava apps:

```sh
npx skills add https://github.com/pbrazdil/onlava
```

The skill teaches agents the onlava app model, directives, local development workflow, debugging commands, observability, database inspection, and TypeScript client generation.

The skill is shared runtime knowledge. Client apps should still keep a small app-local `AGENTS.md` for app root, frontend roots, generated client output paths, required environment names, validation commands, and product invariants. Do not copy the whole skill into every app; keep shared onlava behavior in `SKILL.md` and app-specific facts in the client repository.

## A Minimal App

Create `.onlava.json`:

```json
{"name":"hello"}
```

Create `go.mod`:

```go
module example.com/hello

go 1.26.3

require github.com/pbrazdil/onlava v0.0.0

replace github.com/pbrazdil/onlava => /path/to/onlava
```

Create `service/api.go`:

```go
package service

import "context"

type HelloResponse struct {
	Message string `json:"message"`
}

//onlava:api public path=/hello/:name method=GET
func Hello(ctx context.Context, name string) (*HelloResponse, error) {
	return &HelloResponse{Message: "hello " + name}, nil
}
```

Run it:

```sh
onlava check --json
onlava serve
```

Call it:

```sh
curl http://127.0.0.1:4000/hello/world
```

## Local Development

Use `onlava up` for the full development platform:

```sh
onlava up
```

Common options:

```sh
onlava up --port 4000 --listen 127.0.0.1
onlava up --json
onlava up --detach
onlava system edge dns install
onlava system edge privileged install
onlava system edge install
onlava system edge trust
onlava logs --follow
onlava console
```

`--detach` starts an agent-backed dev session in the background and returns after the session is registered. `onlava logs --follow` follows the current session logs from VictoriaLogs. `onlava console` opens a source-aware terminal console when attached to a real TTY. `onlava down` stops the current or selected session.

`onlava up` uses canonical agent-routed session URLs from `.onlava.json` proxy config. Generated local routes default to `https://api.<session>.local.dev`, `https://console.<session>.local.dev`, and frontend routes under the same `local.dev` base. Configured hosts are exposed separately as friendly aliases only when the live session owns that free alias. Stale alias leases are reclaimed after owner verification; use `onlava up --claim-aliases` only when intentionally transferring live aliases to this session. Use `onlava system edge dns install`, `onlava system edge privileged install`, `onlava system edge install`, and `onlava system edge trust` when you want trusted wildcard local HTTPS routes on the default HTTPS port; edge syncs managed dnsmasq and Caddy when needed and keeps Caddy user-owned.

Example proxy config:

```json
{
  "name": "myapp",
  "proxy": {
    "workspace": "myteam",
    "route_base_domain": "local.dev",
    "frontends": {
      "web": {
        "root": "apps/web"
      },
      "blog": {
        "root": "apps/blog",
        "upstream": "127.0.0.1:5174"
      }
    }
  }
}
```

## CLI Overview

```text
onlava up [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [--claim-aliases] [-v|--verbose] [--json] [--detach]
onlava logs --follow [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [--jsonl|--json]
onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]
onlava system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava system edge install|trust|status|restart|uninstall|dns|privileged [--json]
onlava ps --json [--app-root <path>] [--session <id>] [--watch]
onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
onlava prune --older-than <duration> [--app-root <path>] [--json]
onlava serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
onlava worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
onlava worker deployment set-current --build-id <id> [--deployment <name>] [--app-root <path>] [--json]
onlava worker deployment ramp --build-id <id> --percentage <n> [--deployment <name>] [--app-root <path>] [--json]
onlava worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
onlava version [--json]
onlava system toolchain list [--json] [--include-source-locks] [--images]
onlava system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
onlava system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
onlava system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
onlava doctor [--app-root <path>] [--json]
onlava build [--app-root <path>] [-o <path>]
onlava check [--app-root <path>] [--json]
onlava generate [--app-root <path>] [--dry-run] [--json]
onlava generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
onlava generate sqlc [--app-root <path>] [--dry-run] [--json]
onlava task list [--app-root <path>] [--json]
onlava task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
onlava task run <name> [--app-root <path>]
onlava task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
onlava task graph --json [--app-root <path>]
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--json] [--write] [--quick|--race|--release]
onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava traces list --json [--app-root <path>]
onlava metrics list --json [--app-root <path>]
onlava traces clear --json [--app-root <path>]
onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [-f|--follow] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
onlava db psql [--app-root <path>] [psql args...]
onlava db apply [--app-root <path>] [--json]
onlava db seed [--app-root <path>] [--dry-run] [--json]
onlava db setup [--app-root <path>] [--json]
onlava db reset [--app-root <path>]
onlava db drop [--app-root <path>]
onlava db snapshot create|restore <name> [--app-root <path>]
onlava db branch status|list [--app-root <path>] [--json]
onlava db branch checkout <name> [--app-root <path>] [--json]
onlava db branch reset [--app-root <path>] [--yes]
onlava db branch delete <name> [--app-root <path>] [--force]
onlava db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
onlava db branch diff <branch> [--app-root <path>] [--json]
onlava db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
onlava db branch prune [--older-than <duration>] [--app-root <path>] [--json]
onlava db neon install|start|status|logs|stop|restart|uninstall [--json]
onlava worktree create <name> [--from <branch>] [--app-root <path>] [--json]
onlava worktree list [--app-root <path>] [--json]
onlava worktree remove <name> [--app-root <path>] [--db] [--json]
```

See [docs/local-contract.md](docs/local-contract.md) for the full command contract and JSON schema list.

## Public Go Packages

- `github.com/pbrazdil/onlava` exposes app metadata and current request metadata.
- `github.com/pbrazdil/onlava/auth` exposes request auth state helpers.
- Standard auth owns its tenant tables under `onlava_auth`; app-local `tenants` services or tables are product-domain concerns.
- `github.com/pbrazdil/onlava/errs` exposes coded errors and HTTP status mapping.
- `github.com/pbrazdil/onlava/middleware` exposes middleware request/response types.
- `github.com/pbrazdil/onlava/temporal` exposes workflow/activity declarations and start helpers for the onlava-managed Temporal runtime.
- `github.com/pbrazdil/onlava/cron` exposes cron job declarations.
- `github.com/pbrazdil/onlava/pgxpool` wraps `pgxpool` with onlava DB tracing.
- `github.com/pbrazdil/onlava/et` exposes endpoint/service mocking helpers for tests.

## TypeScript Client Generation

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava generate client --lang typescript --output ./src/onlava-client.ts
```

The generated client understands the app's route model and local wire capabilities. The benchmark fixture in [benchmarks/json-wire](benchmarks/json-wire) compares JSON, wire JSON, binary wire, and automatic wire modes.

`WithMeta` methods also expose parsed `txid` metadata from `X-Txid`/`X-TXID`. Electric-backed write flows can use `observeAPIResponseTxid` to report later Electric observation failures as sync/substrate failures after a committed mutation, rather than as API mutation failures.

Apps can also configure `generators.clients` and use `onlava generate client` or `onlava generate --dry-run --json` to inspect and run configured generators. `onlava generate sqlc` is for generated source artifacts; it must not apply database schema or seed data.

The DB lifecycle split uses `onlava db apply` for schema/app database mutation, `onlava db seed` for initial data such as `SERVICE/db/seed.sql`, and `onlava db setup` for apply then seed. Seed files fail closed when previously-applied content changes or destructive SQL is detected.

`onlava up` runs the setup lifecycle before app startup when DB setup inputs exist, using the same managed `DatabaseURL` that the app receives. Rebuilds skip setup until the apply config or seed file hashes change.

The first Neon dev-cell slices expose `onlava db neon status --json` for generated local Neon substrate state plus Docker/image/container health probes, reserved loopback debug ports, and listener checks for running components, `onlava db neon start --json` and `onlava db neon stop --json` for the generated Docker Compose project, `onlava db neon restart --json` for restarting existing Onlava-owned Neon containers, `onlava db branch status --json` for the worktree branch pin at `.onlava/worktree-db.json`, and `onlava db branch list --json` for Onlava-owned local branch leases in `branches.json` under the agent home. Branch status and list can distinguish pending, missing, expired, protected parent, and ready local leases; pending branch status also reports whether the generated dev-cell is missing or not ready yet. Ready leases may include redacted endpoint metadata but never raw connection URLs, and protected parent leases do not expose endpoint or app-session connection metadata. `onlava db branch checkout <name> --json` writes the local pin and runs the branch-provider ensure boundary; `ONLAVA_DEV_NEON_SELFHOST_DRIVER` selects the actual `neon-selfhost` branch driver, while `ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` is only a local Postgres-shaped development fallback. Without either driver checkout only renews the local lease; a configured driver can mark the branch ready by returning endpoint metadata through the JSON contract. Checkout refuses to reuse a matching foreign local lease. `delete` can remove pending local leases after the documented parent/current guards and delegates ready branch deletion to the configured driver; `reset`, `restore`, and `diff` also delegate to the driver when configured. Without a driver, ready branch delete/reset/restore/diff still return explicit backend placeholders. `expire`, same-project `prune`, and selected-session `down --db` update only Onlava-owned local registry metadata for now and leave foreign leases alone. `onlava down --state` removes the local worktree pin. `onlava worktree create <name> --json` creates a Git worktree and writes the target pin for Neon apps, rolling the worktree back if pin creation fails. `onlava worktree remove <name> --db` verifies the Git worktree before removing local `.onlava` state. `onlava up`, `onlava db psql`, and Electric can consume a non-parent ready lease endpoint, but full built-in Neon branch creation and Electric slot lifecycle hardening are still tracked in the active ExecPlan.

## Managed Toolchain

The root `onlava.toolchain.json` freezes Onlava-owned local tools, images, plugins, and source lock references for this source version. Managed binaries install under `.onlava/toolchain/` by default, while machine-level edge tools install under `~/.onlava/toolchain/`; set `ONLAVA_TOOLCHAIN_DIR` to use a controlled cache elsewhere.

```sh
onlava system toolchain list --json
onlava system toolchain sync --json
onlava system toolchain verify --json
```

Caddy edge, Grafana, Victoria sidecars, and the local Temporal CLI are backing substrate for local capabilities. Caddy edge is managed-toolchain only; for the other tools, use documented env overrides, the managed store, `onlava ps --json` substrate records, and the recorded stdout/stderr log paths when intentionally debugging them. They do not silently fall back to system `PATH` binaries.

## Observability And Inspection

onlava exposes local development logs, traces, metrics, and Grafana through app-session capabilities. The current backing substrate can run VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana for richer local inspection.

Useful commands:

```sh
onlava logs --session current --limit 200
onlava logs --follow
onlava console
onlava logs --session current --source api --level error --jsonl --limit 200
onlava inspect routes --json
onlava inspect endpoints --json
onlava traces list --json --session current --since 15m --slowest
onlava metrics list --json --session current --since 1h
onlava ps --json
onlava harness --json --write
```

Grafana substrate files are generated under `.onlava/grafana/` when you need to debug them. Shared Temporal and Victoria substrate failures are exposed in `onlava ps --json` as `last_exit` / `component_exits` and emit structured dev log events with component, PID, exit code or signal, and log paths. Set `ONLAVA_DEV_GRAFANA=0` to disable Grafana or `ONLAVA_DEV_GRAFANA=1` to require it during `onlava up` startup.

## Development

Run the Go test suite:

```sh
go test ./...
```

Rebuild the CLI after changes:

```sh
go install ./cmd/onlava
```

Run the self-harness when making substantial changes:

```sh
onlava harness self --json --write
```

Run the JSON/wire benchmark:

```sh
benchmarks/json-wire/run.sh
```

## Contributing

onlava prefers small, explicit changes and minimal dependencies. When adding behavior, keep the parsed app model as the source of truth and add tests at stable boundaries: parser validation, generated code, runtime HTTP behavior, CLI JSON contracts, and fixture apps.

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and pull request guidance.

Before opening a pull request, run:

```sh
go test ./...
go install ./cmd/onlava
```

For larger changes, also run:

```sh
onlava harness self --json --write
```

## Security

Please do not open public issues for vulnerabilities. Report security issues to security@onlava.com. See [SECURITY.md](SECURITY.md).

## License

onlava is licensed under the [Apache License 2.0](LICENSE).
