package compiler

import (
	"os"
	"strings"
	"testing"
)

// TestSpecDocumentsTypeSystem keeps SPEC section 3 aligned with the types the
// compiler accepts, so the normative spelling list cannot drift again.
func TestSpecDocumentsTypeSystem(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../docs/spec/SPEC.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, _ := strings.Cut(string(data), "## 3.")
	section, _, _ = strings.Cut(section, "## 4.")
	names := []string{"list(T)", "set(T)", "map(T)", "optional(T)", "nullable(T)"}
	for name := range primitiveTypes {
		names = append(names, name)
	}
	for name := range deploymentOnlyTypes {
		names = append(names, name)
	}
	for _, name := range names {
		if !strings.Contains(section, "`"+name+"`") {
			t.Errorf("SPEC section 3 does not document %s", name)
		}
	}
}
