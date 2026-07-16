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
		crud := byAddress[resolveResourceRef(table, refString(table.Spec["source"]), "crud")]
		if crud.Kind != "scenery.crud" || crud.Spec["list"] == nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page source must resolve to a CRUD list contract", table))
			continue
		}
		load := resourceAddress(crud.Module, "binding", crud.Name+"_list_internal")
		if byAddress[load].Kind != "scenery.binding" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page source has no generated list binding", table))
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
	crud := resources[resolveResourceRef(table, refString(table.Spec["source"]), "crud")]
	if crud.Kind != "scenery.crud" {
		return []Diagnostic{uiDiagnostic("SCN2608", "table_page source must resolve to a CRUD resource", table)}
	}
	list, ok := crud.Spec["list"].(map[string]any)
	if !ok || crud.Spec["http"] == nil {
		return []Diagnostic{uiDiagnostic("SCN2608", "table_page source requires CRUD list and HTTP projections", table)}
	}
	entity := resources[resolveResourceRef(crud, refString(crud.Spec["entity"]), "entity")]
	record := resources[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	allowedFilters, allowedSorts := stringValues(list["filters"]), stringValues(list["sorts"])
	var diagnostics []Diagnostic
	seenColumns := map[string]bool{}
	for _, column := range namedChildren(table.Spec, "column") {
		name, appearance := stringValue(column["name"]), defaultString(stringValue(column["appearance"]), "auto")
		if fields[name] == nil || seenColumns[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2609", "table_page columns require unique entity fields", table))
		}
		seenColumns[name] = true
		if !oneOfString(appearance, "auto", "text", "number", "datetime", "badge") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2609", "table_page column appearance is invalid", table))
		}
		diagnostics = append(diagnostics, validateTablePageComponent(resources, table, column["component"])...)
	}
	if len(seenColumns) == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2608", "table_page requires at least one column", table))
	}
	seenFilters := map[string]bool{}
	for _, filter := range namedChildren(table.Spec, "filter") {
		name := stringValue(filter["name"])
		if fields[name] == nil || seenFilters[name] || !containsDataString(allowedFilters, name) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2610", "table_page filters require unique CRUD-allowlisted entity fields", table))
		}
		seenFilters[name] = true
		diagnostics = append(diagnostics, validateTablePageComponent(resources, table, filter["component"])...)
	}
	seenSorts, defaults := map[string]bool{}, 0
	for _, sortSpec := range namedChildren(table.Spec, "sort") {
		name := stringValue(sortSpec["name"])
		if fields[name] == nil || seenSorts[name] || !containsDataString(allowedSorts, name) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2610", "table_page sorts require unique CRUD-allowlisted entity fields", table))
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
	for _, slot := range []string{"toolbar", "empty"} {
		for _, value := range namedChildren(table.Spec, slot) {
			diagnostics = append(diagnostics, validateTablePageComponent(resources, table, value["component"])...)
		}
	}
	pageSize, validPageSize := integerValue(table.Spec["page_size"])
	maxPageSize, _ := integerValue(list["max_page_size"])
	if !validPageSize || pageSize < 1 || maxPageSize < pageSize {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2613", fmt.Sprintf("table_page page_size must be between 1 and %d", maxPageSize), table))
	}
	for _, match := range tablePageRouteParameter.FindAllStringSubmatch(stringValue(table.Spec["row_link"]), -1) {
		if fields[match[1]] == nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2612", "table_page row_link parameter "+match[1]+" is not an entity field", table))
		}
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
