package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appcfg "scenery.sh/internal/app"
)

func useFakeWorktreeBranchEnsure(t *testing.T) {
	t.Helper()
	prev := ensureDBBranchForWorktreeCreateFn
	ensureDBBranchForWorktreeCreateFn = func(context.Context, appcfg.Config, worktreeDBPin) (dbBranchBackendStatus, error) {
		return dbBranchBackendStatus{Status: "ready", Message: "test branch ready"}, nil
	}
	t.Cleanup(func() { ensureDBBranchForWorktreeCreateFn = prev })
}

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

func TestWorktreeCreateListAndRemoveWithPostgresBranchPin(t *testing.T) {
	useFakeWorktreeBranchEnsure(t)
	agentHome := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	root := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "demo",
		"dev": {
			"services": {
				"postgres": {
					"kind": "postgres",
					"mode": "local",
					"isolation": "database",
					"project": "demo",
					"parent_database": "demo_main",
					"branch_name_template": "{app}/{git_branch}"
				}
			}
		}
	}`)
	runGitForTest(t, root, "init", "-b", "main")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")
	runGitForTest(t, root, "add", ".scenery.json")
	runGitForTest(t, root, "commit", "-m", "initial")

	var createAOut bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &createAOut, []string{"create", "pricing-agent", "--from", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand create A returned error: %v", err)
	}
	var createdA worktreeCreateResult
	if err := json.Unmarshal(createAOut.Bytes(), &createdA); err != nil {
		t.Fatalf("decode create A JSON: %v\n%s", err, createAOut.String())
	}
	if !createdA.OK || createdA.DBPin == nil || createdA.DBPin.Branch != "demo/pricing-agent" {
		t.Fatalf("created A = %+v", createdA)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.worktree.create.v1.schema.json"), createdA); len(diagnostics) != 0 {
		t.Fatalf("create A schema diagnostics = %+v", diagnostics)
	}
	if _, err := os.Stat(filepath.Join(createdA.Path, ".scenery", "worktree-db.json")); err != nil {
		t.Fatalf("target A pin missing: %v", err)
	}

	var createBOut bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &createBOut, []string{"create", "content-agent", "--from", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand create B returned error: %v", err)
	}
	var createdB worktreeCreateResult
	if err := json.Unmarshal(createBOut.Bytes(), &createdB); err != nil {
		t.Fatalf("decode create B JSON: %v\n%s", err, createBOut.String())
	}
	if !createdB.OK || createdB.DBPin == nil || createdB.DBPin.Branch != "demo/content-agent" {
		t.Fatalf("created B = %+v", createdB)
	}
	if createdA.Path == createdB.Path || createdA.DBPin.BranchID == createdB.DBPin.BranchID || createdA.DBPin.Branch == createdB.DBPin.Branch {
		t.Fatalf("created worktrees are not isolated: A=%+v B=%+v", createdA, createdB)
	}
	if _, err := os.Stat(filepath.Join(createdB.Path, ".scenery", "worktree-db.json")); err != nil {
		t.Fatalf("target B pin missing: %v", err)
	}
	var listOut bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &listOut, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand list returned error: %v", err)
	}
	var listed worktreeListResult
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, listOut.String())
	}
	found := map[string]bool{}
	for _, wt := range listed.Worktrees {
		if evalPathForTest(t, wt.Path) == evalPathForTest(t, createdA.Path) && wt.Branch == "pricing-agent" {
			found["pricing-agent"] = true
		}
		if evalPathForTest(t, wt.Path) == evalPathForTest(t, createdB.Path) && wt.Branch == "content-agent" {
			found["content-agent"] = true
		}
	}
	if !found["pricing-agent"] || !found["content-agent"] {
		t.Fatalf("created worktrees not listed: %+v", listed.Worktrees)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.worktree.list.v1.schema.json"), listed); len(diagnostics) != 0 {
		t.Fatalf("list schema diagnostics = %+v", diagnostics)
	}

	for _, name := range []string{"pricing-agent", "content-agent"} {
		var removeOut bytes.Buffer
		if err := runWorktreeCommand(t.Context(), &removeOut, []string{"remove", name, "--app-root", root, "--db", "--json"}); err != nil {
			t.Fatalf("runWorktreeCommand remove %s returned error: %v", name, err)
		}
		var removed worktreeRemoveResult
		if err := json.Unmarshal(removeOut.Bytes(), &removed); err != nil {
			t.Fatalf("decode remove %s JSON: %v\n%s", name, err, removeOut.String())
		}
		if !removed.OK || !removed.DBPinRemoved {
			t.Fatalf("removed %s = %+v", name, removed)
		}
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.worktree.remove.v1.schema.json"), removed); len(diagnostics) != 0 {
			t.Fatalf("remove %s schema diagnostics = %+v", name, diagnostics)
		}
	}
	if _, err := os.Stat(createdA.Path); !os.IsNotExist(err) {
		t.Fatalf("target A path after remove err=%v", err)
	}
	if _, err := os.Stat(createdB.Path); !os.IsNotExist(err) {
		t.Fatalf("target B path after remove err=%v", err)
	}
}

func TestWorktreeCreateRollsBackWhenPostgresBranchPinWriteFails(t *testing.T) {
	useFakeWorktreeBranchEnsure(t)
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","dev":{"services":{"postgres":{"kind":"postgres","mode":"local","isolation":"database","project":"demo","parent_database":"demo_main","branch_name_template":"{app}/{git_branch}"}}}}`)
	runGitForTest(t, root, "init", "-b", "main")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")
	runGitForTest(t, root, "add", ".scenery.json")
	runGitForTest(t, root, "commit", "-m", "initial")

	foreignPin := worktreeDBPin{SchemaVersion: dbBranchPinSchemaVersion, Provider: postgresBranchProviderName, Project: "demo", ParentBranch: dbBranchDefaultParentBranch, Branch: "demo/collision", BranchID: dbLocalBranchID("demo", "demo/collision"), Database: "demo_collision", Role: "scenery", CreatedBy: "external"}
	foreignPin.CreatedBy = "external"
	registryRoot, err := postgresSubstrateRoot()
	if err != nil {
		t.Fatalf("postgresSubstrateRoot: %v", err)
	}
	if err := writePostgresBranchRegistry(registryRoot, dbBranchRegistry{
		SchemaVersion: dbBranchRegistrySchemaVersion,
		Provider:      postgresBranchProviderName,
		Leases: []dbBranchLease{{
			Pin:       foreignPin,
			Status:    "pending",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}},
	}); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	target := defaultWorktreePath(root, "collision")
	err = runWorktreeCommand(t.Context(), &bytes.Buffer{}, []string{"create", "collision", "--from", "main", "--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), "refusing to reuse foreign local Postgres branch lease") {
		t.Fatalf("create error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("rolled-back worktree still exists, stat err=%v", err)
	}
	worktrees, err := listGitWorktrees(t.Context(), root)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	for _, wt := range worktrees {
		if evalPathForTestAllowMissing(t, wt.Path) == target {
			t.Fatalf("rolled-back worktree still registered: %+v", worktrees)
		}
	}
}

func TestWorktreeCreateSkipsPostgresBranchPinForManualBranchPolicy(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","dev":{"services":{"postgres":{"kind":"postgres","mode":"local","isolation":"database","project":"demo","branch_policy":"manual"}}}}`)
	runGitForTest(t, root, "init", "-b", "main")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")
	runGitForTest(t, root, "add", ".scenery.json")
	runGitForTest(t, root, "commit", "-m", "initial")

	var out bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &out, []string{"create", "manual-agent", "--from", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand create returned error: %v", err)
	}
	var created worktreeCreateResult
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode create JSON: %v\n%s", err, out.String())
	}
	if created.DBPin != nil {
		t.Fatalf("manual policy should not auto-pin db branch: %+v", created.DBPin)
	}
	if _, err := os.Stat(worktreeDBPinPath(created.Path)); !os.IsNotExist(err) {
		t.Fatalf("manual policy wrote db pin, stat err=%v", err)
	}
}

