package evolution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
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
	var manifest Manifest
	if err := json.Unmarshal(b, &manifest); err == nil && manifest.Kind == graph.ManifestKind && manifest.SchemaRevision == graph.ManifestSchemaRevision && manifest.SpecRevision == string(spec.CurrentRevision()) {
		return &manifest, nil
	}
	var envelope struct {
		Data struct {
			Manifest *Manifest `json:"manifest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil || envelope.Data.Manifest == nil {
		return nil, fmt.Errorf("%s is not a scenery manifest or compile envelope", reference)
	}
	return envelope.Data.Manifest, nil
}
