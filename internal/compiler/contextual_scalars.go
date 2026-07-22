package compiler

import (
	"fmt"
	"strings"

	"scenery.sh/internal/scn"
)

func contextualizeResourceScalars(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: result})
	var diagnostics []Diagnostic
	for index := range result {
		resource := &result[index]
		resource.Spec = cloneMapValue(resource.Spec)
		convert := func(container map[string]any, field, typeExpression string) {
			if container == nil || container[field] == nil {
				return
			}
			value, err := contextualizeValue(container[field], typeExpression, resource.Module, byAddress)
			if err != nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1212", Severity: "error", Message: err.Error(), Address: resource.Address, Path: "/spec/" + field})
				return
			}
			container[field] = value
		}
		switch resource.Kind {
		case "scenery.record":
			contextualizeNamedChildren(resource.Spec, "field", func(field map[string]any) {
				if field["default"] != nil {
					typeExpression := typeExpressionText(field["type"])
					value, err := contextualizeValue(field["default"], typeExpression, resource.Module, byAddress)
					if err != nil {
						diagnostics = append(diagnostics, Diagnostic{Code: "SCN1212", Severity: "error", Message: err.Error(), Address: resource.Address, Path: "/spec/field/default"})
					} else {
						field["default"] = value
					}
				}
			})
		case "scenery.service":
			diagnostics = append(diagnostics, contextualizeConfig(resource, byAddress)...)
		case "scenery.data-source", "scenery.execution-engine", "scenery.event-bus", "scenery.secret-store":
			provider := byAddress[resolveResourceRef(*resource, refString(resource.Spec["provider"]), "provider")]
			diagnostics = append(diagnostics, contextualizeConfigWithSchema(resource, provider.Spec["config_schema"], byAddress)...)
		case "scenery.execution":
			convert(resource.Spec, "timeout", "duration")
			convert(resource.Spec, "lease", "duration")
			for _, field := range []string{"initial", "maximum"} {
				if retry, ok := resource.Spec["retry"].(map[string]any); ok {
					convert(retry, field, "duration")
				}
			}
			for _, field := range []string{"success", "failure"} {
				if retention, ok := resource.Spec["retention"].(map[string]any); ok {
					convert(retention, field, "duration")
				}
			}
			if deduplication, ok := resource.Spec["deduplication"].(map[string]any); ok {
				convert(deduplication, "retention", "duration")
			}
		case "scenery.schedule":
			if trigger, ok := resource.Spec["trigger"].(map[string]any); ok {
				convert(trigger, "every", "duration")
				convert(trigger, "at", "datetime")
			}
			if catchup, ok := resource.Spec["catchup"].(map[string]any); ok {
				convert(catchup, "maximum_age", "duration")
			}
		case "scenery.http-gateway":
			if timeouts, ok := resource.Spec["timeouts"].(map[string]any); ok {
				for _, field := range []string{"read", "write", "idle", "total_invocation"} {
					convert(timeouts, field, "duration")
				}
			}
		case "scenery.binding":
			if httpSpec, ok := resource.Spec["http"].(map[string]any); ok {
				if timeouts, ok := httpSpec["timeouts"].(map[string]any); ok {
					for _, field := range []string{"read", "write", "idle", "total_invocation"} {
						convert(timeouts, field, "duration")
					}
				}
				contextualizeNamedChildren(httpSpec, "response", func(response map[string]any) {
					contextualizeNamedChildren(response, "cookie", func(cookie map[string]any) {
						convert(cookie, "expires", "datetime")
					})
				})
			}
		case "scenery.fixture":
			entity := byAddress[resolveResourceRef(*resource, refString(resource.Spec["entity"]), "entity")]
			record := byAddress[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
			if values, ok := resource.Spec["values"].([]any); ok && record.Address != "" {
				for rowIndex, rawRow := range values {
					row, _ := rawRow.(map[string]any)
					converted, err := contextualizeRecordValue(row, record, byAddress)
					if err != nil {
						diagnostics = append(diagnostics, Diagnostic{Code: "SCN1212", Severity: "error", Message: err.Error(), Address: resource.Address, Path: fmt.Sprintf("/spec/values/%d", rowIndex)})
					} else {
						values[rowIndex] = converted
					}
				}
				resource.Spec["values"] = values
			}
		case "scenery.module":
			inputs, _ := resource.Spec["interface_inputs"].(map[string]any)
			for name, raw := range inputs {
				declaration, _ := raw.(map[string]any)
				if declaration["default"] == nil {
					continue
				}
				value, err := contextualizeValue(declaration["default"], typeExpressionText(declaration["type"]), moduleInstancePath(*resource), byAddress)
				if err != nil {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN1212", Severity: "error", Message: err.Error(), Address: resource.Address, Path: "/spec/interface_inputs/" + escapeJSONPointer(name) + "/default"})
				} else {
					declaration["default"] = value
				}
			}
		}
		byAddress[resource.Address] = *resource
	}
	return result, diagnostics
}

