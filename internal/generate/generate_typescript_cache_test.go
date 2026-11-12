package generate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestTypeScriptCacheMaterializationDoesNotAffectCheck(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	path := filepath.Join(root, testAppFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("typescript_client \"public_api\" {"), []byte("typescript_client \"public_api\" {\n  materialization = \"cache\""), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderCachedTypeScriptClients(result)
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, files)
	cacheFile := filepath.Join(root, ".scenery", "gen", "typescript", "public_api", "client.ts")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".scenery", "gen")); err != nil {
		t.Fatal(err)
	}
	result, err = compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check requires disposable TypeScript cache: %v %#v", err, result.Diagnostics)
	}
}

func TestExplicitTypeScriptGenerationOwnsItsCacheTarget(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	path := filepath.Join(root, testAppFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("typescript_client \"public_api\" {"), []byte("typescript_client \"public_api\" {\n  materialization = \"cache\""), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := renderCachedTypeScriptClients(result)
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, cached)
	outputRoot := filepath.Join(root, ".scenery", "gen", "typescript", "public_api")
	retired := filepath.Join(outputRoot, "retired.ts")
	descriptorPath := filepath.Join(outputRoot, typescriptGeneratedDescriptorName)
	for _, selector := range []string{"", "typescript_client.public_api"} {
		files, err := renderTypeScriptClientFiles(result, selector)
		if err != nil {
			t.Fatalf("explicit generation %q over its own cache target: %v", selector, err)
		}
		if len(files) != len(cached) {
			t.Fatalf("explicit generation %q rendered %d files, cache sync %d", selector, len(files), len(cached))
		}
	}
	if err := os.WriteFile(retired, []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := renderTypeScriptClientFiles(result, ""); err != nil {
		t.Fatalf("an unowned sibling outside the expected output must not block generation: %v", err)
	}
	if err := os.Remove(descriptorPath); err != nil {
		t.Fatal(err)
	}
	if _, err := renderTypeScriptClientFiles(result, ""); err == nil || !strings.Contains(err.Error(), "exists without verified ownership") {
		t.Fatalf("output without its descriptor must stay protected, got %v", err)
	}
}
