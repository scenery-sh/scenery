package vnext

import (
	"fmt"
	"sort"
	"strings"
)

func setFieldProvenanceTree(origin *Origin, value any, path string, field FieldProvenance) {
	if origin == nil {
		return
	}
	if origin.FieldProvenance == nil {
		origin.FieldProvenance = map[string]FieldProvenance{}
	}
	var walk func(any, string)
	walk = func(current any, currentPath string) {
		switch typed := current.(type) {
		case map[string]any:
			if typed["$ref"] != nil || typed["$scalar"] != nil || typed["$expression"] != nil {
				return
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				childPath := provenanceChildPath(currentPath, key)
				origin.FieldProvenance[childPath] = field
				walk(typed[key], childPath)
			}
		case []any:
			for index, item := range typed {
				childPath := provenanceChildPath(currentPath, fmt.Sprintf("%d", index))
				origin.FieldProvenance[childPath] = field
				walk(item, childPath)
			}
		}
	}
	walk(value, path)
}

func setFieldProvenance(origin *Origin, path string, value any, field FieldProvenance) {
	if origin == nil {
		return
	}
	if origin.FieldProvenance == nil {
		origin.FieldProvenance = map[string]FieldProvenance{}
	}
	origin.FieldProvenance[path] = field
	setFieldProvenanceTree(origin, value, path, field)
}

func markExpansionFieldProvenance(resource *Resource, generator Resource) {
	if resource == nil {
		return
	}
	field := FieldProvenance{
		Kind: "expansion", DeclaredAt: generator.Origin.DeclarationRange, ProvidedBy: generator.Address,
		SourceAddress: generator.Address, Transformations: []string{"declarative_expansion"},
	}
	setFieldProvenanceTree(&resource.Origin, resource.Spec, "/spec", field)
}

func authoredFieldProvenance(block *Block, path, sourceAddress, module string) map[string]FieldProvenance {
	result := map[string]FieldProvenance{}
	var visit func(*Block, string)
	visit = func(current *Block, currentPath string) {
		if current == nil {
			return
		}
		attributeNames := make([]string, 0, len(current.Attributes))
		for name := range current.Attributes {
			attributeNames = append(attributeNames, name)
		}
		sort.Strings(attributeNames)
		for _, name := range attributeNames {
			expression := current.Attributes[name]
			fieldPath := provenanceChildPath(currentPath, name)
			field := authoredExpressionProvenance(expression, sourceAddress, module)
			result[fieldPath] = field
			collectAuthoredExpressionProvenance(result, expression, expressionValue(expression), fieldPath, sourceAddress, module)
		}
		counts := map[string]int{}
		for _, child := range current.Blocks {
			counts[child.Type]++
		}
		indexes := map[string]int{}
		for _, child := range current.Blocks {
			childPath := provenanceChildPath(currentPath, child.Type)
			if counts[child.Type] > 1 {
				childPath = provenanceChildPath(childPath, fmt.Sprintf("%d", indexes[child.Type]))
			}
			indexes[child.Type]++
			rng := child.Range
			result[childPath] = FieldProvenance{Kind: "authored", DeclaredAt: &rng, SourceAddress: sourceAddress}
			if len(child.Labels) > 0 {
				result[provenanceChildPath(childPath, "name")] = FieldProvenance{Kind: "authored", DeclaredAt: &rng, SourceAddress: sourceAddress}
			}
			visit(child, childPath)
		}
	}
	visit(block, path)
	return result
}

func authoredExpressionProvenance(expression Expression, sourceAddress, module string) FieldProvenance {
	rng := expression.Range
	field := FieldProvenance{Kind: "authored", DeclaredAt: &rng, SourceAddress: sourceAddress}
	if reference := refString(expressionValue(expression)); reference != "" {
		field.Input = reference
		field.ProvidedBy = referenceProviderAddress(reference, module)
	}
	return field
}

