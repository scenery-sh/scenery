# Scenery App Development Cookbook

Practical recipes for current Scenery applications. The normative language contract lives under `docs/spec/`; exact CLI and artifact contracts live in `docs/local-contract.md`.

## Start A Native App

Create `.scenery.json` for runtime config, `scenery.scn` for the root graph, and one `scenery.package.scn` for each local module. The checked-in `testdata/apps/basic` app is the smallest runnable reference.

At minimum, root source declares:

- workspace implementation and managed generated roots;
- Go module, toolchain, and target;
- application identity;
- an HTTP gateway;
- local module installation and inputs.

Package source declares package identity, typed inputs, a service constructor, records, operations, executions, and bindings. Use this loop:

```sh
scenery fmt --check -o json
scenery compile --view expanded -o json
scenery generate --target go -o json
scenery check -o json
go test ./...
```

Go source implements generated contracts; it does not declare application resources. A service looks like:

```go
package service

import (
	"context"
	contract "example.com/app/service/scenerycontract"
)

type Service struct{}

func NewService(context.Context, contract.ServiceConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Hello(_ context.Context, input contract.HelloInput) (contract.HelloOutcome, error) {
	return contract.HelloOk{Value: contract.HelloResult{Message: "hello " + input.Name}}, nil
}
```

## Declare A Typed HTTP Operation

Define wire types, operation behavior, execution policy, and HTTP transport separately:

```hcl
record "hello_input" {
  field "name" { type = string }
}

record "hello_result" {
  field "message" { type = string }
}

operation "hello" {
  service = service.service
  input   = record.hello_input
  handler { method = "Hello" }
  result "ok" { type = record.hello_result }
}

execution "hello_direct" {
  operation = operation.hello
  mode      = "direct"
  timeout   = "30s"
}

binding "hello_http" {
  gateway   = var.gateway
  operation = operation.hello
  execution = execution.hello_direct
  protocol  = "http"
  delivery  = "call"

  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

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

Map path, query, header, cookie, and body inputs explicitly in the `http` block. Map each business outcome to an exact status/body/header/cookie representation. Ambiguous same-status outcomes fail compilation instead of relying on declaration order.

For a no-input operation, use `std.type.unit`; do not invent an empty request struct. For a terminal zero-or-more tail, use final `{name...}` and declare one matching `path_tail` mapping.

## Constructor Capabilities And Config

Declare typed package inputs and reference them through lower-snake service config keys:

```hcl
input "roof_model_path" {
  type  = relative_path
  phase = "deployment"
}

service "house" {
  runtime = "go"
  implementation { constructor = "NewService" }
  config { model_path = var.roof_model_path }
}
```

Generated constructor input carries typed config plus declared `datasource` and `object` capabilities. The package input owns phase, constraints, sensitivity, and provenance. Do not pass plaintext sensitive values.

## Internal Calls

Declare an internal binding and a service client:

```hcl
service "house" {
  runtime = "go"
  implementation { constructor = "NewService" }
  client "billing" { binding = billing.binding.quote_internal }
}

binding "quote_internal" {
  operation = operation.quote
  execution = execution.quote_direct
  protocol  = "internal"
  delivery  = "call"
  exposure  = "application"
  authentication = std.authentication.inherit
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  internal {
    visibility = "application"
    principal  = "inherit"
  }
}
```

Use the generated constructor client. It preserves visibility, auth context, tracing, typed cloning, outcomes, and delivery semantics.

## Authentication And Authorization

Bindings reference explicit authentication and authorization resources or standard policies. Public endpoints use `std.authentication.none` plus `std.authorization.public`. Protected bindings use the configured standard-auth provider and generated auth context; app code reads request identity through `scenery.sh/auth`.

Standard auth is enabled in app config because it is a runtime capability. Its framework tables live in the app database's `scenery` schema. Google connections expose app-owned access tokens through `auth.GoogleAccessToken`; do not store third-party refresh tokens in product tables.

## Durable Work, Schedules, And Events

Declare durable executions, schedules, event contracts, consumers, and emissions in package `.scn`. Use `external_name` when a durable identity must remain stable. If persisted input changes incompatibly, increment `revision` and drain or migrate active rows first.

`scenery.sh/durable` provides runtime steps and signals but does not register tasks. Start worker-role execution with:

```sh
scenery worker --app-root <path> --env development
scenery inspect durable -o json
```

## Data, CRUD, Fixtures, Pages, And Renderers

Use current specification resources for data sources, records, entities, views, CRUD expansion, fixtures, pages, and renderers. Expanded graph inspection shows generated services, operations, bindings, and artifacts:

```sh
scenery compile --view expanded -o json
scenery list entity -o json
scenery list page -o json
scenery explain <address> <pointer> -o json
```

Typed fixtures are selected by environment and shared by deployment planning and local seed generation:

```sh
scenery db seed --env development --dry-run -o json
scenery db seed --env development -o json
```

For a filtered table page, make the list API surface explicit and keep the UI declaration thin:

```hcl
crud "orders" {
  entity = entity.order
  list {
    filters       = ["status", "created_at"]
    sorts         = ["created_at"]
    default_sort  = { field = "created_at", direction = "desc" }
    max_page_size = 100
  }
}

