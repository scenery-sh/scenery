package compiler

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	reactExportPattern      = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	tablePageRouteParameter = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
)

const tablePageRendererModule = "scenery.ui.table_page"

func expandTablePageResources(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	occupied := map[string]bool{}
	for _, resource := range resources {
		occupied[resource.Address] = true
	}
	var diagnostics []Diagnostic
	for _, table := range resources {
		if table.Kind != "scenery.table-page" || table.Origin.Kind == "expanded" {
			continue
		}
		source := byAddress[resolveResourceRef(table, refString(table.Spec["source"]), "crud")]
		load := ""
		bindingSource := false
		switch source.Kind {
		case "scenery.crud":
			if source.Spec["list"] == nil {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page CRUD source must declare a list contract", table))
				continue
			}
			load = resourceAddress(source.Module, "binding", source.Name+"_list_internal")
			if byAddress[load].Kind != "scenery.binding" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page CRUD source has no generated list binding", table))
				continue
			}
		case "scenery.binding":
			load = resourceAddress(table.Module, "binding", table.Name+"_load_internal")
			bindingSource = true
		default:
			diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page source must resolve to a CRUD list contract or call-delivery HTTP binding", table))
			continue
		}
		lineage := func(output, key string) Origin {
			return Origin{Kind: "expanded", SourceID: table.Origin.SourceID, ModuleChain: append([]string(nil), table.Origin.ModuleChain...), ExpansionLineage: []ExpansionStep{{Generator: table.Address, GeneratorSchemaRevision: "scenery.table-page", Key: key, SourceRange: table.Origin.DeclarationRange, ParentAddress: table.Address, Output: output}}}
		}
		pageAddress := resourceAddress(table.Module, "page", table.Name)
		rendererAddress := resourceAddress(table.Module, "renderer", table.Name+"_web")
		generated := []Resource{
			{Address: pageAddress, Module: table.Module, Name: table.Name, Kind: "scenery.page", Origin: lineage(pageAddress, "page"), Spec: map[string]any{"path": table.Spec["path"], "load": map[string]any{"$ref": load}}},
			{Address: rendererAddress, Module: table.Module, Name: table.Name + "_web", Kind: "scenery.renderer", Origin: lineage(rendererAddress, "renderer"), Spec: map[string]any{"page": map[string]any{"$ref": pageAddress}, "runtime": "web", "module": tablePageRendererModule, "config": cloneMapValue(table.Spec)}},
		}
		if bindingSource {
			generated = append(generated, Resource{
				Address: load, Module: table.Module, Name: table.Name + "_load_internal", Kind: "scenery.binding", Origin: lineage(load, "load"),
				Spec: map[string]any{
					"operation":      map[string]any{"$ref": resolveResourceRef(source, refString(source.Spec["operation"]), "operation")},
					"execution":      map[string]any{"$ref": resolveResourceRef(source, refString(source.Spec["execution"]), "execution")},
					"protocol":       "internal",
					"delivery":       "call",
					"exposure":       "application",
					"authentication": map[string]any{"$ref": "std.authentication.inherit"},
					"authorization":  map[string]any{"$ref": "std.authorization.public"},
					"pipeline":       map[string]any{"$ref": "std.pipeline.empty"},
					"internal":       map[string]any{"visibility": "application", "principal": "inherit"},
				},
			})
		}
		collision := false
		for _, resource := range generated {
			if occupied[resource.Address] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2614", Severity: "error", Message: "table_page derived address collides with " + resource.Address, Address: table.Address, Related: []Related{{Address: resource.Address}}})
				collision = true
			}
		}
		if collision {
			continue
		}
		for index := range generated {
			markExpansionFieldProvenance(&generated[index], table)
			occupied[generated[index].Address] = true
			byAddress[generated[index].Address] = generated[index]
			result = append(result, generated[index])
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, diagnostics
}

func validateReactComponent(root string, resources map[string]Resource, component Resource) []Diagnostic {
	module, export := stringValue(component.Spec["module"]), stringValue(component.Spec["export"])
	if module == "" || !reactExportPattern.MatchString(export) {
		return []Diagnostic{uiDiagnostic("SCN2607", "react_component requires a workspace-relative module and named export", component)}
	}
	if root != "" {
		probe := component
		probe.Spec = map[string]any{"module": module}
		if _, err := rendererModulePath(root, resources, probe); err != nil {
			return []Diagnostic{uiDiagnostic("SCN2607", strings.ReplaceAll(err.Error(), "renderer", "react_component"), component)}
		}
	}
	return nil
}

