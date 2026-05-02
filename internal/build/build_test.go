package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "onlava.com/internal/app"
	"onlava.com/internal/codegen"
	"onlava.com/internal/parse"
)

func TestCopyTreeSkipsHiddenDirsAndBrokenSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile := func(rel, data string) {
		path := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("go.mod", "module example\n\ngo 1.25.0\n")
	writeFile("svc/api.go", "package svc\n")
	writeFile("node_modules/pkg/index.js", "console.log('skip')\n")

	if err := os.MkdirAll(filepath.Join(src, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../CLAUDE.md", filepath.Join(src, ".cursor", "rules", "broken.mdc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("svc", filepath.Join(src, "svc-link")); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "svc", "api.go")); err != nil {
		t.Fatalf("expected copied Go file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".cursor")); !os.IsNotExist(err) {
		t.Fatalf("expected hidden directory to be skipped, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("expected node_modules to be skipped, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "svc-link")); !os.IsNotExist(err) {
		t.Fatalf("expected symlinked directory to be skipped, stat err = %v", err)
	}
}

func TestCopyTreeRewritesPGXPoolImport(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	path := filepath.Join(src, "svc", "db.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const input = `package svc

import "github.com/jackc/pgx/v5/pgxpool"

func Open(conn string) (*pgxpool.Pool, error) {
	return pgxpool.New(nil, conn)
}
`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "svc", "db.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, `"github.com/jackc/pgx/v5/pgxpool"`) {
		t.Fatalf("expected pgxpool import to be rewritten, got:\n%s", got)
	}
	if !strings.Contains(got, `"onlava.com/pgxpool"`) {
		t.Fatalf("expected onlava.com/pgxpool import to be present, got:\n%s", got)
	}
}

func TestListSourceFilesSkipsLocalSecretsAndArtifacts(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"go.mod",
		"go.sum",
		"svc/api.go",
		"assets/logo.png",
		".env",
		".env.local",
		".DS_Store",
		"__MACOSX/junk",
		"node_modules/pkg/index.js",
		".onlava/state.json",
		".git/config",
		"coverage/out.txt",
	} {
		writeBuildTestFile(t, root, rel, "x")
	}

	files, err := listSourceFiles(root)
	if err != nil {
		t.Fatalf("listSourceFiles() error = %v", err)
	}
	got := strings.Join(files, "\n")
	for _, want := range []string{"go.mod", "go.sum", "svc/api.go", "assets/logo.png"} {
		if !strings.Contains(got, want) {
			t.Fatalf("source files missing %s: %v", want, files)
		}
	}
	for _, unwanted := range []string{".env", ".env.local", ".DS_Store", "__MACOSX", "node_modules", ".onlava", ".git", "coverage"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("source files included %s: %v", unwanted, files)
		}
	}
}

func TestPrepareWritesInspectArtifacts(t *testing.T) {
	appDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)

	writeBuildTestFile(t, appDir, ".onlava.json", `{"name":"inspectartifacts","id":"inspect-id"}`)
	writeBuildTestFile(t, appDir, "go.mod", "module example.com/inspectartifacts\n\ngo 1.26.0\n")
	writeBuildTestFile(t, appDir, "users/api.go", `package users

import "context"

//onlava:service
type Service struct{}

//onlava:api public
func (*Service) Profile(context.Context) error { return nil }
`)
	writeBuildTestFile(t, appDir, "tenants/api.go", `package tenants

import "context"

//onlava:api private path=/tenants/config method=GET
func Config(context.Context) error { return nil }
`)

	model, err := parse.App(appDir, "inspectartifacts")
	if err != nil {
		t.Fatalf("parse.App() error = %v", err)
	}
	if _, err := Prepare(appDir, model, appcfg.Config{Name: "inspectartifacts", ID: "inspect-id"}, PrepareOptions{}); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	for rel, schema := range map[string]string{
		".onlava/gen/app.json":               `"schema_version": "onlava.inspect.app.v1"`,
		".onlava/gen/routes.json":            `"schema_version": "onlava.inspect.routes.v1"`,
		".onlava/gen/services.json":          `"schema_version": "onlava.inspect.services.v1"`,
		".onlava/gen/endpoints.json":         `"schema_version": "onlava.inspect.endpoints.v1"`,
		".onlava/gen/wire/capabilities.json": `"schema_version": "onlava.wire.capabilities.v1"`,
		".onlava/gen/manifest.json":          `"schema_version": "onlava.gen.manifest.v1"`,
	} {
		data, err := os.ReadFile(filepath.Join(appDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", rel, err)
		}
		if !strings.Contains(string(data), schema) {
			t.Fatalf("%s missing %s:\n%s", rel, schema, data)
		}
	}

	appJSON, err := os.ReadFile(filepath.Join(appDir, ".onlava", "gen", "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		App struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"app"`
	}
	if err := json.Unmarshal(appJSON, &payload); err != nil {
		t.Fatalf("json.Unmarshal(app.json): %v", err)
	}
	if payload.App.Name != "inspectartifacts" || payload.App.ID != "inspect-id" {
		t.Fatalf("app payload = %+v", payload.App)
	}

	manifestJSON, err := os.ReadFile(filepath.Join(appDir, ".onlava", "gen", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Artifacts struct {
			App              string `json:"app"`
			Routes           string `json:"routes"`
			Services         string `json:"services"`
			Endpoints        string `json:"endpoints"`
			WireCapabilities string `json:"wire_capabilities"`
			BuildLatest      string `json:"build_latest"`
		} `json:"artifacts"`
		Schemas struct {
			App              string `json:"app"`
			Routes           string `json:"routes"`
			Services         string `json:"services"`
			Endpoints        string `json:"endpoints"`
			WireCapabilities string `json:"wire_capabilities"`
			BuildLatest      string `json:"build_latest"`
		} `json:"schemas"`
		Hashes struct {
			App              string `json:"app"`
			Routes           string `json:"routes"`
			Services         string `json:"services"`
			Endpoints        string `json:"endpoints"`
			WireCapabilities string `json:"wire_capabilities"`
		} `json:"hashes"`
	}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest.json): %v", err)
	}
	if manifest.Artifacts.App != ".onlava/gen/app.json" || manifest.Artifacts.Endpoints != ".onlava/gen/endpoints.json" || manifest.Artifacts.WireCapabilities != ".onlava/gen/wire/capabilities.json" || manifest.Artifacts.BuildLatest != ".onlava/build/latest.json" {
		t.Fatalf("manifest artifacts = %+v", manifest.Artifacts)
	}
	if manifest.Schemas.App != "onlava.inspect.app.v1" || manifest.Schemas.Endpoints != "onlava.inspect.endpoints.v1" || manifest.Schemas.WireCapabilities != "onlava.wire.capabilities.v1" || manifest.Schemas.BuildLatest != "onlava.build.latest.v1" {
		t.Fatalf("manifest schemas = %+v", manifest.Schemas)
	}
	if manifest.Hashes.App == "" || manifest.Hashes.Routes == "" || manifest.Hashes.Services == "" || manifest.Hashes.Endpoints == "" || manifest.Hashes.WireCapabilities == "" {
		t.Fatalf("manifest hashes = %+v", manifest.Hashes)
	}
}

