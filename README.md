# scenery

**One CLI for building, running, and inspecting Go services — built for humans and AI agents.**

Scenery applications use language edition 2027: `scenery.scn` and package-local `scenery.package.scn` files compile into one canonical typed resource graph with Go and TypeScript generation, HTTP/durable/event/data/deployment/UI profiles, semantic inspection, and revision-bound mutation.

Edition 2027 is a feature-complete `0.5-draft` across its claimed resolved profiles, not a stable language release. Surfaces whose semantics are still open—such as declarative extensions, Appendix E workflows, streaming/WebSockets, full registry trust, entity-evolution syntax, platform listener/certificate schemas, and fixed-target native toolchain identities—fail as draft or unsupported instead of receiving invented defaults.

scenery is a Go-native local runtime and toolchain for building service applications from ordinary Go packages.

Applications mark their root with `.scenery.json`, declare their application graph in `.scn`, and implement generated native contracts in Go. scenery handles graph compilation, route registration, auth context, request decoding, generated internal calls, local development supervision, inspection, logs, traces, metrics, and TypeScript client generation.

The Go-comment declaration frontend is not supported. The local dashboard, native observability, local HTTPS routing, and durable worker tooling are development-focused capabilities; their backing services and files are substrate details unless you intentionally debug them.

## Why scenery?

- **One canonical app model.** Services, operations, bindings, auth, middleware, durable executions, schedules, data, and UI resources are declared in edition-2027 `.scn` source.
- **Full local dev loop.** `scenery up` runs the app root's one live dev runtime with file watching, rebuild/restart supervision, dashboard, API explorer, logs, traces, metrics, and optional HTTPS local domains.
- **Typed HTTP by default.** scenery decodes path params, query params, headers, cookies, and JSON bodies into Go structs, then encodes typed responses.
- **Generated internal calls.** Binding clients preserve private access, auth context, tracing, and delivery semantics.
- **Inspectable by tools and agents.** `scenery inspect`, `scenery check`, `scenery logs`, `scenery harness`, and `scenery validate` expose machine-readable JSON contracts.
- **Generated clients.** scenery can generate a TypeScript client for typed routes.

## Status

Available now as draft profile surfaces:

- edition-2027 lossless source, truthful source/effective/expanded graphs with path-indexed provenance, stable revisions, recursive authoring schemas with wire-label policies, schema-driven creation for exactly advertised resource kinds, receipt-proven semantic rename, dependency graph, and normalized agent mutation protocol
- edition-2027 Go contract/application/composition generation and exact TypeScript clients with descriptors, constraints, cross-field validation, canonical HTTP sets, Fetch-safe header validation, declared multipart, and structurally disjoint typed response-map coverage
- edition-2027 exact Go build-input/toolchain identities and runtime-bundle sidecars, with host-CGO native-tool identities and fail-closed fixed-target CGO
- edition-2027 authored CLI execution with generated help/completion and typed outcomes, plus environment-selected typed fixtures shared by deployment and local database seeding
- edition-2027 HTTP, typed terminal zero-or-more path tails, durable execution, schedules, events, data/CRUD/provider, deployment plan/apply, patches, UI validation, and compatibility profiles
- workspace-issued, revision-bound semantic change and deployment transactions; apply rejects caller-recomputed plans before trusting approvals, edits, or provider actions
- `.scenery.json` root discovery
- `scenery up`, `scenery task`, `scenery validate`, `scenery build`, `scenery check`
- typed HTTP bindings
- public, auth, and private endpoints
- authentication resources and request auth helpers
- generated typed service constructors
- middleware
- private/internal endpoint calls
- secrets from environment and local `.env`
- local logs, traces, and metrics inspection
- native local observability
- durable execution and schedule runtime support
- local HTTPS edge and frontend routing with optional trust-store installation
- beta public deploy edge for serving a live local app on your own domain
- dashboard and API explorer
- configured generators, SQLC refresh, database lifecycle commands, and repo task commands
- app-local code tasks
- TypeScript client generation
- benchmark fixture

Exact CLI and artifact details live in [docs/local-contract.md](docs/local-contract.md). The normative edition-2027 design set begins at [docs/specs/vnext/SCENERY_LANGUAGE_SPEC.md](docs/specs/vnext/SCENERY_LANGUAGE_SPEC.md). Agent workflows live in [docs/agent-guide.md](docs/agent-guide.md). Architecture notes live in [ARCHITECTURE.md](ARCHITECTURE.md).

