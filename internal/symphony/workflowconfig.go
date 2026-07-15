package symphony

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WorkflowRuntime is the runner-facing configuration parsed from a workflow's
// WORKFLOW.md: agent limits from the YAML-ish front matter plus the prompt
// template body.
type WorkflowRuntime struct {
	PromptTemplate string
	MaxConcurrency int
	MaxTurns       int
	MaxAttempts    int
	TurnTimeout    time.Duration
	StallTimeout   time.Duration
}

// LoadWorkflowRuntime resolves the runtime workflow configuration for one app:
// the stored workflow markdown wins when present, otherwise WORKFLOW.md in the
// app root is required. Defaults are 20 turns, 3 attempts, a one-hour turn
// timeout, and a five-minute stall timeout.
func LoadWorkflowRuntime(appRoot string, workflow Workflow) (WorkflowRuntime, error) {
	out := WorkflowRuntime{
		MaxTurns:     20,
		MaxAttempts:  3,
		TurnTimeout:  time.Hour,
		StallTimeout: 5 * time.Minute,
	}
	if text := strings.TrimSpace(workflow.WorkflowMarkdown); text != "" {
		return ParseWorkflowRuntime(text, out)
	}
	data, err := os.ReadFile(filepath.Join(appRoot, "WORKFLOW.md"))
	if errors.Is(err, os.ErrNotExist) {
		return out, errors.New("missing WORKFLOW.md")
	}
	if err != nil {
		return out, err
	}
	return ParseWorkflowRuntime(string(data), out)
}

// ParseWorkflowRuntime parses WORKFLOW.md text into out: an optional
// `---`-delimited front matter carrying `agent:` limits followed by the prompt
// template body. Text without front matter is all prompt template.
func ParseWorkflowRuntime(text string, out WorkflowRuntime) (WorkflowRuntime, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		out.PromptTemplate = text
		return out, nil
	}
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			if err := applyWorkflowConfig(lines[1:i], &out); err != nil {
				return out, err
			}
			out.PromptTemplate = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			return out, nil
		}
	}
	return out, errors.New("WORKFLOW.md front matter is not closed")
}

func applyWorkflowConfig(lines []string, out *WorkflowRuntime) error {
	section := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(trimmed, ":") {
			section = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if section != "agent" {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "max_concurrent_agents":
			n, err := parsePositiveInt(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.MaxConcurrency = n
		case "max_turns":
			n, err := parsePositiveInt(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.MaxTurns = n
		case "max_attempts":
			n, err := parsePositiveInt(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.MaxAttempts = n
		case "turn_timeout_ms":
			timeout, err := parsePositiveDurationMillis(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.TurnTimeout = timeout
		case "stall_timeout_ms":
			timeout, err := parsePositiveDurationMillis(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.StallTimeout = timeout
		}
	}
	return nil
}

func parsePositiveInt(value string) (int, error) {
	value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
	value = strings.Trim(value, `"'`)
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n, nil
}

func parsePositiveDurationMillis(value string) (time.Duration, error) {
	n, err := parsePositiveInt(value)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Millisecond, nil
}
