package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) CreateObject(ctx context.Context, actor Actor, req CreateObjectRequest) (*Object, error) {
	if err := validateName("object", req.NameSingular); err != nil {
		return nil, err
	}
	if req.NamePlural == "" {
		req.NamePlural = req.NameSingular + "s"
	}
	if err := validateName("object plural", req.NamePlural); err != nil {
		return nil, err
	}
	tenant, err := s.EnsureTenant(ctx, req.TenantKey, req.TenantName)
	if err != nil {
		return nil, err
	}
	if existing, err := s.loadObjectIfExists(ctx, tenant.ID, req.NameSingular); err != nil {
		return nil, err
	} else if existing != nil {
		if err := s.perms.CanWriteObject(ctx, actor, ObjectRef{TenantID: tenant.ID, TenantKey: tenant.Key, ObjectID: existing.ID, Name: existing.NameSingular}); err != nil {
			return nil, err
		}
		if err := objectMatchesRequest(existing, req); err != nil {
			return nil, err
		}
		if err := s.verifyObjectTable(ctx, s.db, existing.TableName); err != nil {
			return nil, fmt.Errorf("object %s exists but physical schema drift was detected: %w", existing.NameSingular, err)
		}
		return existing, nil
	}
	objectID, err := newUUID()
	if err != nil {
		return nil, err
	}
	tableName := physicalTableName(objectID, req.NameSingular)
	obj := &Object{
		ID:                    objectID,
		TenantID:              tenant.ID,
		NameSingular:          req.NameSingular,
		NamePlural:            req.NamePlural,
		TableName:             tableName,
		LabelSingular:         firstNonEmpty(req.LabelSingular, defaultLabel(req.NameSingular)),
		LabelPlural:           firstNonEmpty(req.LabelPlural, defaultLabel(req.NamePlural)),
		IsCustom:              true,
		IsSystem:              false,
		SchemaVersion:         1,
		OutboxTriggersEnabled: false,
		CreatedAt:             s.now(),
		UpdatedAt:             s.now(),
	}
	if err := s.perms.CanWriteObject(ctx, actor, ObjectRef{TenantID: tenant.ID, TenantKey: tenant.Key, ObjectID: obj.ID, Name: obj.NameSingular}); err != nil {
		return nil, err
	}

	ddl := []string{createObjectTableDDL(obj.TableName)}
	migrationID, err := newMigrationID(ddl)
	if err != nil {
		return nil, err
	}
	var event *Event
	if err := s.withMigrationTx(ctx, tenant.Key, tenant.ID, obj.ID, migrationID, 0, 1, ddl, "", func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, ddl[0]); err != nil {
			return fmt.Errorf("create object table %s: %w", obj.TableName, err)
		}
		if _, err := tx.Exec(ctx, `
			insert into `+qualifiedIdent(MetadataSchema, "objects")+` (
				id, tenant_id, name_singular, name_plural, table_name,
				label_singular, label_plural, is_custom, is_system, schema_version,
				created_at, updated_at
			) values ($1, $2, $3, $4, $5, $6, $7, true, false, 1, $8, $8)
		`, obj.ID, obj.TenantID, obj.NameSingular, obj.NamePlural, obj.TableName, obj.LabelSingular, obj.LabelPlural, obj.CreatedAt); err != nil {
			return fmt.Errorf("insert object metadata %s: %w", obj.NameSingular, err)
		}
		if err := s.verifyObjectTable(ctx, tx, obj.TableName); err != nil {
			return err
		}
		var outboxErr error
		event, outboxErr = s.insertOutbox(ctx, tx, outboxDraft{
			TenantID:      tenant.ID,
			ObjectID:      obj.ID,
			ObjectName:    obj.NameSingular,
			Action:        "object.created",
			ActorID:       actor.ID,
			SchemaVersion: obj.SchemaVersion,
			ChangedFields: []string{"object"},
			After: Record{
				"id":            obj.ID,
				"name_singular": obj.NameSingular,
				"table_name":    obj.TableName,
			},
		})
		return outboxErr
	}); err != nil {
		if isUniqueViolation(err) {
			if existing, loadErr := s.loadObjectIfExists(ctx, tenant.ID, req.NameSingular); loadErr == nil && existing != nil {
				if permErr := s.perms.CanWriteObject(ctx, actor, ObjectRef{TenantID: tenant.ID, TenantKey: tenant.Key, ObjectID: existing.ID, Name: existing.NameSingular}); permErr == nil && objectMatchesRequest(existing, req) == nil {
					if verifyErr := s.verifyObjectTable(ctx, s.db, existing.TableName); verifyErr == nil {
						_ = s.finishMigration(ctx, migrationID, "skipped", "object already exists")
						return existing, nil
					}
				}
			}
		}
		_ = s.finishMigration(ctx, migrationID, "failed", err.Error())
		return nil, err
	}
	if err := s.finishMigration(ctx, migrationID, "applied", ""); err != nil {
		return nil, err
	}
	s.router.publish(event)
	return obj, nil
}

