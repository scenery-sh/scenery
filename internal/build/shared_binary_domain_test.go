package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/graph"
)

func TestSharedBinaryExternalRestoreWithoutChangeTime(t *testing.T) {
	testSharedBinaryRestoredExternalInput(t, "example.test/dependency", false)
}

func TestSharedBinaryFrameworkRestoreWithoutChangeTime(t *testing.T) {
	testSharedBinaryRestoredExternalInput(t, "scenery.sh", false)
}

func TestSharedBinaryExternalInputsCannotRestoreExistingArtifact(t *testing.T) {
	testSharedBinaryRestoredExternalInput(t, "example.test/dependency", true)
}

func TestSharedBinaryOwnedWorkspaceReusesWithLiveChecks(t *testing.T) {
	withoutBuildInputChangeTime(t)
	result, mainPackage := newSharedBinaryDomainFixture(t)
	setSharedBinaryDomainDiscovery(t, result, mainPackage)
	discover := runGoInputList
	lists, builds := 0, 0
	runGoInputList = func(ctx context.Context, directory string, environment []string, args ...string) ([]byte, error) {
		lists++
		return discover(ctx, directory, environment, args...)
	}
	t.Cleanup(SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go command: %v", args)
		}
		builds++
		return os.WriteFile(output, []byte("owned-input-output"), 0o755)
	}))
	for range 2 {
		if err := runSharedGoBuildContext(context.Background(), result); err != nil {
			t.Fatal(err)
		}
	}
	wantBuilds := 1
	if runtime.GOOS == "windows" {
		wantBuilds = 2 // No workspace lock, therefore no shared reuse.
	}
	if builds != wantBuilds || lists != 0 {
		t.Fatalf("owned reuse/live checks: builds=%d want=%d discoveries=%d want=0", builds, wantBuilds, lists)
	}
	// A persisted manifest's exact public digest is not fresh admission.
	encoded, err := json.Marshal(result.BuildInput)
	if err != nil {
		t.Fatal(err)
	}
	var persisted BuildInputManifest
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.sharedWorkspace != "" || persisted.Digest != result.BuildInput.Digest {
		t.Fatal("input ownership must be private without changing manifest identity")
	}
	result.BuildInput = &persisted
	if err := runSharedGoBuildContext(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if builds != wantBuilds+1 || lists != 0 {
		t.Fatalf("persisted manifest authorized reuse: builds=%d discoveries=%d", builds, lists)
	}
}

