package datainspect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pbrazdil/onlava/internal/objectstore"
)

const schemaVersion = "onlava.inspect.data.v1"

type Options struct {
	DatabaseURL string
	TenantKey   string
	ObjectName  string
}

type Response struct {
	SchemaVersion string           `json:"schema_version"`
	Schemas       Schemas          `json:"schemas"`
	Warnings      []string         `json:"warnings,omitempty"`
	Tenants       []TenantSummary  `json:"tenants"`
	Objects       []ObjectSummary  `json:"objects"`
	Migrations    MigrationSummary `json:"migrations"`
	Outbox        OutboxSummary    `json:"outbox"`
}

type Schemas struct {
	Metadata string `json:"metadata"`
	Records  string `json:"records"`
}

type TenantSummary struct {
	ID              string `json:"id"`
	Key             string `json:"key"`
	Name            string `json:"name"`
	Objects         int64  `json:"objects"`
	LatestOutboxSeq int64  `json:"latest_outbox_seq"`
}

type ObjectSummary struct {
	ID                    string         `json:"id"`
	TenantID              string         `json:"tenant_id"`
	TenantKey             string         `json:"tenant_key"`
	Name                  string         `json:"name"`
	PhysicalTable         string         `json:"physical_table"`
	SchemaVersion         int64          `json:"schema_version"`
	OutboxTriggersEnabled bool           `json:"outbox_triggers_enabled"`
	OutboxTriggerName     string         `json:"outbox_trigger_name,omitempty"`
	OutboxTriggerPresent  bool           `json:"outbox_trigger_present"`
	Fields                []FieldSummary `json:"fields"`
	Indexes               []IndexSummary `json:"indexes"`
	Views                 []ViewSummary  `json:"views"`
}

type FieldSummary struct {
	Name         string           `json:"name"`
	Label        string           `json:"label"`
	Type         string           `json:"type"`
	Columns      []string         `json:"columns"`
	Searchable   bool             `json:"searchable"`
	SearchWeight string           `json:"search_weight,omitempty"`
	Relation     *RelationSummary `json:"relation,omitempty"`
}

type RelationSummary struct {
	Object        string `json:"object"`
	Kind          string `json:"kind"`
	InverseField  string `json:"inverse_field,omitempty"`
	OnDelete      string `json:"on_delete"`
	JoinTableName string `json:"join_table_name,omitempty"`
}

type IndexSummary struct {
	Name         string              `json:"name"`
	PhysicalName string              `json:"physical_name"`
	Method       string              `json:"method"`
	Unique       bool                `json:"unique"`
	Fields       []IndexFieldSummary `json:"fields"`
	Physical     PhysicalIndexState  `json:"physical"`
}

type IndexFieldSummary struct {
	Name      string `json:"name"`
	Direction string `json:"direction,omitempty"`
	OpClass   string `json:"opclass,omitempty"`
}

type PhysicalIndexState struct {
	Exists bool `json:"exists"`
	Drift  bool `json:"drift"`
}

type ViewSummary struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Columns    []string        `json:"columns"`
	Filter     json.RawMessage `json:"filter,omitempty"`
	Sort       json.RawMessage `json:"sort,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	Visibility string          `json:"visibility"`
	OwnerID    string          `json:"owner_id,omitempty"`
}

type MigrationSummary struct {
	Pending int64             `json:"pending"`
	Failed  int64             `json:"failed"`
	Latest  []MigrationRecord `json:"latest"`
}

type MigrationRecord struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	TenantKey   string     `json:"tenant_key,omitempty"`
	ObjectID    string     `json:"object_id,omitempty"`
	Object      string     `json:"object,omitempty"`
	FromVersion int64      `json:"from_version"`
	ToVersion   int64      `json:"to_version"`
	Status      string     `json:"status"`
	DDL         []string   `json:"ddl,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type OutboxSummary struct {
	LatestSeq   int64 `json:"latest_seq"`
	Unpublished int64 `json:"unpublished"`
}

