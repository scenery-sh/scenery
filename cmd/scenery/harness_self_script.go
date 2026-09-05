package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const harnessCodeTaskProcessProbeName = "code task process probe"

type harnessCodeTaskProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessCodeTaskProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessCodeTaskProcessProbeStepWithCheck(ctx, repoRoot, runHarnessCodeTaskProcessProbeCheck)
}

func runHarnessCodeTaskProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessCodeTaskProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessCodeTaskProcessProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the Go code-task process boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessCodeTaskProcessProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-code-task-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()

	files := map[string]string{
		".scenery.json":      `{"name":"scriptapp","envs":{"local":{"default":true},"production":{}}}`,
		"go.mod":             "module example.com/scriptapp\n\ngo 1.26.3\n",
		"fixtures/input.txt": "fixture-ok\n",
		"billing/tasks/reconcile.task.go": `//go:build ignore

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
	fmt.Println()
}
`,
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return nil, nil, err
		}
	}

	var stdout bytes.Buffer
	if err := runSceneryScript(ctx, scriptOptions{
		AppRoot: root,
		Env:     "production",
		Target:  "billing:reconcile",
		Args:    []string{"--dry-run", "--limit", "100"},
		Stdout:  &stdout,
	}); err != nil {
		return nil, nil, err
	}
	want := "cwd-fixture=fixture-ok args=--dry-run,--limit,100"
	if got := strings.TrimSpace(stdout.String()); got != want {
		return nil, nil, fmt.Errorf("code task output = %q, want %q", got, want)
	}
	return map[string]any{
		"proof":  "go_code_task_ran_from_app_root_with_real_arguments",
		"target": "billing:reconcile",
		"output": want,
	}, nil, nil
}
