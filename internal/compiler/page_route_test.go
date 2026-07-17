package compiler

import "testing"

func TestValidateGeneratedPageRoute(t *testing.T) {
	resources := map[string]Resource{
		"mail/enum/inbox_view": {
			Address: "mail/enum/inbox_view",
			Module:  "mail",
			Kind:    "scenery.enum",
			Name:    "inbox_view",
			Spec:    map[string]any{"value": []any{map[string]any{"name": "slots"}}},
		},
	}
	valid := Resource{
		Address: "mail/content_page/summary",
		Module:  "mail",
		Kind:    "scenery.content-page",
		Spec: map[string]any{
			"nav_group":        "UI",
			"nav_order":        int64(2),
			"nav_active_paths": []any{"/mailsnext/summary"},
			"search": []any{
				map[string]any{"name": "mail", "type": map[string]any{"$ref": "string"}},
				map[string]any{"name": "view", "type": map[string]any{"$ref": "enum.inbox_view"}},
			},
		},
	}
	if diagnostics := validateGeneratedPageRoute(resources, valid); len(diagnostics) != 0 {
		t.Fatalf("valid route diagnostics = %#v", diagnostics)
	}

	invalid := valid
	invalid.Spec = cloneMapValue(valid.Spec)
	invalid.Spec["nav_group"] = ""
	invalid.Spec["nav_order"] = -1
	invalid.Spec["nav_active_paths"] = []any{"relative"}
	invalid.Spec["search"] = []any{
		map[string]any{"name": "view", "type": map[string]any{"$ref": "enum.missing"}},
		map[string]any{"name": "view", "type": map[string]any{"$ref": "list(string)"}},
	}
	diagnostics := validateGeneratedPageRoute(resources, invalid)
	if len(diagnostics) != 6 {
		t.Fatalf("invalid route diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != "SCN2619" {
			t.Fatalf("diagnostic code = %q, want SCN2619", diagnostic.Code)
		}
	}
}
