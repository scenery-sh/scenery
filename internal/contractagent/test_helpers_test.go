package contractagent

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

var DiagnosticCatalog = graph.DiagnosticCatalog

type ContextBundle = graph.ContextBundle

func Compile(root string) (*Result, error) {
	if root == "testdata/house" {
		root = "../compiler/testdata/house"
	}
	return compiler.Compile(root)
}

func CoreSchema(kind string) (map[string]any, bool) { return spec.CoreSchema(kind) }

var resourceSchemas = spec.ResourceSchemas()

func resourceCreateSchemaRevisions() []string { return spec.ResourceCreateSchemaRevisions() }

func isCanonicalSHA256Digest(value string) bool { return graph.IsCanonicalSHA256Digest(value) }

func renameReceiptDigest(receipt evolution.RenameReceipt) string {
	return evolution.RenameReceiptDigest(receipt)
}

func containsExactString(values []string, want string) bool { return containsString(values, want) }

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	if source == filepath.Join("testdata", "house") {
		source = filepath.Join("..", "compiler", "testdata", "house")
	}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
