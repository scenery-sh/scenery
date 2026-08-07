// Package mcpprojection converts the canonical expanded Scenery graph into the
// provider-neutral MCP capability manifest.  It deliberately consumes only
// compiler/graph output: source parsing, generation, and provider adapters do
// not belong in this package.
package mcpprojection

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
)

const (
	mcpConnectionKind = "scenery.mcp-connection"
	mcpServerKind     = "scenery.mcp-server"
	assistantKind     = "scenery.assistant"
	bindingKind       = "scenery.binding"
	operationKind     = "scenery.operation"
	executionKind     = "scenery.execution"
	recordKind        = "scenery.record"
	enumKind          = "scenery.enum"
	unionKind         = "scenery.union"
	secretKind        = "scenery.secret"
)

const (
	projectionTypeCode       = "SCN2421"
	sensitiveOutputCode      = "SCN2422"
	toolIdentityCode         = "SCN2420"
	projectionLimitsCode     = "SCN2419"
	projectionConnectionCode = "SCN2428"
)

const (
	maxInputBytes  = int64(16 << 20)
	maxResultBytes = int64(16 << 20)
)

// Project accepts either a *compiler.Result or a *graph.Manifest.  A compiler
// result is preferred because it carries the authored workspace revision;
// callers with an already-selected expanded manifest can use the graph form,
// which falls back to that manifest's specification revision.
func Project(input any, server string) (mcpcontract.Manifest, error) {
	var manifest *graph.Manifest
	sourceRevision := ""
	implementationRevision := ""
	switch value := input.(type) {
	case *compiler.Result:
		if value == nil {
			return mcpcontract.Manifest{}, projectionError("failed_precondition", "compiler result is nil")
		}
		if !value.Valid() {
			return mcpcontract.Manifest{}, projectionError("failed_precondition", "compiler result is invalid")
		}
		var err error
		manifest, err = value.ManifestForView("expanded")
		if err != nil {
			return mcpcontract.Manifest{}, projectionError("failed_precondition", err.Error())
		}
		sourceRevision = value.WorkspaceRevision
		implementationRevision = firstRevision(value.ImplementationRevisions)
	case *graph.Manifest:
		manifest = value
	case nil:
		return mcpcontract.Manifest{}, projectionError("failed_precondition", "expanded manifest is nil")
	default:
		return mcpcontract.Manifest{}, projectionError("invalid_request", fmt.Sprintf("unsupported projection input %T", input))
	}
	if manifest == nil {
		return mcpcontract.Manifest{}, projectionError("failed_precondition", "expanded manifest is nil")
	}
	if sourceRevision == "" {
		sourceRevision = manifest.SpecRevision
	}
	return projectManifest(manifest, sourceRevision, implementationRevision, server)
}

// ProjectManifest projects an expanded graph and uses sourceRevision as the
// source/workspace identity in the generated capability manifest.
func ProjectManifest(manifest *graph.Manifest, sourceRevision, server string) (mcpcontract.Manifest, error) {
	if manifest == nil {
		return mcpcontract.Manifest{}, projectionError("failed_precondition", "expanded manifest is nil")
	}
	return projectManifest(manifest, sourceRevision, "", server)
}

func projectManifest(manifest *graph.Manifest, sourceRevision, implementationRevision, server string) (mcpcontract.Manifest, error) {
	if strings.TrimSpace(manifest.ContractRevision) == "" {
		return mcpcontract.Manifest{}, projectionError("failed_precondition", "expanded manifest has no contract revision")
	}
	resources := resourcesByAddress(manifest)
	serverResource, err := selectServer(resources, server)
	if err != nil {
		return mcpcontract.Manifest{}, err
	}
	capabilities, err := projectCapabilities(serverResource, resources)
	if err != nil {
		return mcpcontract.Manifest{}, err
	}
	connections, err := projectConnections(serverResource, resources)
	if err != nil {
		return mcpcontract.Manifest{}, err
	}

	// Construct through JSON so the projection stays coupled to the stable
	// provider-neutral contract tags, not to provider/runtime implementation
	// details.  mcpcontract performs strict decoding of the resulting shape.
	payload := map[string]any{
		"kind":              mcpcontract.ManifestKind,
		"schema_revision":   mcpcontract.ManifestSchemaRevision,
		"protocol_version":  mcpcontract.ProtocolVersion,
		"source_revision":   sourceRevision,
		"contract_revision": manifest.ContractRevision,
		"capabilities":      capabilities,
		"connections":       connections,
	}
	if implementationRevision != "" {
		payload["implementation_revision"] = implementationRevision
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return mcpcontract.Manifest{}, fmt.Errorf("mcp projection encode: %w", err)
	}
	var result mcpcontract.Manifest
	if err := json.Unmarshal(encoded, &result); err != nil {
		return mcpcontract.Manifest{}, fmt.Errorf("mcp projection decode: %w", err)
	}
	return result, nil
}

