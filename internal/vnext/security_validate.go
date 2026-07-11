package vnext

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	sceneryruntime "scenery.sh/runtime"
)

func validateSecurityResources(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.authorization/v1":
			diagnostics = append(diagnostics, validateAuthorizationResource(resource)...)
		case "scenery.pipeline/v1":
			diagnostics = append(diagnostics, validatePipelineResource(resource)...)
		case "scenery.middleware/v1":
			if resource.Origin.Kind != "legacy_v0" {
				diagnostics = append(diagnostics, securityDiagnostic("SCN4203", "unsupported_profile: custom middleware requires a declared middleware ABI profile", resource, "/spec"))
			}
		}
	}
	for _, resource := range resources {
		if resource.Kind == "scenery.binding/v1" && resource.Origin.Kind != "legacy_v0" {
			diagnostics = append(diagnostics, validateBindingAuthentication(byAddress, resource)...)
			diagnostics = append(diagnostics, validateBindingAuthorizationTypes(byAddress, resource)...)
		}
	}
	return diagnostics
}

func validateBindingAuthorizationTypes(resources map[string]Resource, binding Resource) []Diagnostic {
	reference := refOrString(binding.Spec["authorization"])
	if strings.HasPrefix(reference, "std.authorization.") {
		return nil
	}
	address := resolveResourceRef(binding, refString(binding.Spec["authorization"]), "authorization")
	authorization, ok := resources[address]
	if !ok || authorization.Kind != "scenery.authorization/v1" {
		return nil
	}
	operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
	operation, ok := resources[operationAddress]
	if !ok || operation.Kind != "scenery.operation/v1" {
		return nil
	}
	variables := map[string]any{
		"principal": map[string]any{
			"id": "principal", "uid": "principal", "authenticated": true, "tenant_id": "tenant",
			"membership": "member", "roles": []any{"member"},
		},
		"context": map[string]any{
			"invocation_id": "00000000-0000-0000-0000-000000000000", "tenant_id": "tenant", "trace_id": "trace",
			"deadline": "2026-01-01T00:00:00Z", "caller_binding": binding.Address, "execution_id": "execution",
			"deployment": "app/deployment/default", "locale": "en",
		},
		"input": authorizationTypeSample(operation.Spec["input"], operation.Module, resources, map[string]bool{}),
	}
	var diagnostics []Diagnostic
	for _, rule := range orderedChildren(authorization.Spec, "rule") {
		value := rule["allow"]
		field := "allow"
		if denied, exists := rule["deny"]; exists {
			value, field = denied, "deny"
		}
		expression, _ := value.(map[string]any)
		source := stringValue(expression["$expression"])
		if source == "" {
			continue
		}
		if err := sceneryruntime.ValidateContractAuthorizationExpressionAgainst(source, variables); err != nil {
			name := stringValue(rule["name"])
			diagnostics = append(diagnostics, securityDiagnostic("SCN4107", "authorization rule "+name+" is not type-correct for binding "+binding.Address+": "+err.Error(), authorization, "/spec/rule/"+name+"/"+field))
		}
	}
	return diagnostics
}

func authorizationTypeSample(value any, module string, resources map[string]Resource, seen map[string]bool) any {
	reference := refOrString(value)
	if reference != "" {
		switch reference {
		case "bool":
			return true
		case "int", "int32", "int64", "uint32", "uint64", "float32", "float64", "decimal", "size":
			return json.Number("1")
		case "json":
			return map[string]any{}
		case "bytes":
			return "AA=="
		case "string", "uuid", "date", "datetime", "duration", "url", "relative_path":
			return "value"
		case "std.type.problem":
			return map[string]any{"code": "problem", "message": "problem", "path": "path"}
		}
		address := reference
		if !strings.Contains(reference, "/") {
			parts := strings.Split(reference, ".")
			if len(parts) == 2 {
				address = resourceAddress(module, parts[0], parts[1])
			}
		}
		resource, ok := resources[address]
		if !ok || seen[address] {
			return map[string]any{}
		}
		seen[address] = true
		defer delete(seen, address)
		switch resource.Kind {
		case "scenery.record/v1":
			result := map[string]any{}
			for _, field := range namedChildren(resource.Spec, "field") {
				result[stringValue(field["name"])] = authorizationTypeSample(field["type"], resource.Module, resources, seen)
			}
			return result
		case "scenery.enum/v1":
			values := namedChildren(resource.Spec, "value")
			if len(values) > 0 {
				return defaultString(stringValue(values[0]["wire_value"]), stringValue(values[0]["name"]))
			}
		}
	}
	expression, _ := value.(map[string]any)
	raw := strings.TrimSpace(stringValue(expression["$expression"]))
	open := strings.IndexByte(raw, '(')
	if open > 0 && strings.HasSuffix(raw, ")") {
		name := raw[:open]
		arguments := splitTypeArguments(raw[open+1 : len(raw)-1])
		if len(arguments) > 0 {
			inner := authorizationTypeSample(map[string]any{"$ref": arguments[0]}, module, resources, seen)
			switch name {
			case "optional", "nullable":
				return inner
			case "list", "set":
				return []any{inner}
			case "map":
				return map[string]any{"key": inner}
			case "tuple":
				result := make([]any, len(arguments))
				for index, argument := range arguments {
					result[index] = authorizationTypeSample(map[string]any{"$ref": argument}, module, resources, seen)
				}
				return result
			}
		}
	}
	return map[string]any{}
}

