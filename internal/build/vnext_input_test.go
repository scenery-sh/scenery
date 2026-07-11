package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"scenery.sh/internal/parse"
	"scenery.sh/internal/vnext"
)

func TestVNextBuildInputManifestIncludesLocalReplaceBytes(t *testing.T) {
	root := t.TempDir()
	appRoot, dependencyRoot := filepath.Join(root, "app"), filepath.Join(root, "dependency")
	for _, directory := range []string{appRoot, dependencyRoot, filepath.Join(appRoot, "scenery_internal_main")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dependencyRoot, "go.mod"), "module example.test/dependency\n\ngo 1.26\n")
	dependencyFile := filepath.Join(dependencyRoot, "value.go")
	write(dependencyFile, "package dependency\n\nconst Value = 1\n")
	write(filepath.Join(appRoot, "go.mod"), "module example.test/app\n\ngo 1.26\n\nrequire example.test/dependency v0.0.0\nreplace example.test/dependency => ../dependency\n")
	write(filepath.Join(appRoot, "scenery_internal_main", "main.go"), "package main\n\nimport _ \"example.test/dependency\"\n\nfunc main() {}\n")
	result := &Result{AppRoot: appRoot, Dir: appRoot, VNextTarget: &vnext.GoBuildTarget{
		Name: "development", Context: parse.GoTargetContext{ModuleRoot: appRoot, Patterns: []string{"./..."}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
	}}
	before, err := buildVNextInputManifest(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	write(dependencyFile, "package dependency\n\nconst Value = 2\n")
	after, err := buildVNextInputManifest(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == after.Digest {
		t.Fatal("local replacement change did not change build input manifest")
	}
}
