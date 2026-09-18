package compiler

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeCompilerSnapshotFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCapturedWorkspaceRevisionMatchesFilesystemRevision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCompilerSnapshotFile(t, root, "app.scn", "application \"captured\" {}\nworkspace {\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"*.go\", \"go.mod\"]\n  revision_exclude = []\n }\n revision_input \"native\" { paths = [\"native.input\"] }\n}\n")
	writeCompilerSnapshotFile(t, root, "go.mod", "module example.test/captured\n")
	writeCompilerSnapshotFile(t, root, "handler.go", "package captured\nconst value = 1\n")
	writeCompilerSnapshotFile(t, root, "handler_test.go", "package captured\n")
	writeCompilerSnapshotFile(t, root, "native.input", "native-v1\n")
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	inputs, err := WorkspaceRevisionInputs(result)
	if err != nil {
		t.Fatal(err)
	}
	captured := make(map[string][]byte, len(inputs))
	seen := map[string]bool{}
	for _, input := range inputs {
		if !input.Present {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			t.Fatal(err)
		}
		captured[input.Path] = data
		seen[input.Path] = input.Implementation
	}
	for _, path := range []string{"go.mod", "handler.go", "handler_test.go", "native.input"} {
		if !seen[path] {
			t.Fatalf("missing captured implementation input %s: %+v", path, inputs)
		}
	}
	bound := *result
	bound.WorkspaceRevision = ""
	if err := BindCapturedWorkspaceRevision(&bound, captured); err != nil {
		t.Fatal(err)
	}
	if bound.WorkspaceRevision != result.WorkspaceRevision {
		t.Fatalf("captured revision = %s, filesystem revision = %s", bound.WorkspaceRevision, result.WorkspaceRevision)
	}
}

func TestSnapshotUnchangedVerifiesMembershipAndBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, appFilename)
	original := []byte("application \"snapshot\" {}\n")
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(path, original)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	check := func(want bool) {
		t.Helper()
		got, err := SnapshotUnchanged(result)
		if err != nil || got != want {
			t.Fatalf("snapshot unchanged=%v, want %v: %v", got, want, err)
		}
	}
	check(true)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write(path, []byte("application \"modified\" {}\n"))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	check(false)
	write(path, original)
	check(true)
	added := filepath.Join(root, "added.scn")
	write(added, []byte("// additional source\n"))
	check(false)
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestSnapshotUnchangedIncludesImplementationBytes(t *testing.T) {
	root := t.TempDir()
	source := "application \"snapshot\" {}\nworkspace {\n implementation_root \"go\" {\n path = \".\"\n revision_include = [\"*.go\", \"go.mod\"]\n revision_exclude = []\n }\n}\n"
	for path, data := range map[string]string{appFilename: source, "handler.go": "package app\n", "go.mod": "module example.test/app\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v %#v", err, result.Diagnostics)
	}
	if ok, err := SnapshotUnchanged(result); err != nil || !ok {
		t.Fatalf("initial snapshot: %v %v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(root, "handler.go"), []byte("package app\nfunc Edited() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := SnapshotUnchanged(result); err != nil || ok {
		t.Fatalf("edited implementation: %v %v", ok, err)
	}
}

func TestWorkspaceRevisionInputsTrackAbsentResolverCandidates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCompilerSnapshotFile(t, root, "ui/card.ts", "export const Card = 1\n")
	source := &Source{
		Path: filepath.Join(root, appFilename), Relative: appFilename,
		Blocks: []*Block{{Type: "renderer", Attributes: map[string]Expression{
			"module": {Kind: "literal", Value: "ui/card"},
		}}},
	}

	paths, err := workspaceRevisionInputPaths(root, []*Source{source})
	if err != nil {
		t.Fatal(err)
	}
	want := []workspaceRevisionInputPath{
		{relative: appLockFilename, present: false},
		{relative: "ui/card", present: false},
		{relative: "ui/card.ts", present: true},
		{relative: "ui/card.tsx", present: false},
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("resolver inputs = %#v, want %#v", paths, want)
	}

	writeCompilerSnapshotFile(t, root, "ui/card.tsx", "export const Card = 2\n")
	paths, err = workspaceRevisionInputPaths(root, []*Source{source})
	if err != nil {
		t.Fatal(err)
	}
	want = []workspaceRevisionInputPath{
		{relative: appLockFilename, present: false},
		{relative: "ui/card", present: false},
		{relative: "ui/card.tsx", present: true},
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("higher-priority resolver inputs = %#v, want %#v", paths, want)
	}
}

func TestWorkspaceRevisionInputsTrackAbsentOptionalInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := &Block{Type: "workspace", Blocks: []*Block{{Type: "revision_input", Attributes: map[string]Expression{
		"paths":    {Kind: "literal", Value: []any{"go.work.sum"}},
		"optional": {Kind: "literal", Value: true},
	}}}}
	source := &Source{Path: filepath.Join(root, appFilename), Relative: appFilename, Blocks: []*Block{workspace}}
	paths, err := workspaceRevisionInputPaths(root, []*Source{source})
	if err != nil {
		t.Fatal(err)
	}
	want := []workspaceRevisionInputPath{
		{relative: appLockFilename, present: false},
		{relative: "go.work.sum", implementation: true, present: false},
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("optional inputs = %#v, want %#v", paths, want)
	}
}

func TestWorkspaceRevisionWalkSkipsACompletelyExcludedDirectoryWithTheSameMembership(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCompilerSnapshotFile(t, root, "app.scn", "application \"pruned\" {}\nworkspace {\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"**/*.go\", \"go.mod\"]\n  revision_exclude = [\"cache/**\", \"**/generated/**\", \"pkg/skip*/**\"]\n }\n}\n")
	for path, data := range map[string]string{
		"go.mod":                   "module example.test/pruned\n",
		"handler.go":               "package pruned\n",
		"pkg/keep/keep.go":         "package keep\n",
		"pkg/keep/generated/g.go":  "package generated\n",
		"pkg/skipped/deep/s.go":    "package deep\n",
		"cache/mod/dep/dep.go":     "package dep\n",
		"cache/mod/dep/sub/sub.go": "package sub\n",
		"cachekeeper/k.go":         "package cachekeeper\n",
	} {
		writeCompilerSnapshotFile(t, root, path, data)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	var read []string
	inputs, err := WorkspaceRevisionInputsReading(result, nil, func(path string) ([]fs.DirEntry, error) {
		relative, _ := filepath.Rel(root, path)
		read = append(read, filepath.ToSlash(relative))
		return os.ReadDir(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, input := range inputs {
		if input.Present {
			got = append(got, input.Path)
		}
	}
	// An exclusion never changes what it excludes: only how much is read.
	want := []string{"cachekeeper/k.go", "go.mod", "handler.go", "pkg/keep/keep.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("membership = %v, want %v", got, want)
	}
	reference, err := WorkspaceRevisionInputs(result)
	if err != nil || len(reference) != len(inputs) {
		t.Fatalf("a supplied reader changed the membership: %v, %v", reference, err)
	}
	for _, directory := range read {
		if directory == "cache" || strings.HasPrefix(directory, "cache/") || strings.HasPrefix(directory, "pkg/skipped") {
			t.Fatalf("a completely excluded directory was read: %s (all reads %v)", directory, read)
		}
	}
	// A pattern that does not name the directory itself is not proof that the
	// whole directory is excluded, so it is still read.
	if !slices.Contains(read, "pkg/keep/generated") || !slices.Contains(read, "cachekeeper") {
		t.Fatalf("a directory no exclusion names was not read: %v", read)
	}
}
