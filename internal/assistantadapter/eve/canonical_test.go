package eve

import (
	"encoding/json"
	"strings"
	"testing"
)

func canonicalTestModule(root, connections, dynamic string) string {
	return "const built = '" + root + "/.eve/builds/Q9_z/server.mjs';\nconst manifest = {\n" +
		`"agentRoot":"` + root + `/agent",` + "\n" + `"appRoot":"` + root + `",` + "\n" +
		`"connections":` + connections + `,` + "\n" + `"dynamicConnections":` + dynamic + "\n};\nexport { manifest };"
}

func TestCanonicalServerModuleIsLocationIndependentAndResolvesItsGatewayAtRuntime(t *testing.T) {
	const root = "/private/overlay-1"
	dynamic := `[{"slug":"scenery","logicalPath":"connections/scenery.ts"}]`
	canonical := CanonicalizeServerModule([]byte(canonicalTestModule(root, `[]`, dynamic)), root)
	if strings.Contains(string(canonical), root) || strings.Contains(string(canonical), "Q9_z") {
		t.Fatalf("canonical module still names its build: %s", canonical)
	}
	if err := ValidateCanonicalServerModule(canonical); err != nil {
		t.Fatalf("canonical module refused: %v", err)
	}
	for name, module := range map[string]string{
		"not canonicalized":     canonicalTestModule(root, `[]`, dynamic),
		"static scenery":        string(CanonicalizeServerModule([]byte(canonicalTestModule(root, `[{"connectionName":"scenery","url":"http://127.0.0.1:1"}]`, dynamic)), root)),
		"no scenery":            string(CanonicalizeServerModule([]byte(canonicalTestModule(root, `[]`, `[]`)), root)),
		"duplicated scenery":    string(CanonicalizeServerModule([]byte(canonicalTestModule(root, `[]`, `[{"slug":"scenery"},{"slug":"scenery"}]`)), root)),
		"no embedded manifest":  "export {};",
		"duplicate manifests":   string(canonical) + string(canonical),
		"unterminated manifest": "const manifest = {\n",
		"invalid manifest":      "const manifest = {\ninvalid\n};",
	} {
		if err := ValidateCanonicalServerModule([]byte(module)); err == nil {
			t.Fatalf("%s: module accepted", name)
		}
	}
}

func TestCanonicalArtifactManifestKeepsTheGeneratedRuntimeConnection(t *testing.T) {
	const root = "/private/overlay"
	const logicalPath = "connections/scenery.ts"
	manifest := map[string]any{
		"appRoot":     root,
		"agentRoot":   root + "/agent",
		"connections": []any{},
		"definitions": []map[string]string{{
			"kind": "connection", "slug": "scenery", "logicalPath": logicalPath, "sourceId": logicalPath, "sourceKind": "module",
		}},
		"bindings": map[string]any{logicalPath: map[string]any{
			"backing": map[string]string{"kind": "filesystem", "sourcePath": root + "/agent/" + logicalPath},
			"usage":   map[string]bool{"runtimeEntry": true},
		}},
	}
	module := func() []byte {
		artifacts, err := json.Marshal(map[string]any{"manifest": manifest})
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := json.Marshal(string(artifacts))
		if err != nil {
			t.Fatal(err)
		}
		return CanonicalizeServerModule(append(append([]byte("const artifacts = JSON.parse("), embedded...), []byte(");\n")...), root)
	}
	if err := ValidateCanonicalServerModule(module()); err != nil {
		t.Fatal(err)
	}
	manifest["connections"] = []map[string]string{{"slug": "scenery", "url": "http://127.0.0.1:1"}}
	if err := ValidateCanonicalServerModule(module()); err == nil {
		t.Fatal("a static connection was accepted")
	}
}