func validateTablePage(resources map[string]Resource, table Resource) []Diagnostic {
	diagnostics := validateGeneratedPageRoute(resources, table)
	contract, sourceDiagnostics := resolveTablePageSource(resources, table)
	diagnostics = append(diagnostics, sourceDiagnostics...)
	if contract.record.Kind != "scenery.record" {
		return diagnostics
	}
	record := contract.record
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	allowedFilters, allowedSorts := contract.allowedFilters, contract.allowedSorts
	seenColumns := map[string]bool{}
	visibleColumns := 0
	for _, column := range namedChildren(table.Spec, "column") {
		name, appearance := stringValue(column["name"]), defaultString(stringValue(column["appearance"]), "auto")
		if fields[name] == nil || seenColumns[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2609", "table_page columns require unique entity fields", table))
		}
		seenColumns[name] = true
		if column["hidden"] != true {
			visibleColumns++
		}
		if !oneOfString(appearance, "auto", "text", "number", "datetime", "badge") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2609", "table_page column appearance is invalid", table))
		}
		diagnostics = append(diagnostics, validateTablePageComponent(resources, table, column["component"])...)
		if column["status_map"] != nil {
			expression := ""
			if fields[name] != nil {
				expression = unwrapCRUDListType(typeExpression(fields[name]["type"]))
			}
			if appearance != "badge" || expression != "string" && resources[namedFixtureTypeAddress(expression, record.Module)].Kind != "scenery.enum" || !validStatusMapReference(resources, table, column["status_map"]) {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page column status_map requires a badge string or enum column", table))
			}
		}
	}
	if len(seenColumns) == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page requires at least one column", table))
	}
	if visibleColumns == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2609", "table_page requires at least one visible column", table))
	}
	seenFilters := map[string]bool{}
	for _, filter := range namedChildren(table.Spec, "filter") {
		name := stringValue(filter["name"])
		if fields[name] == nil || seenFilters[name] || !containsDataString(allowedFilters, name) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2610", "table_page filters require unique source-allowlisted row fields", table))
		}
		seenFilters[name] = true
		diagnostics = append(diagnostics, validateTablePageComponent(resources, table, filter["component"])...)
		expression := ""
		if fields[name] != nil {
			expression = unwrapCRUDListType(typeExpression(fields[name]["type"]))
		}
		enumField := resources[namedFixtureTypeAddress(expression, record.Module)].Kind == "scenery.enum"
		if filter["status_map"] != nil {
			if expression != "string" && !enumField || !validStatusMapReference(resources, table, filter["status_map"]) {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page filter status_map requires a string or enum field", table))
			}
		} else if expression == "string" && filter["component"] == nil && filter["hidden"] != true {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page string filters require a status_map for their options", table))
		}
		if filter["pinned"] == true && (filter["hidden"] == true || filter["component"] != nil || expression != "string" && !enumField) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page pinned filters require a generated string or enum selector", table))
		}
	}
	seenSorts, defaults := map[string]bool{}, 0
	for _, sortSpec := range namedChildren(table.Spec, "sort") {
		name := stringValue(sortSpec["name"])
		if fields[name] == nil || seenSorts[name] || !containsDataString(allowedSorts, name) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2610", "table_page sorts require unique source-allowlisted row fields", table))
		}
		seenSorts[name] = true
		if direction := stringValue(sortSpec["default"]); direction != "" {
			defaults++
			if direction != "asc" && direction != "desc" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2610", "table_page default sort direction must be asc or desc", table))
			}
		}
	}
	if defaults > 1 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2610", "table_page may declare one default sort", table))
	}
	groups := namedChildren(table.Spec, "group")
	seenGroups, defaultGroups := map[string]bool{}, 0
	for _, group := range groups {
		name := stringValue(group["name"])
		if fields[name] == nil || seenGroups[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page groups require unique row fields", table))
		}
		seenGroups[name] = true
		if group["default"] == true {
			defaultGroups++
		}
	}
	if len(groups) > 0 && contract.paginated {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page group requires a complete-list data source; paginated pages cannot group", table))
	}
	if defaultGroups > 1 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page may declare one default group", table))
	}
	for _, slot := range []string{"toolbar", "footer", "empty", "row_detail", "row_action"} {
		for _, value := range namedChildren(table.Spec, slot) {
			diagnostics = append(diagnostics, validateTablePageComponent(resources, table, value["component"])...)
		}
	}
	for _, toolbar := range orderedChildren(table.Spec, "toolbar") {
		placement := defaultString(stringValue(toolbar["placement"]), "header")
		if placement != "header" && placement != "content" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page toolbar placement must be header or content", table))
		}
	}
	if len(orderedChildren(table.Spec, "row_detail")) > 0 && len(orderedChildren(table.Spec, "row_action")) > 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page row_action and row_detail are mutually exclusive", table))
	}
	for _, rowDetail := range orderedChildren(table.Spec, "row_detail") {
		presentation := defaultString(stringValue(rowDetail["presentation"]), "inline")
		if presentation != "inline" && presentation != "panel" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page row_detail presentation must be inline or panel", table))
		}
		if rowDetail["panel_width"] != nil {
			width, valid := integerValue(rowDetail["panel_width"])
			if presentation != "panel" || !valid || width < 280 || width > 560 {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page row_detail panel_width requires panel presentation and an integer from 280 to 560", table))
			}
		}
		if presentation == "panel" && rowDetail["dialog"] != nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2623", "table_page row_detail dialog is available only with inline presentation", table))
		}
	}
	diagnostics = append(diagnostics, validateTablePageRowDialog(resources, table, fields)...)
	diagnostics = append(diagnostics, validateTablePageStats(resources, table)...)
	seenActions, seenDialogs, primaryActions := map[string]bool{}, map[string]bool{}, 0
	for _, action := range orderedChildren(table.Spec, "action") {
		name := stringValue(action["name"])
		dialog := resources[resolveResourceRef(table, refString(action["dialog"]), "form_dialog")]
		if name == "" || seenActions[name] || strings.TrimSpace(stringValue(action["label"])) == "" || dialog.Kind != "scenery.form-dialog" || seenDialogs[dialog.Address] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page actions require unique names, labels, and form_dialog references", table))
		}
		seenActions[name] = true
		seenDialogs[dialog.Address] = true
		if action["primary"] == true {
			primaryActions++
		}
	}
	if primaryActions > 1 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page may declare at most one primary action", table))
	}
	pageSize, validPageSize := integerValue(table.Spec["page_size"])
	if !validPageSize || pageSize < 1 || contract.maxPageSize > 0 && contract.maxPageSize < pageSize {
		if contract.maxPageSize > 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2613", fmt.Sprintf("table_page page_size must be between 1 and %d", contract.maxPageSize), table))
		} else {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2613", "table_page page_size must be positive", table))
		}
	}
	for _, match := range tablePageRouteParameter.FindAllStringSubmatch(stringValue(table.Spec["row_link"]), -1) {
		if fields[match[1]] == nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2612", "table_page row_link parameter "+match[1]+" is not an entity field", table))
		}
	}
	return diagnostics
}

