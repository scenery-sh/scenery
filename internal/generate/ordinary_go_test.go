package generate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
)

func TestOrdinaryGoProjectionExcludesPrivateComposition(t *testing.T) {
	root := t.TempDir()
	writeMinimalNativeGenerationFixture(t, root)
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := renderGoContractFiles(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated) != 3 {
		t.Fatalf("public projection = %#v", generated)
	}
	for _, file := range generated {
		if !strings.HasPrefix(file.Path, filepath.Join(root, "house/scenerycontract")+string(filepath.Separator)) {
			t.Fatalf("unexpected public artifact %s", file.Path)
		}
	}
}

func TestGoProjectionIdempotence(t *testing.T) {
	root := generatedOrdinaryGoFixture(t)
	path := filepath.Join(root, "house/scenerycontract/types.gen.go")
	stamp := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	generated, err := GenerateGoContracts(root, false)
	if err != nil || len(generated.Changed) != 0 {
		t.Fatalf("second generation = %#v, %v", generated, err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged output mtime changed: %v", err)
	}
}

func TestGoProjectionRejectsForeignOutput(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	path := filepath.Join(root, "house/scenerycontract/types.gen.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := []byte("package scenerycontract\n// authored file\n")
	if err := os.WriteFile(path, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err == nil || !strings.Contains(err.Error(), "without verified ownership") {
		t.Fatalf("foreign output was not rejected: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, foreign) {
		t.Fatal("foreign output changed")
	}
}

func TestGoProjectionRejectsEditedOwnedOutput(t *testing.T) {
	root := generatedOrdinaryGoFixture(t)
	path := filepath.Join(root, "house/scenerycontract/types.gen.go")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := append(contents, []byte("\n// manual edit\n")...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err == nil || !strings.Contains(err.Error(), "unverified generated descriptor") {
		t.Fatalf("edited output was not rejected: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, edited) {
		t.Fatal("edited output changed")
	}
}

func TestGoProjectionMissingReceiptDoesNotAuthorizeOverwrite(t *testing.T) {
	root := generatedOrdinaryGoFixture(t)
	if err := os.Remove(filepath.Join(root, "house/scenerycontract/scenery.package-generated.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err == nil || !strings.Contains(err.Error(), "without verified ownership") {
		t.Fatalf("missing receipt was not rejected: %v", err)
	}
}

func TestGoProjectionRestoresMissingAuthenticatedFile(t *testing.T) {
	root := generatedOrdinaryGoFixture(t)
	path := filepath.Join(root, "house/scenerycontract/types.gen.go")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	generated, err := GenerateGoContracts(root, false)
	if err != nil || len(generated.Changed) != 1 {
		t.Fatalf("missing-file recovery = %#v, %v", generated, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, before) {
		t.Fatal("missing file was not restored exactly")
	}
}

func TestGoProjectionRetirementPreservesUnownedFiles(t *testing.T) {
	root := generatedOrdinaryGoFixture(t)
	note := filepath.Join(root, "house/scenerycontract/notes.txt")
	if err := os.WriteFile(note, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(root, testAppFilename)
	source, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	source = bytes.Replace(source, []byte("module \"house\" {\n  source = \"./house\"\n}"), nil, 1)
	if err := os.WriteFile(appPath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "house/scenerycontract/types.gen.go")); !os.IsNotExist(err) {
		t.Fatalf("obsolete owned output remains: %v", err)
	}
	if got, _ := os.ReadFile(note); string(got) != "keep me\n" {
		t.Fatal("retirement removed unowned bytes")
	}
}

func TestGoProjectionRejectsNestedModule(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "house/go.mod"), []byte("module example.test/nativeapp/house\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err == nil || !strings.Contains(err.Error(), "undeclared module") {
		t.Fatalf("nested module was not rejected: %v", err)
	}
}

func generatedOrdinaryGoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderGoContractFiles(compiled)
	if err != nil {
		t.Fatal(err)
	}
	// Ownership tests start with renderer-authenticated bytes. Durable initial
	// publication and cross-process serialization are release-probe boundaries.
	writeRenderedFixture(t, files)
	return root
}

func writeRenderedFixture(t *testing.T, files []generatedFile) {
	t.Helper()
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.Path, file.Bytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
