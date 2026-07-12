package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rewriteFixtureSceneryReplace(t *testing.T, root string) {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sceneryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(sceneryRoot), 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
