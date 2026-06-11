package build

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/parse"
)

func TestCopyTreeSkipsHiddenDirsAndBrokenSymlinks(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	if !strings.Contains(got, `"scenery.sh/pgxpool"`) {
		t.Fatalf("expected scenery.sh/pgxpool import to be present, got:\n%s", got)
	}
}

func TestSeedSceneryGoSumMergesWorkspaceAndRepoSums(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	repo := t.TempDir()
	writeBuildTestFile(t, workspace, "go.sum", "example.com/app v1.0.0 h1:app\n")
	writeBuildTestFile(t, repo, "go.sum", "example.com/scenery-dep v1.0.0 h1:scenery\nexample.com/app v1.0.0 h1:app\n")

	if err := seedSceneryGoSum(workspace, repo); err != nil {
		t.Fatalf("seedSceneryGoSum() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"example.com/app v1.0.0 h1:app\n",
		"example.com/scenery-dep v1.0.0 h1:scenery\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("go.sum missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "example.com/app") != 1 {
		t.Fatalf("go.sum duplicated workspace entry:\n%s", got)
	}
}

func TestListSourceFilesSkipsLocalSecretsAndArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.com/list\n")
	writeBuildTestFile(t, root, "go.sum", "")
	writeBuildTestFile(t, root, "svc/api.go", "package svc\n\nimport \"embed\"\n\n//go:embed assets/logo.png\nvar _ embed.FS\n")
	for _, rel := range []string{
		"svc/assets/logo.png",
		"assets/unembedded-logo.png",
		"docs/readme.md",
		"var/atlas/plans/2026-06-01-atlas-apply-dry-run.txt",
		".env",
		".env.local",
		".DS_Store",
		"__MACOSX/junk",
		"node_modules/pkg/index.js",
		".scenery/state.json",
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
	for _, want := range []string{"go.mod", "go.sum", "svc/api.go", "svc/assets/logo.png"} {
		if !strings.Contains(got, want) {
			t.Fatalf("source files missing %s: %v", want, files)
		}
	}
	for _, unwanted := range []string{"assets/unembedded-logo.png", "docs/readme.md", "var/atlas/plans", ".env", ".env.local", ".DS_Store", "__MACOSX", "node_modules", ".scenery", ".git", "coverage"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("source files included %s: %v", unwanted, files)
		}
	}
}

func TestBuildPrepSkipsBrowserRuntimeArtifactsAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	dst := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	writeBuildTestFile(t, root, ".scenery.json", `{"name":"browserartifacts"}`)
	writeBuildTestFile(t, root, "go.mod", "module example.com/browserartifacts\n\ngo 1.26.3\n")
	writeBuildTestFile(t, root, "go.sum", "")
	writeBuildTestFile(t, root, "svc/api.go", `package svc

import "context"
import "embed"

//go:embed assets/logo.png
var _ embed.FS

type Response struct {
	Message string
}

//scenery:api public
func Ping(context.Context) (*Response, error) {
	return &Response{Message: "pong"}, nil
}
`)
	for _, rel := range []string{
		"svc/assets/logo.png",
		"assets/unembedded-logo.png",
		"var/browser/Default/Preferences",
		"var/chrome/SingletonLock",
		"var/playwright/cache-marker",
		".scenery/artifacts/chatgpt/profile/Default/Preferences",
	} {
		writeBuildTestFile(t, root, rel, "x")
	}
	socketPaths := []string{}
	if runtime.GOOS != "windows" {
		for _, rel := range []string{"var/browser/SingletonSocket", "assets/live.sock"} {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", path)
			if err != nil {
				t.Logf("skipping unix socket fixture %s: %v", rel, err)
				continue
			}
			t.Cleanup(func() { _ = ln.Close() })
			socketPaths = append(socketPaths, rel)
		}
	}

	files, err := listSourceFiles(root)
	if err != nil {
		t.Fatalf("listSourceFiles() error = %v", err)
	}
	got := strings.Join(files, "\n")
	for _, want := range []string{"go.mod", "go.sum", "svc/api.go", "svc/assets/logo.png"} {
		if !strings.Contains(got, want) {
			t.Fatalf("source files missing %s: %v", want, files)
		}
	}
	for _, unwanted := range append([]string{
		"assets/unembedded-logo.png",
		"var/browser",
		"var/chrome",
		"var/playwright",
		".scenery",
	}, socketPaths...) {
		if strings.Contains(got, unwanted) {
			t.Fatalf("source files included %s: %v", unwanted, files)
		}
	}

	if err := copyTree(root, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}
	for _, unwanted := range append([]string{
		"var/browser",
		"var/chrome",
		"var/playwright",
		".scenery",
	}, socketPaths...) {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(unwanted))); !os.IsNotExist(err) {
			t.Fatalf("copyTree copied %s, stat err = %v", unwanted, err)
		}
	}

	cfg := appcfg.Config{Name: "browserartifacts"}
	model, err := parse.App(root, cfg.Name)
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(root, model, cfg)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	joined := strings.Join(result.SourceFiles, "\n")
	for _, unwanted := range append([]string{
		"var/browser",
		"var/chrome",
		"var/playwright",
		".scenery",
	}, socketPaths...) {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("Prepare source files included %s: %v", unwanted, result.SourceFiles)
		}
	}
}

