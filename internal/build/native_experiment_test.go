package build

import (
	"os"
	"path/filepath"
	"scenery.sh/internal/compiler"
	"strings"
	"testing"
)

func TestNativeExperimentPreservesPreparedProjections(t *testing.T) {
	for _, change := range []string{"projection", "authored", "escape", "module", "missing-entry"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			writeBuildTestFile(t, root, "internal/generated.go", "original")
			writeBuildTestFile(t, root, "authored.go", "authored")
			result := &Result{Dir: root, GeneratedFiles: []string{"internal/generated.go"}, SourceFiles: []string{"authored.go"}}
			files := map[string][]byte{"scenery_native_worker/main.go": []byte("worker"), "scenery_framework_kernel/main.go": []byte("kernel")}
			switch change {
			case "projection":
				files["internal/generated.go"] = []byte("changed")
			case "authored":
				files["authored.go"] = []byte("authored")
			case "escape":
				files["../escaped.go"] = []byte("escape")
			case "module":
				files["internal/foreign/go.mod"] = []byte("module foreign")
			case "missing-entry":
				delete(files, "scenery_native_worker/main.go")
			}
			if err := addNativeExperimentFiles(result, files); err == nil {
				t.Fatal("accepted replacement or unsupported addition")
			}
			for path, want := range map[string]string{"internal/generated.go": "original", "authored.go": "authored"} {
				data, err := os.ReadFile(filepath.Join(root, path))
				if err != nil || string(data) != want {
					t.Fatalf("modified %s: %v", path, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "scenery_native_worker")); !os.IsNotExist(err) {
				t.Fatal("partial publication before validation")
			}
		})
	}
}

func TestNativeExperimentTracksAddedEntrypointBytes(t *testing.T) {
	root := t.TempDir()
	result := &Result{Dir: root}
	files := map[string][]byte{"scenery_native_worker/main.go": []byte("worker"), "scenery_framework_kernel/main.go": []byte("kernel")}
	if err := addNativeExperimentFiles(result, files); err != nil {
		t.Fatal(err)
	}
	var err error
	result.BuildFingerprint, err = workspaceBuildFingerprint(root, nil, result.SourceFiles, result.GeneratedFiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPreparedWorkspace(result); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, root, "scenery_native_worker/main.go", "edited")
	if err := verifyPreparedWorkspace(result); err == nil {
		t.Fatal("accepted modified worker entrypoint")
	}
}

func TestNativeKernelReuseRequiresCurrentInputsAndExactRetainedBytes(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(input, []byte("compiled kernel"), 0755); err != nil {
		t.Fatal(err)
	}
	retained, err := RetainBinary(dir, input)
	if err != nil {
		t.Fatal(err)
	}
	current := &Result{BuildInput: &BuildInputManifest{Digest: "current"}, Target: &compiler.GoBuildTarget{Name: "development"}, ImplementationRevisions: map[string]string{"development": "implementation"}}
	previous := &NativeKernelArtifact{Binary: retained, Digest: strings.TrimPrefix(filepath.Base(retained), "scenery-app-"), BuildInput: &BuildInputManifest{Digest: "current"}, ImplementationRevision: "implementation"}
	if reuse, err := reusableNativeKernel(previous, current, dir); err != nil || !reuse {
		t.Fatalf("unchanged kernel rejected: %v", err)
	}
	previous.ImplementationRevision = "other-target"
	if reuse, err := reusableNativeKernel(previous, current, dir); err != nil || reuse {
		t.Fatalf("different target reused: %v", err)
	}
	previous.ImplementationRevision = "implementation"
	previous.BuildInput.Digest = "old"
	if reuse, err := reusableNativeKernel(previous, current, dir); err != nil || reuse {
		t.Fatalf("old inputs reused: %v", err)
	}
	previous.BuildInput.Digest = "current"
	original := previous.Binary
	previous.Binary = input
	if reuse, err := reusableNativeKernel(previous, current, dir); err == nil || reuse {
		t.Fatal("external kernel path admitted")
	}
	previous.Binary = original
	if err := os.WriteFile(retained, []byte("tampered kernel"), 0755); err != nil {
		t.Fatal(err)
	}
	if reuse, err := reusableNativeKernel(previous, current, dir); err == nil || reuse {
		t.Fatal("corrupt kernel bytes reused")
	}
}
