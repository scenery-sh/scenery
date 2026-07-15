package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/validation"
)

func validationFixtureRoot(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", config)
	writeTestAppFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	return root
}

func TestInspectValidationProfiles(t *testing.T) {
	t.Parallel()

	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {
					"description": "Fast gate",
					"cost": "low",
					"steps": ["task:demo:ok"],
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
	if err := runSceneryInspect([]string{"validation", "--app-root", root, "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect validation: %v", err)
	}
	var resp inspectValidationResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if resp.Kind != validationInspectKind || resp.SchemaRevision != newCLIPayloadIdentity(validationInspectKind).SchemaRevision || resp.Default != "quick" || len(resp.Profiles) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Diagnostics) < 2 {
		t.Fatalf("diagnostics = %+v", resp.Diagnostics)
	}
}

func TestInspectValidationReturnsEmptyArrays(t *testing.T) {
	t.Parallel()

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
	if err := runSceneryInspect([]string{"validation", "--app-root", root, "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect validation: %v", err)
	}
	var resp inspectValidationResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
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
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"cost": "low", "steps": ["task:demo:touch"]}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runSceneryValidate(context.Background(), &out, []string{"quick", "--app-root", root, "--dry-run", "-o", "json"}); err != nil {
		t.Fatalf("validate dry-run: %v", err)
	}
	if fileExists(filepath.Join(root, "SHOULD_NOT_EXIST")) {
		t.Fatalf("dry-run executed shell task")
	}
	var resp validationPlanResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if !resp.OK || len(resp.Steps) != 1 || resp.Steps[0].Name != "task:demo:touch" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestValidateRunsCodeTaskAndWritesResult(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"cost": "low", "steps": ["task:demo:ok"]}
			}
		}
	}`)
	writeTestAppFile(t, root, "demo/tasks/ok.task.go", "//go:build ignore\n\npackage main\nimport \"fmt\"\nfunc main(){fmt.Print(\"hello\")}\n")

	var out bytes.Buffer
	if err := runSceneryValidate(context.Background(), &out, []string{"--app-root", root, "-o", "json", "--write"}); err != nil {
		t.Fatalf("validate: %v\n%s", err, out.String())
	}
	var resp validationResultResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if !resp.OK || len(resp.Steps) != 1 || resp.Steps[0].Evidence.StdoutTail != "hello" {
		t.Fatalf("resp = %+v", resp)
	}
	if !fileExists(filepath.Join(root, ".scenery/harness/validation/latest.json")) {
		t.Fatalf("latest validation result was not written")
	}
}

func TestValidateProfileEnvFlowsToNestedTasks(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "full",
			"profiles": {
				"full": {
					"env": {"PARENT_VALUE": "parent", "TASK_VALUE": "profile"},
					"steps": ["profile:quick"]
				},
				"quick": {"steps": ["task:demo:print-env"]}
			}
		}
	}`)
	writeTestAppFile(t, root, "demo/tasks/print-env.task.go", "//go:build ignore\n\npackage main\nimport (\"fmt\"; \"os\")\nfunc main(){fmt.Printf(\"%s/%s\", os.Getenv(\"PARENT_VALUE\"), os.Getenv(\"TASK_VALUE\"))}\n")

	var out bytes.Buffer
	if err := runSceneryValidate(context.Background(), &out, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("validate: %v\n%s", err, out.String())
	}
	var resp validationResultResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if got := resp.Steps[0].Evidence.StdoutTail; got != "parent/profile" {
		t.Fatalf("stdout tail = %q", got)
	}
	if strings.Join(resp.Selection.ResolvedProfiles, ",") != "full,quick" {
		t.Fatalf("resolved profiles = %+v", resp.Selection.ResolvedProfiles)
	}
}

func TestValidateChangedSelectsMatchingProfiles(t *testing.T) {
	root := validationFixtureRoot(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"cost": "low", "steps": ["task:demo:quick"]},
				"pulse": {"cost": "medium", "paths": ["apps/pulse/**"], "steps": ["task:demo:pulse"]}
			}
		}
	}`)

	oldCollect := validation.CollectChangedFiles
	validation.CollectChangedFiles = func(context.Context, string, string) ([]string, error) {
		return []string{"apps/pulse/src/App.tsx"}, nil
	}
	t.Cleanup(func() { validation.CollectChangedFiles = oldCollect })

	var out bytes.Buffer
	if err := runSceneryValidate(context.Background(), &out, []string{"changed", "--app-root", root, "--base", "origin/main", "--dry-run", "-o", "json"}); err != nil {
		t.Fatalf("validate changed: %v\n%s", err, out.String())
	}
	var resp validationPlanResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if strings.Join(resp.Selection.ResolvedProfiles, ",") != "quick,pulse" {
		t.Fatalf("resolved profiles = %+v", resp.Selection.ResolvedProfiles)
	}
	if len(resp.Selection.MatchedProfiles) != 1 || resp.Selection.MatchedProfiles[0].Profile != "pulse" {
		t.Fatalf("matches = %+v", resp.Selection.MatchedProfiles)
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
	if err := runSceneryValidate(context.Background(), &out, []string{"quick", "--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("validate code task: %v\n%s", err, out.String())
	}
	var resp validationResultResponse
	if err := decodeCLIJSON(out.Bytes(), &resp); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if got := resp.Steps[0].Evidence.StdoutTail; got != "code-task-env" {
		t.Fatalf("stdout tail = %q", got)
	}
}

func TestValidationConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "demo",
		"validation": {
			"profiles": {
				"quick": {"steps": ["check"], "timeout": "1m"}
			}
		}
	}`)
	_, _, err := discoverConfiguredApp(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .scenery.json field "validation.profiles.quick.timeout"`) {
		t.Fatalf("err = %v", err)
	}
	_ = os.RemoveAll(root)
}