func TestPrepareWritesInspectArtifacts(t *testing.T) {
	appDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)

	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"inspectartifacts","id":"inspect-id"}`)
	writeBuildTestFile(t, appDir, "go.mod", "module example.com/inspectartifacts\n\ngo 1.26.3\n")
	writeBuildTestFile(t, appDir, "users/api.go", `package users

import "context"

//scenery:service
type Service struct{}

//scenery:api public
func (*Service) Profile(context.Context) error { return nil }
`)
	writeBuildTestFile(t, appDir, "tenants/api.go", `package tenants

import "context"

//scenery:api private path=/tenants/config method=GET
func Config(context.Context) error { return nil }
`)

	model, err := parse.App(appDir, "inspectartifacts")
	if err != nil {
		t.Fatalf("parse.App() error = %v", err)
	}
	if _, err := Prepare(appDir, model, appcfg.Config{Name: "inspectartifacts", ID: "inspect-id"}); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	for rel, schema := range map[string]string{
		".scenery/gen/app.json":               `"schema_version": "scenery.inspect.app.v1"`,
		".scenery/gen/routes.json":            `"schema_version": "scenery.inspect.routes.v1"`,
		".scenery/gen/services.json":          `"schema_version": "scenery.inspect.services.v1"`,
		".scenery/gen/endpoints.json":         `"schema_version": "scenery.inspect.endpoints.v1"`,
		".scenery/gen/wire/capabilities.json": `"schema_version": "scenery.wire.capabilities.v1"`,
		".scenery/gen/manifest.json":          `"schema_version": "scenery.gen.manifest.v1"`,
	} {
		data, err := os.ReadFile(filepath.Join(appDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", rel, err)
		}
		if !strings.Contains(string(data), schema) {
			t.Fatalf("%s missing %s:\n%s", rel, schema, data)
		}
	}

	appJSON, err := os.ReadFile(filepath.Join(appDir, ".scenery", "gen", "app.json"))
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

	manifestJSON, err := os.ReadFile(filepath.Join(appDir, ".scenery", "gen", "manifest.json"))
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
	if manifest.Artifacts.App != ".scenery/gen/app.json" || manifest.Artifacts.Endpoints != ".scenery/gen/endpoints.json" || manifest.Artifacts.WireCapabilities != ".scenery/gen/wire/capabilities.json" || manifest.Artifacts.BuildLatest != ".scenery/build/latest.json" {
		t.Fatalf("manifest artifacts = %+v", manifest.Artifacts)
	}
	if manifest.Schemas.App != "scenery.inspect.app.v1" || manifest.Schemas.Endpoints != "scenery.inspect.endpoints.v1" || manifest.Schemas.WireCapabilities != "scenery.wire.capabilities.v1" || manifest.Schemas.BuildLatest != "scenery.build.latest.v1" {
		t.Fatalf("manifest schemas = %+v", manifest.Schemas)
	}
	if manifest.Hashes.App == "" || manifest.Hashes.Routes == "" || manifest.Hashes.Services == "" || manifest.Hashes.Endpoints == "" || manifest.Hashes.WireCapabilities == "" {
		t.Fatalf("manifest hashes = %+v", manifest.Hashes)
	}
}

func TestPrepareAndCompileWriteLatestBuildManifest(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
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
	if manifest.SchemaVersion != "scenery.build.latest.v1" {
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

func TestCompileRealGoBuildSmoke(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "scenery-app"),
		NeedsTidy:      true,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"scenery_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.NeedsTidy {
		t.Fatal("expected Compile to clear NeedsTidy")
	}
	if _, err := os.Stat(result.Binary); err != nil {
		t.Fatalf("expected real build binary: %v", err)
	}
	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after compile: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after compile")
	}
	if manifest.Build.Phase != "compiled" || !manifest.Build.BinaryExists || !manifest.Build.BuildStateExists {
		t.Fatalf("manifest build = %+v", manifest.Build)
	}
}

func TestCompileRunsTidyOnlyAfterBuildFailure(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var commands []string
	tidied := false
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			tidied = true
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./scenery_internal_main" {
			if !tidied {
				return fmt.Errorf("go.mod updates needed")
			}
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "scenery-app"),
		NeedsTidy:      true,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"scenery_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := strings.Join(commands, "|"), "build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main|mod tidy|build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main"; got != want {
		t.Fatalf("go commands = %q, want %q", got, want)
	}
	if result.NeedsTidy {
		t.Fatal("expected Compile to clear NeedsTidy")
	}
}

func TestCompilePassesConfiguredGoBuildFlags(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var got []string
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		got = append([]string(nil), args...)
		out, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	}
	t.Cleanup(func() { runGo = old })

	result := &Result{
		AppRoot:      appDir,
		AppName:      "smoke",
		Dir:          workspace,
		Binary:       filepath.Join(workspace, "scenery-app"),
		GoBuildFlags: []string{"-tags=roofmapnet_native", " ", "-gcflags=all=-N -l"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := []string{"build", "-tags=roofmapnet_native", "-gcflags=all=-N -l", "-buildvcs=false", "-o", result.Binary, "./scenery_internal_main"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("go build args = %#v, want %#v", got, want)
	}
}

func TestCompileRetriesTidyWhenBuildReportsStaleGoMod(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var commands []string
	tidied := false
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			tidied = true
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./scenery_internal_main" {
			if !tidied {
				return fmt.Errorf("go build -buildvcs=false failed: exit status 1\ngo: updates to go.mod needed; to update it:\n\tgo mod tidy")
			}
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "scenery-app"),
		NeedsTidy:      false,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"scenery_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := strings.Join(commands, "|"), "build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main|mod tidy|build -buildvcs=false -o "+result.Binary+" ./scenery_internal_main"; got != want {
		t.Fatalf("go commands = %q, want %q", got, want)
	}
}

func TestPrepareReusesPersistentWorkspace(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	first, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if !first.NeedsTidy {
		t.Fatal("expected first prepare to require go mod tidy")
	}
	if err := Compile(first); err != nil {
		t.Fatalf("first compile: %v", err)
	}

	second, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
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

func TestPrepareUsesFingerprintSpecificWorkspaceBinary(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{Name: "buildtest"}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	first, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := Compile(first); err != nil {
		t.Fatalf("first compile: %v", err)
	}

	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }

func Changed() {}
`)
	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse updated app: %v", err)
	}
	second, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if second.Dir != first.Dir {
		t.Fatalf("workspace dir = %q, want %q", second.Dir, first.Dir)
	}
	if first.BuildFingerprint == "" || second.BuildFingerprint == "" {
		t.Fatalf("expected build fingerprints, got first=%q second=%q", first.BuildFingerprint, second.BuildFingerprint)
	}
	if first.BuildFingerprint == second.BuildFingerprint {
		t.Fatalf("expected source change to update build fingerprint %q", first.BuildFingerprint)
	}
	if first.Binary == second.Binary {
		t.Fatalf("expected source change to use a new binary path, got %q", second.Binary)
	}
	for _, binary := range []string{first.Binary, second.Binary} {
		if !strings.HasPrefix(filepath.Base(binary), "scenery-app-") {
			t.Fatalf("binary %q is not fingerprint-specific", binary)
		}
	}
}

