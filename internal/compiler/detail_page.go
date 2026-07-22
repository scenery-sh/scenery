package compiler

import (
	"sort"
	"strings"
)

const detailPageRendererModule = "scenery.ui.detail_page"

type detailPageContract struct {
	operation Resource
	record    Resource
	params    []map[string]any
}

func expandDetailPageResources(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	occupied := map[string]bool{}
	for _, resource := range resources {
		occupied[resource.Address] = true
	}
	var diagnostics []Diagnostic
	for _, detail := range resources {
		if detail.Kind != "scenery.detail-page" || detail.Origin.Kind == "expanded" {
			continue
		}
		source := byAddress[resolveResourceRef(detail, refString(detail.Spec["source"]), "binding")]
		operationAddress := resolveResourceRef(source, refString(source.Spec["operation"]), "operation")
		load := ""
		for _, binding := range resources {
			if binding.Kind == "scenery.binding" && resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation") == operationAddress && isPageInternalBinding(binding) {
				load = binding.Address
				break
			}
		}
		if load == "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page source operation requires an inherited internal binding", detail))
			continue
		}
		lineage := func(output, key string) Origin {
			return Origin{Kind: "expanded", SourceID: detail.Origin.SourceID, ModuleChain: append([]string(nil), detail.Origin.ModuleChain...), ExpansionLineage: []ExpansionStep{{Generator: detail.Address, GeneratorSchemaRevision: "scenery.detail-page", Key: key, SourceRange: detail.Origin.DeclarationRange, ParentAddress: detail.Address, Output: output}}}
		}
		pageAddress := resourceAddress(detail.Module, "page", detail.Name)
		rendererAddress := resourceAddress(detail.Module, "renderer", detail.Name+"_web")
		pageSpec := map[string]any{
			"path":  detail.Spec["path"],
			"load":  map[string]any{"$ref": load},
			"param": detailPageNormalizedParams(detail),
		}
		generated := []Resource{
			{Address: pageAddress, Module: detail.Module, Name: detail.Name, Kind: "scenery.page", Origin: lineage(pageAddress, "page"), Spec: pageSpec},
			{Address: rendererAddress, Module: detail.Module, Name: detail.Name + "_web", Kind: "scenery.renderer", Origin: lineage(rendererAddress, "renderer"), Spec: map[string]any{"page": map[string]any{"$ref": pageAddress}, "runtime": "web", "module": detailPageRendererModule, "config": cloneMapValue(detail.Spec)}},
		}
		collision := false
		for _, resource := range generated {
			if occupied[resource.Address] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2633", Severity: "error", Message: "detail_page derived address collides with " + resource.Address, Address: detail.Address, Related: []Related{{Address: resource.Address}}})
				collision = true
			}
		}
		if collision {
			continue
		}
		for index := range generated {
			markExpansionFieldProvenance(&generated[index], detail)
			occupied[generated[index].Address] = true
			result = append(result, generated[index])
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, diagnostics
}

func validateDetailPage(resources map[string]Resource, detail Resource) []Diagnostic {
	diagnostics := validateGeneratedPageRoute(resources, detail)
	contract, contractDiagnostics := resolveDetailPageContract(resources, detail)
	diagnostics = append(diagnostics, contractDiagnostics...)
	if contract.record.Kind != "scenery.record" {
		return diagnostics
	}
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(contract.record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	diagnostics = append(diagnostics, validateDetailPageSections(resources, detail, contract.record, fields)...)
	diagnostics = append(diagnostics, validateDetailPageActions(resources, detail, contract.record, fields)...)
	diagnostics = append(diagnostics, validateDetailPageTables(resources, detail, contract)...)
	return diagnostics
}

func resolveDetailPageContract(resources map[string]Resource, detail Resource) (detailPageContract, []Diagnostic) {
	var diagnostics []Diagnostic
	presentation := defaultString(strings.TrimSpace(stringValue(detail.Spec["presentation"])), "page")
	if !oneOfString(presentation, "page", "dialog", "both") {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page presentation must be page, dialog, or both", detail))
	}
	path := stringValue(detail.Spec["path"])
	if !validHTTPPath(path) {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page path must be a valid absolute route", detail))
	}
	source := resources[resolveResourceRef(detail, refString(detail.Spec["source"]), "binding")]
	if source.Kind != "scenery.binding" || stringValue(source.Spec["protocol"]) != "http" || stringValue(source.Spec["delivery"]) != "call" {
		return detailPageContract{}, append(diagnostics, uiDiagnostic("SCN2629", "detail_page source must resolve to a call-delivery HTTP binding", detail))
	}
	operation := resources[resolveResourceRef(source, refString(source.Spec["operation"]), "operation")]
	shape := resolveOperationInputShape(resources, operation)
	results := namedChildren(operation.Spec, "result")
	if operation.Kind != "scenery.operation" || shape.Record == nil || len(results) != 1 {
		return detailPageContract{operation: operation}, append(diagnostics, uiDiagnostic("SCN2629", "detail_page source operation requires record input and exactly one result record", detail))
	}
	record := resources[resolveResourceRef(operation, refString(results[0]["type"]), "record")]
	if record.Kind != "scenery.record" {
		return detailPageContract{operation: operation}, append(diagnostics, uiDiagnostic("SCN2629", "detail_page source result must directly reference a record", detail))
	}

	routeParams := detailPagePathParams(path)
	if len(routeParams) == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page path requires at least one parameter", detail))
	}
	declared := map[string]string{}
	for _, param := range namedChildren(detail.Spec, "param") {
		name, input := stringValue(param["name"]), strings.TrimSpace(stringValue(param["input"]))
		if name == "" || input == "" || declared[name] != "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page param overrides require unique route parameter names and operation inputs", detail))
			continue
		}
		declared[name] = input
	}
	seenRoute, claimedInputs := map[string]bool{}, map[string]bool{}
	params := make([]map[string]any, 0, len(routeParams))
	for _, name := range routeParams {
		if seenRoute[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page path parameters must be unique", detail))
			continue
		}
		seenRoute[name] = true
		input := defaultString(declared[name], name)
		field := shape.Fields[input]
		if field.Name == "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page path parameter "+name+" does not resolve to an operation input", detail))
		} else if claimedInputs[input] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page path parameters must not claim the same operation input", detail))
		} else if !httpPathScalarType(field.Type, resources, operation.Module) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page path parameter "+name+" requires a scalar operation input with a supported wire codec", detail))
		}
		claimedInputs[input] = true
		params = append(params, map[string]any{"name": name, "input": input})
	}
	for name := range declared {
		if !seenRoute[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2629", "detail_page param override "+name+" is not present in the path", detail))
		}
	}
	return detailPageContract{operation: operation, record: record, params: params}, diagnostics
}

