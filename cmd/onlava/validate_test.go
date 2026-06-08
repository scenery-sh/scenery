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
)

func validationFixtureRoot(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", config)
	writeTestAppFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	return root
}

func TestInspectValidationProfiles(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"tasks": {
			"ok": {"run": "printf ok"}
		},
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {
					"description": "Fast gate",
					"cost": "low",
					"steps": ["task:ok"],
					"artifacts": ["report.txt"]
				},
				"bad": {
					"cost": "expensive",
					"steps": ["task:missing"]
				}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runOnlavaInspect([]string{"validation", "--app-root", root, "--json"}, &out); err != nil {
		t.Fatalf("inspect validation: %v", err)
	}
	var resp inspectValidationResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if resp.SchemaVersion != validationInspectSchema || resp.Default != "quick" || len(resp.Profiles) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Diagnostics) < 2 {
		t.Fatalf("diagnostics = %+v", resp.Diagnostics)
	}
}

func TestInspectValidationReturnsEmptyArrays(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"steps": ["check"]}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runOnlavaInspect([]string{"validation", "--app-root", root, "--json"}, &out); err != nil {
		t.Fatalf("inspect validation: %v", err)
	}
	var resp inspectValidationResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if resp.Diagnostics == nil {
		t.Fatalf("diagnostics is nil")
	}
	if len(resp.Profiles) != 1 {
		t.Fatalf("profiles = %+v", resp.Profiles)
	}
	if resp.Profiles[0].Paths == nil {
		t.Fatalf("paths is nil")
	}
	if resp.Profiles[0].Artifacts == nil {
		t.Fatalf("artifacts is nil")
	}
}

func TestValidateDryRunDoesNotExecute(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"tasks": {
			"touch": {"run": "touch SHOULD_NOT_EXIST"}
		},
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"cost": "low", "steps": ["task:touch"]}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runOnlavaValidate(context.Background(), &out, []string{"quick", "--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("validate dry-run: %v", err)
	}
	if fileExists(filepath.Join(root, "SHOULD_NOT_EXIST")) {
		t.Fatalf("dry-run executed shell task")
	}
	var resp validationPlanResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !resp.OK || len(resp.Steps) != 1 || resp.Steps[0].Name != "task:touch" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestValidateRunsConfiguredTaskAndWritesResult(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"tasks": {
			"ok": {"run": "printf hello"}
		},
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"cost": "low", "steps": ["task:ok"]}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runOnlavaValidate(context.Background(), &out, []string{"--app-root", root, "--json", "--write"}); err != nil {
		t.Fatalf("validate: %v\n%s", err, out.String())
	}
	var resp validationResultResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !resp.OK || len(resp.Steps) != 1 || resp.Steps[0].Evidence.StdoutTail != "hello" {
		t.Fatalf("resp = %+v", resp)
	}
	if !fileExists(filepath.Join(root, ".onlava/harness/validation/latest.json")) {
		t.Fatalf("latest validation result was not written")
	}
}