func TestPrepareIncludesGoBuildFlagsInFingerprint(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	firstCfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_native"}},
	}
	first, err := Prepare(appDir, model, firstCfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := Compile(first); err != nil {
		t.Fatalf("first compile: %v", err)
	}

	secondCfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_portable"}},
	}
	second, err := Prepare(appDir, model, secondCfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if first.BuildFingerprint == second.BuildFingerprint {
		t.Fatalf("expected build flags to affect fingerprint %q", first.BuildFingerprint)
	}
	if first.Binary == second.Binary {
		t.Fatalf("expected build flags to affect binary path %q", first.Binary)
	}
	if second.ReuseCompiled {
		t.Fatal("expected changed build flags to avoid reusing compiled binary")
	}
}

func TestLoadReusableBinaryRequiresMatchingSourceFingerprint(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{Name: "buildtest"}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	reused, ok, err := LoadReusableBinary(appDir, cfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() error = %v", err)
	}
	if !ok {
		t.Fatal("expected reusable binary")
	}
	if reused.Binary != result.Binary {
		t.Fatalf("reused binary = %q, want %q", reused.Binary, result.Binary)
	}

	writeBuildTestFile(t, appDir, "svc/extra.go", "package svc\n\nfunc extra() {}\n")
	reused, ok, err = LoadReusableBinary(appDir, cfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() after source change error = %v", err)
	}
	if ok || reused != nil {
		t.Fatalf("expected source change to reject cached binary, got ok=%v result=%#v", ok, reused)
	}
}

