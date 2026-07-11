package vnext

import (
	"strings"
	"testing"
)

func TestSecurityValidationRejectsImpureRulesAndPipelineAmbiguity(t *testing.T) {
	resources := []Resource{
		{Address: "app/authorization/member", Kind: "scenery.authorization/v1", Spec: map[string]any{
			"strategy": "deny_unless_allowed", "principal": map[string]any{"$ref": "std.type.authenticated_principal"},
			"rule": map[string]any{"name": "bad", "allow": map[string]any{"$expression": `evil.allowed && file("secret") != ""`}},
		}},
		{Address: "app/pipeline/default", Kind: "scenery.pipeline/v1", Spec: map[string]any{"step": []any{
			map[string]any{"name": "one", "use": map[string]any{"$ref": "std.middleware.recover"}},
			map[string]any{"name": "two", "use": map[string]any{"$ref": "std.middleware.recover"}},
			map[string]any{"name": "custom", "use": map[string]any{"$ref": "middleware.custom"}},
		}}},
	}
	diagnostics := validateSecurityResources(resources)
	for _, code := range []string{"SCN4103", "SCN4104", "SCN4201", "SCN4202"} {
		if !diagnosticsContain(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestAuthorizationDefaultsAndAcceptsDenyRules(t *testing.T) {
	resources := []Resource{{Address: "app/authorization/member", Kind: "scenery.authorization/v1", Spec: map[string]any{
		"principal": map[string]any{"$ref": "std.type.authenticated_principal"},
		"rule": []any{
			map[string]any{"name": "allow_member", "allow": true},
			map[string]any{"name": "deny_blocked", "deny": map[string]any{"$expression": `principal.uid == "blocked"`}},
		},
	}}}
	applySecurityEffectiveDefaults(resources)
	if resources[0].Spec["strategy"] != "deny_unless_allowed" {
		t.Fatalf("strategy = %#v", resources[0].Spec["strategy"])
	}
	if diagnostics := validateSecurityResources(resources); hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	rules := renderContractAuthorizationRules(resources[0])
	if len(rules) != 2 || !strings.Contains(rules[1], `Effect: "deny"`) {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestBindingAuthenticationRejectsUnsupportedProviderAndScheme(t *testing.T) {
	authentication := Resource{Address: "app/authentication/standard", Module: "app", Kind: "scenery.authentication/v1", Name: "standard", Spec: map[string]any{
		"provider": map[string]any{"$ref": "std.provider.other"}, "scheme": "magic",
	}}
	binding := Resource{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
		"protocol": "http", "authentication": map[string]any{"$ref": "app/authentication/standard"},
	}}
	if diagnostics := validateSecurityResources([]Resource{authentication, binding}); !diagnosticsContain(diagnostics, "SCN4106") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	authentication.Spec["provider"] = map[string]any{"$ref": "std.provider.standard_auth"}
	authentication.Spec["scheme"] = "session"
	if diagnostics := validateSecurityResources([]Resource{authentication, binding}); hasErrors(diagnostics) {
		t.Fatalf("supported authentication diagnostics = %#v", diagnostics)
	}
}

func TestCustomMiddlewareFailsAsUnsupportedProfile(t *testing.T) {
	resource := Resource{Address: "house/middleware/custom", Module: "house", Kind: "scenery.middleware/v1", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"protocols": []any{"http"}, "phases": []any{"after_authentication"},
	}}
	if diagnostics := validateSecurityResources([]Resource{resource}); !diagnosticsContain(diagnostics, "SCN4203") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestAuthorizationRulesAreTypeCheckedAgainstBindingInput(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{"field": map[string]any{"name": "count", "type": map[string]any{"$ref": "int32"}}}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}}},
		{Address: "app/authorization/member", Module: "app", Kind: "scenery.authorization/v1", Name: "member", Spec: map[string]any{
			"principal": map[string]any{"$ref": "std.type.authenticated_principal"}, "strategy": "deny_unless_allowed",
			"rule": []any{
				map[string]any{"name": "missing", "allow": map[string]any{"$expression": `input.missing == 1`}},
				map[string]any{"name": "wrong_type", "allow": map[string]any{"$expression": `input.count == "one"`}},
			},
		}},
		{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process"}, "protocol": "http",
			"authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "app/authorization/member"},
		}},
	}
	if diagnostics := validateSecurityResources(resources); !diagnosticsContain(diagnostics, "SCN4107") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}
