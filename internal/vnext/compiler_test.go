package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileHouseCore(t *testing.T) {
	result, err := Compile(filepath.Join("testdata", "house"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("invalid: %#v", result.Diagnostics)
	}
	if result.Manifest.Application.Name != "clean_tech" {
		t.Fatalf("application = %q", result.Manifest.Application.Name)
	}
	if !strings.HasPrefix(result.Manifest.ContractRevision, "sha256:") {
		t.Fatalf("revision = %q", result.Manifest.ContractRevision)
	}
	addresses := map[string]bool{}
	for _, resource := range result.Manifest.Resources {
		addresses[resource.Address] = true
	}
	for _, want := range []string{"app/http_gateway/public_api", "app/module/house", "house/service/house", "house/operation/process_scene", "house/binding/process_scene_http"} {
		if !addresses[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestContractRevisionIgnoresFormatting(t *testing.T) {
	source := filepath.Join("testdata", "house")
	result, err := Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	copyTree(t, source, temp)
	path := filepath.Join(temp, "scenery.scn")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = append([]byte("# formatting changes workspace bytes\n\n"), b...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.ContractRevision != changed.Manifest.ContractRevision {
		t.Fatalf("contract revision changed: %s != %s", result.Manifest.ContractRevision, changed.Manifest.ContractRevision)
	}
	if result.WorkspaceRevision == changed.WorkspaceRevision {
		t.Fatal("workspace revision did not change")
	}
}

func TestMixedModeRequiresExplicitTarget(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	if err := os.WriteFile(filepath.Join(temp, ".scenery.json"), []byte(`{"name":"clean-tech"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	migration := `migration {
  frontend = "scenery.legacy.v0"
  legacy_config = ".scenery.json"
  legacy_service "jobs" { package = "./jobs" }
  native_service "house" { module = module.house }
}`
	if err := os.WriteFile(filepath.Join(temp, "scenery.migration.scn"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected missing target diagnostic")
	}
	if !hasDiagnostic(result.Diagnostics, "SCN5104") {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
}

func TestDuplicateParameterizedRoutesConflict(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "house", "duplicate.scn")
	duplicate := `binding "duplicate" {
  gateway = var.gateway
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol = "http"
  delivery = "call"
  authentication = std.authentication.none
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  http {
    method = "POST"
    path = "/house/process"
    codec_profile = std.codec.http_json_v1
  }
}`
	if err := os.WriteFile(path, []byte(duplicate), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() || !hasDiagnostic(result.Diagnostics, "SCN2002") {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
}

func TestFormatPreservesCommentsAndIsIdempotent(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "scenery.scn")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before = append([]byte("# retained comment\n"), before...)
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(temp, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "# retained comment") {
		t.Fatal("comment was lost")
	}
	result, err := Format(temp, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 0 {
		t.Fatalf("second format changed %#v", result.Changed)
	}
}

func hasDiagnostic(diags []Diagnostic, code string) bool {
	for _, diag := range diags {
		if diag.Code == code {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
