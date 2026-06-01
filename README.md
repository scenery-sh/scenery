# onlava

**One CLI for building, running, and inspecting Go services — built for humans and AI agents.**

onlava is a Go-native local runtime and toolchain for building service applications from ordinary Go packages.

Applications mark their root with `.onlava.json`, declare endpoints with `//onlava:` directives, and run as one local HTTP server. onlava handles service discovery, route registration, auth context, request decoding, generated internal calls, local development supervision, inspection, logs, traces, metrics, MCP, and TypeScript client generation.

onlava is used in production. The stable v0 surface is intentionally small and Go-first; the local dashboard, MCP endpoint, observability, Grafana workbench, local HTTPS routing, Temporal worker tooling, and cron UI are development-focused capabilities. Their backing services and files are substrate details unless you intentionally debug them.

## Why onlava?

- **Go source is the app model.** Services, APIs, auth handlers, middleware, Temporal workflows and activities, and cron jobs are discovered from Go code.
- **One local app server.** `onlava serve` builds once and starts a headless, production-like HTTP server.
- **Full local dev loop.** `onlava dev` runs the app session with file watching, rebuild/restart supervision, dashboard, API explorer, MCP, logs, traces, metrics, Grafana, and optional HTTPS local domains.
- **Typed HTTP by default.** onlava decodes path params, query params, headers, cookies, and JSON bodies into Go structs, then encodes typed responses.
- **Generated internal calls.** Endpoint-to-endpoint calls are rewritten to generated helpers so private access, auth context, and routing semantics are preserved.
- **Inspectable by tools and agents.** `onlava inspect`, `onlava check`, `onlava logs`, and `onlava harness` expose machine-readable JSON contracts.
- **Generated clients.** onlava can generate a TypeScript client with JSON and local wire-format support.

## Status

Available now:

- `.onlava.json` root discovery
- `onlava dev`, `onlava serve`, `onlava run`, `onlava build`, `onlava check`
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
- local HTTPS/frontend proxy with optional trust-store installation
- dashboard, API explorer, and MCP endpoint
- configured generators, SQLC refresh, database sync, and repo task commands
- app-local operational scripts
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

The skill teaches agents the onlava app model, directives, local development workflow, debugging commands, MCP, observability, database inspection, and TypeScript client generation.

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

Use `onlava dev` for the full development platform:

```sh
onlava dev
```

Common options:

```sh
onlava dev --port 4000 --listen 127.0.0.1
onlava dev --json
onlava dev --proxy
onlava dev --proxy --trust
onlava dev --detach
onlava attach
onlava attach --tui
onlava console
```

`--detach` starts an agent-backed dev session in the background and returns after the session is registered. `onlava attach` follows the current session logs from VictoriaLogs. `onlava attach --tui` or `onlava console` opens a source-aware terminal console when attached to a real TTY. `onlava down` stops the current or selected session.

`--proxy` enables local HTTPS/frontend domains from `.onlava.json` proxy config. `--trust` allows onlava to install the local development CA into the OS trust store. Without `--trust`, onlava skips trust-store changes.

Example proxy config:

```json
{
  "name": "myapp",
  "proxy": {
    "workspace": "myteam",
    "api_host": "api.myteam.localhost",
    "console_host": "console.myteam.localhost",
    "mcp_host": "mcp.myteam.localhost",
    "frontends": {
      "web": {
        "host": "web.myteam.localhost",
        "root": "apps/web"
      },
      "blog": {
        "host": "blog.myteam.localhost",
        "root": "apps/blog",
        "upstream": "127.0.0.1:5174"
      }
    }
  }
}
```

## MCP

`onlava dev` exposes MCP as an app-session capability. Use the printed `MCP SSE URL` from the startup banner or the session-scoped MCP route from the local agent manifest.

Current MCP tools cover app metadata, service and endpoint lists, middleware, auth handlers, cron jobs, endpoint calls, source-file reads, referenced environment names, recent traces and spans, discovered PostgreSQL metadata, and SQL queries. MCP is a development convenience surface for agents. Stable automation should use `onlava inspect ... --json`, `onlava logs --jsonl`, schemas, and harness outputs.