type tablePageSourceContract struct {
	record                       Resource
	allowedFilters, allowedSorts []string
	maxPageSize                  int
	paginated                    bool
}

func resolveTablePageSource(resources map[string]Resource, table Resource) (tablePageSourceContract, []Diagnostic) {
	source := resources[resolveResourceRef(table, refString(table.Spec["source"]), "crud")]
	switch source.Kind {
	case "scenery.crud":
		list, ok := source.Spec["list"].(map[string]any)
		if !ok || source.Spec["http"] == nil {
			return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page CRUD source requires list and HTTP projections", table)}
		}
		if strings.TrimSpace(stringValue(table.Spec["items"])) != "" {
			return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page items is only valid with a binding source", table)}
		}
		if len(stringValues(table.Spec["metadata"])) > 0 {
			return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2622", "table_page metadata is available only with a binding source", table)}
		}
		entity := resources[resolveResourceRef(source, refString(source.Spec["entity"]), "entity")]
		record := resources[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
		maxPageSize, _ := integerValue(list["max_page_size"])
		var diagnostics []Diagnostic
		if len(orderedChildren(table.Spec, "pagination")) > 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2624", "table_page page-number pagination is available only with a binding source", table))
		}
		if len(orderedChildren(table.Spec, "query")) > 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page query mapping is available only with a binding source", table))
		}
		seenPredicates := map[string]bool{}
		allowedFilters := stringValues(list["filters"])
		for _, predicate := range namedChildren(table.Spec, "predicate") {
			name := stringValue(predicate["name"])
			rowField := namedChild(record.Spec, "field", name)
			if name == "" || seenPredicates[name] || !containsDataString(allowedFilters, name) || rowField == nil {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page CRUD predicates require unique allowlisted filter input fields", table))
				continue
			}
			seenPredicates[name] = true
			if err := validateFixtureValue(predicate["value"], typeExpression(rowField["type"]), record.Module, resources); err != nil {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page predicate "+name+" value does not match its CRUD filter item type: "+err.Error(), table))
			}
		}
		for _, filter := range namedChildren(table.Spec, "filter") {
			if input := strings.TrimSpace(stringValue(filter["input"])); input != "" && input != stringValue(filter["name"]) {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page CRUD filter input mappings must use the generated same-named list input", table))
			}
		}
		return tablePageSourceContract{
			record: record, allowedFilters: allowedFilters, allowedSorts: stringValues(list["sorts"]),
			maxPageSize: maxPageSize, paginated: true,
		}, diagnostics
	case "scenery.binding":
		return resolveBindingTablePageSource(resources, table, source)
	default:
		return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page source must resolve to a CRUD resource or call-delivery HTTP binding", table)}
	}
}

