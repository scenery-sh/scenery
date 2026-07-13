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

## Generate A TypeScript Client

Declare a root target:

```hcl
typescript_client "public_api" {
  gateways    = [http_gateway.public_api]
  package     = "@example/app-client"
  module      = "esm"
  runtime     = "fetch"
  output_root = "clients/generated/public_api"
}
```

Add the output root to `workspace.managed_generated_roots`, then run:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
```

Commit the generated descriptor and source files. Regenerate after reachable type, binding, codec, gateway, auth, or outcome changes. Generated clients never infer behavior from Go symbols.

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
