package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func validateDataSemantics(root string, resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	productionFixturesAllowed := productionFixturePolicy(resources)
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.data-source":
			provider := byAddress[resolveResourceRef(resource, refString(resource.Spec["provider"]), "provider")]
			if provider.Kind != "scenery.provider" || !dataLifecycleAllowed(stringValue(resource.Spec["lifecycle"])) {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2505", "data source requires a typed provider and managed, external, attached, or ephemeral lifecycle", resource))
			}
			if duplicate := duplicateStrings(stringValues(resource.Spec["require_capabilities"])); duplicate != "" {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2505", "data source repeats required capability "+duplicate, resource))
			}
		case "scenery.entity":
			diagnostics = append(diagnostics, validateEntitySemantics(byAddress, resource)...)
		case "scenery.view":
			diagnostics = append(diagnostics, validateViewSemantics(root, byAddress, resource)...)
		case "scenery.crud":
			diagnostics = append(diagnostics, validateCRUDSemantics(byAddress, resource)...)
		case "scenery.fixture":
			diagnostics = append(diagnostics, validateFixtureSemantics(byAddress, resource, productionFixturesAllowed)...)
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}

func validateEntitySemantics(resources map[string]Resource, entity Resource) []Diagnostic {
	record := resources[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
	dataSource := resources[resolveResourceRef(entity, refString(entity.Spec["data_source"]), "data_source")]
	if record.Kind != "scenery.record" || dataSource.Kind != "scenery.data-source" {
		return []Diagnostic{dataDiagnostic("SCN2506", "entity type and data_source must resolve to a record and data source", entity)}
	}
	mapping, _ := entity.Spec["mapping"].(map[string]any)
	relation, schema := stringValue(mapping["relation"]), stringValue(mapping["schema"])
	if !fixtureSQLIdentifier.MatchString(relation) || schema != "" && !fixtureSQLIdentifier.MatchString(schema) {
		return []Diagnostic{dataDiagnostic("SCN2506", "entity mapping requires portable relation and schema identifiers", entity)}
	}
	recordFields := map[string]bool{}
	for _, field := range namedChildren(record.Spec, "field") {
		recordFields[stringValue(field["name"])] = true
	}
	seenNames, seenColumns := map[string]bool{}, map[string]bool{}
	primaryKeys, tenantKeys := 0, 0
	var diagnostics []Diagnostic
	for _, field := range namedChildren(entity.Spec, "field") {
		name, column := stringValue(field["name"]), stringValue(field["column"])
		if name == "" || !recordFields[name] || seenNames[name] || !fixtureSQLIdentifier.MatchString(column) || seenColumns[column] {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2506", "entity fields require unique record fields and columns", entity))
		}
		seenNames[name], seenColumns[column] = true, true
		if field["primary_key"] == true {
			primaryKeys++
		}
		if field["tenant_key"] == true {
			tenantKeys++
			if field["immutable"] != true {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2506", "entity tenant keys must be immutable", entity))
			}
		}
		if defaultSpec, ok := field["default"].(map[string]any); ok {
			strategy := stringValue(defaultSpec["strategy"])
			if strategy != "uuid_v7" && strategy != "current_datetime" && strategy != "provider" {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2506", "entity field default strategy is unsupported", entity))
			}
		}
	}
	if primaryKeys == 0 {
		diagnostics = append(diagnostics, dataDiagnostic("SCN2506", "entity requires at least one primary key", entity))
	}
	if tenantKeys > 1 {
		diagnostics = append(diagnostics, dataDiagnostic("SCN2506", "entity may declare at most one tenant key", entity))
	}
	return diagnostics
}

func validateViewSemantics(root string, resources map[string]Resource, view Resource) []Diagnostic {
	if resources[resolveResourceRef(view, refString(view.Spec["data_source"]), "data_source")].Kind != "scenery.data-source" {
		return []Diagnostic{dataDiagnostic("SCN2507", "view data_source must resolve to a data source", view)}
	}
	implementation, _ := view.Spec["implementation"].(map[string]any)
	kind, file, name := stringValue(implementation["kind"]), stringValue(implementation["file"]), stringValue(implementation["name"])
	if kind == "" || file == "" || name == "" {
		return []Diagnostic{dataDiagnostic("SCN2507", "view implementation requires kind, file, and name", view)}
	}
	if root == "" {
		return nil
	}
	path, err := dataImplementationPath(root, resources, view, file)
	if err != nil {
		return []Diagnostic{dataDiagnostic("SCN2507", err.Error(), view)}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return []Diagnostic{dataDiagnostic("SCN2507", "view implementation file is unavailable", view)}
	}
	if kind != "sql_query" {
		return []Diagnostic{dataDiagnostic("SCN2507", "view implementation kind is unsupported", view)}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []Diagnostic{dataDiagnostic("SCN2507", "view implementation file is unavailable", view)}
	}
	if err := verifySQLViewColumns(string(data), name, view, resources); err != nil {
		return []Diagnostic{dataDiagnostic("SCN2509", err.Error(), view)}
	}
	return nil
}

var sqlSelectProjectionPattern = regexp.MustCompile(`(?is)\bselect\b(.*?)\bfrom\b`)

func verifySQLViewColumns(source, queryName string, view Resource, resources map[string]Resource) error {
	query := source
	marker := "-- name: " + queryName
	if index := strings.Index(source, marker); index >= 0 {
		query = source[index+len(marker):]
		if next := strings.Index(query, "-- name:"); next >= 0 {
			query = query[:next]
		}
	} else if strings.Contains(source, "-- name:") {
		return fmt.Errorf("view query %s is not declared in the SQL implementation file", queryName)
	}
	match := sqlSelectProjectionPattern.FindStringSubmatch(query)
	if len(match) != 2 {
		return fmt.Errorf("view SQL query must contain a verifiable SELECT projection")
	}
	projection := strings.TrimSpace(match[1])
	if projection == "*" || strings.Contains(projection, ".*") {
		return fmt.Errorf("view SQL query cannot verify wildcard result columns")
	}
	actual := map[string]bool{}
	for _, expression := range splitSQLProjection(projection) {
		name := sqlProjectionName(expression)
		if name == "" || actual[name] {
			return fmt.Errorf("view SQL query has an ambiguous or duplicate result column")
		}
		actual[name] = true
	}
	resultType := typeExpressionText(view.Spec["result"])
	for _, wrapper := range []string{"list", "optional", "nullable", "set"} {
		prefix := wrapper + "("
		if strings.HasPrefix(resultType, prefix) && strings.HasSuffix(resultType, ")") {
			resultType = strings.TrimSpace(resultType[len(prefix) : len(resultType)-1])
		}
	}
	recordAddress := resolveResourceRef(view, resultType, "record")
	if strings.HasPrefix(resultType, "record.") {
		recordAddress = resourceAddress(view.Module, "record", strings.TrimPrefix(resultType, "record."))
	}
	record := resources[recordAddress]
	if record.Kind != "scenery.record" {
		return fmt.Errorf("view result must resolve to a record for SQL column verification")
	}
	expected := map[string]bool{}
	for _, field := range namedChildren(record.Spec, "field") {
		expected[wireName(field, stringValue(field["name"]))] = true
	}
	if !semanticEqual(expected, actual) {
		return fmt.Errorf("view SQL result columns do not match declared result record: got %v, want %v", sortedBoolKeys(actual), sortedBoolKeys(expected))
	}
	return nil
}

func splitSQLProjection(source string) []string {
	depth, start := 0, 0
	var result []string
	for index, character := range source {
		switch character {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				result = append(result, strings.TrimSpace(source[start:index]))
				start = index + 1
			}
		}
	}
	return append(result, strings.TrimSpace(source[start:]))
}

