package objectstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateIndex(ctx context.Context, actor Actor, objectName string, req CreateIndexRequest) (*Index, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	spec, err := s.buildIndexSpec(ctx, actor, state, req)
	if err != nil {
		return nil, err
	}
	if existing, err := s.loadIndexIfExists(ctx, state.Tenant.ID, state.Object.ID, spec.Name); err != nil {
		return nil, err
	} else if existing != nil {
		if err := indexMatchesRequest(existing, spec); err != nil {
			return nil, err
		}
		if err := s.verifyIndex(ctx, s.db, state.Object.TableName, existing.PhysicalName); err != nil {
			return nil, fmt.Errorf("index %s.%s exists but physical schema drift was detected: %w", objectName, existing.Name, err)
		}
		return existing, nil
	}
	fromVersion := state.Object.SchemaVersion
	toVersion := fromVersion + 1
	ddl, err := createIndexDDL(state.Object.TableName, spec, state.Fields)
	if err != nil {
		return nil, err
	}
	migrationID, err := s.startMigration(ctx, state.Tenant.ID, state.Object.ID, fromVersion, toVersion, []string{ddl})
	if err != nil {
		return nil, err
	}
	var event *Event
	if err := s.withMigrationTx(ctx, state.Tenant.ID, state.Object.ID, migrationID, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("create index %s on object %s: %w", spec.Name, state.Object.NameSingular, err)
		}
		if _, err := tx.Exec(ctx, `
			insert into `+qualifiedIdent(MetadataSchema, "indexes")+` (
				id, tenant_id, object_id, name, physical_name, method, is_unique, is_system, created_at, updated_at
			) values ($1, $2, $3, $4, $5, $6, $7, false, $8, $8)
		`, spec.ID, spec.TenantID, spec.ObjectID, spec.Name, spec.PhysicalName, string(spec.Method), spec.IsUnique, spec.CreatedAt); err != nil {
			return fmt.Errorf("insert index metadata %s.%s: %w", state.Object.NameSingular, spec.Name, err)
		}
		for _, field := range spec.Fields {
			id, err := newUUID()
			if err != nil {
				return err
			}
			direction := normalizedIndexDirection(field)
			if _, err := tx.Exec(ctx, `
				insert into `+qualifiedIdent(MetadataSchema, "index_fields")+` (
					id, tenant_id, index_id, field_id, position, direction, opclass, expression, created_at, updated_at
				) values ($1, $2, $3, $4, $5, $6, $7, '', $8, $8)
			`, id, spec.TenantID, spec.ID, field.FieldID, field.Position, direction, field.OpClass, spec.CreatedAt); err != nil {
				return fmt.Errorf("insert index field metadata %s.%s[%d]: %w", state.Object.NameSingular, spec.Name, field.Position, err)
			}
		}
		if _, err := tx.Exec(ctx, `
			update `+qualifiedIdent(MetadataSchema, "objects")+`
			set schema_version = $1, updated_at = $2
			where id = $3
		`, toVersion, s.now(), state.Object.ID); err != nil {
			return err
		}
		if err := s.verifyIndex(ctx, tx, state.Object.TableName, spec.PhysicalName); err != nil {
			return err
		}
		var outboxErr error
		event, outboxErr = s.insertOutbox(ctx, tx, outboxDraft{
			TenantID:      state.Tenant.ID,
			ObjectID:      state.Object.ID,
			ObjectName:    state.Object.NameSingular,
			Action:        "index.created",
			ActorID:       actor.ID,
			SchemaVersion: toVersion,
			ChangedFields: []string{"index"},
			After: Record{
				"id":            spec.ID,
				"name":          spec.Name,
				"physical_name": spec.PhysicalName,
				"method":        spec.Method,
			},
		})
		return outboxErr
	}); err != nil {
		if isUniqueViolation(err) {
			if existing, loadErr := s.loadIndexIfExists(ctx, state.Tenant.ID, state.Object.ID, spec.Name); loadErr == nil && existing != nil && indexMatchesRequest(existing, spec) == nil {
				if verifyErr := s.verifyIndex(ctx, s.db, state.Object.TableName, existing.PhysicalName); verifyErr == nil {
					_ = s.finishMigration(ctx, migrationID, "skipped", "index already exists")
					return existing, nil
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
	return spec, nil
}

func (s *Store) ListIndexes(ctx context.Context, actor Actor, objectName string, req ListIndexesRequest) ([]Index, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanReadObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	return s.loadIndexes(ctx, state.Tenant.ID, state.Object.ID)
}

func (s *Store) buildIndexSpec(ctx context.Context, actor Actor, state *metadataState, req CreateIndexRequest) (*Index, error) {
	if err := validateName("index", req.Name); err != nil {
		return nil, err
	}
	if len(req.Fields) == 0 {
		return nil, fmt.Errorf("index %s requires at least one field", req.Name)
	}
	method := req.Method
	if method == "" {
		method = IndexMethodBTree
	}
	switch method {
	case IndexMethodBTree, IndexMethodGIN:
	default:
		return nil, fmt.Errorf("index method %q is not supported", method)
	}
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	index := &Index{
		ID:           id,
		TenantID:     state.Tenant.ID,
		ObjectID:     state.Object.ID,
		Name:         req.Name,
		PhysicalName: physicalIndexName(id, req.Name),
		Method:       method,
		IsUnique:     req.Unique,
		IsSystem:     false,
		CreatedAt:    s.now(),
		UpdatedAt:    s.now(),
	}
	seen := map[string]bool{}
	for pos, raw := range req.Fields {
		name := strings.TrimSpace(raw.Field)
		if name == "" {
			return nil, fmt.Errorf("index %s field %d is missing a field name", req.Name, pos)
		}
		if seen[name] {
			return nil, fmt.Errorf("index %s repeats field %q", req.Name, name)
		}
		field := state.Fields[name]
		if field == nil {
			return nil, fmt.Errorf("index field %q does not exist on object %s", name, state.Object.NameSingular)
		}
		if err := s.perms.CanWriteField(ctx, actor, fieldRef(state, field)); err != nil {
			return nil, err
		}
		if err := validateIndexField(method, field, raw); err != nil {
			return nil, err
		}
		seen[name] = true
		item := IndexField{
			Field:     field.Name,
			FieldID:   field.ID,
			Position:  pos,
			Desc:      raw.Desc,
			Direction: normalizedIndexDirection(raw),
			OpClass:   strings.TrimSpace(raw.OpClass),
		}
		index.Fields = append(index.Fields, item)
	}
	if method == IndexMethodGIN && len(index.Fields) != 1 {
		return nil, fmt.Errorf("gin indexes support exactly one field in this version")
	}
	return index, nil
}

func validateIndexField(method IndexMethod, field *Field, spec IndexField) error {
	if isCompositeField(field.Type) || len(field.Columns) != 1 {
		return fmt.Errorf("field %s is composite and cannot be indexed in this version", field.Name)
	}
	switch method {
	case IndexMethodBTree:
		switch field.Type {
		case FieldJSON, FieldRawJSON, FieldFiles, FieldEmails, FieldPhones, FieldMultiSelect:
			return fmt.Errorf("field %s of type %s requires an explicit gin index", field.Name, field.Type)
		default:
			return nil
		}
	case IndexMethodGIN:
		if spec.Desc {
			return fmt.Errorf("gin index field %s cannot specify descending order", field.Name)
		}
		switch field.Type {
		case FieldMultiSelect, FieldJSON, FieldRawJSON:
			return nil
		default:
			return fmt.Errorf("field %s of type %s is not supported for gin indexes", field.Name, field.Type)
		}
	default:
		return fmt.Errorf("index method %q is not supported", method)
	}
}

func createIndexDDL(tableName string, index *Index, fields map[string]*Field) (string, error) {
	parts := make([]string, 0, len(index.Fields))
	for _, item := range index.Fields {
		field := fields[item.Field]
		if field == nil || len(field.Columns) != 1 {
			return "", fmt.Errorf("index field %q is not resolvable", item.Field)
		}
		part := quoteIdent(field.Columns[0].Name)
		if index.Method == IndexMethodBTree {
			part += " " + normalizedIndexDirection(item)
		}
		if strings.TrimSpace(item.OpClass) != "" {
			part += " " + strings.TrimSpace(item.OpClass)
		}
		parts = append(parts, part)
	}
	unique := ""
	if index.IsUnique {
		unique = "unique "
	}
	return `create ` + unique + `index ` + quoteIdent(index.PhysicalName) +
		` on ` + qualifiedIdent(RecordsSchema, tableName) +
		` using ` + string(index.Method) +
		` (` + strings.Join(parts, ", ") + `)`, nil
}

func (s *Store) verifyIndex(ctx context.Context, q Queryer, tableName, physicalName string) error {
	var exists bool
	err := q.QueryRow(ctx, `
		select exists (
			select 1
			from pg_index i
			join pg_class idx on idx.oid = i.indexrelid
			join pg_class tbl on tbl.oid = i.indrelid
			join pg_namespace n on n.oid = tbl.relnamespace
			where n.nspname = $1
			  and tbl.relname = $2
			  and idx.relname = $3
		)
	`, RecordsSchema, tableName, physicalName).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("physical index %s.%s on table %s was not created", RecordsSchema, physicalName, tableName)
	}
	return nil
}

func (s *Store) loadIndexIfExists(ctx context.Context, tenantID, objectID, name string) (*Index, error) {
	var index Index
	var method string
	err := s.db.QueryRow(ctx, `
		select id::text, tenant_id::text, object_id::text, name, physical_name, method, is_unique, is_system, created_at, updated_at
		from `+qualifiedIdent(MetadataSchema, "indexes")+`
		where tenant_id = $1 and object_id = $2 and name = $3
	`, tenantID, objectID, name).Scan(
		&index.ID, &index.TenantID, &index.ObjectID, &index.Name, &index.PhysicalName, &method, &index.IsUnique, &index.IsSystem, &index.CreatedAt, &index.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load index %q: %w", name, err)
	}
	index.Method = IndexMethod(method)
	fields, err := s.loadIndexFields(ctx, tenantID, index.ID)
	if err != nil {
		return nil, err
	}
	index.Fields = fields
	return &index, nil
}

func (s *Store) loadIndexes(ctx context.Context, tenantID, objectID string) ([]Index, error) {
	rows, err := s.db.Query(ctx, `
		select id::text, tenant_id::text, object_id::text, name, physical_name, method, is_unique, is_system, created_at, updated_at
		from `+qualifiedIdent(MetadataSchema, "indexes")+`
		where tenant_id = $1 and object_id = $2
		order by name
	`, tenantID, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var indexes []Index
	for rows.Next() {
		var index Index
		var method string
		if err := rows.Scan(&index.ID, &index.TenantID, &index.ObjectID, &index.Name, &index.PhysicalName, &method, &index.IsUnique, &index.IsSystem, &index.CreatedAt, &index.UpdatedAt); err != nil {
			return nil, err
		}
		index.Method = IndexMethod(method)
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range indexes {
		fields, err := s.loadIndexFields(ctx, tenantID, indexes[i].ID)
		if err != nil {
			return nil, err
		}
		indexes[i].Fields = fields
	}
	return indexes, nil
}

func (s *Store) loadIndexFields(ctx context.Context, tenantID, indexID string) ([]IndexField, error) {
	rows, err := s.db.Query(ctx, `
		select f.name, f.id::text, ix.position, ix.direction, ix.opclass
		from `+qualifiedIdent(MetadataSchema, "index_fields")+` ix
		join `+qualifiedIdent(MetadataSchema, "fields")+` f on f.id = ix.field_id
		where ix.tenant_id = $1 and ix.index_id = $2
		order by ix.position
	`, tenantID, indexID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fields []IndexField
	for rows.Next() {
		var item IndexField
		if err := rows.Scan(&item.Field, &item.FieldID, &item.Position, &item.Direction, &item.OpClass); err != nil {
			return nil, err
		}
		item.Desc = item.Direction == "desc"
		fields = append(fields, item)
	}
	return fields, rows.Err()
}

func indexMatchesRequest(existing, requested *Index) error {
	if existing.Method != requested.Method {
		return fmt.Errorf("index %s already exists with method %s, not %s", existing.Name, existing.Method, requested.Method)
	}
	if existing.IsUnique != requested.IsUnique {
		return fmt.Errorf("index %s already exists with unique=%v, not %v", existing.Name, existing.IsUnique, requested.IsUnique)
	}
	if len(existing.Fields) != len(requested.Fields) {
		return fmt.Errorf("index %s already exists with %d fields, not %d", existing.Name, len(existing.Fields), len(requested.Fields))
	}
	for i := range existing.Fields {
		if existing.Fields[i].Field != requested.Fields[i].Field || existing.Fields[i].Desc != requested.Fields[i].Desc || existing.Fields[i].OpClass != requested.Fields[i].OpClass {
			return fmt.Errorf("index %s already exists with different field shape", existing.Name)
		}
	}
	return nil
}

func normalizedIndexDirection(field IndexField) string {
	direction := strings.ToLower(strings.TrimSpace(field.Direction))
	if direction == "desc" || field.Desc {
		return "desc"
	}
	return "asc"
}
