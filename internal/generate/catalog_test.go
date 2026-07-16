package generate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryOwnedUICatalogContainsTablePageContractAndTokens(t *testing.T) {
	root := t.TempDir()
	files, err := renderUICatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	content := map[string]string{}
	for _, file := range files {
		content[filepath.ToSlash(strings.TrimPrefix(file.Path, root+string(filepath.Separator)))] = string(file.Bytes)
	}
	for path, fragment := range map[string]string{
		"package.json":                      `"name": "@scenery/ui"`,
		"pages/TablePage/TablePage.tsx":     "pagination",
		"pages/TablePage/contract-types.ts": "defineTablePageSlots",
		"pages/TablePage/theme.css":         "--scenery-ui-background",
	} {
		if !strings.Contains(content[path], fragment) {
			t.Errorf("catalog %s missing %q", path, fragment)
		}
	}
}
