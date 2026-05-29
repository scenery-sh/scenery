# Data Platform Developer Runbook

This runbook is for operating the beta `github.com/pbrazdil/onlava/data` platform from app code and local tooling. Use `docs/data-platform.md` for the overview and `docs/local-contract.md` for contract status.

## Opening A Store

```go
store, err := data.Open(ctx, pool, data.Options{})
if err != nil {
	return err
}
actor := data.ActorFromContext(ctx)
```

`pool` can be a `pgxpool.Pool` or any type implementing `data.DB`. Do not import `internal/objectstore` from apps.

Validate:

```sh
go test ./data
```

## Creating Tenant, Object, And Field Metadata

Objects and fields are tenant-scoped. Creating an object creates metadata and a physical PostgreSQL table. Creating a field creates metadata and physical column(s) when appropriate.

```go
_, err = store.CreateObject(ctx, actor, data.CreateObjectRequest{
	TenantKey:    "acme",
	NameSingular: "company",
	NamePlural:   "companies",
})

_, err = store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
	TenantKey: "acme",
	Name:      "name",
	Type:      data.FieldText,
})
```

Inspect:

```sh
onlava inspect data --json --database-url "$DATABASE_URL" --tenant acme --object company
```

## Select And Multi-Select Options

Select fields store option values as text, not PostgreSQL enum types.

```go
_, err = store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
	TenantKey: "acme",
	Name:      "stage",
	Type:      data.FieldSelect,
	Options: []data.FieldOptionRequest{
		{Value: "lead", Label: "Lead"},
		{Value: "won", Label: "Won"},
	},
})
```

Use application validation through the data package. Do not add user-managed enum types directly in PostgreSQL.

## Composite Fields

Composite fields expand into multiple physical columns and reassemble into logical record values on read. Examples include:

```text
full_name
address
currency
emails
phones
```

Use the public record shape and let the data layer handle physical columns.

## Relation Fields

Many-to-one relation fields create a UUID column and a PostgreSQL foreign key.

```go
_, err = store.CreateField(ctx, actor, "deal", data.CreateFieldRequest{
	TenantKey:      "acme",
	Name:           "company",
	Type:           data.FieldRelation,
	RelationObject: "company",
	Relation: data.RelationSettings{
		Kind:     data.RelationManyToOne,
		OnDelete: data.RelationDeleteRestrict,
	},
})
```

Queries can use one-hop paths such as `company.name`. Many-to-many creates a join table, but ergonomic many-to-many record mutation helpers are still beta/limited.

## Indexes

Indexes are metadata-backed and physically created through migration rows.

```go
_, err = store.CreateIndex(ctx, actor, "company", data.CreateIndexRequest{
	TenantKey: "acme",
	Name:      "company_stage_name",
	Method:    data.IndexMethodBTree,
	Fields: []data.IndexField{
		{Field: "stage"},
		{Field: "name"},
	},
})
```

Inspect logical and physical state:

```sh
onlava inspect data --json --database-url "$DATABASE_URL" --tenant acme --object company
```

Do not pass raw SQL fragments as index options. Public index opclasses are intentionally constrained.

## Saved Views

Saved views persist reusable query shapes.

```go
_, err = store.CreateView(ctx, actor, "company", data.CreateViewRequest{
	TenantKey:  "acme",
	Name:       "won_companies",
	Columns:    []string{"name", "stage"},
	Filter:     data.EQ("stage", "won"),
	Sort:       []data.Sort{data.Asc("name")},
	Visibility: data.ViewVisibilityShared,
})

page, err := store.QueryView(ctx, actor, "company", "won_companies", data.QueryViewRequest{
	TenantKey: "acme",
})
```

View mutations require write permission on the object.

## Record CRUD

```go
created, err := store.CreateRecord(ctx, actor, "company", data.CreateRecordRequest{
	TenantKey: "acme",
	Values: data.Record{"name": "Acme", "stage": "lead"},
})

updated, err := store.UpdateRecord(ctx, actor, "company", created.ID, data.UpdateRecordRequest{
	TenantKey: "acme",
	Values: data.Record{"stage": "won"},
})

_, err = store.DeleteRecord(ctx, actor, "company", updated.ID, data.DeleteRecordRequest{
	TenantKey: "acme",
})
```

All mutations run in transactions and write outbox events before commit.

## Query Filters, Sorts, And Cursors

```go
page, err := store.QueryRecords(ctx, actor, "company", data.QueryRecordsRequest{
	TenantKey: "acme",
	Query: data.Query{
		Select: []string{"name", "stage"},
		Filter: data.And(data.EQ("stage", "won"), data.Search("acme")),
		Sort:   []data.Sort{data.Asc("name")},
		Limit:  50,
	},
})
next := page.NextCursor
```

`NextCursor` is opaque. Pass it back only with the same object and sort shape. Cursor pagination uses keyset semantics, not offset pagination.

