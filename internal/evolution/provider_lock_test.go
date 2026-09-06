package evolution

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderLockExplicitCheckAndIdempotentUpdate(t *testing.T) {
	root := t.TempDir()
	writeProviderLockTestFile(t, root, "app.scn", `application "demo" {}
provider "db" { source = "registry.scenery.dev/core/postgres" }
provider "reporting_db" { source = "registry.scenery.dev/core/postgres" }
provider "jobs" { source = "registry.scenery.dev/core/durable" }
`)
	result, err := SyncBuiltinProviderLocks(root, true)
	if err != nil || !result.Changed || len(result.Providers) != 2 {
		t.Fatalf("check: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "app.lock.scn")); !os.IsNotExist(err) {
		t.Fatalf("check wrote lock: %v", err)
	}
	result, err = SyncBuiltinProviderLocks(root, false)
	if err != nil || !result.Changed {
		t.Fatalf("apply: %+v %v", result, err)
	}
	before, err := os.ReadFile(filepath.Join(root, "app.lock.scn"))
	if err != nil {
		t.Fatal(err)
	}
	result, err = SyncBuiltinProviderLocks(root, false)
	if err != nil || result.Changed {
		t.Fatalf("repeat: %+v %v", result, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "app.lock.scn"))
	if !bytes.Equal(before, after) {
		t.Fatal("repeat changed bytes")
	}
}

func TestProviderLockPreservesUnrelatedDependenciesAndComments(t *testing.T) {
	root := t.TempDir()
	writeProviderLockTestFile(t, root, "app.scn", `application "demo" {}
provider "db" { source = "registry.scenery.dev/core/postgres" }
provider "other" { source = "vendor/custom" }
`)
	module := "module \"shared\" {\n  source = \"registry/shared\"\n  integrity = \"sha256:" + strings.Repeat("a", 64) + "\"\n}\n"
	external := "provider \"other\" {\n  source = \"vendor/custom\"\n  integrity = \"sha256:" + strings.Repeat("b", 64) + "\"\n}\n"
	writeProviderLockTestFile(t, root, "app.lock.scn", "# kept\nlock {}\n"+module+external)
	if _, err := SyncBuiltinProviderLocks(root, false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "app.lock.scn"))
	for _, preserved := range []string{"# kept\n", module, external} {
		if !strings.Contains(string(after), preserved) {
			t.Fatalf("lost %q: %s", preserved, after)
		}
	}
	if result, err := SyncBuiltinProviderLocks(root, true); err != nil || result.Changed {
		t.Fatalf("check: %+v %v", result, err)
	}
}

func TestProviderLockRejectsUnsafeAndUnpinnedInputs(t *testing.T) {
	for _, scenario := range []string{"symlink", "malformed", "external"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := "registry.scenery.dev/core/postgres"
			if scenario == "external" {
				source = "vendor/custom"
			}
			writeProviderLockTestFile(t, root, "app.scn", "provider \"db\" { source = \""+source+"\" }\n")
			if scenario == "malformed" {
				writeProviderLockTestFile(t, root, "app.lock.scn", "lock { broken")
			}
			if scenario == "symlink" {
				outside := filepath.Join(t.TempDir(), "keep.scn")
				if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "app.lock.scn")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := SyncBuiltinProviderLocks(root, false); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}

func writeProviderLockTestFile(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
