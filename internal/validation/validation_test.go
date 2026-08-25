package validation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestCollectChangedFilesPlansAppRelativeGitDiffInProcess(t *testing.T) {
	t.Parallel()

	type gitCall struct {
		dir  string
		args []string
	}
	var calls []gitCall
	files, err := collectChangedFilesWithGit(context.Background(), "/repo/app", "base", func(_ context.Context, dir string, args ...string) ([]byte, error) {
		calls = append(calls, gitCall{dir: dir, args: append([]string(nil), args...)})
		switch len(calls) {
		case 1:
			return []byte("/repo\n"), nil
		case 2:
			return []byte("src/z.go\nsrc/a.go\n"), nil
		default:
			t.Fatalf("unexpected git call %d", len(calls))
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("collect changed files: %v", err)
	}
	if !reflect.DeepEqual(files, []string{"src/a.go", "src/z.go"}) {
		t.Fatalf("files = %+v", files)
	}
	want := []gitCall{
		{dir: "/repo/app", args: []string{"rev-parse", "--show-toplevel"}},
		{dir: "/repo", args: []string{"diff", "--name-only", "--relative=app", "base...HEAD", "--", "app"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("git calls = %#v, want %#v", calls, want)
	}
}
