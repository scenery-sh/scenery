# Scenery DSL

Scenery apps are ordinary Go modules plus a small set of declarations. The DSL is split across:

- `.scenery.json` app config
- `//scenery:*` Go directives
- struct tags on request, response, and model fields
- small Go builder packages such as `scenery.sh/model`, `scenery.sh/page`, `scenery.sh/durable`, and `scenery.sh/cron`

Use `docs/local-contract.md` for the exact machine contract. This file is the human map.

## App Root

An app root is marked by `.scenery.json`. `.config.json` is accepted as an alias when `.scenery.json` is absent.

```json
{
  "name": "hello",
  "id": "hello-dev",
  "auth": { "enabled": true },
  "build": { "go_flags": ["-tags=roofmapnet_native"] },
  "watch": { "ignore": ["reference/"] },
  "frontends": {
    "web": { "root": "web" }
  }
}
```

Common config surfaces:

- `name`, `id`: app identity.
- `auth.enabled`: enables built-in standard auth and auth endpoints.
- `build.go_flags`: literal Go build args used by Scenery-owned builds.
- `watch.ignore`: app-root-relative paths ignored by `scenery up` rebuild watching, not by Git.
- `frontends`: frontend roots for dev routing and generated web packages.
- `storage`: Scenery-owned storage stores, access, tenant scoping, and size limits.
- `dev.services`: kind-less Postgres database services; each service maps to one schema in the app/worktree database.
- `database.apply`: explicit database setup command.
- `tasks`, `validation`: app-owned task and validation profiles.

## API Directives

Declare HTTP endpoints with `//scenery:api`.

```go
//scenery:api public path=/hello/:name method=GET
func Hello(ctx context.Context, name string) (*HelloResponse, error) {
	return &HelloResponse{Message: "hello " + name}, nil
}
```

Shape:

```text
//scenery:api public|auth|private [raw] [path=/...] [method=GET,POST] [tag:name]
```

- `public`: reachable without auth.
- `auth`: external route requiring auth.
- `private`: internal-only; call through generated helpers.
- `raw`: handler owns `http.ResponseWriter` and `*http.Request`.
- `path`: route path; `:name` segments become path parameters.
- `method`: one or more HTTP methods.
- `tag:name`: label used by middleware targeting.

Typed endpoint signatures use `context.Context`, optional path params, optional request payload, and typed response/error returns. Raw endpoints use:

```go
func Events(w http.ResponseWriter, req *http.Request)
```

## Request And Response Tags

Request structs can decode from JSON, path/query/header/cookie sources.

```go
type SearchRequest struct {
	Query string `query:"q"`
	Token string `header:"authorization" scenery:"optional"`
}
```

Supported request tags:

- `json`
- `query` or `qs`
- `header`
- `cookie`
- `scenery:"optional"`

Response structs can control status:

```go
type CreatedResponse struct {
	Status int    `json:"-" scenery:"httpstatus"`
	ID     string `json:"id"`
}
```

`scenery:"sensitive"` marks fields for redaction in supported diagnostic paths.

## Services

Use `//scenery:service` for a package service struct with dependencies.

```go
//scenery:service
type Service struct {
	prefix string
}

func initService() (*Service, error) {
	return &Service{prefix: "hello"}, nil
}

//scenery:api public path=/hello method=GET
func (s *Service) Hello(ctx context.Context) (*HelloResponse, error) {
	return &HelloResponse{Message: s.prefix}, nil
}
```

Optional `Shutdown(context.Context)` on the service is called by the runtime.

## Auth Handlers

Use `//scenery:authhandler` only for app-owned auth. Standard auth is usually simpler: set `auth.enabled: true`.

Supported handler returns:

```go
//scenery:authhandler
func Authenticate(ctx context.Context, token string) (auth.UID, error)

//scenery:authhandler
func Authenticate(ctx context.Context, req AuthRequest) (auth.UID, AuthData, error)
```

The second parameter may be `string` or a request struct.

## Middleware

Middleware wraps endpoint execution.

```go
//scenery:middleware target=tag:admin
func AdminOnly(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}

//scenery:middleware global target=all
func Trace(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
```

Targets are `all` or `tag:<name>`.

## Static Model DSL

Mark a Go struct as a static model with `//scenery:model`. Optional `model.Entity[T](...)` calls add metadata.

```go
//scenery:model
type Task struct {
	ID        string    `db:"id"`
	TenantID  string    `db:"tenant_id"`
	Title     string    `db:"title"`
	Status    string    `db:"status"`
	CreatedAt time.Time `db:"created_at"`
}

var taskEntity = model.Entity[Task](
	model.Table("tasks"),
	model.Generate(model.ActionList, model.ActionGet, model.ActionCreate),
	model.Field("Status", model.EnumValues("todo", "doing", "done"), model.Filterable()),
	model.Seed(Task{ID: "seed-task-1", Title: "Seeded", Status: "todo"}),
)
```

Entity options:

- `model.Table(name)`: generated Scenery-owned table in the service schema.
- `model.ExistingTable(schema, table)`: existing physical table; read-only generation only.
- `model.Generate(actions...)`: generate CRUD endpoints. No args means all actions.
- `model.Disable(actions...)`: remove generated actions from the selected set.
- `model.Override(action, endpoint)`: use a handwritten endpoint for an action.
- `model.Seed(rows...)`: generate seed SQL for generated-source entities.
- `model.Field(name, opts...)`: field metadata.

Actions:

- `model.ActionList`
- `model.ActionGet`
- `model.ActionCreate`
- `model.ActionUpdate`
- `model.ActionDelete`

Field options:

