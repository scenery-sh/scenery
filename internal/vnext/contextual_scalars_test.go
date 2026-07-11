package vnext

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
    default = "2GiB"
`)
	explicit := contextualScalarFixture(t, `
    default = duration("1h30m")
`, `
    default = uuid("018f47a2-6f45-7c4a-8b31-4cbbe3c99a22")
`, `
    default = size("2GiB")
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
	if got := fields["capacity"]["default"].(map[string]any); got["$scalar"] != "size" || got["bytes"] != "2147483648" {
		t.Fatalf("size = %#v", got)
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
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte(`language { edition = "2027" }
application "scalar_app" { version = "1.0.0" }
module "types" { source = "./types" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	packageSource := `package "types" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "settings" {
  field "timeout" {
    type = duration` + durationDefault + `  }
  field "id" {
    type = uuid` + uuidDefault + `  }
  field "capacity" {
    type = size` + sizeDefault + `  }
}
`
	if err := os.WriteFile(filepath.Join(moduleRoot, "scenery.package.scn"), []byte(packageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