func resourcesByAddress(manifest *graph.Manifest) map[string]graph.Resource {
	result := make(map[string]graph.Resource, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		result[resource.Address] = resource
	}
	return result
}

func firstRevision(revisions map[string]string) string {
	if len(revisions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(revisions))
	for key := range revisions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value := strings.TrimSpace(revisions[key]); value != "" {
			return value
		}
	}
	return ""
}

func selectServer(resources map[string]graph.Resource, reference string) (graph.Resource, error) {
	if strings.TrimSpace(reference) != "" {
		if !strings.Contains(reference, "/") && !strings.Contains(reference, ".") {
			for _, resource := range resources {
				if resource.Kind == mcpServerKind && resource.Name == reference {
					return resource, nil
				}
			}
		}
		address := resolveAddress(graph.Resource{Module: "app"}, reference, "mcp_server")
		if resource, ok := resources[address]; ok && resource.Kind == mcpServerKind {
			return resource, nil
		}
		return graph.Resource{}, projectionError("invalid_request", "mcp_server "+reference+" was not found")
	}
	servers := make([]graph.Resource, 0)
	for _, resource := range resources {
		if resource.Kind == mcpServerKind {
			servers = append(servers, resource)
		}
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Address < servers[j].Address })
	if len(servers) != 1 {
		return graph.Resource{}, projectionError("failed_precondition", fmt.Sprintf("expected exactly one mcp_server, found %d", len(servers)))
	}
	return servers[0], nil
}

