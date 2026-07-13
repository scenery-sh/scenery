package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestReferenceRejectsHeuristicEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(`{"data":{"manifest":{"resources":[]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifestReference(path)
	if err == nil || !strings.Contains(err.Error(), "unexpected manifest document kind") {
		t.Fatalf("LoadManifestReference error = %v", err)
	}
}