func (s *Store) CreateField(ctx context.Context, actor Actor, objectName string, req CreateFieldRequest) (*Field, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	if err := validateName("field", req.Name); err != nil {
		return nil, err
	}
	fieldType, err := normalizeFieldType(req.Type)
	if err != nil {
		return nil, err
	}
	nullable := true
	if req.Nullable != nil {
		nullable = *req.Nullable
	}
	if _, exists := state.Fields[req.Name]; exists {
		existing := state.Fields[req.Name]
		if err := s.perms.CanWriteField(ctx, actor, fieldRef(state, existing)); err != nil {
			return nil, err
		}
		searchable, searchWeight, err := searchConfig(fieldType, req)
		if err != nil {
			return nil, err
		}
		settings, relationObjectID, _, err := s.fieldSettings(ctx, state, existing.ID, fieldType, nullable, req)
		if err != nil {
			return nil, err
		}
		if err := fieldMatchesRequest(existing, req, fieldType, settings, relationObjectID, searchable, searchWeight); err != nil {
			return nil, err
		}
		if err := s.verifyFieldColumns(ctx, s.db, state.Object.TableName, existing.Columns); err != nil {
			return nil, fmt.Errorf("field %s.%s exists but physical schema drift was detected: %w", objectName, existing.Name, err)
		}
		if err := s.verifyRelationField(ctx, s.db, state.Object.TableName, existing); err != nil {
			return nil, fmt.Errorf("field %s.%s exists but relationship schema drift was detected: %w", objectName, existing.Name, err)
		}
		return existing, nil
	}
	fieldID, err := newUUID()
	if err != nil {
		return nil, err
	}
	columns, err := fieldColumns(req.Name, fieldID, fieldType, nullable)
	if err != nil {
		return nil, err
	}
	searchable, searchWeight, err := searchConfig(fieldType, req)
	if err != nil {
		return nil, err
	}
	settings, relationObjectID, relation, err := s.fieldSettings(ctx, state, fieldID, fieldType, nullable, req)
	if err != nil {
		return nil, err
	}
	if relation != nil && relation.Kind == RelationManyToMany {
		columns = nil
	}
	field := &Field{
		ID:               fieldID,
		TenantID:         state.Tenant.ID,
		ObjectID:         state.Object.ID,
		Name:             req.Name,
		Label:            firstNonEmpty(req.Label, defaultLabel(req.Name)),
		Type:             fieldType,
		IsCustom:         true,
		IsSystem:         false,
		IsNullable:       nullable,
		IsUnique:         req.Unique,
		IsArray:          req.Array,
		IsSearchable:     searchable,
		SearchWeight:     searchWeight,
		RelationObjectID: relationObjectID,
		Settings:         settings,
		Columns:          columns,
		CreatedAt:        s.now(),
		UpdatedAt:        s.now(),
	}
	if err := s.perms.CanWriteField(ctx, actor, fieldRef(state, field)); err != nil {
		return nil, err
	}
	if err := validateFieldOptions(field, req.Options); err != nil {
		return nil, err
	}

	fromVersion := state.Object.SchemaVersion
	toVersion := fromVersion + 1
	ddl := addFieldDDL(state.Object.TableName, field)
	relationDDL, err := relationFieldDDL(state.Object.TableName, field, relation)
	if err != nil {
		return nil, err
	}
	ddl = append(ddl, relationDDL...)
	migrationID, err := newMigrationID(ddl)
	if err != nil {
		return nil, err
	}
	var event *Event
	if err := s.withMigrationTx(ctx, state.Tenant.Key, state.Tenant.ID, state.Object.ID, migrationID, fromVersion, toVersion, ddl, "", func(tx pgxTx) error {
		for _, stmt := range ddl {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("apply field migration %s.%s: %w", state.Object.NameSingular, field.Name, err)
			}
		}
		columnsData, err := json.Marshal(field.Columns)
		if err != nil {
			return err
		}
		settingsData, err := json.Marshal(field.Settings)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into `+qualifiedIdent(MetadataSchema, "fields")+` (
				id, tenant_id, object_id, name, label, type, is_custom, is_system,
				is_nullable, is_unique, is_array, is_searchable, search_weight,
				relation_object_id, settings,
				storage_columns, created_at, updated_at
			) values ($1, $2, $3, $4, $5, $6, true, false, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15)
		`, field.ID, field.TenantID, field.ObjectID, field.Name, field.Label, string(field.Type), field.IsNullable, field.IsUnique, field.IsArray, field.IsSearchable, field.SearchWeight, nullableUUID(field.RelationObjectID), string(settingsData), string(columnsData), field.CreatedAt); err != nil {
			return fmt.Errorf("insert field metadata %s.%s: %w", state.Object.NameSingular, field.Name, err)
		}
		for index, optionReq := range req.Options {
			option, err := buildFieldOption(state.Tenant.ID, field.ID, optionReq, index)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				insert into `+qualifiedIdent(MetadataSchema, "field_options")+` (
					id, tenant_id, field_id, value, label, color, position, is_archived
				) values ($1, $2, $3, $4, $5, $6, $7, false)
			`, option.ID, option.TenantID, option.FieldID, option.Value, option.Label, option.Color, option.Position); err != nil {
				return fmt.Errorf("insert field option %s.%s=%s: %w", state.Object.NameSingular, field.Name, option.Value, err)
			}
			field.Options = append(field.Options, option)
		}
		toVersion, err = s.bumpObjectSchemaVersion(ctx, tx, state.Object.ID)
		if err != nil {
			return err
		}
		if err := s.verifyFieldColumns(ctx, tx, state.Object.TableName, field.Columns); err != nil {
			return err
		}
		if err := s.verifyRelationField(ctx, tx, state.Object.TableName, field); err != nil {
			return err
		}
		var outboxErr error
		event, outboxErr = s.insertOutbox(ctx, tx, outboxDraft{
			TenantID:      state.Tenant.ID,
			ObjectID:      state.Object.ID,
			ObjectName:    state.Object.NameSingular,
			Action:        "field.created",
			ActorID:       actor.ID,
			SchemaVersion: toVersion,
			ChangedFields: []string{field.Name},
			After: Record{
				"id":      field.ID,
				"name":    field.Name,
				"type":    field.Type,
				"columns": field.Columns,
			},
		})
		return outboxErr
	}); err != nil {
		if isUniqueViolation(err) {
			if freshState, loadErr := s.loadState(ctx, req.TenantKey, objectName); loadErr == nil {
				if existing := freshState.Fields[req.Name]; existing != nil {
					searchable, searchWeight, searchErr := searchConfig(fieldType, req)
					settings, relationObjectID, _, settingsErr := s.fieldSettings(ctx, freshState, existing.ID, fieldType, nullable, req)
					if permErr := s.perms.CanWriteField(ctx, actor, fieldRef(freshState, existing)); searchErr == nil && settingsErr == nil && permErr == nil && fieldMatchesRequest(existing, req, fieldType, settings, relationObjectID, searchable, searchWeight) == nil {
						if verifyErr := s.verifyFieldColumns(ctx, s.db, freshState.Object.TableName, existing.Columns); verifyErr == nil && s.verifyRelationField(ctx, s.db, freshState.Object.TableName, existing) == nil {
							_ = s.finishMigration(ctx, migrationID, "skipped", "field already exists")
							return existing, nil
						}
					}
				}
			}
		}
		_ = s.finishMigration(ctx, migrationID, "failed", err.Error())
		return nil, err
	}
	if err := s.finishMigration(ctx, migrationID, "applied", ""); err != nil {
		return nil, err
	}
	s.router.publish(event)
	field.ObjectID = state.Object.ID
	return field, nil
}

