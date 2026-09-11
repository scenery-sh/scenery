package generate

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestPrivateGoProjectionPreservesGenerationIdentity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	read := func() []generatedFile {
		t.Helper()
		files, err := renderExpectedGoApplicationFiles(result, newProjectionInput(result))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	first := read()
	if len(first) == 0 || !reflect.DeepEqual(first, read()) {
		t.Fatal("unchanged private projection differs")
	}
	first[0].Bytes[0] ^= 1
	if reflect.DeepEqual(first, read()) {
		t.Fatal("caller mutation leaked into the projection cache")
	}
	result.ImplementationRevisions = map[string]string{"development": "sha256:" + strings.Repeat("a", 64)}
	implementation := read()
	composition := generatedSourceWithSuffix(implementation, "composition/composition.gen.go")
	if !strings.Contains(composition, `RuntimeRevision: "`+result.ImplementationRevisions["development"]+`"`) {
		t.Fatal("private cache hid a changed implementation identity")
	}
	result.WorkspaceRevision = "sha256:" + strings.Repeat("b", 64)
	workspace := read()
	if reflect.DeepEqual(implementation, workspace) {
		t.Fatal("private cache hid a changed assistant MCP workspace identity")
	}
	want, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil || !reflect.DeepEqual(workspace, want) {
		t.Fatalf("cached projection differs from fresh rendering: %v", err)
	}
}
