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

func TestParseScriptTarget(t *testing.T) {
	t.Parallel()

	target, err := parseScriptTarget("billing:reconcile")
	if err != nil {
		t.Fatalf("parseScriptTarget: %v", err)
	}
	if target.Domain != "billing" || target.Name != "reconcile" {
		t.Fatalf("target = %+v", target)
	}
	for _, value := range []string{
		"billing",
		"billing:",
		":reconcile",
		"billing:../x",
		"../billing:reconcile",
		"billing:reconcile:again",
		"-billing:run",
		"billing.reports:run",
		"billing:reconcile.v2",
		"billing:reconcile extra",
		"billing:reconcile$",
	} {
		if _, err := parseScriptTarget(value); err == nil {
			t.Fatalf("parseScriptTarget(%q) succeeded, want error", value)
		}
	}
}

func TestScriptResolverFindsLayoutsAndAmbiguity(t *testing.T) {
	t.Parallel()

	root := scriptFixtureRoot(t)
	writeTestAppFile(t, root, "billing/scripts/reconcile.script.go", "//go:build ignore\n\npackage main\n")
	writeTestAppFile(t, root, "billing/scripts/reconcile.script.ts", "console.log('ts')\n")
	writeTestAppFile(t, root, "billing/scripts/backfill/main.go", "package main\nfunc main() {}\n")
	writeTestAppFile(t, root, "billing/scripts/import/index.ts", "console.log('import')\n")

	scripts, err := listScriptCandidates(root)
	if err != nil {
		t.Fatalf("listScriptCandidates: %v", err)
	}
	if len(scripts) != 4 {
		t.Fatalf("scripts = %d: %+v", len(scripts), scripts)
	}

	target, _ := parseScriptTarget("billing:reconcile")
	if _, _, err := resolveScriptCandidate(root, target, ""); err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "reconcile.script.go") || !strings.Contains(err.Error(), "reconcile.script.ts") {
		t.Fatalf("ambiguity error = %v", err)
	}
	goCandidate, _, err := resolveScriptCandidate(root, target, scriptLangGo)
	if err != nil {
		t.Fatalf("resolve go candidate: %v", err)
	}
	if goCandidate.Lang != scriptLangGo || goCandidate.Layout != "go-file" {
		t.Fatalf("go candidate = %+v", goCandidate)
	}

	missing, _ := parseScriptTarget("billing:missing")
	if _, searched, err := resolveScriptCandidate(root, missing, ""); err == nil || len(searched) != 4 || !strings.Contains(err.Error(), "billing/scripts/missing.script.go") {
		t.Fatalf("missing err = %v searched=%+v", err, searched)
	}
}

