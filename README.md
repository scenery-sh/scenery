# scenery

**One CLI for building, running, and inspecting Go services — built for humans and AI agents.**

scenery is a Go-native local runtime and toolchain for building service applications from ordinary Go packages.

Applications mark their root with `.scenery.json`, declare endpoints with `//scenery:` directives, and run as one local HTTP server. scenery handles service discovery, route registration, auth context, request decoding, generated internal calls, local development supervision, inspection, logs, traces, metrics, and TypeScript client generation.

scenery is used in production. The stable v0 surface is intentionally small and Go-first; the local dashboard, observability, Grafana workbench, local HTTPS routing, Temporal worker tooling, and cron UI are development-focused capabilities. Their backing services and files are substrate details unless you intentionally debug them.

## Why scenery?

- **Go source is the app model.** Services, APIs, auth handlers, middleware, Temporal workflows and activities, cron jobs, and beta static model/page IR are discovered from Go code.
- **One local app server.** `scenery serve` builds once and starts a headless, production-like HTTP server.
- **Full local dev loop.** `scenery up` runs the app root's one live dev runtime with file watching, rebuild/restart supervision, dashboard, API explorer, logs, traces, metrics, Grafana, and optional HTTPS local domains.
- **Typed HTTP by default.** scenery decodes path params, query params, headers, cookies, and JSON bodies into Go structs, then encodes typed responses.
- **Generated internal calls.** Endpoint-to-endpoint calls are rewritten to generated helpers so private access, auth context, and routing semantics are preserved.
- **Inspectable by tools and agents.** `scenery inspect`, `scenery check`, `scenery logs`, `scenery harness`, and `scenery validate` expose machine-readable JSON contracts.
- **Generated clients.** scenery can generate a TypeScript client with JSON and local wire-format support.

## Status

Available now:

- `.scenery.json` root discovery
- `scenery up`, `scenery serve`, `scenery task`, `scenery validate`, `scenery build`, `scenery check`
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
- `psql`, only when using `scenery db psql`

Run `scenery doctor --json` after install when you want a read-only readiness report for the host, Go toolchain, disk/memory resources, Docker engine reachability, and optional local-development dependencies.

## Install From Source

```sh
git clone https://github.com/scenery-sh/scenery.git scenery
cd scenery
go install ./cmd/scenery
scenery doctor --json
scenery version --json
```

The module path is `scenery.sh`. Source installs are useful when working from a checkout or testing unreleased changes.

## Prebuilt CLI Binaries

