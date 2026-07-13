package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func applyPatches(resources []Resource) ([]Resource, []Diagnostic) {
	result := make([]Resource, len(resources))
	copy(result, resources)
	indexes := map[string]int{}
	for index, resource := range result {
		indexes[resource.Address] = index
	}
	var patches []Resource
	for _, resource := range result {
		if resource.Kind == "scenery.patch" {
			patches = append(patches, resource)
		}
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].Address < patches[j].Address })
	var diagnostics []Diagnostic
	writers := map[string]string{}
	for _, patch := range patches {
		targetAddress, patchable, targetDiagnostic := resolvePatchTarget(patch, result, indexes)
		if targetDiagnostic != nil {
			diagnostics = append(diagnostics, *targetDiagnostic)
			continue
		}
		index, ok := indexes[targetAddress]
		if !ok {
			diagnostics = append(diagnostics, patchDiagnostic("SCN2901", "patch target not found", patch))
			continue
		}
		target := result[index]
		schema, _ := patch.Spec["schema"].(string)
		targetSchema, _ := CoreSchema(target.Kind)
		targetSchemaRevision, _ := targetSchema["schema_revision"].(string)
		if schema == "" || targetSchemaRevision == "" || schema != targetSchemaRevision {
			diagnostics = append(diagnostics, patchDiagnostic("SCN2902", "patch schema must match target schema_revision "+targetSchemaRevision, patch))
			continue
		}
		expect, _ := patch.Spec["expect"].(map[string]any)
		set, _ := patch.Spec["set"].(map[string]any)
		if expect == nil || set == nil || expect["path"] == nil || set["path"] == nil {
			diagnostics = append(diagnostics, patchDiagnostic("SCN2902", "patch requires expect and set preconditions", patch))
			continue
		}
		expectPath, _ := expect["path"].(string)
		setPath, _ := set["path"].(string)
		if expectPath == "" || setPath == "" || expectPath != setPath || !patchable[setPath] {
			diagnostics = append(diagnostics, patchDiagnostic("SCN2906", "patch target is private or path is not explicitly patchable: "+setPath, patch))
			continue
		}
		writerKey := targetAddress + "\x00" + setPath
		if previous := writers[writerKey]; previous != "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2907", Severity: "error", Message: fmt.Sprintf("patch %s: path already patched by %s", patch.Name, previous), Address: patch.Address, Related: []Related{{Address: previous}}})
			continue
		}
		current, found := resourcePointerValue(target, expectPath)
		if !found || !semanticEqual(current, expect["value"]) {
			diagnostics = append(diagnostics, patchDiagnostic("SCN2903", "patch precondition failed at "+expectPath, patch))
			continue
		}
		copyBytes, _ := json.Marshal(target.Spec)
		var copied map[string]any
		_ = json.Unmarshal(copyBytes, &copied)
		target.Spec = copied
		if !setResourcePointer(&target, setPath, set["value"]) {
			diagnostics = append(diagnostics, patchDiagnostic("SCN2904", "patch path is not writable: "+setPath, patch))
			continue
		}
		target.Origin.Patches = canonicalStrings(append(target.Origin.Patches, patch.Address))
		if target.Origin.FieldProvenance == nil {
			target.Origin.FieldProvenance = map[string]FieldProvenance{}
		}
		declaredAt := patch.Origin.DeclarationRange
		if field, ok := patch.Origin.FieldProvenance["/spec/set/value"]; ok && field.DeclaredAt != nil {
			declaredAt = field.DeclaredAt
		}
		target.Origin.FieldProvenance[setPath] = FieldProvenance{
			Kind: "patch", DeclaredAt: declaredAt, ProvidedBy: patch.Address,
			SourceAddress: target.Address, Transformations: []string{"exact_patch"},
		}
		result[index] = target
		writers[writerKey] = patch.Address
	}
	return result, diagnostics
}

