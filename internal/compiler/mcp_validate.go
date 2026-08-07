package compiler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	scenery "scenery.sh/internal/contract"
)

// MCP source and graph validation deliberately lives in the compiler. The
// runtime may consume the resulting graph, but it must not reinterpret source
// hints (especially effect, approval, or external connection metadata).
const (
	mcpBindingShapeCode      = "SCN2419"
	mcpToolIdentityCode      = "SCN2420"
	mcpProjectionTypeCode    = "SCN2421"
	mcpSensitiveOutputCode   = "SCN2422"
	mcpEffectMetadataCode    = "SCN2423"
	assistantRouteCollision  = "SCN2424"
	assistantSourcePathCode  = "SCN2425"
	assistantPackageLockCode = "SCN2426"
	assistantAdapterCode     = "SCN2427"
	mcpConnectionTransport   = "SCN2428"
	mcpConnectionAuth        = "SCN2429"
	mcpReferenceCycle        = "SCN2430"
)

const (
	mcpMaxToolNameBytes = 128
	mcpMaxInputBytes    = 16 << 20
	mcpMaxResultBytes   = 16 << 20
)

func validateMCPResource(resource Resource, byAddress map[string]Resource) []Diagnostic {
	switch resource.Kind {
	case "scenery.mcp-connection":
		return validateMCPConnection(resource, byAddress)
	case "scenery.mcp-server":
		return validateMCPServer(resource, byAddress)
	case "scenery.assistant":
		return validateAssistantResource(resource, byAddress)
	default:
		return nil
	}
}

func validateMCPBinding(binding Resource, byAddress map[string]Resource) []Diagnostic {
	if stringValue(binding.Spec["protocol"]) != "mcp" {
		return nil
	}
	var diagnostics []Diagnostic
	mcp, ok := binding.Spec["mcp"].(map[string]any)
	if !ok || mcp == nil {
		return []Diagnostic{mcpDiagnostic(mcpBindingShapeCode, "MCP binding requires exactly one mcp child", binding, "/spec/mcp")}
	}
	if stringValue(binding.Spec["delivery"]) != "call" {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "MCP binding delivery must be call", binding, "/spec/delivery"))
	}
	name := stringValue(mcp["name"])
	if !validMCPToolName(name) {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpToolIdentityCode, "MCP tool name must match ^[a-z][a-z0-9_]{0,127}$", binding, "/spec/mcp/name"))
	}
	for _, field := range []string{"read_only", "destructive", "idempotent", "open_world", "allow_sensitive_output"} {
		if raw, present := mcp[field]; present {
			if _, ok := raw.(bool); !ok {
				diagnostics = append(diagnostics, mcpDiagnostic(mcpEffectMetadataCode, "MCP effect metadata "+field+" must be boolean", binding, "/spec/mcp/"+field))
			}
		}
	}
	readOnly, _ := mcp["read_only"].(bool)
	destructive, _ := mcp["destructive"].(bool)
	if readOnly && destructive {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpEffectMetadataCode, "MCP tool cannot be both read_only and destructive", binding, "/spec/mcp"))
	}

	operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
	operation, operationOK := byAddress[operationAddress]
	if !operationOK || operation.Kind != "scenery.operation" {
		return sortMCPDiagnostics(diagnostics)
	}
	if !mcpInputTypeSupported(operation.Spec["input"], operation.Module, byAddress, map[string]bool{}) {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpProjectionTypeCode, "MCP tool input must be std.type.unit or a JSON-object record", binding, "/spec/operation/input"))
	}
	for _, outcomeKind := range []string{"result", "error"} {
		for _, outcome := range namedChildren(operation.Spec, outcomeKind) {
			if !mcpTypeSupported(outcome["type"], operation.Module, byAddress, map[string]bool{}) {
				diagnostics = append(diagnostics, mcpDiagnostic(mcpProjectionTypeCode, "MCP tool outcome type has no exact JSON representation", binding, "/spec/operation/"+outcomeKind))
			}
		}
	}
	if mcpOutputHasSensitiveFields(operation, operation.Module, byAddress, map[string]bool{}) && mcp["allow_sensitive_output"] != true {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpSensitiveOutputCode, "MCP tool sensitive output requires allow_sensitive_output = true", binding, "/spec/mcp/allow_sensitive_output"))
	}

	if idempotency := operation.Spec["idempotency"]; idempotency != nil {
		mode, _ := idempotency.(map[string]any)
		keyed := stringValue(mode["mode"]) == "keyed"
		hint, hintSet := mcp["idempotent"].(bool)
		if hintSet && hint != keyed {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpEffectMetadataCode, "MCP idempotent hint contradicts operation idempotency", binding, "/spec/mcp/idempotent"))
		}
	}
	executionAddress := resolveResourceRef(binding, refString(binding.Spec["execution"]), "execution")
	if execution, ok := byAddress[executionAddress]; ok && execution.Kind == "scenery.execution" {
		if stringValue(execution.Spec["mode"]) == "durable" && stringValue(binding.Spec["delivery"]) != "call" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "MCP durable tools must use call delivery", binding, "/spec/delivery"))
		}
	}
	return sortMCPDiagnostics(diagnostics)
}

