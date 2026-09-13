package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestHarnessExplicitProofSelection(t *testing.T) {
	t.Parallel()
	want := strings.Fields("parallel-runtime postgres ui fixtures storage core-separation capability-authority auth worktree agent-restart assistant-init assistant-runtime build-info cli-process dev-follower dev-process dev-lock dev-cleanup inspect-go toolchain-build worktree-git edge generation native-contract snapshot-backup typescript code-task victoria desktop deploy-ssh validation-git test-cache")
	if got := harnessProbeIDs(); !slices.Equal(got, want) {
		t.Fatalf("functional inventory = %v, want %v", got, want)
	}
	for _, tc := range []struct {
		args []string
		mode string
		ids  []string
	}{
		{nil, harnessSelfModeDefault, nil},
		{[]string{"--quick"}, harnessSelfModeQuick, nil},
		{[]string{"--race"}, harnessSelfModeRace, nil},
		{[]string{"--release"}, harnessSelfModeRelease, want},
		{[]string{"--probe", "worktree", "--probe", "auth"}, harnessSelfModeProbe, []string{"auth", "worktree"}},
		{[]string{"--probe", "auth"}, harnessSelfModeProbe, []string{"auth"}},
		{[]string{"--benchmark", "edit-latency"}, harnessSelfModeBenchmark, nil},
		{[]string{"--benchmark", "worktree-cost"}, harnessSelfModeBenchmark, nil},
		{[]string{"--benchmark", "edit-latency"}, harnessSelfModeBenchmark, nil},
	} {
		opts, err := parseHarnessSelfArgs(tc.args)
		if err != nil || opts.Mode != tc.mode {
			t.Fatalf("%v: opts=%+v err=%v", tc.args, opts, err)
		}
		var ids []string
		for _, probe := range selectedHarnessProbes(opts) {
			if probe.run == nil {
				t.Fatalf("%s has no implementation", probe.id)
			}
			ids = append(ids, probe.id)
		}
		if !slices.Equal(ids, tc.ids) {
			t.Fatalf("%v selected %v, want %v", tc.args, ids, tc.ids)
		}
		if tc.mode == harnessSelfModeBenchmark && opts.Benchmark != tc.args[1] {
			t.Fatalf("benchmark lost: %+v", opts)
		}
	}
}

func TestHarnessInvalidProofSelectionFailsBeforeWork(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--probe", "unknown"}, {"--probe", "auth", "--probe", "auth"},
		{"--benchmark", "unknown"}, {"--benchmark", "worktree-cost", "--benchmark", "worktree-cost"},
		{"--benchmark", "edit-latency", "--benchmark", "edit-latency"},
		{"--release", "--probe", "auth"}, {"--probe", "auth", "--release"},
		{"--quick", "--probe", "auth"}, {"--probe", "auth", "--race"},
		{"--probe", "auth", "--benchmark", "worktree-cost"},
		{"--benchmark", "worktree-cost", "--probe", "auth"},
		{"--probe", "auth", "--fresh-tests"}, {"--benchmark", "worktree-cost", "--fresh-tests"},
		{"--probe"}, {"--benchmark"}, {"--probe", ""},
	} {
		_, parseErr := parseHarnessSelfArgs(args)
		if parseErr == nil {
			t.Fatalf("accepted invalid selection %v", args)
		}
		// No repository, build, or probe can be reached before this exact error.
		err := runSceneryHarnessSelf(context.Background(), io.Discard, args)
		if err == nil || err.Error() != parseErr.Error() {
			t.Fatalf("%v reached work: %v, want %v", args, err, parseErr)
		}
	}
}

func TestHarnessProbeRetainsFailureAndFocusedRerun(t *testing.T) {
	t.Parallel()
	resp := harnessSelfResponse{Steps: []harnessStep{{Name: "common", OK: true}}}
	calls := 0
	probe := harnessSingleProbe("auth", func(context.Context, string) harnessStep {
		calls++
		return harnessStep{
			Name: "auth", OK: false, Error: "assertion failed",
			Summary: map[string]any{"assertions": 15},
			Evidence: &harnessEvidence{
				Command: []string{"fixture", "check"}, CWD: "/temporary-fixture",
				ReproCommand: "old broad rerun",
			},
			Diagnostics: []checkDiagnostic{{
				Severity: "error", Message: "assertion failed",
				SuggestedAction: "Fix auth, then rerun `go run ./scripts/verify --release --summary --write`.",
			}},
		}
	})
	runHarnessProbe(context.Background(), "/repo", &resp, harnessArtifactContext{}, probe)
	if calls != 1 || len(resp.Steps) != 2 || resp.Steps[0].Command != nil {
		t.Fatalf("probe dispatch changed unrelated work: calls=%d steps=%+v", calls, resp.Steps)
	}
	step := resp.Steps[1]
	if step.OK || step.Error != "assertion failed" || step.Summary["assertions"] != 15 {
		t.Fatalf("failure evidence lost: %+v", step)
	}
	want := []string{"go", "run", "./scripts/verify", "--repo-root", "/repo", "--probe", "auth", "--summary", "--write"}
	if !slices.Equal(step.Command, want) {
		t.Fatalf("rerun = %v, want %v", step.Command, want)
	}
	if !slices.Equal(step.Evidence.Command, []string{"fixture", "check"}) || step.Evidence.CWD != "/temporary-fixture" {
		t.Fatalf("actual execution evidence changed: %+v", step.Evidence)
	}
	if got := harnessStepRerunCommand("/repo", step); got != reproCommand(want, "/repo") {
		t.Fatalf("agent rerun = %q, want focused repository command", got)
	}
	action := step.Diagnostics[0].SuggestedAction
	if !strings.Contains(action, "--probe auth") || strings.Contains(action, "--release") || !strings.HasPrefix(action, "Fix auth,") {
		t.Fatalf("incorrect focused advice: %s", action)
	}
}