func validateBindingAuthentication(resources map[string]Resource, binding Resource) []Diagnostic {
	reference := refOrString(binding.Spec["authentication"])
	protocol := stringValue(binding.Spec["protocol"])
	standard := map[string]map[string]bool{
		"http":     {"std.authentication.none": true},
		"internal": {"std.authentication.inherit": true},
		"event":    {"std.authentication.service_identity": true},
		"cli":      {"std.authentication.local_developer": true},
	}
	if strings.HasPrefix(reference, "std.authentication.") {
		if standard[protocol][reference] {
			return nil
		}
		return []Diagnostic{securityDiagnostic("SCN4106", "authentication profile "+reference+" is not supported for "+protocol+" bindings", binding, "/spec/authentication")}
	}
	address := resolveResourceRef(binding, refString(binding.Spec["authentication"]), "authentication")
	authentication, ok := resources[address]
	if !ok || authentication.Kind != "scenery.authentication/v1" {
		return []Diagnostic{securityDiagnostic("SCN4106", "authentication must resolve to a typed authentication resource", binding, "/spec/authentication")}
	}
	provider, scheme := refOrString(authentication.Spec["provider"]), stringValue(authentication.Spec["scheme"])
	if protocol != "http" || provider != "std.provider.standard_auth" || scheme != "session" {
		return []Diagnostic{securityDiagnostic("SCN4106", "capability_unavailable: runtime-http/v1 supports only std.provider.standard_auth session authentication", binding, "/spec/authentication")}
	}
	return nil
}

func validateAuthorizationResource(resource Resource) []Diagnostic {
	strategy := stringValue(resource.Spec["strategy"])
	knownStrategies := map[string]bool{"deny_unless_allowed": true, "any_allow": true, "allow_if_any": true, "all_must_allow": true, "allow_if_all": true, "first_applicable": true}
	var diagnostics []Diagnostic
	if !knownStrategies[strategy] {
		diagnostics = append(diagnostics, securityDiagnostic("SCN4101", "authorization strategy is unsupported or missing", resource, "/spec/strategy"))
	}
	for _, rule := range orderedChildren(resource.Spec, "rule") {
		name := stringValue(rule["name"])
		allow, hasAllow := rule["allow"]
		deny, hasDeny := rule["deny"]
		if hasAllow == hasDeny {
			diagnostics = append(diagnostics, securityDiagnostic("SCN4102", "authorization rule "+name+" requires exactly one of allow or deny", resource, "/spec/rule/"+name))
			continue
		}
		value, field := allow, "allow"
		if hasDeny {
			value, field = deny, "deny"
		}
		if _, ok := value.(bool); ok {
			continue
		}
		expressionValue, ok := value.(map[string]any)
		source := stringValue(expressionValue["$expression"])
		if !ok || strings.TrimSpace(source) == "" {
			diagnostics = append(diagnostics, securityDiagnostic("SCN4102", "authorization rule "+name+" requires a boolean expression", resource, "/spec/rule/"+name+"/"+field))
			continue
		}
		expression, parseDiagnostics := hclsyntax.ParseExpression([]byte(source), "authorization.scn", hcl.Pos{Line: 1, Column: 1})
		if parseDiagnostics.HasErrors() {
			diagnostics = append(diagnostics, securityDiagnostic("SCN4102", "authorization rule "+name+" has invalid syntax", resource, "/spec/rule/"+name+"/allow"))
			continue
		}
		for _, traversal := range expression.Variables() {
			root := traversal.RootName()
			if root != "principal" && root != "context" && root != "input" {
				diagnostics = append(diagnostics, securityDiagnostic("SCN4103", fmt.Sprintf("authorization rule %s uses forbidden root %s", name, root), resource, "/spec/rule/"+name+"/allow"))
			}
		}
		_ = hclsyntax.VisitAll(expression, func(node hclsyntax.Node) hcl.Diagnostics {
			if call, ok := node.(*hclsyntax.FunctionCallExpr); ok && call.Name != "contains" {
				diagnostics = append(diagnostics, securityDiagnostic("SCN4104", fmt.Sprintf("authorization rule %s uses forbidden function %s", name, call.Name), resource, "/spec/rule/"+name+"/allow"))
			}
			return nil
		})
		if err := sceneryruntime.ValidateContractAuthorizationExpression(source); err != nil {
			diagnostics = append(diagnostics, securityDiagnostic("SCN4105", "authorization rule "+name+" is outside the runtime expression subset: "+err.Error(), resource, "/spec/rule/"+name+"/allow"))
		}
	}
	return diagnostics
}

func applySecurityEffectiveDefaults(resources []Resource) {
	for index := range resources {
		if resources[index].Kind == "scenery.authorization/v1" && strings.TrimSpace(stringValue(resources[index].Spec["strategy"])) == "" {
			resources[index].Spec["strategy"] = "deny_unless_allowed"
		}
	}
}

func validatePipelineResource(resource Resource) []Diagnostic {
	standard := map[string]bool{
		"std.middleware.request_id": true,
		"std.middleware.trace":      true,
		"std.middleware.recover":    true,
	}
	seen := map[string]bool{}
	var diagnostics []Diagnostic
	for index, step := range orderedChildren(resource.Spec, "step") {
		use := refOrString(step["use"])
		path := fmt.Sprintf("/spec/step/%d", index)
		if !standard[use] {
			diagnostics = append(diagnostics, securityDiagnostic("SCN4201", "runtime-http/v1 pipeline step requires a supported standard middleware", resource, path))
		}
		if seen[use] {
			diagnostics = append(diagnostics, securityDiagnostic("SCN4202", "exclusive standard middleware is duplicated", resource, path))
		}
		seen[use] = true
	}
	return diagnostics
}

func securityDiagnostic(code, message string, resource Resource, path string) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address, Path: path}
}