func TestPrepareAndCompileWriteLatestBuildManifest(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after prepare: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after prepare")
	}
	if manifest.SchemaVersion != "onlava.build.latest.v1" {
		t.Fatalf("schema_version = %q", manifest.SchemaVersion)
	}
	if manifest.Build.Phase != "prepared" {
		t.Fatalf("phase after prepare = %q", manifest.Build.Phase)
	}
	if manifest.Build.BuildStateExists {
		t.Fatal("did not expect build state after prepare")
	}

	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}
	manifest, ok, err = ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after compile: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after compile")
	}
	if manifest.Build.Phase != "compiled" {
		t.Fatalf("phase after compile = %q", manifest.Build.Phase)
	}
	if !manifest.Build.BinaryExists {
		t.Fatal("expected binary to exist after compile")
	}
	if !manifest.Build.BuildStateExists {
		t.Fatal("expected build state to exist after compile")
	}
}

func TestPrepareReusesPersistentWorkspace(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	first, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if !first.NeedsTidy {
		t.Fatal("expected first prepare to require go mod tidy")
	}
	if err := Compile(first); err != nil {
		t.Fatalf("first compile: %v", err)
	}

	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("re-parse app: %v", err)
	}
	second, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{ChangedPaths: []string{"svc/api.go"}})
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if first.Dir != second.Dir {
		t.Fatalf("workspace dir = %q, want %q", second.Dir, first.Dir)
	}
	if second.NeedsTidy {
		t.Fatal("expected incremental prepare to skip go mod tidy")
	}
	if err := Compile(second); err != nil {
		t.Fatalf("second compile without tidy: %v", err)
	}
}