func validateDetailPageSections(resources map[string]Resource, detail, record Resource, fields map[string]map[string]any) []Diagnostic {
	sections := orderedChildren(detail.Spec, "section")
	if len(sections) == 0 {
		return []Diagnostic{uiDiagnostic("SCN2630", "detail_page requires at least one section", detail)}
	}
	seenSections, seenFields := map[string]bool{}, map[string]bool{}
	var diagnostics []Diagnostic
	for _, section := range sections {
		name := stringValue(section["name"])
		declaredFields := orderedChildren(section, "field")
		if name == "" || seenSections[name] || strings.TrimSpace(stringValue(section["label"])) == "" || len(declaredFields) == 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2630", "detail_page sections require unique names, labels, and at least one field", detail))
		}
		seenSections[name] = true
		for _, declared := range declaredFields {
			fieldName := stringValue(declared["name"])
			field := fields[fieldName]
			appearance := defaultString(stringValue(declared["appearance"]), "auto")
			if fieldName == "" || field == nil || seenFields[fieldName] {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2630", "detail_page section fields require unique top-level result fields", detail))
			}
			seenFields[fieldName] = true
			if !oneOfString(appearance, "auto", "text", "number", "datetime", "badge") {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2630", "detail_page field appearance is invalid", detail))
			}
			if value, exists := declared["hide_empty"]; exists {
				if _, ok := value.(bool); !ok {
					diagnostics = append(diagnostics, uiDiagnostic("SCN2630", "detail_page field hide_empty must be a boolean", detail))
				}
			}
			if declared["status_map"] != nil {
				expression := unwrapCRUDListType(typeExpression(field["type"]))
				if appearance != "badge" || expression != "string" && resources[namedFixtureTypeAddress(expression, record.Module)].Kind != "scenery.enum" || !validStatusMapReference(resources, detail, declared["status_map"]) {
					diagnostics = append(diagnostics, uiDiagnostic("SCN2630", "detail_page status_map requires a badge string or enum field", detail))
				}
			}
		}
	}
	return diagnostics
}

func validateDetailPageActions(resources map[string]Resource, detail, record Resource, fields map[string]map[string]any) []Diagnostic {
	var diagnostics []Diagnostic
	seen := map[string]bool{}
	for _, action := range orderedChildren(detail.Spec, "action") {
		name := stringValue(action["name"])
		dialog := resources[resolveResourceRef(detail, refString(action["dialog"]), "form_dialog")]
		if name == "" || seen[name] || strings.TrimSpace(stringValue(action["label"])) == "" || dialog.Kind != "scenery.form-dialog" || dialog.Module != detail.Module {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2631", "detail_page actions require unique names and same-module form_dialog resources", detail))
			continue
		}
		seen[name] = true
		binding := resources[resolveResourceRef(dialog, refString(dialog.Spec["source"]), "binding")]
		operation := resources[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
		shape := resolveOperationInputShape(resources, operation)
		if shape.Record == nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2631", "detail_page action dialog requires a record input", detail))
			continue
		}
		for inputName, input := range shape.Fields {
			field := fields[inputName]
			if field == nil || !detailPageTypesCompatible(resources, record.Module, field["type"], operation.Module, input.Type) {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2631", "detail_page action dialog input fields must match result fields for seeding", detail))
			}
		}
	}
	for _, slot := range orderedChildren(detail.Spec, "actions") {
		component := resources[resolveResourceRef(detail, refString(slot["component"]), "react_component")]
		if component.Kind != "scenery.react-component" || component.Module != detail.Module {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2631", "detail_page actions slot must resolve to a same-module react_component", detail))
		}
	}
	return diagnostics
}

