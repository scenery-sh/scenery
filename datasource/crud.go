package datasource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrCRUDNotFound = errors.New("datasource CRUD row not found")

type CRUDField struct {
	Name            string
	Column          string
	Type            string
	PrimaryKey      bool
	TenantKey       bool
	Immutable       bool
	DefaultStrategy string
}

type CRUDSpec struct {
	Address  string
	Schema   string
	Relation string
	Fields   []CRUDField
}

// InvokeCRUD executes the built-in std.crud.entity implementation against an
// injected SQL capability. Input and output are schema-directed JSON bytes;
// generated adapters own conversion to and from closed contract outcomes.
func InvokeCRUD(ctx context.Context, database SQL, spec CRUDSpec, action string, input []byte) ([]byte, error) {
	if database == nil {
		return nil, fmt.Errorf("CRUD %s has no SQL data source", spec.Address)
	}
	if err := validateCRUDSpec(spec); err != nil {
		return nil, err
	}
	values, err := decodeCRUDInput(input)
	if err != nil {
		return nil, fmt.Errorf("CRUD %s input: %w", spec.Address, err)
	}
	switch action {
	case "list":
		return listCRUD(ctx, database, spec, values)
	case "get":
		return getCRUD(ctx, database, spec, values)
	case "create":
		return createCRUD(ctx, database, spec, values)
	case "update":
		return updateCRUD(ctx, database, spec, values)
	case "delete":
		return deleteCRUD(ctx, database, spec, values)
	default:
		return nil, fmt.Errorf("CRUD %s has unsupported action %q", spec.Address, action)
	}
}

func listCRUD(ctx context.Context, database SQL, spec CRUDSpec, input map[string]json.RawMessage) ([]byte, error) {
	where, arguments, err := crudWhere(spec, input, func(field CRUDField) bool { return field.TenantKey })
	if err != nil {
		return nil, err
	}
	query := "SELECT " + crudColumns(spec) + " FROM " + crudRelation(spec) + where
	if order := crudOrder(spec); order != "" {
		query += " ORDER BY " + order
	}
	rows, err := database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("CRUD %s list: %w", spec.Address, err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, scanErr := scanCRUDRow(rows, spec.Fields)
		if scanErr != nil {
			return nil, fmt.Errorf("CRUD %s list row: %w", spec.Address, scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("CRUD %s list rows: %w", spec.Address, err)
	}
	return json.Marshal(map[string]any{"items": items})
}

func getCRUD(ctx context.Context, database SQL, spec CRUDSpec, input map[string]json.RawMessage) ([]byte, error) {
	where, arguments, err := crudWhere(spec, input, func(field CRUDField) bool { return field.PrimaryKey || field.TenantKey })
	if err != nil {
		return nil, err
	}
	query := "SELECT " + crudColumns(spec) + " FROM " + crudRelation(spec) + where + " LIMIT 1"
	value, err := scanCRUDRow(database.QueryRowContext(ctx, query, arguments...), spec.Fields)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCRUDNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("CRUD %s get: %w", spec.Address, err)
	}
	return json.Marshal(map[string]any{"value": value})
}

func createCRUD(ctx context.Context, database SQL, spec CRUDSpec, input map[string]json.RawMessage) ([]byte, error) {
	var columns []string
	var arguments []any
	for _, field := range spec.Fields {
		raw, exists := input[field.Name]
		if !exists {
			value, generated, err := crudDefault(field.DefaultStrategy)
			if err != nil {
				return nil, err
			}
			if !generated {
				continue
			}
			columns, arguments = append(columns, quoteCRUDIdentifier(field.Column)), append(arguments, value)
			continue
		}
		value, err := decodeCRUDSQLValue(raw)
		if err != nil {
			return nil, fmt.Errorf("CRUD field %s: %w", field.Name, err)
		}
		columns, arguments = append(columns, quoteCRUDIdentifier(field.Column)), append(arguments, value)
	}
	query := "INSERT INTO " + crudRelation(spec)
	if len(columns) == 0 {
		query += " DEFAULT VALUES"
	} else {
		query += " (" + strings.Join(columns, ", ") + ") VALUES (" + crudPlaceholders(1, len(columns)) + ")"
	}
	query += " RETURNING " + crudColumns(spec)
	value, err := scanCRUDRow(database.QueryRowContext(ctx, query, arguments...), spec.Fields)
	if err != nil {
		return nil, fmt.Errorf("CRUD %s create: %w", spec.Address, err)
	}
	return json.Marshal(map[string]any{"value": value})
}

func updateCRUD(ctx context.Context, database SQL, spec CRUDSpec, input map[string]json.RawMessage) ([]byte, error) {
	var assignments []string
	var arguments []any
	for _, field := range spec.Fields {
		if field.PrimaryKey || field.TenantKey || field.Immutable {
			continue
		}
		raw, exists := input[field.Name]
		if !exists {
			continue
		}
		value, err := decodeCRUDSQLValue(raw)
		if err != nil {
			return nil, fmt.Errorf("CRUD field %s: %w", field.Name, err)
		}
		arguments = append(arguments, value)
		assignments = append(assignments, quoteCRUDIdentifier(field.Column)+fmt.Sprintf(" = $%d", len(arguments)))
	}
	if len(assignments) == 0 {
		return getCRUD(ctx, database, spec, input)
	}
	where, whereArguments, err := crudWhereFrom(spec, input, len(arguments)+1, func(field CRUDField) bool { return field.PrimaryKey || field.TenantKey })
	if err != nil {
		return nil, err
	}
	arguments = append(arguments, whereArguments...)
	query := "UPDATE " + crudRelation(spec) + " SET " + strings.Join(assignments, ", ") + where + " RETURNING " + crudColumns(spec)
	value, err := scanCRUDRow(database.QueryRowContext(ctx, query, arguments...), spec.Fields)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCRUDNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("CRUD %s update: %w", spec.Address, err)
	}
	return json.Marshal(map[string]any{"value": value})
}

