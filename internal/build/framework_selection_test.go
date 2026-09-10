package build

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

func TestPreparedFrameworkIsIndependentOfDesiredModule(t *testing.T) {
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
	source, err = FrameworkSourceManifest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, root, "candidate", "immutable executable bytes")
	digest, err := digestExecutable(filepath.Join(root, "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, ".scenery/framework/bin", strings.TrimPrefix(source.Digest, "sha256:"), "test-platform", strings.TrimPrefix(digest, "sha256:"), "scenery")
	writeBuildTestFile(t, filepath.Dir(binary), "scenery", "immutable executable bytes")
	selection := FrameworkSelection{ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema), AppRoot: root, Source: source, Executable: binary, ExecutableDigest: digest}
	writeBuildTestFile(t, root, "go.mod", "module app\n\ngo 1.27.0\nrequire scenery.sh v0.0.0\nreplace scenery.sh => ./origin\n")
	if err := VerifyFrameworkSelection(context.Background(), selection); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, origin, "runtime.go", "package scenery\nconst Changed = true\n")
	if err := VerifyPreparedFramework(selection); err != nil {
		t.Fatalf("desired edit invalidated immutable producer: %v", err)
	}
	if err := VerifyFrameworkSelection(context.Background(), selection); err == nil {
		t.Fatal("desired module mismatch accepted")
	}
	selection.Source.Inputs.SpecRevision = "sha256:" + strings.Repeat("f", 64)
	if err := VerifyPreparedFramework(selection); err == nil {
		t.Fatal("another producer's nested manifest accepted")
	}
	if err := WriteFrameworkSelection(selection); err == nil {
		t.Fatal("bootstrap published another executable's receipt")
	}
}
