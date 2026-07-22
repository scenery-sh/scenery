package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/scn"
)

func TestCompileReportsEveryLegacyContractFilenameRename(t *testing.T) {
	tests := []struct {
		name        string
		legacy      string
		replacement string
		prepare     func(t *testing.T, root string)
	}{
		{
			name: "app", legacy: scn.LegacyAppFilename, replacement: scn.AppFilename,
			prepare: func(t *testing.T, root string) {
				writeLegacyFilenameTestFile(t, filepath.Join(root, scn.LegacyAppFilename), `application "legacy" {}`)
			},
		},
		{
			name: "lock", legacy: scn.LegacyAppLockFilename, replacement: scn.AppLockFilename,
			prepare: func(t *testing.T, root string) {
				writeLegacyFilenameTestFile(t, filepath.Join(root, scn.AppFilename), `application "legacy" {}`)
				writeLegacyFilenameTestFile(t, filepath.Join(root, scn.LegacyAppLockFilename), `lock {}`)
			},
		},
		{
			name: "package", legacy: scn.LegacyPackageFilename, replacement: scn.PackageFilename,
			prepare: func(t *testing.T, root string) {
				writeLegacyFilenameTestFile(t, filepath.Join(root, scn.AppFilename), `
application "legacy" {}
module "example" { source = "./example" }
`)
				writeLegacyFilenameTestFile(t, filepath.Join(root, "example", scn.LegacyPackageFilename), `package "example" {}`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.prepare(t, root)
			result, err := Compile(root)
			if err != nil {
				t.Fatal(err)
			}
			if result.Valid() || result.Manifest != nil {
				t.Fatalf("retired filename compiled as an alias: valid=%t manifest=%#v", result.Valid(), result.Manifest)
			}
			var got *Diagnostic
			for index := range result.Diagnostics {
				if result.Diagnostics[index].Code == "SCN1021" {
					got = &result.Diagnostics[index]
					break
				}
			}
			if got == nil {
				t.Fatalf("diagnostics = %#v", result.Diagnostics)
			}
			if got.Details["legacy_filename"] != test.legacy || got.Details["replacement_filename"] != test.replacement {
				t.Fatalf("details = %#v", got.Details)
			}
			wantSuggestion := `rename "` + test.legacy + `" to "` + test.replacement + `"`
			if len(got.Suggestions) != 1 || got.Suggestions[0] != wantSuggestion {
				t.Fatalf("suggestions = %#v, want %q", got.Suggestions, wantSuggestion)
			}
		})
	}
}

func writeLegacyFilenameTestFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