## Requirements

- Go 1.26+
- Bun, only when working on the dashboard UI or the benchmark fixture

Run `scenery doctor -o json` after install when you want a read-only readiness report for the host, Go toolchain, disk/memory resources, Docker engine reachability, and optional local-development dependencies.

## Install From Source

```sh
git clone https://github.com/scenery-sh/scenery.git scenery
cd scenery
go install ./cmd/scenery
scenery doctor -o json
scenery version -o json
```

The module path is `scenery.sh`. Source installs are useful when working from a checkout or testing unreleased changes.
Release binaries embed the built dashboard UI from `apps/consolenext/` and do not build it at runtime. From source, run `./scripts/build-dashboard-ui-embed.sh` before `go install ./cmd/scenery` when the installed binary should carry the current dashboard build.

## Prebuilt CLI Binaries

Tagged releases currently publish a prebuilt `scenery` archive for macOS Apple Silicon (`darwin/arm64`) on the [GitHub Releases](https://github.com/scenery-sh/scenery/releases) page.

After installing a prebuilt binary, verify it with:

```sh
scenery version -o json
```

To update a local prebuilt install later:

```sh
scenery upgrade
```

`scenery upgrade` verifies the selected release archive against `checksums.txt`, replaces the current local binary, and then syncs managed toolchain entries already present in the local store. Use `scenery upgrade --toolchain all` when you intentionally want every frozen tool and image from the upgraded binary pulled immediately.

## Public Deploy Edge

`scenery deploy` is a beta operator surface for serving a live local app on a public domain from a macOS machine. Add a public domain to the app config:

```json
{"name":"hello","deploy":{"domain":"hello.example.com","root":"app"}}
```

Then configure the machine once, enable the app, and keep a live dev runtime running:

```sh
scenery deploy setup --acme-ca staging --acme-email ops@example.com
scenery deploy enable --app-root /path/to/app
scenery up --detach --app-root /path/to/app
scenery deploy status -o json
```

Point DNS A/AAAA records at the reported public IP and forward router TCP 80/443 to the reported LAN IP. `scenery deploy status -o json` reports listener, DNS, reachability, sleep, firewall, and certificate diagnostics. Switch to `--acme-ca production` after staging works.

## Agent Skill

scenery includes an installable agent skill for using scenery apps:

```sh
npx skills add https://github.com/scenery-sh/scenery
```

The skill teaches agents the edition-2027 app model, local development workflow, debugging commands, observability, database inspection, and generated-client workflow.

The skill is shared runtime knowledge. Client apps should still keep a small app-local `AGENTS.md` for app root, frontend roots, generated client output paths, required environment names, validation commands, and product invariants. Do not copy the whole skill into every app; keep shared scenery behavior in `SKILL.md` and app-specific facts in the client repository.

## A Minimal App

Create `.scenery.json` and `go.mod`:

```json
{"name":"hello"}
```

```go
module example.com/hello

go 1.26.3

require scenery.sh v0.0.0

replace scenery.sh => /path/to/scenery
```

Create `scenery.scn`:

```hcl
language {
  edition = "2027"
  require_profiles = [
    "scenery.compiler-core/v1",
    "scenery.go-implementation/v1",
    "scenery.runtime-http/v1",
  ]
}

workspace {
  implementation_root "application" {
    path = "."
    revision_include = ["**/*.go", "go.mod"]
  }
  managed_generated_roots = ["service/scenerycontract", "internal/scenerygen"]
}

go_module "application" {
  root        = "."
  import_path = "example.com/hello"
}
go_toolchain "application" {
  version     = "1.26.3"
  experiments = []
}
go_target "development" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
  cgo       = "disabled"
}
application "hello" { version = "1.0.0" }
http_gateway "public" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
module "service" {
  source = "./service"
  inputs = { gateway = http_gateway.public }
}
```

Create `service/scenery.package.scn` with one service, operation, execution, and HTTP binding:

```hcl
package "service" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract { import_path = "example.com/hello/service" }
}
input "gateway" { type = resource_ref("http_gateway") }
service "service" {
  runtime = "go"
  implementation { constructor = "NewService" }
}
record "hello_input" { field "name" { type = string } }
record "hello_result" { field "message" { type = string } }
operation "hello" {
  service = service.service
  input   = record.hello_input
  handler { method = "Hello" }
  result "ok" { type = record.hello_result }
}
execution "hello_direct" {
  operation = operation.hello
  mode      = "direct"
}
binding "hello_http" {
  gateway   = var.gateway
  operation = operation.hello
  execution = execution.hello_direct
  protocol  = "http"
  delivery  = "call"
  authentication = std.authentication.none
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  http {
    method        = "POST"
    path          = "/hello"
    codec_profile = std.codec.http_json_v1
    body { codec = "json", to = operation.hello.input }
    response "ok" {
      when   = result.ok
      status = 200
      body { codec = "json", from = result.ok }
    }
  }
}
```

Implement the generated contract in `service/api.go`:

```go
package service

import (
	"context"
	contract "example.com/hello/service/scenerycontract"
)

type Service struct{}

func NewService(context.Context, contract.ServiceConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Hello(_ context.Context, input contract.HelloInput) (contract.HelloOutcome, error) {
	return contract.HelloOk{Value: contract.HelloResult{Message: "hello " + input.Name}}, nil
}
```

Generate, check, and run it:

```sh
scenery generate --target go -o json
scenery check -o json
scenery up --detach
```

Call it (use `scenery ps -o json` to discover the base URL):

```sh
curl -H 'Content-Type: application/json' -d '{"name":"world"}' http://localhost:4001/api/hello
```

The checked-in native fixture at `testdata/apps/basic` is the compact runnable reference. `.scenery.json` configures the runtime; it does not declare application resources.

## Local Development

Use `scenery up` for the full development platform:

```sh
scenery up
```

Common options:

```sh
scenery up --port 4000 --listen 127.0.0.1
scenery up -o jsonl
scenery up --detach
scenery system edge dns install
scenery system edge privileged install
scenery system edge install
scenery system edge trust
scenery logs --follow
scenery console
```

`--detach` starts the app root's agent-backed dev runtime in the background and, by default, returns after the API and configured frontends are ready; use `--wait registered` for the faster registration-only path. `scenery logs --follow` follows that app root's logs from VictoriaLogs. `scenery console` opens a source-aware terminal console when attached to a real TTY. `scenery down` stops the app root's one live runtime; for shared storage cells, it releases only that runtime's lease and preserves shared data. Use Git worktrees when you need multiple live code copies.

`scenery up` defaults to path routing: one live app root gets one browser-facing base URL such as `http://localhost:4001`, and services live under paths such as `/api/`, `/consolenext/`, `/web/`, `/blog/`, and `/runtime/`. `scenery ps` and `scenery ps -o json` report the base URL plus service routes. `dev.routing.port`, `dev.routing.port_start`, and `dev.routing.port_end` may pin or constrain the assigned localhost port; otherwise Scenery chooses a stable free port for the app root/session. Set `dev.routing.mode` to `host` only when you intentionally want the default `local.dev` edge/DNS route path.

In host mode, generated routes use the local edge/DNS path under `local.dev`. Use `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when you want trusted wildcard local HTTPS routes on the default HTTPS port; edge syncs managed dnsmasq and Caddy when needed and keeps Caddy user-owned.

Example frontend config:

```json
{
  "name": "myapp",
  "dev": {
    "routing": {
      "mode": "path"
    }
  },
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
```

## CLI Overview

```text
scenery up [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [--verbose] [-o jsonl] [--detach]
scenery logs --follow [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [-o jsonl|-o json]
scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>]
scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [-o json]
scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [-o json]
scenery system edge install|trust|status|restart|uninstall|dns|privileged [-o json]
scenery help <command>|all|-o json
scenery ps [-o json] [--app-root <path>] [--watch]
scenery down [--app-root <path>] [--db] [--state] [--all] [-o json]
scenery prune --older-than <duration> [--app-root <path>] [-o json]
scenery worker [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker durable --endpoint <url> --token <token> [--service <name>]... [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker durable jobs list|inspect|cancel|retry [job-id] --service <name> [--app-root <path>] -o json
scenery worker durable token create --service <name> [--name <name>] [--id <id>] [--app-root <path>] -o json
scenery version [-o json]
scenery upgrade [--version latest|vX.Y.Z] [--target <path>] [--toolchain installed|all|none] [--force] [--dry-run] [-o json]
scenery deploy enable|disable|status|setup|resume|teardown [-o json]
scenery system toolchain list [-o json] [--include-source-locks] [--images]
scenery system toolchain sync [-o json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain verify [-o json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
scenery system toolchain path [-o json] --tool <name> [--platform <goos/goarch>]
scenery doctor [--app-root <path>] [-o json]
scenery build [--app-root <path>] [--output <path>] [-o human|json]
scenery fmt --check [--app-root <path>] -o json
scenery check [--app-root <path>] -o json
scenery compile [--app-root <path>] [--view source|effective|expanded] -o json
scenery list|get|explain|graph ... [--app-root <path>] -o json
scenery diff --semantic BASE TARGET [--rename-receipts <path>] -o json
scenery generate [--app-root <path>] [--target go|contracts|typescript_client.<name>] [--check] -o json
scenery changes plan|apply ... -o json
scenery generate sqlc [--app-root <path>] [--dry-run] [-o json]
scenery task list [--app-root <path>] [-o json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [-o json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery task graph -o json [--app-root <path>]
scenery validate [<profile>] [--app-root <path>] [-o json] [--write] [--dry-run]
scenery validate changed [--base <ref>] [--app-root <path>] [-o json] [--write] [--dry-run]
scenery harness [--app-root <path>] [-o json] [--write] [--with-validation[=<profile>]]
scenery harness self [--repo-root <path>] [-o json] [--write] [--quick|--race|--release] [--fresh-tests]
scenery harness ui -o json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
scenery inspect app|routes|services|endpoints|build|paths|durable -o json [--app-root <path>]
scenery inspect docs -o json [--repo-root <path>]
scenery traces list -o json [--app-root <path>]
scenery metrics list -o json [--app-root <path>]
scenery traces clear -o json [--app-root <path>]
scenery logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--follow] [-o jsonl|-o json]
scenery test [--app-root <path>] [go test flags/packages...]
scenery db list [--app-root <path>] [-o json]
scenery db shell [--app-root <path>] [--service <name>] [psql args...]
scenery db apply [--app-root <path>] [-o json]
scenery db seed [--app-root <path>] [--env <name>] [--dry-run] [-o json]
scenery db setup [--app-root <path>] [-o json]
scenery db reset [--app-root <path>] [--service <name>] [--yes]
scenery db drop [--app-root <path>] [--service <name>] [--yes]
scenery db server status|start|stop|logs [-o json] [--yes]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [-o json]
scenery worktree list [--app-root <path>] [-o json]
scenery worktree remove <name> [--app-root <path>] [--db] [-o json]
```

`scenery db list -o json` reports the app's Postgres database and service schemas.
An explicit app-level `DATABASE_URL` wins and makes the database external;
otherwise `scenery up` creates one isolated database per app root/worktree on the
shared local Postgres dev server, with one schema per configured service plus
the `scenery` schema for framework state.

See [docs/local-contract.md](docs/local-contract.md) for the full command contract and JSON schema list.

## Public Go Packages

- `scenery.sh` exposes app metadata and current request metadata.
- `scenery.sh/auth` exposes request auth state helpers.
- Standard auth owns its tenant tables under the app database's `scenery` schema; app-local `tenants` services or tables are product-domain concerns. Google connections store encrypted refresh tokens for app-owned Google API calls through `auth.GoogleAccessToken`.
- `scenery.sh/errs` exposes coded errors and HTTP status mapping.
- `scenery.sh/durable` exposes non-registering durable runtime helpers such as steps and signals; task, execution, and schedule ownership is declared in `.scn`.
- `scenery.sh/db` exposes Postgres `*sql.DB` pools pinned to a service schema for app code and sqlc.
- `scenery.sh/datasource` and `scenery.sh/object` expose typed constructor capabilities.

## TypeScript Client Generation

Declare each TypeScript target in `scenery.scn`, keep its `output_root` beneath a declared managed generated root, then run:

```sh
scenery inspect endpoints -o json
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
```

The generated client implements the declared gateway/binding contract.

`WithMeta` methods expose response headers, status, and the raw `Response` alongside decoded data.

`scenery generate sqlc` remains the configured SQLC source-artifact command; it must not apply database schema or seed data.

The DB lifecycle split uses `scenery db apply` for schema/app database mutation, `scenery db seed` for initial data such as `SERVICE/db/seed.sql`, and `scenery db setup` for apply then seed. Seed files apply to their matching service schema and fail closed when previously-applied content changes or destructive SQL is detected.

`scenery up` runs the setup lifecycle before app startup when DB setup inputs exist, using the same managed service database env values that the app receives. Rebuilds skip setup until the apply config or seed file hashes change.

Worktree database isolation is automatic: the managed database name includes a
hash of the app root, so `scenery worktree create <name> -o json` only creates the
Git worktree. `scenery db reset <service>` drops and recreates only that service
schema. Portable database and storage save/load is tracked by active plan 0100.

The default self-harness includes a Docker-gated Postgres probe for the shared
server, one app database, service schemas, durable state, auth bootstrap,
worktree isolation, and service-schema reset.

## Managed Toolchain

The root `scenery.toolchain.json` freezes Scenery-owned local tools, images, plugins, and source lock references for this source version. Managed binaries install under `.scenery/toolchain/` by default, while machine-level edge tools install under `~/.scenery/toolchain/`; set `SCENERY_TOOLCHAIN_DIR` to use a controlled cache elsewhere.

```sh
scenery system toolchain list -o json
scenery system toolchain sync -o json
scenery system toolchain verify -o json
```

`scenery upgrade` uses the upgraded binary's bundled manifest for the post-upgrade toolchain sync, so pinned versions change with the Scenery release instead of ambient system tools.

Caddy edge and Victoria sidecars are backing substrate for local capabilities; Caddy edge is managed-toolchain only. Storage is not a managed substrate: declaring `storage.stores` makes `scenery up` serve them from a Scenery-owned local directory tree (atomic writes, checked fsync, sidecar metadata) with no managed process, toolchain artifact, or dev-service entry. Offsite durability is an operator concern — replicate the storage-cell object directories to S3 with `rclone`/`restic` (see `docs/app-development-cookbook.md`). For the managed tools, use documented env overrides, the managed store, `scenery ps -o json` substrate records, and the recorded stdout/stderr log paths when intentionally debugging them. They do not silently fall back to system `PATH` binaries.

## Observability And Inspection

scenery exposes local development logs, traces, and metrics through app-session capabilities. The current backing substrate can run VictoriaMetrics, VictoriaLogs, and VictoriaTraces for local inspection.

Useful commands:

```sh
scenery logs --limit 200
scenery logs --follow
scenery console
scenery logs --source api --level error -o jsonl --limit 200
scenery inspect routes -o json
scenery inspect endpoints -o json
scenery traces list -o json --since 15m --slowest
scenery metrics list -o json --since 1h
scenery ps -o json
scenery harness -o json --write
```

Victoria substrate failures are exposed in `scenery ps -o json` as `last_exit` / `component_exits` and emit structured dev log events with component, PID, exit code or signal, and log paths. Dead registered runtime children such as managed frontend processes appear as session `degraded` status with `status_reason`; managed Vite/Astro frontends are restarted by `scenery up` when their dev-server process exits unexpectedly.

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
scenery harness self -o json --write
```

Self-harness Go test steps use the Go test result cache by default; add
`--fresh-tests` only for explicit fresh measurement or nondeterminism
investigation. That lane uses the locally measured package parallelism `-p 3`.
Timing reports distinguish cached, fresh, and release budgets; only fresh runs
confirm package/test hotspots in isolation.

## Contributing

scenery prefers small, explicit changes and minimal dependencies. When adding behavior, keep the canonical edition-2027 graph as the source of truth and add tests at stable boundaries: `.scn` validation, generated code, runtime HTTP behavior, CLI JSON contracts, and fixture apps.

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and pull request guidance.

Before opening a pull request, run:

```sh
go test ./...
go install ./cmd/scenery
```

For larger changes, also run:

```sh
scenery harness self -o json --write
```

## Security

Please do not open public issues for vulnerabilities. Report security issues to security@scenery.sh. See [SECURITY.md](SECURITY.md).

## License

scenery is licensed under the [Apache License 2.0](LICENSE).
