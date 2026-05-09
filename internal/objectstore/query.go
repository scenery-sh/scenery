package objectstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const defaultQueryLimit = 100
const maxQueryLimit = 1000

type resultColumn struct {
	Alias  string
	Field  string
	Part   string
	Hidden bool
}

type compiledQuery struct {
	SQL           string
	Args          []any
	Columns       []resultColumn
	CursorColumns []resultColumn
	EffectiveSort []Sort
	Limit         int
	SchemaVersion int64
	Object        string
}

type cursorPayload struct {
	Version       int    `json:"v"`
	Object        string `json:"object"`
	SchemaVersion int64  `json:"schema_version"`
	Sort          []Sort `json:"sort"`
	Values        []any  `json:"values"`
}

func (s *Store) QueryRecords(ctx context.Context, actor Actor, objectName string, req QueryRecordsRequest) (*RecordPage, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanReadObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	for _, field := range state.Fields {
		if err := s.perms.CanReadField(ctx, actor, fieldRef(state, field)); err != nil {
			return nil, err
		}
	}
	permissionFilter, err := s.perms.RowFilter(ctx, actor, objectRef(state))
	if err != nil {
		return nil, err
	}
	query := req.Query
	if query.Object == "" {
		query.Object = objectName
	}
	if permissionFilter != nil {
		query.Filter = andFilters(query.Filter, permissionFilter)
	}
	compiled, err := compileQuery(state, query)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, compiled.SQL, compiled.Args...)
	if err != nil {
		return nil, fmt.Errorf("query records for object %s: %w", objectName, err)
	}
	defer rows.Close()
	var records []Record
	var cursorValueSets [][]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		records = append(records, recordFromValues(compiled.Columns, values))
		cursorValues := make([]any, 0, len(compiled.CursorColumns))
		cursorStart := len(compiled.Columns)
		for i := range compiled.CursorColumns {
			cursorValues = append(cursorValues, decodeJSONValue(values[cursorStart+i]))
		}
		cursorValueSets = append(cursorValueSets, cursorValues)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var nextCursor string
	if len(records) > compiled.Limit {
		records = records[:compiled.Limit]
		cursorValueSets = cursorValueSets[:compiled.Limit]
		lastValues := cursorValueSets[len(cursorValueSets)-1]
		nextCursor, err = encodeCursor(cursorPayload{
			Version:       1,
			Object:        compiled.Object,
			SchemaVersion: compiled.SchemaVersion,
			Sort:          compiled.EffectiveSort,
			Values:        lastValues,
		})
		if err != nil {
			return nil, err
		}
	}
	return &RecordPage{Records: records, NextCursor: nextCursor}, nil
}

