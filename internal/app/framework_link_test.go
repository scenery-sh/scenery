package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameworkLinkLifecycleAndModFile(t *testing.T) {
	t.Parallel()
	appRoot := t.TempDir()
	origin := t.TempDir()
	if err := os.WriteFile(filepath.Join(origin, "go.mod"), []byte("module scenery.sh\n\ngo 1.27.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "go.sum"), []byte("example.test/from-scenery v1.0.0 h1:abc=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "go.mod"), []byte("module example.test/app\n\ngo 1.27.0\n\nrequire scenery.sh v0.3.7\n\nreplace example.test/local => ./local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "go.sum"), []byte("example.test/from-app v1.0.0 h1:xyz=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, linked, err := ReadFrameworkLink(appRoot); err != nil || linked {
		t.Fatalf("expected unlinked app, got linked=%v err=%v", linked, err)
	}

	written, err := WriteFrameworkLink(appRoot, origin)
	if err != nil {
		t.Fatal(err)
	}
	read, linked, err := ReadFrameworkLink(appRoot)
	if err != nil || !linked {
		t.Fatalf("expected linked app, got linked=%v err=%v", linked, err)
	}
	if read.AppRoot != written.AppRoot || read.SourceOrigin != written.SourceOrigin {
		t.Fatalf("link mismatch: got %+v want %+v", read, written)
	}

	modPath, err := SyncLinkedGoModFile(appRoot, written.SourceOrigin)
	if err != nil {
		t.Fatal(err)
	}
	modBytes, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatal(err)
	}
	modText := string(modBytes)
	for _, want := range []string{
		"replace scenery.sh => " + written.SourceOrigin,
		"replace example.test/local => " + filepath.Join(written.AppRoot, "local"),
	} {
		if !strings.Contains(modText, want) {
			t.Fatalf("linked go.mod missing %q:\n%s", want, modText)
		}
	}
	sumBytes, err := os.ReadFile(FrameworkGoSumPath(appRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.test/from-app v1.0.0 h1:xyz=", "example.test/from-scenery v1.0.0 h1:abc="} {
		if !strings.Contains(string(sumBytes), want) {
			t.Fatalf("linked go.sum missing %q:\n%s", want, string(sumBytes))
		}
	}

	// Tracked app go.mod must remain completely untouched.
	appMod, err := os.ReadFile(filepath.Join(appRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(appMod), "replace scenery.sh") {
		t.Fatalf("tracked app go.mod was modified:\n%s", string(appMod))
	}

	removed, err := RemoveFrameworkLink(appRoot)
	if err != nil || !removed {
		t.Fatalf("RemoveFrameworkLink removed=%v err=%v", removed, err)
	}
	if _, linked, err := ReadFrameworkLink(appRoot); err != nil || linked {
		t.Fatalf("expected unlinked after removal, got linked=%v err=%v", linked, err)
	}
	if _, err := os.Stat(modPath); !os.IsNotExist(err) {
		t.Fatalf("expected linked go.mod removed, err=%v", err)
	}
}

func TestWriteFrameworkLinkRejectsNonSceneryModule(t *testing.T) {
	t.Parallel()
	appRoot := t.TempDir()
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.test/not-scenery\n\ngo 1.27.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFrameworkLink(appRoot, other); err == nil || !strings.Contains(err.Error(), "not the scenery.sh module") {
		t.Fatalf("expected non-scenery error, got %v", err)
	}
}