table_page "orders" {
  path = "/orders"
  source = crud.orders
  title = "Orders"
  column "number" {}
  column "status" { appearance = "badge" }
  filter "status" {}
  filter "created_at" {}
  sort "created_at" { default = "desc" }
  row_link = "/orders/{id}"
}
```

The CRUD runtime applies enum-array and datetime-range filters, query-bound keyset cursors, stable primary-key tie-breaking, and server-side limit clamping. Add declared `react_component` overrides only where the catalog defaults are insufficient.

For a two-pane page, keep the declaration generic and the domain UI app-owned:

```hcl
split_page "inbox" {
  path   = "/inbox"
  source = binding.inbox_http
  title  = "Inbox"

  sidebar        { component = react_component.inbox_list }
  sidebar_actions { component = react_component.inbox_actions }
  detail_header  { component = react_component.inbox_header }
  detail         { component = react_component.inbox_detail }
}
```

The source operation has unit input, exactly one result, and both HTTP and inherited internal bindings. Its slot modules receive raw loading/error/result state plus URL-backed selection state. Each slot should use `QueryState` from `@scenery/ui` to render those branches consistently. Scenery supplies the reusable split layout; it contains no inbox-specific component.

For a centered one-column page, reuse that operation/binding shape and declare only the page shell slots:

```hcl
content_page "summary" {
  path      = "/summary"
  source    = binding.summary_http
  title     = "Summary"
  max_width = 960

  actions { component = react_component.summary_actions }
  content { component = react_component.summary_content }
}
```

The generated adapter renders catalog `Page`, puts `actions` in its header, and passes the same typed raw request state to both slots. Use `queryStateProps(state, "summary")` with `QueryState` in the content component instead of inventing another loading/error union.

For a CRUD collection, keep the higher-level `table_page` declaration. Its generated adapter uses the same `Page` shell and renders the chrome-less catalog `QueryTable` as content. Declared `toolbar` becomes the page action slot; cell, filter, and empty-state components remain app-owned typed slots. The built-in grid, enum and datetime filters, sorting, pagination, loading, empty, and error states use Astryx components and tokens, so customize the app theme through Astryx rather than catalog-specific CSS variables.

## Generate A TypeScript Client

Declare a root target:

```hcl
typescript_client "public_api" {
  gateways    = [http_gateway.public_api]
  package     = "@example/app-client"
  module      = "esm"
  runtime     = "fetch"
  output_root = "clients/generated/public_api"
  react {
    tsconfig = "apps/web/tsconfig.json"
  }
}
```

Add the output root to `workspace.managed_generated_roots`, then run:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
```

Commit the generated descriptor and source files. Regenerate after reachable type, binding, codec, gateway, auth, or outcome changes. Generated clients never infer behavior from Go symbols.

With `react`, the same transaction owns `react/<table>.generated.tsx`, `react/pages.generated.ts`, and `react/scenery-ui/`. The app mounts the neutral `generatedPages` array in its router. Install frontend dependencies before generation; `scenery doctor -o json` reports the declared tsconfig, `node_modules`, and managed checker readiness.

Mount generated pages beneath the app's TanStack `QueryClientProvider`. Content,
split, and table pages use stable page-address query keys, so the app's cache,
deduplication, retry, and invalidation defaults apply automatically. Generated
pages do not mark arbitrary API results for persistent storage; enable persistence
only through an explicit app policy that is appropriate for the data.

## Semantic Changes And Compatibility

Use canonical graph operations rather than editing generated files:

