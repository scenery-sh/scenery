package validation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"

	inspectdata "scenery.sh/internal/inspect"
)

// StepRunner executes one resolved plan step, writing captured output to
// stdout and stderr. ExecutePlan may call it concurrently for distinct steps.
type StepRunner func(ctx context.Context, step PlanStep, stdout, stderr io.Writer) error

// ArtifactSink persists one step's captured output and returns the artifacts
// written plus any write diagnostics. A nil sink writes nothing. ExecutePlan
// may call it concurrently, so artifact paths must be unique per step ID.
type ArtifactSink func(step PlanStep, stdout, stderr []byte) ([]OutputArtifact, []Diagnostic)

// OutputArtifact is one captured-output artifact written by an ArtifactSink.
type OutputArtifact struct {
	Name string
	Path string
}

// Artifact is one artifact recorded in a validation result.
type Artifact struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// Result is the outcome of executing a resolved validation plan.
type Result struct {
	OK          bool
	GeneratedAt string
	App         inspectdata.AppRef
	Profile     string
	Selection   Selection
	Steps       []StepResult
	Artifacts   []Artifact
	Diagnostics []Diagnostic
	NextActions []string
}

// StepResult is the outcome of executing one plan step, including the raw
// material callers need to shape execution evidence.
type StepResult struct {
	ID      string
	Name    string
	Kind    string
	Profile string
	OK      bool
	// Started is when the step began executing.
	Started time.Time
	// Duration covers command execution and artifact writing.
	Duration          time.Duration
	ExecutionDuration time.Duration
	ArtifactDuration  time.Duration
	// Command and CWD echo the executed plan step.
	Command []string
	CWD     string
	// Stdout and Stderr hold the full captured output.
	Stdout string
	Stderr string
	// Err is the raw step command error, nil on success.
	Err error
	// Error is the rendered failure message: the first artifact-write
	// diagnostic, overridden by the command error when one occurred.
	Error string
	// Artifacts lists the captured-output artifacts written for the step.
	Artifacts []OutputArtifact
}

// DefaultParallelism bounds how many independent validation steps run at once.
func DefaultParallelism() int {
	return min(4, runtime.GOMAXPROCS(0))
}

// stepCanceledError marks a step stopped because another step failed first.
// It matches context.Canceled and keeps the runner error for exit evidence.
type stepCanceledError struct {
	failed string
	err    error
}

func (e *stepCanceledError) Error() string { return "canceled after " + e.failed + " failed" }

func (e *stepCanceledError) Unwrap() []error { return []error{context.Canceled, e.err} }

// ExecutePlan runs a plan, refusing to run anything when the plan carries
// diagnostics. Builtin steps (scenery check) run first, serially, as a fast
// gate; the remaining steps are grouped by profile so steps within one profile
// keep their declared order and fail fast, while distinct profiles run
// concurrently on up to parallelism workers, so run and writeArtifacts must be
// safe for concurrent use. The first failure cancels in-flight steps and stops
// scheduling new ones. Result steps, artifacts and next actions follow plan
// order.
func ExecutePlan(ctx context.Context, plan ResolvedPlan, parallelism int, run StepRunner, writeArtifacts ArtifactSink) Result {
	result := Result{
		OK:          len(plan.Diagnostics) == 0,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		App:         plan.App,
		Profile:     plan.Profile,
		Selection:   plan.Selection,
		Diagnostics: append([]Diagnostic(nil), plan.Diagnostics...),
		Steps:       []StepResult{},
	}
	if len(plan.Diagnostics) > 0 {
		result.NextActions = []string{"Fix validation configuration diagnostics, then rerun: scenery validate " + plan.Profile + " -o json --write"}
		finishCoverage(&result)
		return result
	}
	results := executeSteps(ctx, plan, max(parallelism, 1), run, writeArtifacts)
	for _, res := range results {
		if res == nil {
			continue
		}
		result.Steps = append(result.Steps, *res)
		for _, artifact := range res.Artifacts {
			kind := "artifact"
			if strings.Contains(artifact.Name, "stdout") {
				kind = "stdout"
			} else if strings.Contains(artifact.Name, "stderr") {
				kind = "stderr"
			}
			result.Artifacts = append(result.Artifacts, Artifact{Path: artifact.Path, Kind: kind})
		}
		if res.OK {
			continue
		}
		result.OK = false
		var canceled *stepCanceledError
		if !errors.As(res.Err, &canceled) {
			result.NextActions = append(result.NextActions, "Fix "+res.Name+", then rerun: scenery validate "+plan.Profile+" -o json --write")
		}
	}
	finishCoverage(&result)
	return result
}

// executeSteps returns one result per plan step, indexed by plan position;
// steps never started stay nil.
func executeSteps(ctx context.Context, plan ResolvedPlan, parallelism int, run StepRunner, writeArtifacts ArtifactSink) []*StepResult {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]*StepResult, len(plan.Steps))
	var mu sync.Mutex
	failed := ""
	execute := func(i int) bool {
		if runCtx.Err() != nil {
			return false
		}
		res := runStep(runCtx, plan, plan.Steps[i], run, writeArtifacts)
		if !res.OK {
			mu.Lock()
			switch {
			case failed == "":
				failed = res.Name
				cancel()
			case res.Err != nil && ctx.Err() == nil:
				res.Err = &stepCanceledError{failed: failed, err: res.Err}
				res.Error = res.Err.Error()
			}
			mu.Unlock()
		}
		results[i] = &res
		return res.OK
	}
	var groups [][]int
	groupByProfile := map[string]int{}
	for i, step := range plan.Steps {
		if step.Kind == "builtin" {
			if !execute(i) {
				return results
			}
			continue
		}
		idx, ok := groupByProfile[step.Profile]
		if !ok {
			idx = len(groups)
			groupByProfile[step.Profile] = idx
			groups = append(groups, nil)
		}
		groups[idx] = append(groups[idx], i)
	}
	queue := make(chan []int)
	var wg sync.WaitGroup
	for range min(parallelism, len(groups)) {
		wg.Go(func() {
			for group := range queue {
				for _, i := range group {
					if !execute(i) {
						break
					}
				}
			}
		})
	}
	for _, group := range groups {
		if runCtx.Err() != nil {
			break
		}
		queue <- group
	}
	close(queue)
	wg.Wait()
	return results
}

func runStep(ctx context.Context, plan ResolvedPlan, step PlanStep, run StepRunner, writeArtifacts ArtifactSink) StepResult {
	started := time.Now()
	var stdout, stderr bytes.Buffer
	err := run(ctx, step, &stdout, &stderr)
	executionDuration := time.Since(started)
	reportStarted := time.Now()
	var artifacts []OutputArtifact
	var diagnostics []Diagnostic
	if writeArtifacts != nil {
		artifacts, diagnostics = writeArtifacts(step, stdout.Bytes(), stderr.Bytes())
	}
	res := StepResult{
		ID:                step.ID,
		Name:              step.Name,
		Kind:              step.Kind,
		Profile:           step.Profile,
		OK:                err == nil,
		Started:           started,
		Duration:          time.Since(started),
		ExecutionDuration: executionDuration,
		ArtifactDuration:  time.Since(reportStarted),
		Command:           append([]string(nil), step.Command...),
		CWD:               firstNonEmpty(step.CWD, plan.App.Root),
		Stdout:            stdout.String(),
		Stderr:            stderr.String(),
		Err:               err,
		Artifacts:         artifacts,
	}
	if len(diagnostics) > 0 {
		res.Error = diagnostics[0].Message
		res.OK = false
	}
	if err != nil {
		res.Error = strings.TrimSpace(err.Error())
	}
	return res
}
