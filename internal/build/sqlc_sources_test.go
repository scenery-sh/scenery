package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestOwnedSQLCSourcesAreTheClaimedGoFilesPresentInTheCheckout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(relative, contents string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if sources, err := ownedSQLCSources(root); err != nil || len(sources) != 0 {
		t.Fatalf("a checkout without a receipt owns %v: %v", sources, err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	receipt, err := json.Marshal(map[string]any{
		"kind": "scenery.sqlc-generated", "schema_revision": compiler.SQLCReceiptSchemaRevision(), "owner": root,
		"input_revision": digest, "tools": map[string]string{"sqlc": digest}, "roots": []string{"db/gen"},
		"files": map[string]string{"db/gen/query.sql.go": digest, "db/gen/removed.go": digest, "db/gen/notes.txt": digest},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(compiler.SQLCReceiptPath, string(receipt))
	write("db/gen/query.sql.go", "package gen\n")
	write("db/gen/notes.txt", "not a build input\n")
	write("db/gen/authored.go", "package gen\n")
	sources, err := ownedSQLCSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || string(sources["db/gen/query.sql.go"]) != "package gen\n" {
		t.Fatalf("owned sources = %v", sources)
	}
	if err := os.Remove(filepath.Join(root, "db/gen/query.sql.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "db/gen/authored.go"), filepath.Join(root, "db/gen/query.sql.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedSQLCSources(root); err == nil {
		t.Fatal("a symbolic link was read as owned SQLC output")
	}
}
