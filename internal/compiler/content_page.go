package compiler

import "sort"

const contentPageRendererModule = "scenery.ui.content_page"

func expandContentPageResources(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	occupied := map[string]bool{}
	for _, resource := range resources {
		occupied[resource.Address] = true
	}
	var diagnostics []Diagnostic
	for _, content := range resources {
		if content.Kind != "scenery.content-page" || content.Origin.Kind == "expanded" {
			continue
		}
		source := byAddress[resolveResourceRef(content, refString(content.Spec["source"]), "binding")]
		operationAddress := resolveResourceRef(source, refString(source.Spec["operation"]), "operation")
		load := ""
		for _, binding := range resources {
			if binding.Kind == "scenery.binding" && resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation") == operationAddress && isPageInternalBinding(binding) {
				load = binding.Address
				break
			}
		}
		if load == "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2617", "content_page source operation requires an inherited internal binding", content))
			continue
		}
		lineage := func(output, key string) Origin {
			return Origin{Kind: "expanded", SourceID: content.Origin.SourceID, ModuleChain: append([]string(nil), content.Origin.ModuleChain...), ExpansionLineage: []ExpansionStep{{Generator: content.Address, GeneratorSchemaRevision: "scenery.content-page", Key: key, SourceRange: content.Origin.DeclarationRange, ParentAddress: content.Address, Output: output}}}
		}
		pageAddress := resourceAddress(content.Module, "page", content.Name)
		rendererAddress := resourceAddress(content.Module, "renderer", content.Name+"_web")
		generated := []Resource{
			{Address: pageAddress, Module: content.Module, Name: content.Name, Kind: "scenery.page", Origin: lineage(pageAddress, "page"), Spec: map[string]any{"path": content.Spec["path"], "load": map[string]any{"$ref": load}}},
			{Address: rendererAddress, Module: content.Module, Name: content.Name + "_web", Kind: "scenery.renderer", Origin: lineage(rendererAddress, "renderer"), Spec: map[string]any{"page": map[string]any{"$ref": pageAddress}, "runtime": "web", "module": contentPageRendererModule, "config": cloneMapValue(content.Spec)}},
		}
		collision := false
		for _, resource := range generated {
			if occupied[resource.Address] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2618", Severity: "error", Message: "content_page derived address collides with " + resource.Address, Address: content.Address, Related: []Related{{Address: resource.Address}}})
				collision = true
			}
		}
		if collision {
			continue
		}
		for index := range generated {
			markExpansionFieldProvenance(&generated[index], content)
			occupied[generated[index].Address] = true
			result = append(result, generated[index])
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, diagnostics
}

func validateContentPage(resources map[string]Resource, content Resource) []Diagnostic {
	diagnostics := validateGeneratedPageRoute(resources, content)
	source := resources[resolveResourceRef(content, refString(content.Spec["source"]), "binding")]
	if source.Kind != "scenery.binding" || stringValue(source.Spec["protocol"]) != "http" || stringValue(source.Spec["delivery"]) != "call" {
		return append(diagnostics, uiDiagnostic("SCN2617", "content_page source must resolve to a call-delivery HTTP binding", content))
	}
	operation := resources[resolveResourceRef(source, refString(source.Spec["operation"]), "operation")]
	if operation.Kind != "scenery.operation" || typeExpression(operation.Spec["input"]) != "std.type.unit" || len(namedChildren(operation.Spec, "result")) != 1 {
		return append(diagnostics, uiDiagnostic("SCN2617", "content_page source operation requires unit input and exactly one result", content))
	}
	for _, slot := range []string{"content", "actions"} {
		children := orderedChildren(content.Spec, slot)
		if slot == "content" && len(children) != 1 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2617", "content_page requires a content slot", content))
			continue
		}
		for _, child := range children {
			component := resources[resolveResourceRef(content, refString(child["component"]), "react_component")]
			if component.Kind != "scenery.react-component" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2617", "content_page slots must resolve to react_component resources", content))
			}
		}
	}
	if width, ok := integerValue(content.Spec["max_width"]); content.Spec["max_width"] != nil && (!ok || width < 1) {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2617", "content_page max_width must be a positive integer", content))
	}
	return diagnostics
}

func builtinContentPageRenderer(renderer Resource) bool {
	return renderer.Origin.Kind == "expanded" && stringValue(renderer.Spec["module"]) == contentPageRendererModule
}
