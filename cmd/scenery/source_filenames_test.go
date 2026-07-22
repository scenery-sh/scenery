package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/scn"
)

const (
	testAppFilename     = scn.AppFilename
	testPackageFilename = scn.PackageFilename
	testAppLockFilename = scn.AppLockFilename
)

func TestContractCompileReportsLegacyRootFilename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, scn.LegacyAppFilename), []byte(`application "legacy" {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	err := runContractCompile(&output, []string{"--app-root", root, "-o", "json", "--non-interactive", "--quiet"})
	if err == nil {
		t.Fatal("compile unexpectedly accepted retired root filename")
	}
	for _, want := range []string{"SCN1021", scn.LegacyAppFilename, scn.AppFilename} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %s", want, output.String())
		}
	}
}