func TestLoadReusableBinaryRequiresMatchingGoBuildFlags(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_native"}},
	}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Compile(result); err != nil {
		t.Fatalf("compile: %v", err)
	}

	reused, ok, err := LoadReusableBinary(appDir, cfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() error = %v", err)
	}
	if !ok || reused == nil {
		t.Fatal("expected reusable binary with matching build flags")
	}

	changedCfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_portable"}},
	}
	reused, ok, err = LoadReusableBinary(appDir, changedCfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() after build flag change error = %v", err)
	}
	if ok || reused != nil {
		t.Fatalf("expected build flag change to reject cached binary, got ok=%v result=%#v", ok, reused)
	}
}

func TestPrepareReusesExistingFingerprintBinaryWhenStatePointsElsewhere(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{Name: "buildtest"}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	first, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := Compile(first); err != nil {
		t.Fatalf("first compile: %v", err)
	}
	firstBinary := first.Binary

	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error {
	return nil
}
`)
	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse changed app: %v", err)
	}
	second, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if err := Compile(second); err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if second.Binary == firstBinary {
		t.Fatal("expected source change to produce a different fingerprint binary")
	}

	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }
`)
	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse reverted app: %v", err)
	}
	reverted, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("reverted prepare: %v", err)
	}
	if reverted.Binary != firstBinary {
		t.Fatalf("reverted binary = %q, want first binary %q", reverted.Binary, firstBinary)
	}
	if !reverted.ReuseCompiled {
		t.Fatal("expected prepare to reuse existing fingerprint binary after reverting source")
	}
}

