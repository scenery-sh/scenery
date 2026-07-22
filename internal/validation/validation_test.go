package validation

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func writeValidationTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func discoverValidationTestApp(t *testing.T, config string) Planner {
	t.Helper()
	root := t.TempDir()
	var raw map[string]any
	if err := json.Unmarshal([]byte(config), &raw); err != nil {
		t.Fatal(err)
	}
	raw["envs"] = map[string]any{"local": map[string]any{"default": true}}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	writeValidationTestFile(t, root, ".scenery.json", string(encoded))
	writeValidationTestFile(t, root, testAppFilename, "application \"test\" {}\n")
	appRoot, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		t.Fatalf("discover app: %v", err)
	}
	return Planner{AppRoot: appRoot, Config: cfg}
}

func TestValidationGlobMatchesRecursiveMiddleSegments(t *testing.T) {
	t.Parallel()

	if !globMatches("apps/**/src/*.ts", "apps/web/src/main.ts") {
		t.Fatalf("recursive middle glob did not match")
	}
	if globMatches("apps/**/src/*.ts", "apps/web/test/main.ts") {
		t.Fatalf("recursive middle glob matched wrong path")
	}
}

func TestValidationConfigRejectsReservedProfileNames(t *testing.T) {
	t.Parallel()

	planner := discoverValidationTestApp(t, `{
		"name": "demo",
		"validation": {
			"default": "changed",
			"profiles": {
				"changed": {"steps": ["check"]}
			}
		}
	}`)
	diags := planner.ValidateConfig()
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics")
	}
	if !strings.Contains(diags[0].Message, "reserved") {
		t.Fatalf("diagnostics = %+v", diags)
	}
}

func TestValidationConfigDetectsProfileCycles(t *testing.T) {
	t.Parallel()

	planner := discoverValidationTestApp(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"steps": ["profile:full"]},
				"full": {"steps": ["profile:quick"]}
			}
		}
	}`)
	diags := planner.ValidateConfig()
	var cycle string
	for _, diag := range diags {
		if strings.Contains(diag.Message, "profile cycle detected") {
			cycle = diag.Message
			break
		}
	}
	if cycle == "" {
		t.Fatalf("expected a profile cycle diagnostic, got %+v", diags)
	}
	if !strings.Contains(cycle, " -> ") {
		t.Fatalf("cycle diagnostic = %q", cycle)
	}
}

func TestPlanReportsCycleInsteadOfLooping(t *testing.T) {
	t.Parallel()

	planner := discoverValidationTestApp(t, `{
		"name": "demo",
		"validation": {
			"default": "quick",
			"profiles": {
				"quick": {"steps": ["profile:full", "check"]},
				"full": {"steps": ["profile:quick"]}
			}
		}
	}`)
	plan, err := planner.NamedPlan("quick", Selection{Mode: "explicit", Requested: []string{"quick"}})
	if err != nil {
		t.Fatalf("NamedPlan: %v", err)
	}
	if len(plan.Diagnostics) == 0 {
		t.Fatalf("expected cycle diagnostics")
	}
	if strings.Join(plan.Profiles, ",") != "quick,full" {
		t.Fatalf("profiles = %+v", plan.Profiles)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Name != "check" {
		t.Fatalf("steps = %+v", plan.Steps)
	}
}

func TestValidateChangedCollectsPathsRelativeToAppRoot(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	appRoot := filepath.Join(repo, "app")
	writeValidationTestFile(t, appRoot, ".scenery.json", `{"name":"demo"}`)
	writeValidationTestFile(t, appRoot, "src/main.go", "package main\n")
	writeValidationTestFile(t, filepath.Join(repo, "other"), "main.go", "package main\n")
	runValidationGit(t, repo, "init")
	runValidationGit(t, repo, "config", "user.email", "test@example.com")
	runValidationGit(t, repo, "config", "user.name", "Test")
	runValidationGit(t, repo, "add", ".")
	runValidationGit(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runValidationGit(t, repo, "rev-parse", "HEAD"))
	writeValidationTestFile(t, appRoot, "src/main.go", "package main\nconst changed = true\n")
	writeValidationTestFile(t, filepath.Join(repo, "other"), "main.go", "package main\nconst changed = true\n")
	runValidationGit(t, repo, "add", ".")
	runValidationGit(t, repo, "commit", "-m", "change")

	files, err := CollectChangedFiles(context.Background(), appRoot, base)
	if err != nil {
		t.Fatalf("collect changed files: %v", err)
	}
	if strings.Join(files, ",") != "src/main.go" {
		t.Fatalf("files = %+v", files)
	}
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
