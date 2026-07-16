package compiler

import "sort"

const splitPageRendererModule = "scenery.ui.split_page"

func expandSplitPageResources(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	occupied := map[string]bool{}
	for _, resource := range resources {
		occupied[resource.Address] = true
	}
	var diagnostics []Diagnostic
	for _, split := range resources {
		if split.Kind != "scenery.split-page" || split.Origin.Kind == "expanded" {
			continue
		}
		source := byAddress[resolveResourceRef(split, refString(split.Spec["source"]), "binding")]
		operationAddress := resolveResourceRef(source, refString(source.Spec["operation"]), "operation")
		load := ""
		for _, binding := range resources {
			if binding.Kind == "scenery.binding" && resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation") == operationAddress && isPageInternalBinding(binding) {
				load = binding.Address
				break
			}
		}
		if load == "" {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2615", "split_page source operation requires an inherited internal binding", split))
			continue
		}
		lineage := func(output, key string) Origin {
			return Origin{Kind: "expanded", SourceID: split.Origin.SourceID, ModuleChain: append([]string(nil), split.Origin.ModuleChain...), ExpansionLineage: []ExpansionStep{{Generator: split.Address, GeneratorSchemaRevision: "scenery.split-page", Key: key, SourceRange: split.Origin.DeclarationRange, ParentAddress: split.Address, Output: output}}}
		}
		pageAddress := resourceAddress(split.Module, "page", split.Name)
		rendererAddress := resourceAddress(split.Module, "renderer", split.Name+"_web")
		generated := []Resource{
			{Address: pageAddress, Module: split.Module, Name: split.Name, Kind: "scenery.page", Origin: lineage(pageAddress, "page"), Spec: map[string]any{"path": split.Spec["path"], "load": map[string]any{"$ref": load}}},
			{Address: rendererAddress, Module: split.Module, Name: split.Name + "_web", Kind: "scenery.renderer", Origin: lineage(rendererAddress, "renderer"), Spec: map[string]any{"page": map[string]any{"$ref": pageAddress}, "runtime": "web", "module": splitPageRendererModule, "config": cloneMapValue(split.Spec)}},
		}
		collision := false
		for _, resource := range generated {
			if occupied[resource.Address] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2616", Severity: "error", Message: "split_page derived address collides with " + resource.Address, Address: split.Address, Related: []Related{{Address: resource.Address}}})
				collision = true
			}
		}
		if collision {
			continue
		}
		for index := range generated {
			markExpansionFieldProvenance(&generated[index], split)
			occupied[generated[index].Address] = true
			result = append(result, generated[index])
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, diagnostics
}

func validateSplitPage(resources map[string]Resource, split Resource) []Diagnostic {
	source := resources[resolveResourceRef(split, refString(split.Spec["source"]), "binding")]
	if source.Kind != "scenery.binding" || stringValue(source.Spec["protocol"]) != "http" || stringValue(source.Spec["delivery"]) != "call" {
		return []Diagnostic{uiDiagnostic("SCN2615", "split_page source must resolve to a call-delivery HTTP binding", split)}
	}
	operation := resources[resolveResourceRef(source, refString(source.Spec["operation"]), "operation")]
	if operation.Kind != "scenery.operation" || typeExpression(operation.Spec["input"]) != "std.type.unit" || len(namedChildren(operation.Spec, "result")) != 1 {
		return []Diagnostic{uiDiagnostic("SCN2615", "split_page source operation requires unit input and exactly one result", split)}
	}
	var diagnostics []Diagnostic
	for _, slot := range []string{"pane", "detail", "pane_actions", "detail_header"} {
		children := orderedChildren(split.Spec, slot)
		if (slot == "pane" || slot == "detail") && len(children) != 1 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2615", "split_page requires pane and detail slots", split))
			continue
		}
		for _, child := range children {
			component := resources[resolveResourceRef(split, refString(child["component"]), "react_component")]
			if component.Kind != "scenery.react-component" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2615", "split_page slots must resolve to react_component resources", split))
			}
		}
	}
	return diagnostics
}

func builtinSplitPageRenderer(renderer Resource) bool {
	return renderer.Origin.Kind == "expanded" && stringValue(renderer.Spec["module"]) == splitPageRendererModule
}
