package compiler

import "sort"

const workspacePageRendererModule = "scenery.ui.workspace_page"

func expandWorkspacePageResources(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	occupied := map[string]bool{}
	for _, resource := range resources {
		occupied[resource.Address] = true
	}
	var diagnostics []Diagnostic
	for _, workspace := range resources {
		if workspace.Kind != "scenery.workspace-page" || workspace.Origin.Kind == "expanded" {
			continue
		}
		lineage := func(output, key string) Origin {
			return Origin{Kind: "expanded", SourceID: workspace.Origin.SourceID, ModuleChain: append([]string(nil), workspace.Origin.ModuleChain...), ExpansionLineage: []ExpansionStep{{Generator: workspace.Address, GeneratorSchemaRevision: "scenery.workspace-page", Key: key, SourceRange: workspace.Origin.DeclarationRange, ParentAddress: workspace.Address, Output: output}}}
		}
		pageAddress := resourceAddress(workspace.Module, "page", workspace.Name)
		rendererAddress := resourceAddress(workspace.Module, "renderer", workspace.Name+"_web")
		generated := []Resource{
			{Address: pageAddress, Module: workspace.Module, Name: workspace.Name, Kind: "scenery.page", Origin: lineage(pageAddress, "page"), Spec: map[string]any{"path": workspace.Spec["path"]}},
			{Address: rendererAddress, Module: workspace.Module, Name: workspace.Name + "_web", Kind: "scenery.renderer", Origin: lineage(rendererAddress, "renderer"), Spec: map[string]any{"page": map[string]any{"$ref": pageAddress}, "runtime": "web", "module": workspacePageRendererModule, "config": cloneMapValue(workspace.Spec)}},
		}
		collision := false
		for _, resource := range generated {
			if occupied[resource.Address] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2628", Severity: "error", Message: "workspace_page derived address collides with " + resource.Address, Address: workspace.Address, Related: []Related{{Address: resource.Address}}})
				collision = true
			}
		}
		if collision {
			continue
		}
		for index := range generated {
			markExpansionFieldProvenance(&generated[index], workspace)
			occupied[generated[index].Address] = true
			result = append(result, generated[index])
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, diagnostics
}

func validateWorkspacePage(resources map[string]Resource, workspace Resource) []Diagnostic {
	diagnostics := validateGeneratedPageRoute(resources, workspace)
	presentation := defaultString(stringValue(workspace.Spec["presentation"]), "tabs")
	if !oneOfString(presentation, "tabs", "sidebar") {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page presentation must be tabs or sidebar", workspace))
	}
	tabs := orderedChildren(workspace.Spec, "tab")
	if len(tabs) == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page requires at least one tab", workspace))
		return diagnostics
	}
	seenNames := map[string]bool{}
	seenPages := map[string]bool{}
	pageTabs := 0
	for _, tab := range tabs {
		name := stringValue(tab["name"])
		if name == "" || seenNames[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page tabs require unique names", workspace))
		}
		seenNames[name] = true
		pageRef := refString(tab["page"])
		destination := stringValue(tab["destination"])
		if pageRef != "" && destination != "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page tabs cannot declare both page and destination", workspace))
			continue
		}
		if stringValue(tab["available"]) != "" && stringValue(tab["unavailable_reason"]) == "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page tabs with availability require unavailable_reason", workspace))
		}
		if pageRef == "" {
			if presentation != "sidebar" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page navigation-only tabs require sidebar presentation", workspace))
			}
			if destination == "" && (stringValue(tab["available"]) == "" || stringValue(tab["unavailable_reason"]) == "") {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page navigation-only tabs without a destination require availability and unavailable_reason", workspace))
			}
			continue
		}
		pageTabs++
		pageAddress := resolveResourceRef(workspace, pageRef, "table_page")
		page := resources[pageAddress]
		if seenPages[pageAddress] || !oneOfString(page.Kind, "scenery.table-page", "scenery.content-page") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page tabs require distinct table_page or content_page references", workspace))
		}
		if page.Module != workspace.Module {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page tabs must reference pages in the same module", workspace))
		}
		if stringValue(page.Spec["nav_group"]) != "" || page.Spec["nav_order"] != nil || stringValue(page.Spec["nav_label"]) != "" || stringValue(page.Spec["nav_icon"]) != "" || len(stringValues(page.Spec["nav_active_paths"])) > 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page embedded pages must not declare standalone navigation metadata", workspace))
		}
		for _, candidate := range resources {
			if candidate.Kind != "scenery.workspace-page" || candidate.Address == workspace.Address {
				continue
			}
			for _, candidateTab := range orderedChildren(candidate.Spec, "tab") {
				candidatePageRef := refString(candidateTab["page"])
				if candidatePageRef == "" {
					continue
				}
				candidatePage := resolveResourceRef(candidate, candidatePageRef, "table_page")
				if candidatePage == pageAddress {
					diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page embedded pages may belong to only one workspace", workspace))
				}
			}
		}
		seenPages[pageAddress] = true
	}
	if pageTabs == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page requires at least one page-backed tab", workspace))
	}
	diagnostics = append(diagnostics, validateWorkspacePageStats(resources, workspace, tabs)...)
	for _, slot := range orderedChildren(workspace.Spec, "actions") {
		component := resources[resolveResourceRef(workspace, refString(slot["component"]), "react_component")]
		if component.Kind != "scenery.react-component" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2626", "workspace_page actions must resolve to a react_component", workspace))
		}
	}
	return diagnostics
}