func TestWorktreeRemoveRestoresDBStateWhenGitRemoveFails(t *testing.T) {
	useFakeWorktreeBranchEnsure(t)
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","dev":{"services":{"postgres":{"kind":"postgres","mode":"local","isolation":"database","project":"demo","parent_database":"demo_main","branch_name_template":"{app}/{git_branch}"}}}}`)
	runGitForTest(t, root, "init", "-b", "main")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")
	runGitForTest(t, root, "add", ".scenery.json")
	runGitForTest(t, root, "commit", "-m", "initial")

	var out bytes.Buffer
	if err := runWorktreeCommand(t.Context(), &out, []string{"create", "dirty-agent", "--from", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runWorktreeCommand create returned error: %v", err)
	}
	var created worktreeCreateResult
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode create JSON: %v\n%s", err, out.String())
	}
	writeTestAppFile(t, created.Path, ".scenery.json", `{"name":"demo","dirty":true}`)
	err := runWorktreeCommand(t.Context(), &bytes.Buffer{}, []string{"remove", "dirty-agent", "--app-root", root, "--db", "--json"})
	if err == nil {
		t.Fatal("remove should fail for dirty worktree")
	}
	if _, err := os.Stat(worktreeDBPinPath(created.Path)); err != nil {
		t.Fatalf("db state was not restored after failed remove: %v", err)
	}
}

func TestWorktreeRemoveDoesNotDeleteStateForUnlistedTarget(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo"}`)
	runGitForTest(t, root, "init", "-b", "main")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")
	runGitForTest(t, root, "add", ".scenery.json")
	runGitForTest(t, root, "commit", "-m", "initial")

	unlisted := defaultWorktreePath(root, "mistyped")
	writeTestAppFile(t, unlisted, ".scenery/worktree-db.json", `{"sentinel":true}`)
	err := runWorktreeCommand(t.Context(), &bytes.Buffer{}, []string{"remove", "mistyped", "--app-root", root, "--db", "--json"})
	if err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("remove error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(unlisted, ".scenery", "worktree-db.json")); err != nil {
		t.Fatalf("unlisted target state was removed: %v", err)
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

func evalPathForTestAllowMissing(t *testing.T, path string) string {
	t.Helper()
	evaluated, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return evaluated
}
