package compiler

import "scenery.sh/internal/spec"

type DiagnosticDefinition = spec.DiagnosticDefinition

func MarshalCanonical(value any) ([]byte, error) {
	return spec.MarshalCanonical(value)
}

func DiagnosticDefinitionFor(code string) (DiagnosticDefinition, bool) {
	return spec.DiagnosticDefinitionFor(code)
}