- `model.EnumValues(values...)`: enum-like static values.
- `model.Filterable()`: marks a field as filterable.
- `model.Computed()`: marks a computed field; not materialized by generated collection pages yet.
- `model.Relationship()`: relationship marker; joins are not generated yet.
- `model.RenamedFrom(name)`: rename hint.

Column names come from `db:"column"`, then `scenery:"column=column"`, then snake_case field names.

Existing table rules:

- require explicit schema and table
- allow generated list/get only
- reject generated create/update/delete
- reject `model.Seed`
- still generate source rows, source metadata, projections, collections, pages, and routes
- do not generate Atlas HCL or seed SQL for that table

## Static Page DSL

Declare a generated collection page with `//scenery:page` and `page.Collection[T]`.

```go
//scenery:page
var TaskList = page.Collection[Task]{
	Route:   "/tasks",
	Title:   "Tasks",
	Columns: []string{"Title", "Status", "CreatedAt"},
	ColumnDisplays: []page.ColumnDisplayRef{
		page.Column("Status", page.DisplayBadge),
		page.Column("CreatedAt", page.DisplayDateTime),
	},
	Filters: []page.FilterRef{
		page.Filter("Status", page.NotEqual, "done"),
	},
	Sorts: []page.SortRef{
		page.Sort("CreatedAt", page.Desc),
	},
	Slots: []page.ComponentRef{
		page.Component("TaskStatusBadge"),
	},
}
```

Collection fields:

- `Route`: page route.
- `Title`: display title.
- `Columns`: model fields projected into the page record.
- `ColumnDisplays`: display hints.
- `Filters`: static source-row filters.
- `Sorts`: static source-row ordering.
- `Slots`: app-owned frontend components resolved under the frontend root.

Display kinds:

- `page.DisplayText`
- `page.DisplayDateTime`
- `page.DisplayBadge`

Filter ops:

- `page.Equal`
- `page.NotEqual`
- `page.IsNull`
- `page.IsNotNull`

Sort directions:

- `page.Asc`
- `page.Desc`

Generated pages use source rows for storage input and materialize page records for rendering.

## Generated Model And Page Output

Run:

```sh
scenery inspect models --json
scenery inspect views --json
scenery inspect endpoints --json
scenery generate data --dry-run --json
```

Generated data writes disposable files under:

```text
.scenery/gen/db/<service>/schema.hcl
.scenery/gen/db/<service>/seed.sql
.scenery/gen/web/<frontend>/
```

Generated web packages export:

- row, create, and patch types
- entity source definitions with `schema`, `table`, and `qualifiedTable`
- page projection records and materializers
- collection descriptors with static filters/sorts/display metadata
- runtime adapter factories
- default page and route factories
- `registerGeneratedRoutes`

Apps consume generated frontend code through an app-owned alias such as `@scenery/generated`.

## Durable DSL

Use `scenery.sh/durable` for typed durable task declarations, enqueue, schedules, local execution, signals, and step results. Scenery discovers `durable.NewTask` calls, imports their packages into generated main, reconciles declarations into the app Postgres database's `scenery` schema at runtime startup, `durable.Start` writes queued jobs, and `all`/`worker` roles execute registered Go handlers locally with retry scheduling from the task config. `durable.Schedule` records an interval schedule that the API/all runtime materializes into queued jobs. `durable.Step` persists local handler step results by job/key and reuses succeeded results; outside a durable job context it just runs the function. `durable.Signal` appends a JSON signal row and event for a durable run. `scenery inspect durable --json` emits `scenery.inspect.durable.v2` with declarations, service schemas, and redacted app database metadata. The runtime exposes authenticated durable worker HTTP endpoints for lease, heartbeat, complete, and fail with hashed worker tokens and lease-ID fencing.

```go
var detectRoof = durable.NewTask[DetectInput, DetectOutput](
	"roof.detect.v1",
	durable.TaskConfig{
		Service:     "maps",
		MaxAttempts: 3,
		Retry: durable.RetryPolicy{
			InitialInterval: 5 * time.Second,
			BackoffFactor:  2,
			MaxInterval:    2 * time.Minute,
		},
	},
	detectRoofFunc,
)
```

```go
run, err := durable.Start(ctx, detectRoof, input, durable.StartOptions{DedupeKey: "roof:" + input.ID})
```

```go
result, err := durable.Step(ctx, "fetch-imagery", func(ctx context.Context) (Imagery, error) {
	return fetchImagery(ctx, input.TileID)
})
err = durable.Signal(ctx, run, "operator-approved", Approval{By: userID})
err = durable.Schedule(ctx, detectRoof, DetectInput{TileID: "north"}, durable.ScheduleOptions{
	ID:    "detect-north",
	Every: 15 * time.Minute,
})
```

## Cron DSL

Use `scenery.sh/cron` for scheduled jobs.

```go
var _ = cron.NewJob("nightly-sync", cron.JobConfig{
	Every:                cron.Hour,
	Endpoint:             syncNightly,
	OverlapPolicy:        cron.OverlapSkip,
	CatchupWindow:        10 * time.Minute,
	PauseOnFailure:       true,
})

func syncNightly(ctx context.Context) error {
	return nil
}
```

Job config:

- `Title`
- `Endpoint`
- `Every`
- `Schedule`
- `OverlapPolicy`
- `CatchupWindow`
- `PauseOnFailure`

Overlap policies include `skip`, `buffer_one`, `buffer_all`, `cancel_other`, `terminate_other`, and `allow_all`.

## Inspecting The DSL

Use JSON surfaces instead of reading generated cache files:

```sh
scenery check --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery inspect durable --json
scenery inspect generators --json
```

Generated `.scenery/gen/*` files are cache/output, not the API.
