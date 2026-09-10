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
	err := runValidationStepCommand(context.Background(), step.CWD, appcfg.Config{}, step, io.Discard, io.Discard, true, func(cmd *exec.Cmd) error {
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

func TestValidationCommandInspectionAndDryRun(t *testing.T) {
	t.Parallel()
	root := validationFixtureRoot(t, `{"name":"demo","validation":{"default":"quick","profiles":{"quick":{"commands":[{"command":"never-execute-this","args":["a b",""]}]}}}}`)
	var out bytes.Buffer
	if err := runSceneryInspect([]string{"validation", "--app-root", root, "-o", "json"}, &out); err != nil {
		t.Fatal(err)
	}
	var inspected inspectValidationResponse
	if err := decodeCLIJSON(out.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if len(inspected.Diagnostics) != 0 || len(inspected.Profiles) != 1 || len(inspected.Profiles[0].Commands) != 1 || inspected.Profiles[0].StepCount != 1 {
		t.Fatalf("inspection=%+v", inspected)
	}
	out.Reset()
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
