package build

import (
	"context"
	"os"
	"path/filepath"
	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"strings"
	"testing"
)

func TestNativeExperimentWorkspaceOwnership(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	appRoot, ordinary, workspace := filepath.Join(root, "app"), filepath.Join(root, "ordinary"), filepath.Join(root, "experiment")
	if err := claimNativeExperimentWorkspace(appRoot, ordinary, workspace); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "previous.go", "owned")
	if err := claimNativeExperimentWorkspace(appRoot, ordinary, workspace); err != nil {
		t.Fatal(err)
	}
	if err := claimNativeExperimentWorkspace(appRoot+"other", ordinary, workspace); err == nil {
		t.Fatal("adopted foreign workspace")
	}
	for _, path := range []string{appRoot, ordinary, root, filepath.Join(appRoot, "child")} {
		if err := claimNativeExperimentWorkspace(appRoot, ordinary, path); err == nil {
			t.Fatal("accepted overlapping workspace")
		}
	}
	unowned := filepath.Join(root, "unowned")
	writeBuildTestFile(t, unowned, "retained", "preserve")
	if err := claimNativeExperimentWorkspace(appRoot, ordinary, unowned); err == nil {
		t.Fatal("adopted nonempty workspace")
	}
	data, err := os.ReadFile(filepath.Join(unowned, "retained"))
	if err != nil || string(data) != "preserve" {
		t.Fatal("modified unowned bytes")
	}
}

func TestNativeExperimentTracksSelectedEntrypointBytes(t *testing.T) {
	root := t.TempDir()
	result := &Result{Dir: root, GeneratedFiles: []string{"scenery_native_worker/main.go", "scenery_framework_kernel/main.go"}}
	files := map[string][]byte{"scenery_native_worker/main.go": []byte("worker"), "scenery_framework_kernel/main.go": []byte("kernel")}
	if err := validateNativeExperimentProjection(files); err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		writeBuildTestFile(t, root, path, string(data))
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
	for _, path := range []string{"../escape.go", "scenery_internal_main/main.go"} {
		files[path] = []byte("invalid")
		if err := validateNativeExperimentProjection(files); err == nil {
			t.Fatal("accepted invalid projection")
		}
		delete(files, path)
	}
	delete(files, "scenery_native_worker/main.go")
	if err := validateNativeExperimentProjection(files); err == nil {
		t.Fatal("accepted missing entry")
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

func TestNativeRefreshRejectsOrdinaryState(t *testing.T) {
	ctx := context.Background()
	native := &Result{nativeExperiment: true, AppRoot: "/app", Dir: "/native"}
	if _, err := RefreshCachedWorkspaceWithSnapshotContext(ctx, "/app", native, nil); err == nil {
		t.Fatal("ordinary refresh admitted native projection")
	}
	if _, err := RefreshNativeExperiment(ctx, "/app", app.Config{}, nil, "/native", nil, &Result{AppRoot: "/app", Dir: "/native"}); err == nil {
		t.Fatal("native refresh adopted ordinary result")
	}
	if _, err := RefreshNativeExperiment(ctx, "/other", app.Config{}, nil, "/native", nil, native); err == nil {
		t.Fatal("native refresh adopted another root")
	}
}