func validateDetailPageTables(resources map[string]Resource, detail Resource, contract detailPageContract) []Diagnostic {
	paramInputs := map[string]operationInputField{}
	shape := resolveOperationInputShape(resources, contract.operation)
	for _, param := range contract.params {
		paramInputs[stringValue(param["name"])] = shape.Fields[stringValue(param["input"])]
	}
	seen := map[string]bool{}
	var diagnostics []Diagnostic
	for _, related := range orderedChildren(detail.Spec, "table") {
		name, paramName, inputName := stringValue(related["name"]), stringValue(related["param"]), stringValue(related["input"])
		page := resources[resolveResourceRef(detail, refString(related["page"]), "table_page")]
		if name == "" || seen[name] || strings.TrimSpace(stringValue(related["label"])) == "" || page.Kind != "scenery.table-page" || page.Module != detail.Module {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2632", "detail_page related tables require unique names and same-module table_page resources", detail))
			continue
		}
		seen[name] = true
		source := resources[resolveResourceRef(page, refString(page.Spec["source"]), "binding")]
		if source.Kind != "scenery.binding" || stringValue(source.Spec["protocol"]) != "http" || stringValue(source.Spec["delivery"]) != "call" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2632", "detail_page related tables must reference binding-backed table pages", detail))
			continue
		}
		operation := resources[resolveResourceRef(source, refString(source.Spec["operation"]), "operation")]
		relatedShape := resolveOperationInputShape(resources, operation)
		paramField, paramExists := paramInputs[paramName]
		inputField := relatedShape.Fields[inputName]
		if !paramExists || inputField.Name == "" || !detailPageTypesCompatible(resources, contract.operation.Module, paramField.Type, operation.Module, inputField.Type) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2632", "detail_page related table mapping requires a path parameter and type-compatible operation input", detail))
		}
		if detailPageTableClaimedInputs(resources, page)[inputName] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2632", "detail_page related table input is already claimed by the table page", detail))
		}
	}
	return diagnostics
}

func detailPageTableClaimedInputs(resources map[string]Resource, page Resource) map[string]bool {
	claimed := map[string]bool{}
	for _, filter := range namedChildren(page.Spec, "filter") {
		claimed[defaultString(strings.TrimSpace(stringValue(filter["input"])), stringValue(filter["name"]))] = true
	}
	query := firstTablePageChild(page.Spec, "query")
	queryName := func(kind, fallback string) string {
		if query == nil {
			return fallback
		}
		return defaultString(strings.TrimSpace(stringValue(query[kind])), fallback)
	}
	if len(namedChildren(page.Spec, "sort")) > 0 {
		claimed[queryName("sort", "sort")] = true
		claimed[queryName("direction", "direction")] = true
	}
	source := resources[resolveResourceRef(page, refString(page.Spec["source"]), "binding")]
	operation := resources[resolveResourceRef(source, refString(source.Spec["operation"]), "operation")]
	if resolveOperationInputShape(resources, operation).Fields[queryName("search", "search")].Name != "" {
		claimed[queryName("search", "search")] = true
	}
	if pagination := firstTablePageChild(page.Spec, "pagination"); pagination != nil {
		claimed[stringValue(pagination["page"])] = true
		claimed[stringValue(pagination["page_size"])] = true
	}
	for _, predicate := range namedChildren(page.Spec, "predicate") {
		claimed[stringValue(predicate["name"])] = true
	}
	return claimed
}

func detailPageTypesCompatible(resources map[string]Resource, leftModule string, left any, rightModule string, right any) bool {
	leftType, rightType := unwrapCRUDListType(typeExpression(left)), unwrapCRUDListType(typeExpression(right))
	if leftType == rightType {
		return true
	}
	leftValues := enumWireValues(resources, leftModule, left)
	rightValues := enumWireValues(resources, rightModule, right)
	return leftType == "string" && len(rightValues) > 0 || rightType == "string" && len(leftValues) > 0 || len(leftValues) > 0 && len(rightValues) > 0 && sameStringSet(leftValues, rightValues)
}

func detailPagePathParams(path string) []string {
	matches := httpPathParameterPattern.FindAllStringSubmatch(path, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			result = append(result, match[1])
		}
	}
	return result
}

func detailPageNormalizedParams(detail Resource) []any {
	overrides := map[string]string{}
	for _, param := range namedChildren(detail.Spec, "param") {
		overrides[stringValue(param["name"])] = stringValue(param["input"])
	}
	result := make([]any, 0)
	seen := map[string]bool{}
	for _, name := range detailPagePathParams(stringValue(detail.Spec["path"])) {
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, map[string]any{"name": name, "input": defaultString(overrides[name], name)})
	}
	return result
}

func builtinDetailPageRenderer(renderer Resource) bool {
	return renderer.Origin.Kind == "expanded" && stringValue(renderer.Spec["module"]) == detailPageRendererModule
}
