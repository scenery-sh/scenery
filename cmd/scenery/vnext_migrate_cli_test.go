package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/vnext"
)

func TestVNextMigrateAppliesRetainedTransitionPlan(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join("..", "..", "internal", "vnext", "testdata", "native")
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "../../../..", filepath.ToSlash(repositoryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	migration := `migration {
  frontend      = "scenery.legacy.v0"
  legacy_config = ".scenery.json"

  legacy_gateway "default" {
    target = http_gateway.public_api
  }

  legacy_service "house" {
    package   = "./house"
    namespace = "house"
    target    = go_target.development
  }
}
`
	if err := os.WriteFile(filepath.Join(root, "scenery.migration.scn"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "shadow-plan.json")
	var planned bytes.Buffer
	if err := runVNextMigrate(&planned, []string{
		"service", "house", "--shadow", "--dry-run", "--out", planPath, "--app-root", root, "-o", "json",
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan vnext.MigrationPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.PlanID == "" || plan.Action != "shadow" {
		t.Fatalf("retained plan = %#v", plan)
	}
	before, err := vnext.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runVNextMigrate(&bytes.Buffer{}, []string{"apply", planPath, "--app-root", root, "--dry-run", "-o", "json"}); err == nil || !strings.Contains(err.Error(), "not valid for scenery migrate apply") {
		t.Fatalf("apply --dry-run error = %v", err)
	}
	after, err := vnext.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorkspaceRevision != before.WorkspaceRevision {
		t.Fatalf("apply --dry-run mutated workspace: before=%s after=%s", before.WorkspaceRevision, after.WorkspaceRevision)
	}
	if err := runVNextMigrate(&bytes.Buffer{}, []string{"apply", "--plan", planPath, "--app-root", root}); err == nil || !strings.Contains(err.Error(), `unknown flag "--plan"`) {
		t.Fatalf("undocumented --plan alias error = %v", err)
	}
	var output bytes.Buffer
	err = runVNextMigrate(&output, []string{
		"apply", planPath, "--app-root", root, "-o", "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK   bool                   `json:"ok"`
		Data vnext.MigrationReceipt `json:"data"`
	}
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Data.PlanID != plan.PlanID || envelope.Data.Action != "shadow" {
		t.Fatalf("apply response = %#v", envelope)
	}
}

func TestReadMigrationTransitionPlanRequiresExactJSON(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "unknown_field", content: `{"api_version":"scenery.migrate.plan.v1","unexpected":true}`, want: `unknown field "unexpected"`},
		{name: "trailing_value", content: `{} {}`, want: "trailing JSON value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readMigrationTransitionPlan(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decode error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVNextApplyCommandsRejectUnknownPlanFields(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte("language { edition = \"2027\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		run  func(io.Writer, []string) error
		args []string
	}{
		{name: "changes", run: runVNextChanges, args: []string{"apply", planPath, "--app-root", root, "--expect-workspace-revision", "sha256:base", "--expect-contract-revision", "sha256:contract"}},
		{name: "deployment", run: runVNextDeploy, args: []string{"apply", planPath, "--app-root", root, "--expect-workspace-revision", "sha256:base", "--expect-contract-revision", "sha256:contract"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(io.Discard, test.args); err == nil || !strings.Contains(err.Error(), `unknown field "unexpected"`) {
				t.Fatalf("apply decode error = %v", err)
			}
		})
	}
}