type db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func Build(ctx context.Context, opts Options) (Response, error) {
	if strings.TrimSpace(opts.DatabaseURL) == "" {
		return Response{}, fmt.Errorf("inspect data requires --database-url")
	}
	pool, err := pgxpool.New(ctx, opts.DatabaseURL)
	if err != nil {
		return Response{}, fmt.Errorf("connect data inspect database: %w", err)
	}
	defer pool.Close()
	return BuildFromDB(ctx, pool, opts)
}

func BuildFromDB(ctx context.Context, db db, opts Options) (Response, error) {
	resp := Response{
		SchemaVersion: schemaVersion,
		Schemas: Schemas{
			Metadata: objectstore.MetadataSchema,
			Records:  objectstore.RecordsSchema,
		},
		Tenants: []TenantSummary{},
		Objects: []ObjectSummary{},
		Migrations: MigrationSummary{
			Latest: []MigrationRecord{},
		},
	}
	ok, warnings, err := schemasReady(ctx, db)
	if err != nil {
		return Response{}, err
	}
	resp.Warnings = append(resp.Warnings, warnings...)
	if !ok {
		return resp, nil
	}
	if resp.Tenants, err = loadTenants(ctx, db, opts); err != nil {
		return Response{}, err
	}
	if resp.Objects, err = loadObjects(ctx, db, opts); err != nil {
		return Response{}, err
	}
	if resp.Migrations, err = loadMigrations(ctx, db, opts); err != nil {
		return Response{}, err
	}
	if resp.Outbox, err = loadOutbox(ctx, db, opts); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func schemasReady(ctx context.Context, db db) (bool, []string, error) {
	var warnings []string
	metadata, err := schemaExists(ctx, db, objectstore.MetadataSchema)
	if err != nil {
		return false, nil, err
	}
	if !metadata {
		return false, []string{"metadata schema onlava_data does not exist"}, nil
	}
	records, err := schemaExists(ctx, db, objectstore.RecordsSchema)
	if err != nil {
		return false, nil, err
	}
	if !records {
		warnings = append(warnings, "records schema onlava_data_records does not exist")
	}
	for _, table := range []string{"tenants", "objects", "fields", "indexes", "index_fields", "views", "view_fields", "search_documents", "schema_migrations", "outbox_events"} {
		exists, err := tableExists(ctx, db, objectstore.MetadataSchema, table)
		if err != nil {
			return false, nil, err
		}
		if !exists {
			warnings = append(warnings, fmt.Sprintf("metadata table onlava_data.%s does not exist", table))
		}
	}
	return len(warnings) == 0, warnings, nil
}

func schemaExists(ctx context.Context, db db, schema string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `select exists (select 1 from information_schema.schemata where schema_name = $1)`, schema).Scan(&exists)
	return exists, err
}

func tableExists(ctx context.Context, db db, schema, table string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `
		select exists (
			select 1 from information_schema.tables
			where table_schema = $1 and table_name = $2
		)
	`, schema, table).Scan(&exists)
	return exists, err
}

