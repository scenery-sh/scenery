package objectstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const defaultSearchWeight = "D"

func searchConfig(fieldType FieldType, req CreateFieldRequest) (bool, string, error) {
	weight, err := normalizeSearchWeight(req.SearchWeight)
	if err != nil && (req.Searchable || strings.TrimSpace(req.SearchWeight) != "") {
		return false, "", err
	}
	if !req.Searchable {
		return false, defaultSearchWeight, nil
	}
	if err := validateSearchableFieldType(fieldType); err != nil {
		return false, "", err
	}
	return true, weight, nil
}

func normalizeSearchWeight(raw string) (string, error) {
	weight := strings.ToUpper(strings.TrimSpace(raw))
	if weight == "" {
		weight = defaultSearchWeight
	}
	switch weight {
	case "A", "B", "C", "D":
		return weight, nil
	default:
		return "", fmt.Errorf("search weight %q is not supported; use A, B, C, or D", raw)
	}
}

func validateSearchableFieldType(fieldType FieldType) error {
	switch fieldType {
	case FieldText, FieldRichText, FieldSelect, FieldMultiSelect, FieldFullName, FieldAddress, FieldEmails, FieldPhones:
		return nil
	default:
		return fmt.Errorf("field type %s cannot be marked searchable", fieldType)
	}
}

func searchableFields(state *metadataState) []*Field {
	fields := make([]*Field, 0, len(state.Fields))
	for _, field := range state.Fields {
		if field.IsSearchable {
			fields = append(fields, field)
		}
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Name < fields[j].Name
	})
	return fields
}

func (s *Store) upsertSearchDocument(ctx context.Context, q Queryer, state *metadataState, recordID string, record Record) error {
	fields := searchableFields(state)
	args := []any{state.Tenant.ID, state.Object.ID, recordID, s.now()}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		text := searchableTextForValue(record[field.Name])
		if text == "" {
			continue
		}
		weight, err := normalizeSearchWeight(field.SearchWeight)
		if err != nil {
			return err
		}
		args = append(args, text)
		parts = append(parts, fmt.Sprintf("setweight(to_tsvector('simple', coalesce($%d::text, '')), '%s')", len(args), weight))
	}
	document := "''::tsvector"
	if len(parts) > 0 {
		document = strings.Join(parts, " || ")
	}
	_, err := q.Exec(ctx, `
		insert into `+qualifiedIdent(MetadataSchema, "search_documents")+` (
			tenant_id, object_id, record_id, document, updated_at
		) values ($1::uuid, $2::uuid, $3::uuid, `+document+`, $4)
		on conflict (tenant_id, object_id, record_id) do update
		set document = excluded.document, updated_at = excluded.updated_at
	`, args...)
	if err != nil {
		return fmt.Errorf("upsert search document for object %s record %s: %w", state.Object.NameSingular, recordID, err)
	}
	return nil
}

func deleteSearchDocument(ctx context.Context, q Queryer, state *metadataState, recordID string) error {
	_, err := q.Exec(ctx, `
		delete from `+qualifiedIdent(MetadataSchema, "search_documents")+`
		where tenant_id = $1::uuid and object_id = $2::uuid and record_id = $3::uuid
	`, state.Tenant.ID, state.Object.ID, recordID)
	if err != nil {
		return fmt.Errorf("delete search document for object %s record %s: %w", state.Object.NameSingular, recordID, err)
	}
	return nil
}

func compileSearchFilter(state *metadataState, filter *Filter, args *[]any) (string, error) {
	if strings.TrimSpace(filter.Field) != "" {
		return "", fmt.Errorf("search filter is object-wide and does not accept field %q", filter.Field)
	}
	value := strings.TrimSpace(fmt.Sprint(filter.Value))
	if value == "" {
		return "", fmt.Errorf("search filter requires a non-empty value")
	}
	if len(searchableFields(state)) == 0 {
		return "", fmt.Errorf("object %s has no searchable fields", state.Object.NameSingular)
	}
	*args = append(*args, state.Object.ID)
	objectArg := fmt.Sprintf("$%d", len(*args))
	*args = append(*args, value)
	queryArg := fmt.Sprintf("$%d", len(*args))
	return `exists (
		select 1
		from ` + qualifiedIdent(MetadataSchema, "search_documents") + ` sd
		where sd.tenant_id = ` + querySourceAlias + `.tenant_id
		  and sd.object_id = ` + objectArg + `::uuid
		  and sd.record_id = ` + querySourceAlias + `.id
		  and sd.document @@ websearch_to_tsquery('simple', ` + queryArg + `::text)
	)`, nil
}

func recordMatchesSearch(state *metadataState, record Record, value any) bool {
	search := strings.TrimSpace(fmt.Sprint(value))
	if search == "" {
		return false
	}
	haystackParts := make([]string, 0, len(state.Fields))
	for _, field := range searchableFields(state) {
		text := searchableTextForValue(record[field.Name])
		if text != "" {
			haystackParts = append(haystackParts, text)
		}
	}
	haystack := strings.ToLower(strings.Join(haystackParts, " "))
	for _, term := range strings.Fields(strings.ToLower(search)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return haystack != ""
}

func searchableTextForValue(value any) string {
	var parts []string
	appendSearchText(&parts, value)
	return strings.Join(parts, " ")
}

func appendSearchText(parts *[]string, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		if text := strings.TrimSpace(v); text != "" {
			*parts = append(*parts, text)
		}
	case []string:
		for _, item := range v {
			appendSearchText(parts, item)
		}
	case []any:
		for _, item := range v {
			appendSearchText(parts, item)
		}
	case Record:
		appendMapSearchText(parts, map[string]any(v))
	case map[string]any:
		appendMapSearchText(parts, v)
	default:
		text := strings.TrimSpace(fmt.Sprint(v))
		if text != "" {
			*parts = append(*parts, text)
		}
	}
}

func appendMapSearchText(parts *[]string, value map[string]any) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		appendSearchText(parts, value[key])
	}
}
