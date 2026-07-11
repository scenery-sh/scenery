package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatterCanonicalizesCommentsAndContextualPrimitiveLiterals(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
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
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language{edition="2027"}
application "format_app" { version="1.0.0" }
module "parent" { source="./parent" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", "scenery.package.scn"), `package "parent" {
version="1.0.0"
scenery_version=">= 2.0.0, < 3.0.0"
}
module "child" { source="../child" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "child", "scenery.package.scn"), `package "child" {
version="1.0.0"
scenery_version=">= 2.0.0, < 3.0.0"
}
`)
	result, err := Format(root, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range result.Changed {
		seen[path] = true
	}
	if !seen["parent/scenery.package.scn"] || !seen["child/scenery.package.scn"] {
		t.Fatalf("formatted paths = %#v", result.Changed)
	}
}
