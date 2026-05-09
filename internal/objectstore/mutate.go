package objectstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type columnValue struct {
	Name  string
	Value any
	Cast  string
	Field string
}

func (s *Store) CreateRecord(ctx context.Context, actor Actor, objectName string, req CreateRecordRequest) (*RecordResponse, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	values, changedFields, err := s.columnValuesForRecord(ctx, actor, state, req.Values)
	if err != nil {
		return nil, err
	}
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	var event *Event
	var after Record
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOutboxTxContext(ctx, tx, actor, true); err != nil {
		return nil, fmt.Errorf("set outbox transaction context: %w", err)
	}
	columns := []string{"id", "tenant_id", "created_at", "updated_at"}
	args := []any{id, state.Tenant.ID, now, now}
	placeholders := []string{"$1", "$2", "$3", "$4"}
	for _, value := range values {
		columns = append(columns, value.Name)
		args = append(args, value.Value)
		placeholder := fmt.Sprintf("$%d", len(args))
		if value.Cast != "" {
			placeholder += "::" + value.Cast
		}
		placeholders = append(placeholders, placeholder)
	}
	stmt := `insert into ` + qualifiedIdent(RecordsSchema, state.Object.TableName) +
		` (` + quoteIdentList(columns) + `) values (` + strings.Join(placeholders, ", ") + `)`
	if _, err := tx.Exec(ctx, stmt, args...); err != nil {
		return nil, fmt.Errorf("create record for object %s: %w", objectName, err)
	}
	after, err = queryOneRecord(ctx, tx, state, id)
	if err != nil {
		return nil, err
	}
	if err := s.upsertSearchDocument(ctx, tx, state, id, after); err != nil {
		return nil, err
	}
	event, err = s.insertOutbox(ctx, tx, outboxDraft{
		TenantID:      state.Tenant.ID,
		ObjectID:      state.Object.ID,
		ObjectName:    state.Object.NameSingular,
		RecordID:      id,
		Action:        "created",
		ActorID:       actor.ID,
		SchemaVersion: state.Object.SchemaVersion,
		ChangedFields: changedFields,
		After:         after,
		Diff:          diffRecords(nil, after, changedFields),
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.router.publish(event)
	return &RecordResponse{Record: after, Event: event}, nil
}

func (s *Store) UpdateRecord(ctx context.Context, actor Actor, objectName, id string, req UpdateRecordRequest) (*RecordResponse, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	values, changedFields, err := s.columnValuesForRecord(ctx, actor, state, req.Values)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("update record requires at least one value")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOutboxTxContext(ctx, tx, actor, true); err != nil {
		return nil, fmt.Errorf("set outbox transaction context: %w", err)
	}
	before, err := queryOneRecord(ctx, tx, state, id)
	if err != nil {
		return nil, err
	}
	args := []any{}
	sets := make([]string, 0, len(values)+1)
	for _, value := range values {
		args = append(args, value.Value)
		placeholder := fmt.Sprintf("$%d", len(args))
		if value.Cast != "" {
			placeholder += "::" + value.Cast
		}
		sets = append(sets, quoteIdent(value.Name)+" = "+placeholder)
	}
	args = append(args, s.now())
	sets = append(sets, fmt.Sprintf("updated_at = $%d", len(args)))
	args = append(args, state.Tenant.ID, id)
	stmt := `update ` + qualifiedIdent(RecordsSchema, state.Object.TableName) +
		` set ` + strings.Join(sets, ", ") +
		fmt.Sprintf(` where tenant_id = $%d and id = $%d and deleted_at is null`, len(args)-1, len(args))
	tag, err := tx.Exec(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("update record %s for object %s: %w", id, objectName, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("record %s does not exist on object %s", id, objectName)
	}
	after, err := queryOneRecord(ctx, tx, state, id)
	if err != nil {
		return nil, err
	}
	if err := s.upsertSearchDocument(ctx, tx, state, id, after); err != nil {
		return nil, err
	}
	event, err := s.insertOutbox(ctx, tx, outboxDraft{
		TenantID:      state.Tenant.ID,
		ObjectID:      state.Object.ID,
		ObjectName:    state.Object.NameSingular,
		RecordID:      id,
		Action:        "updated",
		ActorID:       actor.ID,
		SchemaVersion: state.Object.SchemaVersion,
		ChangedFields: changedFields,
		Before:        before,
		After:         after,
		Diff:          diffRecords(before, after, changedFields),
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.router.publish(event)
	return &RecordResponse{Record: after, Event: event}, nil
}

func (s *Store) DeleteRecord(ctx context.Context, actor Actor, objectName, id string, req DeleteRecordRequest) (*DeleteRecordResponse, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOutboxTxContext(ctx, tx, actor, true); err != nil {
		return nil, fmt.Errorf("set outbox transaction context: %w", err)
	}
	before, err := queryOneRecord(ctx, tx, state, id)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `update `+qualifiedIdent(RecordsSchema, state.Object.TableName)+`
		set deleted_at = $1, updated_at = $1
		where tenant_id = $2 and id = $3 and deleted_at is null
	`, s.now(), state.Tenant.ID, id)
	if err != nil {
		return nil, fmt.Errorf("delete record %s for object %s: %w", id, objectName, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("record %s does not exist on object %s", id, objectName)
	}
	if err := deleteSearchDocument(ctx, tx, state, id); err != nil {
		return nil, err
	}
	event, err := s.insertOutbox(ctx, tx, outboxDraft{
		TenantID:      state.Tenant.ID,
		ObjectID:      state.Object.ID,
		ObjectName:    state.Object.NameSingular,
		RecordID:      id,
		Action:        "deleted",
		ActorID:       actor.ID,
		SchemaVersion: state.Object.SchemaVersion,
		ChangedFields: []string{"deleted_at"},
		Before:        before,
		Diff:          Record{"deleted_at": true},
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.router.publish(event)
	return &DeleteRecordResponse{ID: id, Event: event}, nil
}

func (s *Store) columnValuesForRecord(ctx context.Context, actor Actor, state *metadataState, values Record) ([]columnValue, []string, error) {
	if len(values) == 0 {
		return nil, nil, nil
	}
	var out []columnValue
	var changed []string
	for fieldName, rawValue := range values {
		field, ok := state.Fields[fieldName]
		if !ok {
			return nil, nil, fmt.Errorf("field %s does not exist on object %s", fieldName, state.Object.NameSingular)
		}
		if err := s.perms.CanWriteField(ctx, actor, fieldRef(state, field)); err != nil {
			return nil, nil, err
		}
		fieldValues, err := valuesForField(field, rawValue)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, fieldValues...)
		changed = append(changed, field.Name)
	}
	sortStrings(changed)
	return out, changed, nil
}

func valuesForField(field *Field, raw any) ([]columnValue, error) {
	if raw == nil {
		if !field.IsNullable {
			return nil, fmt.Errorf("field %s is not nullable", field.Name)
		}
		values := make([]columnValue, 0, len(field.Columns))
		for _, column := range field.Columns {
			values = append(values, columnValue{Name: column.Name, Field: field.Name})
		}
		return values, nil
	}
	if err := validateOptionValue(field, raw); err != nil {
		return nil, err
	}
	if isCompositeField(field.Type) {
		record, ok := raw.(map[string]any)
		if !ok {
			if converted, ok := raw.(Record); ok {
				record = map[string]any(converted)
			}
		}
		if !ok && record == nil {
			return nil, fmt.Errorf("field %s expects an object value", field.Name)
		}
		values := make([]columnValue, 0, len(field.Columns))
		for _, column := range field.Columns {
			values = append(values, dbValueForColumn(field, column, record[column.Part]))
		}
		return values, nil
	}
	if len(field.Columns) != 1 {
		return nil, fmt.Errorf("field %s has no single physical column", field.Name)
	}
	return []columnValue{dbValueForColumn(field, field.Columns[0], raw)}, nil
}

func dbValueForColumn(field *Field, column PhysicalColumn, raw any) columnValue {
	value := raw
	cast := ""
	switch column.SQLType {
	case "uuid":
		cast = "uuid"
	case "jsonb":
		data, _ := json.Marshal(raw)
		value = string(data)
		cast = "jsonb"
	}
	if field.Type == FieldMultiSelect {
		value = stringSlice(raw)
	}
	return columnValue{Name: column.Name, Value: value, Cast: cast, Field: field.Name}
}

func validateOptionValue(field *Field, raw any) error {
	if len(field.Options) == 0 {
		return nil
	}
	valid := selectOptionValues(field.Options)
	switch field.Type {
	case FieldSelect:
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("field %s expects a string select value", field.Name)
		}
		if !valid[value] {
			return fmt.Errorf("field %s option %q is not defined", field.Name, value)
		}
	case FieldMultiSelect:
		for _, value := range stringSlice(raw) {
			if !valid[value] {
				return fmt.Errorf("field %s option %q is not defined", field.Name, value)
			}
		}
	}
	return nil
}

func queryOneRecord(ctx context.Context, q Queryer, state *metadataState, id string) (Record, error) {
	compiled, err := compileQuery(state, Query{
		Filter: &Filter{Op: "eq", Field: "id", Value: id},
		Limit:  1,
	})
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, compiled.SQL, compiled.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("record %s does not exist on object %s", id, state.Object.NameSingular)
	}
	values, err := rows.Values()
	if err != nil {
		return nil, err
	}
	return recordFromValues(compiled.Columns, values), rows.Err()
}

func diffRecords(before, after Record, fields []string) Record {
	diff := Record{}
	for _, field := range fields {
		if !reflect.DeepEqual(before[field], after[field]) {
			diff[field] = after[field]
		}
	}
	return diff
}

func quoteIdentList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, quoteIdent(name))
	}
	return strings.Join(quoted, ", ")
}

func stringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return []string{fmt.Sprint(raw)}
	}
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
