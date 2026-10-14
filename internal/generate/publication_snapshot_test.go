package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestGenerationRejectsSourceEditDuringRendering(t *testing.T) {
	t.Parallel()
	for _, check := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "check"}[check], func(t *testing.T) {
			root := t.TempDir()
			writeMinimalGenerationFixture(t, root)
			_, err := generateFromRoot(root, check, "stale", func(result *compiler.Result) ([]generatedFile, error) {
				files, err := renderGoContractFiles(result)
				if err != nil {
					return nil, err
				}
				appendFixtureFile(t, filepath.Join(root, testAppFilename), "\n// concurrent source edit\n")
				return files, nil
			})
			if err == nil || !strings.Contains(err.Error(), "revision_conflict") {
				t.Fatalf("generation accepted a superseded snapshot: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "house", "scenerycontract", "types.gen.go")); !os.IsNotExist(err) {
				t.Fatalf("superseded generation published output: %v", err)
			}
		})
	}
}

func TestCheckRejectsSupersededCompilerSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	appendFixtureFile(t, filepath.Join(root, testAppFilename), "\n// changed after capture\n")
	if _, err := GenerateGoContractsFromResult(result, true); err == nil || !strings.Contains(err.Error(), "revision_conflict") {
		t.Fatalf("check accepted stale snapshot: %v", err)
	}
}

func TestCheckOnlyReportsDriftAndExplicitGenerationRepairsIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderGoContractFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	path := files[0].Path
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", testPackageFilename)
	source, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packagePath, []byte(strings.Replace(string(source), "type = string", "type = int64", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	checked, err := GenerateGoContracts(root, true)
	if err == nil || len(checked.Changed) == 0 {
		t.Fatalf("check did not report drift: %#v %v", checked, err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(before) {
		t.Fatalf("check repaired output: %v", err)
	}
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	if checked, err := GenerateGoContracts(root, true); err != nil || len(checked.Changed) != 0 {
		t.Fatalf("explicit generation did not repair drift: %#v %v", checked, err)
	}
}
