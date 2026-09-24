package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/build"
)

func TestFrameworkSelectionDiffersWithoutReadingSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	producer := "sha256:" + strings.Repeat("ab", 32)
	other := strings.Repeat("cd", 32)
	snapshot := func(hex string) string { return filepath.Join(root, ".scenery", "framework", "source", hex) }
	for _, test := range []struct {
		name            string
		desired         build.DesiredFramework
		producerVersion string
		want            bool
	}{
		{name: "same pin", desired: build.DesiredFramework{Version: "v0.3.7"}, producerVersion: "v0.3.7"},
		{name: "bumped pin", desired: build.DesiredFramework{Version: "v0.3.8"}, producerVersion: "v0.3.7", want: true},
		{name: "source producer, pinned app", desired: build.DesiredFramework{Version: "v0.3.7"}, producerVersion: "dev", want: true},
		{name: "same snapshot", desired: build.DesiredFramework{Version: "v0.0.0", Root: snapshot(strings.TrimPrefix(producer, "sha256:"))}, producerVersion: "dev"},
		{name: "other snapshot", desired: build.DesiredFramework{Version: "v0.0.0", Root: snapshot(other)}, producerVersion: "dev", want: true},
		// Selecting a mutable checkout rewrites go.mod; that stays explicit.
		{name: "external checkout", desired: build.DesiredFramework{Version: "v0.0.0", Root: filepath.Join(root, "..", "scenery")}, producerVersion: "dev"},
	} {
		if got := frameworkSelectionDiffers(root, test.desired, test.producerVersion, producer); got != test.want {
			t.Errorf("%s: differs = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestFrameworkHandoffRetriesFailedSelectionAfterSwitchingBack(t *testing.T) {
	originalChanged, originalPrepare := changedAppFrameworkFunc, prepareFrameworkHandoffFunc
	t.Cleanup(func() { changedAppFrameworkFunc, prepareFrameworkHandoffFunc = originalChanged, originalPrepare })
	running, next := build.DesiredFramework{Version: "v0.3.7"}, build.DesiredFramework{Version: "v0.3.8"}
	selected := next
	changedAppFrameworkFunc = func(string) (build.DesiredFramework, bool) { return selected, selected != running }
	prepared := 0
	prepareErr := errors.New("module proxy unavailable")
	prepareFrameworkHandoffFunc = func(ctx context.Context, _ string) (*frameworkHandoff, error) {
		prepared++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if prepareErr != nil {
			return nil, prepareErr
		}
		return &frameworkHandoff{Executable: "/app/.scenery/framework/bin/b/scenery", Version: next.Version}, nil
	}
	var out bytes.Buffer
	supervisor := &devSupervisor{root: t.TempDir(), console: newRunConsole(&out, &bytes.Buffer{}, false, true, "demo", "/app")}
	var failed build.DesiredFramework
	step := func(ctx context.Context) *frameworkHandoff {
		t.Helper()
		return supervisor.frameworkHandoffBeforeBuild(ctx, &failed)
	}

	// An interrupted preparation is not remembered as a failure.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if handoff := step(canceled); handoff != nil || failed != (build.DesiredFramework{}) {
		t.Fatalf("canceled preparation: handoff %v, failed %+v", handoff, failed)
	}
	// A failed selection is reported once, not prepared on every rebuild.
	for range 2 {
		if handoff := step(context.Background()); handoff != nil {
			t.Fatalf("failed preparation handed off to %+v", handoff)
		}
	}
	if prepared != 2 || strings.Count(out.String(), `"build.error"`) != 1 {
		t.Fatalf("prepared %d times, reported:\n%s", prepared, out.String())
	}
	// Selecting the running producer again ends that episode.
	selected = running
	if handoff := step(context.Background()); handoff != nil {
		t.Fatalf("running selection handed off to %+v", handoff)
	}
	selected, prepareErr = next, nil
	handoff := step(context.Background())
	if handoff == nil || handoff.Version != next.Version || prepared != 3 {
		t.Fatalf("reselected framework: handoff %+v after %d preparations", handoff, prepared)
	}
}

func TestFrameworkHandoffContinuesForegroundAndDetachedRuns(t *testing.T) {
	handoff := &frameworkHandoff{Executable: "/app/.scenery/framework/bin/b/scenery", Version: "v0.3.8", SourceDigest: "sha256:" + strings.Repeat("cd", 32)}
	var executable string
	var args, environment []string
	original := execFrameworkHandoff
	t.Cleanup(func() { execFrameworkHandoff = original })
	execFrameworkHandoff = func(path string, argv, env []string) error {
		executable, args, environment = path, argv, env
		return nil
	}

	t.Setenv(detachedDevChildEnv, "")
	if err := continueWithFramework(handoff, []string{"-o", "jsonl", "--app-root", "/app"}); err != nil {
		t.Fatal(err)
	}
	if executable != handoff.Executable || !slices.Equal(args, []string{"up", "-o", "jsonl", "--app-root", "/app"}) {
		t.Fatalf("foreground continued as %s %q", executable, args)
	}

	// A detached supervisor relaunches through the new producer's launcher.
	t.Setenv(detachedDevChildEnv, "1")
	if err := continueWithFramework(handoff, []string{"--env", "all", "-o", "jsonl", "--app-root", "/app"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"up", "--env", "all", "--app-root", "/app", "--detach", "-o", "json"}) {
		t.Fatalf("detached relaunch args = %q", args)
	}
	for _, entry := range environment {
		if strings.HasPrefix(entry, detachedDevChildEnv+"=") {
			t.Fatalf("detached relaunch kept the child marker %q", entry)
		}
	}

	execFrameworkHandoff = func(string, []string, []string) error { return errors.New("exec format error") }
	err := continueWithFramework(handoff, nil)
	if err == nil || !strings.Contains(err.Error(), "run scenery up again") {
		t.Fatalf("failed exec error = %v", err)
	}
}

func TestRunConsoleEndsHandoffStreamWithCleanSummary(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, true, "demo", "/app")
	handoff := &frameworkHandoff{Executable: "/app/.scenery/framework/bin/b/scenery", Version: "v0.3.8", SourceDigest: "sha256:" + strings.Repeat("cd", 32)}
	console.FrameworkHandoff(handoff)
	console.Finish(handoff)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stream = %q", lines)
	}
	var event struct {
		Event    string `json:"event"`
		Terminal bool   `json:"terminal"`
		Data     struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil || event.Terminal || event.Data.Type != "framework.handoff" || event.Data.Data["executable"] != handoff.Executable {
		t.Fatalf("handoff event = %s (%v)", lines[0], err)
	}
	var summary struct {
		Event    string         `json:"event"`
		Terminal bool           `json:"terminal"`
		Data     map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &summary); err != nil || summary.Event != "summary" || !summary.Terminal {
		t.Fatalf("summary = %s (%v)", lines[1], err)
	}
	next, _ := summary.Data["handoff"].(map[string]any)
	if summary.Data["ok"] != true || summary.Data["error"] != nil || next["framework_version"] != "v0.3.8" || next["executable"] != handoff.Executable {
		t.Fatalf("summary data = %v", summary.Data)
	}
}

func TestUpCommandContinuesWithHandedOffFramework(t *testing.T) {
	handoff := &frameworkHandoff{Executable: "/app/.scenery/framework/bin/b/scenery", Version: "v0.3.8"}
	originalWatch, originalExec := runWithWatchFunc, execFrameworkHandoff
	t.Cleanup(func() { runWithWatchFunc, execFrameworkHandoff = originalWatch, originalExec })
	runWithWatchFunc = func(devListenRequest, bool, bool, bool, string, string, func()) error {
		return handoff
	}
	var args []string
	execFrameworkHandoff = func(_ string, argv, _ []string) error {
		args = argv
		return nil
	}
	t.Setenv(detachedDevChildEnv, "")
	root := filepath.Join(t.TempDir(), "missing")
	if err := upCommand([]string{"-o", "jsonl", "--app-root", root}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"up", "-o", "jsonl", "--app-root", root}) {
		t.Fatalf("up continued with %q", args)
	}
}
