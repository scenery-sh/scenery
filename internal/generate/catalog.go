package generate

import (
	"embed"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed catalog
var uiCatalog embed.FS

func renderUICatalog(root string) ([]generatedFile, error) {
	var files []generatedFile
	err := fs.WalkDir(uiCatalog, "catalog", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := uiCatalog.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, generatedFile{Path: filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "catalog/"))), Bytes: data})
		return nil
	})
	return files, err
}
