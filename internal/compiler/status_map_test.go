package compiler

import "testing"

func TestStatusMapRequiresUniqueSupportedStatuses(t *testing.T) {
	valid := Resource{Address: "house/status_map/state", Kind: "scenery.status-map", Spec: map[string]any{
		"status": []any{
			map[string]any{"name": "open", "label": "Open", "variant": "neutral"},
			map[string]any{"name": "done", "label": "Done", "variant": "green"},
		},
	}}
	if diagnostics := validateStatusMap(valid); hasErrors(diagnostics) {
		t.Fatalf("valid status map diagnostics = %#v", diagnostics)
	}
	invalid := valid
	invalid.Spec = cloneMapValue(valid.Spec)
	invalid.Spec["status"] = []any{
		map[string]any{"name": "open", "label": "", "variant": "unknown"},
		map[string]any{"name": "open", "label": "Again", "variant": "green"},
	}
	if diagnostics := validateStatusMap(invalid); !hasDiagnostic(diagnostics, "SCN2620") {
		t.Fatalf("invalid status map diagnostics = %#v", diagnostics)
	}
}
