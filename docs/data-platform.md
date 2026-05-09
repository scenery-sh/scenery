# onlava Data Platform

The `github.com/pbrazdil/onlava/data` package exposes onlava's beta dynamic data platform for Go apps.

It is metadata-driven, but not an ORM. Objects and fields live in PostgreSQL metadata tables, while ordinary scalar fields are backed by real PostgreSQL tables, columns, indexes, foreign keys, and outbox rows.

## Open A Store

```go
store, err := data.Open(ctx, pool, data.Options{})
if err != nil {
	return err
}
actor := data.ActorFromContext(ctx)
```

`pool` can be a `pgxpool.Pool` or any value implementing `data.DB`.

## Objects And Fields

```go
_, err = store.CreateObject(ctx, actor, data.CreateObjectRequest{
	TenantKey:    "acme",
	NameSingular: "company",
	NamePlural:   "companies",
})

_, err = store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
	TenantKey: "acme",
	Name:      "stage",
	Type:      data.FieldSelect,
	Options: []data.FieldOptionRequest{
		{Value: "lead"},
		{Value: "won"},
	},
})
```

Select fields use text metadata options, not PostgreSQL enum types.

## Records And Queries

```go
_, err = store.CreateRecord(ctx, actor, "company", data.CreateRecordRequest{
	TenantKey: "acme",
	Values: data.Record{
		"name":  "Acme",
		"stage": "won",
	},
})

page, err := store.QueryRecords(ctx, actor, "company", data.QueryRecordsRequest{
	TenantKey: "acme",
	Query: data.Query{
		Select: []string{"name", "stage"},
		Filter: data.EQ("stage", "won"),
		Sort:   []data.Sort{data.Asc("name")},
		Limit:  50,
	},
})
```

`RecordPage.NextCursor` is an opaque keyset cursor. Reuse the same object and sort shape when passing it back as `Query.Cursor`.

## Relations

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

`many_to_one` creates a UUID column and PostgreSQL foreign key. Queries can use one-hop paths such as `company.name`. `many_to_many` creates a join table; ergonomic record mutation helpers for many-to-many fields are not stable yet.

## Indexes And Saved Views

```go
_, err = store.CreateIndex(ctx, actor, "company", data.CreateIndexRequest{
	TenantKey: "acme",
	Name:      "company_stage_name",
	Fields: []data.IndexField{
		{Field: "stage"},
		{Field: "name"},
	},
})

_, err = store.CreateView(ctx, actor, "company", data.CreateViewRequest{
	TenantKey:  "acme",
	Name:       "won_companies",
	Columns:    []string{"name", "stage"},
	Filter:     data.EQ("stage", "won"),
	Sort:       []data.Sort{data.Asc("name")},
	Visibility: data.ViewVisibilityShared,
})
```

Saved views are reusable query shapes. Use `QueryView` to execute one.

## Errors

Public data methods wrap failures in `*data.Error` where possible. Use `data.CodeOf(err)` to classify errors:

```go
if err != nil {
	switch data.CodeOf(err) {
	case data.ErrorInvalidCursor:
		// Ask the caller to restart pagination.
	case data.ErrorFieldNotFound:
		// The app asked for a field not present in metadata.
	}
}
```

The data package is still beta. `docs/local-contract.md` is the source of truth for which parts are stable, beta, or dev-only.
