package build

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
)

func TestBuildInputDigestCacheReusesMetadataStableBytesAndRejectsPreservedMtimeEdit(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "input.go")
	if err := os.WriteFile(path, []byte("package input\nconst Value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if buildInputFileChangeTime(info) == 0 {
		t.Skip("platform does not expose a conservative file change timestamp")
	}
	reads := 0
	read := func(path string) ([]byte, error) {
		reads++
		return os.ReadFile(path)
	}
	first, hit, err := cachedBuildInputFileDigest(path, info, read)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("first digest unexpectedly hit the cache")
	}
	secondInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	second, hit, err := cachedBuildInputFileDigest(path, secondInfo, read)
	if err != nil || !hit || second != first || reads != 1 {
		t.Fatalf("stable digest reuse: first=%s second=%s hit=%t reads=%d err=%v", first, second, hit, reads, err)
	}
	if err := os.WriteFile(path, []byte("package input\nconst Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	// Some filesystems can coalesce rapid metadata updates. Wait only when the
	// observed change timestamp has not advanced, then rewrite once more.
	changedInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if buildInputFileChangeTime(changedInfo) == buildInputFileChangeTime(info) {
		time.Sleep(time.Millisecond)
		if err := os.WriteFile(path, []byte("package input\nconst Value = 3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		changedInfo, err = os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	changed, hit, err := cachedBuildInputFileDigest(path, changedInfo, read)
	if err != nil || hit || changed == first || reads != 2 {
		t.Fatalf("preserved-mtime edit: before=%s after=%s hit=%t reads=%d err=%v", first, changed, hit, reads, err)
	}
}

func TestGoInputDiscoveryRequestsEveryConsumedField(t *testing.T) {
	requested := strings.Split(goBuildInputFields, ",")
	shape := reflect.TypeFor[goListPackage]()
	var fields []string
	for i := 0; i < shape.NumField(); i++ {
		fields = append(fields, shape.Field(i).Name)
	}
	slices.Sort(fields)
	slices.Sort(requested)
	if !slices.Equal(fields, requested) {
		t.Fatalf("Go input projection does not match consumed fields: requested=%v consumed=%v", requested, fields)
	}
}

func TestRetainedBuildInputGraphSkipsBodyEditListAndRelistsImportChange(t *testing.T) {
	resetRetainedBuildInputGraphsForTesting()
	t.Cleanup(resetRetainedBuildInputGraphsForTesting)
	root := t.TempDir()
	mainDir := filepath.Join(root, "scenery_internal_main")
	if err := os.MkdirAll(mainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(root, "go.mod")
	mainFile := filepath.Join(mainDir, "main.go")
	if err := os.WriteFile(goMod, []byte("module example.test/app\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMain := func(source string) {
		t.Helper()
		if err := os.WriteFile(mainFile, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeMain("package main\nfunc main() { println(\"A\") }\n")
	module := &goListModule{Path: "example.test/app", Dir: root, GoMod: goMod}
	encoded, err := json.Marshal(goListPackage{Dir: mainDir, ImportPath: "example.test/app/scenery_internal_main", GoFiles: []string{"main.go"}, Module: module})
	if err != nil {
		t.Fatal(err)
	}
	originalList := runGoInputList
	lists := 0
	runGoInputList = func(context.Context, string, []string, ...string) ([]byte, error) {
		lists++
		return encoded, nil
	}
	t.Cleanup(func() { runGoInputList = originalList })
	result := &Result{AppRoot: root, Dir: root, Target: &compiler.GoBuildTarget{Name: "development"}}
	first, err := buildInputManifest(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	writeMain("package main\nfunc main() { println(\"B\") }\n")
	second, err := buildInputManifest(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	if lists != 1 || first.Digest == second.Digest {
		t.Fatalf("body edit lists=%d first=%s second=%s", lists, first.Digest, second.Digest)
	}
	writeMain("package main\nimport _ \"embed\"\nfunc main() { println(\"C\") }\n")
	if _, err := buildInputManifest(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if lists != 2 {
		t.Fatalf("import edit reused stale package graph: lists=%d", lists)
	}
	writeMain("//go:build darwin\n\npackage main\nimport _ \"embed\"\nfunc main() { println(\"D\") }\n")
	if _, err := buildInputManifest(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if lists != 3 {
		t.Fatalf("build directive edit reused stale package graph: lists=%d", lists)
	}
	if err := os.WriteFile(filepath.Join(mainDir, "added.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(mainDir, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := buildInputManifest(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if lists != 4 {
		t.Fatalf("package membership edit reused stale package graph: lists=%d", lists)
	}
	if err := os.WriteFile(goMod, []byte("module example.test/app\n\ngo 1.27\n\nrequire example.test/dependency v1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildInputManifest(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if lists != 5 {
		t.Fatalf("module edit reused stale package graph: lists=%d", lists)
	}
}

func TestBuildInputManifestIncludesLocalReplaceBytesFromGoListInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot, dependencyRoot := filepath.Join(root, "app"), filepath.Join(root, "dependency")
	for _, directory := range []string{appRoot, dependencyRoot, filepath.Join(appRoot, "scenery_internal_main")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dependencyRoot, "go.mod"), "module example.test/dependency\n\ngo 1.26\n")
	dependencyFile := filepath.Join(dependencyRoot, "value.go")
	write(dependencyFile, "package dependency\n\nconst Value = 1\n")
	write(filepath.Join(appRoot, "go.mod"), "module example.test/app\n\ngo 1.26\n\nrequire example.test/dependency v0.0.0\nreplace example.test/dependency => ../dependency\n")
	write(filepath.Join(appRoot, "scenery_internal_main", "main.go"), "package main\n\nimport _ \"example.test/dependency\"\n\nfunc main() {}\n")
	result := &Result{AppRoot: appRoot, Dir: appRoot, Target: &compiler.GoBuildTarget{Name: "development"}}
	var goList bytes.Buffer
	encoder := json.NewEncoder(&goList)
	for _, pkg := range []goListPackage{
		{
			Dir: filepath.Join(appRoot, "scenery_internal_main"), ImportPath: "example.test/app/scenery_internal_main", GoFiles: []string{"main.go"},
			Module: &goListModule{Path: "example.test/app", GoMod: filepath.Join(appRoot, "go.mod")},
		},
		{
			Dir: dependencyRoot, ImportPath: "example.test/dependency", GoFiles: []string{"value.go"},
			Module: &goListModule{Path: "example.test/dependency", Replace: &goListModule{Path: dependencyRoot, GoMod: filepath.Join(dependencyRoot, "go.mod")}},
		},
	} {
		if err := encoder.Encode(pkg); err != nil {
			t.Fatal(err)
		}
	}
	before, err := buildInputManifestFromGoList(result, goList.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	write(dependencyFile, "package dependency\n\nconst Value = 2\n")
	after, err := buildInputManifestFromGoList(result, goList.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == after.Digest {
		t.Fatal("local replacement change did not change build input manifest")
	}
}

func TestBuildInputManifestUsesPublishedModuleSourceDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "scenery.sh@v1.0.0")
	metadata := filepath.Join(root, "cache", "download", "scenery.sh", "@v")
	for _, directory := range []string{source, metadata} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, contents := range map[string]string{
		filepath.Join(source, "go.mod"):       "module scenery.sh\n\ngo 1.27\n",
		filepath.Join(source, "value.go"):     "package scenery\nconst Value = 1\n",
		filepath.Join(metadata, "v1.0.0.mod"): "module scenery.sh\n\ngo 1.27\n",
	} {
		if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := goListPackage{Dir: source, ImportPath: "scenery.sh", GoFiles: []string{"value.go"}, Module: &goListModule{Path: "scenery.sh", Dir: source, Version: "v1.0.0", GoMod: filepath.Join(metadata, "v1.0.0.mod")}}
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	result := &Result{AppRoot: root, Dir: root, Target: &compiler.GoBuildTarget{Name: "development"}}
	if _, err := buildInputManifestFromGoList(result, data); err != nil {
		t.Fatal(err)
	}
	canonicalSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.FrameworkSourceRoot != canonicalSource || result.FrameworkSourceDigest == "" {
		t.Fatalf("framework source = %q %q", result.FrameworkSourceRoot, result.FrameworkSourceDigest)
	}
	pkg.Module.Dir = ""
	data, err = json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildInputManifestFromGoList(result, data); err == nil {
		t.Fatal("accepted a framework graph without its source directory")
	}
}

// A build whose request verified the framework source binds that observation
// without reading the tree again; a verification of another root does not.
func TestBuildInputsBindTheFrameworkSourceTheirBuildVerified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "framework")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"go.mod": "module scenery.sh\n\ngo 1.27\n", "value.go": "package scenery\nconst Value = 1\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(goListPackage{Dir: source, ImportPath: "scenery.sh", GoFiles: []string{"value.go"}, Module: &goListModule{Path: "scenery.sh", Dir: source, GoMod: filepath.Join(source, "go.mod")}})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := FrameworkSourceManifest(source)
	if err != nil {
		t.Fatal(err)
	}
	verified := FrameworkSource{Root: actual.Root, Digest: "sha256:" + strings.Repeat("a", 64)}
	for _, check := range []struct {
		name   string
		source FrameworkSource
		want   string
	}{
		{name: "verified root", source: verified, want: verified.Digest},
		{name: "other root", source: FrameworkSource{Root: filepath.Join(root, "other"), Digest: verified.Digest}, want: actual.Digest},
	} {
		ctx := context.WithValue(context.Background(), verifiedFrameworkSourceKey{}, check.source)
		result := &Result{AppRoot: root, Dir: root, Target: &compiler.GoBuildTarget{Name: "development"}}
		manifest, err := buildInputManifestFromGoListObserved(ctx, result, data, nil)
		if err != nil {
			t.Fatal(err)
		}
		entries := map[string]string{}
		for _, entry := range manifest.Entries {
			entries[entry.Identity] = entry.Digest
		}
		if entries["framework/scenery.sh/source"] != check.want || result.FrameworkSourceDigest != check.want {
			t.Errorf("%s: framework source input %q (result %q), want %q", check.name, entries["framework/scenery.sh/source"], result.FrameworkSourceDigest, check.want)
		}
	}
}
