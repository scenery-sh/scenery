package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameworkSnapshotIsContentBoundAndIndependent(t *testing.T) {
	root := t.TempDir()
	sourceRoot, target := filepath.Join(root, "source"), filepath.Join(root, "snapshot")
	writeBuildTestFile(t, sourceRoot, "go.mod", "module scenery.sh\n\ngo 1.27.0\n")
	writeBuildTestFile(t, sourceRoot, "runtime/runtime.go", "package runtime\nimport _ \"embed\"\n//go:embed contract.json\nvar contract string\n")
	writeBuildTestFile(t, sourceRoot, "runtime/contract.json", `{"contract":"current"}`)
	writeBuildTestFile(t, sourceRoot, "runtime/runtime_test.go", "package runtime\n")
	before, err := FrameworkSourceManifest(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializeFrameworkSource(before, target); err != nil {
		t.Fatal(err)
	}
	frozen, err := FrameworkSourceManifest(target)
	if err != nil || frozen.Digest != before.Digest {
		t.Fatalf("frozen=%+v err=%v", frozen, err)
	}
	writeBuildTestFile(t, sourceRoot, "runtime/runtime_test.go", "package runtime\n// Test-only change.\n")
	afterTestEdit, err := FrameworkSourceManifest(sourceRoot)
	if err != nil || afterTestEdit.Digest != before.Digest {
		t.Fatal("test-only framework edit changed the compiled producer identity")
	}
	writeBuildTestFile(t, sourceRoot, "runtime/contract.json", `{"contract":"changed"}`)
	afterRuntimeEdit, err := FrameworkSourceManifest(sourceRoot)
	if err != nil || afterRuntimeEdit.Digest == before.Digest {
		t.Fatal("embedded runtime contract change did not change producer identity")
	}
	stillFrozen, err := FrameworkSourceManifest(target)
	if err != nil || stillFrozen.Digest != before.Digest {
		t.Fatal("source edit altered the already-selected framework snapshot")
	}
	if err := os.Chmod(filepath.Join(target, "runtime/contract.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, target, "runtime/contract.json", `{"contract":"tampered"}`)
	if err := materializeFrameworkSource(before, target); err == nil {
		t.Fatal("mutated retained snapshot was accepted by its content-addressed path")
	}
}

func TestFrameworkProducerLinkerFlagsStampDigestAndSourceRoot(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("ab", 32)
	flags, err := FrameworkProducerLinkerFlags(digest, "/opt/app/.scenery/framework/source/abc/")
	if err != nil {
		t.Fatal(err)
	}
	want := "-X=scenery.sh/internal/build.linkedFrameworkDigest=" + digest + " -X=scenery.sh/internal/app.linkedRepoRoot=/opt/app/.scenery/framework/source/abc"
	if flags != want {
		t.Fatalf("flags = %q, want %q", flags, want)
	}
	spaced, err := FrameworkProducerLinkerFlags(digest, "/Users/dev/My Apps/shop")
	if err != nil || !strings.HasSuffix(spaced, " '-X=scenery.sh/internal/app.linkedRepoRoot=/Users/dev/My Apps/shop'") {
		t.Fatalf("root with spaces = %q, %v", spaced, err)
	}
	for _, root := range []string{"", "relative/root", "/root\nwith/newline", "/root'quoted", "/root\"quoted"} {
		if _, err := FrameworkProducerLinkerFlags(digest, root); err == nil {
			t.Errorf("source root %q was accepted", root)
		}
	}
	if _, err := FrameworkProducerLinkerFlags("sha256:short", "/opt/app"); err == nil {
		t.Error("invalid digest was accepted")
	}
}
