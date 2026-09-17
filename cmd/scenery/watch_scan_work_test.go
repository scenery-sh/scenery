package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/dirlisting"
)

func writeScanFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// settleDirectoryListings makes every directory old enough for its listing to
// be reused as soon as it is read.
func settleDirectoryListings(t *testing.T) {
	t.Helper()
	t.Cleanup(dirlisting.SetClockForTesting(func() time.Time { return time.Now().Add(time.Hour) }))
}

func scanServiceTree(t *testing.T, packages int) string {
	t.Helper()
	root := t.TempDir()
	for index := range packages {
		name := "pkg" + strconv.Itoa(index)
		writeScanFile(t, root, "services/"+name+"/service.go", "package "+name+"\n\nconst message = \"a\"\n")
	}
	return root
}

// The mandatory work of a rescan follows what changed, not the size of the
// tree: an unchanged tree reads no directory listing and hashes no file, and a
// body edit hashes only the edited file.
func TestWatchRescanWorkFollowsChangesNotTreeSize(t *testing.T) {
	settleDirectoryListings(t)
	root := scanServiceTree(t, 60)
	first, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	directories := first.scanStats.dirsRead + first.scanStats.dirsReused
	if len(first.files) != 60 || directories < 62 {
		t.Fatalf("first scan captured %d files in %d directories", len(first.files), directories)
	}
	unchanged, err := scanWatchedFilesReusing(root, first)
	if err != nil {
		t.Fatal(err)
	}
	if stats := unchanged.scanStats; stats.dirsRead != 0 || stats.dirsReused != directories || stats.filesHashed != 0 {
		t.Fatalf("rescan of an unchanged tree = %+v", stats)
	}
	writeScanFile(t, root, "services/pkg7/service.go", "package pkg7\n\nconst message = \"b\"\n")
	edited, err := scanWatchedFilesReusing(root, unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if stats := edited.scanStats; stats.dirsRead != 0 || stats.filesHashed != 1 || snapshotsEqual(unchanged, edited) {
		t.Fatalf("rescan after a body edit = %+v (changed %t)", stats, !snapshotsEqual(unchanged, edited))
	}
	fresh, err := scanWatchedFilesFresh(root, edited)
	if err != nil {
		t.Fatal(err)
	}
	if stats := fresh.scanStats; stats.dirsReused != 0 || stats.dirsRead != directories || !buildInputSnapshotsEqual(edited, fresh) {
		t.Fatalf("fresh scan = %+v (equal %t)", stats, buildInputSnapshotsEqual(edited, fresh))
	}
}

// A rescan reusing directory listings observes the files and generated claims
// an independent walk with os.ReadDir observes, after membership changes whose
// directory modification times were restored.
func TestWatchScanMatchesAnIndependentWalkAfterRestoredDirectoryTimes(t *testing.T) {
	settleDirectoryListings(t)
	root := scanServiceTree(t, 8)
	first, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := scanWatchedFilesReusing(root, first)
	if err != nil {
		t.Fatal(err)
	}
	restoring := func(rel string, change func()) {
		t.Helper()
		dir := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		change()
		if err := os.Chtimes(dir, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	restoring("services/pkg1", func() {
		if err := os.Rename(filepath.Join(root, "services/pkg1/service.go"), filepath.Join(root, "services/pkg1/renamed.go")); err != nil {
			t.Fatal(err)
		}
	})
	restoring("services/pkg2", func() { writeScanFile(t, root, "services/pkg2/package.scn", "service \"pkg2\" {}\n") })
	restoring("services/pkg3", func() {
		writeScanFile(t, root, "services/pkg3/scenery.generated.json", `{"kind":"scenery.generated","files":["claimed.go"]}`)
		writeScanFile(t, root, "services/pkg3/claimed.go", "package pkg3\n")
	})
	restoring("services", func() {
		if err := os.RemoveAll(filepath.Join(root, "services/pkg4")); err != nil {
			t.Fatal(err)
		}
	})
	current, err := scanWatchedFilesReusing(root, previous)
	if err != nil {
		t.Fatal(err)
	}

	var files, claims []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if entry.Name() == "scenery.generated.json" {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var descriptor struct{ Files []string }
			if err := json.Unmarshal(data, &descriptor); err != nil {
				return err
			}
			claims = append(claims, rel)
			for _, file := range descriptor.Files {
				claims = append(claims, filepath.ToSlash(filepath.Join(filepath.Dir(rel), file)))
			}
			return nil
		}
		if isWatchedFile(rel) {
			files = append(files, rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	files = slices.DeleteFunc(files, func(rel string) bool { return slices.Contains(claims, rel) })
	captured := make([]string, 0, len(current.files))
	for rel := range current.files {
		captured = append(captured, rel)
	}
	generated := make([]string, 0, len(current.generated))
	for rel := range current.generated {
		generated = append(generated, rel)
	}
	for _, values := range [][]string{files, captured, claims, generated} {
		slices.Sort(values)
	}
	if !slices.Equal(captured, files) {
		t.Fatalf("scan captured %v, an independent walk found %v", captured, files)
	}
	if !slices.Equal(generated, claims) {
		t.Fatalf("scan found generated %v, an independent walk found %v", generated, claims)
	}
	reference, err := compiler.ReconcileGeneratedPaths(root)
	if err != nil || len(reference) != len(claims) {
		t.Fatalf("reconciled generated paths = %v, %v", reference, err)
	}
	if !strings.Contains(strings.Join(captured, " "), "services/pkg1/renamed.go") {
		t.Fatal("the renamed file is missing")
	}
}
