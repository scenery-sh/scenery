package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/parse"
)

func TestPrepareAndCompileWriteLatestBuildManifest(t *testing.T) {
	t.Parallel()

	appDir := copyBuildFixture(t, "native")

	model, err := parse.Analyze(appDir, "nativeapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "nativeapp"})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after prepare: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after prepare")
	}
	if manifest.Kind != latestBuildKind || manifest.SchemaRevision != machine.ArtifactSchemaRevision(latestBuildSchemaDescriptor) {
		t.Fatalf("artifact identity = %#v", manifest.ArtifactIdentity)
	}
	if manifest.Build.Phase != "prepared" {
		t.Fatalf("phase after prepare = %q", manifest.Build.Phase)
	}
	if manifest.Build.BuildStateExists {
		t.Fatal("did not expect build state after prepare")
	}

	if err := os.WriteFile(result.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write reusable binary: %v", err)
	}
	result.NeedsTidy = false
	result.ReuseCompiled = true
	if err := Compile(result); err != nil {
		t.Fatalf("compile reusable result: %v", err)
	}
	manifest, ok, err = ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after compile: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after compile")
	}
	if manifest.Build.Phase != "compiled" {
		t.Fatalf("phase after compile = %q", manifest.Build.Phase)
	}
	if !manifest.Build.BinaryExists {
		t.Fatal("expected binary to exist after compile")
	}
	if !manifest.Build.BuildStateExists {
		t.Fatal("expected build state to exist after compile")
	}
}

func TestCompileRealGoBuildSmoke(t *testing.T) {
	t.Parallel()

	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "scenery-app"),
		NeedsTidy:      true,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"scenery_internal_main/main.go"},
	}
	if err := os.WriteFile(result.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stale build output: %v", err)
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.NeedsTidy {
		t.Fatal("expected Compile to clear NeedsTidy")
	}
	if _, err := os.Stat(result.Binary); err != nil {
		t.Fatalf("expected real build binary: %v", err)
	}
	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after compile: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after compile")
	}
	if manifest.Build.Phase != "compiled" || !manifest.Build.BinaryExists || !manifest.Build.BuildStateExists {
		t.Fatalf("manifest build = %+v", manifest.Build)
	}
}

func TestCompileRunsTidyOnlyAfterBuildFailure(t *testing.T) {
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var commands []string
	tidied := false
	old := runGo
	runGo = func(_ context.Context, _ string, _ []string, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			tidied = true
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./scenery_internal_main" {
			if !tidied {
				return fmt.Errorf("go.mod updates needed")
			}
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "scenery-app"),
		NeedsTidy:      true,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"scenery_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := strings.Join(commands, "|"), "build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main|mod tidy|build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main"; got != want {
		t.Fatalf("go commands = %q, want %q", got, want)
	}
	if result.NeedsTidy {
		t.Fatal("expected Compile to clear NeedsTidy")
	}
}

func TestCompilePassesConfiguredGoBuildFlags(t *testing.T) {
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var got []string
	old := runGo
	runGo = func(_ context.Context, _ string, _ []string, args ...string) error {
		got = append([]string(nil), args...)
		out, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	}
	t.Cleanup(func() { runGo = old })

	result := &Result{
		AppRoot:      appDir,
		AppName:      "smoke",
		Dir:          workspace,
		Binary:       filepath.Join(workspace, "scenery-app"),
		GoBuildFlags: []string{"-tags=roofmapnet_native", " ", "-gcflags=all=-N -l"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := []string{"build", "-tags=roofmapnet_native", "-gcflags=all=-N -l", "-buildvcs=false", "-o", result.Binary, "./scenery_internal_main"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("go build args = %#v, want %#v", got, want)
	}
}

func TestCompilePrunesStaleFingerprintBinariesAfterSuccess(t *testing.T) {
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	const previousFingerprint = "11111111111111111111111111111111"
	if err := saveBuildState(workspace, buildState{
		Version:          buildStateVersion,
		BuildFingerprint: previousFingerprint,
	}); err != nil {
		t.Fatal(err)
	}
	previousBinary := filepath.Join(workspace, workspaceBinaryName(appDir, previousFingerprint))
	staleBinary := filepath.Join(workspace, "scenery-app-2222222222222222")
	unmanagedPrefixFile := filepath.Join(workspace, "scenery-app-not-a-fingerprint")
	for _, path := range []string{previousBinary, staleBinary, unmanagedPrefixFile} {
		if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	restore := SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
		}
		return os.WriteFile(output, []byte("current"), 0o755)
	})
	t.Cleanup(restore)

	const currentFingerprint = "33333333333333333333333333333333"
	result := &Result{
		AppRoot:          appDir,
		AppName:          "smoke",
		Dir:              workspace,
		Binary:           filepath.Join(workspace, workspaceBinaryName(appDir, currentFingerprint)),
		BuildFingerprint: currentFingerprint,
		SourceFiles:      []string{"go.mod"},
		GeneratedFiles:   []string{"scenery_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	for _, path := range []string{result.Binary, previousBinary, unmanagedPrefixFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain: %v", filepath.Base(path), err)
		}
	}
	if _, err := os.Stat(staleBinary); !os.IsNotExist(err) {
		t.Fatalf("expected stale fingerprint binary to be removed, stat err = %v", err)
	}
}

func TestCompileFailureDoesNotPruneFingerprintBinaries(t *testing.T) {
	workspace := t.TempDir()
	staleBinary := filepath.Join(workspace, "scenery-app-1111111111111111")
	if err := os.WriteFile(staleBinary, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := SetGoRunnerForTesting(func(_ context.Context, _ string, _ ...string) error {
		return fmt.Errorf("build failed")
	})
	t.Cleanup(restore)

	result := &Result{
		Dir:    workspace,
		Binary: filepath.Join(workspace, "scenery-app-2222222222222222"),
	}
	if err := Compile(result); err == nil {
		t.Fatal("expected Compile() to fail")
	}
	if _, err := os.Stat(staleBinary); err != nil {
		t.Fatalf("expected stale binary to remain after failed build: %v", err)
	}
}

func TestCompileRetriesTidyWhenBuildReportsStaleGoMod(t *testing.T) {
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var commands []string
	tidied := false
	old := runGo
	runGo = func(_ context.Context, _ string, _ []string, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			tidied = true
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./scenery_internal_main" {
			if !tidied {
				return fmt.Errorf("go build -buildvcs=false failed: exit status 1\ngo: updates to go.mod needed; to update it:\n\tgo mod tidy")
			}
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "scenery-app"),
		NeedsTidy:      false,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"scenery_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := strings.Join(commands, "|"), "build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main|mod tidy|build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main"; got != want {
		t.Fatalf("go commands = %q, want %q", got, want)
	}
}
