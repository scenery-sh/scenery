package datasource

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrCRUDNotFound      = errors.New("datasource CRUD row not found")
	ErrCRUDInvalidCursor = errors.New("datasource CRUD cursor is invalid for this query")
)

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
	List     *CRUDListSpec
}

type CRUDListSpec struct {
	Filters          []string
	Search           []string
	Sorts            []string
	DefaultSort      string
	DefaultDirection string
	MaxPageSize      int
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
	if spec.List != nil {
		return listCRUDPage(ctx, database, spec, input)
	}
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

type crudListCursor struct {
	Version     int               `json:"v"`
	Fingerprint string            `json:"fingerprint"`
	Values      []json.RawMessage `json:"values"`
}

func listCRUDPage(ctx context.Context, database SQL, spec CRUDSpec, input map[string]json.RawMessage) ([]byte, error) {
	where, arguments, err := crudWhere(spec, input, func(field CRUDField) bool { return field.TenantKey })
	if err != nil {
		return nil, err
	}
	var clauses []string
	if where != "" {
		clauses = append(clauses, strings.TrimPrefix(where, " WHERE "))
	}
	fields := crudFieldsByName(spec.Fields)
	filterValues := map[string]any{}
	for _, field := range spec.Fields {
		if !field.TenantKey {
			continue
		}
		raw, present := input[field.Name]
		if !present {
			continue
		}
		value, err := decodeCRUDSQLValue(raw)
		if err != nil {
			return nil, fmt.Errorf("CRUD %s tenant scope %s: %w", spec.Address, field.Name, err)
		}
		filterValues["tenant."+field.Name] = value
	}
	for _, name := range spec.List.Filters {
		field := fields[name]
		if crudBaseType(field.Type) == "string" || strings.Contains(field.Type, "enum.") || strings.Contains(field.Type, "/enum/") {
			raw, present := input[name]
			if !present {
				continue
			}
			values, err := decodeCRUDList(raw)
			if err != nil {
				return nil, fmt.Errorf("CRUD %s filter %s: %w", spec.Address, name, err)
			}
			sort.Slice(values, func(i, j int) bool { return fmt.Sprint(values[i]) < fmt.Sprint(values[j]) })
			values = compactCRUDValues(values)
			filterValues[name] = values
			if len(values) == 0 {
				clauses = append(clauses, "FALSE")
				continue
			}
			placeholders := make([]string, len(values))
			for index, value := range values {
				arguments = append(arguments, value)
				placeholders[index] = fmt.Sprintf("$%d", len(arguments))
			}
			clauses = append(clauses, quoteCRUDIdentifier(field.Column)+" IN ("+strings.Join(placeholders, ", ")+")")
			continue
		}
		for _, suffix := range []struct {
			name, operator string
		}{{"_from", ">="}, {"_to", "<="}} {
			key := name + suffix.name
			raw, present := input[key]
			if !present {
				continue
			}
			value, err := decodeCRUDSQLValue(raw)
			if err != nil {
				return nil, fmt.Errorf("CRUD %s filter %s: %w", spec.Address, key, err)
			}
			filterValues[key] = value
			arguments = append(arguments, value)
			clauses = append(clauses, quoteCRUDIdentifier(field.Column)+" "+suffix.operator+fmt.Sprintf(" $%d", len(arguments)))
		}
	}
	search, err := crudOptionalString(input["search"])
	if err != nil {
		return nil, err
	}
	search = strings.TrimSpace(search)
	if search != "" {
		filterValues["search"] = search
		arguments = append(arguments, "%"+escapeCRUDLike(search)+"%")
		placeholder := fmt.Sprintf("$%d", len(arguments))
		searchClauses := make([]string, 0, len(spec.List.Search))
		for _, name := range spec.List.Search {
			searchClauses = append(searchClauses, "LOWER("+quoteCRUDIdentifier(fields[name].Column)+") LIKE LOWER("+placeholder+") ESCAPE '\\'")
		}
		clauses = append(clauses, "("+strings.Join(searchClauses, " OR ")+")")
	}
	sortName, err := crudOptionalString(input["sort"])
	if err != nil {
		return nil, err
	}
	if sortName == "" {
		sortName = spec.List.DefaultSort
	}
	direction, err := crudOptionalString(input["direction"])
	if err != nil {
		return nil, err
	}
	if direction == "" {
		direction = spec.List.DefaultDirection
	}
	if direction == "" {
		direction = "asc"
	}
	if direction != "asc" && direction != "desc" || sortName != "" && !containsCRUDString(spec.List.Sorts, sortName) {
		return nil, fmt.Errorf("CRUD %s list sort is not allowlisted", spec.Address)
	}
	order := crudListOrder(spec.Fields, sortName)
	fingerprint := crudQueryFingerprint(sortName, direction, filterValues)
	cursorText, err := crudOptionalString(input["cursor"])
	if err != nil {
		return nil, err
	}
	if cursorText != "" {
		cursor, err := decodeCRUDCursor(cursorText, fingerprint, len(order))
		if err != nil {
			return nil, ErrCRUDInvalidCursor
		}
		cursorClause, cursorArguments, err := crudCursorClause(order, direction, cursor.Values, len(arguments)+1)
		if err != nil {
			return nil, ErrCRUDInvalidCursor
		}
		clauses, arguments = append(clauses, cursorClause), append(arguments, cursorArguments...)
	}
	limit := 50
	if spec.List.MaxPageSize < limit {
		limit = spec.List.MaxPageSize
	}
	if raw, present := input["limit"]; present {
		value, err := crudInteger(raw)
		if err != nil || value < 1 {
			return nil, fmt.Errorf("CRUD %s list limit must be positive", spec.Address)
		}
		limit = min(value, spec.List.MaxPageSize)
	}
	query := "SELECT " + crudColumns(spec) + " FROM " + crudRelation(spec)
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	orderSQL := make([]string, len(order))
	for index, field := range order {
		orderSQL[index] = quoteCRUDIdentifier(field.Column) + " " + strings.ToUpper(direction) + " NULLS LAST"
	}
	arguments = append(arguments, limit+1)
	query += " ORDER BY " + strings.Join(orderSQL, ", ") + fmt.Sprintf(" LIMIT $%d", len(arguments))
	rows, err := database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("CRUD %s list: %w", spec.Address, err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
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
	result := map[string]any{"items": items}
	if len(items) > limit {
		items = items[:limit]
		result["items"] = items
		values := make([]any, len(order))
		for index, field := range order {
			values[index] = items[len(items)-1][field.Name]
		}
		encoded, err := json.Marshal(crudListCursor{Version: 1, Fingerprint: fingerprint, Values: marshalCRUDCursorValues(values)})
		if err != nil {
			return nil, err
		}
		result["next_cursor"] = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return json.Marshal(result)
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

func crudFieldsByName(fields []CRUDField) map[string]CRUDField {
	result := make(map[string]CRUDField, len(fields))
	for _, field := range fields {
		result[field.Name] = field
	}
	return result
}

func crudListOrder(fields []CRUDField, sortName string) []CRUDField {
	byName := crudFieldsByName(fields)
	var result []CRUDField
	if sortName != "" {
		result = append(result, byName[sortName])
	}
	var primary []CRUDField
	for _, field := range fields {
		if field.PrimaryKey && field.Name != sortName {
			primary = append(primary, field)
		}
	}
	sort.Slice(primary, func(i, j int) bool { return primary[i].Name < primary[j].Name })
	return append(result, primary...)
}

func crudQueryFingerprint(sortName, direction string, filters map[string]any) string {
	data, _ := json.Marshal(struct {
		Sort      string         `json:"sort"`
		Direction string         `json:"direction"`
		Filters   map[string]any `json:"filters"`
	}{sortName, direction, filters})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func decodeCRUDCursor(value, fingerprint string, values int) (crudListCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return crudListCursor{}, err
	}
	var cursor crudListCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Fingerprint != fingerprint || len(cursor.Values) != values {
		return crudListCursor{}, ErrCRUDInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return crudListCursor{}, ErrCRUDInvalidCursor
	}
	return cursor, nil
}

func compactCRUDValues(values []any) []any {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if fmt.Sprint(value) != fmt.Sprint(result[len(result)-1]) {
			result = append(result, value)
		}
	}
	return result
}

func crudCursorClause(order []CRUDField, direction string, values []json.RawMessage, start int) (string, []any, error) {
	operator := ">"
	if direction == "desc" {
		operator = "<"
	}
	var branches []string
	var arguments []any
	for index, field := range order {
		if string(values[index]) == "null" {
			continue
		}
		var parts []string
		for prefix := 0; prefix < index; prefix++ {
			value, err := decodeCRUDSQLValue(values[prefix])
			if err != nil {
				return "", nil, err
			}
			arguments = append(arguments, value)
			parts = append(parts, quoteCRUDIdentifier(order[prefix].Column)+fmt.Sprintf(" IS NOT DISTINCT FROM $%d", start+len(arguments)-1))
		}
		value, err := decodeCRUDSQLValue(values[index])
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, value)
		parts = append(parts, "("+quoteCRUDIdentifier(field.Column)+" "+operator+fmt.Sprintf(" $%d", start+len(arguments)-1)+" OR "+quoteCRUDIdentifier(field.Column)+" IS NULL)")
		branches = append(branches, "("+strings.Join(parts, " AND ")+")")
	}
	if len(branches) == 0 {
		return "FALSE", arguments, nil
	}
	return "(" + strings.Join(branches, " OR ") + ")", arguments, nil
}

func marshalCRUDCursorValues(values []any) []json.RawMessage {
	result := make([]json.RawMessage, len(values))
	for index, value := range values {
		result[index], _ = json.Marshal(value)
	}
	return result
}

func crudOptionalString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func crudInteger(raw json.RawMessage) (int, error) {
	if len(raw) > 0 && raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return 0, err
		}
		return strconv.Atoi(encoded)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func decodeCRUDList(raw json.RawMessage) ([]any, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	result := make([]any, len(values))
	for index, value := range values {
		decoded, err := decodeCRUDSQLValue(value)
		if err != nil {
			return nil, err
		}
		result[index] = decoded
	}
	return result, nil
}

func containsCRUDString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	if spec.List != nil {
		if spec.List.MaxPageSize < 1 || spec.List.DefaultDirection != "" && spec.List.DefaultDirection != "asc" && spec.List.DefaultDirection != "desc" {
			return fmt.Errorf("CRUD %s has invalid list pagination", spec.Address)
		}
		for _, name := range append(append(append([]string(nil), spec.List.Filters...), spec.List.Search...), spec.List.Sorts...) {
			if !names[name] {
				return fmt.Errorf("CRUD %s list references unknown field %s", spec.Address, name)
			}
		}
		if spec.List.DefaultSort != "" && !containsCRUDString(spec.List.Sorts, spec.List.DefaultSort) {
			return fmt.Errorf("CRUD %s default list sort is not allowlisted", spec.Address)
		}
	}
	return nil
}

func escapeCRUDLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func crudBaseType(value string) string {
	value = strings.TrimSpace(value)
	for {
		open := strings.IndexByte(value, '(')
		if open < 0 || !strings.HasSuffix(value, ")") {
			return value
		}
		wrapper := strings.TrimSpace(value[:open])
		if wrapper != "optional" && wrapper != "nullable" {
			return value
		}
		value = strings.TrimSpace(value[open+1 : len(value)-1])
	}
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
