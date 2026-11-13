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

func TestMissingCachedTypeScriptClientIsMaterializedOnce(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	path := filepath.Join(root, testAppFilename)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	joined := 0
	// A client kept in source is the application's to generate.
	if missing, err := MaterializeMissingCachedTypeScriptClients(t.Context(), root, func() { joined++ }); err != nil || len(missing) != 0 {
		t.Fatalf("source client: %v, %v", missing, err)
	}
	cached := bytes.Replace(source, []byte("typescript_client \"public_api\" {"), []byte("typescript_client \"public_api\" {\n  materialization = \"cache\""), 1)
	if err := os.WriteFile(path, cached, 0o644); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(root, ".scenery", "gen", "typescript", "public_api", "client.ts")
	missing, err := MaterializeMissingCachedTypeScriptClients(t.Context(), root, func() { joined++ })
	if err != nil || len(missing) != 1 || missing[0] != "public_api" {
		t.Fatalf("first start: %v, %v", missing, err)
	}
	written, err := os.ReadFile(client)
	if err != nil || joined != 1 {
		t.Fatalf("first start joined %d reads: %v", joined, err)
	}
	// An existing client is not rewritten, even when its bytes are stale.
	if err := os.WriteFile(client, append(written, []byte("// stale\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	// An existing client is left to the build, which refreshes it.
	missing, err = MaterializeMissingCachedTypeScriptClients(t.Context(), root, func() { joined++ })
	again, readErr := os.ReadFile(client)
	if err != nil || readErr != nil || len(missing) != 0 || joined != 1 || !bytes.HasSuffix(again, []byte("// stale\n")) {
		t.Fatalf("later start: %v, %v, %v, joined %d", missing, err, readErr, joined)
	}
	// A client declared in another root source is found as the compiler finds it.
	if err := os.RemoveAll(filepath.Join(root, ".scenery", "gen")); err != nil {
		t.Fatal(err)
	}
	block := cached[bytes.Index(cached, []byte("typescript_client \"public_api\" {")):]
	depth, end := 0, 0
	for i, c := range block {
		if c == '{' {
			depth++
		} else if c == '}' {
			if depth--; depth == 0 {
				end = i + 1
				break
			}
		}
	}
	if err := os.WriteFile(path, bytes.Replace(cached, block[:end], nil, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clients.scn"), block[:end], 0o644); err != nil {
		t.Fatal(err)
	}
	if missing, err := MaterializeMissingCachedTypeScriptClients(t.Context(), root, nil); err != nil || len(missing) != 1 {
		t.Fatalf("client in another root source: %v, %v", missing, err)
	}
	if _, err := os.Stat(client); err != nil {
		t.Fatalf("client in another root source was not written: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "clients.scn")); err != nil {
		t.Fatal(err)
	}
	// A source that does not compile is reported by the build, not here.
	if err := os.RemoveAll(filepath.Join(root, ".scenery", "gen")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(cached, []byte("\nunknown_block \"x\" {}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if missing, err := MaterializeMissingCachedTypeScriptClients(t.Context(), root, func() { joined++ }); err != nil || len(missing) != 1 {
		t.Fatalf("invalid source: %v, %v", missing, err)
	}
	if _, err := os.Stat(client); !os.IsNotExist(err) {
		t.Fatalf("invalid source wrote a client: %v", err)
	}
}
