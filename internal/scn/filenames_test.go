package scn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceFilesUsesRoleNamedContractFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{AppFilename, AppLockFilename, "extra.scn"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# source\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := SourceFiles(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || filepath.Base(paths[0]) != AppFilename || filepath.Base(paths[1]) != "extra.scn" {
		t.Fatalf("root source paths = %v", paths)
	}

	packageRoot := t.TempDir()
	for _, name := range []string{AppFilename, PackageFilename, AppLockFilename, "types.scn"} {
		if err := os.WriteFile(filepath.Join(packageRoot, name), []byte("# source\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err = SourceFiles(packageRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || filepath.Base(paths[0]) != PackageFilename || filepath.Base(paths[1]) != "types.scn" {
		t.Fatalf("package source paths = %v", paths)
	}
}