func resolveBindingTablePageSource(resources map[string]Resource, table, binding Resource) (tablePageSourceContract, []Diagnostic) {
	if stringValue(binding.Spec["protocol"]) != "http" || stringValue(binding.Spec["delivery"]) != "call" {
		return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page binding source must use call-delivery HTTP", table)}
	}
	operation := resources[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
	results := namedChildren(operation.Spec, "result")
	if operation.Kind != "scenery.operation" || len(results) != 1 {
		return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page binding source requires exactly one result record", table)}
	}
	resultRecord := resources[resolveResourceRef(operation, refString(results[0]["type"]), "record")]
	itemsName := strings.TrimSpace(stringValue(table.Spec["items"]))
	itemsField := namedChild(resultRecord.Spec, "field", itemsName)
	itemsType := unwrapCRUDListType(typeExpression(itemsField["type"]))
	itemType, ok := wrappedFixtureType(itemsType, "list")
	if resultRecord.Kind != "scenery.record" || itemsName == "" || !ok {
		return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page binding source requires items naming a list(record) result field", table)}
	}
	record := resources[resolveResourceRef(operation, itemType, "record")]
	if record.Kind != "scenery.record" {
		return tablePageSourceContract{}, []Diagnostic{uiDiagnostic("SCN2608", "table_page binding source items must contain records", table)}
	}
	metadataNames := stringValues(table.Spec["metadata"])
	seenMetadata := map[string]bool{}
	totalName := ""
	if pagination := firstTablePageChild(table.Spec, "pagination"); pagination != nil {
		totalName = strings.TrimSpace(stringValue(pagination["total"]))
	}
	var metadataDiagnostics []Diagnostic
	for _, name := range metadataNames {
		if name == "" || seenMetadata[name] || name == itemsName || name == totalName || namedChild(resultRecord.Spec, "field", name) == nil {
			metadataDiagnostics = append(metadataDiagnostics, uiDiagnostic("SCN2622", "table_page metadata requires unique result fields other than items and pagination total", table))
		}
		seenMetadata[name] = true
	}

	shape := resolveOperationInputShape(resources, operation)
	diagnostics := metadataDiagnostics
	mappedInputs := map[string]string{}
	claimInput := func(name, use string) bool {
		if name == "" {
			return false
		}
		if previous := mappedInputs[name]; previous != "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page input "+name+" is mapped by both "+previous+" and "+use, table))
			return false
		}
		mappedInputs[name] = use
		return true
	}
	allowedFilters := make([]string, 0)
	for _, filter := range namedChildren(table.Spec, "filter") {
		name := stringValue(filter["name"])
		input := defaultString(strings.TrimSpace(stringValue(filter["input"])), name)
		field := shape.Fields[input]
		if !claimInput(input, "filter "+name) {
			continue
		}
		if field.Name == "" || !field.Optional && !field.HasDefault || !tablePageFilterInputMatchesRow(resources, operation.Module, field.Type, namedChild(record.Spec, "field", name)) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page binding filter "+name+" requires a mapped optional/defaulted scalar or list input with the row field type", table))
			continue
		}
		allowedFilters = append(allowedFilters, name)
	}

	query := firstTablePageChild(table.Spec, "query")
	queryName := func(kind, fallback string) string {
		if query == nil {
			return fallback
		}
		return defaultString(strings.TrimSpace(stringValue(query[kind])), fallback)
	}
	allowedSorts := make([]string, 0)
	sorts := namedChildren(table.Spec, "sort")
	if len(sorts) > 0 {
		sortName, directionName := queryName("sort", "sort"), queryName("direction", "direction")
		sortField, directionField := shape.Fields[sortName], shape.Fields[directionName]
		sortValues, directionValues := enumWireValues(resources, operation.Module, sortField.Type), enumWireValues(resources, operation.Module, directionField.Type)
		sortString := unwrapCRUDListType(typeExpression(sortField.Type)) == "string"
		directionString := unwrapCRUDListType(typeExpression(directionField.Type)) == "string"
		hasDefaultSort := false
		for _, sortSpec := range sorts {
			hasDefaultSort = hasDefaultSort || stringValue(sortSpec["default"]) != ""
		}
		if !claimInput(sortName, "query sort") || !claimInput(directionName, "query direction") || sortField.Name == "" || directionField.Name == "" ||
			(!sortField.Optional && !sortField.HasDefault && !hasDefaultSort) || (!directionField.Optional && !directionField.HasDefault && !hasDefaultSort) ||
			(!sortString && len(sortValues) == 0) || (!directionString && !sameStringSet(directionValues, []string{"asc", "desc"})) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page binding sort mappings require string or enum input fields and a usable direction", table))
		} else {
			for _, sortSpec := range sorts {
				name := stringValue(sortSpec["name"])
				if sortString || containsDataString(sortValues, name) {
					allowedSorts = append(allowedSorts, name)
				}
			}
		}
	}
	searchName := queryName("search", "search")
	if search := shape.Fields[searchName]; search.Name != "" {
		claimInput(searchName, "query search")
		if unwrapCRUDListType(typeExpression(search.Type)) != "string" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page binding search mapping must target a string input field", table))
		}
	} else if query != nil && (strings.TrimSpace(stringValue(query["search"])) != "" || query["search_hidden"] == true) {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page binding search mapping must name an operation input field", table))
	}

	paginated := false
	if pagination := firstTablePageChild(table.Spec, "pagination"); pagination != nil {
		paginated = true
		pageName, pageSizeName, totalName := strings.TrimSpace(stringValue(pagination["page"])), strings.TrimSpace(stringValue(pagination["page_size"])), strings.TrimSpace(stringValue(pagination["total"]))
		pageField, pageSizeField := shape.Fields[pageName], shape.Fields[pageSizeName]
		totalField := namedChild(resultRecord.Spec, "field", totalName)
		if !claimInput(pageName, "pagination page") || !claimInput(pageSizeName, "pagination page_size") ||
			!tablePageIntegerType(pageField.Type) || !tablePageIntegerType(pageSizeField.Type) || totalField == nil || !tablePageIntegerType(totalField["type"]) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2624", "table_page pagination must map distinct integer page/page_size inputs and an integer total result field", table))
		}
	}

	seenPredicates := map[string]bool{}
	for _, predicate := range namedChildren(table.Spec, "predicate") {
		name := stringValue(predicate["name"])
		field := shape.Fields[name]
		if name == "" || seenPredicates[name] || field.Name == "" || !claimInput(name, "predicate") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page predicates require unique operation input field labels", table))
			continue
		}
		seenPredicates[name] = true
		if err := validateFixtureValue(predicate["value"], typeExpression(field.Type), operation.Module, resources); err != nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2625", "table_page predicate "+name+" value does not match its input type: "+err.Error(), table))
		}
	}
	return tablePageSourceContract{record: record, allowedFilters: allowedFilters, allowedSorts: allowedSorts, paginated: paginated}, diagnostics
}