func deleteCRUD(ctx context.Context, database SQL, spec CRUDSpec, input map[string]json.RawMessage) ([]byte, error) {
	where, arguments, err := crudWhere(spec, input, func(field CRUDField) bool { return field.PrimaryKey || field.TenantKey })
	if err != nil {
		return nil, err
	}
	query := "DELETE FROM " + crudRelation(spec) + where + " RETURNING " + crudColumns(spec)
	value, err := scanCRUDRow(database.QueryRowContext(ctx, query, arguments...), spec.Fields)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCRUDNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("CRUD %s delete: %w", spec.Address, err)
	}
	return json.Marshal(map[string]any{"value": value})
}

type crudScanner interface{ Scan(...any) error }

func scanCRUDRow(scanner crudScanner, fields []CRUDField) (map[string]any, error) {
	values := make([]any, len(fields))
	pointers := make([]any, len(fields))
	for index := range values {
		pointers[index] = &values[index]
	}
	if err := scanner.Scan(pointers...); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(fields))
	for index, field := range fields {
		result[field.Name] = normalizeCRUDResult(values[index], field.Type)
	}
	return result, nil
}

func normalizeCRUDResult(value any, typeName string) any {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case []byte:
		if typeName == "json" && json.Valid(typed) {
			return json.RawMessage(append([]byte(nil), typed...))
		}
		return string(typed)
	default:
		return value
	}
}

func crudWhere(spec CRUDSpec, input map[string]json.RawMessage, include func(CRUDField) bool) (string, []any, error) {
	return crudWhereFrom(spec, input, 1, include)
}

func crudWhereFrom(spec CRUDSpec, input map[string]json.RawMessage, start int, include func(CRUDField) bool) (string, []any, error) {
	var clauses []string
	var arguments []any
	for _, field := range spec.Fields {
		if !include(field) {
			continue
		}
		raw, ok := input[field.Name]
		if !ok {
			return "", nil, fmt.Errorf("CRUD %s requires field %s", spec.Address, field.Name)
		}
		value, err := decodeCRUDSQLValue(raw)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, quoteCRUDIdentifier(field.Column)+fmt.Sprintf(" = $%d", start+len(arguments)))
		arguments = append(arguments, value)
	}
	if len(clauses) == 0 {
		return "", arguments, nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), arguments, nil
}

func decodeCRUDInput(input []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("input must be an object")
	}
	return object, nil
}

func decodeCRUDSQLValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if number, ok := value.(json.Number); ok {
		return number.String(), nil
	}
	if object, ok := value.(map[string]any); ok {
		encoded, err := json.Marshal(object)
		return string(encoded), err
	}
	if list, ok := value.([]any); ok {
		encoded, err := json.Marshal(list)
		return string(encoded), err
	}
	return value, nil
}

func crudDefault(strategy string) (any, bool, error) {
	switch strategy {
	case "":
		return nil, false, nil
	case "provider":
		return nil, false, nil
	case "uuid_v7":
		value, err := uuid.NewV7()
		return value.String(), err == nil, err
	case "current_datetime":
		return time.Now().UTC(), true, nil
	default:
		return nil, false, fmt.Errorf("unsupported CRUD default strategy %q", strategy)
	}
}

var crudIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateCRUDSpec(spec CRUDSpec) error {
	if !crudIdentifierPattern.MatchString(spec.Relation) || spec.Schema != "" && !crudIdentifierPattern.MatchString(spec.Schema) || len(spec.Fields) == 0 {
		return fmt.Errorf("CRUD %s has an invalid relation or no fields", spec.Address)
	}
	names, columns := map[string]bool{}, map[string]bool{}
	primaryKeys := 0
	for _, field := range spec.Fields {
		if !crudIdentifierPattern.MatchString(field.Name) || !crudIdentifierPattern.MatchString(field.Column) || names[field.Name] || columns[field.Column] {
			return fmt.Errorf("CRUD %s has an invalid or duplicate field mapping", spec.Address)
		}
		names[field.Name], columns[field.Column] = true, true
		if field.PrimaryKey {
			primaryKeys++
		}
	}
	if primaryKeys == 0 {
		return fmt.Errorf("CRUD %s has no primary key", spec.Address)
	}
	return nil
}

func crudRelation(spec CRUDSpec) string {
	relation := quoteCRUDIdentifier(spec.Relation)
	if spec.Schema != "" {
		return quoteCRUDIdentifier(spec.Schema) + "." + relation
	}
	return relation
}

func crudColumns(spec CRUDSpec) string {
	columns := make([]string, 0, len(spec.Fields))
	for _, field := range spec.Fields {
		columns = append(columns, quoteCRUDIdentifier(field.Column))
	}
	return strings.Join(columns, ", ")
}

func crudOrder(spec CRUDSpec) string {
	var fields []string
	for _, field := range spec.Fields {
		if field.PrimaryKey {
			fields = append(fields, quoteCRUDIdentifier(field.Column))
		}
	}
	sort.Strings(fields)
	return strings.Join(fields, ", ")
}

func crudPlaceholders(start, count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = fmt.Sprintf("$%d", start+index)
	}
	return strings.Join(values, ", ")
}

func quoteCRUDIdentifier(value string) string { return `"` + value + `"` }