```sh
scenery list service -o json
scenery schema scenery.service -o json
scenery changes plan ... -o json
scenery changes apply <plan> ... -o json
scenery diff --semantic <base> <target> -o json
```

Plan/apply is revision-bound, single-use, and tied to the exact issued plan. Inspect required approvals and risk records before apply. Use rename receipts for semantic identity changes; do not approximate a rename as unrelated delete/create when continuity matters.

## App-Local Code Tasks

Place single-file Go tasks beneath `<domain>/tasks/` with `//go:build ignore`, or use a task directory. That build constraint keeps the file out of app packages; it is not an application declaration.

```sh
scenery task list -o json
scenery task inspect billing:reconcile -o json
scenery task run billing:reconcile -- --limit 100
```

## Database Lifecycle

Keep database mutation separate from generation:

```sh
scenery db list -o json
scenery db apply -o json
scenery db seed --env development --dry-run -o json
scenery db setup -o json
```

`db apply` runs configured schema/app setup. `db seed` applies service-local seed files and typed fixture plans. Previously applied content changes and destructive seed SQL fail closed. `db setup` runs apply then seed. `scenery generate sqlc` refreshes source only.

Save or restore a portable point-in-time copy of the database and storage cell explicitly:

```sh
scenery snapshot save --db --storage --output app.zip -o json
scenery snapshot verify --input app.zip -o json
scenery down
scenery snapshot load --db --storage --input app.zip --mode overwrite --yes -o json
```

The archive is checksummed before load. Use `--mode merge` only when an atomic data-only database insert and storage conflict policy are intended; use `--dry-run` to preflight. Snapshots are operator-created restore points, not continuous offsite replication.

### Scheduled Off-Machine Backups

Run the repository's backup runner from the host scheduler during a quiet write window. It serializes runs per output directory, recovers a stale lock after an interrupted job, validates every archive checksum, copies with rclone only after validation, and prunes local history only after all earlier steps succeed:

```sh
/path/to/scenery/scripts/snapshot-backup.sh \
  --app-root /srv/my-app \
  --output-dir /var/backups/my-app \
  --keep 14 \
  --copy-to s3:company-backups/my-app
```

Configure rclone credentials outside the app and apply a remote bucket lifecycle policy for remote retention. The script deliberately installs no Scenery schedule. On macOS, create a user launch agent whose `ProgramArguments` are the command and arguments above and whose `StartCalendarInterval` contains `Hour = 3`; on Linux use a systemd timer, or add the same command to cron. Keep stdout/stderr in an operator-owned log and alert on nonzero exit.

At least monthly, restore the newest replicated archive into a disposable worktree with a separate `SCENERY_AGENT_HOME`, then run the app's data assertions as well as route readiness:

```sh
scenery snapshot verify --input /tmp/replicated-latest.zip -o json
SCENERY_AGENT_HOME=/tmp/scenery-restore-drill-agent scenery snapshot load \
  --app-root /tmp/my-app-restore-drill --input /tmp/replicated-latest.zip \
  --db --storage --mode overwrite --yes -o json
SCENERY_AGENT_HOME=/tmp/scenery-restore-drill-agent scenery up \
  --detach --wait ready --app-root /tmp/my-app-restore-drill
```

Check representative database rows and stored objects through the app, then run `scenery down --all` for the drill root and remove only the disposable worktree and drill agent home. A checksum-only verify is not a restore drill.

## Storage

Declare storage cells/stores in app config. App code uses `scenery.sh/storage`:

```go
store, err := storage.Default(ctx)
if err != nil { return err }
_, err = store.Put(ctx, "reports/latest.json", reader, storage.PutOptions{ContentType: "application/json"})
```

Tenant-scoped internal calls require standard-auth context or `storage.WithTenantID`. Inspect and operate through:

```sh
scenery inspect storage -o json
scenery storage status -o json
scenery storage ls app -o json
```

Treat store roots and proxy sockets as substrate. Replicate local storage-cell object and metadata trees offsite with operator tooling when continuous durability requires it; snapshots provide explicit database-plus-storage restore points.

## Local Development

```sh
scenery up --detach
scenery ps -o json
scenery logs --follow
scenery traces list -o json --since 15m
scenery metrics list -o json --since 1h
scenery down
```

The default wait checks every advertised route and one script or stylesheet asset from each frontend. If this returns successfully, the printed URLs have passed an end-to-end HTTP probe; use `--wait registered` only when another process will own the readiness wait.

