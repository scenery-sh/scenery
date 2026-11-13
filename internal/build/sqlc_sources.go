package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/compiler"
)

// ownedSQLCSources returns the Go build inputs among the outputs that the SQLC
// ownership receipt claims. The compiler leaves those paths out of authored
// source membership because `scenery generate sqlc` owns them, and no build
// step renders them, so the build takes their published bytes from the
// checkout as generated workspace files.
func ownedSQLCSources(appRoot string) (map[string][]byte, error) {
	receipt, err := compiler.ReadSQLCArtifactReceipt(appRoot)
	if err != nil {
		return nil, err
	}
	sources := make(map[string][]byte)
	for rel := range receipt.Files {
		rel = filepath.ToSlash(rel)
		if !isGoWorkspaceSourceFile(rel) {
			continue
		}
		path := filepath.Join(appRoot, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("owned SQLC output %s is not a regular file", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sources[rel] = data
	}
	return sources, nil
}
