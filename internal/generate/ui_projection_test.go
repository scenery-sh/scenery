package generate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestOptionalUIRetirementPreservesCommonClientAndAuthoredSlots(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	target := typescriptTargets(result.Manifest.Resources, "public_api")[0]
	output := filepath.Join(root, "clients/generated/public_api")
	plain, err := renderTypeScriptTargetWithCatalog(result, target, output, nil)
	if err != nil {
		t.Fatal(err)
	}
	target.Spec = cloneMapValue(target.Spec)
	target.Spec["react"] = map[string]any{}
	withUI, err := renderTypeScriptTargetWithCatalog(result, target, output, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client.ts", "types.ts", "runtime.ts", "index.ts"} {
		before := []byte(generatedSourceWithSuffix(plain, "/"+name))
		after := []byte(generatedSourceWithSuffix(withUI, "/"+name))
		// The UI has its own index; compare the common root file explicitly.
		for _, file := range withUI {
			if file.Path == filepath.Join(output, name) {
				after = file.Bytes
			}
		}
		if len(before) == 0 || !bytes.Equal(before, after) {
			t.Fatalf("optional UI changed common %s", name)
		}
	}
	for _, file := range plain {
		if strings.Contains(file.Path, "/react/") || bytes.Contains(file.Bytes, []byte("from \"react\"")) {
			t.Fatalf("plain client includes UI: %s", file.Path)
		}
	}
	writeRenderedFixture(t, withUI)
	slot := filepath.Join(output, "react", "application-slot.tsx")
	const authored = "export const ApplicationSlot = () => null;\n"
	if err := os.WriteFile(slot, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	retired, err := includeStaleGeneratedFiles(output, plain, map[string]bool{typescriptGeneratedDescriptorName: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, file := range retired {
		if file.Remove {
			removed++
			if !strings.Contains(file.Path, "/react/") || file.Path == slot {
				t.Fatalf("UI retirement selected an unrelated file: %s", file.Path)
			}
		}
	}
	if removed == 0 {
		t.Fatal("UI retirement removed no owned output")
	}
	if _, err := inspectGeneratedFiles(root, retired); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(slot); err != nil || string(got) != authored {
		t.Fatalf("application slot changed: %q, %v", got, err)
	}
}

func TestUIRenderFailurePublishesNoneOfSharedArtifactSet(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"ui-failure","envs":{"local":{"default":true,"ui_catalog":"broken-ui"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "broken-ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(root, testAppFilename)
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	appSource = bytes.Replace(appSource, []byte(`typescript_client "public_api" {`), []byte("typescript_client \"public_api\" {\n  react { tsconfig = \"tsconfig.json\" }"), 1)
	if err := os.WriteFile(appPath, appSource, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v; diagnostics: %v", err, result.Diagnostics)
	}
	shared := filepath.Join(root, "house/scenerycontract/types.gen.go")
	_, err = GenerateSnapshotWithContext(t.Context(), result, "", false)
	if err == nil || !strings.Contains(err.Error(), "not a UI catalog root") {
		t.Fatalf("expected UI render failure: %v", err)
	}
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Fatalf("shared output published after failed UI render: %v", err)
	}
}
