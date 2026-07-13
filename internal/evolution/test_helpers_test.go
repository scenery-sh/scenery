package evolution

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/scn"
)

func copyTree(t *testing.T, source, target string) {
	t.Helper()
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

func containsJSONText(data []byte, want string) bool {
	var value any
	if json.Unmarshal(data, &value) == nil {
		encoded, _ := json.Marshal(value)
		return strings.Contains(string(encoded), want)
	}
	return strings.Contains(string(data), want)
}

func expressionText(value any) string {
	if expression, ok := value.(map[string]any); ok {
		text, _ := expression["$expression"].(string)
		return strings.TrimSpace(text)
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func hasSCNErrors(diagnostics []scn.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func Compile(root string) (*Result, error) {
	if err := recoverInterruptedChangeTransaction(root, false); err != nil {
		return nil, err
	}
	return compiler.Compile(root)
}

func Check(root string) (*Result, error) {
	result, err := Compile(root)
	if err == nil {
		result.Diagnostics = append(result.Diagnostics, generate.Check(result)...)
	}
	return result, err
}

func writeNestedModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
