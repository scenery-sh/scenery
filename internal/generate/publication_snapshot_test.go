package generate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/lifecycle"
)

func TestGenerationRejectsSourceEditDuringRendering(t *testing.T) {
	t.Parallel()
	for _, check := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "check"}[check], func(t *testing.T) {
			root := t.TempDir()
			writeMinimalGenerationFixture(t, root)
			compiled, err := compiler.Compile(root)
			if err != nil {
				t.Fatal(err)
			}
			op, err := newGenerationOperation(compiled, "contracts", check)
			if err != nil {
				t.Fatal(err)
			}
			_, err = op.run(context.Background(), func(owner bool) ([]lifecycle.Node, error) {
				nodes, err := op.plan(owner)
				for i := range nodes {
					if nodes[i].ID == "go.render" {
						action := nodes[i].Action
						nodes[i].Action = func(ctx context.Context) error {
							if err := action(ctx); err != nil {
								return err
							}
							appendFixtureFile(t, filepath.Join(root, testAppFilename), "\n// concurrent source edit\n")
							return nil
						}
					}
				}
				return nodes, err
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

func TestCheckOnlyReportsDriftWithoutRepair(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderGoContractFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, files)
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
}
