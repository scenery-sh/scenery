package build

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDesiredFrameworkReadsOnlyTheAuthoredSelection(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, module, version, root, fails string
	}{
		{name: "pinned", module: "require scenery.sh v0.3.7\n", version: "v0.3.7"},
		{name: "snapshot", module: "require scenery.sh v0.0.0\nreplace scenery.sh => ./.scenery/framework/source/abc\n", version: "v0.0.0", root: ".scenery/framework/source/abc"},
		{name: "other-version-replaced", module: "require scenery.sh v0.3.7\nreplace scenery.sh v0.3.6 => ../old\n", version: "v0.3.7"},
		{name: "absolute-replacement", module: "require scenery.sh v0.3.7\nreplace scenery.sh v0.3.7 => /src/scenery\n", version: "v0.3.7", root: "/src/scenery"},
		{name: "remote-replacement", module: "require scenery.sh v0.3.7\nreplace scenery.sh => example.test/fork v0.1.0\n", fails: "remote replacement"},
		{name: "unpinned", fails: "must pin scenery.sh"},
	} {
		appRoot := filepath.Join(root, test.name)
		writeBuildTestFile(t, appRoot, "go.mod", "module example.test/app\n\ngo 1.27.0\n"+test.module)
		want := DesiredFramework{Version: test.version, Root: test.root}
		if test.root != "" && !filepath.IsAbs(test.root) {
			want.Root = filepath.Join(appRoot, filepath.FromSlash(test.root))
		}
		got, err := ReadDesiredFramework(appRoot)
		if test.fails != "" {
			if err == nil || !strings.Contains(err.Error(), test.fails) {
				t.Errorf("%s: err = %v, want %q", test.name, err, test.fails)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", test.name, got, err, want)
		}
	}
}

func TestFrameworkSnapshotAndPreparedExecutableIdentity(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hex := strings.Repeat("ab", 32)
	store := filepath.Join(root, ".scenery", "framework")
	if digest, ok := FrameworkSnapshotDigest(root, filepath.Join(store, "source", hex)); !ok || digest != "sha256:"+hex {
		t.Fatalf("snapshot digest = %q, %v", digest, ok)
	}
	// A root spelled through a symlink (macOS temp directories) still names
	// its snapshot before that snapshot is materialized.
	spelled := t.TempDir()
	if digest, ok := FrameworkSnapshotDigest(spelled, filepath.Join(spelled, ".scenery", "framework", "source", hex)); !ok || digest != "sha256:"+hex {
		t.Fatalf("unmaterialized snapshot digest = %q, %v", digest, ok)
	}
	for _, other := range []string{
		filepath.Join(store, "source", hex, "nested"),
		filepath.Join(store, "source", "not-a-digest"),
		filepath.Join(root, "..", "scenery"),
		filepath.Join(store, "bin", hex),
	} {
		if digest, ok := FrameworkSnapshotDigest(root, other); ok {
			t.Errorf("%s named snapshot %s", other, digest)
		}
	}
	executable := filepath.Join(store, "bin", hex, "darwin-arm64-go1.27.0", strings.Repeat("cd", 32), "scenery")
	if !preparedFrameworkExecutable(root, "sha256:"+hex, executable) {
		t.Fatal("prepared producer was not recognized")
	}
	if preparedFrameworkExecutable(root, "sha256:"+strings.Repeat("ef", 32), executable) {
		t.Fatal("a producer of other source was recognized as this one")
	}
	if preparedFrameworkExecutable(root, "sha256:"+hex, filepath.Join(root, ".scenery", "harness", "bin", "scenery")) {
		t.Fatal("a repository harness binary was recognized as a prepared producer")
	}
	if preparedFrameworkExecutable(filepath.Join(root, "other"), "sha256:"+hex, executable) {
		t.Fatal("another app root's producer was recognized")
	}
}
