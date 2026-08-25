package build

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestBuildInputManifestIncludesLocalReplaceBytesFromGoListInProcess(t *testing.T) {
	t.Parallel()

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
	result := &Result{AppRoot: appRoot, Dir: appRoot, Target: &compiler.GoBuildTarget{Name: "development"}}
	var goList bytes.Buffer
	encoder := json.NewEncoder(&goList)
	for _, pkg := range []goListPackage{
		{
			Dir: filepath.Join(appRoot, "scenery_internal_main"), ImportPath: "example.test/app/scenery_internal_main", GoFiles: []string{"main.go"},
			Module: &goListModule{Path: "example.test/app", GoMod: filepath.Join(appRoot, "go.mod")},
		},
		{
			Dir: dependencyRoot, ImportPath: "example.test/dependency", GoFiles: []string{"value.go"},
			Module: &goListModule{Path: "example.test/dependency", Replace: &goListModule{Path: dependencyRoot, GoMod: filepath.Join(dependencyRoot, "go.mod")}},
		},
	} {
		if err := encoder.Encode(pkg); err != nil {
			t.Fatal(err)
		}
	}
	before, err := buildInputManifestFromGoList(result, goList.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	write(dependencyFile, "package dependency\n\nconst Value = 2\n")
	after, err := buildInputManifestFromGoList(result, goList.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == after.Digest {
		t.Fatal("local replacement change did not change build input manifest")
	}
}
