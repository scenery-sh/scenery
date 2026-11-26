package pythonbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestCacheFingerprintTracksModuleAndWorkspaceManifests(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", "[project]\nname = \"app\"\n")
	write("uv.lock", "version = 1\n")
	write("echo/service.py", "def new_service(input):\n    return object()\n")
	write("echo/test_service.py", "# tests do not affect worker binary\n")
	write("echo/__pycache__/service.cpython-314.pyc", "bytecode")
	write("echo/.venv/bin/python", "ignored")
	write("echo/.env", "SECRET=ignored")

	svc := Service{
		PythonWorkerTarget: compiler.PythonWorkerTarget{
			Workspace: ".", Directory: "echo", Entry: "service.py", Version: "3.14.6",
		},
		Name: "echo/service/echo",
	}

	first, err := buildFingerprint(root, svc, "/usr/bin/python3.14")
	if err != nil {
		t.Fatal(err)
	}
	// Editing skipped files must not invalidate the fingerprint.
	write("echo/test_service.py", "# edited test\n")
	write("echo/__pycache__/service.cpython-314.pyc", "changed")
	same, err := buildFingerprint(root, svc, "/usr/bin/python3.14")
	if err != nil || same != first {
		t.Fatalf("editing test/cache files changed fingerprint: %q vs %q (%v)", first, same, err)
	}
	// Editing interpreter, module source, or workspace lockfile must invalidate it.
	if diffInterp, err := buildFingerprint(root, svc, "/opt/python/3.14"); err != nil || diffInterp == first {
		t.Fatalf("changing interpreter did not change fingerprint: %v", err)
	}
	write("echo/service.py", "def new_service(input):\n    return None\n")
	second, err := buildFingerprint(root, svc, "/usr/bin/python3.14")
	if err != nil || second == first {
		t.Fatalf("editing service.py did not change fingerprint: %v", err)
	}
	write("uv.lock", "version = 2\n")
	third, err := buildFingerprint(root, svc, "/usr/bin/python3.14")
	if err != nil || third == second {
		t.Fatalf("editing uv.lock did not change fingerprint: %v", err)
	}
}

func TestReadBuildCacheAndPruneRemovesOnlyStaleWorkers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	privateRoot := filepath.Join(root, ".scenery", "python")
	if err := os.MkdirAll(filepath.Join(root, "echo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "echo", "service.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := Service{
		PythonWorkerTarget: compiler.PythonWorkerTarget{
			Workspace: ".", Directory: "echo", Entry: "service.py", Version: "3.14.6",
		},
		Name: "echo/service/echo",
	}
	const interp = "/usr/bin/python3.14"
	fp, err := buildFingerprint(root, svc, interp)
	if err != nil {
		t.Fatal(err)
	}
	activeHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	staleHex := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	activePath := filepath.Join(privateRoot, "worker-"+activeHex)
	stalePath := filepath.Join(privateRoot, "worker-"+staleHex)
	for _, p := range []string{activePath, stalePath} {
		if err := os.WriteFile(p, []byte("#!/usr/bin/python3.14\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cacheFile := filepath.Join(privateRoot, "build-0123456789abcdef.json")
	encoded, err := json.Marshal(buildCacheEntry{Inputs: fp, Executable: activeHex, Interpreter: interp})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	hit := readBuildCache(root, cacheFile, svc, interp)
	if !hit.CacheHit || hit.Path != activePath || hit.Digest != "sha256:"+activeHex {
		t.Fatalf("expected cache hit, got %#v", hit)
	}
	if miss := readBuildCache(root, cacheFile, svc, "/other/python"); miss.CacheHit {
		t.Fatalf("expected interpreter mismatch miss, got %#v", miss)
	}
	if err := Prune(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale artifact was not pruned: %v", err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active artifact was pruned: %v", err)
	}
}

func TestExportWorkerSourcesExcludesTestsSecretsAndSelf(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", "[project]\nname = \"app\"\n")
	write("echo/service.py", "ok = True\n")
	write("echo/scenerycontract/_build.py", "# builder\n")
	write("echo/test_service.py", "# skipped\n")
	write("echo/.env", "SECRET=1\n")
	write("echo/__pycache__/a.pyc", "bin")
	write("echo/oci-out/Dockerfile", "FROM scratch\n")

	svc := Service{
		PythonWorkerTarget: compiler.PythonWorkerTarget{
			Workspace: ".", Directory: "echo", Entry: "service.py", Version: "3.14.6",
		},
		Name: "echo/service/echo",
	}
	dest := filepath.Join(root, "exported")
	if err := ExportWorkerSources(root, []Service{svc}, dest, filepath.Join(root, "echo", "oci-out")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pyproject.toml", "echo/service.py", "echo/scenerycontract/_build.py"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("missing exported %s: %v", want, err)
		}
	}
	for _, absent := range []string{"echo/test_service.py", "echo/.env", "echo/__pycache__", "echo/oci-out"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(absent))); !os.IsNotExist(err) {
			t.Errorf("unexpected exported %s: %v", absent, err)
		}
	}
}
