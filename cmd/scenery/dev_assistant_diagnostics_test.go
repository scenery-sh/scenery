package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/devdash"
)

func TestAssistantOutputTailKeepsSplitLinesTheLastUnterminatedLineAndRedacts(t *testing.T) {
	tail := &assistantOutputTail{}
	for _, chunk := range []string{"npm warn deprecated\nError: Cannot find mod", "ule 'eve/connections'\r\n\n", "fetch https://user:pw@registry.example.test/eve\n", "\x1b[38;5;246m\x1b[0m\n", "build \x1b[31mfailed\x1b[0m: token=abc123"} {
		if _, err := tail.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := tail.Lines(); len(got) != 3 {
		t.Fatalf("an unterminated line was published while the command could still write: %q", got)
	}
	tail.flush()
	want := []string{
		"npm warn deprecated",
		"Error: Cannot find module 'eve/connections'",
		"fetch https://[redacted]@registry.example.test/eve",
		"build failed: token=[redacted]",
	}
	if got := tail.Lines(); !slices.Equal(got, want) {
		t.Fatalf("tail = %q, want %q", got, want)
	}
	for range assistantDiagnosticLines + 10 {
		_, _ = tail.Write([]byte("line\n"))
	}
	if got := tail.Lines(); len(got) != assistantDiagnosticLines {
		t.Fatalf("tail kept %d lines, want the bound %d", len(got), assistantDiagnosticLines)
	}
	_, _ = tail.Write([]byte(strings.Repeat("x", assistantDiagnosticBytes*2)))
	if got := tail.Lines(); len(got[len(got)-1]) > assistantDiagnosticBytes+len("…") {
		t.Fatalf("an unterminated line exceeded its bound: %d bytes", len(got[len(got)-1]))
	}
}

func TestAssistantPreparationFailureNamesItsCauseAfterTheOverlayIsRemoved(t *testing.T) {
	s, result, _ := assistantStageFixture(t)
	beforeConfig, beforeStatus := s.RuntimeConfig(), s.Status()
	var steps []map[string]any
	var levels []string
	s.config.OnEvent = func(_ context.Context, _ devdash.DevSource, level, message string, fields map[string]any) {
		if message == "assistant.step" {
			steps, levels = append(steps, fields), append(levels, level)
		}
	}
	// The provider wrote its explanation across several writes and ended on a
	// line without a newline, then exited; the helper overlay it wrote into is
	// removed when the preparation fails.
	s.config.BuildOverlay = func(context.Context, string, string, string, string) error {
		return captureAssistantProviderOutput("assistant provider build", func(output io.Writer) error {
			for _, chunk := range []string{"building agent\nError: Expected the connection ", "export to match the public eve shape\n", "exit: token=abc123"} {
				_, _ = output.Write([]byte(chunk))
			}
			return errors.New("exit status 1")
		})
	}
	stage, err := s.stage(context.Background(), nextAssistantStageResult(result))
	if err == nil {
		t.Fatal("failed preparation was accepted")
	}
	s.releaseStage(stage)
	if !slices.Equal(beforeConfig.Assistants, s.RuntimeConfig().Assistants) || len(beforeStatus) != len(s.Status()) || beforeStatus[0].State != s.Status()[0].State {
		t.Fatal("a recorded failure changed the assistant's published state")
	}
	build := assistantStepEvent(t, steps, levels, "assistant.build")
	if build.fields["ok"] != false || build.level != "error" || build.fields["error"] != "assistant provider build: exit status 1" {
		t.Fatalf("failed build step = %s %v", build.level, build.fields)
	}
	stageEvent := assistantStepEvent(t, steps, levels, "assistant.stage")
	if text, _ := stageEvent.fields["error"].(string); !strings.Contains(text, "assistant provider build: exit status 1") {
		t.Fatalf("stage step does not name its cause: %v", stageEvent.fields)
	}
	path, _ := build.fields["detail_path"].(string)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the failure record did not outlive the removed overlay: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("failure record mode = %v, want private", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record assistantPreparationFailure
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	want := []string{"building agent", "Error: Expected the connection export to match the public eve shape", "exit: token=[redacted]"}
	if record.AssistantAddress != "app/assistant/support" || record.Step != "assistant.build" || !slices.Equal(record.ProviderOutput, want) {
		t.Fatalf("failure record = %+v", record)
	}
	if strings.Contains(string(data), "abc123") {
		t.Fatal("the failure record kept a credential")
	}
	stagePath := assistantPreparationFailurePath(s.config.StateRoot, assistantDefinition{Name: "support"}, "assistant.stage")
	if _, err := os.Stat(stagePath); err != nil {
		t.Fatalf("the failed stage kept no record: %v", err)
	}
	// The helper is then prepared again by its retry, a path that runs no stage
	// step. No failure before a working preparation describes the assistant any
	// longer.
	s.config.BuildOverlay = func(context.Context, string, string, string, string) error { return nil }
	retry := s.captureStage().prepared["app/assistant/support"]
	retry.overlay = eve.Overlay{}
	if err := s.prepareOverlay(context.Background(), &retry); err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{path, stagePath} {
		if _, err := os.Stat(record); !os.IsNotExist(err) {
			t.Fatalf("a prepared assistant kept an earlier failure record %s: %v", record, err)
		}
	}
}

type assistantStepRecord struct {
	level  string
	fields map[string]any
}

func assistantStepEvent(t *testing.T, steps []map[string]any, levels []string, name string) assistantStepRecord {
	t.Helper()
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index]["name"] == name {
			return assistantStepRecord{level: levels[index], fields: steps[index]}
		}
	}
	t.Fatalf("no %s step among %v", name, steps)
	return assistantStepRecord{}
}