func TestValidateProfileEnvFlowsToNestedTasks(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"tasks": {
			"print-env": {
				"run": "printf '%s/%s' \"$PARENT_VALUE\" \"$TASK_VALUE\"",
				"env": {"TASK_VALUE": "task"}
			}
		},
		"validation": {
			"default": "full",
			"profiles": {
				"full": {
					"env": {"PARENT_VALUE": "parent", "TASK_VALUE": "profile"},
					"steps": ["profile:quick"]
				},
				"quick": {"steps": ["task:print-env"]}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runOnlavaValidate(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("validate: %v\n%s", err, out.String())
	}
	var resp validationResultResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if got := resp.Steps[0].Evidence.StdoutTail; got != "parent/task" {
		t.Fatalf("stdout tail = %q", got)
	}
	if strings.Join(resp.Selection.ResolvedProfiles, ",") != "full,quick" {
		t.Fatalf("resolved profiles = %+v", resp.Selection.ResolvedProfiles)
	}
}

func TestValidateChangedSelectsMatchingProfiles(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"tasks": {
			"quick-task": {"run": "printf quick"},
			"pulse-task": {"run": "printf pulse"}
		},
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"cost": "low", "steps": ["task:quick-task"]},
				"pulse": {"cost": "medium", "paths": ["apps/pulse/**"], "steps": ["task:pulse-task"]}
			}
		}
	}`)

	oldCollect := collectValidationChangedFiles
	collectValidationChangedFiles = func(context.Context, string, string) ([]string, error) {
		return []string{"apps/pulse/src/App.tsx"}, nil
	}
	t.Cleanup(func() { collectValidationChangedFiles = oldCollect })

	var out bytes.Buffer
	if err := runOnlavaValidate(context.Background(), &out, []string{"changed", "--app-root", root, "--base", "origin/main", "--dry-run", "--json"}); err != nil {
		t.Fatalf("validate changed: %v\n%s", err, out.String())
	}
	var resp validationPlanResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if strings.Join(resp.Selection.ResolvedProfiles, ",") != "quick,pulse" {
		t.Fatalf("resolved profiles = %+v", resp.Selection.ResolvedProfiles)
	}
	if len(resp.Selection.MatchedProfiles) != 1 || resp.Selection.MatchedProfiles[0].Profile != "pulse" {
		t.Fatalf("matches = %+v", resp.Selection.MatchedProfiles)
	}
}

func TestValidateChangedCollectsPathsRelativeToAppRoot(t *testing.T) {
	repo := t.TempDir()
	appRoot := filepath.Join(repo, "app")
	writeTestAppFile(t, appRoot, ".onlava.json", `{"name":"demo"}`)
	writeTestAppFile(t, appRoot, "src/main.go", "package main\n")
	writeTestAppFile(t, filepath.Join(repo, "other"), "main.go", "package main\n")
	runValidationGit(t, repo, "init")
	runValidationGit(t, repo, "config", "user.email", "test@example.com")
	runValidationGit(t, repo, "config", "user.name", "Test")
	runValidationGit(t, repo, "add", ".")
	runValidationGit(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runValidationGit(t, repo, "rev-parse", "HEAD"))
	writeTestAppFile(t, appRoot, "src/main.go", "package main\nconst changed = true\n")
	writeTestAppFile(t, filepath.Join(repo, "other"), "main.go", "package main\nconst changed = true\n")
	runValidationGit(t, repo, "add", ".")
	runValidationGit(t, repo, "commit", "-m", "change")

	files, err := collectValidationChangedFiles(context.Background(), appRoot, base)
	if err != nil {
		t.Fatalf("collect changed files: %v", err)
	}
	if strings.Join(files, ",") != "src/main.go" {
		t.Fatalf("files = %+v", files)
	}
}

func TestValidationGlobMatchesRecursiveMiddleSegments(t *testing.T) {
	if !validationGlobMatches("apps/**/src/*.ts", "apps/web/src/main.ts") {
		t.Fatalf("recursive middle glob did not match")
	}
	if validationGlobMatches("apps/**/src/*.ts", "apps/web/test/main.ts") {
		t.Fatalf("recursive middle glob matched wrong path")
	}
}

func TestValidateCapturesCodeTaskOutput(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {
					"cost": "low",
					"env": {"CODE_TASK_VALUE": "code-task-env"},
					"steps": ["task:billing:reconcile"]
				}
			}
		}
	}`)
	writeTestAppFile(t, root, "billing/tasks/reconcile.task.go", "//go:build ignore\n\npackage main\nimport \"fmt\"\nfunc main(){fmt.Print(\"code-task\")}\n")

	prev := scriptCommandContext
	scriptCommandContext = func(ctx context.Context, program string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s' \"$CODE_TASK_VALUE\"")
	}
	t.Cleanup(func() { scriptCommandContext = prev })

	var out bytes.Buffer
	if err := runOnlavaValidate(context.Background(), &out, []string{"quick", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("validate code task: %v\n%s", err, out.String())
	}
	var resp validationResultResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if got := resp.Steps[0].Evidence.StdoutTail; got != "code-task-env" {
		t.Fatalf("stdout tail = %q", got)
	}
}

func TestValidationConfigRejectsReservedProfileNames(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "changed",
			"profiles": {
				"changed": {"steps": ["check"]}
			}
		}
	}`)
	appRoot, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatalf("discover app: %v", err)
	}
	diags := validateValidationConfig(appRoot, cfg)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics")
	}
	if !strings.Contains(diags[0].Message, "reserved") {
		t.Fatalf("diagnostics = %+v", diags)
	}
}

func TestValidationConfigRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
		"name": "demo",
		"validation": {
			"profiles": {
				"quick": {"steps": ["check"], "timeout": "1m"}
			}
		}
	}`)
	_, _, err := discoverConfiguredApp(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .onlava.json field "validation.profiles.quick.timeout"`) {
		t.Fatalf("err = %v", err)
	}
	_ = os.RemoveAll(root)
}

func runValidationGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
