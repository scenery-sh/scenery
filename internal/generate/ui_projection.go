package generate

import (
	"scenery.sh/internal/uigen"
)

func renderUICatalog(appRoot, root string) ([]generatedFile, error) {
	files, err := uigen.RenderCatalog(appRoot, root)
	return uiProjectionFiles(files), err
}

func uiProjectionFiles(files []uigen.File) []generatedFile {
	result := make([]generatedFile, len(files))
	for i, file := range files {
		result[i] = generatedFile{Path: file.Path, Bytes: file.Bytes}
	}
	return result
}
