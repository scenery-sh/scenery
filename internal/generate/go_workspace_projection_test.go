package generate

import (
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestGoWorkspaceProjectionRejectsInvalidContract(t *testing.T) {
	for _, result := range []*compiler.Result{nil, {}, {ContractStatus: "invalid"}} {
		if _, err := PrepareGoWorkspace(result); err == nil {
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
