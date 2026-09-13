//go:build scenery_build_cache_integration

package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharedBinaryCrossProcessOwnedExternalInputsAreSnapshotBound(t *testing.T) {
	withoutBuildInputChangeTime(t)
	for _, selection := range []string{"tracked_go", "ignored_go", "temporary_embed", "native"} {
		t.Run(selection, func(t *testing.T) {
			appRoot := t.TempDir()
			workspace := filepath.Join(appRoot, ".scenery", "build", "workspace")
			dependency := t.TempDir()
			writeOwnedModuleFixture(t, appRoot, workspace, dependency)
			input := prepareOwnedModuleCompilerInput(t, dependency, selection)
			writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\nimport (\"fmt\"; dependency \"example.test/dependency\")\nfunc main() { fmt.Print(dependency.Value) }\n")

			sources, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(sources) != 1 {
				t.Fatalf("owned sources = %#v", sources)
			}
			if err := os.WriteFile(input.path, input.replacement, 0o644); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(t.TempDir(), "application")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := runRealGo(ctx, workspace, nil, "build", "-buildvcs=false", "-o", binary, "./scenery_internal_main"); err != nil {
				t.Fatal(err)
			}
			if err := input.restore(); err != nil {
				t.Fatal(err)
			}
			input.assertRestored(t)
			if err := VerifyOwnedGoModuleSourcesContext(ctx, sources); err != nil {
				t.Fatalf("restored origin no longer matches captured A: %v", err)
			}
			output, err := exec.CommandContext(ctx, binary).CombinedOutput()
			if err != nil || string(output) != "A" {
				t.Fatalf("compiler did not consume captured A: output=%q err=%v", output, err)
			}
		})
	}
}

func prepareOwnedModuleCompilerInput(t *testing.T, dependency, selection string) sharedBinaryNativeInput {
	t.Helper()
	if selection != "native" {
		return prepareSharedBinaryNativeInput(t, dependency, selection)
	}
	writeBuildTestFile(t, dependency, "dep.go", `package dependency

/*
#include "value.h"
*/
import "C"

var Value = C.GoString(C.value())
`)
	input := sharedBinaryNativeInput{
		path:        filepath.Join(dependency, "value.h"),
		original:    []byte("static const char* value(void) { return \"A\"; }\n"),
		replacement: []byte("static const char* value(void) { return \"B\"; }\n"),
	}
	if err := os.WriteFile(input.path, input.original, 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	input.before, err = buildInputLstat(input.path)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestSharedBinaryCrossProcessOwnedExternalInputRejectsPersistentDrift(t *testing.T) {
	withoutBuildInputChangeTime(t)
	appRoot := t.TempDir()
	workspace := filepath.Join(appRoot, ".scenery", "build", "workspace")
	dependency := t.TempDir()
	writeOwnedModuleFixture(t, appRoot, workspace, dependency)
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"A\"\n")
	sources, err := bindOwnedGoModuleSources(context.Background(), appRoot, workspace, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"B\"\n")
	if err := VerifyOwnedGoModuleSourcesContext(context.Background(), sources); err == nil || !strings.Contains(err.Error(), "discard this candidate") {
		t.Fatalf("persistent origin drift was accepted: %v", err)
	}
}