func TestCompileUpdatesDependencyFingerprintAfterSuccessfulBuild(t *testing.T) {
	old := runGo
	runGo = func(_ context.Context, dir string, args ...string) error {
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./scenery_internal_main" {
			if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("example.com/dep v1.0.0 h1:dep\n"), 0o644); err != nil {
				return err
			}
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "go.sum", "")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")
	result := &Result{
		AppRoot:               appDir,
		AppName:               "buildtest",
		Dir:                   workspace,
		Binary:                filepath.Join(workspace, "scenery-app-test"),
		NeedsTidy:             true,
		DependencyFingerprint: "stale",
		BuildFingerprint:      "test",
		SourceFiles:           []string{"go.mod"},
		GeneratedFiles:        []string{"scenery_internal_main/main.go"},
	}

	if err := Compile(result); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	state, err := loadBuildState(workspace)
	if err != nil {
		t.Fatalf("loadBuildState: %v", err)
	}
	want, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatalf("dependencyFingerprintFromWorkspace: %v", err)
	}
	if state.DependencyFingerprint != want {
		t.Fatalf("saved dependency fingerprint = %q, want post-build fingerprint %q", state.DependencyFingerprint, want)
	}
}

func TestCompileReusesExistingBinaryDespiteDependencyFingerprintDrift(t *testing.T) {
	old := runGo
	runGo = func(_ context.Context, dir string, args ...string) error {
		return fmt.Errorf("unexpected fake go command in %s: go %s", dir, strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "go.sum", "example.com/dep v1.0.0 h1:dep\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")
	depFingerprint, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatalf("dependencyFingerprintFromWorkspace: %v", err)
	}
	binary := filepath.Join(workspace, "scenery-app-existing")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write cached binary: %v", err)
	}
	result := &Result{
		AppRoot:               appDir,
		AppName:               "buildtest",
		Dir:                   workspace,
		Binary:                binary,
		NeedsTidy:             true,
		DependencyFingerprint: depFingerprint,
		BuildFingerprint:      "existing",
		ReuseCompiled:         true,
		SourceFiles:           []string{"go.mod"},
		GeneratedFiles:        []string{"scenery_internal_main/main.go"},
	}

	if err := Compile(result); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if result.NeedsTidy {
		t.Fatal("expected cached compile to clear NeedsTidy")
	}
	state, err := loadBuildState(workspace)
	if err != nil {
		t.Fatalf("loadBuildState: %v", err)
	}
	if state.DependencyFingerprint != depFingerprint {
		t.Fatalf("saved dependency fingerprint = %q, want %q", state.DependencyFingerprint, depFingerprint)
	}
}

func TestCachedGeneratorFingerprintInvalidatesOnSourceMetadata(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "internal", "codegen", "sample.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second != first {
		t.Fatalf("cached fingerprint changed without source metadata change: %q != %q", second, first)
	}
	cachePath, err := generatorFingerprintCachePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}

	if err := os.WriteFile(sourcePath, []byte("package internal\n\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(sourcePath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	third, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(third) error = %v", err)
	}
	if third == first {
		t.Fatalf("cached fingerprint did not change after source metadata changed: %q", third)
	}
}

func TestCachedGeneratorFingerprintIncludesRootPackageFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "scenery.go")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package scenery\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("package scenery\n\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(sourcePath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second == first {
		t.Fatalf("cached fingerprint did not change after root package source changed: %q", second)
	}
}