func validateWorkspacePageStats(resources map[string]Resource, workspace Resource, tabs []map[string]any) []Diagnostic {
	statsChildren := orderedChildren(workspace.Spec, "stats")
	needsStats := false
	for _, tab := range tabs {
		needsStats = needsStats || stringValue(tab["count"]) != "" || stringValue(tab["available"]) != ""
	}
	if len(statsChildren) == 0 {
		if needsStats {
			return []Diagnostic{uiDiagnostic("SCN2627", "workspace_page count and availability fields require stats", workspace)}
		}
		return nil
	}
	stats := statsChildren[0]
	binding := resources[resolveResourceRef(workspace, refString(stats["source"]), "binding")]
	if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "http" || stringValue(binding.Spec["delivery"]) != "call" {
		return []Diagnostic{uiDiagnostic("SCN2627", "workspace_page stats source must resolve to a call-delivery HTTP binding", workspace)}
	}
	operation := resources[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
	results := namedChildren(operation.Spec, "result")
	if operation.Kind != "scenery.operation" || typeExpression(operation.Spec["input"]) != "std.type.unit" || len(results) != 1 {
		return []Diagnostic{uiDiagnostic("SCN2627", "workspace_page stats operation requires unit input and exactly one result record", workspace)}
	}
	record := resources[resolveResourceRef(operation, refString(results[0]["type"]), "record")]
	if record.Kind != "scenery.record" {
		return []Diagnostic{uiDiagnostic("SCN2627", "workspace_page stats result must be a flat record", workspace)}
	}
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	var diagnostics []Diagnostic
	for _, tab := range tabs {
		if name := stringValue(tab["count"]); name != "" {
			field := fields[name]
			if field == nil || !oneOfString(unwrapCRUDListType(typeExpression(field["type"])), "int", "int32", "int64", "uint32", "uint64") {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2627", "workspace_page tab count must name an integer stats field", workspace))
			}
		}
		if name := stringValue(tab["available"]); name != "" {
			field := fields[name]
			if field == nil || unwrapCRUDListType(typeExpression(field["type"])) != "bool" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2627", "workspace_page tab available must name a boolean stats field", workspace))
			}
		}
	}
	for _, tile := range orderedChildren(stats, "tile") {
		field := fields[stringValue(tile["name"])]
		expression := unwrapCRUDListType(typeExpression(field["type"]))
		if field == nil || !oneOfString(expression, "decimal", "float32", "float64", "int", "int32", "int64", "string", "uint32", "uint64") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2627", "workspace_page stats tiles require numeric or string result fields", workspace))
		}
		appearance, subAppearance := defaultString(stringValue(tile["appearance"]), "plain"), defaultString(stringValue(tile["sub_appearance"]), "plain")
		if !oneOfString(appearance, "plain", "money", "count", "percent") || !oneOfString(subAppearance, "plain", "money", "count", "percent") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2634", "workspace_page stats tile appearance must be plain, money, count, or percent", workspace))
		}
		if expression == "string" && appearance != "plain" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2634", "workspace_page stats tile appearance "+appearance+" requires a numeric result field", workspace))
		}
		if sub := stringValue(tile["sub"]); sub != "" {
			subField := fields[sub]
			subExpression := unwrapCRUDListType(typeExpression(subField["type"]))
			if subField == nil || !oneOfString(subExpression, "decimal", "float32", "float64", "int", "int32", "int64", "string", "uint32", "uint64") {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2634", "workspace_page stats tile sub must name a numeric or string result field", workspace))
			}
			if subExpression == "string" && subAppearance != "plain" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2634", "workspace_page stats tile sub_appearance "+subAppearance+" requires a numeric result field", workspace))
			}
		}
	}
	return diagnostics
}

func builtinWorkspacePageRenderer(renderer Resource) bool {
	return renderer.Origin.Kind == "expanded" && stringValue(renderer.Spec["module"]) == workspacePageRendererModule
}