func firstTablePageChild(spec map[string]any, kind string) map[string]any {
	children := orderedChildren(spec, kind)
	if len(children) == 0 {
		return nil
	}
	return children[0]
}

func tablePageIntegerType(value any) bool {
	return oneOfString(unwrapCRUDListType(typeExpression(value)), "int", "int32", "int64", "uint32", "uint64")
}

func tablePageFilterInputMatchesRow(resources map[string]Resource, module string, input any, rowField map[string]any) bool {
	if rowField == nil {
		return false
	}
	inputType := unwrapCRUDListType(typeExpression(input))
	if inner, list := wrappedFixtureType(inputType, "list"); list {
		inputType = inner
	}
	rowType := unwrapCRUDListType(typeExpression(rowField["type"]))
	if inputType == rowType {
		return true
	}
	inputValues := enumWireValues(resources, module, input)
	rowValues := enumWireValues(resources, module, rowField["type"])
	if inputType == "string" && len(rowValues) > 0 || rowType == "string" && len(inputValues) > 0 {
		return true
	}
	return len(inputValues) > 0 && len(rowValues) > 0 && sameStringSet(inputValues, rowValues)
}

func namedChild(spec map[string]any, kind, name string) map[string]any {
	for _, child := range namedChildren(spec, kind) {
		if stringValue(child["name"]) == name {
			return child
		}
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for _, value := range left {
		if !containsDataString(right, value) {
			return false
		}
	}
	return true
}

func validateTablePageRowDialog(resources map[string]Resource, table Resource, rowFields map[string]map[string]any) []Diagnostic {
	children := orderedChildren(table.Spec, "row_detail")
	if len(children) == 0 || children[0]["dialog"] == nil {
		return nil
	}
	dialog := resources[resolveResourceRef(table, refString(children[0]["dialog"]), "form_dialog")]
	if dialog.Kind != "scenery.form-dialog" {
		return []Diagnostic{uiDiagnostic("SCN2622", "table_page row_detail dialog must resolve to a form_dialog", table)}
	}
	binding := resources[resolveResourceRef(dialog, refString(dialog.Spec["source"]), "binding")]
	operation := resources[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
	shape := resolveOperationInputShape(resources, operation)
	if shape.Record == nil {
		return []Diagnostic{uiDiagnostic("SCN2622", "table_page row_detail dialog requires a record input", table)}
	}
	var diagnostics []Diagnostic
	for name, inputField := range shape.Fields {
		rowField := rowFields[name]
		if rowField == nil || unwrapCRUDListType(typeExpression(rowField["type"])) != unwrapCRUDListType(typeExpression(inputField.Type)) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page row_detail dialog input fields must match row fields for seeding", table))
		}
	}
	return diagnostics
}

func validStatusMapReference(resources map[string]Resource, owner Resource, value any) bool {
	statusMap := resources[resolveResourceRef(owner, refString(value), "status_map")]
	return statusMap.Kind == "scenery.status-map"
}

func validateTablePageStats(resources map[string]Resource, table Resource) []Diagnostic {
	children := orderedChildren(table.Spec, "stats")
	if len(children) == 0 {
		return nil
	}
	stats := children[0]
	binding := resources[resolveResourceRef(table, refString(stats["source"]), "binding")]
	if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "http" || stringValue(binding.Spec["delivery"]) != "call" {
		return []Diagnostic{uiDiagnostic("SCN2622", "table_page stats source must resolve to a call-delivery HTTP binding", table)}
	}
	operation := resources[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
	results := namedChildren(operation.Spec, "result")
	if operation.Kind != "scenery.operation" || typeExpression(operation.Spec["input"]) != "std.type.unit" || len(results) != 1 {
		return []Diagnostic{uiDiagnostic("SCN2622", "table_page stats operation requires unit input and exactly one result record", table)}
	}
	record := resources[resolveResourceRef(operation, refString(results[0]["type"]), "record")]
	if record.Kind != "scenery.record" {
		return []Diagnostic{uiDiagnostic("SCN2622", "table_page stats result must be a flat record", table)}
	}
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	seen := map[string]bool{}
	var diagnostics []Diagnostic
	for _, tile := range orderedChildren(stats, "tile") {
		name := stringValue(tile["name"])
		field := fields[name]
		expression := unwrapCRUDListType(typeExpression(field["type"]))
		if name == "" || seen[name] || field == nil || !oneOfString(expression, "decimal", "float32", "float64", "int", "int32", "int64", "string", "uint32", "uint64") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page stats tiles require unique numeric or string result fields", table))
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2622", "table_page stats requires at least one tile", table))
	}
	return diagnostics
}

func validateTablePageComponent(resources map[string]Resource, table Resource, value any) []Diagnostic {
	if value == nil {
		return nil
	}
	component := resources[resolveResourceRef(table, refString(value), "react_component")]
	if component.Kind != "scenery.react-component" {
		return []Diagnostic{uiDiagnostic("SCN2611", "table_page slot component must resolve to a react_component", table)}
	}
	return nil
}

func oneOfString(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}