func projectCapabilities(server graph.Resource, resources map[string]graph.Resource) ([]map[string]any, error) {
	children := namedChildren(server.Spec, "capability")
	sort.SliceStable(children, func(i, j int) bool {
		left, right := stringValue(children[i]["name"]), stringValue(children[j]["name"])
		if left != right {
			return left < right
		}
		return refString(children[i]["binding"]) < refString(children[j]["binding"])
	})
	maxInput, err := boundedInteger(server.Spec["max_input_bytes"], maxInputBytes)
	if err != nil {
		return nil, projectionErrorAt(projectionLimitsCode, server.Address, "/spec/max_input_bytes", err.Error())
	}
	maxResult, err := boundedInteger(server.Spec["max_result_bytes"], maxResultBytes)
	if err != nil {
		return nil, projectionErrorAt(projectionLimitsCode, server.Address, "/spec/max_result_bytes", err.Error())
	}
	seen := map[string]string{}
	result := make([]map[string]any, 0, len(children))
	for _, child := range children {
		name := stringValue(child["name"])
		if name == "" {
			name = stringValue(child["label"])
		}
		if name == "" {
			return nil, projectionErrorAt(toolIdentityCode, server.Address, "/spec/capability/name", "MCP capability name is required")
		}
		if previous, exists := seen[name]; exists {
			return nil, projectionErrorAt(toolIdentityCode, server.Address, "/spec/capability/name", "duplicate MCP capability name "+name+" (previous binding "+previous+")")
		}
		bindingAddress := resolveAddress(server, refString(child["binding"]), "binding")
		binding, ok := resources[bindingAddress]
		if !ok || binding.Kind != bindingKind || stringValue(binding.Spec["protocol"]) != "mcp" {
			return nil, projectionErrorAt("SCN2419", server.Address, "/spec/capability/binding", "capability binding must reference an MCP binding")
		}
		seen[name] = binding.Address
		operationAddress := resolveAddress(binding, refString(binding.Spec["operation"]), "operation")
		executionAddress := resolveAddress(binding, refString(binding.Spec["execution"]), "execution")
		operation, ok := resources[operationAddress]
		if !ok || operation.Kind != operationKind {
			return nil, projectionErrorAt(projectionTypeCode, binding.Address, "/spec/operation", "MCP binding operation is unavailable")
		}
		execution, executionOK := resources[executionAddress]
		durable := executionOK && execution.Kind == executionKind && stringValue(execution.Spec["mode"]) == "durable"
		mcp, _ := binding.Spec["mcp"].(map[string]any)
		input, _, err := schemaForType(binding, operation.Spec["input"], operation.Module, resources, newSchemaState())
		if err != nil {
			return nil, projectionErrorAt(projectionTypeCode, binding.Address, "/spec/operation/input", err.Error())
		}
		if !isJSONObjectSchema(input) {
			return nil, projectionErrorAt(projectionTypeCode, binding.Address, "/spec/operation/input", "MCP tool input schema must have an object root")
		}
		output, outputSensitive, err := operationOutputSchema(operation, resources)
		if err != nil {
			return nil, projectionErrorAt(projectionTypeCode, binding.Address, "/spec/operation/result", err.Error())
		}
		if outputSensitive && mcp["allow_sensitive_output"] != true {
			return nil, projectionErrorAt(sensitiveOutputCode, binding.Address, "/spec/mcp/allow_sensitive_output", "sensitive MCP projection requires allow_sensitive_output = true")
		}
		approval := stringValue(child["approval"])
		if approval == "" {
			approval = "never"
		}
		if approval != string(mcpcontract.ApprovalNever) && approval != string(mcpcontract.ApprovalAlways) {
			return nil, projectionErrorAt("SCN2423", server.Address, "/spec/capability/approval", "MCP capability approval policy is unsupported")
		}
		readOnly, destructive := boolValue(mcp["read_only"]), boolValue(mcp["destructive"])
		if readOnly && destructive {
			return nil, projectionErrorAt("SCN2423", binding.Address, "/spec/mcp", "MCP capability cannot be both read_only and destructive")
		}
		auth := map[string]any{"authentication": refOrString(binding.Spec["authentication"]), "authorization": refOrString(binding.Spec["authorization"])}
		effect := map[string]any{
			"read_only":   readOnly,
			"destructive": destructive,
			"idempotent":  boolValue(mcp["idempotent"]),
			"open_world":  boolValue(mcp["open_world"]),
		}
		result = append(result, map[string]any{
			"id": binding.Address, "name": name, "title": stringValue(mcp["title"]), "description": stringValue(mcp["description"]),
			"input_schema": input, "output_schema": output, "operation_address": operationAddress, "execution_address": executionAddress,
			"origin": map[string]any{"kind": "local", "address": binding.Address}, "auth": auth,
			"limits": map[string]any{"max_input_bytes": maxInput, "max_result_bytes": maxResult}, "effect": effect, "approval": approval,
			"allow_sensitive_output": boolValue(mcp["allow_sensitive_output"]), "durable": durable,
		})
	}
	return result, nil
}

func projectConnections(server graph.Resource, resources map[string]graph.Resource) ([]map[string]any, error) {
	children := namedChildren(server.Spec, "connection")
	sort.SliceStable(children, func(i, j int) bool {
		left, right := stringValue(children[i]["namespace"]), stringValue(children[j]["namespace"])
		if left != right {
			return left < right
		}
		return refString(children[i]["connection"]) < refString(children[j]["connection"])
	})
	seen := map[string]string{}
	result := make([]map[string]any, 0, len(children))
	for _, child := range children {
		namespace := stringValue(child["namespace"])
		if namespace == "" {
			return nil, projectionErrorAt(toolIdentityCode, server.Address, "/spec/connection/namespace", "MCP connection namespace is required")
		}
		if previous, exists := seen[namespace]; exists {
			return nil, projectionErrorAt(toolIdentityCode, server.Address, "/spec/connection/namespace", "duplicate MCP connection namespace "+namespace+" (previous connection "+previous+")")
		}
		address := resolveAddress(server, refString(child["connection"]), "mcp_connection")
		connection, ok := resources[address]
		if !ok || connection.Kind != mcpConnectionKind {
			return nil, projectionErrorAt(projectionConnectionCode, server.Address, "/spec/connection/connection", "MCP connection reference is unavailable")
		}
		seen[namespace] = connection.Address
		tools, _ := connection.Spec["tools"].(map[string]any)
		result = append(result, map[string]any{
			"address": connection.Address, "namespace": namespace, "required": boolValue(child["required"]),
			"allow": stringList(tools["allow"]), "block": stringList(tools["block"]),
		})
	}
	return result, nil
}