func collectAuthoredExpressionProvenance(result map[string]FieldProvenance, expression Expression, value any, path, sourceAddress, module string) {
	if result == nil {
		return
	}
	if reference := refString(value); reference != "" {
		field := authoredExpressionProvenance(expression, sourceAddress, module)
		field.Input = reference
		field.ProvidedBy = referenceProviderAddress(reference, module)
		result[path] = field
		return
	}
	var visit func(any, string, string)
	visit = func(current any, currentPath, relativePath string) {
		switch typed := current.(type) {
		case map[string]any:
			if refString(typed) != "" {
				field := authoredExpressionProvenance(expression, sourceAddress, module)
				if rng, ok := expression.ValueRanges[relativePath]; ok {
					field.DeclaredAt = &rng
				}
				field.Input = refString(typed)
				field.ProvidedBy = referenceProviderAddress(field.Input, module)
				result[currentPath] = field
				return
			}
			keys := make([]string, 0, len(typed))
			for name := range typed {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			for _, name := range keys {
				childPath := provenanceChildPath(currentPath, name)
				childRelative := provenanceChildPath(relativePath, name)
				field := authoredExpressionProvenance(expression, sourceAddress, module)
				if rng, ok := expression.ValueRanges[childRelative]; ok {
					field.DeclaredAt = &rng
				}
				result[childPath] = field
				visit(typed[name], childPath, childRelative)
			}
		case []any:
			for index, child := range typed {
				segment := fmt.Sprintf("%d", index)
				childPath := provenanceChildPath(currentPath, segment)
				childRelative := provenanceChildPath(relativePath, segment)
				field := authoredExpressionProvenance(expression, sourceAddress, module)
				if rng, ok := expression.ValueRanges[childRelative]; ok {
					field.DeclaredAt = &rng
				}
				result[childPath] = field
				visit(child, childPath, childRelative)
			}
		}
	}
	visit(value, path, "")
}

func referenceProviderAddress(reference, module string) string {
	if reference == "" {
		return ""
	}
	if strings.Contains(reference, "/") || strings.HasPrefix(reference, "std.") || strings.HasPrefix(reference, "input.") || strings.HasPrefix(reference, "result.") || strings.HasPrefix(reference, "error.") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) >= 3 && parts[0] == "module" {
		return resourceAddress(module, "module", parts[1]) + "/export/" + strings.Join(parts[2:], "/")
	}
	if len(parts) == 2 && parts[0] == "var" {
		return moduleResourceAddress(module) + "/input/" + parts[1]
	}
	if len(parts) >= 2 {
		return resourceAddress(module, parts[0], parts[1])
	}
	return reference
}

func attachPackageInterfaceProvenance(resource *Resource, sources []*Source, module string) {
	if resource == nil {
		return
	}
	for _, item := range blocksFromSources(sources) {
		block := item.Block
		switch block.Type {
		case "package":
			attachPackageInterfaceBlockProvenance(resource, block, "/spec/package", module)
			if len(block.Labels) == 1 {
				rng := block.Range
				resource.Origin.FieldProvenance["/spec/package/name"] = FieldProvenance{Kind: "authored", DeclaredAt: &rng, SourceAddress: resource.Address}
			}
		case "input":
			if len(block.Labels) == 1 {
				attachPackageInterfaceBlockProvenance(resource, block, provenanceChildPath("/spec/interface_inputs", block.Labels[0]), module)
			}
		case "export":
			if len(block.Labels) != 1 {
				continue
			}
			name := block.Labels[0]
			attachPackageInterfaceBlockProvenance(resource, block, provenanceChildPath("/spec/export_metadata", name), module)
			if expression, ok := block.Attributes["value"]; ok {
				path := provenanceChildPath("/spec/exports", name)
				field := authoredExpressionProvenance(expression, resource.Address, module)
				setFieldProvenance(&resource.Origin, path, expressionValue(expression), field)
				collectAuthoredExpressionProvenance(resource.Origin.FieldProvenance, expression, expressionValue(expression), path, resource.Address, module)
			}
		}
	}
}

func attachPackageInterfaceBlockProvenance(resource *Resource, block *Block, path, module string) {
	if resource == nil || block == nil {
		return
	}
	if resource.Origin.FieldProvenance == nil {
		resource.Origin.FieldProvenance = map[string]FieldProvenance{}
	}
	rng := block.Range
	resource.Origin.FieldProvenance[path] = FieldProvenance{Kind: "authored", DeclaredAt: &rng, SourceAddress: resource.Address}
	for fieldPath, field := range authoredFieldProvenance(block, path, resource.Address, module) {
		resource.Origin.FieldProvenance[fieldPath] = field
	}
}

func markResolvedReferenceProvenance(resource *Resource, before, after any, path, module string, inputProvenance map[string]FieldProvenance) {
	if resource == nil {
		return
	}
	if reference := refString(before); reference != "" {
		if semanticEqual(before, after) {
			return
		}
		field := nearestFieldProvenance(resource.Origin, path)
		if strings.HasPrefix(reference, "var.") {
			if supplied, ok := inputProvenance[strings.TrimPrefix(reference, "var.")]; ok {
				field = supplied
			}
		} else if strings.HasPrefix(reference, "module.") {
			field.Kind = "module_export"
			field.Transformations = appendUniqueString(field.Transformations, "module_export_substitution")
		} else {
			field.Kind = "reference"
			field.Transformations = appendUniqueString(field.Transformations, "reference_resolution")
		}
		field.Input = reference
		field.ProvidedBy = referenceProviderAddress(reference, module)
		field.SourceAddress = resource.Address
		setFieldProvenance(&resource.Origin, path, after, field)
		return
	}
	switch oldValue := before.(type) {
	case map[string]any:
		newValue, _ := after.(map[string]any)
		for name, child := range oldValue {
			markResolvedReferenceProvenance(resource, child, newValue[name], provenanceChildPath(path, name), module, inputProvenance)
		}
	case []any:
		newValue, _ := after.([]any)
		for index, child := range oldValue {
			var resolved any
			if index < len(newValue) {
				resolved = newValue[index]
			}
			markResolvedReferenceProvenance(resource, child, resolved, provenanceChildPath(path, fmt.Sprintf("%d", index)), module, inputProvenance)
		}
	}
}

