package compiler

import (
	"fmt"
	"sort"
	"strings"
)

var crudActions = map[string]bool{"list": true, "get": true, "create": true, "update": true, "delete": true}

func expandDataResources(resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	for index := range result {
		if result[index].Kind != "scenery.crud" {
			continue
		}
		actions := canonicalStrings(stringValues(result[index].Spec["actions"]))
		canonicalActions := make([]any, len(actions))
		for actionIndex, action := range actions {
			canonicalActions[actionIndex] = action
		}
		result[index].Spec = cloneMapValue(result[index].Spec)
		result[index].Spec["actions"] = canonicalActions
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	occupied := make(map[string]bool, len(resources))
	for _, resource := range resources {
		occupied[resource.Address] = true
	}
	var diagnostics []Diagnostic
	for _, crud := range resources {
		if crud.Kind != "scenery.crud" || crud.Origin.Kind == "expanded" {
			continue
		}
		generated, err := expandCRUDResource(byAddress, crud)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2504", Severity: "error", Message: err.Error(), Address: crud.Address})
			continue
		}
		collision := false
		for _, resource := range generated {
			if occupied[resource.Address] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2510", Severity: "error", Message: "CRUD derived address collides with an existing resource " + resource.Address, Address: crud.Address, Related: []Related{{Address: resource.Address}}})
				collision = true
			}
		}
		if collision {
			continue
		}
		for index := range generated {
			markExpansionFieldProvenance(&generated[index], crud)
			resource := generated[index]
			occupied[resource.Address] = true
			byAddress[resource.Address] = resource
			result = append(result, resource)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, diagnostics
}

func expandCRUDResource(resources map[string]Resource, crud Resource) ([]Resource, error) {
	entityAddress := resolveResourceRef(crud, refString(crud.Spec["entity"]), "entity")
	entity, ok := resources[entityAddress]
	if !ok || entity.Kind != "scenery.entity" {
		return nil, fmt.Errorf("CRUD entity must resolve to an entity")
	}
	recordAddress := resolveResourceRef(entity, refString(entity.Spec["type"]), "record")
	record, ok := resources[recordAddress]
	if !ok || record.Kind != "scenery.record" {
		return nil, fmt.Errorf("CRUD entity type must resolve to a record")
	}
	actions := stringValues(crud.Spec["actions"])
	if len(actions) == 0 {
		return nil, fmt.Errorf("CRUD actions must not be empty")
	}
	actionSet := map[string]bool{}
	for _, action := range actions {
		if !crudActions[action] || actionSet[action] {
			return nil, fmt.Errorf("CRUD action %q is unsupported or duplicated", action)
		}
		actionSet[action] = true
	}
	actions = actions[:0]
	for action := range actionSet {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	primary, entityFields, err := crudEntityFields(record, entity)
	if err != nil {
		return nil, err
	}
	lineage := func(output, rule string) Origin {
		return Origin{Kind: "expanded", SourceID: crud.Origin.SourceID, ModuleChain: append([]string(nil), crud.Origin.ModuleChain...), ExpansionLineage: []ExpansionStep{{Generator: crud.Address, GeneratorSchemaRevision: "scenery.crud", Key: rule, SourceRange: crud.Origin.DeclarationRange, ParentAddress: crud.Address, Output: output}}}
	}
	serviceName := crud.Name + "_data"
	serviceAddress := resourceAddress(crud.Module, "service", serviceName)
	generated := []Resource{{
		Address: serviceAddress, Module: crud.Module, Name: serviceName, Kind: "scenery.service", Origin: lineage(serviceAddress, "service"),
		Spec: map[string]any{"runtime": "provider", "implementation": map[string]any{"adapter": "provider_crud_v1", "entity": map[string]any{"$ref": entity.Address}}},
	}}
	for _, action := range actions {
		inputName, resultName := crud.Name+"_"+action+"_input", crud.Name+"_"+action+"_result"
		inputAddress, resultAddress := resourceAddress(crud.Module, "record", inputName), resourceAddress(crud.Module, "record", resultName)
		inputFields := crudInputFields(action, primary, entityFields)
		resultFields := []any{map[string]any{"name": "value", "type": map[string]any{"$ref": "record." + record.Name}}}
		if action == "list" {
			resultFields = []any{map[string]any{"name": "items", "type": map[string]any{"$expression": "list(record." + record.Name + ")"}}}
		}
		generated = append(generated,
			Resource{Address: inputAddress, Module: crud.Module, Name: inputName, Kind: "scenery.record", Origin: lineage(inputAddress, action+".input"), Spec: map[string]any{"field": inputFields}},
			Resource{Address: resultAddress, Module: crud.Module, Name: resultName, Kind: "scenery.record", Origin: lineage(resultAddress, action+".result"), Spec: map[string]any{"field": resultFields}},
		)
		operationName := crud.Name + "_" + action
		operationAddress := resourceAddress(crud.Module, "operation", operationName)
		executionName := operationName + "_direct"
		executionAddress := resourceAddress(crud.Module, "execution", executionName)
		operation := Resource{
			Address: operationAddress, Module: crud.Module, Name: operationName, Kind: "scenery.operation", Origin: lineage(operationAddress, action+".operation"),
			Spec: map[string]any{
				"service": map[string]any{"$ref": "service." + serviceName}, "input": map[string]any{"$ref": "record." + inputName},
				"handler": map[string]any{"adapter": "provider_crud_v1", "action": action, "entity": map[string]any{"$ref": entity.Address}},
				"result":  map[string]any{"name": "ok", "type": map[string]any{"$ref": "record." + resultName}},
			},
		}
		if action == "get" || action == "update" || action == "delete" {
			operation.Spec["error"] = map[string]any{"name": "not_found", "type": map[string]any{"$ref": "std.type.problem"}}
		}
		executionSpec := cloneMapValue(crud.Spec["execution"])
		executionSpec["operation"] = map[string]any{"$ref": "operation." + operationName}
		if executionSpec["mode"] == nil {
			executionSpec["mode"] = "direct"
		}
		generated = append(generated, operation, Resource{Address: executionAddress, Module: crud.Module, Name: executionName, Kind: "scenery.execution", Origin: lineage(executionAddress, action+".execution"), Spec: executionSpec})
		if httpSpec, ok := crud.Spec["http"].(map[string]any); ok {
			binding := expandCRUDHTTPBinding(crud, entity, record, action, operationName, executionName, primary, entityFields, httpSpec, lineage)
			generated = append(generated, binding)
		}
		if internalSpec, ok := crud.Spec["internal"].(map[string]any); ok {
			bindingName := operationName + "_internal"
			address := resourceAddress(crud.Module, "binding", bindingName)
			visibility := defaultString(stringValue(internalSpec["visibility"]), "application")
			exposure := "application"
			if visibility == "package" {
				exposure = "local"
			}
			generated = append(generated, Resource{Address: address, Module: crud.Module, Name: bindingName, Kind: "scenery.binding", Origin: lineage(address, action+".internal"), Spec: map[string]any{
				"operation": map[string]any{"$ref": "operation." + operationName}, "execution": map[string]any{"$ref": "execution." + executionName}, "protocol": "internal", "delivery": "call", "exposure": exposure,
				"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": internalSpec["authorization"], "pipeline": internalSpec["pipeline"], "internal": map[string]any{"visibility": visibility, "principal": "inherit"},
			}})
		}
	}
	return generated, nil
}

func crudEntityFields(record, entity Resource) ([]map[string]any, []map[string]any, error) {
	recordFields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		recordFields[stringValue(field["name"])] = field
	}
	var primary, all []map[string]any
	for _, mapping := range namedChildren(entity.Spec, "field") {
		name := stringValue(mapping["name"])
		field, ok := recordFields[name]
		if !ok {
			return nil, nil, fmt.Errorf("entity field %s does not exist in %s", name, record.Address)
		}
		copy := cloneMapValue(field)
		copy["entity_mapping"] = cloneMapValue(mapping)
		all = append(all, copy)
		if mapping["primary_key"] == true {
			primary = append(primary, copy)
		}
	}
	if len(primary) == 0 {
		return nil, nil, fmt.Errorf("CRUD entity requires at least one primary key")
	}
	sort.Slice(primary, func(i, j int) bool { return stringValue(primary[i]["name"]) < stringValue(primary[j]["name"]) })
	sort.Slice(all, func(i, j int) bool { return stringValue(all[i]["name"]) < stringValue(all[j]["name"]) })
	return primary, all, nil
}

func crudInputFields(action string, primary, all []map[string]any) []any {
	selected := all
	if action == "get" || action == "delete" {
		selected = crudKeyFields(primary, all)
	} else if action == "list" {
		selected = crudTenantFields(all)
	} else if action == "update" {
		selected = nil
		for _, field := range all {
			mapping, _ := field["entity_mapping"].(map[string]any)
			if mapping["immutable"] == true && mapping["primary_key"] != true && mapping["tenant_key"] != true {
				continue
			}
			selected = append(selected, field)
		}
	}
	primaryNames := map[string]bool{}
	for _, field := range primary {
		primaryNames[stringValue(field["name"])] = true
	}
	result := make([]any, 0, len(selected))
	for _, field := range selected {
		copy := map[string]any{"name": field["name"], "type": field["type"]}
		mapping, _ := field["entity_mapping"].(map[string]any)
		if action == "create" && mapping["default"] != nil || action == "update" && !primaryNames[stringValue(field["name"])] && mapping["tenant_key"] != true {
			copy["type"] = map[string]any{"$expression": "optional(" + typeExpression(field["type"]) + ")"}
		}
		result = append(result, copy)
	}
	return result
}

func crudKeyFields(primary, all []map[string]any) []map[string]any {
	selected := append([]map[string]any(nil), primary...)
	seen := map[string]bool{}
	for _, field := range selected {
		seen[stringValue(field["name"])] = true
	}
	for _, field := range crudTenantFields(all) {
		if !seen[stringValue(field["name"])] {
			selected = append(selected, field)
		}
	}
	return selected
}

func crudTenantFields(all []map[string]any) []map[string]any {
	var selected []map[string]any
	for _, field := range all {
		mapping, _ := field["entity_mapping"].(map[string]any)
		if mapping["tenant_key"] == true {
			selected = append(selected, field)
		}
	}
	return selected
}

func expandCRUDHTTPBinding(crud, entity, record Resource, action, operationName, executionName string, primary, all []map[string]any, httpSpec map[string]any, lineage func(string, string) Origin) Resource {
	name := operationName + "_http"
	address := resourceAddress(crud.Module, "binding", name)
	basePath := strings.TrimSuffix(stringValue(httpSpec["path"]), "/")
	method := map[string]string{"list": "GET", "get": "GET", "create": "POST", "update": "PATCH", "delete": "DELETE"}[action]
	path := basePath
	child := map[string]any{"method": method, "path": path, "codec_profile": httpSpec["codec_profile"]}
	if action == "get" || action == "update" || action == "delete" {
		for _, field := range primary {
			wire := wireName(field, stringValue(field["name"]))
			path += "/{" + wire + "}"
			mapping := map[string]any{"name": wire, "to": map[string]any{"$ref": "operation." + operationName + ".input." + stringValue(field["name"])}}
			appendNamedChild(child, "path_parameter", mapping)
		}
		child["path"] = path
	}
	var bodyExcept []any
	for _, field := range crudTenantFields(all) {
		name := stringValue(field["name"])
		appendNamedChild(child, "context", map[string]any{
			"name": name, "from": map[string]any{"$ref": "principal.tenant_id"}, "to": map[string]any{"$ref": "operation." + operationName + ".input." + name},
		})
		bodyExcept = append(bodyExcept, map[string]any{"$ref": "operation." + operationName + ".input." + name})
	}
	if action == "create" || action == "update" {
		body := map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation." + operationName + ".input"}}
		if action == "update" {
			for _, field := range primary {
				bodyExcept = append(bodyExcept, map[string]any{"$ref": "operation." + operationName + ".input." + stringValue(field["name"])})
			}
		}
		if len(bodyExcept) > 0 {
			body["except"] = bodyExcept
		}
		child["body"] = body
	}
	response := map[string]any{"name": "ok", "when": map[string]any{"$ref": "result.ok"}, "status": map[string]string{"create": "201", "delete": "200"}[action]}
	if response["status"] == "" {
		response["status"] = "200"
	}
	response["body"] = map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.ok"}}
	responses := []any{response}
	if action == "get" || action == "update" || action == "delete" {
		responses = append(responses, map[string]any{
			"name": "not_found", "when": map[string]any{"$ref": "error.not_found"}, "status": "404",
			"body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "error.not_found"}},
		})
	}
	child["response"] = responses
	return Resource{Address: address, Module: crud.Module, Name: name, Kind: "scenery.binding", Origin: lineage(address, action+".http"), Spec: map[string]any{
		"gateway": httpSpec["gateway"], "operation": map[string]any{"$ref": "operation." + operationName}, "execution": map[string]any{"$ref": "execution." + executionName},
		"protocol": "http", "delivery": "call", "exposure": defaultString(stringValue(httpSpec["exposure"]), "internet"), "authentication": httpSpec["authentication"], "authorization": httpSpec["authorization"], "pipeline": httpSpec["pipeline"], "http": child,
	}}
}

func appendNamedChild(spec map[string]any, name string, value map[string]any) {
	if current, ok := spec[name]; ok {
		switch typed := current.(type) {
		case []any:
			spec[name] = append(typed, value)
		default:
			spec[name] = []any{typed, value}
		}
		return
	}
	spec[name] = value
}

func cloneMapValue(value any) map[string]any {
	result := map[string]any{}
	if source, ok := value.(map[string]any); ok {
		for key, item := range source {
			result[key] = item
		}
	}
	return result
}