func sqlProjectionName(expression string) string {
	fields := strings.Fields(strings.TrimSpace(expression))
	if len(fields) == 0 {
		return ""
	}
	name := fields[len(fields)-1]
	if len(fields) >= 3 && strings.EqualFold(fields[len(fields)-2], "as") {
		name = fields[len(fields)-1]
	} else if len(fields) == 1 {
		parts := strings.Split(name, ".")
		name = parts[len(parts)-1]
	}
	return strings.Trim(strings.TrimSpace(name), "\"`")
}

func validateCRUDSemantics(resources map[string]Resource, crud Resource) []Diagnostic {
	actions := stringValues(crud.Spec["actions"])
	seen := map[string]bool{}
	for _, action := range actions {
		if !crudActions[action] || seen[action] {
			return []Diagnostic{dataDiagnostic("SCN2504", "CRUD actions must be unique supported actions", crud)}
		}
		seen[action] = true
	}
	if len(seen) == 0 || refOrString(crud.Spec["implementation"]) != "std.crud.entity" {
		return []Diagnostic{dataDiagnostic("SCN2504", "CRUD requires actions and a typed implementation", crud)}
	}
	execution, _ := crud.Spec["execution"].(map[string]any)
	if mode := stringValue(execution["mode"]); mode != "direct" && mode != "durable" {
		return []Diagnostic{dataDiagnostic("SCN2504", "CRUD execution mode must be direct or durable", crud)}
	}
	if httpSpec, ok := crud.Spec["http"].(map[string]any); ok && missingAny(httpSpec, "path", "codec_profile", "gateway", "authentication", "authorization", "pipeline") {
		return []Diagnostic{dataDiagnostic("SCN2504", "CRUD HTTP projection requires path, codec, gateway, authentication, authorization, and pipeline", crud)}
	}
	list, ok := crud.Spec["list"].(map[string]any)
	if !ok {
		return nil
	}
	if !seen["list"] {
		return []Diagnostic{dataDiagnostic("SCN2512", "CRUD list capabilities require the list action", crud)}
	}
	entity := resources[resolveResourceRef(crud, refString(crud.Spec["entity"]), "entity")]
	record := resources[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
	recordFields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		recordFields[stringValue(field["name"])] = field
	}
	fields := map[string]map[string]any{}
	for _, mapping := range namedChildren(entity.Spec, "field") {
		name := stringValue(mapping["name"])
		fields[name] = recordFields[name]
	}
	var diagnostics []Diagnostic
	filters, sorts := stringValues(list["filters"]), stringValues(list["sorts"])
	for _, name := range filters {
		field := fields[name]
		if field == nil {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2512", "CRUD list filter references unknown entity field "+name, crud))
			continue
		}
		typeName := unwrapCRUDListType(typeExpression(field["type"]))
		if typeName != "datetime" && resources[namedFixtureTypeAddress(typeName, record.Module)].Kind != "scenery.enum" {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2513", "CRUD list filter "+name+" must be an enum or datetime field", crud))
		}
	}
	for _, name := range sorts {
		field := fields[name]
		if field == nil {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2512", "CRUD list sort references unknown entity field "+name, crud))
			continue
		}
		if !crudListScalarType(unwrapCRUDListType(typeExpression(field["type"])), record.Module, resources) {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2514", "CRUD list sort "+name+" must be a scalar field", crud))
		}
	}
	if defaultSort, present := list["default_sort"]; present {
		value, valid := defaultSort.(map[string]any)
		field, direction := stringValue(value["field"]), stringValue(value["direction"])
		if !valid || !containsDataString(sorts, field) || direction != "asc" && direction != "desc" || len(value) != 2 {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2514", "CRUD default_sort requires exactly an allowlisted field and asc or desc direction", crud))
		}
	}
	return diagnostics
}

