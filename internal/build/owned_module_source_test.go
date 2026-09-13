package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedGoModuleSourceBindsImmutableGeneration(t *testing.T) {
	appRoot := t.TempDir()
	workspace := filepath.Join(appRoot, ".scenery", "build", "workspace")
	dependency := t.TempDir()
	writeOwnedModuleFixture(t, appRoot, workspace, dependency)
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"A\"\n")
	writeBuildTestFile(t, dependency, ".inputs/value.txt", "captured")
	writeBuildTestFile(t, dependency, ".git", "gitdir: elsewhere\n")
	if err := os.MkdirAll(filepath.Join(dependency, "assets", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	sources, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDependency, err := filepath.EvalSymlinks(dependency)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].ModulePath != "example.test/dependency" || sources[0].OriginRoot != canonicalDependency {
		t.Fatalf("owned sources = %#v", sources)
	}
	source := sources[0]
	if !pathWithinRoot(ownedGoModuleRoot(appRoot), source.GenerationRoot) || source.Digest == "" {
		t.Fatalf("generation escaped owned root: %#v", source)
	}
	goMod, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "replace example.test/dependency => "+source.GenerationRoot) {
		t.Fatalf("workspace does not select owned generation:\n%s", goMod)
	}
	if data, err := os.ReadFile(filepath.Join(source.GenerationRoot, "dep.go")); err != nil || string(data) != "package dependency\nconst Value = \"A\"\n" {
		t.Fatalf("captured dependency = %q, err=%v", data, err)
	}
	originInfo, originErr := os.Stat(filepath.Join(dependency, "dep.go"))
	generationInfo, generationErr := os.Stat(filepath.Join(source.GenerationRoot, "dep.go"))
	if originErr != nil || generationErr != nil || os.SameFile(originInfo, generationInfo) {
		t.Fatalf("generation retained a link to mutable source: origin=%v generation=%v", originErr, generationErr)
	}
	if info, err := os.Stat(filepath.Join(source.GenerationRoot, "assets", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory membership was not captured: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(source.GenerationRoot, ".inputs", "value.txt")); err != nil || string(data) != "captured" {
		t.Fatalf("hidden compiler input was not captured: %q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(source.GenerationRoot, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("VCS worktree pointer entered source generation: %v", err)
	}
	if err := VerifyOwnedGoModuleSourcesContext(context.Background(), sources); err != nil {
		t.Fatal(err)
	}
	workspaceModPath := filepath.Join(workspace, "go.mod")
	workspaceMod, err := os.ReadFile(workspaceModPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceModPath, append(workspaceMod, []byte("\nrequire example.test/tidied v1.2.3 // indirect\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil); err != nil {
		t.Fatal(err)
	}
	workspaceMod, err = os.ReadFile(workspaceModPath)
	if err != nil || !strings.Contains(string(workspaceMod), "example.test/tidied v1.2.3 // indirect") {
		t.Fatalf("binding discarded workspace tidy result: %s err=%v", workspaceMod, err)
	}
	var stable workspaceMutation
	if _, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, &stable); err != nil {
		t.Fatal(err)
	}
	if stable.filesWritten != 0 {
		t.Fatalf("stable binding rewrote %d workspace files: %v", stable.filesWritten, stable.writtenPaths)
	}

	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"B\"\n")
	if err := VerifyOwnedGoModuleSourcesContext(context.Background(), sources); err == nil || !strings.Contains(err.Error(), "source changed after capture") {
		t.Fatalf("live source drift was accepted: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(source.GenerationRoot, "dep.go")); err != nil || strings.Contains(string(data), "B") {
		t.Fatalf("owned generation followed mutable origin: %q err=%v", data, err)
	}
}

func TestOwnedGoModuleSourceRejectsSymlinksAndCancellation(t *testing.T) {
	appRoot := t.TempDir()
	workspace := filepath.Join(appRoot, ".scenery", "build", "workspace")
	dependency := t.TempDir()
	writeOwnedModuleFixture(t, appRoot, workspace, dependency)
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\n")
	if err := os.Symlink("dep.go", filepath.Join(dependency, "alias.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil); err == nil || !strings.Contains(err.Error(), "contains a symlink") {
		t.Fatalf("symlinked source was accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(dependency, "alias.go")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bindOwnedGoModuleSources(ctx, appRoot, workspace, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture = %v", err)
	}
}

func TestOwnedGoModuleSourceCanceledCopyRemovesStaging(t *testing.T) {
	appRoot, dependency := t.TempDir(), t.TempDir()
	writeBuildTestFile(t, dependency, "go.mod", "module example.test/dependency\n\ngo 1.26.3\n")
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\n")
	ctx := &errAfterContext{remaining: 3} // root plus the two source entries
	if _, _, err := materializeOwnedGoModuleSource(ctx, appRoot, "example.test/dependency", dependency); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy = %v", err)
	}
	entries, err := os.ReadDir(ownedGoModuleRoot(appRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled staging remained: %v", entries)
	}
}

func TestOwnedGoModuleSourceRebuildsCorruption(t *testing.T) {
	appRoot := t.TempDir()
	workspace := filepath.Join(appRoot, ".scenery", "build", "workspace")
	dependency := t.TempDir()
	writeOwnedModuleFixture(t, appRoot, workspace, dependency)
	original := "package dependency\nconst Value = \"A\"\n"
	writeBuildTestFile(t, dependency, "dep.go", original)

	first, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	generation := first[0].GenerationRoot
	if err := os.Chmod(generation, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(generation, "dep.go"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, "dep.go"), []byte("package dependency\nconst Value = \"CORRUPT\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].GenerationRoot != generation || second[0].Digest != first[0].Digest {
		t.Fatalf("rebuilt generation identity = %#v, want %#v", second, first)
	}
	if data, err := os.ReadFile(filepath.Join(generation, "dep.go")); err != nil || string(data) != original {
		t.Fatalf("corrupt generation was retained: %q err=%v", data, err)
	}
	if err := VerifyOwnedGoModuleSourcesContext(context.Background(), second); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedGoModuleSourcesRemainWorktreeScoped(t *testing.T) {
	dependency := t.TempDir()
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"A\"\n")
	appA, appB := t.TempDir(), t.TempDir()
	workspaceA := filepath.Join(appA, ".scenery", "build", "workspace")
	workspaceB := filepath.Join(appB, ".scenery", "build", "workspace")
	writeOwnedModuleFixture(t, appA, workspaceA, dependency)
	writeOwnedModuleFixture(t, appB, workspaceB, dependency)

	first, err := bindOwnedGoModuleSources(context.Background(), appA, workspaceA, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"B\"\n")
	second, err := bindOwnedGoModuleSources(context.Background(), appB, workspaceB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].GenerationRoot == second[0].GenerationRoot || first[0].Digest == second[0].Digest {
		t.Fatalf("worktree generations were conflated: A=%#v B=%#v", first[0], second[0])
	}
	if !pathWithinRoot(ownedGoModuleRoot(appA), first[0].GenerationRoot) || !pathWithinRoot(ownedGoModuleRoot(appB), second[0].GenerationRoot) {
		t.Fatalf("generation ownership escaped app roots: A=%#v B=%#v", first[0], second[0])
	}
	if data, err := os.ReadFile(filepath.Join(first[0].GenerationRoot, "dep.go")); err != nil || !strings.Contains(string(data), "A") {
		t.Fatalf("first worktree generation changed: %q err=%v", data, err)
	}
}

func writeOwnedModuleFixture(t *testing.T, appRoot, workspace, dependency string) {
	t.Helper()
	goMod := "module example.test/application\n\ngo 1.26.3\n\nrequire example.test/dependency v0.0.0\n\nreplace example.test/dependency => " + dependency + "\n"
	writeBuildTestFile(t, appRoot, "go.mod", goMod)
	writeBuildTestFile(t, workspace, "go.mod", goMod)
	writeBuildTestFile(t, dependency, "go.mod", "module example.test/dependency\n\ngo 1.26.3\n")
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type errAfterContext struct{ remaining int }

func (c *errAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *errAfterContext) Done() <-chan struct{}       { return nil }
func (c *errAfterContext) Value(any) any               { return nil }
func (c *errAfterContext) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}
