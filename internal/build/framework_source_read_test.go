package build

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFrameworkSourceListingReadsEachGoFileOnce(t *testing.T) {
	root := t.TempDir()
	write := func(relative, contents string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module scenery.sh\n")
	write("assets/embed.go", "package assets\n\nimport _ \"embed\"\n\n//go:embed page.html\nvar page string\n")
	write("assets/page.html", "<p>page</p>\n")
	write("assets/unselected.html", "<p>no</p>\n")
	for index := range 40 {
		write(filepath.ToSlash(filepath.Join("pkg", "file"+string(rune('a'+index%26))+string(rune('a'+index/26))+".go")), "package pkg\n")
	}
	files, infos, err := frameworkSourceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files, "assets/page.html") || slices.Contains(files, "assets/unselected.html") || !slices.Contains(files, "assets/embed.go") || len(infos) != 41 {
		t.Fatalf("listing = %d files, %d stamped Go files: %v", len(files), len(infos), files)
	}
	// The listing's one read of a Go file also retained its digest.
	unread := func(string) ([]byte, error) { return nil, errors.New("the file was read a second time") }
	for relative, info := range infos {
		if _, retained, err := cachedBuildInputFileDigest(filepath.Join(root, filepath.FromSlash(relative)), info, unread); err != nil || !retained {
			t.Fatalf("%s: retained %t, %v", relative, retained, err)
		}
	}
	again, _, err := frameworkSourceFiles(root)
	if err != nil || !slices.Equal(again, files) {
		t.Fatalf("second listing = %v, %v", again, err)
	}
}
