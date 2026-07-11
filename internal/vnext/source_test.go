package vnext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStaticCompositeValuesKeepBooleanLiteralsAsValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, []byte(`module "house" {
  source = "./house"
  inputs = {
    enabled = true
    gateway = http_gateway.public_api
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	if hasErrors(diagnostics) || source == nil || len(source.Blocks) != 1 {
		t.Fatalf("parse = %#v diagnostics %#v", source, diagnostics)
	}
	inputs, _ := source.Blocks[0].Attributes["inputs"].Value.(map[string]any)
	if inputs["enabled"] != true || refString(inputs["gateway"]) != "http_gateway.public_api" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestPrimitiveConstructorsNormalizeBeforeIR(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, []byte(`deployment "test" {
  environment = "test"
  timeout = duration("1h30m")
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
	value := blockSpec(source.Blocks[0])["timeout"]
	want := map[string]any{"$scalar": "duration", "nanoseconds": "5400000000000"}
	if !semanticEqual(value, want) {
		t.Fatalf("value = %#v", value)
	}
}

func TestBooleanKeywordsAreLiteralsRatherThanReferences(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, []byte(`enum "mode" {
  open = true
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
	expression := source.Blocks[0].Attributes["open"]
	if expression.Kind != "literal" || expression.Value != true {
		t.Fatalf("open = %#v", expression)
	}
}

func TestDurableRuntimeKeysRemainTypedInputExpressions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.package.scn")
	sourceText := `operation "run" {
  idempotency {
    mode = "keyed"
    key = [input.queue_key, input.request_id]
  }
}
execution "run_durable" {
  concurrency {
    key = input.queue_key
    limit = 1
  }
}
`
	if err := os.WriteFile(path, []byte(sourceText), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
	if diagnostics := validateStaticExpressions([]*Source{source}); hasErrors(diagnostics) {
		t.Fatalf("static-expression diagnostics: %#v", diagnostics)
	}
	idempotency := blockSpec(source.Blocks[0])["idempotency"].(map[string]any)
	keys := idempotency["key"].([]any)
	if len(keys) != 2 || expressionText(keys[0]) != "input.queue_key" || expressionText(keys[1]) != "input.request_id" {
		t.Fatalf("idempotency keys = %#v", keys)
	}
	concurrency := blockSpec(source.Blocks[1])["concurrency"].(map[string]any)
	if expressionText(concurrency["key"]) != "input.queue_key" {
		t.Fatalf("concurrency key = %#v", concurrency["key"])
	}
}

func TestExactNumbersNormalizeToTaggedSemanticScalars(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, []byte(`provider "numbers" {
  config = {
    large    = 9007199254740993
    negative = -42
    price    = -123.4500
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	config := blockSpec(source.Blocks[0])["config"].(map[string]any)
	wants := map[string]any{
		"large":    map[string]any{"$scalar": "int", "value": "9007199254740993"},
		"negative": map[string]any{"$scalar": "int", "value": "-42"},
		"price":    map[string]any{"$scalar": "decimal", "coefficient": "-12345", "scale": "2"},
	}
	for name, want := range wants {
		if !semanticEqual(config[name], want) {
			t.Errorf("%s = %#v, want %#v", name, config[name], want)
		}
	}
}

func TestDeclaredWorkspaceEntriesIncludeViewImplementationFiles(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "house")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "queries"), 0o755); err != nil {
		t.Fatal(err)
	}
	queryPath := filepath.Join(moduleRoot, "queries", "scene.sql")
	if err := os.WriteFile(queryPath, []byte("select 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := &Source{
		Path: filepath.Join(moduleRoot, "scenery.package.scn"), Relative: "house/scenery.package.scn",
		Blocks: []*Block{{Type: "view", Labels: []string{"scenes"}, Blocks: []*Block{{Type: "implementation", Attributes: map[string]Expression{"file": {Kind: "literal", Value: "queries/scene.sql"}}}}}},
	}
	entries, err := declaredWorkspaceEntries(root, []*Source{source})
	if err != nil {
		t.Fatal(err)
	}
	if string(entries["house/queries/scene.sql"]) != "select 1\n" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestWorkspaceRevisionIncludesLockfileAndExplicitRevisionInputs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte("lock-v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := &Block{Type: "workspace", Blocks: []*Block{{Type: "revision_input", Labels: []string{"go_workspace"}, Attributes: map[string]Expression{
		"paths": {Kind: "literal", Value: []any{"go.work", "go.work.sum"}}, "optional": {Kind: "literal", Value: true},
	}}}}
	source := &Source{Path: filepath.Join(root, "scenery.scn"), Relative: "scenery.scn", Bytes: []byte("workspace {}\n"), Blocks: []*Block{workspace}}
	first, err := computeWorkspaceRevision(root, []*Source{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte("lock-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := computeWorkspaceRevision(root, []*Source{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("lockfile change did not change workspace revision")
	}
	entries, err := declaredWorkspaceEntries(root, []*Source{source})
	if err != nil {
		t.Fatal(err)
	}
	if string(entries["go.work"]) != "go 1.25\n" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestWorkspaceRevisionExclusionsWinAndSymlinkedInputsFail(t *testing.T) {
	root := t.TempDir()
	implementation := filepath.Join(root, "house")
	if err := os.MkdirAll(implementation, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(implementation, "service.go"), []byte("package house\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := &Block{Type: "workspace", Blocks: []*Block{{Type: "implementation_root", Labels: []string{"house"}, Attributes: map[string]Expression{
		"path": {Kind: "literal", Value: "house"}, "revision_include": {Kind: "literal", Value: []any{"**/*.go"}}, "revision_exclude": {Kind: "literal", Value: []any{"**/*.go"}},
	}}}}
	source := &Source{Path: filepath.Join(root, "scenery.scn"), Relative: "scenery.scn", Blocks: []*Block{workspace}}
	entries, err := declaredWorkspaceEntries(root, []*Source{source})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := entries["house/service.go"]; exists {
		t.Fatal("excluded implementation file entered workspace revision")
	}

	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "input.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	workspace.Blocks = []*Block{{Type: "revision_input", Labels: []string{"bad"}, Attributes: map[string]Expression{"paths": {Kind: "literal", Value: []any{"input.txt"}}}}}
	if _, err := declaredWorkspaceEntries(root, []*Source{source}); err == nil {
		t.Fatal("expected symlink revision input failure")
	}
}

func TestWorkspaceGlobDialectRejectsHostExtensionsAndEmbeddedDoubleStar(t *testing.T) {
	for _, pattern := range []string{"[ab].go", `foo\\bar.go`, "foo**bar.go", "foo/**bar", "foo//bar"} {
		if err := validateWorkspaceGlobs([]string{pattern}); err == nil {
			t.Errorf("accepted invalid workspace glob %q", pattern)
		}
	}
	for _, pattern := range []string{"*.go", "src/?.go", "**/*.go", "assets/**"} {
		if err := validateWorkspaceGlobs([]string{pattern}); err != nil {
			t.Errorf("rejected valid workspace glob %q: %v", pattern, err)
		}
	}
	if !matchesAnyGlob([]string{"src/?.go"}, "src/é.go") || matchesAnyGlob([]string{"src/?.go"}, "src/ab.go") {
		t.Fatal("? did not match exactly one Unicode scalar")
	}
	if !matchesAnyGlob([]string{"src/**/test?.go"}, "src/a/b/test1.go") || matchesAnyGlob([]string{"src/**/test?.go"}, "src/a/test12.go") {
		t.Fatal("** complete-segment matching is incorrect")
	}
}