Discover URLs from `scenery ps -o json`; do not guess hidden ports. Use a Git worktree for a second live code copy.

### Branded Dev Domain Per Worktree

To serve local dev at your own domain instead of `localhost:<port>`, add a path-mode dev domain to `.scenery.json`:

```json
{
	"frontends": {
	  "next": { "root": "apps/next" },
	  "blog": { "root": "apps/blog" }
	},
	"envs": {
	  "local": {
		"default": true,
		"domain": "local.example.com",
		"expose": ["api", "next"],
		"frontends": {"next": {"serve": "development"}, "blog": {"serve": "production"}}
	  }
	}
}
```

Branch `main` serves `https://local.example.com/`; a worktree on branch `pricing` serves `https://pricing-local.example.com/` (dash join — every host stays one DNS label deep, inside a single `*.example.com` wildcard and Cloudflare Universal SSL). The URL structure is unchanged path mode: `/api/`, `/console/`, `/<frontend>/`.

`expose` narrows what the domain origin serves; absent means everything, and `localhost:<port>` always serves everything. `serve: "production"` builds that frontend once and serves the built `dist/` statically — no dev server, no HMR; editing its sources rebuilds the bundle in place.

Loopback-only setup (this machine's browser only):

1. A records for `local.example.com` and `*.example.com` to `127.0.0.1` (plain DNS, no proxying).
2. `scenery system edge install`, then `scenery system edge trust`.

Cloudflare-fronted setup (reachable from any device; Cloudflare terminates public TLS, so no local CA trust anywhere):

1. Proxied A records for `local.example.com` and the `*.example.com` catch-all pointing at your static IP; explicit records for real sites keep winning over the wildcard.
2. Set the zone SSL mode to "Full" (not "Full (strict)") so Cloudflare accepts the Scenery edge's internal origin certificate.
3. Forward router ports 80/443 to the dev machine and run `scenery deploy setup` once so the edge listens publicly.
4. Consider Cloudflare Access in front of the dev hostnames — with `expose` absent, the whole dev surface (console and runtime included) is internet-reachable.

When the edge is not serving, `scenery up` still starts and keeps localhost URLs; the warning names the missing setup step. Domain hosts are single-owner per branch label: a second worktree on the same branch keeps localhost URLs and reports `domain_host_conflict`.

## Serve A Production Frontend Publicly

For a public deployment that should not ship the Vite dev runtime, mark the frontend production and give the app a deploy domain:

```json
{
  "frontends": { "app": { "root": "apps/app" } },
  "envs": {
	"local": {"default": true, "frontends": {"app": {"serve": "development"}}},
	"production": {"domain": "app.example.com", "frontends": {"app": {"serve": "production"}}, "deploy": {"root": "app", "ssh": ["my-server"]}}
  }
}
```

`scenery deploy my-server` then syncs source, waits for remote readiness, and runs remote `scenery deploy publish`: the frontend builds on the server (`vite build --base /<name>/`), lands as an immutable release under the Scenery agent home, and the managed Caddy edge serves it directly — compressed, cached (`/assets/*` immutable, entry document revalidated), with SPA fallback and byte ranges — while `/api/*` keeps flowing through the Scenery router. A failed build, invalid Caddyfile, or failed probe leaves the previous frontend public. On a Linux server, run `scenery deploy setup` once as root first (systemd units for the agent, edge, and boot resume). Verify with `scenery deploy status -o json`: each target's `frontends[].mode` should be `caddy_static`.

## Debug A Failing App

1. Run `scenery doctor -o json`.
2. Run `scenery check -o json` and branch on diagnostic codes.
3. Inspect source/effective/expanded views and provenance.
4. Run `scenery generate --check -o json` for artifact drift.
5. Inspect runtime state with `scenery ps -o json` and logs/traces/metrics.
6. Debug substrate only when scenery status identifies it as the failing layer.

Useful commands:

```sh
scenery inspect app -o json
scenery inspect routes -o json
scenery inspect endpoints -o json
scenery inspect build -o json
scenery inspect paths -o json
scenery logs -o jsonl --limit 200
```

## Validation Checklist

Before finishing an app change:

```sh
scenery fmt --check -o json
scenery check -o json
scenery generate --check -o json
go test ./...
scenery harness -o json --write
```

For generated TypeScript, also run the host app's typecheck/tests. For UI work, follow the target subtree instructions and use `scenery harness ui -o json --write` when behavior is browser-visible.
