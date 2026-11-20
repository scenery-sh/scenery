package validation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

func executeTestPlan(steps ...PlanStep) ResolvedPlan {
	return ResolvedPlan{Profile: "quick", Steps: steps}
}

func commandStep(name string) PlanStep {
	return PlanStep{ID: "quick/" + name, Name: name, Kind: "command", Profile: "quick"}
}

// waitOrFail bounds synchronization waits so a serial regression fails
// instead of deadlocking the test.
func waitOrFail(ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-time.After(time.Second):
		return errors.New("timed out waiting for a concurrent step")
	}
}

func TestExecutePlanGatesBuiltinThenRunsIndependentStepsConcurrentlyInPlanOrder(t *testing.T) {
	t.Parallel()
	names := []string{"a", "b", "c"}
	plan := executeTestPlan(commandStep("a"), PlanStep{ID: "quick/check", Name: "check", Kind: "builtin"}, commandStep("b"), commandStep("c"))
	finished := map[string]chan struct{}{}
	for _, name := range names {
		finished[name] = make(chan struct{})
	}
	gateDone := make(chan struct{})
	var mu sync.Mutex
	var completion []string
	run := func(_ context.Context, step PlanStep, stdout, _ io.Writer) error {
		if step.Kind == "builtin" {
			_, _ = io.WriteString(stdout, step.Name)
			close(gateDone)
			return nil
		}
		select {
		case <-gateDone:
		default:
			return errors.New(step.Name + " started before the builtin gate")
		}
		// Each step waits for its successor, so steps can only finish when all
		// run at once, and they complete in reverse plan order.
		if next := slices.Index(names, step.Name) + 1; next < len(names) {
			if err := waitOrFail(finished[names[next]]); err != nil {
				return err
			}
		}
		_, _ = io.WriteString(stdout, step.Name)
		mu.Lock()
		completion = append(completion, step.Name)
		mu.Unlock()
		close(finished[step.Name])
		return nil
	}
	sink := func(step PlanStep, stdout, _ []byte) ([]OutputArtifact, []Diagnostic) {
		return []OutputArtifact{{Name: step.ID + " stdout", Path: string(stdout) + ".txt"}}, nil
	}
	result := ExecutePlan(context.Background(), plan, 3, run, sink)
	if !result.OK {
		t.Fatalf("result=%+v", result)
	}
	var order, artifacts []string
	for _, step := range result.Steps {
		order = append(order, step.Name)
	}
	for _, artifact := range result.Artifacts {
		artifacts = append(artifacts, artifact.Path)
	}
	if !slices.Equal(order, []string{"a", "check", "b", "c"}) || !slices.Equal(artifacts, []string{"a.txt", "check.txt", "b.txt", "c.txt"}) {
		t.Fatalf("steps=%v artifacts=%v", order, artifacts)
	}
	if !slices.Equal(completion, []string{"c", "b", "a"}) {
		t.Fatalf("completion=%v", completion)
	}
}

func TestExecutePlanBuiltinGateFailureSkipsIndependentSteps(t *testing.T) {
	t.Parallel()
	plan := executeTestPlan(commandStep("a"), PlanStep{ID: "quick/check", Name: "check", Kind: "builtin"})
	var ran []string
	result := ExecutePlan(context.Background(), plan, 4, func(_ context.Context, step PlanStep, _, _ io.Writer) error {
		ran = append(ran, step.Name)
		if step.Kind == "builtin" {
			return errors.New("contract broken")
		}
		return nil
	}, nil)
	if result.OK || !slices.Equal(ran, []string{"check"}) || len(result.Steps) != 1 || len(result.NextActions) != 1 || result.NextActions[0] != "Fix check, then rerun: scenery validate quick -o json --write" {
		t.Fatalf("ran=%v result=%+v", ran, result)
	}
}

func TestExecutePlanFailureCancelsInFlightStepsAndStopsScheduling(t *testing.T) {
	t.Parallel()
	plan := executeTestPlan(commandStep("a"), commandStep("b"), commandStep("c"), commandStep("d"))
	bStarted := make(chan struct{})
	var mu sync.Mutex
	var ran []string
	result := ExecutePlan(context.Background(), plan, 2, func(ctx context.Context, step PlanStep, _, _ io.Writer) error {
		mu.Lock()
		ran = append(ran, step.Name)
		mu.Unlock()
		switch step.Name {
		case "a":
			if err := waitOrFail(bStarted); err != nil {
				return err
			}
			return errors.New("assertion failed")
		case "b":
			close(bStarted)
			if err := waitOrFail(ctx.Done()); err != nil {
				return err
			}
			return fmt.Errorf("signal: killed")
		}
		return nil
	}, nil)
	slices.Sort(ran)
	if result.OK || !slices.Equal(ran, []string{"a", "b"}) || len(result.Steps) != 2 {
		t.Fatalf("ran=%v result=%+v", ran, result)
	}
	failed, canceled := result.Steps[0], result.Steps[1]
	if failed.Name != "a" || failed.Error != "assertion failed" || errors.Is(failed.Err, context.Canceled) {
		t.Fatalf("failed step=%+v", failed)
	}
	if canceled.Name != "b" || !errors.Is(canceled.Err, context.Canceled) || canceled.Error != "canceled after a failed" {
		t.Fatalf("canceled step=%+v", canceled)
	}
	if !slices.Equal(result.NextActions, []string{"Fix a, then rerun: scenery validate quick -o json --write"}) {
		t.Fatalf("next actions=%v", result.NextActions)
	}
}
