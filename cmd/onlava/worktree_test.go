package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWorktreeArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseWorktreeArgs([]string{"create", "pricing-agent", "--from", "main", "--app-root", "/tmp/app", "--json"})
	if err != nil {
		t.Fatalf("parseWorktreeArgs returned error: %v", err)
	}
	if opts.Command != "create" || opts.Name != "pricing-agent" || opts.From != "main" || opts.AppRoot != "/tmp/app" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseWorktreeArgs([]string{"create"}); err == nil || !strings.Contains(err.Error(), "requires <name>") {
		t.Fatalf("missing name error = %v", err)
	}
}

func TestWorktreeCreateListAndRemoveWithNeonPin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".onlava.json", `{
		"name": "demo",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"isolation": "branch",
					"project": "demo",
					"branch_name_template": "{app}/{git_branch}"
				}
			}
		}
	}`)
	runGitForTest(t, root, "init", "-b", "main")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")
	runGitForTest(t, root, "add", ".onlava.json")
	runGitForTest(t, root, "commit", "-m", "initial")

	var createOut bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &createOut, []string{"create", "pricing-agent", "--from", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand create returned error: %v", err)
	}
	var created worktreeCreateResult
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatalf("decode create JSON: %v\n%s", err, createOut.String())
	}
	if !created.OK || created.DBPin == nil || created.DBPin.Branch != "demo/pricing-agent" {
		t.Fatalf("created = %+v", created)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.worktree.create.v1.schema.json"), created); len(diagnostics) != 0 {
		t.Fatalf("create schema diagnostics = %+v", diagnostics)
	}
	if _, err := os.Stat(filepath.Join(created.Path, ".onlava", "worktree-db.json")); err != nil {
		t.Fatalf("target pin missing: %v", err)
	}

	var listOut bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &listOut, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand list returned error: %v", err)
	}
	var listed worktreeListResult
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, listOut.String())
	}
	var found bool
	for _, wt := range listed.Worktrees {
		if evalPathForTest(t, wt.Path) == evalPathForTest(t, created.Path) && wt.Branch == "pricing-agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created worktree not listed: %+v", listed.Worktrees)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.worktree.list.v1.schema.json"), listed); len(diagnostics) != 0 {
		t.Fatalf("list schema diagnostics = %+v", diagnostics)
	}

	var removeOut bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &removeOut, []string{"remove", "pricing-agent", "--app-root", root, "--db", "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand remove returned error: %v", err)
	}
	var removed worktreeRemoveResult
	if err := json.Unmarshal(removeOut.Bytes(), &removed); err != nil {
		t.Fatalf("decode remove JSON: %v\n%s", err, removeOut.String())
	}
	if !removed.OK || !removed.DBPinRemoved {
		t.Fatalf("removed = %+v", removed)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.worktree.remove.v1.schema.json"), removed); len(diagnostics) != 0 {
		t.Fatalf("remove schema diagnostics = %+v", diagnostics)
	}
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("target path after remove err=%v", err)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func evalPathForTest(t *testing.T, path string) string {
	t.Helper()
	evaluated, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks %s: %v", path, err)
	}
	return evaluated
}
