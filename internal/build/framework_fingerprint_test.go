package build

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedFrameworkFingerprintChangesWhenFrameworkSourceChanges(t *testing.T) {
	t.Parallel()

	repo := newFrameworkFingerprintRepo(t)
	first, err := cachedFrameworkFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedFrameworkFingerprint(first) error = %v", err)
	}
	touchFrameworkFile(t, repo, "auth/standard_dev.go", "package auth\n\nfunc Changed() {}\n")
	second, err := cachedFrameworkFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedFrameworkFingerprint(second) error = %v", err)
	}
	if second == first {
		t.Fatalf("framework fingerprint did not change after source edit: %q", second)
	}
}

func TestCurrentFrameworkFingerprintSkipsWorkspaceWithoutLocalReplace(t *testing.T) {
	old := cachedFrameworkFingerprintFunc
	called := false
	cachedFrameworkFingerprintFunc = func(string) (string, error) {
		called = true
		return "", fmt.Errorf("unexpected framework scan")
	}
	t.Cleanup(func() { cachedFrameworkFingerprintFunc = old })

	workspace := t.TempDir()
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/app\n\ngo 1.26.3\n\nrequire scenery.sh v0.1.0\n")
	fingerprint, ok, err := currentFrameworkFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatalf("currentFrameworkFingerprintFromWorkspace() error = %v", err)
	}
	if ok || fingerprint != "" {
		t.Fatalf("fingerprint=%q ok=%v, want no local framework", fingerprint, ok)
	}
	if called {
		t.Fatal("expected no-replace workspace to skip framework scan")
	}
}

func BenchmarkCachedFrameworkFingerprintWarmPath(b *testing.B) {
	repo := repoRootForBenchmark(b)
	if _, err := cachedFrameworkFingerprint(repo); err != nil {
		b.Fatalf("prime cachedFrameworkFingerprint: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cachedFrameworkFingerprint(repo); err != nil {
			b.Fatal(err)
		}
	}
}

func newFrameworkFingerprintRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeBuildTestFile(t, repo, "go.mod", "module scenery.sh\n\ngo 1.26.3\n")
	writeBuildTestFile(t, repo, "go.sum", "")
	writeBuildTestFile(t, repo, "auth/standard_dev.go", "package auth\n\nfunc StandardDev() {}\n")
	writeBuildTestFile(t, repo, "auth/standard.go", "package auth\n\nimport \"embed\"\n\n//go:embed db/gen/schema.sql\nvar _ embed.FS\n")
	writeBuildTestFile(t, repo, "auth/db/gen/schema.sql", "create table users(id text);\n")
	return repo
}

func touchFrameworkFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func repoRootForBenchmark(b *testing.B) string {
	b.Helper()
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
