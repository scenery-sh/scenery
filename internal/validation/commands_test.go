package validation

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"scenery.sh/internal/app"
)

func TestTypedCommandsPreserveOrderArgumentsAndFailFastEvidence(t *testing.T) {
	t.Parallel()
	planner := discoverValidationTestApp(t, `{"name":"demo","validation":{"default":"quick","profiles":{
		"quick":{"steps":["profile:child"],"commands":[{"command":"go","args":["test","a b","","$(literal)"]},{"command":"unused"}],"env":{"CHECK":"parent"}},
		"child":{"commands":[{"command":"child"}],"env":{"CHECK":"child"}}
	}}}`)
	plan, err := planner.Plan(context.Background(), PlanRequest{})
	if err != nil || len(plan.Diagnostics) != 0 || len(plan.Steps) != 3 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	want := []string{"go", "test", "a b", "", "$(literal)"}
	if plan.Steps[0].Command[0] != "child" || plan.Steps[0].Env["CHECK"] != "child" || !reflect.DeepEqual(plan.Steps[1].Command, want) || plan.Steps[1].Env["CHECK"] != "parent" {
		t.Fatalf("steps=%+v", plan.Steps)
	}
	calls := 0
	result := ExecutePlan(context.Background(), plan, func(_ context.Context, step PlanStep, stdout, _ io.Writer) error {
		calls++
		if step.Command[0] == "go" {
			_, _ = io.WriteString(stdout, "failed check")
			return errors.New("expected failure")
		}
		return nil
	}, nil)
	if result.OK || calls != 2 || len(result.Steps) != 2 || !reflect.DeepEqual(result.Steps[1].Command, want) || result.Steps[1].Stdout != "failed check" {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestTypedCommandsRejectInvalidExecutableAndNULArguments(t *testing.T) {
	t.Parallel()
	for _, command := range []app.ValidationCommandConfig{{}, {Command: " "}, {Command: "go\x00"}, {Command: "go", Args: []string{"bad\x00arg"}}} {
		p := Planner{Config: app.Config{Validation: app.ValidationConfig{Default: "quick", Profiles: map[string]app.ValidationProfileConfig{"quick": {Commands: []app.ValidationCommandConfig{command}}}}}}
		if len(p.ValidateConfig()) == 0 {
			t.Fatalf("accepted %+v", command)
		}
	}
}
