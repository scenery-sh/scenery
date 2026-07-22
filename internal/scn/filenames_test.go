package scn

import (
	"errors"
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

func TestSourceFilesRejectsEveryLegacyContractFilename(t *testing.T) {
	tests := []struct {
		legacy      string
		replacement string
	}{
		{LegacyAppFilename, AppFilename},
		{LegacyPackageFilename, PackageFilename},
		{LegacyAppLockFilename, AppLockFilename},
	}
	for _, test := range tests {
		t.Run(test.legacy, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, test.legacy)
			if err := os.WriteFile(path, []byte("# retired\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := SourceFiles(root, true)
			var legacyErr *LegacyFilenameError
			if !errors.As(err, &legacyErr) {
				t.Fatalf("SourceFiles() error = %T %v", err, err)
			}
			if legacyErr.Path != path || legacyErr.Legacy != test.legacy || legacyErr.Replacement != test.replacement {
				t.Fatalf("legacy error = %#v", legacyErr)
			}
		})
	}
}

func TestFormatPathsRejectsSelectedLegacyContractFilename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LegacyAppFilename)
	if err := os.WriteFile(path, []byte(`application "legacy" {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FormatPaths(root, []string{path}, true)
	var legacyErr *LegacyFilenameError
	if !errors.As(err, &legacyErr) || legacyErr.Legacy != LegacyAppFilename || legacyErr.Replacement != AppFilename {
		t.Fatalf("FormatPaths() error = %T %v", err, err)
	}
}