func validateMCPConnection(resource Resource, byAddress map[string]Resource) []Diagnostic {
	var diagnostics []Diagnostic
	transport := stringValue(resource.Spec["transport"])
	if transport != "streamable_http" {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, "MCP connection transport must be streamable_http", resource, "/spec/transport"))
	}
	if rawURL := strings.TrimSpace(stringValue(resource.Spec["url"])); rawURL == "" {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, "MCP connection URL is required", resource, "/spec/url"))
	} else if err := validateMCPURL(rawURL); err != nil {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, err.Error(), resource, "/spec/url"))
	}
	if timeout := stringValue(resource.Spec["connect_timeout"]); timeout != "" {
		if !mcpPositiveDuration(timeout) {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, "MCP connect_timeout must be positive", resource, "/spec/connect_timeout"))
		}
	}
	if timeout := stringValue(resource.Spec["call_timeout"]); timeout != "" {
		if !mcpPositiveDuration(timeout) {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, "MCP call_timeout must be positive", resource, "/spec/call_timeout"))
		}
	}

	auth, _ := resource.Spec["auth"].(map[string]any)
	scheme := stringValue(auth["scheme"])
	if scheme == "" {
		scheme = "none"
	}
	secretReference := refString(auth["secret"])
	header := strings.TrimSpace(stringValue(auth["header"]))
	switch scheme {
	case "none":
		if secretReference != "" || header != "" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionAuth, "MCP no-auth connection cannot declare a secret or header", resource, "/spec/auth"))
		}
	case "bearer":
		if !mcpSecretReference(resource, secretReference, byAddress) {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionAuth, "MCP bearer auth requires a typed Scenery secret reference", resource, "/spec/auth/secret"))
		}
		if header != "" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionAuth, "MCP bearer auth cannot declare a custom header", resource, "/spec/auth/header"))
		}
	case "header":
		if !mcpSecretReference(resource, secretReference, byAddress) || !validMCPHeaderName(header) {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionAuth, "MCP header auth requires one valid header name and typed Scenery secret", resource, "/spec/auth"))
		}
	default:
		diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionAuth, "MCP external auth scheme is unsupported", resource, "/spec/auth/scheme"))
	}

	tools, _ := resource.Spec["tools"].(map[string]any)
	allow, block := mcpStringList(tools["allow"]), mcpStringList(tools["block"])
	if len(allow) > 0 && len(block) > 0 {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, "MCP connection tools cannot specify both allow and block", resource, "/spec/tools"))
	}
	for _, names := range [][]string{allow, block} {
		seen := map[string]bool{}
		for _, name := range names {
			if strings.TrimSpace(name) == "" || seen[name] {
				diagnostics = append(diagnostics, mcpDiagnostic(mcpConnectionTransport, "MCP connection tool filters must contain unique non-empty names", resource, "/spec/tools"))
			}
			seen[name] = true
		}
	}
	return sortMCPDiagnostics(diagnostics)
}

