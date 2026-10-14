package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
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