func TestScriptInspectJSON(t *testing.T) {
	t.Parallel()

	root := scriptFixtureRoot(t)
	writeTestAppFile(t, root, "billing/scripts/reconcile/main.go", "package main\nfunc main() {}\n")

	var out bytes.Buffer
	if err := runOnlavaScriptInspect(context.Background(), &out, scriptOptions{AppRoot: root, Target: "billing:reconcile", JSON: true}); err != nil {
		t.Fatalf("runOnlavaScriptInspect: %v", err)
	}
	var payload scriptInspectOutput
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Target.Domain != "billing" || payload.Candidate.Layout != "go-dir" || payload.Candidate.Path != "billing/scripts/reconcile/main.go" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunScriptArgsSplitAfterTarget(t *testing.T) {
	t.Parallel()

	opts, err := parseScriptRunArgs([]string{"--app-root", "/tmp/app", "--env", "production", "--lang", "go", "billing:reconcile", "--dry-run", "--limit", "100"})
	if err != nil {
		t.Fatalf("parseScriptRunArgs: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.Lang != scriptLangGo || opts.Target != "billing:reconcile" || strings.Join(opts.Args, " ") != "--dry-run --limit 100" {
		t.Fatalf("opts = %+v", opts)
	}

	opts, err = parseScriptRunArgs([]string{"--app-root", "/tmp/app", "--env", "production", "billing:reconcile", "--", "--dry-run"})
	if err != nil {
		t.Fatalf("parseScriptRunArgs: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.Target != "billing:reconcile" || strings.Join(opts.Args, " ") != "--dry-run" {
		t.Fatalf("top-level run opts = %+v", opts)
	}

	opts, err = parseScriptRunArgs([]string{"billing:reconcile", "--env", "production"})
	if err != nil {
		t.Fatalf("parseScriptRunArgs script args: %v", err)
	}
	if strings.Join(opts.Args, " ") != "--env production" {
		t.Fatalf("script args = %+v", opts.Args)
	}

	for _, args := range [][]string{
		{"billing:"},
		{":reconcile"},
		{"billing:reconcile:extra"},
		{"../billing:reconcile"},
		{"billing:reconcile.v2"},
	} {
		if _, err := parseScriptRunArgs(args); err == nil {
			t.Fatalf("parseScriptRunArgs(%v) succeeded, want script target error", args)
		}
	}
}

func TestTopLevelRunDispatchesToScriptRunner(t *testing.T) {
	root := scriptFixtureRoot(t)
	writeTestAppFile(t, root, "billing/scripts/reconcile.script.go", "//go:build ignore\n\npackage main\nfunc main() {}\n")

	prev := scriptCommandContext
	defer func() { scriptCommandContext = prev }()

	var gotProgram string
	var gotArgs []string
	scriptCommandContext = func(ctx context.Context, program string, args ...string) *exec.Cmd {
		gotProgram = program
		gotArgs = append([]string(nil), args...)
		return exec.CommandContext(ctx, "true")
	}

	if err := run([]string{"run", "--app-root", root, "--env", "production", "--lang", "go", "billing:reconcile", "--dry-run"}); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if gotProgram != "go" || strings.Join(gotArgs, " ") != "run ./billing/scripts/reconcile.script.go --dry-run" {
		t.Fatalf("script command = %q %+v", gotProgram, gotArgs)
	}
}

func TestTopLevelRunListAndInspectSubcommands(t *testing.T) {
	root := scriptFixtureRoot(t)
	writeTestAppFile(t, root, "billing/scripts/reconcile/main.go", "package main\nfunc main() {}\n")

	out := captureStdout(t, func() error {
		return run([]string{"run", "list", "--app-root", root, "--json"})
	})
	if !strings.Contains(out, `"domain": "billing"`) || !strings.Contains(out, `"name": "reconcile"`) {
		t.Fatalf("list output = %s", out)
	}

	out = captureStdout(t, func() error {
		return run([]string{"run", "inspect", "billing:reconcile", "--app-root", root, "--json"})
	})
	if !strings.Contains(out, `"layout": "go-dir"`) {
		t.Fatalf("inspect output = %s", out)
	}
}

func TestScriptCommandIsRemoved(t *testing.T) {
	if err := run([]string{"script", "list"}); err == nil || !strings.Contains(err.Error(), `unknown command "script"`) {
		t.Fatalf("run script error = %v", err)
	}
}

func TestGoScriptBuildTagValidation(t *testing.T) {
	t.Parallel()

	root := scriptFixtureRoot(t)
	writeTestAppFile(t, root, "billing/scripts/good.script.go", "//go:build ignore\n\npackage main\nfunc main() {}\n")
	writeTestAppFile(t, root, "billing/scripts/bad.script.go", "package main\nfunc main() {}\n")

	if err := validateGoScriptBuildTag(filepath.Join(root, "billing/scripts/good.script.go")); err != nil {
		t.Fatalf("good build tag: %v", err)
	}
	if err := validateGoScriptBuildTag(filepath.Join(root, "billing/scripts/bad.script.go")); err == nil || !strings.Contains(err.Error(), "must start with //go:build ignore") {
		t.Fatalf("bad build tag err = %v", err)
	}
}

func TestTypeScriptScriptCommandPrefersBunThenNode(t *testing.T) {
	prev := execLookPath
	defer func() { execLookPath = prev }()
	execLookPath = func(name string) (string, error) {
		switch name {
		case "bun":
			return "/bin/bun", nil
		default:
			return "", os.ErrNotExist
		}
	}
	program, args, err := typeScriptScriptCommand("billing/scripts/reconcile.script.ts")
	if err != nil {
		t.Fatalf("typeScriptScriptCommand bun: %v", err)
	}
	if program != "/bin/bun" || strings.Join(args, " ") != "billing/scripts/reconcile.script.ts" {
		t.Fatalf("bun command = %s %+v", program, args)
	}

	execLookPath = func(name string) (string, error) {
		switch name {
		case "node":
			return "/bin/node", nil
		default:
			return "", os.ErrNotExist
		}
	}
	program, args, err = typeScriptScriptCommand("billing/scripts/reconcile.script.ts")
	if err != nil {
		t.Fatalf("typeScriptScriptCommand node: %v", err)
	}
	if program != "/bin/node" || strings.Join(args, " ") != "--import tsx billing/scripts/reconcile.script.ts" {
		t.Fatalf("node command = %s %+v", program, args)
	}
}

func TestRunOnlavaScriptRunsGoFileFromAppRoot(t *testing.T) {
	root := scriptFixtureRoot(t)
	writeTestAppFile(t, root, "fixtures/input.txt", "fixture-ok\n")
	writeTestAppFile(t, root, "billing/scripts/reconcile.script.go", `//go:build ignore

package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	data, err := os.ReadFile("fixtures/input.txt")
	if err != nil {
		panic(err)
	}
	fmt.Printf("cwd-fixture=%s", strings.TrimSpace(string(data)))
	fmt.Printf(" args=%s", strings.Join(os.Args[1:], ","))
	fmt.Printf(" app=%s env=%s\n", os.Getenv("ONLAVA_APP_ID"), os.Getenv("ONLAVA_ENV"))
}
`)

	out := captureStdout(t, func() error {
		return runOnlavaScript(context.Background(), scriptOptions{
			AppRoot: root,
			Env:     "production",
			Target:  "billing:reconcile",
			Args:    []string{"--dry-run", "--limit", "100"},
		})
	})
	for _, want := range []string{
		"cwd-fixture=fixture-ok",
		"args=--dry-run,--limit,100",
		"app=scriptapp",
		"env=production",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("script output missing %q:\n%s", want, out)
		}
	}
}

func scriptFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"scriptapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/scriptapp\n\ngo 1.26.3\n")
	return root
}
