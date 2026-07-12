package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "scenery-build-test-cache-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create build test cache: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "set build test cache: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
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
	writeBuildTestFile(t, root, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRoot(t)+"\n")
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
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, appDir, "go.mod", goMod)
	writeBuildTestFile(t, appDir, "svc/api.go", serviceSource)

	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", goMod)
	writeBuildTestFile(t, workspace, "svc/api.go", serviceSource)
	writeBuildTestFile(t, workspace, "svc/scenery.gen.go", "package svc\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	depFingerprint, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	sourceFiles := []string{"go.mod", "svc/api.go"}
	generatedFiles := []string{"scenery_internal_main/main.go", "svc/scenery.gen.go"}
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
	}
	if err := saveBuildState(workspace, buildState{
		Version:                   buildStateVersion,
		DependencyFingerprint:     depFingerprint,
		SourceMetadataFingerprint: sourceMetadataFingerprint,
		GeneratorFingerprint:      generatorFingerprint,
		BuildFingerprint:          buildFingerprint,
		GraphFingerprint:          graphFingerprint,
		Metadata:                  append([]byte(nil), result.Metadata...),
		APIEncoding:               append([]byte(nil), result.APIEncoding...),
		SourceStamps:              sourceStamps,
		GeneratedFiles:            generatedFiles,
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
