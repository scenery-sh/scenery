package build

import (
	"bytes"
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
