package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSharedBinaryRejectsChangedFramework(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "framework")
}

func TestSharedBinaryRejectsChangedLocalReplace(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "local_replace")
}

func TestSharedBinaryRejectsChangedWorkspace(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "workspace")
}

func TestSharedBinaryRejectsRestoredInputs(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "restored")
}

func testSharedBinaryRejectsChangedInputs(t *testing.T, kind string) {
	t.Helper()
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, result := newCachedBuildTestWorkspace(t, "input-proof")
	prepareSharedBinaryTestResult(root, result)
	dependency := t.TempDir()
	writeBuildTestFile(t, dependency, "go.mod", "module scenery.sh\n")
	writeBuildTestFile(t, dependency, "dep.go", "package dep\nconst Value = \"A\"\n")
	inputPath := filepath.Join(dependency, "dep.go")
	if kind == "workspace" {
		inputPath = filepath.Join(result.Dir, "svc/api.go")
	}
	original, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "framework" {
		source, err := FrameworkSourceManifest(dependency)
		if err != nil {
			t.Fatal(err)
		}
		result.FrameworkSourceRoot, result.FrameworkSourceDigest = dependency, source.Digest
	}
	// Inject only Go's package discovery. Actual file hashes, module
	// metadata, workspace and framework checks remain production code.
	discover := func(_ context.Context, candidate *Result) (*BuildInputManifest, error) {
		data, err := json.Marshal(goListPackage{Dir: dependency, ImportPath: "example.test/local", GoFiles: []string{"dep.go"},
			Module: &goListModule{Path: "example.test/local", Replace: &goListModule{Dir: dependency, GoMod: filepath.Join(dependency, "go.mod")}}})
		if err != nil {
			return nil, err
		}
		return buildInputManifestFromGoList(candidate, data)
	}
	result.BuildInput, err = discover(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	bindSharedBinaryTestIdentity(result)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	builds := 0
	restore := SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go invocation: %v", args)
		}
		builds++
		if builds == 1 {
			close(started)
			<-release
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return err
		}
		if kind == "restored" && builds == 1 {
			if err := os.WriteFile(inputPath, original, 0o644); err != nil {
				return err
			}
		}
		return os.WriteFile(output, data, 0o755)
	})
	t.Cleanup(restore)
	build := func() error {
		return runSharedGoBuildWithInputCheck(context.Background(), result, func(ctx context.Context) error {
			return verifySharedBinaryInputs(ctx, result, discover)
		})
	}
	done := make(chan error, 1)
	go func() { done <- build() }()
	<-started
	if err := os.WriteFile(inputPath, append([]byte("// B\n"), original...), 0o644); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed input was not rejected: %v", err)
	}
	if err := os.WriteFile(inputPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	result.BuildInput, err = discover(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	if err := build(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Binary)
	if err != nil || string(data) != string(original) || builds != 2 {
		t.Fatalf("A retry restored rejected B: builds=%d data=%q err=%v", builds, data, err)
	}
}
