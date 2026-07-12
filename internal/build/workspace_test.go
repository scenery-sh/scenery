package build

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

}