func validateMCPServer(resource Resource, byAddress map[string]Resource) []Diagnostic {
	var diagnostics []Diagnostic
	if !mcpPositiveBoundedInteger(resource.Spec["max_input_bytes"], mcpMaxInputBytes) {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "MCP server max_input_bytes must be positive and bounded", resource, "/spec/max_input_bytes"))
	}
	if !mcpPositiveBoundedInteger(resource.Spec["max_result_bytes"], mcpMaxResultBytes) {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "MCP server max_result_bytes must be positive and bounded", resource, "/spec/max_result_bytes"))
	}

	names := map[string]Resource{}
	namespaces := map[string]Resource{}
	for _, capability := range namedChildren(resource.Spec, "capability") {
		name := stringValue(capability["name"])
		if !validMCPToolName(name) {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpToolIdentityCode, "MCP capability name must match ^[a-z][a-z0-9_]{0,127}$", resource, "/spec/capability/name"))
		}
		if previous, exists := names[name]; exists && name != "" {
			diagnostics = append(diagnostics, Diagnostic{Code: mcpToolIdentityCode, Severity: "error", Message: "duplicate MCP tool name " + name, Address: resource.Address, Path: "/spec/capability/name", Related: []Related{{Address: previous.Address}}})
		} else if name != "" {
			names[name] = resource
		}
		bindingAddress := resolveResourceRef(resource, refString(capability["binding"]), "binding")
		binding, ok := byAddress[bindingAddress]
		if !ok || binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "mcp" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "MCP capability binding must reference an MCP binding", resource, "/spec/capability/binding"))
		}
		approval := stringValue(capability["approval"])
		if approval != "" && approval != "always" && approval != "never" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpEffectMetadataCode, "MCP capability approval policy is unsupported", resource, "/spec/capability/approval"))
		}
	}
	for _, connection := range namedChildren(resource.Spec, "connection") {
		namespace := stringValue(connection["namespace"])
		if !validMCPToolName(namespace) {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpToolIdentityCode, "MCP connection namespace must match ^[a-z][a-z0-9_]{0,127}$", resource, "/spec/connection/namespace"))
		}
		if previous, exists := namespaces[namespace]; exists && namespace != "" {
			diagnostics = append(diagnostics, Diagnostic{Code: mcpToolIdentityCode, Severity: "error", Message: "duplicate MCP connection namespace " + namespace, Address: resource.Address, Path: "/spec/connection/namespace", Related: []Related{{Address: previous.Address}}})
		} else if namespace != "" {
			namespaces[namespace] = resource
		}
		connectionAddress := resolveResourceRef(resource, refString(connection["connection"]), "mcp_connection")
		remote, ok := byAddress[connectionAddress]
		if !ok || remote.Kind != "scenery.mcp-connection" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "MCP server connection must reference an mcp_connection", resource, "/spec/connection/connection"))
			continue
		}
		for _, remoteName := range mcpStringList(mcpToolsField(remote, "allow")) {
			final := namespace + "__" + remoteName
			if local, exists := names[final]; exists {
				diagnostics = append(diagnostics, Diagnostic{Code: mcpToolIdentityCode, Severity: "error", Message: "MCP remote tool collides with local tool " + final, Address: resource.Address, Path: "/spec/connection/namespace", Related: []Related{{Address: local.Address}}})
			}
		}
	}
	return sortMCPDiagnostics(diagnostics)
}