func TestCachedGeneratorFingerprintIncludesEmbeddedFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "auth", "standard.go")
	embedPath := filepath.Join(repo, "auth", "db", "gen", "schema.sql")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(embedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package auth\n\nimport \"embed\"\n\n//go:embed db/gen/schema.sql\nvar _ embed.FS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(embedPath, []byte("create table one();\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(embedPath, []byte("create table two();\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(embedPath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second == first {
		t.Fatalf("cached fingerprint did not change after embedded source changed: %q", second)
	}
}

func TestCachedGeneratorFingerprintIgnoresUnrelatedInternalPackages(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	repo := t.TempDir()
	trackedPath := filepath.Join(repo, "internal", "codegen", "sample.go")
	unrelatedPath := filepath.Join(repo, "internal", "agent", "sample.go")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(trackedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(unrelatedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackedPath, []byte("package codegen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("package agent\n\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(unrelatedPath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second != first {
		t.Fatalf("cached fingerprint changed for unrelated internal package: %q != %q", second, first)
	}
}

func TestPrepareMarksTidyNeededWhenGoModChanges(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
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

	next, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
	if err != nil {
		t.Fatalf("prepare after go.mod change: %v", err)
	}
	if !next.NeedsTidy {
		t.Fatal("expected go.mod change to require go mod tidy")
	}
}

func TestSyncWorkspaceRemovesStaleFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.com/test\n")
	writeBuildTestFile(t, root, "svc/api.go", "package svc\n")
	if err := removeUnexpectedFilesFromLists(root, []string{"go.mod", "svc/api.go"}, []string{"scenery_internal_main/x"}); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	stalePath := filepath.Join(root, "svc", "stale.go")
	if err := os.WriteFile(stalePath, []byte("package svc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeUnexpectedFilesFromLists(root, []string{"go.mod", "svc/api.go"}, []string{"scenery_internal_main/x"}); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, stat err = %v", err)
	}
}

func TestLoadCachedGraph(t *testing.T) {
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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

func TestLoadCachedGraphRequiresMatchingGoBuildFlags(t *testing.T) {
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")
	flags := []string{"-tags=roofmapnet_native"}

	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("loadBuildState() error = %v", err)
	}
	buildFingerprint, err := workspaceBuildFingerprint(result.Dir, flags, sourceFilesFromStamps(state.SourceStamps), state.GeneratedFiles)
	if err != nil {
		t.Fatalf("workspaceBuildFingerprint() error = %v", err)
	}
	state.GoBuildFlags = append([]string(nil), flags...)
	state.BuildFingerprint = buildFingerprint
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("saveBuildState() error = %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: flags},
	}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() with matching flags error = %v", err)
	}
	if !ok || cached == nil {
		t.Fatal("expected cached graph with matching build flags")
	}
	if strings.Join(cached.Result.GoBuildFlags, "\x00") != strings.Join(flags, "\x00") {
		t.Fatalf("cached build flags = %#v, want %#v", cached.Result.GoBuildFlags, flags)
	}

	cached, ok, err = LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() with missing flags error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected missing build flags to reject cached graph, got ok=%v cached=%#v", ok, cached)
	}
}

func TestCompileCachedGraphWritesLatestBuildManifest(t *testing.T) {
	useFakeGoRunner(t)
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

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

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected old build state to be rejected, got ok=%v cached=%#v", ok, cached)
	}
}

func TestLoadCachedGraphRejectsGeneratorFingerprintMismatch(t *testing.T) {
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	statePath := filepath.Join(result.Dir, buildStateFile)
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("load build state: %v", err)
	}
	state.GeneratorFingerprint = "stale-generator"
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("save stale build state: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected stale generator fingerprint to be rejected, got ok=%v cached=%#v", ok, cached)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file should remain for fallback regeneration: %v", err)
	}
}

func TestRefreshCachedWorkspaceResyncsMissingSourceFiles(t *testing.T) {
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	newFile := "svc/helper.go"
	writeBuildTestFile(t, appDir, newFile, "package svc\n\nfunc helper() {}\n")

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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
	found := slices.Contains(cached.Result.SourceFiles, newFile)
	if !found {
		t.Fatalf("refreshed source files missing %s: %v", newFile, cached.Result.SourceFiles)
	}
}

