package symphony

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseWorkflowRuntimeFrontMatter(t *testing.T) {
	t.Parallel()

	defaults := WorkflowRuntime{
		MaxTurns:     20,
		MaxAttempts:  3,
		TurnTimeout:  time.Hour,
		StallTimeout: 5 * time.Minute,
	}
	out, err := ParseWorkflowRuntime("---\nagent:\n  max_concurrent_agents: 2\n  max_turns: 7\n  max_attempts: 1\n  turn_timeout_ms: 1500 # comment\n  stall_timeout_ms: \"2500\"\n  unknown_future_key: ignored\nother:\n  max_turns: 99\n---\nTicket {{ issue.identifier }}", defaults)
	if err != nil {
		t.Fatal(err)
	}
	want := WorkflowRuntime{
		PromptTemplate: "Ticket {{ issue.identifier }}",
		MaxConcurrency: 2,
		MaxTurns:       7,
		MaxAttempts:    1,
		TurnTimeout:    1500 * time.Millisecond,
		StallTimeout:   2500 * time.Millisecond,
	}
	if out != want {
		t.Fatalf("out = %+v, want %+v", out, want)
	}
}

func TestParseWorkflowRuntimeWithoutFrontMatterIsAllPrompt(t *testing.T) {
	t.Parallel()

	defaults := WorkflowRuntime{MaxTurns: 20, MaxAttempts: 3, TurnTimeout: time.Hour, StallTimeout: 5 * time.Minute}
	out, err := ParseWorkflowRuntime("  Just the prompt body.\n", defaults)
	if err != nil {
		t.Fatal(err)
	}
	if out.PromptTemplate != "Just the prompt body." {
		t.Fatalf("prompt = %q", out.PromptTemplate)
	}
	if out.MaxTurns != 20 || out.MaxAttempts != 3 || out.TurnTimeout != time.Hour || out.StallTimeout != 5*time.Minute {
		t.Fatalf("defaults changed: %+v", out)
	}
}

func TestParseWorkflowRuntimeRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"unclosed front matter": "---\nagent:\n  max_turns: 5",
		"non-numeric":           "---\nagent:\n  max_turns: soon\n---\nPrompt",
		"non-positive":          "---\nagent:\n  max_attempts: 0\n---\nPrompt",
		"negative timeout":      "---\nagent:\n  turn_timeout_ms: -5\n---\nPrompt",
	} {
		if _, err := ParseWorkflowRuntime(text, WorkflowRuntime{}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadWorkflowRuntimePrefersStoredMarkdownAndRequiresWorkflowFile(t *testing.T) {
	t.Parallel()

	appRoot := t.TempDir()
	if _, err := LoadWorkflowRuntime(appRoot, Workflow{}); err == nil || !strings.Contains(err.Error(), "missing WORKFLOW.md") {
		t.Fatalf("missing file err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "WORKFLOW.md"), []byte("---\nagent:\n  max_turns: 4\n---\nFrom file"), 0o644); err != nil {
		t.Fatal(err)
	}
	fromFile, err := LoadWorkflowRuntime(appRoot, Workflow{})
	if err != nil || fromFile.MaxTurns != 4 || fromFile.PromptTemplate != "From file" {
		t.Fatalf("from file = %+v, err = %v", fromFile, err)
	}
	stored, err := LoadWorkflowRuntime(appRoot, Workflow{WorkflowMarkdown: "From store"})
	if err != nil || stored.PromptTemplate != "From store" || stored.MaxTurns != 20 {
		t.Fatalf("stored = %+v, err = %v", stored, err)
	}
}
