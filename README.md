# onlava

**One CLI for building, running, and inspecting Go services — built for humans and AI agents.**

onlava is a Go-native local runtime and toolchain for building service applications from ordinary Go packages.

Applications mark their root with `.onlava.json`, declare endpoints with `//onlava:` directives, and run as one local HTTP server. onlava handles service discovery, route registration, auth context, request decoding, generated internal calls, local development supervision, inspection, logs, traces, and TypeScript client generation.

onlava is used in production. The stable v0 surface is intentionally small and Go-first; the local dashboard, DB Studio, MCP endpoint, Victoria observability sidecars, Grafana workbench, local HTTPS proxy, Temporal worker tooling, and cron UI are development-focused companion tools.

## Why onlava?

- **Go source is the app model.** Services, APIs, auth handlers, middleware, Temporal workflows and activities, and cron jobs are discovered from Go code.
- **One local app server.** `onlava run` builds once and starts a headless, production-like HTTP server.
- **Full local dev loop.** `onlava dev` adds file watching, rebuild/restart supervision, dashboard, API explorer, DB Studio, logs, traces, metrics, Grafana, and optional HTTPS local domains.
- **Typed HTTP by default.** onlava decodes path params, query params, headers, cookies, and JSON bodies into Go structs, then encodes typed responses.
- **Generated internal calls.** Endpoint-to-endpoint calls are rewritten to generated helpers so private access, auth context, and routing semantics are preserved.
- **Inspectable by tools and agents.** `onlava inspect`, `onlava check`, `onlava logs`, and `onlava harness` expose machine-readable JSON contracts.
- **Generated clients.** onlava can generate a TypeScript client with JSON and local wire-format support.

## Status

Available now:

- `.onlava.json` root discovery
- `onlava dev`, `onlava run`, `onlava build`, `onlava check`
- typed and raw HTTP endpoints
- public, auth, and private endpoints
- auth handlers and request auth helpers
- service struct initialization
- middleware
- private/internal endpoint calls
- secrets from environment and local `.env`
- local logs, traces, and metrics inspection
- local Grafana provisioning over Victoria observability sidecars
- Temporal workflow/activity and cron local runtime support
- local HTTPS/frontend proxy with optional trust-store installation
- dashboard, API explorer, MCP endpoint, and DB Studio integration
- TypeScript client generation
- JSON/wire benchmark fixture

Stable v0 API details live in [docs/local-contract.md](docs/local-contract.md). Architecture notes live in [ARCHITECTURE.md](ARCHITECTURE.md).

## Requirements

- Go 1.26+
- Bun, only when working on the dashboard UI, DB Studio UI, or the benchmark fixture
- `psql`, only when using `onlava psql`

## Install From Source

```sh
git clone https://github.com/pbrazdil/onlava.git onlava
cd onlava
go install ./cmd/onlava
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

The skill teaches agents the onlava app model, directives, local development workflow, debugging commands, observability, `psql`, and TypeScript client generation.

## A Minimal App

Create `.onlava.json`:

```json
{"name":"hello"}
```

Create `go.mod`:

```go
module example.com/hello

go 1.26.0

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
onlava run
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
```

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

## CLI Overview

```text
onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [-v|--verbose] [--json] [--proxy] [--trust]
onlava run [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava version [--json]
onlava build [--app-root <path>] [-o <path>] [--db-studio]
onlava check [--app-root <path>] [--json]
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--json] [--write]
onlava inspect app|routes|services|endpoints|wire|build|paths|traces|metrics --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [-f|--follow] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]
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
onlava gen client --lang typescript --output ./src/onlava-client.ts
```

The generated client understands the app's route model and local wire capabilities. The benchmark fixture in [benchmarks/json-wire](benchmarks/json-wire) compares JSON, wire JSON, binary wire, and automatic wire modes.

## Observability And Inspection

onlava writes local development logs and traces, and `onlava dev` can run VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana for richer local inspection.

Useful commands:

```sh
onlava logs --limit 200
onlava logs --jsonl --limit 200
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect traces --json --since 15m --slowest
onlava inspect metrics --json --since 1h
onlava harness --json --write
```

Grafana files are generated under `.onlava/grafana/`. Set `ONLAVA_DEV_GRAFANA=0` to disable it or `ONLAVA_DEV_GRAFANA=1` to require it during `onlava dev` startup.

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
