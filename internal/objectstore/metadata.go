package objectstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func Open(ctx context.Context, db DB, opts Options) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("object store requires a database")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	perms := opts.Permissions
	if perms == nil {
		perms = AllowAllPermissions{}
	}
	store := &Store{
		db:     db,
		perms:  perms,
		now:    now,
		router: newLiveRouter(),
	}
	if err := store.bootstrap(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) bootstrap(ctx context.Context) error {
	stmts := []string{
		`create schema if not exists ` + quoteIdent(MetadataSchema),
		`create schema if not exists ` + quoteIdent(RecordsSchema),
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "tenants") + ` (
			id uuid primary key,
			key text not null unique,
			name text not null,
			created_at timestamptz not null,
			updated_at timestamptz not null
		)`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "objects") + ` (
			id uuid primary key,
			tenant_id uuid not null references ` + qualifiedIdent(MetadataSchema, "tenants") + `(id) on delete cascade,
			name_singular text not null,
			name_plural text not null,
			table_name text not null,
			label_singular text not null,
			label_plural text not null,
			is_custom boolean not null,
			is_system boolean not null,
			schema_version bigint not null,
			outbox_triggers_enabled boolean not null default false,
			created_at timestamptz not null,
			updated_at timestamptz not null,
			unique (tenant_id, name_singular),
			unique (tenant_id, table_name)
		)`,
		`alter table ` + qualifiedIdent(MetadataSchema, "objects") + ` add column if not exists outbox_triggers_enabled boolean not null default false`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "fields") + ` (
			id uuid primary key,
			tenant_id uuid not null references ` + qualifiedIdent(MetadataSchema, "tenants") + `(id) on delete cascade,
			object_id uuid not null references ` + qualifiedIdent(MetadataSchema, "objects") + `(id) on delete cascade,
			name text not null,
			label text not null,
			type text not null,
			is_custom boolean not null,
			is_system boolean not null,
			is_nullable boolean not null,
			is_unique boolean not null,
			is_array boolean not null,
			relation_object_id uuid null,
			settings jsonb not null default '{}'::jsonb,
			storage_columns jsonb not null default '[]'::jsonb,
			created_at timestamptz not null,
			updated_at timestamptz not null,
			unique (tenant_id, object_id, name)
		)`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "field_options") + ` (
			id uuid primary key,
			tenant_id uuid not null references ` + qualifiedIdent(MetadataSchema, "tenants") + `(id) on delete cascade,
			field_id uuid not null references ` + qualifiedIdent(MetadataSchema, "fields") + `(id) on delete cascade,
			value text not null,
			label text not null,
			color text not null default '',
			position integer not null,
			is_archived boolean not null default false,
			unique (tenant_id, field_id, value)
		)`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "indexes") + ` (
			id uuid primary key,
			tenant_id uuid not null references ` + qualifiedIdent(MetadataSchema, "tenants") + `(id) on delete cascade,
			object_id uuid not null references ` + qualifiedIdent(MetadataSchema, "objects") + `(id) on delete cascade,
			name text not null,
			physical_name text not null,
			method text not null,
			is_unique boolean not null default false,
			is_system boolean not null default false,
			created_at timestamptz not null,
			updated_at timestamptz not null,
			unique (tenant_id, object_id, name),
			unique (tenant_id, object_id, physical_name)
		)`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "index_fields") + ` (
			id uuid primary key,
			tenant_id uuid not null references ` + qualifiedIdent(MetadataSchema, "tenants") + `(id) on delete cascade,
			index_id uuid not null references ` + qualifiedIdent(MetadataSchema, "indexes") + `(id) on delete cascade,
			field_id uuid not null references ` + qualifiedIdent(MetadataSchema, "fields") + `(id) on delete cascade,
			position integer not null,
			direction text not null default 'asc',
			opclass text not null default '',
			expression text not null default '',
			created_at timestamptz not null,
			updated_at timestamptz not null,
			unique (tenant_id, index_id, position)
		)`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "schema_migrations") + ` (
			id uuid primary key,
			tenant_id uuid not null,
			object_id uuid null,
			from_version bigint not null,
			to_version bigint not null,
			status text not null,
			ddl jsonb not null,
			started_at timestamptz not null,
			finished_at timestamptz null,
			error text not null default ''
		)`,
		`create table if not exists ` + qualifiedIdent(MetadataSchema, "outbox_events") + ` (
			seq bigserial primary key,
			id uuid not null unique,
			tenant_id uuid not null,
			object_id uuid null,
			object_name text not null,
			record_id uuid null,
			action text not null,
			actor_id text not null default '',
			schema_version bigint not null,
			changed_fields text[] not null default '{}'::text[],
			before jsonb null,
			after jsonb null,
			diff jsonb null,
			created_at timestamptz not null,
			published_at timestamptz null
		)`,
		`create index if not exists outbox_events_tenant_seq_idx on ` + qualifiedIdent(MetadataSchema, "outbox_events") + ` (tenant_id, seq)`,
		`create index if not exists outbox_events_object_seq_idx on ` + qualifiedIdent(MetadataSchema, "outbox_events") + ` (tenant_id, object_name, seq)`,
		recordChangeTriggerFunctionDDL(),
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("bootstrap data metadata: %w", err)
		}
	}
	return nil
}

