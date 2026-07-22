package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func diagnosticsContain(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func deploymentPlanFixture(t *testing.T, lifecycle string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sceneryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(sceneryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, appFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"scenery.runtime-http",`, `"scenery.runtime-http",
    "scenery.data",
    "scenery.deployment",`, 1))
	data = append(data, []byte(`

provider "postgres" {
  source = "registry.scenery.dev/core/postgres"
}
data_source "database" {
  provider = provider.postgres
  lifecycle = "`+lifecycle+`"
  config = { database = "nativeapp" }
}
deployment "preview" {
  environment = "preview"
  data_source {
    target = data_source.database
    config = { database = "nativeapp_preview" }
  }
}
`)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	integrity, ok := BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		t.Fatal("builtin postgres descriptor is unavailable")
	}
	lockfile := fmt.Sprintf(`lock {}
provider "postgres" {
  source = "registry.scenery.dev/core/postgres"
  integrity = %q
  compile_descriptor_digest = %q
  runtime_abi = "scenery.data-runtime/v1"
  deployment_abi = %q
}
`, integrity, integrity, deploymentProviderABI)
	if err := os.WriteFile(filepath.Join(root, appLockFilename), []byte(lockfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeNestedModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
