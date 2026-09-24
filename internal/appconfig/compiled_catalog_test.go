package appconfig

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

// TestCatalogFromCompiledApplication derives the catalog from a real
// compilation of the native fixture with configurable inputs added.
func TestCatalogFromCompiledApplication(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join("..", "compiler", "testdata", "native")
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(root, relative), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, relative), data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	edit := func(name, old, replacement string) {
		t.Helper()
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), old) {
			t.Fatalf("edit %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("app.scn", "gateway = http_gateway.public_api", "gateway = http_gateway.public_api\n    process_concurrency = 4")
	edit(filepath.Join("house", "package.scn"), "input \"gateway\" {\n  type = resource_ref(\"http_gateway\")\n}", `input "gateway" {
  type = resource_ref("http_gateway")
}

input "weather_pack_root" {
  type  = optional(host_path)
  phase = "deployment"
}

input "process_concurrency" {
  type    = uint32
  phase   = "deployment"
  default = 2
}

input "provider_token" {
  type      = resource_ref("secret")
  phase     = "deployment"
  sensitive = true
}`)
	edit(filepath.Join("house", "package.scn"), "  implementation {\n    constructor = \"NewService\"\n  }", `  implementation {
    constructor = "NewService"
  }

  config {
    weather_pack_root   = var.weather_pack_root
    process_concurrency = var.process_concurrency
    provider_token      = var.provider_token
  }`)
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v %#v", err, result.Diagnostics)
	}
	catalog, err := BuildCatalog(result.Manifest, FrameworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, input := range catalog.Inputs {
		keys = append(keys, input.Key)
		if input.Key == AssistantProviderKey {
			continue
		}
		if len(input.Consumers) != 1 || input.Consumers[0].Service != "house/service/house" {
			t.Fatalf("%s consumers = %#v", input.Key, input.Consumers)
		}
	}
	if strings.Join(keys, ",") != "assistant.openai_api_key,house.process_concurrency,house.provider_token,house.weather_pack_root" {
		t.Fatalf("keys = %v", keys)
	}
	if concurrency, _ := catalog.Lookup("house.process_concurrency"); string(concurrency.Default) != "4" {
		t.Fatalf("application baseline = %s", concurrency.Default)
	}
}
