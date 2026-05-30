package build

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/codegen"
	"github.com/pbrazdil/onlava/internal/parse"
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
	if !strings.Contains(got, `"github.com/pbrazdil/onlava/pgxpool"`) {
		t.Fatalf("expected github.com/pbrazdil/onlava/pgxpool import to be present, got:\n%s", got)
	}
}

func TestSeedOnlavaGoSumMergesWorkspaceAndRepoSums(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	repo := t.TempDir()
	writeBuildTestFile(t, workspace, "go.sum", "example.com/app v1.0.0 h1:app\n")
	writeBuildTestFile(t, repo, "go.sum", "example.com/onlava-dep v1.0.0 h1:onlava\nexample.com/app v1.0.0 h1:app\n")

	if err := seedOnlavaGoSum(workspace, repo); err != nil {
		t.Fatalf("seedOnlavaGoSum() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"example.com/app v1.0.0 h1:app\n",
		"example.com/onlava-dep v1.0.0 h1:onlava\n",
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

func TestBuildPrepSkipsBrowserRuntimeArtifactsAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	dst := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", t.TempDir())
	writeBuildTestFile(t, root, ".onlava.json", `{"name":"browserartifacts"}`)
	writeBuildTestFile(t, root, "go.mod", "module example.com/browserartifacts\n\ngo 1.26.0\n")
	writeBuildTestFile(t, root, "go.sum", "")
	writeBuildTestFile(t, root, "svc/api.go", `package svc

import "context"

type Response struct {
	Message string
}

//onlava:api public
func Ping(context.Context) (*Response, error) {
	return &Response{Message: "pong"}, nil
}
`)
	for _, rel := range []string{
		"assets/logo.png",
		"var/browser/Default/Preferences",
		"var/chrome/SingletonLock",
		"var/playwright/cache-marker",
		".onlava/artifacts/chatgpt/profile/Default/Preferences",
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
	for _, want := range []string{"go.mod", "go.sum", "svc/api.go", "assets/logo.png"} {
		if !strings.Contains(got, want) {
			t.Fatalf("source files missing %s: %v", want, files)
		}
	}
	for _, unwanted := range append([]string{
		"var/browser",
		"var/chrome",
		"var/playwright",
		".onlava",
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
		".onlava",
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
	result, err := Prepare(root, model, cfg, PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	joined := strings.Join(result.SourceFiles, "\n")
	for _, unwanted := range append([]string{
		"var/browser",
		"var/chrome",
		"var/playwright",
		".onlava",
	}, socketPaths...) {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("Prepare source files included %s: %v", unwanted, result.SourceFiles)
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
	useFakeGoRunner(t)
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

func TestCompileRealGoBuildSmoke(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".onlava.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.0\n")
	writeBuildTestFile(t, workspace, "onlava_internal_main/main.go", "package main\n\nfunc main() {}\n")

	result := &Result{
		AppRoot:        appDir,
		AppName:        "smoke",
		Dir:            workspace,
		Binary:         filepath.Join(workspace, "onlava-app"),
		NeedsTidy:      true,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"onlava_internal_main/main.go"},
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
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".onlava.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.0\n")
	writeBuildTestFile(t, workspace, "onlava_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var commands []string
	tidied := false
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			tidied = true
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./onlava_internal_main" {
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
		Binary:         filepath.Join(workspace, "onlava-app"),
		NeedsTidy:      true,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"onlava_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := strings.Join(commands, "|"), "build -buildvcs=false -o "+result.Binary+" ./onlava_internal_main|mod tidy|build -buildvcs=false -o "+result.Binary+" ./onlava_internal_main"; got != want {
		t.Fatalf("go commands = %q, want %q", got, want)
	}
	if result.NeedsTidy {
		t.Fatal("expected Compile to clear NeedsTidy")
	}
}

func TestCompileRetriesTidyWhenBuildReportsStaleGoMod(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()
	writeBuildTestFile(t, appDir, ".onlava.json", `{"name":"smoke"}`)

	workspace, err := workspaceDir(appDir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/smoke\n\ngo 1.26.0\n")
	writeBuildTestFile(t, workspace, "onlava_internal_main/main.go", "package main\n\nfunc main() {}\n")

	var commands []string
	tidied := false
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			tidied = true
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./onlava_internal_main" {
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
		Binary:         filepath.Join(workspace, "onlava-app"),
		NeedsTidy:      false,
		SourceFiles:    []string{"go.mod"},
		GeneratedFiles: []string{"onlava_internal_main/main.go"},
	}
	if err := Compile(result); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := strings.Join(commands, "|"), "build -buildvcs=false -o "+result.Binary+" ./onlava_internal_main|mod tidy|build -buildvcs=false -o "+result.Binary+" ./onlava_internal_main"; got != want {
		t.Fatalf("go commands = %q, want %q", got, want)
	}
}

func TestCompileReusesSharedWorkspaceFingerprintBinary(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	t.Setenv("ONLAVA_ALLOW_TEST_WORKSPACE_KEY", "1")
	t.Setenv("ONLAVA_TEST_WORKSPACE_KEY", "shared-buildtest")

	var builds int
	old := runGo
	runGo = func(_ context.Context, _ string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return nil
		}
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./onlava_internal_main" {
			builds++
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	firstRoot := newBuildTestAppNamed(t, "buildtest")
	firstModel, err := parse.App(firstRoot, "buildtest")
	if err != nil {
		t.Fatalf("parse first app: %v", err)
	}
	first, err := Prepare(firstRoot, firstModel, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare first app: %v", err)
	}
	if first.ReuseCompiled {
		t.Fatal("first compile unexpectedly reused a binary")
	}
	if err := Compile(first); err != nil {
		t.Fatalf("compile first app: %v", err)
	}

	secondRoot := newBuildTestAppNamed(t, "buildtest")
	secondModel, err := parse.App(secondRoot, "buildtest")
	if err != nil {
		t.Fatalf("parse second app: %v", err)
	}
	second, err := Prepare(secondRoot, secondModel, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
	if err != nil {
		t.Fatalf("prepare second app: %v", err)
	}
	if !second.ReuseCompiled {
		t.Fatalf("second compile did not mark the fingerprinted binary reusable: first fingerprint=%s second fingerprint=%s second needs tidy=%v binary exists=%v",
			first.BuildFingerprint, second.BuildFingerprint, second.NeedsTidy, pathExists(second.Binary))
	}
	if first.Binary != second.Binary {
		t.Fatalf("binary paths differ: first=%s second=%s", first.Binary, second.Binary)
	}
	if err := Compile(second); err != nil {
		t.Fatalf("compile second app: %v", err)
	}
	if builds != 1 {
		t.Fatalf("go build calls = %d, want 1", builds)
	}
}

func TestWorkspaceDirRejectsUnguardedTestWorkspaceKey(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	t.Setenv("ONLAVA_TEST_WORKSPACE_KEY", "shared-buildtest")

	_, err := workspaceDir(t.TempDir(), "buildtest")
	if err == nil || !strings.Contains(err.Error(), "ONLAVA_ALLOW_TEST_WORKSPACE_KEY=1") {
		t.Fatalf("workspaceDir() error = %v, want guarded test workspace key error", err)
	}
}

func TestPrepareReusesPersistentWorkspace(t *testing.T) {
	useFakeGoRunner(t)
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

func TestLoadReusableBinaryRequiresMatchingSourceFingerprint(t *testing.T) {
	useFakeGoRunner(t)
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{Name: "buildtest"}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	result, err := Prepare(appDir, model, cfg, PrepareOptions{})
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

func TestCachedGeneratorFingerprintInvalidatesOnSourceMetadata(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
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
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "onlava.go")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package onlava\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("package onlava\n\nconst X = 1\n"), 0o644); err != nil {
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
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
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
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
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

	next, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"}, PrepareOptions{})
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
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

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
	useFakeGoRunner(t)
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

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

	cached, ok, err := LoadCachedGraph(appDir, "buildtest", "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected old build state to be rejected, got ok=%v cached=%#v", ok, cached)
	}
}

func TestRefreshCachedWorkspaceResyncsMissingSourceFiles(t *testing.T) {
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

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
	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

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
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

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
	t.Parallel()

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
	writeBuildTestFile(t, root, "go.mod", "module example.com/buildtest\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeBuildTestFile(t, root, ".onlava.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, root, "svc/api.go", `package svc

import "context"

//onlava:api public
func Hello(ctx context.Context) error { return nil }
`)
	return root
}

func newCachedBuildTestWorkspace(t *testing.T, graphFingerprint string) (string, *Result) {
	t.Helper()
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	appDir := t.TempDir()

	const goMod = "module example.com/buildtest\n\ngo 1.26.0\n"
	const serviceSource = `package svc

import "context"

//onlava:api public
func Hello(ctx context.Context) error { return nil }
`
	writeBuildTestFile(t, appDir, ".onlava.json", `{"name":"buildtest"}`)
	writeBuildTestFile(t, appDir, "go.mod", goMod)
	writeBuildTestFile(t, appDir, "svc/api.go", serviceSource)

	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", goMod)
	writeBuildTestFile(t, workspace, "svc/api.go", serviceSource)
	writeBuildTestFile(t, workspace, "svc/onlava.gen.go", "package svc\n")
	writeBuildTestFile(t, workspace, "onlava_internal_main/main.go", "package main\n\nfunc main() {}\n")

	depFingerprint, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	sourceFiles := []string{"go.mod", "svc/api.go"}
	generatedFiles := []string{"onlava_internal_main/main.go", "svc/onlava.gen.go"}
	buildFingerprint, err := workspaceBuildFingerprint(workspace, sourceFiles, generatedFiles)
	if err != nil {
		t.Fatal(err)
	}
	result := &Result{
		AppRoot:               appDir,
		AppName:               "buildtest",
		Dir:                   workspace,
		Binary:                filepath.Join(workspace, workspaceBinaryName(appDir, buildFingerprint)),
		NeedsTidy:             false,
		DependencyFingerprint: depFingerprint,
		BuildFingerprint:      buildFingerprint,
		GraphFingerprint:      graphFingerprint,
		Metadata:              json.RawMessage(`{"ok":true}`),
		APIEncoding:           json.RawMessage(`{"api":"v1"}`),
		SourceFiles:           append([]string(nil), sourceFiles...),
		GeneratedFiles:        append([]string(nil), generatedFiles...),
	}
	if err := saveBuildState(workspace, buildState{
		Version:               buildStateVersion,
		DependencyFingerprint: depFingerprint,
		BuildFingerprint:      buildFingerprint,
		GraphFingerprint:      graphFingerprint,
		Metadata:              append([]byte(nil), result.Metadata...),
		APIEncoding:           append([]byte(nil), result.APIEncoding...),
		SourceFiles:           sourceFiles,
		GeneratedFiles:        generatedFiles,
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
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./onlava_internal_main" {
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })
}