func resolvePatchTarget(patch Resource, resources []Resource, indexes map[string]int) (string, map[string]bool, *Diagnostic) {
	reference := refString(patch.Spec["target"])
	parts := strings.Split(reference, ".")
	if len(parts) == 3 && parts[0] == "module" {
		moduleAddress := resourceAddress("app", "module", parts[1])
		moduleIndex, ok := indexes[moduleAddress]
		if !ok {
			diagnostic := patchDiagnostic("SCN2901", "patch module target not found", patch)
			return "", nil, &diagnostic
		}
		module := resources[moduleIndex]
		metadata, _ := module.Spec["export_metadata"].(map[string]any)
		export, _ := metadata[parts[2]].(map[string]any)
		valueReference := refString(export["value"])
		if valueReference == "" {
			diagnostic := patchDiagnostic("SCN2906", "patch target is not an exported resource", patch)
			return "", nil, &diagnostic
		}
		targetAddress := valueReference
		if !strings.Contains(valueReference, "/") {
			valueParts := strings.Split(valueReference, ".")
			if len(valueParts) != 2 {
				diagnostic := patchDiagnostic("SCN2906", "patch export does not resolve to one resource", patch)
				return "", nil, &diagnostic
			}
			targetAddress = resourceAddress(module.Name, valueParts[0], valueParts[1])
		}
		return targetAddress, stringListSet(export["patchable"]), nil
	}
	index, ok := indexes[reference]
	if !ok {
		diagnostic := patchDiagnostic("SCN2901", "patch target not found", patch)
		return "", nil, &diagnostic
	}
	target := resources[index]
	if target.Module != patch.Module {
		for _, module := range resources {
			if module.Kind != "scenery.module" || moduleInstancePath(module) != target.Module {
				continue
			}
			metadata, _ := module.Spec["export_metadata"].(map[string]any)
			for _, raw := range metadata {
				export, _ := raw.(map[string]any)
				valueReference := refString(export["value"])
				if valueReference != reference {
					continue
				}
				return reference, stringListSet(export["patchable"]), nil
			}
		}
		diagnostic := patchDiagnostic("SCN2906", "cross-module patches must target an explicitly patchable export", patch)
		return "", nil, &diagnostic
	}
	_, ok = indexes[moduleResourceAddress(target.Module)]
	if !ok {
		diagnostic := patchDiagnostic("SCN2906", "patch target has no package boundary", patch)
		return "", nil, &diagnostic
	}
	return reference, map[string]bool{}, nil
}

func resourcePointerValue(resource Resource, pointer string) (any, bool) {
	parts := pointerParts(pointer)
	if len(parts) == 0 || parts[0] != "spec" {
		return nil, false
	}
	var current any = resource.Spec
	for _, part := range parts[1:] {
		next, ok := semanticPointerStep(current, part)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func ResourcePointerValue(resource Resource, pointer string) (any, bool) {
	return resourcePointerValue(resource, pointer)
}

func setResourcePointer(resource *Resource, pointer string, value any) bool {
	parts := pointerParts(pointer)
	if len(parts) < 2 || parts[0] != "spec" {
		return false
	}
	var current any = resource.Spec
	for _, part := range parts[1 : len(parts)-1] {
		next, ok := semanticPointerStep(current, part)
		if !ok {
			return false
		}
		current = next
	}
	values, ok := current.(map[string]any)
	if !ok {
		return false
	}
	if _, exists := values[parts[len(parts)-1]]; !exists {
		return false
	}
	values[parts[len(parts)-1]] = value
	return true
}

func semanticPointerStep(current any, part string) (any, bool) {
	switch values := current.(type) {
	case map[string]any:
		if next, ok := values[part]; ok {
			return next, true
		}
		if stringValue(values["name"]) == part {
			return values, true
		}
	case []any:
		if index, err := strconv.Atoi(part); err == nil && index >= 0 && index < len(values) {
			return values[index], true
		}
		for _, raw := range values {
			child, _ := raw.(map[string]any)
			if stringValue(child["name"]) == part {
				return child, true
			}
		}
	}
	return nil, false
}

func pointerParts(pointer string) []string {
	if !strings.HasPrefix(pointer, "/") {
		return nil
	}
	raw := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	parts := make([]string, len(raw))
	for index, part := range raw {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts
}

func patchDiagnostic(code, message string, patch Resource) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: fmt.Sprintf("patch %s: %s", patch.Name, message), Address: patch.Address}
}