func TestSharedBinaryPostBuildCheckRelistsOnlyAfterDirectoryChange(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.go")
	writeBuildTestFile(t, root, "input.go", "package input\n")
	observed := map[string]buildInputFileStamp{}
	for _, path := range []string{root, input} {
		if err := observeBuildInputPath(observed, path); err != nil {
			t.Fatal(err)
		}
	}
	result, _ := newSharedBinaryDomainFixture(t)
	result.BuildInput = &BuildInputManifest{Digest: "same", observed: observed}
	discoveries := 0
	discover := func(context.Context, *Result) (*BuildInputManifest, error) {
		discoveries++
		return &BuildInputManifest{Digest: "same", observed: observed}, nil
	}
	if err := verifySharedBinaryInputs(context.Background(), result, discover); err != nil {
		t.Fatal(err)
	}
	if discoveries != 0 {
		t.Fatalf("stable inputs triggered %d package graph discoveries", discoveries)
	}
	changedAt := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(root, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	if err := verifySharedBinaryInputs(context.Background(), result, discover); err != nil {
		t.Fatal(err)
	}
	if discoveries != 1 {
		t.Fatalf("directory membership stamp triggered %d discoveries, want 1", discoveries)
	}
}

func TestSharedBinaryPostBuildCheckRejectsChangedFileWithoutRelisting(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.go")
	writeBuildTestFile(t, root, "input.go", "package input\nconst Value = 1\n")
	observed := map[string]buildInputFileStamp{}
	for _, path := range []string{root, input} {
		if err := observeBuildInputPath(observed, path); err != nil {
			t.Fatal(err)
		}
	}
	result, _ := newSharedBinaryDomainFixture(t)
	result.BuildInput = &BuildInputManifest{Digest: "same", observed: observed}
	discoveries := 0
	if err := os.WriteFile(input, []byte("package input\nconst Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifySharedBinaryInputs(context.Background(), result, func(context.Context, *Result) (*BuildInputManifest, error) {
		discoveries++
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed during compilation") {
		t.Fatalf("changed file accepted: %v", err)
	}
	if discoveries != 0 {
		t.Fatalf("changed file triggered package graph discovery: %d", discoveries)
	}
}

func TestSharedBinaryDomainRejectsResolverAndToolReads(t *testing.T) {
	for _, name := range []string{"missing_module", "missing_entrypoint", "external_module", "local_replace", "external_embed", "assembly", "unused_require", "unused_replace", "cgo", "native_environment", "native_input", "target_flags", "actual_flags", "actual_environment", "external_framework", "wrong_workspace"} {
		t.Run(name, func(t *testing.T) {
			result, pkg := newSharedBinaryDomainFixture(t)
			switch name {
			case "missing_module":
				pkg.Module = nil
			case "missing_entrypoint":
				pkg.GoFiles = nil
			case "external_module":
				pkg.Module.Dir = result.Dir + "-external"
			case "local_replace":
				pkg.Module.Replace = &goListModule{Dir: result.Dir, GoMod: filepath.Join(result.Dir, "go.mod")}
			case "external_embed":
				external := filepath.Join(t.TempDir(), "asset.txt")
				if err := os.WriteFile(external, []byte("asset"), 0o644); err != nil {
					t.Fatal(err)
				}
				relative, err := filepath.Rel(pkg.Dir, external)
				if err != nil {
					t.Fatal(err)
				}
				pkg.EmbedFiles = []string{relative}
			case "assembly":
				writeBuildTestFile(t, pkg.Dir, "native.s", "// external includes are not captured\n")
				pkg.SFiles = []string{"native.s"}
			case "unused_require", "unused_replace":
				directive := "require example.test/unused v0.0.0\n"
				if name == "unused_replace" {
					directive = "replace example.test/unused => ../external\n"
				}
				writeBuildTestFile(t, result.Dir, "go.mod", "module example.test/app\n\n"+directive)
			case "cgo":
				result.Target.Context.CGOEnabled = true
			case "native_environment":
				result.Target.Context.NativeToolEnv = map[string]string{"CGO_CFLAGS": "-I/external"}
			case "native_input":
				result.Target.Effective = map[string]any{"native_inputs": []any{"scenery_internal_main"}}
			case "target_flags":
				result.Target.Context.BuildFlags = []string{"-overlay=/external/overlay.json"}
			case "actual_flags":
				result.GoBuildFlags = []string{"-toolexec=/external/wrapper"}
			case "actual_environment":
				result.GoEnvironment = append(result.GoEnvironment, "GOFLAGS=-overlay=/external/overlay.json")
			case "external_framework":
				result.FrameworkSourceRoot = result.Dir + "-framework"
			}
			encoded, err := json.Marshal(pkg)
			if err != nil {
				t.Fatal(err)
			}
			result.BuildInput, err = buildInputManifestFromGoList(result, encoded)
			if err != nil {
				if name == "external_embed" && strings.Contains(err.Error(), "not a regular file or directory") {
					// The existing traversal guard may reject the escaping path
					// even before a build input domain can be constructed.
					return
				}
				t.Fatal(err)
			}
			if name == "wrong_workspace" {
				result.Dir += "-other-owner"
			}
			if sharedBinaryInputsSupported(result) {
				t.Fatal("unowned input domain admitted")
			}
		})
	}
}

// The compiler reads B, then restores A in the same inode with identical size,
// mode and mtime before returning. Every metadata observation deliberately
// lacks change time. Neither hashes nor end-of-action stamps distinguish this
// action from A, so only the cache's input ownership boundary can protect it.
func testSharedBinaryRestoredExternalInput(t *testing.T, module string, prepopulate bool) {
	t.Helper()
	withoutBuildInputChangeTime(t)
	result, mainPackage := newSharedBinaryDomainFixture(t)
	dependency := t.TempDir()
	writeBuildTestFile(t, dependency, "go.mod", "module "+module+"\n\ngo 1.27.0\n")
	const sourceA = "package dependency\nconst Value = \"A\"\n"
	const sourceB = "package dependency\nconst Value = \"B\"\n"
	writeBuildTestFile(t, dependency, "dep.go", sourceA)
	path := filepath.Join(dependency, "dep.go")
	before, err := buildInputLstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if buildInputFileChangeTime(before) != 0 {
		t.Fatal("regression must execute without change-time information")
	}
	external := goListPackage{Dir: dependency, ImportPath: module, GoFiles: []string{"dep.go"},
		Module: &goListModule{Path: module, Replace: &goListModule{Dir: dependency, GoMod: filepath.Join(dependency, "go.mod")}}}
	setSharedBinaryDomainDiscovery(t, result, mainPackage, external)
	originalDigest := result.BuildInput.Digest
	key, expected, err := sharedBinaryKey(result)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if prepopulate {
		seed := filepath.Join(t.TempDir(), "cached")
		if err := os.WriteFile(seed, []byte("cached-A"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := publishSharedBinary(cacheRoot, key, expected, seed); err != nil {
			t.Fatal(err)
		}
	}
	builds := 0
	t.Cleanup(SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go command: %v", args)
		}
		builds++
		if builds == 1 {
			if err := os.WriteFile(path, []byte(sourceB), before.Mode()); err != nil {
				return err
			}
		}
		consumed, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		want := sourceA
		if builds == 1 {
			want = sourceB
		}
		if string(consumed) != want {
			return fmt.Errorf("compiler consumed %q, want %q", consumed, want)
		}
		if err := os.WriteFile(path, []byte(sourceA), before.Mode()); err != nil {
			return err
		}
		if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
			return err
		}
		return os.WriteFile(output, consumed, 0o755)
	}))
	for attempt, want := range []string{sourceB, sourceA} {
		if err := runSharedGoBuildContext(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		if builds != attempt+1 {
			t.Fatalf("external source restored a shared executable: builds=%d, attempt=%d", builds, attempt)
		}
		binary, err := os.ReadFile(result.Binary)
		if err != nil || string(binary) != want {
			t.Fatalf("private compiled output=%q want=%q err=%v", binary, want, err)
		}
		after, err := buildInputLstat(path)
		if err != nil || buildInputStamp(before) != buildInputStamp(after) {
			t.Fatalf("non-change-time metadata was not restored: before=%+v after=%+v err=%v", buildInputStamp(before), buildInputStamp(after), err)
		}
		current, err := buildInputManifest(context.Background(), result)
		if err != nil || current.Digest != originalDigest {
			t.Fatalf("final A input identity changed: %v", err)
		}
		_, cached, hit, err := loadSharedBinary(cacheRoot, key)
		if err != nil || hit != prepopulate {
			t.Fatalf("external compilation changed shared cache membership: hit=%t err=%v", hit, err)
		}
		if hit && string(cached) != "cached-A" {
			t.Fatalf("external compilation replaced existing shared bytes: %q", cached)
		}
	}
}

func newSharedBinaryDomainFixture(t *testing.T) (*Result, goListPackage) {
	t.Helper()
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	workspace := t.TempDir()
	writeBuildTestFile(t, workspace, "go.mod", "module example.test/app\n\ngo 1.27.0\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\nfunc main() {}\n")
	result := &Result{AppRoot: workspace, Dir: workspace, SourceFiles: []string{"scenery_internal_main/main.go"},
		Target:                  &compiler.GoBuildTarget{Name: "dev", Role: "development", Context: gotarget.Context{ModuleRoot: workspace, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}},
		Contract:                &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "sha256:" + strings.Repeat("a", 64)}},
		ImplementationRevisions: map[string]string{"dev": "sha256:" + strings.Repeat("b", 64)},
		RuntimeLinkerMetadata:   map[string]string{"scenery.sh/runtime.linkedGoTarget": "dev"}}
	result.GoEnvironment = gotarget.Environment(result.Target.Context)
	if err := refreshWorkspaceBuildIdentity(result); err != nil {
		t.Fatal(err)
	}
	mainPackage := goListPackage{Dir: filepath.Join(workspace, "scenery_internal_main"), ImportPath: "example.test/app/scenery_internal_main", GoFiles: []string{"main.go"},
		Module: &goListModule{Path: "example.test/app", Dir: workspace, GoMod: filepath.Join(workspace, "go.mod")}}
	return result, mainPackage
}

func setSharedBinaryDomainDiscovery(t *testing.T, result *Result, packages ...goListPackage) {
	t.Helper()
	var output bytes.Buffer
	for _, pkg := range packages {
		if err := json.NewEncoder(&output).Encode(pkg); err != nil {
			t.Fatal(err)
		}
	}
	previous := runGoInputList
	t.Cleanup(func() { runGoInputList = previous })
	runGoInputList = func(context.Context, string, []string, ...string) ([]byte, error) { return output.Bytes(), nil }
	var err error
	result.BuildInput, err = buildInputManifest(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
}

type buildInputInfoWithoutChangeTime struct{ os.FileInfo }

func (info buildInputInfoWithoutChangeTime) Sys() any {
	device, inode := buildInputFileIdentity(info.FileInfo)
	return struct{ Dev, Ino uint64 }{device, inode}
}

func withoutBuildInputChangeTime(t *testing.T) {
	t.Helper()
	previous := buildInputLstat
	t.Cleanup(func() { buildInputLstat = previous })
	buildInputLstat = func(path string) (os.FileInfo, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		return buildInputInfoWithoutChangeTime{info}, nil
	}
}