Tagged releases publish prebuilt `scenery` archives for macOS, Linux, and Windows on the [GitHub Releases](https://github.com/scenery-sh/scenery/releases) page.

After installing a prebuilt binary, verify it with:

```sh
scenery version --json
```

## Agent Skill

scenery includes an installable agent skill for using scenery apps:

```sh
npx skills add https://github.com/scenery-sh/scenery
```

The skill teaches agents the scenery app model, directives, local development workflow, debugging commands, observability, database inspection, and TypeScript client generation.

The skill is shared runtime knowledge. Client apps should still keep a small app-local `AGENTS.md` for app root, frontend roots, generated client output paths, required environment names, validation commands, and product invariants. Do not copy the whole skill into every app; keep shared scenery behavior in `SKILL.md` and app-specific facts in the client repository.

## A Minimal App

Create `.scenery.json`:

```json
{"name":"hello"}
```

When an app requires Go build tags or other build-time flags, keep them in the app config instead of exporting `GOFLAGS` for every command:

```json
{"name":"hello","build":{"go_flags":["-tags=roofmapnet_native"]}}
```

Create `go.mod`:

```go
module example.com/hello

go 1.26.3

require scenery.sh v0.0.0

replace scenery.sh => /path/to/scenery
```

Create `service/api.go`:

```go
package service

import "context"

type HelloResponse struct {
	Message string `json:"message"`
}

//scenery:api public path=/hello/:name method=GET
func Hello(ctx context.Context, name string) (*HelloResponse, error) {
	return &HelloResponse{Message: "hello " + name}, nil
}
```

Run it:

```sh
scenery check --json
scenery serve
```

Call it:

```sh
curl http://127.0.0.1:4000/hello/world
```

## Local Development

Use `scenery up` for the full development platform:

```sh
scenery up
```

Common options:

```sh
scenery up --port 4000 --listen 127.0.0.1
scenery up --json
scenery up --detach
scenery system edge dns install
scenery system edge privileged install
scenery system edge install
scenery system edge trust
scenery logs --follow
scenery console
```

`--detach` starts the app root's agent-backed dev runtime in the background and returns after it is registered. `scenery logs --follow` follows that app root's logs from VictoriaLogs. `scenery console` opens a source-aware terminal console when attached to a real TTY. `scenery down` stops the app root's one live runtime. Use Git worktrees when you need multiple live code copies.

`scenery up` uses canonical agent-routed app URLs from `.scenery.json` proxy config. Generated local routes default to `https://api.<route-id>.local.dev`, `https://console.<route-id>.local.dev`, and frontend routes under the same `local.dev` base. The route id is internal state, not something users select. If `proxy.route_base_domain` is explicitly configured, the local edge is required for normal browser-facing URLs: startup fails loudly when DNS, the privileged listener, Caddy, or the HTTPS probe is not ready instead of publishing internal `:9440` router URLs as app routes. Configured hosts are exposed separately as friendly aliases only when the live app root owns that free alias. Stale alias leases are reclaimed after owner verification; use `scenery up --claim-aliases` only when intentionally transferring live aliases to this app root. Use `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when you want trusted wildcard local HTTPS routes on the default HTTPS port; edge syncs managed dnsmasq and Caddy when needed and keeps Caddy user-owned.

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
scenery up [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [-v|--verbose] [--json] [--detach]
scenery logs --follow [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [--jsonl|--json]
scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]
scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
scenery system edge install|trust|status|restart|uninstall|dns|privileged [--json]
scenery help <command>|all|--json
scenery ps [--json] [--app-root <path>] [--watch]
scenery down [--app-root <path>] [--db] [--state] [--all] [--json]
scenery prune --older-than <duration> [--app-root <path>] [--json]
scenery serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker bindings [--app-root <path>] [--out <dir>] [--json]
scenery worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
scenery worker deployment set-current --build-id <id> [--deployment <name>] [--app-root <path>] [--json]
scenery worker deployment ramp --build-id <id> --percentage <n> [--deployment <name>] [--app-root <path>] [--json]
scenery worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
scenery version [--json]
scenery system toolchain list [--json] [--include-source-locks] [--images]
scenery system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
scenery system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
scenery doctor [--app-root <path>] [--json]
scenery build [--app-root <path>] [-o <path>]
scenery check [--app-root <path>] [--json]
scenery generate [--app-root <path>] [--dry-run] [--json]
scenery generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
scenery generate sqlc [--app-root <path>] [--dry-run] [--json]
scenery task list [--app-root <path>] [--json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery task graph --json [--app-root <path>]
scenery validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery harness [--app-root <path>] [--json] [--write] [--with-validation[=<profile>]]
scenery harness self [--repo-root <path>] [--json] [--write] [--quick|--race|--release] [--fresh-tests]
scenery harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
scenery inspect app|routes|services|endpoints|models|views|wire|build|paths|generators|temporal --json [--app-root <path>]
scenery inspect docs --json [--repo-root <path>]
scenery traces list --json [--app-root <path>]
scenery metrics list --json [--app-root <path>]
scenery traces clear --json [--app-root <path>]
scenery logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [-f|--follow] [--jsonl|--json]
scenery test [--app-root <path>] [go test flags/packages...]
scenery generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
scenery db psql [--app-root <path>] [psql args...]
scenery db apply [--app-root <path>] [--json]
scenery db seed [--app-root <path>] [--dry-run] [--json]
scenery db setup [--app-root <path>] [--json]
scenery db reset [--app-root <path>]
scenery db drop [--app-root <path>]
scenery db snapshot create|restore <name> [--app-root <path>]
scenery db branch status|list [--app-root <path>] [--json]
scenery db branch checkout <name> [--app-root <path>] [--json]
scenery db branch reset [--app-root <path>] [--yes]
scenery db branch delete <name> [--app-root <path>] [--force]
scenery db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
scenery db branch diff <branch> [--app-root <path>] [--json]
scenery db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
scenery db branch prune [--older-than <duration>] [--app-root <path>] [--json]
scenery db postgres install|start|status|logs|stop|restart|uninstall [--json]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [--json]
scenery worktree list [--app-root <path>] [--json]
scenery worktree remove <name> [--app-root <path>] [--db] [--json]
```

For managed Postgres branches, `scenery db snapshot create|restore` uses host
`pg_dump` and `psql` against the ready branch database.

See [docs/local-contract.md](docs/local-contract.md) for the full command contract and JSON schema list.

## Public Go Packages

- `scenery.sh` exposes app metadata and current request metadata.
- `scenery.sh/auth` exposes request auth state helpers.
- Standard auth owns its tenant tables under `scenery_auth`; app-local `tenants` services or tables are product-domain concerns.
- `scenery.sh/errs` exposes coded errors and HTTP status mapping.
- `scenery.sh/middleware` exposes middleware request/response types.
- `scenery.sh/model` and `scenery.sh/page` expose beta static model/page IR vocabulary, including generated CRUD action policy, for inspection and generators.
- `scenery.sh/temporal` exposes workflow/activity declarations and start helpers for the scenery-managed Temporal runtime.
- `scenery.sh/cron` exposes cron job declarations.
- `scenery.sh/pgxpool` wraps `pgxpool` with scenery DB tracing.
- `scenery.sh/et` exposes endpoint/service mocking helpers for tests.

## TypeScript Client Generation

```sh
scenery inspect endpoints --json
scenery inspect wire --json
scenery generate client --lang typescript --output ./src/scenery-client.ts
```

The generated client understands the app's route model and local wire capabilities. The benchmark fixture in [benchmarks/json-wire](benchmarks/json-wire) compares JSON, wire JSON, binary wire, and automatic wire modes.

`WithMeta` methods also expose parsed `txid` metadata from `X-Txid`/`X-TXID`. Electric-backed write flows can use `observeAPIResponseTxid` to report later Electric observation failures as sync/substrate failures after a committed mutation, rather than as API mutation failures.

Apps can also configure `generators.clients` and use `scenery generate client` or `scenery generate --dry-run --json` to inspect and run configured generators. `scenery generate sqlc` is for generated source artifacts; it must not apply database schema or seed data.

The DB lifecycle split uses `scenery db apply` for schema/app database mutation, `scenery db seed` for initial data such as `SERVICE/db/seed.sql`, and `scenery db setup` for apply then seed. Seed files fail closed when previously-applied content changes or destructive SQL is detected.

`scenery up` runs the setup lifecycle before app startup when DB setup inputs exist, using the same managed `DatabaseURL` that the app receives. Rebuilds skip setup until the apply config or seed file hashes change.

`scenery db postgres install|start|status|logs|stop|restart|uninstall --json` manages the shared local Postgres dev cell. `scenery db branch status --json` inspects the worktree branch pin at `.scenery/worktree-db.json`, and `scenery db branch list --json` lists Scenery-owned local branch leases in `branches.json` under `~/.scenery/agent/postgres/` or the `SCENERY_AGENT_HOME` equivalent. Branch status and list distinguish missing, expired, protected parent, and ready local leases. Ready leases may include redacted endpoint metadata but never raw connection URLs, and protected parent leases do not expose endpoint or app-runtime connection metadata.

Postgres branch creation is implemented for `dev.services.postgres.kind: "postgres"`, `mode: "local"`, `isolation: "database"`, and `branch_strategy: "template_database"`. `scenery db branch checkout <name> --json` writes the local pin, ensures the parent template database exists, clones or reuses the branch database from that template, and records a ready endpoint. `reset` recreates the branch from the parent template, `delete` drops the branch database and removes the lease, `expire` updates lease metadata, and `prune` removes expired non-current branch databases when the Postgres admin substrate is reachable. `scenery worktree create <name> --json` creates a Git worktree and writes the target Postgres branch pin, rolling the worktree back if pin creation or branch ensure fails. `scenery up`, `scenery db psql`, DB setup, and Electric consume ready branch endpoints.

The default self-harness includes the live Postgres branch lifecycle proof.

## Managed Toolchain

The root `scenery.toolchain.json` freezes Scenery-owned local tools, images, plugins, and source lock references for this source version. Managed binaries install under `.scenery/toolchain/` by default, while machine-level edge tools install under `~/.scenery/toolchain/`; set `SCENERY_TOOLCHAIN_DIR` to use a controlled cache elsewhere.

```sh
scenery system toolchain list --json
scenery system toolchain sync --json
scenery system toolchain verify --json
```

Caddy edge, Grafana, Victoria sidecars, and the local Temporal CLI are backing substrate for local capabilities. Caddy edge is managed-toolchain only; for the other tools, use documented env overrides, the managed store, `scenery ps --json` substrate records, and the recorded stdout/stderr log paths when intentionally debugging them. They do not silently fall back to system `PATH` binaries.

## Observability And Inspection

scenery exposes local development logs, traces, metrics, and Grafana through app-session capabilities. The current backing substrate can run VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana for richer local inspection.

Useful commands:

```sh
scenery logs --limit 200
scenery logs --follow
scenery console
scenery logs --source api --level error --jsonl --limit 200
scenery inspect routes --json
scenery inspect endpoints --json
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery ps --json
scenery harness --json --write
```

Grafana substrate files are generated under `.scenery/grafana/` when you need to debug them. Shared Temporal and Victoria substrate failures are exposed in `scenery ps --json` as `last_exit` / `component_exits` and emit structured dev log events with component, PID, exit code or signal, and log paths. Set `SCENERY_DEV_GRAFANA=0` to disable Grafana or `SCENERY_DEV_GRAFANA=1` to require it during `scenery up` startup.

## Development

Run the Go test suite:

```sh
go test ./...
```

Rebuild the CLI after changes:

```sh
go install ./cmd/scenery
```

Run the self-harness when making substantial changes:

```sh
scenery harness self --json --write
```

Self-harness Go test steps use the Go test result cache by default; add
`--fresh-tests` when you need a fresh `-count=1` run.

Run the JSON/wire benchmark:

```sh
benchmarks/json-wire/run.sh
```

## Contributing

scenery prefers small, explicit changes and minimal dependencies. When adding behavior, keep the parsed app model as the source of truth and add tests at stable boundaries: parser validation, generated code, runtime HTTP behavior, CLI JSON contracts, and fixture apps.

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and pull request guidance.

Before opening a pull request, run:

```sh
go test ./...
go install ./cmd/scenery
```

For larger changes, also run:

```sh
scenery harness self --json --write
```

## Security

Please do not open public issues for vulnerabilities. Report security issues to security@scenery.sh. See [SECURITY.md](SECURITY.md).

## License

scenery is licensed under the [Apache License 2.0](LICENSE).