func (s *Store) EnsureTenant(ctx context.Context, key, name string) (*Tenant, error) {
	if err := validateName("tenant", key); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultLabel(key)
	}
	now := s.now()
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	var tenant Tenant
	err = s.db.QueryRow(ctx, `
		insert into `+qualifiedIdent(MetadataSchema, "tenants")+` (id, key, name, created_at, updated_at)
		values ($1, $2, $3, $4, $4)
		on conflict (key) do update set name = excluded.name, updated_at = excluded.updated_at
		returning id::text, key, name, created_at, updated_at
	`, id, key, name, now).Scan(&tenant.ID, &tenant.Key, &tenant.Name, &tenant.CreatedAt, &tenant.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("ensure data tenant %q: %w", key, err)
	}
	return &tenant, nil
}

func (s *Store) loadTenant(ctx context.Context, key string) (*Tenant, error) {
	if err := validateName("tenant", key); err != nil {
		return nil, err
	}
	var tenant Tenant
	err := s.db.QueryRow(ctx, `
		select id::text, key, name, created_at, updated_at
		from `+qualifiedIdent(MetadataSchema, "tenants")+`
		where key = $1
	`, key).Scan(&tenant.ID, &tenant.Key, &tenant.Name, &tenant.CreatedAt, &tenant.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("load data tenant %q: %w", key, err)
	}
	return &tenant, nil
}

func (s *Store) loadState(ctx context.Context, tenantKey, objectName string) (*metadataState, error) {
	tenant, err := s.loadTenant(ctx, tenantKey)
	if err != nil {
		return nil, err
	}
	object, err := s.loadObject(ctx, tenant.ID, objectName)
	if err != nil {
		return nil, err
	}
	fields, err := s.loadFields(ctx, tenant.ID, object.ID)
	if err != nil {
		return nil, err
	}
	return &metadataState{
		Tenant: tenant,
		Object: object,
		Fields: fields,
	}, nil
}

func (s *Store) loadObject(ctx context.Context, tenantID, objectName string) (*Object, error) {
	if err := validateName("object", objectName); err != nil {
		return nil, err
	}
	var obj Object
	err := s.db.QueryRow(ctx, `
		select id::text, tenant_id::text, name_singular, name_plural, table_name,
		       label_singular, label_plural, is_custom, is_system, schema_version,
		       outbox_triggers_enabled, created_at, updated_at
		from `+qualifiedIdent(MetadataSchema, "objects")+`
		where tenant_id = $1 and name_singular = $2
	`, tenantID, objectName).Scan(
		&obj.ID, &obj.TenantID, &obj.NameSingular, &obj.NamePlural, &obj.TableName,
		&obj.LabelSingular, &obj.LabelPlural, &obj.IsCustom, &obj.IsSystem, &obj.SchemaVersion,
		&obj.OutboxTriggersEnabled, &obj.CreatedAt, &obj.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load data object %q: %w", objectName, err)
	}
	return &obj, nil
}

func (s *Store) loadFields(ctx context.Context, tenantID, objectID string) (map[string]*Field, error) {
	rows, err := s.db.Query(ctx, `
		select id::text, tenant_id::text, object_id::text, name, label, type,
		       is_custom, is_system, is_nullable, is_unique, is_array,
		       coalesce(relation_object_id::text, ''), settings, storage_columns,
		       created_at, updated_at
		from `+qualifiedIdent(MetadataSchema, "fields")+`
		where tenant_id = $1 and object_id = $2
		order by name
	`, tenantID, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := map[string]*Field{}
	fieldsByID := map[string]*Field{}
	fieldIDs := []string{}
	for rows.Next() {
		var field Field
		var fieldType string
		var settingsData []byte
		var columnsData []byte
		if err := rows.Scan(
			&field.ID, &field.TenantID, &field.ObjectID, &field.Name, &field.Label, &fieldType,
			&field.IsCustom, &field.IsSystem, &field.IsNullable, &field.IsUnique, &field.IsArray,
			&field.RelationObjectID, &settingsData, &columnsData,
			&field.CreatedAt, &field.UpdatedAt,
		); err != nil {
			return nil, err
		}
		field.Type = FieldType(fieldType)
		if len(settingsData) > 0 {
			_ = json.Unmarshal(settingsData, &field.Settings)
		}
		if field.Settings == nil {
			field.Settings = map[string]any{}
		}
		if len(columnsData) > 0 {
			if err := json.Unmarshal(columnsData, &field.Columns); err != nil {
				return nil, fmt.Errorf("decode field %s columns: %w", field.Name, err)
			}
		}
		fields[field.Name] = &field
		fieldsByID[field.ID] = &field
		fieldIDs = append(fieldIDs, field.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, fieldID := range fieldIDs {
		field := fieldsByID[fieldID]
		options, err := s.loadFieldOptions(ctx, tenantID, field.ID)
		if err != nil {
			return nil, err
		}
		field.Options = options
	}
	return fields, nil
}

func (s *Store) loadFieldOptions(ctx context.Context, tenantID, fieldID string) ([]FieldOption, error) {
	rows, err := s.db.Query(ctx, `
		select id::text, tenant_id::text, field_id::text, value, label, color, position, is_archived
		from `+qualifiedIdent(MetadataSchema, "field_options")+`
		where tenant_id = $1 and field_id = $2
		order by position, value
	`, tenantID, fieldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var options []FieldOption
	for rows.Next() {
		var option FieldOption
		if err := rows.Scan(&option.ID, &option.TenantID, &option.FieldID, &option.Value, &option.Label, &option.Color, &option.Position, &option.IsArchived); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}
