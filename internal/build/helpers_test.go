package build

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/filemeta"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "scenery-build-test-cache-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create build test cache: %v\n", err)
		os.Exit(1)
	}
	restore := devcache.SetRoot(cacheDir)
	code := m.Run()
	restore()
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
}

func TestPatchGoModDataPreservesPublishedDependencyForTrimpathBinary(t *testing.T) {
	input := []byte("module example.com/buildtest\n\ngo 1.26.3\n\nrequire scenery.sh v0.3.3\n")
	got, err := patchGoModData(input, "scenery.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "scenery.sh v0.3.3") {
		t.Fatalf("published requirement was not preserved:\n%s", got)
	}
	if strings.Contains(string(got), "replace scenery.sh") {
		t.Fatalf("trimpath framework root produced invalid replacement:\n%s", got)
	}
}

func newBuildTestApp(t *testing.T) string {
	t.Helper()
	return newBuildTestAppNamed(t, "")
}

func newBuildTestAppNamed(t *testing.T, base string) string {
	t.Helper()
	root := t.TempDir()
	if strings.TrimSpace(base) != "" {
		root = filepath.Join(root, base)
	}
	writeBuildTestFile(t, root, "go.mod", "module example.com/buildtest\n\ngo 1.27.0\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRoot(t)+"\n")
	writeBuildTestFile(t, root, ".scenery.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, root, "svc/api.go", `package svc

import "context"

func Hello(ctx context.Context) error { return nil }
`)
	return root
}

func newCachedBuildTestWorkspace(t *testing.T, graphFingerprint string) (string, *Result) {
	t.Helper()
	appDir := t.TempDir()

	const goMod = "module example.com/buildtest\n\ngo 1.26.3\n"
	const serviceSource = `package svc

import "context"

func Hello(ctx context.Context) error { return nil }
`
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"buildtest","envs":{"local":{"default":true}}}`)
	writeBuildTestFile(t, appDir, "app.scn", `application "buildtest" {}
go_module "application" {
  root = "."
  import_path = "example.com/buildtest"
}
go_toolchain "application" { version = "1.27.0" }
go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
  cgo = "disabled"
}
`)
	writeBuildTestFile(t, appDir, "go.mod", goMod)
	writeBuildTestFile(t, appDir, "svc/api.go", serviceSource)

	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", goMod)
	writeBuildTestFile(t, workspace, ".scenery.json", `{"name":"buildtest","envs":{"local":{"default":true}}}`)
	writeBuildTestFile(t, workspace, "svc/api.go", serviceSource)
	writeBuildTestFile(t, workspace, "svc/scenery.gen.go", "package svc\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	depFingerprint, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	sourceFiles := []string{".scenery.json", "go.mod", "svc/api.go"}
	generatedFiles := []string{"scenery_internal_main/main.go", "svc/scenery.gen.go"}
	generatedStamps, err := artifactPathStamps(workspace, generatedFiles)
	if err != nil {
		t.Fatal(err)
	}
	buildFingerprint, err := workspaceBuildFingerprint(workspace, nil, sourceFiles, generatedFiles)
	if err != nil {
		t.Fatal(err)
	}
	sourceStamps := make(map[string]SourceStamp, len(sourceFiles))
	for _, rel := range sourceFiles {
		info, err := os.Stat(filepath.Join(appDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		sourceStamps[rel] = sourceStampFromInfo(info)
	}
	sourceMetadataFingerprint := sourceStampsFingerprint(sourceStamps)
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	result := &Result{
		AppRoot:                   appDir,
		AppName:                   "buildtest",
		Dir:                       workspace,
		Binary:                    filepath.Join(workspace, workspaceBinaryName(appDir, buildFingerprint)),
		NeedsTidy:                 false,
		DependencyFingerprint:     depFingerprint,
		SourceMetadataFingerprint: sourceMetadataFingerprint,
		GeneratorFingerprint:      generatorFingerprint,
		BuildFingerprint:          buildFingerprint,
		GraphFingerprint:          graphFingerprint,
		Metadata:                  json.RawMessage(`{"ok":true}`),
		APIEncoding:               json.RawMessage(`{"api":"v1"}`),
		SourceFiles:               append([]string(nil), sourceFiles...),
		SourceStamps:              sourceStamps,
		GeneratedFiles:            append([]string(nil), generatedFiles...),
		GeneratedStamps:           generatedStamps,
	}
	contract, err := compiler.Compile(appDir)
	if err != nil {
		t.Fatal(err)
	}
	target, err := compiler.ResolveGoBuildTarget(contract, "", "development")
	if err != nil {
		t.Fatal(err)
	}
	result.Contract, result.Target = contract, &target
	preparationFingerprint, err := PreparationFingerprint(appcfg.Config{Name: "buildtest"}, contract)
	if err != nil {
		t.Fatal(err)
	}
	result.PreparationFingerprint = preparationFingerprint
	result.BuildInput = newBuildInputManifest(target.Name, map[string]string{"fixture": "sha256:" + strings.Repeat("a", 64)})
	result.ImplementationRevisions, _ = compiler.ComputeImplementationRevisions(contract, map[string]string{target.Name: result.BuildInput.Digest})
	if err := writeRuntimeBundle(result); err != nil {
		t.Fatal(err)
	}
	if err := saveBuildState(workspace, buildState{
		Version:                   buildStateVersion,
		DependencyFingerprint:     depFingerprint,
		SourceMetadataFingerprint: sourceMetadataFingerprint,
		GeneratorFingerprint:      generatorFingerprint,
		PreparationFingerprint:    preparationFingerprint,
		BuildFingerprint:          buildFingerprint,
		GraphFingerprint:          graphFingerprint,
		Metadata:                  append([]byte(nil), result.Metadata...),
		APIEncoding:               append([]byte(nil), result.APIEncoding...),
		SourceStamps:              sourceStamps,
		GeneratedFiles:            generatedFiles,
		GeneratedStamps:           generatedStamps,
		PublicGeneratedStamps:     map[string]string{},
		CachedTypeScriptStamps:    map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	return appDir, result
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func writeBuildTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func useFakeGoRunner(t *testing.T) {
	t.Helper()
	old := runGo
	runGo = func(_ context.Context, _ string, _ []string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return nil
		}
		if out, ok := fakeGoBuildOutput(args); ok {
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })
}

func fakeGoBuildOutput(args []string) (string, bool) {
	if len(args) < 5 || args[0] != "build" || args[len(args)-1] != "./scenery_internal_main" {
		return "", false
	}
	for i := 1; i < len(args)-2; i++ {
		if args[i] == "-buildvcs=false" && args[i+1] == "-o" {
			return args[i+2], true
		}
	}
	return "", false
}

func prepareCompileTestResult(result *Result) *Result {
	result.Contract = &compiler.Result{}
	result.Target = &compiler.GoBuildTarget{}
	result.RuntimeLinkerMetadata = map[string]string{
		"scenery.sh/runtime.linkedContractRevision":       "test-contract",
		"scenery.sh/runtime.linkedImplementationRevision": "test-implementation",
		"scenery.sh/runtime.linkedBuildInputDigest":       "test-build-input",
		"scenery.sh/runtime.linkedGoTarget":               "test-target",
	}
	return result
}

// developmentProcessInputs projects the complete input set of one process
// entrypoint. Identification uses developmentProcessDigest; this projection
// serves diagnostics and tests that assert membership.
func (manifest *BuildInputManifest) developmentProcessInputs(name string) (*BuildInputManifest, string, error) {
	graph := manifest.processes
	if graph == nil {
		return nil, "", fmt.Errorf("build inputs do not include development process entrypoints")
	}
	main := graph.mains[name]
	if main == "" {
		return nil, "", fmt.Errorf("build inputs do not include development process %s", name)
	}
	entries := maps.Clone(graph.global)
	seen := map[string]bool{}
	pending := []string{main}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		maps.Copy(entries, graph.packages[current])
		pending = append(pending, graph.imports[current]...)
	}
	return newBuildInputManifest(manifest.Target, entries), main, nil
}

func buildInputFileChangeTime(info os.FileInfo) int64 {
	identity, known := filemeta.Read(info)
	if !known {
		return 0
	}
	return identity.ChangeTimeNano
}

func buildInputFileIdentity(info os.FileInfo) (uint64, uint64) {
	identity, _ := filemeta.Read(info)
	return identity.Device, identity.Inode
}

func buildInputManifestFromGoList(result *Result, output []byte) (*BuildInputManifest, error) {
	return buildInputManifestFromGoListObserved(context.Background(), result, output, nil)
}

func resetRetainedBuildInputGraphsForTesting() {
	retainedBuildInputGraphs.Range(func(key, _ any) bool {
		retainedBuildInputGraphs.Delete(key)
		return true
	})
}

func verifyCurrentSourceState(root, workspace string, state buildState) error {
	return verifyCurrentSourceStateWithSnapshot(root, workspace, state, nil)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (shouldSkipDir(rel) || shouldSkipRuntimeArtifactDir(rel)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && shouldSkipFile(rel) {
			return nil
		}
		if shouldSkipSymlink(path, d) {
			return nil
		}
		if !d.IsDir() && shouldSkipNonRegularFile(path, d) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func removeUnexpectedFilesFromLists(root string, sourceFiles, generatedFiles []string) error {
	return removeUnexpectedFilesFromListsObserved(root, sourceFiles, generatedFiles, nil)
}

func seedSceneryGoSum(workspaceDir, repoRoot string) error {
	return seedSceneryGoSumObserved(workspaceDir, repoRoot, nil)
}

// syncSourceFiles mirrors the app's source files into the workspace and
// returns the synced file list plus the stamp observed for every file. A file
// is skipped only when its current stat still matches the stamp recorded by a
// previous sync and the workspace copy exists; anything else is re-read and
// rewritten. The sync never trusts external change notifications, so changes
// missed by a file watcher (git pulls, edits while stopped) are still picked
// up here. Stamps are captured before reading the content: if a file changes
// mid-read, the recorded stamp is older than its stat and the next sync
// rewrites it. Files in skip are tracked and stamped but never written; their
// workspace content is owned by generated-file sync.
func syncSourceFiles(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}) ([]string, map[string]SourceStamp, error) {
	return syncSourceFilesWithSnapshot(root, appRoot, prevStamps, skip, nil)
}

func syncSourceFilesWithSnapshot(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}, snapshot *SourceSnapshot) ([]string, map[string]SourceStamp, error) {
	return syncSourceFilesWithSnapshotObserved(root, appRoot, prevStamps, skip, snapshot, nil)
}

// A cached executable is usable only after publishing the current public
// projection and proving its private workspace still contains those bytes.
// Cache metadata alone cannot establish this after deletion or a branch switch.
func refreshCachedGoProjection(appRoot string, result *Result, snapshot *SourceSnapshot, prepared ...*compiler.Result) (bool, error) {
	return refreshCachedGoProjectionContext(context.Background(), appRoot, result, snapshot, prepared...)
}

func syncGeneratedFiles(root, appRoot string, gen *codegen.Output, prev, sourceFiles []string) ([]string, error) {
	return syncGeneratedFilesObserved(root, appRoot, gen, prev, sourceFiles, nil)
}

// frameworkFingerprintFiles lists the framework source input files: Go and
// native sources, module files and the files their embed directives select.
func frameworkFingerprintFiles(repoRoot string) ([]string, error) {
	files, _, err := frameworkSourceFiles(repoRoot)
	return files, err
}

func LoadCachedGraph(appRoot string, cfg appcfg.Config, graphFingerprint string) (*CachedGraph, bool, error) {
	return loadCachedGraphContext(context.Background(), appRoot, cfg, graphFingerprint, "", nil)
}

func RefreshCachedWorkspace(appRoot string, result *Result) (bool, error) {
	return prepareCachedWorkspace(context.Background(), appRoot, appcfg.Config{}, result, nil, false)
}
