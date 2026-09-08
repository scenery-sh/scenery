package harnessreport

import (
	"scenery.sh/internal/machine"
	"strings"
)

func newCLIPayloadIdentity(kind string) PayloadIdentity { return machine.NewPayloadIdentity(kind) }

func TailString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func CountDiagnostics(diagnostics []Diagnostic) (errors int, warnings int) {
	for _, diag := range diagnostics {
		switch diag.Severity {
		case "error":
			errors++
		case "warning":
			warnings++
		}
	}
	return errors, warnings
}

func ArtifactName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "artifact"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ':' || r == '/' || r == ' ':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "artifact"
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