func TestRefreshCachedWorkspaceResyncsChangedSourceFiles(t *testing.T) {
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	// Regression: a git pull changed svc/api.go without the dev watcher
	// reporting it; the workspace copy must still be refreshed from disk
	// instead of being trusted because it exists.
	const updated = `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }

func pulledInChange() {}
`
	writeBuildTestFile(t, appDir, "svc/api.go", updated)

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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
	data, err := os.ReadFile(filepath.Join(cached.Result.Dir, "svc", "api.go"))
	if err != nil {
		t.Fatalf("read workspace copy: %v", err)
	}
	if string(data) != updated {
		t.Fatalf("workspace copy = %q, want resynced %q", data, updated)
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenSourceFileMissing(t *testing.T) {
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	target := filepath.Join(result.Dir, "svc", "api.go")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove source file: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	reused, err := RefreshCachedWorkspace(cached.Result.AppRoot, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if !reused {
		t.Fatal("expected cached workspace refresh to be reusable")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected missing source file to be restored: %v", err)
	}
}

func TestRefreshCachedWorkspaceMarksNeedsTidyWhenImportsChange(t *testing.T) {
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	writeBuildTestFile(t, appDir, "svc/extra.go", `package svc

import _ "rsc.io/quote"
`)

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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

func TestRefreshCachedWorkspaceSeedsDependencyFingerprintBeforeReuse(t *testing.T) {
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	if err := seedSceneryGoSum(result.Dir, repoRoot(t)); err != nil {
		t.Fatalf("seedSceneryGoSum() error = %v", err)
	}
	depFingerprint, err := dependencyFingerprintFromWorkspace(result.Dir)
	if err != nil {
		t.Fatalf("dependencyFingerprintFromWorkspace() error = %v", err)
	}
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("loadBuildState() error = %v", err)
	}
	state.DependencyFingerprint = depFingerprint
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("saveBuildState() error = %v", err)
	}
	if err := os.WriteFile(result.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write cached binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(result.Dir, "go.sum"), nil, 0o644); err != nil {
		t.Fatalf("write stale workspace go.sum: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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
	if cached.Result.NeedsTidy {
		t.Fatal("expected seeded dependency fingerprint to avoid tidy")
	}
	if !cached.Result.ReuseCompiled {
		t.Fatal("expected existing fingerprint binary to be reused")
	}
	if cached.Result.DependencyFingerprint != depFingerprint {
		t.Fatalf("dependency fingerprint = %q, want seeded %q", cached.Result.DependencyFingerprint, depFingerprint)
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenGeneratedFileMissing(t *testing.T) {
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	target := filepath.Join(result.Dir, "svc", "scenery.gen.go")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove generated file: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
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

func TestSyncSourceFilesDetectsNewFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nimport \"embed\"\n\n//go:embed templates/*\nvar _ embed.FS\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}

	const asset = "svc/templates/cv_classic.css"
	writeBuildTestFile(t, appRoot, asset, "body { color: black; }\n")

	got, _, err := syncSourceFiles(root, appRoot, prevStamps, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(asset))); err != nil {
		t.Fatalf("expected new asset to be synced into workspace: %v", err)
	}
	found := slices.Contains(got, asset)
	if !found {
		t.Fatalf("expected source files to include %s, got %v", asset, got)
	}
}

func TestSyncSourceFilesResyncsFilesChangedOnDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nfunc oldSymbol() {}\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}

	// Simulate a change the file watcher never reported, e.g. a git pull
	// rewriting the file between a watch snapshot and the workspace sync.
	const updated = "package svc\n\nfunc replacementSymbol() {}\n"
	writeBuildTestFile(t, appRoot, "svc/api.go", updated)

	if _, _, err := syncSourceFiles(root, appRoot, prevStamps, nil); err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "svc", "api.go"))
	if err != nil {
		t.Fatalf("read workspace copy: %v", err)
	}
	if string(data) != updated {
		t.Fatalf("workspace copy = %q, want resynced %q", data, updated)
	}
}

func TestSyncSourceFilesRestoresDeletedWorkspaceCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nfunc helper() {}\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "svc", "api.go")); err != nil {
		t.Fatalf("remove workspace copy: %v", err)
	}

	if _, _, err := syncSourceFiles(root, appRoot, prevStamps, nil); err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "svc", "api.go")); err != nil {
		t.Fatalf("expected deleted workspace copy to be restored: %v", err)
	}
}