Current limitation: nullable cursor sort behavior is conservative. If cursor pagination rejects a nullable sort, add a non-null sortable field or restart the query without a cursor.

## Live Events And SSE

Expose events through a raw onlava endpoint:

```go
//onlava:api auth raw path=/data/events method=GET
func Events(w http.ResponseWriter, r *http.Request) {
	actor := data.ActorFromContext(r.Context())
	_ = store.ServeEvents(r.Context(), actor, w, r)
}
```

SSE clients can reconnect with `after_seq` or `Last-Event-ID`. Event matching is query-aware:

```text
created: match after
updated: match before or after
deleted: match before
```

## Trigger-Backed Outbox

Normal data mutations write precise outbox rows. Trigger-backed outbox catches direct SQL changes.

```go
_, err = store.EnableOutboxTriggers(ctx, actor, "acme", "company")
```

Trigger-backed events cannot know the app actor unless the transaction sets onlava actor context. Direct SQL edits without actor context should be treated as anonymous/system actor events.

## Import And Export

Export:

```go
bundle, err := store.ExportTenant(ctx, actor, data.ExportTenantRequest{
	TenantKey: "acme",
})
```

Import:

```go
resp, err := store.ImportTenant(ctx, actor, data.ImportTenantRequest{
	Bundle:          *bundle,
	TargetTenantKey: "acme_copy",
})
```

Imported records get new IDs. Use `resp.RecordIDMap` to reconcile references.

Validate schema:

```sh
onlava inspect docs --json
```

The export bundle schema is `docs/schemas/onlava.data.export.v1.schema.json`.

## Standard Auth Tenant Permissions

Standard auth exposes a `tenant_id`; data permissions can map that directly to `TenantKey`.

```go
store, err := data.Open(ctx, pool, data.Options{
	Permissions: data.StandardAuthPermissions{},
})
tenantKey, err := data.RequireTenantKeyFromContext(ctx)
actor := data.ActorFromContext(ctx)
```

`StandardAuthPermissions` fails closed when there is no auth tenant or the caller asks for another tenant. Use `Base` to add object, field, or row-level rules.

## Inspect Data Output

Use:

```sh
onlava inspect data --json --database-url "$DATABASE_URL"
onlava inspect data --json --database-url "$DATABASE_URL" --tenant acme
onlava inspect data --json --database-url "$DATABASE_URL" --tenant acme --object company
```

Inspect answers:

```text
Did metadata bootstrap?
Which tenants and objects exist?
What physical tables, columns, indexes, triggers, and relations exist?
Are migrations failed or pending?
What is latest outbox sequence?
Are saved views and search metadata present?
```

## Migration Failure Recovery

When a migration fails:

1. Run `onlava inspect data --json --database-url "$DATABASE_URL" --tenant <tenant> --object <object>`.
2. Check failed rows in `onlava_data.schema_migrations`.
3. Compare metadata to physical schema.
4. Fix the cause, such as name collision, unsupported type change, or permissions.
5. Retry through the public data API.

Do not manually bump `schema_version`. The migrator bumps versions only after verification.

## Schema Drift Debugging

Use inspect output first. Physical drift usually appears as missing columns, missing indexes, missing triggers, or relation constraint mismatch.

For local database inspection:

```sh
onlava psql
```

Then inspect:

```sql
select * from onlava_data.schema_migrations order by started_at desc limit 20;
select * from onlava_data.outbox_events order by seq desc limit 20;
```

## Direct SQL Caveats

Direct SQL writes bypass application validation and permission hooks. Trigger-backed outbox can capture direct row changes, but it does not enforce app-level permissions, update search documents, or infer actor context unless configured in the transaction.

Use public `data.Store` mutations when correctness matters.

## Performance Notes

- Prefer real scalar fields and metadata-backed indexes for list/query paths.
- Use keyset cursors for pagination.
- Use `data.Search("term")` only on objects with searchable fields.
- Inspect index state before assuming a slow query is a runtime bug.
- Keep selected fields narrow for live subscriptions.

## Known Beta Limitations

- Many-to-many relation physical structures exist, but ergonomic record mutation helpers are limited.
- Relation path queries are intentionally shallow.
- Search documents are maintained by normal data mutations; direct SQL does not automatically rebuild them in this version.
- Trigger-backed outbox captures direct SQL writes but actor context may be anonymous/system.
- Cursor pagination rejects or constrains some nullable sort cases.
- Dashboard Data Explorer is a local developer surface, not production UI.

## Validation Matrix

Unit and public package:

```sh
go test ./data ./internal/objectstore ./internal/datainspect
```

PostgreSQL-backed tests:

```sh
ONLAVA_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/objectstore ./internal/datainspect
```

`ONLAVA_TEST_DATABASE_URL` should point at a PostgreSQL database where the test helper may create package-scoped databases.

Full repo:

```sh
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```