func validateAssistantResource(resource Resource, byAddress map[string]Resource) []Diagnostic {
	var diagnostics []Diagnostic
	serverAddress := resolveResourceRef(resource, refString(resource.Spec["mcp_server"]), "mcp_server")
	if serverAddress != "" {
		if server, ok := byAddress[serverAddress]; !ok || server.Kind != "scenery.mcp-server" {
			diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "assistant mcp_server must reference an mcp_server", resource, "/spec/mcp_server"))
		}
	}
	implementation, _ := resource.Spec["implementation"].(map[string]any)
	adapter := stringValue(implementation["adapter"])
	if adapter != "" && adapter != "eve" {
		diagnostics = append(diagnostics, mcpDiagnostic(assistantAdapterCode, "assistant adapter is unsupported", resource, "/spec/implementation/adapter"))
	}
	surface, _ := resource.Spec["surface"].(map[string]any)
	if sessionAccess := stringValue(surface["session_access"]); sessionAccess != "" && sessionAccess != "initiator" {
		diagnostics = append(diagnostics, mcpDiagnostic(mcpBindingShapeCode, "assistant session_access must be initiator", resource, "/spec/surface/session_access"))
	}
	if path := strings.TrimSpace(stringValue(surface["path"])); path != "" {
		if !validAssistantPath(path) {
			diagnostics = append(diagnostics, mcpDiagnostic(assistantSourcePathCode, "assistant surface path must be an absolute normalized path without wildcards", resource, "/spec/surface/path"))
		}
	}
	return sortMCPDiagnostics(diagnostics)
}

func validateMCPGraph(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	graph := map[string][]string{}
	for _, resource := range resources {
		if resource.Kind != "scenery.mcp-server" && resource.Kind != "scenery.mcp-connection" && resource.Kind != "scenery.assistant" {
			continue
		}
		for _, reference := range mcpReferenceValues(resource.Spec) {
			address := resolveResourceRef(resource, reference, "mcp_connection")
			if target, ok := byAddress[address]; ok && (target.Kind == "scenery.mcp-server" || target.Kind == "scenery.mcp-connection" || target.Kind == "scenery.assistant") {
				graph[resource.Address] = append(graph[resource.Address], target.Address)
			}
		}
		sort.Strings(graph[resource.Address])
	}
	state := map[string]uint8{}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(address string) {
		state[address] = 1
		for _, next := range graph[address] {
			if state[next] == 0 {
				visit(next)
				continue
			}
			if state[next] == 1 && !seen[address+"\x00"+next] {
				seen[address+"\x00"+next] = true
				diagnostics = append(diagnostics, Diagnostic{Code: mcpReferenceCycle, Severity: "error", Message: "MCP server and connection references contain a cycle", Address: address, Related: []Related{{Address: next}}})
			}
		}
		state[address] = 2
	}
	addresses := make([]string, 0, len(graph))
	for address := range graph {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		if state[address] == 0 {
			visit(address)
		}
	}
	return sortMCPDiagnostics(diagnostics)
}