func operationOutputSchema(operation graph.Resource, resources map[string]graph.Resource) (json.RawMessage, bool, error) {
	results := namedChildren(operation.Spec, "result")
	errors := namedChildren(operation.Spec, "error")
	if len(results) == 0 && len(errors) == 0 {
		return rawSchema(map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"outcome": map[string]any{"type": "string"},
				"value":   map[string]any{"type": "object", "additionalProperties": false, "maxProperties": 0},
			},
			"required": []string{"outcome", "value"},
		}), false, nil
	}
	type outcome struct {
		kind string
		name string
		item map[string]any
	}
	children := make([]outcome, 0, len(results)+len(errors))
	for _, child := range results {
		children = append(children, outcome{kind: "result", name: stringValue(child["name"]), item: child})
	}
	for _, child := range errors {
		children = append(children, outcome{kind: "error", name: stringValue(child["name"]), item: child})
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].name != children[j].name {
			return children[i].name < children[j].name
		}
		return children[i].kind < children[j].kind
	})
	variants := make([]any, 0, len(children))
	scannedNames := map[string]bool{}
	sensitive := false
	for _, child := range children {
		if child.name == "" {
			return nil, false, fmt.Errorf("operation outcome name is required")
		}
		if scannedNames[child.name] {
			return nil, false, fmt.Errorf("duplicate operation outcome name %s", child.name)
		}
		scannedNames[child.name] = true
		schema, itemSensitive, err := schemaForType(operation, child.item["type"], operation.Module, resources, newSchemaState())
		if err != nil {
			return nil, false, err
		}
		sensitive = sensitive || itemSensitive
		fieldName := "value"
		if child.kind == "error" {
			fieldName = "problem"
		}
		variants = append(variants, map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"outcome": map[string]any{"type": "string", "const": child.name},
				fieldName: schema,
			},
			"required": []string{"outcome", fieldName},
		})
	}
	return rawSchema(map[string]any{"type": "object", "oneOf": variants}), sensitive, nil
}

func isJSONObjectSchema(schema any) bool {
	object, ok := schema.(map[string]any)
	return ok && object["type"] == "object"
}

type schemaState struct{ visiting map[string]bool }

func newSchemaState() *schemaState { return &schemaState{visiting: map[string]bool{}} }

func schemaForType(owner graph.Resource, value any, module string, resources map[string]graph.Resource, state *schemaState) (any, bool, error) {
	expression := typeExpression(value)
	if expression == "" {
		return nil, false, fmt.Errorf("type is required")
	}
	if name, args, ok := parseCall(expression); ok {
		switch name {
		case "optional":
			if len(args) != 1 {
				return nil, false, fmt.Errorf("optional requires one type")
			}
			return schemaForType(owner, map[string]any{"$expression": args[0]}, module, resources, state)
		case "nullable":
			if len(args) != 1 {
				return nil, false, fmt.Errorf("nullable requires one type")
			}
			inner, sensitive, err := schemaForType(owner, map[string]any{"$expression": args[0]}, module, resources, state)
			if err != nil {
				return nil, false, err
			}
			return map[string]any{"anyOf": []any{inner, map[string]any{"type": "null"}}}, sensitive, nil
		case "list", "set", "map":
			if len(args) != 1 {
				return nil, false, fmt.Errorf("%s requires one type", name)
			}
			inner, sensitive, err := schemaForType(owner, map[string]any{"$expression": args[0]}, module, resources, state)
			if err != nil {
				return nil, false, err
			}
			if name == "map" {
				return map[string]any{"type": "object", "additionalProperties": inner}, sensitive, nil
			}
			return map[string]any{"type": "array", "items": inner, "uniqueItems": name == "set"}, sensitive, nil
		case "tuple":
			items := make([]any, 0, len(args))
			sensitive := false
			for _, arg := range args {
				item, itemSensitive, err := schemaForType(owner, map[string]any{"$expression": arg}, module, resources, state)
				if err != nil {
					return nil, false, err
				}
				sensitive = sensitive || itemSensitive
				items = append(items, item)
			}
			return map[string]any{"type": "array", "prefixItems": items, "minItems": len(items), "maxItems": len(items)}, sensitive, nil
		}
	}
	if primitive, ok := primitiveSchema(expression); ok {
		return primitive, false, nil
	}
	if strings.HasPrefix(expression, "record.") || strings.Contains(expression, "/record/") {
		address := resolveTypeAddress(module, expression, "record")
		record, ok := resources[address]
		if !ok || record.Kind != recordKind {
			return nil, false, fmt.Errorf("record %s is unavailable", expression)
		}
		return recordSchema(record, resources, state)
	}
	if strings.HasPrefix(expression, "enum.") || strings.Contains(expression, "/enum/") {
		address := resolveTypeAddress(module, expression, "enum")
		enum, ok := resources[address]
		if !ok || enum.Kind != enumKind {
			return nil, false, fmt.Errorf("enum %s is unavailable", expression)
		}
		return enumSchema(enum)
	}
	if strings.HasPrefix(expression, "union.") || strings.Contains(expression, "/union/") {
		address := resolveTypeAddress(module, expression, "union")
		union, ok := resources[address]
		if !ok || union.Kind != unionKind {
			return nil, false, fmt.Errorf("union %s is unavailable", expression)
		}
		return unionSchema(union, resources, state)
	}
	return nil, false, fmt.Errorf("unsupported type %s", expression)
}

