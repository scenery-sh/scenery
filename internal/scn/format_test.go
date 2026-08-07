package scn

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatterCanonicalizesCommentsAndContextualPrimitiveLiterals(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, AppFilename)
	before := `// settings
record "settings" { /* exact values */
  field "timeout" {
    type = duration
    default = duration("1h30m")
  }
  field "when" {
    type = datetime
    default = "2027-03-14T10:15:30.120+01:00"
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(root, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	for _, want := range []string{"# settings", "# exact values", `default = "1h30m"`, `default = "2027-03-14T09:15:30.12Z"`} {
		if !strings.Contains(text, want) {
			t.Errorf("formatted source missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "//") || strings.Contains(text, "/*") || strings.Contains(text, "duration(") {
		t.Fatalf("formatter retained non-canonical syntax:\n%s", text)
	}
	result, err := Format(root, true)
	if err != nil || len(result.Changed) != 0 {
		t.Fatalf("second format = %#v err=%v", result, err)
	}
}

func TestFormatterDiscoversNestedLocalPackageSources(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"parent", "child"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeNestedModuleFile(t, filepath.Join(root, AppFilename), `application "format_app" {}
module "parent" { source="./parent" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", PackageFilename), `package "parent" {
}
module "child" { source="../child" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "child", PackageFilename), `package "child" { }
`)
	result, err := Format(root, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range result.Changed {
		seen[path] = true
	}
	if !seen["parent/"+PackageFilename] || !seen["child/"+PackageFilename] {
		t.Fatalf("formatted paths = %#v", result.Changed)
	}
}

func TestFormatterRoundTripsAssistantMCPBlockShapes(t *testing.T) {
	before := []byte(`binding "process_scene_mcp" {
mcp {
name="process_scene"
}
}
mcp_connection "docs" {
auth {
scheme="bearer"
}
tools {
allow=["search","fetch"]
}
}
mcp_server "support" {
capability "process_scene" {
binding=module.house.process_scene_mcp
}
connection "docs" {
connection=mcp_connection.docs
}
}
assistant "support" {
implementation {
adapter="eve"
}
surface {
path="/assistants/support"
}
}
`)
	formatted, err := CanonicalFormat(before, AppFilename)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, formatted) {
		t.Fatalf("formatter did not canonicalize compact source: %q", formatted)
	}
	for _, want := range []string{
		`binding "process_scene_mcp" {`,
		`mcp_connection "docs" {`,
		`  auth {`,
		`  tools {`,
		`mcp_server "support" {`,
		`  capability "process_scene" {`,
		`  connection "docs" {`,
		`assistant "support" {`,
		`  implementation {`,
		`  surface {`,
	} {
		if !strings.Contains(string(formatted), want) {
			t.Errorf("formatted source missing %q:\n%s", want, formatted)
		}
	}
	second, err := CanonicalFormat(formatted, AppFilename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatted, second) {
		t.Fatalf("formatter is not idempotent:\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}

	root := t.TempDir()
	path := filepath.Join(root, AppFilename)
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, diagnostics := Parse(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("formatted source diagnostics = %#v", diagnostics)
	}
	if parsed == nil || parsed.CST == nil || !bytes.Equal(parsed.CST.Bytes(), formatted) {
		t.Fatalf("formatted source did not survive CST parse: %#v", parsed.CST)
	}
}

func TestFormatterUsesNestedSchemaPrimitiveMetadata(t *testing.T) {
	before := []byte("typescript_client \"public_api\" {\nreact {\ntsconfig = \"configs/Cafe\\u0301.json\"\n}\n}\n")
	formatted, err := CanonicalFormat(before, AppFilename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatted), "tsconfig = \"configs/Café.json\"") {
		t.Fatalf("nested relative path was not normalized from schema metadata:\n%s", formatted)
	}
	second, err := CanonicalFormat(formatted, AppFilename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatted, second) {
		t.Fatalf("formatter is not idempotent:\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}
}

func TestFormatterCanonicalizesNewAssistantMCPPrimitiveMetadata(t *testing.T) {
	before := []byte(`mcp_connection "docs" {
connect_timeout = duration("5s")
call_timeout = duration("30s")
}
assistant "support" {
implementation {
source = "assistants/Café"
package = "assistants/Café/package.json"
package_lock = "assistants/Café/package-lock.json"
}
}
`)
	formatted, err := CanonicalFormat(before, AppFilename)
	if err != nil {
		t.Fatal(err)
	}
	text := string(formatted)
	for _, want := range []string{
		`connect_timeout`, `"5s"`,
		`call_timeout`, `"30s"`,
		`source`, `"assistants/Café"`,
		`package`, `"assistants/Café/package.json"`,
		`package_lock`, `"assistants/Café/package-lock.json"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("formatted source missing %q:\n%s", want, formatted)
		}
	}
	if strings.Contains(text, `duration("`) {
		t.Fatalf("formatter retained contextual duration constructors:\n%s", formatted)
	}
	second, err := CanonicalFormat(formatted, AppFilename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatted, second) {
		t.Fatalf("formatter is not idempotent:\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}
}

func writeNestedModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
