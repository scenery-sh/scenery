package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Diagnostics reach agents and humans as single-line build errors, so the
// readable source path must ride on the diagnostic itself; the hashed
// source id in Range is not reversible by consumers.
func TestCompileDiagnosticsCarrySourcePath(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	target := filepath.Join(root, "house", "package.scn")
	source, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	// A single-line block with two attributes is the SCN1000 shape that
	// motivated this test: the error surfaced with no file location.
	broken := append(source, []byte("\nsetting \"bad\" { label = \"a\" hidden = true }\n")...)
	if err := os.WriteFile(target, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected compile diagnostics for broken package source")
	}
	found := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity != "error" {
			continue
		}
		found = true
		if diagnostic.Range != nil && diagnostic.Path != "house/package.scn" {
			t.Fatalf("diagnostic %s path = %q, want house/package.scn", diagnostic.Code, diagnostic.Path)
		}
	}
	if !found {
		t.Fatal("no error diagnostics reported")
	}
}
