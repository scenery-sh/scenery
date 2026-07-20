package compiler

func validateFormDialog(resources map[string]Resource, dialog Resource) []Diagnostic {
	binding := resources[resolveResourceRef(dialog, refString(dialog.Spec["source"]), "binding")]
	if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "http" || stringValue(binding.Spec["delivery"]) != "call" {
		return []Diagnostic{uiDiagnostic("SCN2621", "form_dialog source must resolve to a call-delivery HTTP mutation binding", dialog)}
	}
	httpSpec, _ := binding.Spec["http"].(map[string]any)
	if method := stringValue(httpSpec["method"]); method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return []Diagnostic{uiDiagnostic("SCN2621", "form_dialog source must use a mutation HTTP method", dialog)}
	}
	operation := resources[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
	shape := resolveOperationInputShape(resources, operation)
	if operation.Kind != "scenery.operation" || shape.Record == nil {
		return []Diagnostic{uiDiagnostic("SCN2621", "form_dialog source operation must have a record input", dialog)}
	}
	seen := map[string]bool{}
	var diagnostics []Diagnostic
	for _, field := range shape.Fields {
		expression := unwrapCRUDListType(typeExpression(field.Type))
		if expression != "string" && len(field.EnumValues) == 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2621", "form_dialog supports string and enum input fields", dialog))
		}
	}
	for _, declared := range orderedChildren(dialog.Spec, "field") {
		name := stringValue(declared["name"])
		field, exists := shape.Fields[name]
		if name == "" || !exists || seen[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2621", "form_dialog fields require unique operation input fields", dialog))
			continue
		}
		seen[name] = true
		control := defaultString(stringValue(declared["control"]), "auto")
		if !oneOfString(control, "auto", "select", "text", "textarea") {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2621", "form_dialog field control is invalid", dialog))
		}
		if control == "select" && len(field.EnumValues) == 0 && declared["status_map"] == nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2621", "form_dialog select control requires enum values or a status_map", dialog))
		}
		if declared["status_map"] != nil {
			statusMap := resources[resolveResourceRef(dialog, refString(declared["status_map"]), "status_map")]
			expression := unwrapCRUDListType(typeExpression(field.Type))
			if statusMap.Kind != "scenery.status-map" || expression != "string" && len(field.EnumValues) == 0 {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2621", "form_dialog field status_map requires a string or enum field", dialog))
			}
		}
	}
	return diagnostics
}