func validateMCPAssistantPaths(root string, resources []Resource) []Diagnostic {
	assistants := make([]Resource, 0)
	for _, resource := range resources {
		if resource.Kind == "scenery.assistant" {
			assistants = append(assistants, resource)
		}
	}
	sort.Slice(assistants, func(i, j int) bool { return assistants[i].Address < assistants[j].Address })
	var diagnostics []Diagnostic
	routes := map[string]Resource{}
	for _, assistant := range assistants {
		implementation, _ := assistant.Spec["implementation"].(map[string]any)
		source := strings.TrimSpace(stringValue(implementation["source"]))
		packagePath := strings.TrimSpace(stringValue(implementation["package"]))
		lockPath := strings.TrimSpace(stringValue(implementation["package_lock"]))
		if source == "" || !mcpRelativePath(source) {
			diagnostics = append(diagnostics, mcpDiagnostic(assistantSourcePathCode, "assistant source must be a workspace-relative path", assistant, "/spec/implementation/source"))
		} else if root != "" {
			absolute := filepath.Clean(filepath.Join(root, filepath.FromSlash(source)))
			if !pathWithin(root, absolute) {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantSourcePathCode, "assistant source escapes the workspace", assistant, "/spec/implementation/source"))
			} else if info, err := os.Lstat(absolute); err != nil || !info.IsDir() {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantSourcePathCode, "assistant source must be an available directory", assistant, "/spec/implementation/source"))
			} else if err := rejectPathSymlinks(root, absolute); err != nil {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantSourcePathCode, "assistant source must not traverse symlinks", assistant, "/spec/implementation/source"))
			}
		}
		if packagePath == "" || lockPath == "" || filepath.Base(filepath.FromSlash(lockPath)) != "package-lock.json" {
			diagnostics = append(diagnostics, mcpDiagnostic(assistantPackageLockCode, "assistant implementation requires an exact package and package-lock.json", assistant, "/spec/implementation/package_lock"))
		}
		for _, item := range []struct {
			path    string
			field   string
			regular bool
		}{{packagePath, "/spec/implementation/package", true}, {lockPath, "/spec/implementation/package_lock", true}} {
			path, field, regular := item.path, item.field, item.regular
			if path == "" || !mcpRelativePath(path) {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantPackageLockCode, "assistant package paths must be workspace-relative", assistant, field))
				continue
			}
			if root == "" {
				continue
			}
			absolute := filepath.Clean(filepath.Join(root, filepath.FromSlash(path)))
			if !pathWithin(root, absolute) {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantPackageLockCode, "assistant package path escapes the workspace", assistant, field))
				continue
			}
			if source != "" && mcpRelativePath(source) {
				sourceRoot := filepath.Clean(filepath.Join(root, filepath.FromSlash(source)))
				if !pathWithin(sourceRoot, absolute) {
					diagnostics = append(diagnostics, mcpDiagnostic(assistantSourcePathCode, "assistant package path escapes the assistant source root", assistant, field))
					continue
				}
			}
			info, err := os.Lstat(absolute)
			if err != nil || (regular && !info.Mode().IsRegular()) || (!regular && !info.IsDir()) {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantPackageLockCode, "assistant package file is unavailable", assistant, field))
				continue
			}
			if err := rejectPathSymlinks(root, absolute); err != nil {
				diagnostics = append(diagnostics, mcpDiagnostic(assistantPackageLockCode, "assistant package file must not traverse symlinks", assistant, field))
			}
			if filepath.Base(filepath.FromSlash(path)) == "package.json" {
				if content, err := os.ReadFile(absolute); err != nil || !json.Valid(content) {
					diagnostics = append(diagnostics, mcpDiagnostic(assistantPackageLockCode, "assistant package.json must contain valid JSON", assistant, field))
				}
			}
		}
		surface, _ := assistant.Spec["surface"].(map[string]any)
		path := strings.TrimSpace(stringValue(surface["path"]))
		if path == "" {
			continue
		}
		gatewayAddress := resolveResourceRef(assistant, refString(surface["gateway"]), "http_gateway")
		gateway := resourcesByAddress(&Manifest{Resources: resources})[gatewayAddress]
		base := stringValue(gateway.Spec["base_path"])
		route := canonicalRoute(joinHTTPPath(base, path))
		if previous, exists := routes[route]; exists {
			diagnostics = append(diagnostics, Diagnostic{Code: assistantRouteCollision, Severity: "error", Message: "assistant surface route collides with " + previous.Address, Address: assistant.Address, Path: "/spec/surface/path", Related: []Related{{Address: previous.Address}}})
		} else {
			routes[route] = assistant
		}
	}
	// An assistant surface cannot claim an already-owned HTTP route. Exact
	// canonical matching is intentional: generated assistant subroutes remain
	// private to the assistant mount and do not shadow unrelated application
	// paths.
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for route, assistant := range routes {
		for _, binding := range resources {
			if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "http" {
				continue
			}
			gateway := byAddress[resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway")]
			httpSpec, _ := binding.Spec["http"].(map[string]any)
			candidate := canonicalRoute(joinHTTPPath(stringValue(gateway.Spec["base_path"]), stringValue(httpSpec["path"])))
			if route == candidate {
				diagnostics = append(diagnostics, Diagnostic{Code: assistantRouteCollision, Severity: "error", Message: "assistant surface route collides with HTTP binding " + binding.Address, Address: assistant.Address, Related: []Related{{Address: binding.Address}}})
			}
		}
	}
	return sortMCPDiagnostics(diagnostics)
}

