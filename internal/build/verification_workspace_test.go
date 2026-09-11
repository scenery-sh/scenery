package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedWorkspaceRejectsChangedBytesAndMembership(t *testing.T) {
	for _, change := range []string{"source", "generated", "module", "added", "deleted", "moved", "symlink"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			writeBuildTestFile(t, root, "go.mod", "module example.test/prepared\n")
			writeBuildTestFile(t, root, "source.go", "package sample\nconst Name = 1\n")
			writeBuildTestFile(t, root, "generated.go", "package sample\nconst Generated = 1\n")
			result := &Result{Dir: root, SourceFiles: []string{"go.mod", "source.go"}, GeneratedFiles: []string{"generated.go"}}
			var err error
			result.BuildFingerprint, err = workspaceBuildFingerprint(root, nil, result.SourceFiles, result.GeneratedFiles)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyPreparedWorkspace(result); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "source.go")
			switch change {
			case "source", "generated", "module":
				switch change {
				case "generated":
					path = filepath.Join(root, "generated.go")
				case "module":
					path = filepath.Join(root, "go.mod")
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)-2] ^= 1
				if err := os.WriteFile(path, data, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "added":
				writeBuildTestFile(t, root, "new.go", "package sample\n")
			case "deleted":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "moved":
				if err := os.Rename(path, filepath.Join(root, "moved.go")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "generated.go"), path); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyPreparedWorkspace(result); err == nil {
				t.Fatalf("prepared workspace accepted %s mutation", change)
			}
		})
	}
}
