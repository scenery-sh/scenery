package generate

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestBuildGoWorkspaceSharesPublicBytesAndRejectsDrift(t *testing.T) {
	root := t.TempDir()
	writeMinimalNativeGenerationFixture(t, root)
	authored := filepath.Join(root, "house", "unverified.go")
	if err := os.WriteFile(authored, []byte("package house\nfunc invalid( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	// Durable publication is exercised by the native process probe. This root
	// tests exact shared bytes and snapshot validation without filesystem sync.
	public, err := renderExpectedGoPackageFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, public)
	projection, err := PrepareBuildGoWorkspace(result)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RenderGoWorkspaceFiles(result)
	if err != nil || !reflect.DeepEqual(projection.Files, want) {
		t.Fatalf("private projection differs: %v", err)
	}
	publicCount, privateCount := 0, 0
	for relative, expected := range projection.Files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if strings.HasPrefix(relative, "internal/scenerygen/") {
			privateCount++
			if !os.IsNotExist(err) {
				t.Fatalf("private composition was published: %s", relative)
			}
			continue
		}
		publicCount++
		if err != nil || !bytes.Equal(data, expected) {
			t.Fatalf("public bytes differ: %s %v", relative, err)
		}
	}
	if publicCount == 0 || privateCount == 0 {
		t.Fatal("fixture did not exercise public and private artifacts")
	}
	appendFixtureFile(t, filepath.Join(root, "house", testPackageFilename), "\n")
	if _, err := PrepareBuildGoWorkspace(result); err == nil || !strings.Contains(err.Error(), "revision_conflict") {
		t.Fatalf("stale operation snapshot accepted: %v", err)
	}
}

func TestGoWorkspaceProjectionRejectsInvalidContract(t *testing.T) {
	for _, result := range []*compiler.Result{nil, {}, {ContractStatus: "invalid"}} {
		if _, err := PrepareBuildGoWorkspace(result); err == nil {
			t.Fatal("invalid contract accepted")
		}
	}
}

func TestRenderedGoWorkspaceFilesKeepsIndependentBytes(t *testing.T) {
	root := t.TempDir()
	source := []byte("package generated\n")
	files := []generatedFile{{Path: filepath.Join(root, "generated.go"), Bytes: source}, {Path: filepath.Join(root, "retired.go"), Remove: true}}
	rendered, err := renderedGoWorkspaceFiles(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 1 || string(rendered["generated.go"]) != string(source) {
		t.Fatalf("workspace projection = %#v", rendered)
	}
	rendered["generated.go"][0] = 'X'
	if source[0] != 'p' {
		t.Fatal("workspace mutation changed verification bytes")
	}
	if _, err := renderedGoWorkspaceFiles(root, append(files, files[0])); err == nil {
		t.Fatal("duplicate artifact accepted")
	}
	if _, err := renderedGoWorkspaceFiles(root, []generatedFile{{Path: filepath.Join(root, "..", "outside.go")}}); err == nil {
		t.Fatal("escaping artifact accepted")
	}
}
