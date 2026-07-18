package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scenery.sh/internal/spec"
)

func TestManifestSchemaRevisionMatchesCheckedSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "docs", "schemas", "scenery.library.artifact.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := spec.SchemaDocumentRevision(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != ManifestSchemaRevision {
		t.Fatalf("schema revision = %s, want %s", ManifestSchemaRevision, revision)
	}
}

func TestReadManifestArtifactRejectsUnsupportedAndTamperedIdentity(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "library.json")
	write := func(manifest Manifest) {
		t.Helper()
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(Manifest{Kind: ManifestKind, SchemaRevision: ManifestSchemaRevision, Library: "demo", Version: "v1.0.0", ABIHash: "sha256:abi", Artifacts: map[string]Artifact{}})
	if _, _, _, err := readManifestArtifact(manifestPath); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("unsupported platform error = %v", err)
	}
	write(Manifest{Kind: ManifestKind, SchemaRevision: ManifestSchemaRevision, Library: "demo", Version: "v1.0.0", ABIHash: "sha256:abi", Artifacts: map[string]Artifact{
		runtime.GOOS + "_" + runtime.GOARCH: {GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Path: "demo", SHA256: "bad"},
	}})
	if _, _, _, err := readManifestArtifact(manifestPath); err == nil || !strings.Contains(err.Error(), "invalid platform identity") {
		t.Fatalf("invalid digest error = %v", err)
	}
}

func TestReadManifestArtifactRejectsPathOutsideManifestDirectory(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "library.json")
	glibcFloor := ""
	if runtime.GOOS == "linux" {
		glibcFloor = "2.36"
	}
	manifest := Manifest{
		Kind: ManifestKind, SchemaRevision: ManifestSchemaRevision, Library: "demo",
		Version: "v1.0.0", ABIHash: "sha256:abi",
		Artifacts: map[string]Artifact{
			runtime.GOOS + "_" + runtime.GOARCH: {
				GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Path: "../demo",
				SHA256: "sha256:" + strings.Repeat("0", 64), GoVersion: runtime.Version(),
				GlibcFloor: glibcFloor,
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readManifestArtifact(manifestPath); err == nil || !strings.Contains(err.Error(), "normalized relative path") {
		t.Fatalf("path error = %v", err)
	}
}

func TestLoaderRequiresUniqueOperationSymbols(t *testing.T) {
	if _, err := NewLoader("demo", "sha256:abi", []string{"Run", "Run"}); err == nil {
		t.Fatal("NewLoader accepted duplicate operation symbols")
	}
	loader, err := NewLoader("demo", "sha256:abi", []string{"Second", "First"})
	if err != nil {
		t.Fatal(err)
	}
	if got := loader.operations; len(got) != 2 || got[0] != "First" || got[1] != "Second" {
		t.Fatalf("operations = %#v", got)
	}
}

func TestExternalLibraryArtifact(t *testing.T) {
	manifestPath := os.Getenv("SCENERY_TEST_LIBRARY_MANIFEST")
	symbol := os.Getenv("SCENERY_TEST_LIBRARY_SYMBOL")
	if manifestPath == "" || symbol == "" {
		t.Skip("set SCENERY_TEST_LIBRARY_MANIFEST and SCENERY_TEST_LIBRARY_SYMBOL")
	}
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	loader, err := NewLoader(manifest.Library, manifest.ABIHash, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Swap(manifestPath); err != nil {
		t.Fatal(err)
	}
	versions := loader.Versions()
	if len(versions) != 1 || !versions[0].Current {
		t.Fatalf("loaded versions = %#v", versions)
	}
	_, callErr := loader.Call(symbol, []byte(os.Getenv("SCENERY_TEST_LIBRARY_INPUT")))
	expectedError := os.Getenv("SCENERY_TEST_LIBRARY_EXPECT_ERROR")
	if expectedError == "" {
		if callErr != nil {
			t.Fatal(callErr)
		}
	} else if callErr == nil || !strings.Contains(callErr.Error(), expectedError) {
		t.Fatalf("call error = %v, want substring %q", callErr, expectedError)
	}
}
