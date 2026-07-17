package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectUIJSONContract(t *testing.T) {
	root := writeInspectUIFixture(t)
	var output bytes.Buffer
	if err := runSceneryInspect([]string{"ui", "--app-root", root, "--frontend", "web", "-o", "json"}, &output); err != nil {
		t.Fatalf("inspect ui: %v\n%s", err, output.String())
	}

	var payload inspectUIResponse
	if err := decodeCLIJSON(output.Bytes(), &payload); err != nil {
		t.Fatalf("decode inspect ui: %v\n%s", err, output.String())
	}
	if payload.Kind != inspectUIKind || payload.SchemaRevision != newCLIPayloadIdentity(inspectUIKind).SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if payload.App.Name != "ui-fixture" || len(payload.Frontends) != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	frontend := payload.Frontends[0]
	if frontend.Name != "web" || frontend.DesignSystem != "astryx" || len(frontend.Files) != 1 {
		t.Fatalf("frontend = %#v", frontend)
	}
	if frontend.Files[0].Path != "src/page.tsx" || frontend.Files[0].Score != 5 {
		t.Fatalf("file = %#v", frontend.Files[0])
	}
	schemaPath := filepath.Join("..", "..", "docs", "schemas", "scenery.inspect.ui.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) > 0 {
		t.Fatalf("schema diagnostics = %v\n%s", diagnostics, output.String())
	}
}

func TestInspectUIHumanAndEmptyApp(t *testing.T) {
	root := writeInspectUIFixture(t)
	var output bytes.Buffer
	if err := runSceneryInspect([]string{"ui", "--app-root", root}, &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Frontend: legacy (apps/legacy)  design system: none",
		"No Astryx, @scenery/ui, or StyleX token imports found.",
		"Frontend: web (apps/web)  design system: astryx",
		"SCORE",
		"src/page.tsx",
		"TOTAL (1 files)",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("human output missing %q:\n%s", expected, output.String())
		}
	}

	empty := t.TempDir()
	writeInspectUIFile(t, empty, ".scenery.json", `{
  "name": "empty",
  "frontends": {},
  "envs": {"local": {"default": true}}
}`)
	output.Reset()
	if err := runSceneryInspect([]string{"ui", "--app-root", empty, "-o", "json"}, &output); err != nil {
		t.Fatal(err)
	}
	var payload inspectUIResponse
	if err := decodeCLIJSON(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Frontends == nil || len(payload.Frontends) != 0 {
		t.Fatalf("frontends = %#v, want non-nil empty", payload.Frontends)
	}
}

func TestInspectUIRejectsInvalidFrontendFlag(t *testing.T) {
	root := writeInspectUIFixture(t)
	var output bytes.Buffer
	if err := runSceneryInspect([]string{"ui", "--app-root", root, "--frontend", "missing"}, &output); err == nil {
		t.Fatal("unknown frontend succeeded")
	}
	if err := runSceneryInspect([]string{"app", "--app-root", root, "--frontend", "web", "-o", "json"}, &output); err == nil {
		t.Fatal("--frontend on inspect app succeeded")
	}
}

func writeInspectUIFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeInspectUIFile(t, root, ".scenery.json", `{
  "name": "ui-fixture",
  "frontends": {
    "web": {"root": "apps/web"},
    "legacy": {"root": "apps/legacy"}
  },
  "envs": {"local": {"default": true}}
}`)
	writeInspectUIFile(t, root, "apps/web/src/page.tsx", `
import * as stylex from "@stylexjs/stylex";
import { Text } from "@astryxdesign/core/Text";
import { colorVars } from "@astryxdesign/core/theme/tokens.stylex";
const styles = stylex.create({ root: { color: colorVars["--color-text"], width: "2rem" } });
export const Page = () => <main style={{ display: "block" }}><Text xstyle={styles.root}>Hi</Text></main>;
`)
	writeInspectUIFile(t, root, "apps/web/src/generated/owned.tsx", `<div />`)
	writeInspectUIFile(t, root, "apps/legacy/src/page.tsx", `export const Page = () => <div />;`)
	return root
}

func writeInspectUIFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