func compileQuery(state *metadataState, query Query) (*compiledQuery, error) {
	if query.Object != "" && query.Object != state.Object.NameSingular {
		return nil, fmt.Errorf("query object %q does not match endpoint object %q", query.Object, state.Object.NameSingular)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	if limit > maxQueryLimit {
		limit = maxQueryLimit
	}
	selected, err := selectedFields(state, query.Select)
	if err != nil {
		return nil, err
	}
	effectiveSort, err := effectiveSorts(state, query.Sort)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeAndValidateCursor(state, query.Cursor, effectiveSort)
	if err != nil {
		return nil, err
	}
	args := []any{state.Tenant.ID}
	var cols []string
	var resultCols []resultColumn
	addSystem := func(name string, expr string) {
		cols = append(cols, expr+" as "+quoteIdent(name))
		resultCols = append(resultCols, resultColumn{Alias: name, Field: name})
	}
	addSystem("id", `to_jsonb(id::text)`)
	addSystem("created_at", `to_jsonb(created_at)`)
	addSystem("updated_at", `to_jsonb(updated_at)`)
	for _, fieldName := range selected {
		field := state.Fields[fieldName]
		for _, column := range field.Columns {
			alias := column.Name
			if !isCompositeField(field.Type) && column.Part == "" {
				alias = field.Name
			}
			cols = append(cols, `to_jsonb(`+quoteIdent(column.Name)+`) as `+quoteIdent(alias))
			resultCols = append(resultCols, resultColumn{Alias: alias, Field: field.Name, Part: column.Part})
		}
	}
	where := []string{`tenant_id = $1`, `deleted_at is null`}
	if query.Filter != nil {
		filterSQL, err := compileFilter(state, query.Filter, &args)
		if err != nil {
			return nil, err
		}
		if filterSQL != "" {
			where = append(where, filterSQL)
		}
	}
	if cursor != nil {
		cursorSQL, err := compileCursorPredicate(state, effectiveSort, cursor.Values, &args)
		if err != nil {
			return nil, err
		}
		where = append(where, cursorSQL)
	}
	orderBy, err := compileSort(state, effectiveSort)
	if err != nil {
		return nil, err
	}
	cursorCols, err := cursorColumns(state, effectiveSort)
	if err != nil {
		return nil, err
	}
	for i, col := range cursorCols {
		alias := fmt.Sprintf("__cursor_%d", i)
		cols = append(cols, `to_jsonb(`+col+`) as `+quoteIdent(alias))
	}
	args = append(args, limit+1)
	sql := `select ` + strings.Join(cols, ", ") +
		` from ` + qualifiedIdent(RecordsSchema, state.Object.TableName) +
		` where ` + strings.Join(where, " and ") +
		` order by ` + orderBy +
		fmt.Sprintf(` limit $%d`, len(args))
	return &compiledQuery{
		SQL:           sql,
		Args:          args,
		Columns:       resultCols,
		CursorColumns: cursorResultColumns(effectiveSort),
		EffectiveSort: effectiveSort,
		Limit:         limit,
		SchemaVersion: state.Object.SchemaVersion,
		Object:        state.Object.NameSingular,
	}, nil
}

func selectedFields(state *metadataState, requested []string) ([]string, error) {
	if len(requested) == 0 {
		names := make([]string, 0, len(state.Fields))
		for name := range state.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	seen := map[string]bool{}
	var names []string
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		if _, ok := state.Fields[name]; !ok {
			return nil, fmt.Errorf("selected field %q does not exist on object %s", name, state.Object.NameSingular)
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

func compileFilter(state *metadataState, filter *Filter, args *[]any) (string, error) {
	if filter == nil {
		return "", nil
	}
	op := strings.ToLower(strings.TrimSpace(filter.Op))
	switch op {
	case "":
		return "", nil
	case "and", "or":
		if len(filter.Filters) == 0 {
			return "", fmt.Errorf("filter %s requires nested filters", op)
		}
		parts := make([]string, 0, len(filter.Filters))
		for i := range filter.Filters {
			part, err := compileFilter(state, &filter.Filters[i], args)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, "("+part+")")
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		return strings.Join(parts, " "+op+" "), nil
	case "not":
		if len(filter.Filters) != 1 {
			return "", fmt.Errorf("filter not requires exactly one nested filter")
		}
		part, err := compileFilter(state, &filter.Filters[0], args)
		if err != nil {
			return "", err
		}
		return "not (" + part + ")", nil
	}

	column, field, err := filterColumn(state, filter.Field)
	if err != nil {
		return "", err
	}
	if field != nil {
		if err := validateFilterOperator(field, op); err != nil {
			return "", err
		}
	}
	addArg := func(value any) string {
		*args = append(*args, value)
		return fmt.Sprintf("$%d", len(*args))
	}
	switch op {
	case "eq":
		if filter.Value == nil {
			return column + " is null", nil
		}
		return column + " = " + addArg(filter.Value), nil
	case "neq":
		if filter.Value == nil {
			return column + " is not null", nil
		}
		return column + " <> " + addArg(filter.Value), nil
	case "gt":
		return column + " > " + addArg(filter.Value), nil
	case "gte":
		return column + " >= " + addArg(filter.Value), nil
	case "lt":
		return column + " < " + addArg(filter.Value), nil
	case "lte":
		return column + " <= " + addArg(filter.Value), nil
	case "in":
		if len(filter.Values) == 0 {
			return "", fmt.Errorf("filter in requires values")
		}
		placeholders := make([]string, 0, len(filter.Values))
		for _, value := range filter.Values {
			placeholders = append(placeholders, addArg(value))
		}
		return column + " in (" + strings.Join(placeholders, ", ") + ")", nil
	case "is_null":
		if boolValue(filter.Value, true) {
			return column + " is null", nil
		}
		return column + " is not null", nil
	case "contains":
		return column + " ilike '%' || " + addArg(filter.Value) + " || '%'", nil
	default:
		return "", fmt.Errorf("filter operator %q is not supported", op)
	}
}

func filterColumn(state *metadataState, name string) (string, *Field, error) {
	name = strings.TrimSpace(name)
	switch name {
	case "id":
		return "id::text", nil, nil
	case "created_at", "updated_at":
		return quoteIdent(name), nil, nil
	}
	field, ok := state.Fields[name]
	if !ok {
		return "", nil, fmt.Errorf("filter field %q does not exist on object %s", name, state.Object.NameSingular)
	}
	if isCompositeField(field.Type) {
		return "", nil, fmt.Errorf("filter field %q is composite and cannot be filtered in the first data platform slice", name)
	}
	if len(field.Columns) != 1 {
		return "", nil, fmt.Errorf("filter field %q has no single physical column", name)
	}
	return quoteIdent(field.Columns[0].Name), field, nil
}

func compileSort(state *metadataState, sorts []Sort) (string, error) {
	if len(sorts) == 0 {
		return quoteIdent("id") + " asc", nil
	}
	parts := make([]string, 0, len(sorts))
	for _, sortSpec := range sorts {
		column, _, err := filterColumn(state, sortSpec.Field)
		if err != nil {
			return "", err
		}
		dir := "asc"
		if sortSpec.Desc {
			dir = "desc"
		}
		parts = append(parts, column+" "+dir)
	}
	return strings.Join(parts, ", "), nil
}

func effectiveSorts(state *metadataState, sorts []Sort) ([]Sort, error) {
	out := make([]Sort, 0, len(sorts)+1)
	hasID := false
	if len(sorts) == 0 {
		out = append(out, Sort{Field: "id"})
		hasID = true
	}
	for _, sortSpec := range sorts {
		field := strings.TrimSpace(sortSpec.Field)
		if field == "" {
			return nil, fmt.Errorf("sort field is required")
		}
		if _, _, err := filterColumn(state, field); err != nil {
			return nil, err
		}
		if field == "id" {
			hasID = true
		}
		out = append(out, Sort{Field: field, Desc: sortSpec.Desc})
	}
	if !hasID {
		out = append(out, Sort{Field: "id"})
	}
	return out, nil
}

func cursorColumns(state *metadataState, sorts []Sort) ([]string, error) {
	out := make([]string, 0, len(sorts))
	for _, sortSpec := range sorts {
		column, _, err := filterColumn(state, sortSpec.Field)
		if err != nil {
			return nil, err
		}
		out = append(out, column)
	}
	return out, nil
}

func cursorResultColumns(sorts []Sort) []resultColumn {
	out := make([]resultColumn, 0, len(sorts))
	for _, sortSpec := range sorts {
		out = append(out, resultColumn{Field: sortSpec.Field, Hidden: true})
	}
	return out
}

func compileCursorPredicate(state *metadataState, sorts []Sort, values []any, args *[]any) (string, error) {
	if len(values) != len(sorts) {
		return "", fmt.Errorf("cursor has %d values for %d sort fields", len(values), len(sorts))
	}
	columns, err := cursorColumns(state, sorts)
	if err != nil {
		return "", err
	}
	addArg := func(value any) string {
		*args = append(*args, value)
		return fmt.Sprintf("$%d", len(*args))
	}
	parts := make([]string, 0, len(sorts))
	var equals []string
	for i, sortSpec := range sorts {
		cmp := ">"
		if sortSpec.Desc {
			cmp = "<"
		}
		arg := addArg(values[i])
		termParts := append([]string{}, equals...)
		termParts = append(termParts, columns[i]+" "+cmp+" "+arg)
		parts = append(parts, "("+strings.Join(termParts, " and ")+")")
		equals = append(equals, columns[i]+" = "+arg)
	}
	return "(" + strings.Join(parts, " or ") + ")", nil
}

func encodeCursor(payload cursorPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeAndValidateCursor(state *metadataState, raw string, sorts []Sort) (*cursorPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode query cursor: %w", err)
	}
	var payload cursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode query cursor json: %w", err)
	}
	if payload.Version != 1 {
		return nil, fmt.Errorf("cursor version %d is not supported", payload.Version)
	}
	if payload.Object != state.Object.NameSingular {
		return nil, fmt.Errorf("cursor is for object %q, not %q", payload.Object, state.Object.NameSingular)
	}
	if payload.SchemaVersion != 0 && payload.SchemaVersion != state.Object.SchemaVersion {
		return nil, fmt.Errorf("cursor schema version %d does not match object schema version %d", payload.SchemaVersion, state.Object.SchemaVersion)
	}
	if !sortsEqual(payload.Sort, sorts) {
		return nil, fmt.Errorf("cursor sort shape does not match query sort")
	}
	if len(payload.Values) != len(sorts) {
		return nil, fmt.Errorf("cursor has %d values for %d sort fields", len(payload.Values), len(sorts))
	}
	return &payload, nil
}

func sortsEqual(a, b []Sort) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Field != b[i].Field || a[i].Desc != b[i].Desc {
			return false
		}
	}
	return true
}

func recordFromValues(columns []resultColumn, values []any) Record {
	record := Record{}
	composites := map[string]Record{}
	for i, column := range columns {
		var value any
		if i < len(values) {
			value = decodeJSONValue(values[i])
		}
		if column.Part == "" {
			record[column.Field] = value
			continue
		}
		item := composites[column.Field]
		if item == nil {
			item = Record{}
			composites[column.Field] = item
		}
		item[column.Part] = value
	}
	for field, value := range composites {
		record[field] = value
	}
	return record
}

func decodeJSONValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		var out any
		if err := json.Unmarshal(v, &out); err == nil {
			return out
		}
		return string(v)
	case string:
		var out any
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return out
		}
		return v
	default:
		return v
	}
}

func andFilters(a, b *Filter) *Filter {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &Filter{Op: "and", Filters: []Filter{*a, *b}}
}

func boolValue(value any, fallback bool) bool {
	v, ok := value.(bool)
	if !ok {
		return fallback
	}
	return v
}