func unwrapCRUDListType(value string) string {
	for {
		unwrapped := false
		for _, wrapper := range []string{"optional", "nullable"} {
			if inner, ok := wrappedFixtureType(value, wrapper); ok {
				value, unwrapped = inner, true
				break
			}
		}
		if !unwrapped {
			return strings.TrimSpace(value)
		}
	}
}

func crudListScalarType(value, module string, resources map[string]Resource) bool {
	if resources[namedFixtureTypeAddress(value, module)].Kind == "scenery.enum" {
		return true
	}
	switch value {
	case "bool", "int", "int32", "uint32", "int64", "uint64", "decimal", "float32", "float64", "string", "uuid", "date", "datetime", "duration", "size", "url", "relative_path":
		return true
	default:
		return false
	}
}

func containsDataString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateFixtureSemantics(resources map[string]Resource, fixture Resource, productionAllowed map[string]bool) []Diagnostic {
	entity := resources[resolveResourceRef(fixture, refString(fixture.Spec["entity"]), "entity")]
	if entity.Kind != "scenery.entity" {
		return []Diagnostic{dataDiagnostic("SCN2508", "fixture entity must resolve to an entity", fixture)}
	}
	mode := stringValue(fixture.Spec["mode"])
	if mode != "insert" && mode != "upsert" && mode != "replace" {
		return []Diagnostic{dataDiagnostic("SCN2508", "fixture mode must be insert, upsert, or replace", fixture)}
	}
	environments := stringValues(fixture.Spec["environments"])
	if len(environments) == 0 || duplicateStrings(environments) != "" {
		return []Diagnostic{dataDiagnostic("SCN2508", "fixture environments must be non-empty and unique", fixture)}
	}
	for _, environment := range environments {
		if strings.TrimSpace(environment) == "" {
			return []Diagnostic{dataDiagnostic("SCN2508", "fixture environments must not contain an empty name", fixture)}
		}
		if environment == "production" && !productionAllowed[entity.Address] && !productionAllowed["*"] {
			return []Diagnostic{dataDiagnostic("SCN2508", "production fixture requires an explicit deployment fixture policy", fixture)}
		}
	}
	record := resources[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
	fields := map[string]map[string]any{}
	defaults := map[string]bool{}
	persisted := map[string]bool{}
	primary := map[string]bool{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	for _, mapping := range namedChildren(entity.Spec, "field") {
		name := stringValue(mapping["name"])
		defaults[name] = mapping["default"] != nil
		persisted[name] = true
		primary[name] = mapping["primary_key"] == true
	}
	values, ok := fixture.Spec["values"].([]any)
	if !ok || len(values) == 0 {
		return []Diagnostic{dataDiagnostic("SCN2508", "fixture values must contain typed rows", fixture)}
	}
	for _, value := range values {
		row, ok := value.(map[string]any)
		if !ok {
			return []Diagnostic{dataDiagnostic("SCN2508", "fixture row must be an object", fixture)}
		}
		for name := range row {
			if fields[name] == nil {
				return []Diagnostic{dataDiagnostic("SCN2508", "fixture row contains unknown field "+name, fixture)}
			}
			if !persisted[name] {
				return []Diagnostic{dataDiagnostic("SCN2508", "fixture row contains non-persisted field "+name, fixture)}
			}
			if err := validateFixtureFieldValue(row[name], fields[name], record.Module, resources); err != nil {
				return []Diagnostic{dataDiagnostic("SCN2508", "fixture field "+name+" is invalid: "+err.Error(), fixture)}
			}
		}
		for name, field := range fields {
			if _, present := row[name]; !present && !isOptionalType(field["type"]) && !defaults[name] {
				return []Diagnostic{dataDiagnostic("SCN2508", "fixture row is missing required field "+name, fixture)}
			}
		}
		if mode == "upsert" || mode == "replace" {
			for name := range primary {
				if primary[name] && row[name] == nil {
					return []Diagnostic{dataDiagnostic("SCN2508", "fixture "+mode+" row is missing primary key field "+name, fixture)}
				}
			}
		}
	}
	return nil
}

func productionFixturePolicy(resources []Resource) map[string]bool {
	allowed := map[string]bool{}
	for _, deployment := range resources {
		if deployment.Kind != "scenery.deployment" {
			continue
		}
		for _, policy := range namedChildren(deployment.Spec, "fixture_policy") {
			if policy["allow_production"] != true {
				continue
			}
			target := refString(policy["entity"])
			if target == "" {
				allowed["*"] = true
			} else {
				allowed[resolveResourceRef(deployment, target, "entity")] = true
			}
		}
	}
	return allowed
}

func enrichDataImplementationDigests(root string, resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: result})
	var diagnostics []Diagnostic
	for index := range result {
		resource := &result[index]
		if resource.Kind != "scenery.view" {
			continue
		}
		implementation, _ := resource.Spec["implementation"].(map[string]any)
		file := stringValue(implementation["file"])
		if file == "" {
			continue
		}
		path, err := dataImplementationPath(root, byAddress, *resource, file)
		if err != nil {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2507", err.Error(), *resource))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2507", "view implementation file is unavailable", *resource))
			continue
		}
		sum := sha256.Sum256(data)
		resource.Spec = cloneMapValue(resource.Spec)
		resource.Spec["implementation_digest"] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return result, diagnostics
}

