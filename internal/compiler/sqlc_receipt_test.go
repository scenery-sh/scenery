package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLCClassificationRequiresCompleteReceipt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(SQLCReceiptPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	valid := func() map[string]any {
		return map[string]any{"kind": "scenery.sqlc-generated", "schema_revision": SQLCReceiptSchemaRevision(), "owner": root, "input_revision": digest, "tools": map[string]string{"sqlc": digest}, "roots": []string{"gen"}, "files": map[string]string{"gen/authored.go": digest}}
	}
	write := func(receipt map[string]any) {
		t.Helper()
		data, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(valid())
	paths, err := GeneratedPaths(root)
	if err != nil || !paths["gen/authored.go"] {
		t.Fatalf("valid receipt not classified: %v %v", paths, err)
	}
	for _, test := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"schema", func(r map[string]any) { delete(r, "schema_revision") }},
		{"foreign", func(r map[string]any) { r["owner"] = root + "-other" }},
		{"roots", func(r map[string]any) { r["roots"] = []string{"other"} }},
		{"unsafe root", func(r map[string]any) { r["roots"] = []string{"../gen"} }},
		{"file digest", func(r map[string]any) { r["files"] = map[string]string{"gen/authored.go": strings.ToUpper(digest)} }},
		{"input digest", func(r map[string]any) { r["input_revision"] = "missing" }},
		{"tool digest", func(r map[string]any) { r["tools"] = map[string]string{"sqlc": "missing"} }},
		{"missing tools", func(r map[string]any) { delete(r, "tools") }},
		{"unknown field", func(r map[string]any) { r["unknown"] = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			receipt := valid()
			test.edit(receipt)
			write(receipt)
			paths, err := GeneratedPaths(root)
			if err == nil || paths["gen/authored.go"] {
				t.Fatalf("invalid receipt granted classification: %v %v", paths, err)
			}
		})
	}
}
