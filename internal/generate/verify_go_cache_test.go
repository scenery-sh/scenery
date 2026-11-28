package generate

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

func TestGoVerifyIncrementalCacheReloadsOnlyChangedPackage(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.test/incverify\n\ngo 1.23\n",
		"a/a.go": "package a\nfunc Value() int { return 1 }\n",
		"b/b.go": "package b\nimport \"example.test/incverify/a\"\nfunc Use() int { return a.Value() }\n",
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	SetImplementationProofProducer("sha256:test-inc-producer")
	t.Cleanup(func() { SetImplementationProofProducer("") })

	var calls [][]string
	orig := analyzeTargetWithSnapshotFunc
	analyzeTargetWithSnapshotFunc = func(ctx context.Context, r, name string, overlay map[string][]byte, target gotarget.Context, patterns []string) (*model.App, map[string]parse.PackageSnapshot, string, error) {
		calls = append(calls, slices.Clone(patterns))
		return orig(ctx, r, name, overlay, target, patterns)
	}
	t.Cleanup(func() { analyzeTargetWithSnapshotFunc = orig })

	result := &compiler.Result{
		Root:     root,
		Manifest: &Manifest{Application: ApplicationIdentity{Name: "incverify"}},
	}
	targets := []compiler.GoBuildTarget{{
		Address: "app/go_target/main",
		Role:    "contract",
		Context: gotarget.Context{
			ModuleRoot: root,
			Patterns:   []string{"./..."},
		},
	}}

	// 1. Cold run loads full target (nil patterns).
	if diags := verifyGoTargetsIncremental(context.Background(), result, root, nil, targets); len(diags) != 0 {
		t.Fatalf("cold verify failed: %+v", diags)
	}
	if len(calls) != 1 || calls[0] != nil {
		t.Fatalf("cold calls = %#v, want [nil]", calls)
	}

	// 2. Body-only edit in a/a.go reloads ONLY ./a.
	if err := os.WriteFile(filepath.Join(root, "a", "a.go"), []byte("package a\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if diags := verifyGoTargetsIncremental(context.Background(), result, root, nil, targets); len(diags) != 0 {
		t.Fatalf("body-only verify failed: %+v", diags)
	}
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"./a"}) {
		t.Fatalf("body-only calls = %#v, want [[\"./a\"]]", calls)
	}

	// 3. Exported API edit in a/a.go reloads ./a then importer ./b.
	if err := os.WriteFile(filepath.Join(root, "a", "a.go"), []byte("package a\nfunc Value() int { return 2 }\nfunc Extra() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if diags := verifyGoTargetsIncremental(context.Background(), result, root, nil, targets); len(diags) != 0 {
		t.Fatalf("api-change verify failed: %+v", diags)
	}
	if len(calls) != 2 || !slices.Equal(calls[0], []string{"./a"}) || !slices.Equal(calls[1], []string{"./b"}) {
		t.Fatalf("api-change calls = %#v, want [[\"./a\"], [\"./b\"]]", calls)
	}

	// 4. Syntax error in a/a.go falls back to full load and returns SCN6202.
	if err := os.WriteFile(filepath.Join(root, "a", "a.go"), []byte("package a\nfunc Broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diags := verifyGoTargetsIncremental(context.Background(), result, root, nil, targets)
	if len(diags) == 0 || diags[0].Code != "SCN6202" {
		t.Fatalf("broken syntax diags = %+v, want SCN6202", diags)
	}
}
