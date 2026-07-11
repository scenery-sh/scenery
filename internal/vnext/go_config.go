package vnext

import (
	"fmt"
	"sort"
	"strings"
)

func enrichPackageGoServiceSchemas(resources []Resource, sources []*Source) ([]Resource, []Diagnostic) {
	declarations := packageInputDeclarations(sources)
	result := append([]Resource(nil), resources...)
	var diagnostics []Diagnostic
	for index := range result {
		service := &result[index]
		if service.Kind != "scenery.service/v1" || stringValue(service.Spec["runtime"]) != "go" {
			continue
		}
		config, _ := service.Spec["config"].(map[string]any)
		if len(config) == 0 {
			continue
		}
		names := make([]string, 0, len(config))
		for name := range config {
			names = append(names, name)
		}
		sort.Strings(names)
		schema := make([]any, 0, len(names))
		for _, name := range names {
			if !sceneryIdentifierPattern.MatchString(name) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN3405", Severity: "error", Message: "Go service config keys must use lower_snake_case", Address: service.Address, Path: "/spec/config/" + name})
			}
			reference := refString(config[name])
			if !strings.HasPrefix(reference, "var.") {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN3401", Severity: "error", Message: "Go service config " + name + " must reference a typed package input", Address: service.Address, Path: "/spec/config/" + name})
				continue
			}
			inputName := strings.TrimPrefix(reference, "var.")
			declaration, ok := declarations[inputName]
			if !ok || declaration.Type == "" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN3402", Severity: "error", Message: "Go service config " + name + " references an unavailable typed input", Address: service.Address, Path: "/spec/config/" + name})
				continue
			}
			phase := declaration.Phase
			if phase == "" {
				phase = "contract"
			}
			if phase != "contract" && phase != "implementation" && phase != "deployment" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN3406", Severity: "error", Message: "Go service config " + name + " uses an invalid package input phase", Address: service.Address, Path: "/spec/config/" + name})
			}
			field := map[string]any{"name": name, "type": declaration.Type, "phase": phase, "sensitive": declaration.Sensitive}
			for constraint, value := range declaration.Constraints {
				field[constraint] = cloneSemanticValue(value)
			}
			schema = append(schema, field)
		}
		service.Spec["config_schema"] = schema
	}
	return result, diagnostics
}

func validateGoServiceConfiguration(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	for _, service := range resources {
		if service.Kind != "scenery.service/v1" || service.Origin.Kind == "legacy_v0" {
			continue
		}
		config, _ := service.Spec["config"].(map[string]any)
		schema := namedChildren(service.Spec, "config_schema")
		declared := map[string]bool{}
		for _, field := range schema {
			name, typeExpression := stringValue(field["name"]), stringValue(field["type"])
			declared[name] = true
			value, exists := config[name]
			if !exists {
				diagnostics = append(diagnostics, goConfigDiagnostic("SCN3403", "Go service config has no resolved value", service, name))
				continue
			}
			reference := refString(value)
			if typeExpression == `resource_ref("secret")` {
				address := resolveResourceRef(service, reference, "secret")
				secret, ok := byAddress[address]
				if reference == "" || !ok || secret.Kind != "scenery.secret/v1" || field["sensitive"] != true {
					diagnostics = append(diagnostics, goConfigDiagnostic("SCN4001", "secret configuration requires a sensitive typed secret reference", service, name))
				}
				continue
			}
			if reference != "" {
				diagnostics = append(diagnostics, goConfigDiagnostic("SCN4002", "resource or secret reference cannot flow into non-secret configuration", service, name))
				continue
			}
			if field["sensitive"] == true {
				diagnostics = append(diagnostics, goConfigDiagnostic("SCN4003", "sensitive Go configuration must use resource_ref(\"secret\")", service, name))
			}
			if err := validateFixtureFieldValue(value, field, service.Module, byAddress); err != nil {
				diagnostics = append(diagnostics, goConfigDiagnostic("SCN3407", "Go service config value does not match its package input type: "+err.Error(), service, name))
			}
		}
		for name := range config {
			if !declared[name] {
				diagnostics = append(diagnostics, goConfigDiagnostic("SCN3404", "Go service config has no stable package input type", service, name))
			}
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		return diagnostics[i].Path < diagnostics[j].Path
	})
	return diagnostics
}

func goConfigDiagnostic(code, message string, service Resource, name string) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: fmt.Sprintf("%s %q", message, name), Address: service.Address, Path: "/spec/config/" + name}
}