func TestPrepareMarksTidyNeededWhenGoModChanges(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	goModPath := filepath.Join(appDir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\nrequire golang.org/x/text v0.22.0\n")...)
	if err := os.WriteFile(goModPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("re-parse app: %v", err)
	}
	next, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare after go.mod change: %v", err)
	}
	if !next.NeedsTidy {
		t.Fatal("expected go.mod change to require go mod tidy")
	}
}

func TestSyncWorkspaceRemovesStaleFiles(t *testing.T) {
	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.com/test\n")
	writeBuildTestFile(t, root, "svc/api.go", "package svc\n")
	if err := removeUnexpectedFilesFromLists(root, []string{"go.mod", "svc/api.go"}, []string{"onlava_internal_main/x"}); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	stalePath := filepath.Join(root, "svc", "stale.go")
	if err := os.WriteFile(stalePath, []byte("package svc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeUnexpectedFilesFromLists(root, []string{"go.mod", "svc/api.go"}, []string{"onlava_internal_main/x"}); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, stat err = %v", err)
	}
}

func TestLoadCachedGraph(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result.GraphFingerprint = "graph-1"
	result.Metadata = json.RawMessage(`{"ok":true}`)
	result.APIEncoding = json.RawMessage(`{"api":"v1"}`)
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil {
		t.Fatal("expected cached graph to load")
	}
	if string(cached.Metadata) != `{"ok":true}` {
		t.Fatalf("metadata = %s", cached.Metadata)
	}
	if string(cached.APIEncoding) != `{"api":"v1"}` {
		t.Fatalf("api encoding = %s", cached.APIEncoding)
	}
	if cached.Result == nil || cached.Result.Dir == "" {
		t.Fatal("expected cached result to include workspace")
	}
	if cached.Result.AppRoot != appDir || cached.Result.AppName != "buildtest" {
		t.Fatalf("cached result identity = %+v", cached.Result)
	}
}

func TestCompileCachedGraphWritesLatestBuildManifest(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result.GraphFingerprint = "graph-1"
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	if err := Compile(cached.Result); err != nil {
		t.Fatalf("compile cached result: %v", err)
	}

	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after cached compile: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after cached compile")
	}
	if manifest.App.Root != appDir || manifest.App.Name != "buildtest" {
		t.Fatalf("manifest app = %+v", manifest.App)
	}
	if manifest.Build.Phase != "compiled" {
		t.Fatalf("phase after cached compile = %q", manifest.Build.Phase)
	}
}

func TestLoadCachedGraphRejectsOldBuildStateVersion(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result.GraphFingerprint = "graph-1"
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	statePath := filepath.Join(result.Dir, buildStateFile)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read build state: %v", err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode build state: %v", err)
	}
	delete(state, "version")
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatalf("encode old build state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write old build state: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected old build state to be rejected, got ok=%v cached=%#v", ok, cached)
	}
}