func dataImplementationPath(root string, resources map[string]Resource, resource Resource, declared string) (string, error) {
	if filepath.IsAbs(declared) || strings.HasPrefix(filepath.Clean(declared), "..") {
		return "", fmt.Errorf("view implementation file must be workspace-relative")
	}
	base := root
	if resource.Module != "app" {
		module := resources[moduleResourceAddress(resource.Module)]
		source := stringValue(module.Spec["workspace_package_root"])
		if source == "" {
			source = stringValue(module.Spec["source"])
		}
		if source == "" {
			return "", fmt.Errorf("view module source is unavailable")
		}
		base = filepath.Join(root, filepath.FromSlash(source))
	}
	path := filepath.Clean(filepath.Join(base, filepath.FromSlash(declared)))
	if !pathWithin(root, path) {
		return "", fmt.Errorf("view implementation file escapes the workspace")
	}
	if err := rejectPathSymlinks(root, path); err != nil {
		return "", fmt.Errorf("view implementation file is not symlink-safe: %w", err)
	}
	return path, nil
}

func dataLifecycleAllowed(value string) bool {
	return value == "managed" || value == "external" || value == "attached" || value == "ephemeral"
}

func duplicateStrings(values []string) string {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return value
		}
		seen[value] = true
	}
	return ""
}

func dataDiagnostic(code, message string, resource Resource) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address}
}
