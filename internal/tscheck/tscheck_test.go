package tscheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckClassifiesGeneratedAndApplicationDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "tsgo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$SCENERY_TSCHECK_OUTPUT\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outputRoot := filepath.Join(root, "generated", "client")
	files := []File{{Path: filepath.Join(outputRoot, "react", "orders.generated.tsx"), Bytes: []byte("export {}\n")}}

	t.Setenv("SCENERY_TSCHECK_OUTPUT", filepath.Join(filepath.Dir(outputRoot), ".scenery-tscheck-test", "react", "orders.generated.tsx")+"(1,1): error TS2322")
	err := Check(context.Background(), binary, root, outputRoot, "tsconfig.json", files)
	classified, ok := err.(*Error)
	if !ok || classified.Code != "SCN6320" {
		t.Fatalf("generated diagnostic = %#v", err)
	}

	t.Setenv("SCENERY_TSCHECK_OUTPUT", filepath.Join(root, "components", "broken.tsx")+"(1,1): error TS2304")
	err = Check(context.Background(), binary, root, outputRoot, "tsconfig.json", files)
	classified, ok = err.(*Error)
	if !ok || classified.Code != "SCN6321" {
		t.Fatalf("application diagnostic = %#v", err)
	}
}

func TestCheckRequiresNodeModulesBeforeStartingChecker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Check(context.Background(), "/missing", root, filepath.Join(root, "generated"), "tsconfig.json", nil)
	classified, ok := err.(*Error)
	if !ok || classified.Code != "SCN6322" {
		t.Fatalf("readiness diagnostic = %#v", err)
	}
}
