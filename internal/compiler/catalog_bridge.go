package compiler

import "scenery.sh/internal/spec"

type DiagnosticDefinition = spec.DiagnosticDefinition

func MarshalCanonical(value any) ([]byte, error) {
	return spec.MarshalCanonical(value)
}

func DiagnosticDefinitions() []DiagnosticDefinition {
	return spec.DiagnosticDefinitions()
}

func DiagnosticDefinitionFor(code string) (DiagnosticDefinition, bool) {
	return spec.DiagnosticDefinitionFor(code)
}

func parseDiagnosticDefinitions(rows string) []DiagnosticDefinition {
	return spec.ParseDiagnosticDefinitions(rows)
}

func diagnosticCategory(code string) string {
	return spec.DiagnosticCategory(code)
}
