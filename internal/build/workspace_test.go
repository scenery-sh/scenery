package build

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
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
	t.Parallel()

	root := t.TempDir()
	dst := t.TempDir()
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
	t.Parallel()

	appDir := t.TempDir()

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
		".scenery/gen/app.json":       `"schema_version": "scenery.inspect.app.v1"`,
		".scenery/gen/routes.json":    `"schema_version": "scenery.inspect.routes.v1"`,
		".scenery/gen/services.json":  `"schema_version": "scenery.inspect.services.v1"`,
		".scenery/gen/endpoints.json": `"schema_version": "scenery.inspect.endpoints.v1"`,
		".scenery/gen/models.json":    `"schema_version": "scenery.inspect.models.v1"`,
		".scenery/gen/views.json":     `"schema_version": "scenery.inspect.views.v1"`,
		".scenery/gen/manifest.json":  `"schema_version": "scenery.gen.manifest.v1"`,
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
			App         string `json:"app"`
			Routes      string `json:"routes"`
			Services    string `json:"services"`
			Endpoints   string `json:"endpoints"`
			Models      string `json:"models"`
			Views       string `json:"views"`
			BuildLatest string `json:"build_latest"`
		} `json:"artifacts"`
		Schemas struct {
			App         string `json:"app"`
			Routes      string `json:"routes"`
			Services    string `json:"services"`
			Endpoints   string `json:"endpoints"`
			Models      string `json:"models"`
			Views       string `json:"views"`
			BuildLatest string `json:"build_latest"`
		} `json:"schemas"`
		Hashes struct {
			App       string `json:"app"`
			Routes    string `json:"routes"`
			Services  string `json:"services"`
			Endpoints string `json:"endpoints"`
			Models    string `json:"models"`
			Views     string `json:"views"`
		} `json:"hashes"`
	}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest.json): %v", err)
	}
	if manifest.Artifacts.App != ".scenery/gen/app.json" || manifest.Artifacts.Endpoints != ".scenery/gen/endpoints.json" || manifest.Artifacts.Models != ".scenery/gen/models.json" || manifest.Artifacts.Views != ".scenery/gen/views.json" || manifest.Artifacts.BuildLatest != ".scenery/build/latest.json" {
		t.Fatalf("manifest artifacts = %+v", manifest.Artifacts)
	}
	if manifest.Schemas.App != "scenery.inspect.app.v1" || manifest.Schemas.Endpoints != "scenery.inspect.endpoints.v1" || manifest.Schemas.Models != "scenery.inspect.models.v1" || manifest.Schemas.Views != "scenery.inspect.views.v1" || manifest.Schemas.BuildLatest != "scenery.build.latest.v1" {
		t.Fatalf("manifest schemas = %+v", manifest.Schemas)
	}
	if manifest.Hashes.App == "" || manifest.Hashes.Routes == "" || manifest.Hashes.Services == "" || manifest.Hashes.Endpoints == "" || manifest.Hashes.Models == "" || manifest.Hashes.Views == "" {
		t.Fatalf("manifest hashes = %+v", manifest.Hashes)
	}
}
