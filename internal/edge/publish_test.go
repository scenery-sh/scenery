package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePublishFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublishFrontendArtifactCopiesNestedAssetsAndSwitchesCurrent(t *testing.T) {
	source := t.TempDir()
	root := t.TempDir()
	writePublishFixture(t, source, map[string]string{
		"index.html":           "<html>app</html>",
		"assets/app-abc123.js": "js",
		"models/scene.glb":     "glb",
	})
	record, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: root,
		AppID:         "microgrid-platform",
		Frontend:      "platform",
		SourceDir:     source,
		ReleaseID:     "20260715T000000Z",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if record.Files != 3 {
		t.Fatalf("expected 3 files, got %d", record.Files)
	}
	releaseDir, entry, err := CurrentPublishedRelease(record.CurrentPath)
	if err != nil {
		t.Fatalf("current release: %v", err)
	}
	if !entry {
		t.Fatal("entry document missing")
	}
	if releaseDir != record.ReleaseDir {
		t.Fatalf("current %s != release %s", releaseDir, record.ReleaseDir)
	}
	data, err := os.ReadFile(filepath.Join(releaseDir, "assets", "app-abc123.js"))
	if err != nil || string(data) != "js" {
		t.Fatalf("nested asset content: %q err %v", data, err)
	}
	info, err := os.Stat(filepath.Join(releaseDir, "index.html"))
	if err != nil || info.Mode().Perm()&0o444 != 0o444 {
		t.Fatalf("published file must be world readable, got %v err %v", info.Mode(), err)
	}
}

func TestPublishFrontendArtifactRejectsMissingEntryDocument(t *testing.T) {
	source := t.TempDir()
	writePublishFixture(t, source, map[string]string{"assets/app.js": "js"})
	_, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: t.TempDir(), AppID: "app", Frontend: "web", SourceDir: source,
	})
	if err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("expected entry document error, got %v", err)
	}
}

func TestPublishFrontendArtifactRejectsSymlinks(t *testing.T) {
	source := t.TempDir()
	writePublishFixture(t, source, map[string]string{"index.html": "x"})
	if err := os.Symlink("/etc/hosts", filepath.Join(source, "escape")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: t.TempDir(), AppID: "app", Frontend: "web", SourceDir: source,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestPublishFrontendArtifactRejectsUnsafeIdentifiers(t *testing.T) {
	source := t.TempDir()
	writePublishFixture(t, source, map[string]string{"index.html": "x"})
	for _, bad := range []struct{ app, frontend string }{
		{"../escape", "web"},
		{"app", "../web"},
		{"app", "we/b"},
		{"", "web"},
	} {
		_, err := PublishFrontendArtifact(PublishInput{
			ArtifactsRoot: t.TempDir(), AppID: bad.app, Frontend: bad.frontend, SourceDir: source,
		})
		if err == nil {
			t.Fatalf("expected identifier rejection for %+v", bad)
		}
	}
}

func TestPublishFrontendArtifactRepeatAndRetention(t *testing.T) {
	source := t.TempDir()
	root := t.TempDir()
	writePublishFixture(t, source, map[string]string{"index.html": "x"})
	var last PublishedFrontend
	releases := []string{"r1", "r2", "r3", "r4", "r5"}
	for _, id := range releases {
		record, err := PublishFrontendArtifact(PublishInput{
			ArtifactsRoot: root, AppID: "app", Frontend: "web", SourceDir: source, ReleaseID: id,
		})
		if err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
		last = record
	}
	if _, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: root, AppID: "app", Frontend: "web", SourceDir: source, ReleaseID: "r5",
	}); err == nil {
		t.Fatal("expected duplicate release id rejection")
	}
	releaseDir, _, err := CurrentPublishedRelease(last.CurrentPath)
	if err != nil || filepath.Base(releaseDir) != "r5" {
		t.Fatalf("current should be r5, got %s err %v", releaseDir, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "app", "web"))
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) != publishRetainReleases {
		t.Fatalf("expected %d retained releases, got %v", publishRetainReleases, dirs)
	}
}

func TestPublishFrontendArtifactCleansInterruptedStaging(t *testing.T) {
	source := t.TempDir()
	root := t.TempDir()
	writePublishFixture(t, source, map[string]string{"index.html": "x"})
	frontendDir := filepath.Join(root, "app", "web")
	if err := os.MkdirAll(filepath.Join(frontendDir, ".staging-crashed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: root, AppID: "app", Frontend: "web", SourceDir: source, ReleaseID: "r1",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(frontendDir, ".staging-crashed")); !os.IsNotExist(err) {
		t.Fatalf("stale staging directory should be removed, err %v", err)
	}
}

func TestCurrentPublishedReleaseRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	frontendDir := filepath.Join(root, "app", "web")
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(frontendDir, "current")
	if err := os.Symlink("../../elsewhere", current); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, _, err := CurrentPublishedRelease(current); err == nil {
		t.Fatal("expected escape rejection")
	}
}
