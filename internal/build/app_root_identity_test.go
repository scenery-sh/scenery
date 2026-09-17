package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

// A session started through a symbolic link and a development build of the
// same spelling must name one build: the workspace and the local framework
// replacement written into its go.mod follow the resolved root.
func TestSymlinkedAppRootGivesUpAndBuildOneWorkspaceAndBuildInput(t *testing.T) {
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "checkout", "app")
	for rel, contents := range map[string]string{
		appcfg.PrimaryConfigFilename: `{"name":"linked","envs":{"local":{"default":true}}}`,
		"go.mod":                     "module example.test/linked\n\ngo 1.26\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => ./.scenery/framework/source/sha256-" + strings.Repeat("a", 64) + "\n",
		"linked.go":                  "package linked\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(base, "checkout"), alias); err != nil {
		t.Fatal(err)
	}
	spelled := filepath.Join(alias, "app")

	// scenery up --detach --app-root <spelled> hands the local agent's worktree
	// root to the session child, which discovers the app root again.
	parent, _, err := appcfg.DiscoverRoot(spelled)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := localagent.PathsForWorktree(filepath.Join(base, "agent"), parent)
	if err != nil {
		t.Fatal(err)
	}
	upRoot, upConfig, err := appcfg.DiscoverRoot(worktree.AppRoot)
	if err != nil {
		t.Fatal(err)
	}
	// scenery build --development --app-root <spelled>
	buildRoot, buildConfig, err := appcfg.DiscoverRoot(spelled)
	if err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(base, "cache")
	identity := func(root string, cfg appcfg.Config) (string, string) {
		t.Helper()
		workspace, err := WorkspaceDirAt(cacheRoot, root, cfg.Name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, _, err := syncSourceFiles(workspace, root, nil, nil); err != nil {
			t.Fatal(err)
		}
		output, err := json.Marshal(goListPackage{
			Dir: workspace, ImportPath: "example.test/linked", GoFiles: []string{"linked.go"},
			Module: &goListModule{Path: "example.test/linked", Dir: workspace, GoMod: filepath.Join(workspace, "go.mod")},
		})
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := buildInputManifestFromGoList(&Result{AppRoot: root, Dir: workspace, Target: &compiler.GoBuildTarget{Name: "development"}}, output)
		if err != nil {
			t.Fatal(err)
		}
		return workspace, manifest.Digest
	}
	upWorkspace, upDigest := identity(upRoot, upConfig)
	buildWorkspace, buildDigest := identity(buildRoot, buildConfig)
	if upWorkspace != buildWorkspace || upDigest != buildDigest {
		t.Fatalf("up builds in %q with input %s, build in %q with input %s", upWorkspace, upDigest, buildWorkspace, buildDigest)
	}
	if upRoot != root || buildRoot != root {
		t.Fatalf("up root %q and build root %q, want the canonical root %q", upRoot, buildRoot, root)
	}
}
