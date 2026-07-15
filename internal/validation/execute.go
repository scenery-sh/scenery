package validation

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"time"

	inspectdata "scenery.sh/internal/inspect"
)

// StepRunner executes one resolved plan step, writing captured output to
// stdout and stderr.
type StepRunner func(ctx context.Context, step PlanStep, stdout, stderr io.Writer) error

// ArtifactSink persists one step's captured output and returns the artifacts
// written plus any write diagnostics. A nil sink writes nothing.
type ArtifactSink func(stepName string, stdout, stderr []byte) ([]OutputArtifact, []Diagnostic)

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
	Duration time.Duration
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

// ExecutePlan runs every plan step in order until one fails, refusing to run
// anything when the plan carries diagnostics.
func ExecutePlan(ctx context.Context, plan ResolvedPlan, run StepRunner, writeArtifacts ArtifactSink) Result {
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
		return result
	}
	for _, step := range plan.Steps {
		res := runStep(ctx, plan, step, run, writeArtifacts)
		result.Steps = append(result.Steps, res)
		for _, artifact := range res.Artifacts {
			kind := "artifact"
			if strings.Contains(artifact.Name, "stdout") {
				kind = "stdout"
			} else if strings.Contains(artifact.Name, "stderr") {
				kind = "stderr"
			}
			result.Artifacts = append(result.Artifacts, Artifact{Path: artifact.Path, Kind: kind})
		}
		if !res.OK {
			result.OK = false
			result.NextActions = []string{"Fix " + res.Name + ", then rerun: scenery validate " + plan.Profile + " -o json --write"}
			break
		}
	}
	return result
}

func runStep(ctx context.Context, plan ResolvedPlan, step PlanStep, run StepRunner, writeArtifacts ArtifactSink) StepResult {
	started := time.Now()
	var stdout, stderr bytes.Buffer
	err := run(ctx, step, &stdout, &stderr)
	var artifacts []OutputArtifact
	var diagnostics []Diagnostic
	if writeArtifacts != nil {
		artifacts, diagnostics = writeArtifacts(step.Name, stdout.Bytes(), stderr.Bytes())
	}
	res := StepResult{
		ID:        step.ID,
		Name:      step.Name,
		Kind:      step.Kind,
		Profile:   step.Profile,
		OK:        err == nil,
		Started:   started,
		Duration:  time.Since(started),
		Command:   append([]string(nil), step.Command...),
		CWD:       firstNonEmpty(step.CWD, plan.App.Root),
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Err:       err,
		Artifacts: artifacts,
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

// RunWithCapturedProcessOutput runs fn while the process-level os.Stdout and
// os.Stderr are redirected into the given writers, restoring them afterwards.
func RunWithCapturedProcessOutput(stdout, stderr io.Writer, fn func() error) error {
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return err
	}
	os.Stdout = outW
	os.Stderr = errW
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdout, outR)
		close(outDone)
	}()
	go func() {
		_, _ = io.Copy(stderr, errR)
		close(errDone)
	}()
	runErr := fn()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	<-outDone
	<-errDone
	_ = outR.Close()
	_ = errR.Close()
	return runErr
}