func markContextualScalarProvenance(before, after []Resource) {
	beforeByAddress := map[string]Resource{}
	for _, resource := range before {
		beforeByAddress[resource.Address] = resource
	}
	for index := range after {
		previous, ok := beforeByAddress[after[index].Address]
		if !ok {
			continue
		}
		var visit func(any, any, string)
		visit = func(oldValue, newValue any, path string) {
			if scalar, ok := newValue.(map[string]any); ok && stringValue(scalar["$scalar"]) != "" {
				oldCanonical, _ := MarshalCanonical(oldValue)
				newCanonical, _ := MarshalCanonical(newValue)
				if string(oldCanonical) != string(newCanonical) {
					field := nearestFieldProvenance(after[index].Origin, path)
					field.Transformations = appendUniqueString(field.Transformations, "contextual_"+stringValue(scalar["$scalar"]))
					setFieldProvenance(&after[index].Origin, path, newValue, field)
				}
				return
			}
			switch typed := newValue.(type) {
			case map[string]any:
				oldMap, _ := oldValue.(map[string]any)
				for name, child := range typed {
					visit(oldMap[name], child, provenanceChildPath(path, name))
				}
			case []any:
				oldItems, _ := oldValue.([]any)
				for itemIndex, child := range typed {
					var oldChild any
					if itemIndex < len(oldItems) {
						oldChild = oldItems[itemIndex]
					}
					visit(oldChild, child, provenanceChildPath(path, fmt.Sprintf("%d", itemIndex)))
				}
			}
		}
		visit(previous.Spec, after[index].Spec, "/spec")
	}
}

func completeFieldProvenance(resources []Resource, stage string) {
	for index := range resources {
		var visit func(any, string)
		visit = func(value any, path string) {
			switch typed := value.(type) {
			case map[string]any:
				if typed["$ref"] != nil || typed["$scalar"] != nil || typed["$expression"] != nil {
					return
				}
				keys := make([]string, 0, len(typed))
				for name := range typed {
					keys = append(keys, name)
				}
				sort.Strings(keys)
				for _, name := range keys {
					childPath := provenanceChildPath(path, name)
					ensureFieldProvenance(&resources[index], childPath, stage)
					visit(typed[name], childPath)
				}
			case []any:
				for itemIndex, child := range typed {
					childPath := provenanceChildPath(path, fmt.Sprintf("%d", itemIndex))
					ensureFieldProvenance(&resources[index], childPath, stage)
					visit(child, childPath)
				}
			}
		}
		visit(resources[index].Spec, "/spec")
	}
}

func provenanceChildPath(parent, name string) string {
	return parent + "/" + escapeJSONPointer(name)
}

type provenanceNamedChild struct {
	Value map[string]any
	Path  string
}

func provenanceNamedChildren(parent map[string]any, key, parentPath string) []provenanceNamedChild {
	if parent == nil {
		return nil
	}
	base := provenanceChildPath(parentPath, key)
	switch value := parent[key].(type) {
	case map[string]any:
		return []provenanceNamedChild{{Value: value, Path: base}}
	case []any:
		result := make([]provenanceNamedChild, 0, len(value))
		for index, item := range value {
			child, ok := item.(map[string]any)
			if ok {
				result = append(result, provenanceNamedChild{Value: child, Path: provenanceChildPath(base, fmt.Sprintf("%d", index))})
			}
		}
		return result
	default:
		return nil
	}
}

func rebaseFieldProvenance(origin *Origin, from, to string) {
	if origin == nil || from == to || len(origin.FieldProvenance) == 0 {
		return
	}
	updates := map[string]FieldProvenance{}
	for path, field := range origin.FieldProvenance {
		if path == from || strings.HasPrefix(path, from+"/") {
			updates[to+strings.TrimPrefix(path, from)] = field
			delete(origin.FieldProvenance, path)
		}
	}
	for path, field := range updates {
		origin.FieldProvenance[path] = field
	}
}

func ensureFieldProvenance(resource *Resource, path, stage string) {
	if _, ok := resource.Origin.FieldProvenance[path]; ok {
		return
	}
	field := nearestFieldProvenance(resource.Origin, path)
	if field.Kind == "" {
		field = FieldProvenance{Kind: "derived", ProvidedBy: resource.Address, SourceAddress: resource.Address, Transformations: []string{"compiler_" + stage}}
	}
	if resource.Origin.FieldProvenance == nil {
		resource.Origin.FieldProvenance = map[string]FieldProvenance{}
	}
	resource.Origin.FieldProvenance[path] = field
}

func nearestFieldProvenance(origin Origin, path string) FieldProvenance {
	for candidate := path; candidate != ""; {
		if field, ok := origin.FieldProvenance[candidate]; ok {
			return field
		}
		index := strings.LastIndex(candidate, "/")
		if index <= 0 {
			break
		}
		candidate = candidate[:index]
	}
	return FieldProvenance{}
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