func recordSchema(record graph.Resource, resources map[string]graph.Resource, state *schemaState) (any, bool, error) {
	if state.visiting[record.Address] {
		return nil, false, fmt.Errorf("recursive record type %s is not projectable", record.Address)
	}
	state.visiting[record.Address] = true
	defer delete(state.visiting, record.Address)
	fields := namedChildren(record.Spec, "field")
	sort.SliceStable(fields, func(i, j int) bool { return wireName(fields[i]) < wireName(fields[j]) })
	properties := map[string]any{}
	var required []string
	sensitive := false
	for _, field := range fields {
		name := wireName(field)
		if name == "" {
			return nil, false, fmt.Errorf("record field name is required")
		}
		schema, itemSensitive, err := schemaForType(record, field["type"], record.Module, resources, state)
		if err != nil {
			return nil, false, err
		}
		sensitive = sensitive || itemSensitive || boolValue(field["sensitive"])
		applyConstraints(schema, field)
		properties[name] = schema
		if !isOptionalType(field["type"]) {
			required = append(required, name)
		}
	}
	result := map[string]any{"type": "object", "properties": properties}
	if stringValue(record.Spec["unknown_fields"]) == "reject" {
		result["additionalProperties"] = false
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result, sensitive, nil
}

func enumSchema(resource graph.Resource) (any, bool, error) {
	values := namedChildren(resource.Spec, "value")
	sort.SliceStable(values, func(i, j int) bool { return stringValue(values[i]["name"]) < stringValue(values[j]["name"]) })
	result := map[string]any{"type": "string"}
	if !boolValue(resource.Spec["open"]) {
		enum := make([]string, 0, len(values))
		for _, value := range values {
			wire := stringValue(value["wire_value"])
			if wire == "" {
				wire = stringValue(value["name"])
			}
			enum = append(enum, wire)
		}
		result["enum"] = enum
	}
	return result, false, nil
}

func unionSchema(resource graph.Resource, resources map[string]graph.Resource, state *schemaState) (any, bool, error) {
	if state.visiting[resource.Address] {
		return nil, false, fmt.Errorf("recursive union type %s is not projectable", resource.Address)
	}
	state.visiting[resource.Address] = true
	defer delete(state.visiting, resource.Address)
	variants := namedChildren(resource.Spec, "variant")
	sort.SliceStable(variants, func(i, j int) bool { return stringValue(variants[i]["name"]) < stringValue(variants[j]["name"]) })
	items := make([]any, 0, len(variants))
	sensitive := false
	for _, variant := range variants {
		item, itemSensitive, err := schemaForType(resource, variant["type"], resource.Module, resources, state)
		if err != nil {
			return nil, false, err
		}
		sensitive = sensitive || itemSensitive
		items = append(items, item)
	}
	return map[string]any{"oneOf": items}, sensitive, nil
}

func primitiveSchema(expression string) (any, bool) {
	switch expression {
	case "std.type.unit":
		return map[string]any{"type": "object", "additionalProperties": false, "maxProperties": 0}, true
	case "bool":
		return map[string]any{"type": "boolean"}, true
	case "int32":
		return map[string]any{"type": "integer", "format": "int32"}, true
	case "uint32":
		return map[string]any{"type": "integer", "format": "uint32", "minimum": 0, "maximum": uint64(math.MaxUint32)}, true
	case "float32", "float64":
		return map[string]any{"type": "number", "format": expression}, true
	case "bytes":
		return map[string]any{"type": "string", "contentEncoding": "base64"}, true
	case "string":
		return map[string]any{"type": "string"}, true
	case "uuid", "date", "datetime", "duration", "url", "relative_path":
		format := expression
		if expression == "datetime" {
			format = "date-time"
		}
		if expression == "relative_path" {
			format = "scenery-relative-path"
		}
		return map[string]any{"type": "string", "format": format}, true
	case "int", "int64", "uint64", "decimal", "size":
		return map[string]any{"type": "string", "format": "scenery-" + expression}, true
	case "json":
		return map[string]any{}, true
	case "std.type.problem":
		return map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"code", "message"}, "additionalProperties": false}, true
	case "std.type.execution_receipt":
		return map[string]any{"type": "object", "properties": map[string]any{"durable_identity": map[string]any{"type": "string"}, "execution_id": map[string]any{"type": "string"}, "accepted_revision": map[string]any{"type": "string"}, "status_url": map[string]any{"type": "string", "format": "url"}}, "required": []string{"durable_identity", "execution_id", "accepted_revision"}, "additionalProperties": false}, true
	default:
		return nil, false
	}
}

