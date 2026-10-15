package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/lifecycle"
)

func TestGenerationPlanOmitsUnselectedTypeScript(t *testing.T) {
	t.Parallel()
	for _, cachedOnly := range []bool{false, true} {
		root := t.TempDir()
		source := "application \"selected\" {}\n"
		if cachedOnly {
			source += `workspace { managed_generated_roots = ["clients"] }
http_gateway "api" {
 exposure = "internet"
 base_path = "/"
 cors = std.cors.none
 trusted_proxies = std.trusted_proxies.none
 forwarded = std.forwarded_headers.reject
}

typescript_client "api" {
 gateways = [http_gateway.api]
 output_root = "clients"
 package = "@example/api"
 module = "esm"
 runtime = "fetch"
}
`
		}
		if err := os.WriteFile(filepath.Join(root, "app.scn"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		compiled, err := compiler.Compile(root)
		if err != nil || !compiled.Valid() {
			t.Fatalf("compile: %v %#v", err, compiled)
		}
		op, err := newGenerationOperation(compiled, "", false)
		if err != nil {
			t.Fatal(err)
		}
		op.cachedOnly = cachedOnly
		nodes, err := op.plan(false)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range nodes {
			if strings.HasPrefix(string(node.ID), "typescript.") {
				t.Fatalf("unselected TypeScript producer/check present: %s", node.ID)
			}
		}
	}
}

func TestGenerationPlanReportsGoFilesAndDestinationScope(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	compiled, err := compiler.Compile(root)
	if err != nil || !compiled.Valid() {
		t.Fatalf("compile: %v", err)
	}
	op, err := newGenerationOperation(compiled, "contracts", false)
	if err != nil {
		t.Fatal(err)
	}
	op.goRenderer = func(*compiler.Result) ([]generatedFile, error) {
		t.Fatal("inspection rendered Go bytes")
		return nil, nil
	}
	nodes, err := op.plan(false)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, node := range nodes {
		if node.ID == "go.render" {
			for _, output := range node.Outputs {
				paths[output.Path] = true
			}
		}
	}
	for _, path := range []string{"house/scenerycontract", "house/scenerycontract/types.gen.go", "house/scenerycontract/contract.gen.go", "house/scenerycontract/scenery.package-generated.json"} {
		if !paths[path] {
			t.Fatalf("missing Go destination %s: %v", path, paths)
		}
	}
	for _, path := range []string{"house/scenerycontract", "house/scenerycontract/nested"} {
		other := lifecycle.Node{ID: "other", Kind: "render", Cache: lifecycle.Reusable, Outputs: []lifecycle.Reference{{Name: "other", Kind: "output_root", Path: path, Ownership: lifecycle.Managed}}}
		if _, err := lifecycle.New(append(nodes, other)); err == nil {
			t.Fatalf("Go scope collision admitted at %s", path)
		}
	}
}

func TestAllTypeScriptLibraryGenerationDoesNotSelectGo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	goPath := filepath.Join(root, "house/scenerycontract/contract.gen.go")
	authored := []byte("package scenerycontract\nconst authored = true\n")
	if err := os.MkdirAll(filepath.Dir(goPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goPath, authored, 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(root)
	if err != nil || !compiled.Valid() {
		t.Fatalf("compile: %v", err)
	}
	files, err := renderTypeScriptClientFiles(compiled, "")
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, files)
	for _, check := range []bool{false, true} {
		for _, fromSnapshot := range []bool{false, true} {
			var result GenerateResult
			if fromSnapshot {
				result, err = GenerateTypeScriptClientsFromResult(compiled, "", check)
			} else {
				result, err = GenerateTypeScriptClients(root, "", check)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range result.Checked {
				if !strings.HasPrefix(path, "clients/generated/public_api/") {
					t.Fatalf("TypeScript API checked unrelated output: %s", path)
				}
			}
		}
	}
	current, err := os.ReadFile(goPath)
	if err != nil || string(current) != string(authored) {
		t.Fatalf("TypeScript API changed authored Go bytes: %v", err)
	}
}
