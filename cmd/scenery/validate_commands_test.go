package main

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/validation"
)

func TestValidationCommandUsesLiteralArgvAndProfileEnvironment(t *testing.T) {
	t.Parallel()
	want := []string{"go", "test", "a b", "", "$(literal)"}
	step := validation.PlanStep{Kind: "command", Name: "command-1", Command: want, CWD: t.TempDir(), Env: map[string]string{"VALIDATION_TEST": "value"}}
	calls := 0
	err := runValidationStepCommand(context.Background(), step.CWD, appcfg.Config{}, step, io.Discard, io.Discard, func(cmd *exec.Cmd) error {
		calls++
		if !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != step.CWD || !slices.Contains(cmd.Env, "VALIDATION_TEST=value") || cmd.Stdin != nil {
			t.Fatalf("command=%+v", cmd)
		}
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestValidationCommandDryRun(t *testing.T) {
	t.Parallel()
	root := validationFixtureRoot(t, `{"name":"demo","validation":{"default":"quick","profiles":{"quick":{"commands":[{"command":"never-execute-this","args":["a b",""]}]}}}}`)
	var out bytes.Buffer
	if err := runSceneryValidate(context.Background(), &out, []string{"quick", "--app-root", root, "--dry-run", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var plan validationPlanResponse
	if err := decodeCLIJSON(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || !reflect.DeepEqual(plan.Steps[0].Command, []string{"never-execute-this", "a b", ""}) {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestValidationOutputArtifactsAreUniquePerStepID(t *testing.T) {
	t.Parallel()
	ctx := validationArtifactContext{Root: t.TempDir(), Enabled: true, RunID: "run"}
	var paths []string
	for _, id := range []string{"quick/command-1", "quick/child/command-1"} {
		artifacts, diags := writeValidationOutputArtifacts(ctx, validation.PlanStep{ID: id, Name: "command-1"}, []byte(id), nil)
		if len(diags) != 0 || len(artifacts) != 1 {
			t.Fatalf("artifacts=%+v diags=%+v", artifacts, diags)
		}
		paths = append(paths, artifacts[0].Path)
	}
	if paths[0] == paths[1] {
		t.Fatalf("steps sharing a name share artifact path %s", paths[0])
	}
}