func contextualizeNamedChildren(spec map[string]any, name string, visit func(map[string]any)) {
	value := spec[name]
	switch typed := value.(type) {
	case map[string]any:
		copy := cloneMapValue(typed)
		visit(copy)
		spec[name] = copy
	case []any:
		copy := append([]any(nil), typed...)
		for index, raw := range copy {
			child, _ := raw.(map[string]any)
			child = cloneMapValue(child)
			visit(child)
			copy[index] = child
		}
		spec[name] = copy
	}
}

func contextualizeConfig(resource *Resource, resources map[string]Resource) []Diagnostic {
	fields := namedChildren(resource.Spec, "config_schema")
	schema := map[string]any{}
	for _, field := range fields {
		schema[stringValue(field["name"])] = field
	}
	return contextualizeConfigWithSchema(resource, schema, resources)
}

func contextualizeConfigWithSchema(resource *Resource, rawSchema any, resources map[string]Resource) []Diagnostic {
	config, _ := resource.Spec["config"].(map[string]any)
	schema, _ := rawSchema.(map[string]any)
	if config == nil || schema == nil {
		return nil
	}
	config = cloneMapValue(config)
	var diagnostics []Diagnostic
	for name, value := range config {
		field, _ := schema[name].(map[string]any)
		typeExpression := typeExpressionText(field["type"])
		if typeExpression == "" {
			typeExpression = stringValue(field["type"])
		}
		converted, err := contextualizeValue(value, typeExpression, resource.Module, resources)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1212", Severity: "error", Message: err.Error(), Address: resource.Address, Path: "/spec/config/" + escapeJSONPointer(name)})
			continue
		}
		config[name] = converted
	}
	resource.Spec["config"] = config
	return diagnostics
}

func contextualizeValue(value any, typeExpression, module string, resources map[string]Resource) (any, error) {
	typeExpression = strings.TrimSpace(typeExpression)
	for _, wrapper := range []string{"optional", "nullable"} {
		prefix := wrapper + "("
		if strings.HasPrefix(typeExpression, prefix) && strings.HasSuffix(typeExpression, ")") {
			if value == nil {
				return nil, nil
			}
			return contextualizeValue(value, strings.TrimSpace(typeExpression[len(prefix):len(typeExpression)-1]), module, resources)
		}
	}
	for _, wrapper := range []string{"list", "set"} {
		prefix := wrapper + "("
		if strings.HasPrefix(typeExpression, prefix) && strings.HasSuffix(typeExpression, ")") {
			items, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("%s literal must be a list", typeExpression)
			}
			result := make([]any, len(items))
			for index, item := range items {
				converted, err := contextualizeValue(item, strings.TrimSpace(typeExpression[len(prefix):len(typeExpression)-1]), module, resources)
				if err != nil {
					return nil, err
				}
				result[index] = converted
			}
			return result, nil
		}
	}
	if strings.HasPrefix(typeExpression, "map(") && strings.HasSuffix(typeExpression, ")") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s literal must be an object", typeExpression)
		}
		result := make(map[string]any, len(object))
		for key, item := range object {
			converted, err := contextualizeValue(item, strings.TrimSpace(typeExpression[len("map("):len(typeExpression)-1]), module, resources)
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	}
	recordAddress := ""
	if strings.HasPrefix(typeExpression, "record.") {
		recordAddress = resourceAddress(module, "record", strings.TrimPrefix(typeExpression, "record."))
	} else if strings.Contains(typeExpression, "/record/") {
		recordAddress = typeExpression
	}
	if record := resources[recordAddress]; record.Address != "" {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("record literal for %s must be an object", typeExpression)
		}
		return contextualizeRecordValue(object, record, resources)
	}
	text, isString := value.(string)
	if !isString {
		return value, nil
	}
	return scn.ContextualizePrimitive(text, typeExpression)
}

func contextualizeRecordValue(value map[string]any, record Resource, resources map[string]Resource) (map[string]any, error) {
	result := cloneMapValue(value)
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	for name, raw := range result {
		field := fields[name]
		if field == nil {
			continue
		}
		converted, err := contextualizeValue(raw, typeExpressionText(field["type"]), record.Module, resources)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
		result[name] = converted
	}
	return result, nil
}

func typeExpressionText(value any) string {
	if reference := refString(value); reference != "" {
		return reference
	}
	if expression, ok := value.(map[string]any); ok {
		text, _ := expression["$expression"].(string)
		return strings.TrimSpace(text)
	}
	return stringValue(value)
}