func applyConstraints(schema any, field map[string]any) {
	object, ok := schema.(map[string]any)
	if !ok {
		return
	}
	for _, constraint := range []struct{ schemaName, fieldName string }{
		{schemaName: "minimum", fieldName: "minimum"}, {schemaName: "maximum", fieldName: "maximum"},
		{schemaName: "minLength", fieldName: "min_length"}, {schemaName: "maxLength", fieldName: "max_length"},
		{schemaName: "pattern", fieldName: "pattern"}, {schemaName: "format", fieldName: "format"},
		{schemaName: "minItems", fieldName: "min_items"}, {schemaName: "maxItems", fieldName: "max_items"},
	} {
		name, fieldName := constraint.schemaName, constraint.fieldName
		if value, exists := field[fieldName]; exists {
			object[name] = scalarJSON(value)
		}
	}
	if value, exists := field["unique_items"]; exists {
		object["uniqueItems"] = boolValue(value)
	}
}

func boundedInteger(value any, max int64) (int64, error) {
	text := scalarText(value)
	if text == "" {
		return 0, fmt.Errorf("limit is required")
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil || parsed <= 0 || parsed > max {
		return 0, fmt.Errorf("limit must be positive and <= %d", max)
	}
	return parsed, nil
}

func scalarJSON(value any) any {
	if scalar, ok := value.(map[string]any); ok {
		switch stringValue(scalar["$scalar"]) {
		case "int":
			if parsed, err := strconv.ParseInt(stringValue(scalar["value"]), 10, 64); err == nil {
				return parsed
			}
		case "size":
			if parsed, err := strconv.ParseInt(stringValue(scalar["bytes"]), 10, 64); err == nil {
				return parsed
			}
		case "duration":
			if parsed, err := strconv.ParseInt(stringValue(scalar["nanoseconds"]), 10, 64); err == nil {
				return parsed
			}
		case "decimal":
			if value := stringValue(scalar["value"]); value != "" {
				return value
			}
		}
	}
	return value
}

func scalarText(value any) string {
	if scalar, ok := value.(map[string]any); ok {
		switch stringValue(scalar["$scalar"]) {
		case "int":
			return stringValue(scalar["value"])
		case "size":
			return stringValue(scalar["bytes"])
		case "duration":
			return stringValue(scalar["nanoseconds"])
		default:
			return stringValue(scalar["value"])
		}
	}
	return stringValue(value)
}

func rawSchema(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return json.RawMessage(encoded)
}

func projectionError(kind, message string) error { return fmt.Errorf("%s: %s", kind, message) }

func projectionErrorAt(code, address, path, message string) error {
	return fmt.Errorf("%s: %s at %s%s", code, message, address, path)
}

func namedChildren(parent map[string]any, name string) []map[string]any {
	switch value := parent[name].(type) {
	case map[string]any:
		return []map[string]any{value}
	case []any:
		result := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if child, ok := item.(map[string]any); ok {
				result = append(result, child)
			}
		}
		return result
	default:
		return nil
	}
}