func TestRefreshCachedWorkspaceResyncsMissingSourceFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result.GraphFingerprint = "graph-1"
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	newFile := "svc/helper.go"
	writeBuildTestFile(t, appDir, newFile, "package svc\n\nfunc helper() {}\n")

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}
	if _, err := os.Stat(filepath.Join(cached.Result.Dir, filepath.FromSlash(newFile))); !os.IsNotExist(err) {
		t.Fatalf("expected cached workspace to initially miss %s, stat err=%v", newFile, err)
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if !reused {
		t.Fatal("expected cached workspace refresh to be reusable")
	}
	if _, err := os.Stat(filepath.Join(cached.Result.Dir, filepath.FromSlash(newFile))); err != nil {
		t.Fatalf("expected refreshed workspace to include %s: %v", newFile, err)
	}
	found := false
	for _, rel := range cached.Result.SourceFiles {
		if rel == newFile {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("refreshed source files missing %s: %v", newFile, cached.Result.SourceFiles)
	}
}

func TestRefreshCachedWorkspaceMarksNeedsTidyWhenImportsChange(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result.GraphFingerprint = "graph-1"
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	writeBuildTestFile(t, appDir, "svc/extra.go", `package svc

import _ "rsc.io/quote"
`)

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if !reused {
		t.Fatal("expected cached workspace refresh to be reusable")
	}
	if !cached.Result.NeedsTidy {
		t.Fatal("expected refreshed cached workspace to require go mod tidy")
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenGeneratedFileMissing(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result.GraphFingerprint = "graph-1"
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	target := filepath.Join(result.Dir, "svc", "onlava.gen.go")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove generated file: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused {
		t.Fatal("expected cached workspace refresh to force regeneration when a generated file is missing")
	}
}

func TestSyncSourceFilesDetectsNewFilesOutsideChangedPaths(t *testing.T) {
	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n")

	prev, err := syncAllSourceFiles(root, appRoot, nil)
	if err != nil {
		t.Fatalf("syncAllSourceFiles() error = %v", err)
	}

	const asset = "svc/templates/cv_classic.css"
	writeBuildTestFile(t, appRoot, asset, "body { color: black; }\n")

	got, err := syncSourceFiles(root, appRoot, prev, []string{"svc/api.go"})
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(asset))); err != nil {
		t.Fatalf("expected new asset to be synced into workspace: %v", err)
	}
	found := false
	for _, rel := range got {
		if rel == asset {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected source files to include %s, got %v", asset, got)
	}
}

func TestSyncGeneratedFilesKeepsPathsThatAreNowRegularSourceFiles(t *testing.T) {
	root := t.TempDir()
	appRoot := t.TempDir()
	rel := "house/rooftopology_api.go"
	writeBuildTestFile(t, appRoot, rel, "package house\n\nfunc helper() {}\n")
	writeBuildTestFile(t, root, rel, "package house\n\nfunc oldGenerated() {}\n")

	got, err := syncGeneratedFiles(root, appRoot, &codegen.Output{
		Rewritten: map[string][]byte{},
		Generated: map[string][]byte{},
	}, []string{rel}, []string{rel})
	if err != nil {
		t.Fatalf("syncGeneratedFiles() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("generated files = %v, want empty", got)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("expected source-backed file to remain: %v", err)
	}
	if string(data) != "package house\n\nfunc oldGenerated() {}\n" {
		t.Fatalf("unexpected file contents after syncGeneratedFiles: %q", data)
	}
}

func newBuildTestApp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.com/buildtest\n\ngo 1.26.0\n\nrequire onlava.com v0.0.0\n\nreplace onlava.com => "+repoRoot(t)+"\n")
	writeBuildTestFile(t, root, ".onlava.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, root, "svc/api.go", `package svc

import "context"

//onlava:api public
func Hello(ctx context.Context) error { return nil }
`)
	return root
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func writeBuildTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