func loadTenants(ctx context.Context, db db, opts Options) ([]TenantSummary, error) {
	rows, err := db.Query(ctx, `
		select t.id::text, t.key, t.name, count(distinct o.id)::bigint, coalesce(max(e.seq), 0)::bigint
		from onlava_data.tenants t
		left join onlava_data.objects o on o.tenant_id = t.id
		left join onlava_data.outbox_events e on e.tenant_id = t.id
		where ($1::text = '' or t.key = $1)
		group by t.id, t.key, t.name
		order by t.key
	`, strings.TrimSpace(opts.TenantKey))
	if err != nil {
		return nil, fmt.Errorf("inspect data tenants: %w", err)
	}
	defer rows.Close()
	tenants := []TenantSummary{}
	for rows.Next() {
		var tenant TenantSummary
		if err := rows.Scan(&tenant.ID, &tenant.Key, &tenant.Name, &tenant.Objects, &tenant.LatestOutboxSeq); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}

func loadObjects(ctx context.Context, db db, opts Options) ([]ObjectSummary, error) {
	rows, err := db.Query(ctx, `
		select o.id::text, o.tenant_id::text, t.key, o.name_singular, o.table_name, o.schema_version,
		       o.outbox_triggers_enabled,
		       'outbox__' || substring(replace(o.id::text, '-', '') from 1 for 12) as outbox_trigger_name,
		       exists (
		         select 1
		         from pg_trigger tr
		         join pg_class c on c.oid = tr.tgrelid
		         join pg_namespace n on n.oid = c.relnamespace
		         where n.nspname = $3
		           and c.relname = o.table_name
		           and tr.tgname = 'outbox__' || substring(replace(o.id::text, '-', '') from 1 for 12)
		           and not tr.tgisinternal
		       ) as outbox_trigger_present
		from onlava_data.objects o
		join onlava_data.tenants t on t.id = o.tenant_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by t.key, o.name_singular
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName), objectstore.RecordsSchema)
	if err != nil {
		return nil, fmt.Errorf("inspect data objects: %w", err)
	}
	defer rows.Close()
	objects := []ObjectSummary{}
	byID := map[string]int{}
	for rows.Next() {
		var object ObjectSummary
		if err := rows.Scan(
			&object.ID, &object.TenantID, &object.TenantKey, &object.Name, &object.PhysicalTable, &object.SchemaVersion,
			&object.OutboxTriggersEnabled, &object.OutboxTriggerName, &object.OutboxTriggerPresent,
		); err != nil {
			return nil, err
		}
		object.Fields = []FieldSummary{}
		object.Indexes = []IndexSummary{}
		object.Views = []ViewSummary{}
		objects = append(objects, object)
		byID[object.ID] = len(objects) - 1
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		return objects, nil
	}
	fieldRows, err := db.Query(ctx, `
		select f.object_id::text, f.name, f.label, f.type, f.storage_columns,
		       f.is_searchable, f.search_weight, coalesce(ro.name_singular, ''), f.settings
		from onlava_data.fields f
		join onlava_data.objects o on o.id = f.object_id
		join onlava_data.tenants t on t.id = f.tenant_id
		left join onlava_data.objects ro on ro.id = f.relation_object_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by t.key, o.name_singular, f.name
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName))
	if err != nil {
		return nil, fmt.Errorf("inspect data fields: %w", err)
	}
	defer fieldRows.Close()
	for fieldRows.Next() {
		var objectID string
		var field FieldSummary
		var columnsJSON []byte
		var relationObject string
		var settingsJSON []byte
		if err := fieldRows.Scan(&objectID, &field.Name, &field.Label, &field.Type, &columnsJSON, &field.Searchable, &field.SearchWeight, &relationObject, &settingsJSON); err != nil {
			return nil, err
		}
		field.Columns = columnNames(columnsJSON)
		field.Relation = relationSummary(field.Type, relationObject, settingsJSON)
		if index, ok := byID[objectID]; ok {
			objects[index].Fields = append(objects[index].Fields, field)
		}
	}
	if err := fieldRows.Err(); err != nil {
		return nil, err
	}
	indexRows, err := db.Query(ctx, `
		select i.object_id::text, i.name, i.physical_name, i.method, i.is_unique,
		       exists (
		         select 1
		         from pg_index pi
		         join pg_class idx on idx.oid = pi.indexrelid
		         join pg_class tbl on tbl.oid = pi.indrelid
		         join pg_namespace n on n.oid = tbl.relnamespace
		         where n.nspname = $3
		           and tbl.relname = o.table_name
		           and idx.relname = i.physical_name
		       ) as physical_exists
		from onlava_data.indexes i
		join onlava_data.objects o on o.id = i.object_id
		join onlava_data.tenants t on t.id = i.tenant_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by t.key, o.name_singular, i.name
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName), objectstore.RecordsSchema)
	if err != nil {
		return nil, fmt.Errorf("inspect data indexes: %w", err)
	}
	defer indexRows.Close()
	indexPositions := map[string][2]int{}
	for indexRows.Next() {
		var objectID string
		var physicalExists bool
		var item IndexSummary
		if err := indexRows.Scan(&objectID, &item.Name, &item.PhysicalName, &item.Method, &item.Unique, &physicalExists); err != nil {
			return nil, err
		}
		item.Fields = []IndexFieldSummary{}
		item.Physical = PhysicalIndexState{Exists: physicalExists, Drift: !physicalExists}
		if objectIndex, ok := byID[objectID]; ok {
			objects[objectIndex].Indexes = append(objects[objectIndex].Indexes, item)
			indexPositions[item.PhysicalName] = [2]int{objectIndex, len(objects[objectIndex].Indexes) - 1}
		}
	}
	if err := indexRows.Err(); err != nil {
		return nil, err
	}
	indexFieldRows, err := db.Query(ctx, `
		select i.physical_name, f.name, ix.direction, ix.opclass
		from onlava_data.index_fields ix
		join onlava_data.indexes i on i.id = ix.index_id
		join onlava_data.fields f on f.id = ix.field_id
		join onlava_data.objects o on o.id = i.object_id
		join onlava_data.tenants t on t.id = i.tenant_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by t.key, o.name_singular, i.name, ix.position
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName))
	if err != nil {
		return nil, fmt.Errorf("inspect data index fields: %w", err)
	}
	defer indexFieldRows.Close()
	for indexFieldRows.Next() {
		var physicalName string
		var field IndexFieldSummary
		if err := indexFieldRows.Scan(&physicalName, &field.Name, &field.Direction, &field.OpClass); err != nil {
			return nil, err
		}
		if position, ok := indexPositions[physicalName]; ok {
			objects[position[0]].Indexes[position[1]].Fields = append(objects[position[0]].Indexes[position[1]].Fields, field)
		}
	}
	if err := indexFieldRows.Err(); err != nil {
		return nil, err
	}
	viewRows, err := db.Query(ctx, `
		select v.object_id::text, v.name, v.type, v.filter, v.sort, v.limit_count, v.visibility, v.owner_id
		from onlava_data.views v
		join onlava_data.objects o on o.id = v.object_id
		join onlava_data.tenants t on t.id = v.tenant_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by t.key, o.name_singular, v.name
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName))
	if err != nil {
		return nil, fmt.Errorf("inspect data views: %w", err)
	}
	defer viewRows.Close()
	viewPositions := map[string][2]int{}
	for viewRows.Next() {
		var objectID string
		var view ViewSummary
		var filterJSON, sortJSON []byte
		if err := viewRows.Scan(&objectID, &view.Name, &view.Type, &filterJSON, &sortJSON, &view.Limit, &view.Visibility, &view.OwnerID); err != nil {
			return nil, err
		}
		view.Filter = rawJSONOrNil(filterJSON)
		view.Sort = rawJSONOrNil(sortJSON)
		if objectIndex, ok := byID[objectID]; ok {
			objects[objectIndex].Views = append(objects[objectIndex].Views, view)
			viewPositions[objectID+"."+view.Name] = [2]int{objectIndex, len(objects[objectIndex].Views) - 1}
		}
	}
	if err := viewRows.Err(); err != nil {
		return nil, err
	}
	viewFieldRows, err := db.Query(ctx, `
		select v.object_id::text, v.name, vf.field_name
		from onlava_data.view_fields vf
		join onlava_data.views v on v.id = vf.view_id
		join onlava_data.objects o on o.id = v.object_id
		join onlava_data.tenants t on t.id = v.tenant_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by t.key, o.name_singular, v.name, vf.position
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName))
	if err != nil {
		return nil, fmt.Errorf("inspect data view fields: %w", err)
	}
	defer viewFieldRows.Close()
	for viewFieldRows.Next() {
		var objectID, viewName, field string
		if err := viewFieldRows.Scan(&objectID, &viewName, &field); err != nil {
			return nil, err
		}
		if position, ok := viewPositions[objectID+"."+viewName]; ok {
			objects[position[0]].Views[position[1]].Columns = append(objects[position[0]].Views[position[1]].Columns, field)
		}
	}
	return objects, viewFieldRows.Err()
}