type pgxTx interface {
	Queryer
	Commit(context.Context) error
	Rollback(context.Context) error
}

const (
	metadataBootstrapLockName     = "metadata-bootstrap"
	sharedTriggerFunctionLockName = "shared-trigger-function"
	tenantSchemaMigrationLockName = "tenant-schema-migration"
	tenantRecordSchemaLockName    = "tenant-record-schema"
	objectSchemaMigrationLockName = "object-schema-migration"
)

func (s *Store) withMigrationTx(ctx context.Context, tenantKey, tenantID, objectID, migrationID string, fromVersion, toVersion int64, ddl []string, sharedLockName string, fn func(pgxTx) error) error {
	for attempt := 0; ; attempt++ {
		err := s.withMigrationTxOnce(ctx, tenantKey, tenantID, objectID, migrationID, fromVersion, toVersion, ddl, sharedLockName, fn)
		if !isDeadlockDetected(err) {
			return err
		}
		if retryErr := waitBeforeAdvisoryLockRetry(ctx, attempt); retryErr != nil {
			return err
		}
	}
}

func (s *Store) withMigrationTxOnce(ctx context.Context, tenantKey, tenantID, objectID, migrationID string, fromVersion, toVersion int64, ddl []string, sharedLockName string, fn func(pgxTx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockMetadataBootstrapRead(ctx, tx); err != nil {
		return err
	}
	if sharedLockName != "" {
		if err := lockSharedSchemaMigration(ctx, tx, sharedLockName); err != nil {
			return err
		}
	}
	if err := lockTenantSchemaMigration(ctx, tx, tenantKey); err != nil {
		return err
	}
	if err := lockRecordSchemaWrite(ctx, tx, tenantKey); err != nil {
		return err
	}
	if err := lockObjectMigration(ctx, tx, tenantID, objectID); err != nil {
		return err
	}
	if objectID != "" && fromVersion > 0 {
		currentVersion, err := loadObjectSchemaVersion(ctx, tx, tenantID, objectID)
		if err != nil {
			return err
		}
		fromVersion = currentVersion
		toVersion = currentVersion + 1
	}
	if err := s.insertMigration(ctx, tx, migrationID, tenantID, objectID, fromVersion, toVersion, ddl); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `savepoint onlava_objectstore_migration`); err != nil {
		return fmt.Errorf("create migration savepoint: %w", err)
	}
	if err := fn(tx); err != nil {
		if isDeadlockDetected(err) {
			return err
		}
		if _, rollbackErr := tx.Exec(ctx, `rollback to savepoint onlava_objectstore_migration`); rollbackErr != nil {
			return fmt.Errorf("%w; rollback migration savepoint: %v", err, rollbackErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("%w; commit migration marker: %v", err, commitErr)
		}
		return err
	}
	if _, err := tx.Exec(ctx, `release savepoint onlava_objectstore_migration`); err != nil {
		return fmt.Errorf("release migration savepoint: %w", err)
	}
	return tx.Commit(ctx)
}

func isDeadlockDetected(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func waitBeforeAdvisoryLockRetry(ctx context.Context, attempt int) error {
	const maxAttempts = 32
	if attempt >= maxAttempts {
		return fmt.Errorf("advisory lock retry attempts exhausted")
	}
	delay := time.Duration(attempt+1) * 5 * time.Millisecond
	if delay > 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// DDL paths and tenant upserts acquire locks in this order:
// 1. shared metadata-bootstrap lock, 2. optional shared-schema lock,
// 3. tenant schema lock, 4. exclusive tenant record-schema barrier,
// 5. object lock.
// Metadata bootstrap takes the bootstrap lock exclusively. Record writes take the
// same bootstrap lock in shared mode, then the tenant record-schema barrier in
// shared mode so writes and DDL in other tenants are not blocked by tenant-local
// DDL.
func lockMetadataBootstrapWrite(ctx context.Context, q Queryer) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock($1)`, advisoryLockKey("objectstore", metadataBootstrapLockName)); err != nil {
		return fmt.Errorf("lock metadata bootstrap: %w", err)
	}
	return nil
}

func lockMetadataBootstrapRead(ctx context.Context, q Queryer) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, advisoryLockKey("objectstore", metadataBootstrapLockName)); err != nil {
		return fmt.Errorf("lock metadata bootstrap read: %w", err)
	}
	return nil
}

func lockTenantSchemaMigration(ctx context.Context, q Queryer, tenantKey string) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock($1)`, advisoryLockKey("objectstore", tenantSchemaMigrationLockName, tenantKey)); err != nil {
		return fmt.Errorf("lock tenant schema migration: %w", err)
	}
	return nil
}

func lockSharedSchemaMigration(ctx context.Context, q Queryer, name string) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock($1)`, advisoryLockKey("objectstore", "shared-schema-migration", name)); err != nil {
		return fmt.Errorf("lock shared schema migration: %w", err)
	}
	return nil
}

func lockRecordSchemaRead(ctx context.Context, q Queryer, tenantKey string) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, advisoryLockKey("objectstore", tenantRecordSchemaLockName, tenantKey)); err != nil {
		return fmt.Errorf("lock record schema read: %w", err)
	}
	return nil
}

func lockRecordSchemaWrite(ctx context.Context, q Queryer, tenantKey string) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock($1)`, advisoryLockKey("objectstore", tenantRecordSchemaLockName, tenantKey)); err != nil {
		return fmt.Errorf("lock record schema write: %w", err)
	}
	return nil
}

