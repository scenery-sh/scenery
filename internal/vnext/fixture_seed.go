package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FixtureSeedPlan is the deterministic SQL projection of one fixture for one
// selected environment. Deployment providers and the local db seed command use
// the same projection.
type FixtureSeedPlan struct {
	Address     string `json:"address"`
	DataSource  string `json:"data_source"`
	Database    string `json:"database"`
	Environment string `json:"environment"`
	Path        string `json:"path"`
	SQL         string `json:"sql"`
	SHA256      string `json:"sha256"`
}

// BuildFixtureSeedPlans projects validated edition-2027 fixtures into bounded
// PostgreSQL statements. It never connects to a provider or mutates state.
func BuildFixtureSeedPlans(result *Result, environment string) ([]FixtureSeedPlan, error) {
	environment = strings.TrimSpace(environment)
	if result == nil || result.Manifest == nil || !result.Valid() {
		return nil, fmt.Errorf("fixture planning requires a valid edition-2027 contract")
	}
	if environment == "" {
		return nil, fmt.Errorf("fixture planning requires an environment")
	}
	resources := resourcesByAddress(result.Manifest)
	var plans []FixtureSeedPlan
	for _, fixture := range result.Manifest.Resources {
		if fixture.Kind != "scenery.fixture/v1" || !containsFixtureEnvironment(stringValues(fixture.Spec["environments"]), environment) {
			continue
		}
		entity := resources[resolveResourceRef(fixture, refString(fixture.Spec["entity"]), "entity")]
		dataSource := resources[resolveResourceRef(entity, refString(entity.Spec["data_source"]), "data_source")]
		record := resources[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
		if entity.Kind != "scenery.entity/v1" || dataSource.Kind != "scenery.data-source/v1" || record.Kind != "scenery.record/v1" {
			return nil, fmt.Errorf("fixture %s has unresolved persistence resources", fixture.Address)
		}
		sql, err := renderFixtureSQL(fixture, entity, record, resources)
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", fixture.Address, err)
		}
		database := dataSource.Name
		if config, _ := dataSource.Spec["config"].(map[string]any); stringValue(config["database"]) != "" {
			database = stringValue(config["database"])
		}
		digest := sha256.Sum256([]byte(sql))
		plans = append(plans, FixtureSeedPlan{
			Address: fixture.Address, DataSource: dataSource.Address, Database: database, Environment: environment,
			Path: ".scenery/vnext/fixtures/" + strings.ReplaceAll(fixture.Address, "/", "_") + "." + environment + ".sql",
			SQL:  sql, SHA256: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Address < plans[j].Address })
	return plans, nil
}

type fixtureColumn struct {
	Name       string
	Column     string
	PrimaryKey bool
	Field      map[string]any
}

func renderFixtureSQL(fixture, entity, record Resource, resources map[string]Resource) (string, error) {
	mapping, _ := entity.Spec["mapping"].(map[string]any)
	relation := stringValue(mapping["relation"])
	schema := stringValue(mapping["schema"])
	if !fixtureSQLIdentifier.MatchString(relation) || schema != "" && !fixtureSQLIdentifier.MatchString(schema) {
		return "", fmt.Errorf("entity relation is not a portable SQL identifier")
	}
	columns := make([]fixtureColumn, 0)
	known := map[string]fixtureColumn{}
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	for _, field := range namedChildren(entity.Spec, "field") {
		name := stringValue(field["name"])
		column := fixtureColumn{Name: name, Column: stringValue(field["column"]), PrimaryKey: field["primary_key"] == true, Field: fields[name]}
		if !fixtureSQLIdentifier.MatchString(column.Name) || !fixtureSQLIdentifier.MatchString(column.Column) {
			return "", fmt.Errorf("entity field mapping is not a portable SQL identifier")
		}
		columns = append(columns, column)
		known[column.Name] = column
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].Name < columns[j].Name })
	var primary []fixtureColumn
	for _, column := range columns {
		if column.PrimaryKey {
			primary = append(primary, column)
		}
	}
	if len(primary) == 0 {
		return "", fmt.Errorf("entity has no primary key")
	}
	rows, _ := fixture.Spec["values"].([]any)
	var output strings.Builder
	fmt.Fprintf(&output, "-- scenery fixture %s (%s)\n", fixture.Address, stringValue(fixture.Spec["mode"]))
	for index, raw := range rows {
		row, _ := raw.(map[string]any)
		selected := make([]fixtureColumn, 0, len(row))
		for name := range row {
			column, ok := known[name]
			if !ok {
				return "", fmt.Errorf("row %d contains unmapped field %s", index, name)
			}
			selected = append(selected, column)
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
		if len(selected) == 0 {
			return "", fmt.Errorf("row %d has no persisted fields", index)
		}
		quotedColumns, literals := make([]string, len(selected)), make([]string, len(selected))
		for selectedIndex, column := range selected {
			if err := validateFixtureFieldValue(row[column.Name], column.Field, record.Module, resources); err != nil {
				return "", fmt.Errorf("row %d field %s: %w", index, column.Name, err)
			}
			literal, err := fixtureSQLLiteralForType(row[column.Name], typeExpressionText(column.Field["type"]), record.Module, resources)
			if err != nil {
				return "", fmt.Errorf("row %d field %s: %w", index, column.Name, err)
			}
			quotedColumns[selectedIndex], literals[selectedIndex] = quoteFixtureSQLIdentifier(column.Column), literal
		}
		fmt.Fprintf(&output, "INSERT INTO %s (%s) VALUES (%s)", fixtureSQLRelation(schema, relation), strings.Join(quotedColumns, ", "), strings.Join(literals, ", "))
		mode := stringValue(fixture.Spec["mode"])
		if mode == "upsert" || mode == "replace" {
			keys := make([]string, len(primary))
			for keyIndex, column := range primary {
				if _, ok := row[column.Name]; !ok {
					return "", fmt.Errorf("row %d is missing primary key field %s", index, column.Name)
				}
				keys[keyIndex] = quoteFixtureSQLIdentifier(column.Column)
			}
			assignments := fixtureConflictAssignments(mode, columns, row)
			fmt.Fprintf(&output, " ON CONFLICT (%s) ", strings.Join(keys, ", "))
			if len(assignments) == 0 {
				output.WriteString("DO NOTHING")
			} else {
				output.WriteString("DO UPDATE SET " + strings.Join(assignments, ", "))
			}
		}
		output.WriteString(";\n")
	}
	return output.String(), nil
}