func loadMigrations(ctx context.Context, db db, opts Options) (MigrationSummary, error) {
	var summary MigrationSummary
	summary.Latest = []MigrationRecord{}
	err := db.QueryRow(ctx, `
		select
			count(*) filter (where m.status in ('pending', 'running'))::bigint,
			count(*) filter (where m.status = 'failed')::bigint
		from onlava_data.schema_migrations m
		left join onlava_data.tenants t on t.id = m.tenant_id
		left join onlava_data.objects o on o.id = m.object_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName)).Scan(&summary.Pending, &summary.Failed)
	if err != nil {
		return summary, fmt.Errorf("inspect data migration counts: %w", err)
	}
	rows, err := db.Query(ctx, `
		select m.id::text, m.tenant_id::text, coalesce(t.key, ''), coalesce(m.object_id::text, ''),
		       coalesce(o.name_singular, ''), m.from_version, m.to_version, m.status,
		       m.ddl, m.started_at, m.finished_at, m.error
		from onlava_data.schema_migrations m
		left join onlava_data.tenants t on t.id = m.tenant_id
		left join onlava_data.objects o on o.id = m.object_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or o.name_singular = $2)
		order by m.started_at desc
		limit 10
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName))
	if err != nil {
		return summary, fmt.Errorf("inspect data latest migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item MigrationRecord
		var ddlJSON []byte
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.TenantKey, &item.ObjectID, &item.Object,
			&item.FromVersion, &item.ToVersion, &item.Status, &ddlJSON,
			&item.StartedAt, &item.FinishedAt, &item.Error,
		); err != nil {
			return summary, err
		}
		item.DDL = stringArray(ddlJSON)
		summary.Latest = append(summary.Latest, item)
	}
	return summary, rows.Err()
}