func lockObjectMigration(ctx context.Context, q Queryer, tenantID, objectID string) error {
	if _, err := q.Exec(ctx, `select pg_advisory_xact_lock($1)`, advisoryLockKey("objectstore", objectSchemaMigrationLockName, tenantID, objectID)); err != nil {
		return fmt.Errorf("lock object migration: %w", err)
	}
	return nil
}

func loadObjectSchemaVersion(ctx context.Context, q Queryer, tenantID, objectID string) (int64, error) {
	var version int64
	err := q.QueryRow(ctx, `
		select schema_version
		from `+qualifiedIdent(MetadataSchema, "objects")+`
		where tenant_id = $1 and id = $2
	`, tenantID, objectID).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("load object schema version: %w", err)
	}
	return version, nil
}

func (s *Store) bumpObjectSchemaVersion(ctx context.Context, q Queryer, objectID string) (int64, error) {
	var version int64
	err := q.QueryRow(ctx, `
		update `+qualifiedIdent(MetadataSchema, "objects")+`
		set schema_version = schema_version + 1, updated_at = $1
		where id = $2
		returning schema_version
	`, s.now(), objectID).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("bump object schema version: %w", err)
	}
	return version, nil
}

func newMigrationID(ddl []string) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if _, err := json.Marshal(ddl); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) insertMigration(ctx context.Context, q Queryer, migrationID, tenantID, objectID string, fromVersion, toVersion int64, ddl []string) error {
	ddlData, err := json.Marshal(ddl)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		insert into `+qualifiedIdent(MetadataSchema, "schema_migrations")+` (
			id, tenant_id, object_id, from_version, to_version, status, ddl, started_at
		) values ($1, $2, $3, $4, $5, 'running', $6, $7)
	`, migrationID, tenantID, nullableUUID(objectID), fromVersion, toVersion, string(ddlData), s.now())
	if err != nil {
		return fmt.Errorf("start schema migration: %w", err)
	}
	return nil
}

func (s *Store) finishMigration(ctx context.Context, migrationID, status, message string) error {
	_, err := s.db.Exec(ctx, `
		update `+qualifiedIdent(MetadataSchema, "schema_migrations")+`
		set status = $1, finished_at = $2, error = $3
		where id = $4
	`, status, s.now(), message, migrationID)
	return err
}

func createObjectTableDDL(tableName string) string {
	return `create table ` + qualifiedIdent(RecordsSchema, tableName) + ` (
		id uuid primary key,
		tenant_id uuid not null,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		deleted_at timestamptz null
	)`
}

func addFieldDDL(tableName string, field *Field) []string {
	var ddl []string
	for _, column := range field.Columns {
		stmt := `alter table ` + qualifiedIdent(RecordsSchema, tableName) + ` add column ` + quoteIdent(column.Name) + ` ` + column.SQLType
		if !column.Nullable {
			stmt += ` not null`
		}
		ddl = append(ddl, stmt)
	}
	if field.IsUnique && len(field.Columns) == 1 {
		constraintName := safeColumnName("uniq_"+tableName, field.Columns[0].Name)
		ddl = append(ddl, `alter table `+qualifiedIdent(RecordsSchema, tableName)+` add constraint `+quoteIdent(constraintName)+` unique (`+quoteIdent(field.Columns[0].Name)+`)`)
	}
	return ddl
}

func (s *Store) verifyObjectTable(ctx context.Context, q Queryer, tableName string) error {
	var exists bool
	err := q.QueryRow(ctx, `
		select exists (
			select 1 from information_schema.tables
			where table_schema = $1 and table_name = $2
		)
	`, RecordsSchema, tableName).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("physical table %s.%s was not created", RecordsSchema, tableName)
	}
	required := []PhysicalColumn{
		{Name: "id", SQLType: "uuid"},
		{Name: "tenant_id", SQLType: "uuid"},
		{Name: "created_at", SQLType: "timestamp with time zone"},
		{Name: "updated_at", SQLType: "timestamp with time zone"},
	}
	return s.verifyFieldColumns(ctx, q, tableName, required)
}

func (s *Store) verifyFieldColumns(ctx context.Context, q Queryer, tableName string, columns []PhysicalColumn) error {
	rows, err := q.Query(ctx, `
		select column_name
		from information_schema.columns
		where table_schema = $1 and table_name = $2
	`, RecordsSchema, tableName)
	if err != nil {
		return err
	}
	defer rows.Close()
	actual := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		actual[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range columns {
		if !actual[column.Name] {
			return fmt.Errorf("physical column %s.%s.%s was not created", RecordsSchema, tableName, column.Name)
		}
	}
	return nil
}

func validateFieldOptions(field *Field, options []FieldOptionRequest) error {
	switch field.Type {
	case FieldSelect, FieldMultiSelect:
	default:
		if len(options) > 0 {
			return fmt.Errorf("field options are only supported for select and multi_select fields")
		}
	}
	seen := map[string]bool{}
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		if value == "" {
			return fmt.Errorf("field option value is required")
		}
		if seen[value] {
			return fmt.Errorf("duplicate field option value %q", value)
		}
		seen[value] = true
	}
	return nil
}

func buildFieldOption(tenantID, fieldID string, req FieldOptionRequest, index int) (FieldOption, error) {
	id, err := newUUID()
	if err != nil {
		return FieldOption{}, err
	}
	value := strings.TrimSpace(req.Value)
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = defaultLabel(value)
	}
	return FieldOption{
		ID:       id,
		TenantID: tenantID,
		FieldID:  fieldID,
		Value:    value,
		Label:    label,
		Color:    strings.TrimSpace(req.Color),
		Position: index,
	}, nil
}

func (s *Store) loadObjectIfExists(ctx context.Context, tenantID, objectName string) (*Object, error) {
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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load data object %q: %w", objectName, err)
	}
	return &obj, nil
}

func objectMatchesRequest(existing *Object, req CreateObjectRequest) error {
	if existing == nil {
		return fmt.Errorf("object metadata is missing")
	}
	labelSingular := firstNonEmpty(req.LabelSingular, defaultLabel(req.NameSingular))
	labelPlural := firstNonEmpty(req.LabelPlural, defaultLabel(req.NamePlural))
	if existing.NamePlural != req.NamePlural {
		return fmt.Errorf("object %s already exists with plural %q, not %q", existing.NameSingular, existing.NamePlural, req.NamePlural)
	}
	if existing.LabelSingular != labelSingular || existing.LabelPlural != labelPlural {
		return fmt.Errorf("object %s already exists with different labels", existing.NameSingular)
	}
	return nil
}

func fieldMatchesRequest(existing *Field, req CreateFieldRequest, fieldType FieldType, settings map[string]any, relationObjectID string, searchable bool, searchWeight string) error {
	if existing == nil {
		return fmt.Errorf("field metadata is missing")
	}
	nullable := true
	if req.Nullable != nil {
		nullable = *req.Nullable
	}
	label := firstNonEmpty(req.Label, defaultLabel(req.Name))
	switch {
	case existing.Type != fieldType:
		return fmt.Errorf("field %s already exists with type %s, not %s", existing.Name, existing.Type, fieldType)
	case existing.Label != label:
		return fmt.Errorf("field %s already exists with different label", existing.Name)
	case existing.IsNullable != nullable:
		return fmt.Errorf("field %s already exists with nullable=%v, not %v", existing.Name, existing.IsNullable, nullable)
	case existing.IsUnique != req.Unique:
		return fmt.Errorf("field %s already exists with unique=%v, not %v", existing.Name, existing.IsUnique, req.Unique)
	case existing.IsArray != req.Array:
		return fmt.Errorf("field %s already exists with array=%v, not %v", existing.Name, existing.IsArray, req.Array)
	case existing.IsSearchable != searchable:
		return fmt.Errorf("field %s already exists with searchable=%v, not %v", existing.Name, existing.IsSearchable, searchable)
	case existing.SearchWeight != searchWeight:
		return fmt.Errorf("field %s already exists with search_weight=%s, not %s", existing.Name, existing.SearchWeight, searchWeight)
	case existing.RelationObjectID != relationObjectID:
		return fmt.Errorf("field %s already exists with different relation object", existing.Name)
	case !jsonEqual(existing.Settings, settings):
		return fmt.Errorf("field %s already exists with different settings", existing.Name)
	case !fieldOptionsMatch(existing.Options, req.Options):
		return fmt.Errorf("field %s already exists with different options", existing.Name)
	}
	return nil
}

func fieldOptionsMatch(existing []FieldOption, requested []FieldOptionRequest) bool {
	if len(existing) != len(requested) {
		return false
	}
	for i := range requested {
		value := strings.TrimSpace(requested[i].Value)
		label := strings.TrimSpace(requested[i].Label)
		if label == "" {
			label = defaultLabel(value)
		}
		if existing[i].Value != value || existing[i].Label != label || existing[i].Color != strings.TrimSpace(requested[i].Color) {
			return false
		}
	}
	return true
}

func jsonEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