See [docs/agent-guide.md](docs/agent-guide.md) for the tool list and usage guidance.

## CLI Overview

```text
onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [-v|--verbose] [--json] [--proxy] [--trust] [--detach]
onlava attach [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [--jsonl|--json] [--tui]
onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]
onlava agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava status --json [--app-root <path>] [--session <id>] [--watch]
onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
onlava prune --older-than <duration> [--app-root <path>] [--json]
onlava serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<script> [script args...]
onlava worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
onlava worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
onlava temporal deployment set-current --build-id <id> [--deployment <name>] [--app-root <path>] [--json]
onlava temporal deployment ramp --build-id <id> --percentage <n> [--deployment <name>] [--app-root <path>] [--json]
onlava temporal deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
onlava version [--json]
onlava toolchain list [--json] [--include-source-locks] [--images]
onlava toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
onlava toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
onlava toolchain path [--json] --tool <name> [--platform <goos/goarch>]
onlava doctor [--app-root <path>] [--json]
onlava build [--app-root <path>] [-o <path>]
onlava check [--app-root <path>] [--json]
onlava generate [--app-root <path>] [--dry-run] [--json]
onlava generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
onlava generate sqlc [--app-root <path>] [--dry-run] [--json]
onlava task list [--app-root <path>] [--json]
onlava task run <name> [--app-root <path>]
onlava task graph --json [--app-root <path>]
onlava run list [--app-root <path>] [--json]
onlava run inspect <domain>:<script> [--app-root <path>] [--lang go|typescript] [--json]
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--json] [--write] [--quick|--race|--release]
onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|traces|metrics --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava admin traces clear --json [--app-root <path>]
onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [-f|--follow] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]
onlava db psql [--app-root <path>] [psql args...]
onlava db sync [--app-root <path>]
onlava db reset [--app-root <path>]
onlava db drop [--app-root <path>]
onlava db snapshot create|restore <name> [--app-root <path>]
onlava psql [--app-root <path>] [psql args...]
```

See [docs/local-contract.md](docs/local-contract.md) for the full command contract and JSON schema list.

## Public Go Packages

- `github.com/pbrazdil/onlava` exposes app metadata and current request metadata.
- `github.com/pbrazdil/onlava/auth` exposes request auth state helpers.
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
onlava gen client --lang typescript --output ./src/onlava-client.ts
```

The generated client understands the app's route model and local wire capabilities. The benchmark fixture in [benchmarks/json-wire](benchmarks/json-wire) compares JSON, wire JSON, binary wire, and automatic wire modes.

Apps can also configure `generators.clients` and use `onlava generate client` or `onlava generate --dry-run --json` to inspect and run configured generators. `onlava generate sqlc` is for file generation; database mutation belongs under `onlava db sync`.

## Managed Toolchain

The root `onlava.toolchain.json` freezes Onlava-owned local tools, images, plugins, and source lock references for this source version. Managed binaries install under `.onlava/toolchain/` by default; set `ONLAVA_TOOLCHAIN_DIR` to use a controlled cache elsewhere.

```sh
onlava toolchain list --json
onlava toolchain sync --json
onlava toolchain verify --json
```

Grafana, Victoria sidecars, and the local Temporal CLI are backing substrate for local capabilities. When intentionally debugging them, use explicit env overrides or the managed store; they do not silently fall back to system `PATH` binaries.

## Observability And Inspection

onlava exposes local development logs, traces, metrics, and Grafana through app-session capabilities. The current backing substrate can run VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana for richer local inspection.

Useful commands:

```sh
onlava logs --session current --limit 200
onlava attach
onlava attach --tui
onlava logs --session current --source api --level error --jsonl --limit 200
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect traces --json --session current --since 15m --slowest
onlava inspect metrics --json --session current --since 1h
onlava harness --json --write
```

Grafana substrate files are generated under `.onlava/grafana/` when you need to debug them. Set `ONLAVA_DEV_GRAFANA=0` to disable Grafana or `ONLAVA_DEV_GRAFANA=1` to require it during `onlava dev` startup.

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
