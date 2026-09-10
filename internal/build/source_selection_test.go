package build

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceModulePreservesSelectedFramework(t *testing.T) {
	root := t.TempDir()
	input := []byte("module example.test/app\n\ngo 1.27.0\n\nrequire scenery.sh v0.3.3\nreplace scenery.sh => ../selected-framework\nreplace example.test/shared v1.0.0 => ./shared\nreplace example.test/remote => example.test/fork v1.2.3\n")
	got, err := patchGoModData(input, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"require scenery.sh v0.3.3",
		"replace scenery.sh => " + filepath.Join(filepath.Dir(root), "selected-framework"),
		"replace example.test/shared v1.0.0 => " + filepath.Join(root, "shared"),
		"replace example.test/remote => example.test/fork v1.2.3",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("workspace changed dependency selection; missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "v0.0.0") {
		t.Fatalf("workspace rewrote the selected version: %s", got)
	}
}
