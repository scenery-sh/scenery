package tscheck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckClassifiesGeneratedAndApplicationDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "tsgo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$SCENERY_TSCHECK_OUTPUT\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outputRoot := filepath.Join(root, "generated", "client")
	files := []File{{Path: filepath.Join(outputRoot, "react", "orders.generated.tsx"), Bytes: []byte("export {}\n")}}

	t.Setenv("SCENERY_TSCHECK_OUTPUT", filepath.Join(filepath.Dir(outputRoot), ".scenery-tscheck-test", "react", "orders.generated.tsx")+"(1,1): error TS2322")
	err := Check(context.Background(), binary, root, outputRoot, "tsconfig.json", files)
	classified, ok := err.(*Error)
	if !ok || classified.Code != "SCN6320" {
		t.Fatalf("generated diagnostic = %#v", err)
	}

	t.Setenv("SCENERY_TSCHECK_OUTPUT", filepath.Join(root, "components", "broken.tsx")+"(1,1): error TS2304")
	err = Check(context.Background(), binary, root, outputRoot, "tsconfig.json", files)
	classified, ok = err.(*Error)
	if !ok || classified.Code != "SCN6321" {
		t.Fatalf("application diagnostic = %#v", err)
	}
}

func TestCheckRequiresNodeModulesBeforeStartingChecker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Check(context.Background(), "/missing", root, filepath.Join(root, "generated"), "tsconfig.json", nil)
	classified, ok := err.(*Error)
	if !ok || classified.Code != "SCN6322" {
		t.Fatalf("readiness diagnostic = %#v", err)
	}
}

func TestCheckRedirectsSceneryUIAliasToStagedCatalog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "app", "tsconfig.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"compilerOptions":{"paths":{"@/*":["./src/*"],"@scenery/ui":["./old/index.ts"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	capturePath := filepath.Join(root, "staged-config.json")
	binary := filepath.Join(root, "tsgo")
	script := `#!/bin/sh
if [ "$1" = "--showConfig" ]; then
  printf '%s\n' '{"compilerOptions":{"paths":{"@/*":["./src/*"],"@scenery/ui":["./old/index.ts"]}}}'
  exit 0
fi
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--project" ]; then
    cp "$2" "$SCENERY_CAPTURE_CONFIG"
    exit 0
  fi
  shift
done
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCENERY_CAPTURE_CONFIG", capturePath)
	outputRoot := filepath.Join(root, "app", "src", "generated", "scenery")
	files := []File{
		{Path: filepath.Join(outputRoot, "react", "scenery-ui", "index.ts"), Bytes: []byte("export {}\n")},
		{Path: filepath.Join(outputRoot, "react", "scenery-ui", "tokens.stylex.ts"), Bytes: []byte("export {}\n")},
	}
	if err := Check(context.Background(), binary, root, outputRoot, filepath.ToSlash(filepath.Join("app", "tsconfig.json")), files); err != nil {
		t.Fatal(err)
	}

	encoded, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		CompilerOptions struct {
			Paths map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(encoded, &config); err != nil {
		t.Fatal(err)
	}
	if got := config.CompilerOptions.Paths["@scenery/ui"]; len(got) != 1 || !strings.Contains(got[0], ".scenery-tscheck-") || strings.Contains(got[0], "/old/") {
		t.Fatalf("@scenery/ui paths = %#v", got)
	}
	if got := config.CompilerOptions.Paths["@/*"]; len(got) != 1 || got[0] != filepath.ToSlash(filepath.Join(root, "app", "src", "*")) {
		t.Fatalf("@/* paths = %#v", got)
	}
}
