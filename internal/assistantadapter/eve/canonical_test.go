package eve

import (
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
		"not canonicalized":    canonicalTestModule(root, `[]`, dynamic),
		"static scenery":       string(CanonicalizeServerModule([]byte(canonicalTestModule(root, `[{"connectionName":"scenery","url":"http://127.0.0.1:1"}]`, dynamic)), root)),
		"no scenery":           string(CanonicalizeServerModule([]byte(canonicalTestModule(root, `[]`, `[]`)), root)),
		"duplicated scenery":   string(CanonicalizeServerModule([]byte(canonicalTestModule(root, `[]`, `[{"slug":"scenery"},{"slug":"scenery"}]`)), root)),
		"no embedded manifest": "export {};",
	} {
		if err := ValidateCanonicalServerModule([]byte(module)); err == nil {
			t.Fatalf("%s: module accepted", name)
		}
	}
}