func resolveAddress(owner graph.Resource, reference, kind string) string {
	if reference == "" || strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 {
		return reference
	}
	module := owner.Module
	if isRootKind(kind) || isRootKind(parts[0]) {
		module = "app"
	}
	return graph.ResourceAddress(module, parts[0], parts[1])
}

func resolveTypeAddress(module, expression, kind string) string {
	if strings.Contains(expression, "/") {
		return expression
	}
	parts := strings.Split(expression, ".")
	if len(parts) != 2 {
		return expression
	}
	return graph.ResourceAddress(module, parts[0], parts[1])
}

func isRootKind(kind string) bool {
	switch kind {
	case "application", "workspace", "go_module", "go_toolchain", "go_target", "http_gateway", "authentication", "authorization", "workload_identity", "pipeline", "provider", "data_source", "execution_engine", "event_bus", "secret_store", "secret", "deployment", "typescript_client", "patch", "mcp_connection", "mcp_server", "assistant":
		return true
	default:
		return false
	}
}

func typeExpression(value any) string {
	if reference := refString(value); reference != "" {
		return reference
	}
	if object, ok := value.(map[string]any); ok {
		return strings.TrimSpace(stringValue(object["$expression"]))
	}
	return strings.TrimSpace(stringValue(value))
}

func refString(value any) string {
	if object, ok := value.(map[string]any); ok {
		return strings.TrimSpace(stringValue(object["$ref"]))
	}
	return strings.TrimSpace(stringValue(value))
}

func refOrString(value any) string { return refString(value) }

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	case map[string]any:
		switch stringValue(typed["$scalar"]) {
		case "int":
			return stringValue(typed["value"])
		case "size":
			return stringValue(typed["bytes"])
		case "duration":
			return stringValue(typed["nanoseconds"])
		case "decimal":
			if value := stringValue(typed["value"]); value != "" {
				return value
			}
			coefficient := stringValue(typed["coefficient"])
			scale, _ := strconv.Atoi(stringValue(typed["scale"]))
			if coefficient != "" {
				return decimalText(coefficient, scale)
			}
		default:
			return stringValue(typed["value"])
		}
	}
	return ""
}

func decimalText(coefficient string, scale int) string {
	rational := new(big.Rat)
	if _, ok := rational.SetString(coefficient); !ok {
		return coefficient
	}
	if scale > 0 {
		denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
		rational.Quo(rational, new(big.Rat).SetInt(denominator))
	}
	return rational.FloatString(scale)
}

func boolValue(value any) bool {
	parsed, _ := value.(bool)
	return parsed
}

func stringList(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text := stringValue(item); text != "" {
			result = append(result, text)
		}
	}
	sort.Strings(result)
	return result
}

func wireName(field map[string]any) string {
	if name := stringValue(field["wire_name"]); name != "" {
		return name
	}
	return stringValue(field["name"])
}

func isOptionalType(value any) bool {
	expression := typeExpression(value)
	return strings.HasPrefix(expression, "optional(") && strings.HasSuffix(expression, ")")
}

func schemaName(resource graph.Resource) string {
	return strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(resource.Address)
}

func parseCall(value string) (string, []string, bool) {
	open := strings.IndexByte(value, '(')
	if open <= 0 || !strings.HasSuffix(value, ")") {
		return "", nil, false
	}
	name := strings.TrimSpace(value[:open])
	args, ok := splitTypeArguments(value[open+1 : len(value)-1])
	return name, args, ok
}

func splitTypeArguments(value string) ([]string, bool) {
	if strings.TrimSpace(value) == "" {
		return []string{}, true
	}
	var result []string
	start, depth := 0, 0
	for index, char := range value {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, false
			}
		case ',':
			if depth == 0 {
				item := strings.TrimSpace(value[start:index])
				if item == "" {
					return nil, false
				}
				result = append(result, item)
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	item := strings.TrimSpace(value[start:])
	if item == "" {
		return nil, false
	}
	return append(result, item), true
}
