package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

// The prepared bytes and the authored module are verified concurrently; each
// still fails the selection on its own, and a changed prepared executable is
// reported ahead of a module disagreement.
func TestFrameworkSelectionVerifiesPreparedBytesAndModuleTogether(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(root, "origin")
	writeBuildTestFile(t, origin, "go.mod", "module scenery.sh\n\ngo 1.27.0\n")
	writeBuildTestFile(t, origin, "runtime.go", "package scenery\n")
	source, err := FrameworkSourceManifest(origin)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, ".scenery/framework/source", strings.TrimPrefix(source.Digest, "sha256:"))
	if err := materializeFrameworkSource(source, snapshot); err != nil {
		t.Fatal(err)
	}
	if source, err = FrameworkSourceManifest(snapshot); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, root, "candidate", "executable bytes")
	digest, err := digestExecutable(filepath.Join(root, "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, ".scenery/framework/bin", strings.TrimPrefix(source.Digest, "sha256:"), "test-platform", strings.TrimPrefix(digest, "sha256:"), "scenery")
	writeBuildTestFile(t, filepath.Dir(binary), "scenery", "executable bytes")
	selection := FrameworkSelection{ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema), AppRoot: root, Source: source, Executable: binary, ExecutableDigest: digest}
	writeBuildTestFile(t, root, "go.mod", "module app\n\ngo 1.27.0\nrequire scenery.sh v0.0.0\nreplace scenery.sh => ./origin\n")
	if err := VerifyFrameworkSelection(context.Background(), selection); err != nil {
		t.Fatal(err)
	}

	writeBuildTestFile(t, origin, "runtime.go", "package scenery\nconst Changed = true\n")
	if err := VerifyFrameworkSelection(context.Background(), selection); err == nil || !strings.Contains(err.Error(), "application module and prepared Scenery producer disagree") {
		t.Fatalf("module disagreement = %v", err)
	}
	if err := os.WriteFile(binary, []byte("changed bytes...."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFrameworkSelection(context.Background(), selection); err == nil || !strings.Contains(err.Error(), "prepared framework executable content changed") {
		t.Fatalf("changed executable with a disagreeing module = %v", err)
	}
	writeBuildTestFile(t, origin, "runtime.go", "package scenery\n")
	if err := VerifyFrameworkSelection(context.Background(), selection); err == nil || !strings.Contains(err.Error(), "prepared framework executable content changed") {
		t.Fatalf("changed executable with an agreeing module = %v", err)
	}
}
