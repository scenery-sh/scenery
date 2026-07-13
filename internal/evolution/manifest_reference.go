package evolution

import (
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func LoadManifestReference(reference string) (*Manifest, error) {
	info, err := os.Stat(reference)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		result, compileErr := compiler.Compile(reference)
		if compileErr != nil {
			return nil, compileErr
		}
		if !result.Valid() {
			return nil, fmt.Errorf("%s does not compile to a valid manifest", reference)
		}
		return result.Manifest, nil
	}
	b, err := os.ReadFile(filepath.Clean(reference))
	if err != nil {
		return nil, err
	}
	manifest, err := graph.DecodeManifest(b)
	if err != nil {
		return nil, fmt.Errorf("%s is not a current scenery manifest or compile envelope: %w", reference, err)
	}
	return manifest, nil
}
