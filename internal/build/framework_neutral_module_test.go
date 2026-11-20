package build

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
)

// Worktrees of one commit select identical framework snapshots at different
// paths. Their process identities agree, so they share linked executables,
// while the snapshot content and every other byte of the module stay inputs.
func TestProcessIdentityIgnoresTheFrameworkSnapshotPathButNotItsContent(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	worktree := func(name, framework, module string) string {
		t.Helper()
		workspace := filepath.Join(root, name, "workspace")
		snapshot := filepath.Join(root, name, ".scenery/framework/source", digest)
		writeBuildTestFile(t, snapshot, "go.mod", "module scenery.sh\n\ngo 1.27\n")
		writeBuildTestFile(t, snapshot, "runtime/value.go", framework)
		writeBuildTestFile(t, workspace, "go.mod", module+"replace scenery.sh => "+snapshot+"\n")
		writeBuildTestFile(t, workspace, "scenery_internal_processes/host/main.go", "package main\n\nimport _ \"scenery.sh/runtime\"\n\nfunc main() {}\n")
		var listed bytes.Buffer
		for _, pkg := range []goListPackage{
			{Dir: filepath.Join(workspace, "scenery_internal_processes/host"), ImportPath: "example.test/app/scenery_internal_processes/host", GoFiles: []string{"main.go"},
				Imports: []string{"scenery.sh/runtime"}, Module: &goListModule{Path: "example.test/app", Main: true, Dir: workspace, GoMod: filepath.Join(workspace, "go.mod")}},
			{Dir: filepath.Join(snapshot, "runtime"), ImportPath: "scenery.sh/runtime", GoFiles: []string{"value.go"},
				Module: &goListModule{Path: "scenery.sh", Version: "v0.0.0", Replace: &goListModule{Path: snapshot, Dir: snapshot, GoMod: filepath.Join(snapshot, "go.mod")}}},
		} {
			if err := json.NewEncoder(&listed).Encode(pkg); err != nil {
				t.Fatal(err)
			}
		}
		result := &Result{AppRoot: workspace, Dir: workspace, Target: &compiler.GoBuildTarget{Name: "development"}}
		manifest, err := buildInputManifestFromGoListObserved(context.Background(), result, listed.Bytes(), nil)
		if err != nil {
			t.Fatal(err)
		}
		identity, _, err := manifest.developmentProcessDigest("host")
		if err != nil {
			t.Fatal(err)
		}
		return identity
	}
	const module = "module example.test/app\n\ngo 1.27\n\nrequire scenery.sh v0.0.0\n"
	const framework = "package runtime\n\nconst Value = 1\n"
	first := worktree("first", framework, module)
	if second := worktree("second", framework, module); second != first {
		t.Fatal("worktrees that select identical framework source at different paths have different process identities")
	}
	if changed := worktree("changed-framework", "package runtime\n\nconst Value = 2\n", module); changed == first {
		t.Fatal("framework source content is not a process input")
	}
	if changed := worktree("changed-module", framework, module+"require example.test/other v1.0.0\n"); changed == first {
		t.Fatal("a module edit outside the framework replacement is not a process input")
	}
}
