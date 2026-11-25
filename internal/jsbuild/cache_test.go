package jsbuild

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestCacheFingerprintsInputsBelowSkippedDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	module := filepath.Join(root, "svc")
	files := []string{"svc/main.ts", "svc/target/rules.ts", "svc/dist/vendor.ts", "svc/lib/out/generated.ts", "svc/node_modules/pkg/index.js", "node_modules/shared/index.js"}
	inputs := make([]string, 0, len(files))
	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("export {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}
	dependencies := cacheDependencies(root, module, inputs)
	if want := []string{"node_modules/shared/index.js", "svc/dist/vendor.ts", "svc/lib/out/generated.ts", "svc/node_modules/pkg/index.js", "svc/target/rules.ts"}; !slices.Equal(dependencies, want) {
		t.Fatalf("dependencies = %v, want %v", dependencies, want)
	}
	service := Service{JSWorkerTarget: compiler.JSWorkerTarget{Directory: "svc", Workspace: "."}, Name: "svc/service/svc"}
	before, err := buildFingerprint(root, service, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range dependencies[1:] {
		if err := os.WriteFile(filepath.Join(root, name), []byte("export const changed = true;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		after, err := buildFingerprint(root, service, dependencies)
		if err != nil || after == before {
			t.Fatalf("editing %s left the fingerprint unchanged (%v)", name, err)
		}
		before = after
	}
}
