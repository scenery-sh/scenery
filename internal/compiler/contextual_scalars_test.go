package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextualPrimitiveLiteralsLowerToExactScalars(t *testing.T) {
	quoted := contextualScalarFixture(t, `
    default = "1h30m"
`, `
    default = "018f47a2-6f45-7c4a-8b31-4cbbe3c99a22"
`, `
    default = "1.5KiB"
`)
	explicit := contextualScalarFixture(t, `
    default = duration("1h30m")
`, `
    default = uuid("018f47a2-6f45-7c4a-8b31-4cbbe3c99a22")
`, `
    default = size("1.5KiB")
`)
	quotedResult, err := Compile(quoted)
	if err != nil || !quotedResult.Valid() {
		t.Fatalf("quoted compile: %v diagnostics=%#v", err, quotedResult.Diagnostics)
	}
	explicitResult, err := Compile(explicit)
	if err != nil || !explicitResult.Valid() {
		t.Fatalf("explicit compile: %v diagnostics=%#v", err, explicitResult.Diagnostics)
	}
	if quotedResult.Manifest.ContractRevision != explicitResult.Manifest.ContractRevision {
		t.Fatalf("equivalent literal forms changed contract revision: %s != %s", quotedResult.Manifest.ContractRevision, explicitResult.Manifest.ContractRevision)
	}
	record := resourcesByAddress(quotedResult.Manifest)["types/record/settings"]
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	if got := fields["timeout"]["default"].(map[string]any); got["$scalar"] != "duration" || got["nanoseconds"] != "5400000000000" {
		t.Fatalf("duration = %#v", got)
	}
	if got := fields["id"]["default"].(map[string]any); got["$scalar"] != "uuid" || got["value"] != "018f47a2-6f45-7c4a-8b31-4cbbe3c99a22" {
		t.Fatalf("uuid = %#v", got)
	}
	if got := fields["capacity"]["default"].(map[string]any); got["$scalar"] != "size" || got["bytes"] != "1536" {
		t.Fatalf("size = %#v", got)
	}
	if got := fields["model"]["default"].(map[string]any); got["$scalar"] != "relative_path" || got["value"] != "models/Café" {
		t.Fatalf("relative path = %#v", got)
	}
}

func TestInvalidContextualPrimitiveLiteralIsDiagnostic(t *testing.T) {
	root := contextualScalarFixture(t, `
    default = "not-a-duration"
`, "", "")
	result, err := Compile(root)
	if err != nil || !hasDiagnostic(result.Diagnostics, "SCN1212") {
		t.Fatalf("err=%v diagnostics=%#v", err, result.Diagnostics)
	}
}

func contextualScalarFixture(t *testing.T, durationDefault, uuidDefault, sizeDefault string) string {
	t.Helper()
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "types")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, appFilename), []byte(`application "scalar_app" {}
module "types" { source = "./types" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	packageSource := `package "types" {
}
record "settings" {
  field "timeout" {
    type = duration` + durationDefault + `  }
  field "id" {
    type = uuid` + uuidDefault + `  }
  field "capacity" {
    type = size` + sizeDefault + `  }
  field "model" {
    type    = relative_path
    default = "models/Café"
  }
}
`
	if err := os.WriteFile(filepath.Join(moduleRoot, packageFilename), []byte(packageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