func fixtureConflictAssignments(mode string, columns []fixtureColumn, row map[string]any) []string {
	var assignments []string
	for _, column := range columns {
		if column.PrimaryKey {
			continue
		}
		quoted := quoteFixtureSQLIdentifier(column.Column)
		if _, present := row[column.Name]; present {
			assignments = append(assignments, quoted+" = EXCLUDED."+quoted)
		} else if mode == "replace" {
			assignments = append(assignments, quoted+" = DEFAULT")
		}
	}
	return assignments
}

var fixtureSQLIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func fixtureSQLRelation(schema, relation string) string {
	if schema == "" {
		return quoteFixtureSQLIdentifier(relation)
	}
	return quoteFixtureSQLIdentifier(schema) + "." + quoteFixtureSQLIdentifier(relation)
}

func quoteFixtureSQLIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func fixtureSQLLiteral(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if typed {
			return "TRUE", nil
		}
		return "FALSE", nil
	case string:
		return quoteFixtureSQLString(typed), nil
	case map[string]any:
		if kind := stringValue(typed["$scalar"]); kind != "" {
			field := "value"
			if kind == "duration" {
				field = "nanoseconds"
			} else if kind == "size" {
				field = "bytes"
			}
			text := fmt.Sprint(typed[field])
			if (kind == "int" || kind == "decimal") && fixtureSQLNumber.MatchString(text) {
				return text, nil
			}
			return quoteFixtureSQLString(text), nil
		}
		if reference := refString(typed); reference != "" {
			return quoteFixtureSQLString(lastRef(reference)), nil
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return quoteFixtureSQLString(string(encoded)), nil
	case []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return quoteFixtureSQLString(string(encoded)), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported fixture value type %T", value)
	}
}

func fixtureSQLLiteralForType(value any, typeExpression, module string, resources map[string]Resource) (string, error) {
	for _, wrapper := range []string{"optional", "nullable"} {
		if inner, ok := wrappedFixtureType(typeExpression, wrapper); ok {
			if value == nil {
				return "NULL", nil
			}
			return fixtureSQLLiteralForType(value, inner, module, resources)
		}
	}
	if enum := resources[namedFixtureTypeAddress(typeExpression, module)]; enum.Kind == "scenery.enum/v1" {
		candidate := stringValue(value)
		if reference := refString(value); reference != "" {
			candidate = lastRef(reference)
		}
		for _, item := range namedChildren(enum.Spec, "value") {
			name := stringValue(item["name"])
			if candidate == name || candidate == wireName(item, name) {
				return quoteFixtureSQLString(wireName(item, name)), nil
			}
		}
	}
	return fixtureSQLLiteral(value)
}

var fixtureSQLNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func quoteFixtureSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func containsFixtureEnvironment(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