func mcpOutputHasSensitiveFields(operation Resource, module string, resources map[string]Resource, visiting map[string]bool) bool {
	for _, kind := range []string{"result", "error"} {
		for _, outcome := range namedChildren(operation.Spec, kind) {
			if mcpTypeHasSensitive(outcome["type"], module, resources, visiting) {
				return true
			}
		}
	}
	return false
}

func mcpTypeHasSensitive(value any, module string, resources map[string]Resource, visiting map[string]bool) bool {
	expression := strings.TrimSpace(typeExpression(value))
	for {
		matched := false
		for _, wrapper := range []string{"optional", "nullable", "list", "set", "map"} {
			prefix := wrapper + "("
			if strings.HasPrefix(expression, prefix) && strings.HasSuffix(expression, ")") {
				expression = strings.TrimSpace(expression[len(prefix) : len(expression)-1])
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}
	if strings.HasPrefix(expression, "tuple(") && strings.HasSuffix(expression, ")") {
		for _, argument := range splitTypeArguments(expression[len("tuple(") : len(expression)-1]) {
			if mcpTypeHasSensitive(map[string]any{"$ref": argument}, module, resources, visiting) {
				return true
			}
		}
		return false
	}
	record, ok := mcpRecordForExpression(expression, module, resources)
	if !ok {
		if union, ok := mcpUnionForExpression(expression, module, resources); ok {
			if visiting[union.Address] {
				return false
			}
			visiting[union.Address] = true
			defer delete(visiting, union.Address)
			for _, variant := range namedChildren(union.Spec, "variant") {
				if mcpTypeHasSensitive(variant["type"], union.Module, resources, visiting) {
					return true
				}
			}
		}
		return false
	}
	if visiting[record.Address] {
		return false
	}
	visiting[record.Address] = true
	defer delete(visiting, record.Address)
	for _, field := range namedChildren(record.Spec, "field") {
		if field["sensitive"] == true || mcpTypeHasSensitive(field["type"], record.Module, resources, visiting) {
			return true
		}
	}
	return false
}

func mcpInputTypeSupported(value any, module string, resources map[string]Resource, visiting map[string]bool) bool {
	expression := strings.TrimSpace(typeExpression(value))
	for _, wrapper := range []string{"optional", "nullable"} {
		prefix := wrapper + "("
		if strings.HasPrefix(expression, prefix) && strings.HasSuffix(expression, ")") {
			expression = strings.TrimSpace(expression[len(prefix) : len(expression)-1])
		}
	}
	if expression == "std.type.unit" {
		return true
	}
	record, ok := mcpRecordForExpression(expression, module, resources)
	return ok && mcpRecordJSONSupported(record, resources, visiting)
}

func mcpRecordJSONSupported(record Resource, resources map[string]Resource, visiting map[string]bool) bool {
	if visiting[record.Address] {
		return true
	}
	visiting[record.Address] = true
	defer delete(visiting, record.Address)
	for _, field := range namedChildren(record.Spec, "field") {
		if !mcpTypeSupported(field["type"], record.Module, resources, visiting) {
			return false
		}
	}
	return true
}

func mcpTypeSupported(value any, module string, resources map[string]Resource, visiting map[string]bool) bool {
	expression := strings.TrimSpace(typeExpression(value))
	for _, wrapper := range []string{"optional", "nullable"} {
		prefix := wrapper + "("
		if strings.HasPrefix(expression, prefix) && strings.HasSuffix(expression, ")") {
			return mcpTypeSupported(map[string]any{"$ref": strings.TrimSpace(expression[len(prefix) : len(expression)-1])}, module, resources, visiting)
		}
	}
	if mcpPrimitiveJSONType(expression) {
		return true
	}
	if strings.HasPrefix(expression, "list(") || strings.HasPrefix(expression, "set(") || strings.HasPrefix(expression, "map(") {
		open := strings.IndexByte(expression, '(')
		if open < 0 || !strings.HasSuffix(expression, ")") {
			return false
		}
		return mcpTypeSupported(map[string]any{"$ref": strings.TrimSpace(expression[open+1 : len(expression)-1])}, module, resources, visiting)
	}
	if strings.HasPrefix(expression, "tuple(") && strings.HasSuffix(expression, ")") {
		for _, argument := range splitTypeArguments(expression[len("tuple(") : len(expression)-1]) {
			if !mcpTypeSupported(map[string]any{"$ref": argument}, module, resources, visiting) {
				return false
			}
		}
		return true
	}
	if record, ok := mcpRecordForExpression(expression, module, resources); ok {
		return mcpRecordJSONSupported(record, resources, visiting)
	}
	if union, ok := mcpUnionForExpression(expression, module, resources); ok {
		if visiting[union.Address] {
			return true
		}
		visiting[union.Address] = true
		defer delete(visiting, union.Address)
		for _, variant := range namedChildren(union.Spec, "variant") {
			if !mcpTypeSupported(variant["type"], union.Module, resources, visiting) {
				return false
			}
		}
		return true
	}
	return false
}

func mcpPrimitiveJSONType(expression string) bool {
	switch expression {
	case "bool", "int", "int32", "int64", "uint32", "uint64", "decimal", "float32", "float64", "string", "bytes", "uuid", "date", "datetime", "duration", "size", "url", "relative_path", "json", "std.type.unit", "std.type.problem", "std.type.execution_receipt":
		return true
	default:
		return false
	}
}

func mcpRecordForExpression(expression, module string, resources map[string]Resource) (Resource, bool) {
	if strings.Contains(expression, "/record/") {
		record, ok := resources[expression]
		return record, ok && record.Kind == "scenery.record"
	}
	parts := strings.Split(expression, ".")
	if len(parts) != 2 || parts[0] != "record" {
		return Resource{}, false
	}
	record, ok := resources[resourceAddress(module, "record", parts[1])]
	return record, ok && record.Kind == "scenery.record"
}

func mcpUnionForExpression(expression, module string, resources map[string]Resource) (Resource, bool) {
	if strings.Contains(expression, "/union/") {
		union, ok := resources[expression]
		return union, ok && union.Kind == "scenery.union"
	}
	parts := strings.Split(expression, ".")
	if len(parts) != 2 || parts[0] != "union" {
		return Resource{}, false
	}
	union, ok := resources[resourceAddress(module, "union", parts[1])]
	return union, ok && union.Kind == "scenery.union"
}

func validateMCPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("MCP connection URL must be an absolute URL without credentials or fragments")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return fmt.Errorf("MCP connection URL must use HTTPS except for loopback development URLs")
}

func mcpPositiveDuration(value string) bool {
	duration, err := scenery.ParseDuration(value)
	return err == nil && duration.Sign() > 0
}

func mcpPositiveBoundedInteger(value any, maximum int) bool {
	integer, ok := integerValue(value)
	return ok && integer > 0 && integer <= maximum
}

func mcpSecretReference(resource Resource, reference string, byAddress map[string]Resource) bool {
	if reference == "" {
		return false
	}
	address := resolveResourceRef(resource, reference, "secret")
	secret, ok := byAddress[address]
	return ok && secret.Kind == "scenery.secret"
}

func validMCPHeaderName(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", char) {
			continue
		}
		return false
	}
	return true
}

func validMCPToolName(value string) bool {
	if value == "" || len(value) > mcpMaxToolNameBytes {
		return false
	}
	return validSemanticName(value)
}

func validAssistantPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "*:") && validHTTPPath(value)
}

func mcpRelativePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func mcpStringList(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func mcpToolsField(resource Resource, field string) any {
	tools, _ := resource.Spec["tools"].(map[string]any)
	return tools[field]
}

func mcpReferenceValues(value any) []string {
	var references []string
	walkRefs(value, "", func(_ string, reference string) {
		if strings.Contains(reference, "/mcp_") || strings.HasPrefix(reference, "mcp_server.") || strings.HasPrefix(reference, "mcp_connection.") || strings.HasPrefix(reference, "assistant.") {
			references = append(references, reference)
		}
	})
	return canonicalStrings(references)
}

func mcpDiagnostic(code, message string, resource Resource, path string) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address, Path: path}
}

func sortMCPDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}
		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}