func loadOutbox(ctx context.Context, db db, opts Options) (OutboxSummary, error) {
	var summary OutboxSummary
	err := db.QueryRow(ctx, `
		select coalesce(max(e.seq), 0)::bigint,
		       count(*) filter (where e.published_at is null)::bigint
		from onlava_data.outbox_events e
		left join onlava_data.tenants t on t.id = e.tenant_id
		where ($1::text = '' or t.key = $1)
		  and ($2::text = '' or e.object_name = $2)
	`, strings.TrimSpace(opts.TenantKey), strings.TrimSpace(opts.ObjectName)).Scan(&summary.LatestSeq, &summary.Unpublished)
	if err != nil {
		return summary, fmt.Errorf("inspect data outbox: %w", err)
	}
	return summary, nil
}

func columnNames(data []byte) []string {
	var columns []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &columns); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		if strings.TrimSpace(column.Name) != "" {
			out = append(out, column.Name)
		}
	}
	return out
}

func relationSummary(fieldType, relationObject string, settingsJSON []byte) *RelationSummary {
	if fieldType != string(objectstore.FieldRelation) {
		return nil
	}
	var settings map[string]any
	_ = json.Unmarshal(settingsJSON, &settings)
	kind := stringSetting(settings, "relation_kind")
	if kind == "" {
		kind = string(objectstore.RelationManyToOne)
	}
	onDelete := stringSetting(settings, "on_delete")
	if onDelete == "" {
		onDelete = string(objectstore.RelationDeleteRestrict)
	}
	return &RelationSummary{
		Object:        relationObject,
		Kind:          kind,
		InverseField:  stringSetting(settings, "inverse_field"),
		OnDelete:      onDelete,
		JoinTableName: stringSetting(settings, "join_table_name"),
	}
}

func rawJSONOrNil(data []byte) json.RawMessage {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return json.RawMessage(trimmed)
}

func stringSetting(settings map[string]any, key string) string {
	if settings == nil {
		return ""
	}
	if value, ok := settings[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func stringArray(data []byte) []string {
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil
	}
	return values
}

var _ db = (*pgxpool.Pool)(nil)