func TestSyncGeneratedFilesKeepsPathsThatAreNowRegularSourceFiles(t *testing.T) {
	t.Parallel()

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
	return newBuildTestAppNamed(t, "")
}

func newBuildTestAppNamed(t *testing.T, base string) string {
	t.Helper()
	root := t.TempDir()
	if strings.TrimSpace(base) != "" {
		root = filepath.Join(root, base)
	}
	writeBuildTestFile(t, root, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRoot(t)+"\n")
	writeBuildTestFile(t, root, ".scenery.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, root, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }
`)
	return root
}

func newCachedBuildTestWorkspace(t *testing.T, graphFingerprint string) (string, *Result) {
	t.Helper()
	cacheDir := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()

	const goMod = "module example.com/buildtest\n\ngo 1.26.3\n"
	const serviceSource = `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }
`
	writeBuildTestFile(t, appDir, ".scenery.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, appDir, "go.mod", goMod)
	writeBuildTestFile(t, appDir, "svc/api.go", serviceSource)

	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", goMod)
	writeBuildTestFile(t, workspace, "svc/api.go", serviceSource)
	writeBuildTestFile(t, workspace, "svc/scenery.gen.go", "package svc\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")

	depFingerprint, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	sourceFiles := []string{"go.mod", "svc/api.go"}
	generatedFiles := []string{"scenery_internal_main/main.go", "svc/scenery.gen.go"}
	buildFingerprint, err := workspaceBuildFingerprint(workspace, nil, sourceFiles, generatedFiles)
	if err != nil {
		t.Fatal(err)
	}
	sourceStamps := make(map[string]SourceStamp, len(sourceFiles))
	for _, rel := range sourceFiles {
		info, err := os.Stat(filepath.Join(appDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		sourceStamps[rel] = sourceStampFromInfo(info)
	}
	sourceMetadataFingerprint := sourceStampsFingerprint(sourceStamps)
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	result := &Result{
		AppRoot:                   appDir,
		AppName:                   "buildtest",
		Dir:                       workspace,
		Binary:                    filepath.Join(workspace, workspaceBinaryName(appDir, buildFingerprint)),
		NeedsTidy:                 false,
		DependencyFingerprint:     depFingerprint,
		SourceMetadataFingerprint: sourceMetadataFingerprint,
		GeneratorFingerprint:      generatorFingerprint,
		BuildFingerprint:          buildFingerprint,
		GraphFingerprint:          graphFingerprint,
		Metadata:                  json.RawMessage(`{"ok":true}`),
		APIEncoding:               json.RawMessage(`{"api":"v1"}`),
		SourceFiles:               append([]string(nil), sourceFiles...),
		SourceStamps:              sourceStamps,
		GeneratedFiles:            append([]string(nil), generatedFiles...),
	}
	if err := saveBuildState(workspace, buildState{
		Version:                   buildStateVersion,
		DependencyFingerprint:     depFingerprint,
		SourceMetadataFingerprint: sourceMetadataFingerprint,
		GeneratorFingerprint:      generatorFingerprint,
		BuildFingerprint:          buildFingerprint,
		GraphFingerprint:          graphFingerprint,
		Metadata:                  append([]byte(nil), result.Metadata...),
		APIEncoding:               append([]byte(nil), result.APIEncoding...),
		SourceStamps:              sourceStamps,
		GeneratedFiles:            generatedFiles,
	}); err != nil {
		t.Fatal(err)
	}
	return appDir, result
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

func useFakeGoRunner(t *testing.T) {
	t.Helper()
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return nil
		}
		if out, ok := fakeGoBuildOutput(args); ok {
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })
}

func fakeGoBuildOutput(args []string) (string, bool) {
	if len(args) < 5 || args[0] != "build" || args[len(args)-1] != "./scenery_internal_main" {
		return "", false
	}
	for i := 1; i < len(args)-2; i++ {
		if args[i] == "-buildvcs=false" && args[i+1] == "-o" {
			return args[i+2], true
		}
	}
	return "", false
}
