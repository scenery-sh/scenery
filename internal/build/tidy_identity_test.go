package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTidyInvalidatesLinkedIdentityWhenConsumedInputsChange(t *testing.T) {
	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.test/app\n")
	before, err := workspaceBuildFingerprint(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := prepareCompileTestResult(&Result{
		Dir: root, AppRoot: root, BuildFingerprint: before,
		BuildInput:              &BuildInputManifest{Digest: "before"},
		ImplementationRevisions: map[string]string{"development": "before"},
	})
	restore := SetGoRunnerForTesting(func(_ context.Context, dir string, args ...string) error {
		if len(args) != 2 || args[0] != "mod" || args[1] != "tidy" {
			t.Fatalf("unexpected command: %v", args)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\nrequire example.test/dep v1.0.0\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "go.sum"), []byte("example.test/dep v1.0.0 h1:fixture\n"), 0o644)
	})
	defer restore()
	if err := tidyWorkspace(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if result.BuildFingerprint == before || result.BuildInput != nil || result.ImplementationRevisions != nil || result.RuntimeLinkerMetadata != nil {
		t.Fatal("tidy retained pre-change runtime identity")
	}
	final, err := workspaceBuildFingerprint(root, nil)
	if err != nil || result.BuildFingerprint != final || result.Binary != filepath.Join(root, workspaceBinaryName(root, final)) {
		t.Fatalf("final binary key mismatch: %v", err)
	}
	// Repeating identical tidy bytes does not invalidate newly prepared metadata.
	result.BuildInput = &BuildInputManifest{Digest: "after"}
	result.RuntimeLinkerMetadata = map[string]string{"new": "after"}
	if err := tidyWorkspace(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if result.BuildInput == nil || result.RuntimeLinkerMetadata["new"] != "after" {
		t.Fatal("unchanged consumed inputs invalidated current metadata")
	}
}
