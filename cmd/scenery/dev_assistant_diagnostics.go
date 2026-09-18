package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A development assistant whose helper cannot be prepared answers neutrally
// and is retried, so the failure itself must stay readable: the step that
// failed, its error, and the provider's own last output. That output belongs to
// a private overlay that a failed preparation removes, so a bounded, redacted
// record of the last failure of each step is kept beside the overlays in the
// assistant state directory and named by the step's build event.
const (
	assistantDiagnosticsDir = "diagnostics"
	// assistantDiagnosticLines bounds the provider output a record keeps.
	assistantDiagnosticLines = 64
	// assistantDiagnosticBytes bounds one output line and an error text.
	assistantDiagnosticBytes = 2000
)

// assistantOutputTail keeps the last lines a provider command writes. A line
// may arrive across several writes; the last one may have no newline, which
// flush keeps once the command stopped writing.
type assistantOutputTail struct {
	mu      sync.Mutex
	lines   []string
	pending []byte
}

func (tail *assistantOutputTail) Write(data []byte) (int, error) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	tail.pending = append(tail.pending, data...)
	for {
		index := bytes.IndexByte(tail.pending, '\n')
		if index < 0 {
			break
		}
		tail.addLocked(string(tail.pending[:index]))
		tail.pending = tail.pending[index+1:]
	}
	if len(tail.pending) > assistantDiagnosticBytes {
		tail.addLocked(string(tail.pending))
		tail.pending = nil
	}
	return len(data), nil
}

func (tail *assistantOutputTail) flush() {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if len(tail.pending) > 0 {
		tail.addLocked(string(tail.pending))
		tail.pending = nil
	}
}

func (tail *assistantOutputTail) addLocked(line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	line = assistantTerminalControlPattern.ReplaceAllString(line, "")
	if strings.TrimSpace(line) == "" {
		return
	}
	tail.lines = append(tail.lines, boundAssistantDiagnostic(redactAssistantDiagnostic(line)))
	if len(tail.lines) > assistantDiagnosticLines {
		tail.lines = tail.lines[len(tail.lines)-assistantDiagnosticLines:]
	}
}

func (tail *assistantOutputTail) Lines() []string {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	return append([]string(nil), tail.lines...)
}

// assistantProviderError is the failure of a provider command. Its text names
// the command and its exit; the command's output travels beside it, so the
// output reaches the private record without entering any public message.
type assistantProviderError struct {
	command string
	err     error
	output  []string
}

func (e *assistantProviderError) Error() string { return e.command + ": " + e.err.Error() }

func (e *assistantProviderError) Unwrap() error { return e.err }

// runAssistantProviderCommand runs a provider command and keeps the bounded
// tail of what it wrote for the failure it may return.
func runAssistantProviderCommand(command *exec.Cmd, name string) error {
	return captureAssistantProviderOutput(name, func(output io.Writer) error {
		command.Stdout, command.Stderr = output, output
		return command.Run()
	})
}

// captureAssistantProviderOutput runs a provider step that writes to output.
// The step has stopped writing when run returns, so its last line is kept even
// without a newline.
func captureAssistantProviderOutput(name string, run func(io.Writer) error) error {
	tail := &assistantOutputTail{}
	err := run(tail)
	tail.flush()
	if err != nil {
		return &assistantProviderError{command: name, err: err, output: tail.Lines()}
	}
	return nil
}

var (
	// assistantTerminalControlPattern matches terminal color and cursor
	// sequences, which a provider writes for a terminal and which would spend
	// the line bound without saying anything.
	assistantTerminalControlPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	assistantCredentialPattern      = regexp.MustCompile(`(?i)\b(authorization|bearer|token|secret|password|passwd|api[_-]?key)\b(\s*[:=]\s*|\s+)("[^"]*"|\S+)`)
	assistantURLCredentialsPattern  = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)
)

// redactAssistantDiagnostic removes credential values a provider may print:
// values after credential-like keys and passwords in URLs.
func redactAssistantDiagnostic(text string) string {
	text = assistantCredentialPattern.ReplaceAllString(text, "${1}${2}[redacted]")
	return assistantURLCredentialsPattern.ReplaceAllString(text, "://[redacted]@")
}

func boundAssistantDiagnostic(text string) string {
	if len(text) > assistantDiagnosticBytes {
		return text[:assistantDiagnosticBytes] + "…"
	}
	return text
}

// assistantPreparationFailure is the private record of the last failure of one
// preparation step of one assistant.
type assistantPreparationFailure struct {
	Kind             string    `json:"kind"`
	AssistantAddress string    `json:"assistant_address"`
	Step             string    `json:"step"`
	Error            string    `json:"error"`
	ProviderOutput   []string  `json:"provider_output_tail,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
}

// assistantPreparationFailurePath is the record of step for definition.
func assistantPreparationFailurePath(stateRoot string, definition assistantDefinition, step string) string {
	return filepath.Join(stateRoot, assistantDiagnosticsDir, sanitizeRouteLabel(definition.Name)+"-"+strings.ReplaceAll(step, ".", "-")+".json")
}

// recordAssistantPreparationStep keeps the record of a failed step and removes
// the record of a step that succeeded, so a record describes the step's latest
// attempt. It returns the record's path when one was written; a record that
// cannot be written changes no behavior.
func recordAssistantPreparationStep(stateRoot string, definition assistantDefinition, step string, stepErr error, now time.Time) string {
	if strings.TrimSpace(stateRoot) == "" {
		return ""
	}
	path := assistantPreparationFailurePath(stateRoot, definition, step)
	if stepErr == nil {
		_ = os.Remove(path)
		return ""
	}
	// A cancelled attempt did not fail for a cause of its own; the step's
	// latest failure, if any, keeps describing it.
	if errors.Is(stepErr, context.Canceled) {
		return ""
	}
	record := assistantPreparationFailure{
		Kind: "scenery.assistant.preparation-failure", AssistantAddress: definition.Address, Step: step,
		Error: assistantStepErrorText(stepErr), RecordedAt: now.UTC(),
	}
	var provider *assistantProviderError
	if errors.As(stepErr, &provider) {
		record.ProviderOutput = provider.output
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".failure-*")
	if err != nil {
		return ""
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	_, writeErr := temporary.Write(append(data, '\n'))
	if closeErr := temporary.Close(); writeErr != nil || closeErr != nil {
		return ""
	}
	if err := os.Chmod(temporary.Name(), 0o600); err != nil {
		return ""
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return ""
	}
	return path
}

// assistantPreparationSteps are the steps a failure record may describe.
var assistantPreparationSteps = []string{"assistant.dependencies", "assistant.build", "assistant.cache_restore", "assistant.cache_publish", "assistant.stage"}

// clearAssistantPreparationFailures removes every failure record of an
// assistant once it has been prepared: a working preparation supersedes the
// failures before it, whichever path prepared it.
func clearAssistantPreparationFailures(stateRoot string, definition assistantDefinition) {
	if strings.TrimSpace(stateRoot) == "" {
		return
	}
	for _, step := range assistantPreparationSteps {
		_ = os.Remove(assistantPreparationFailurePath(stateRoot, definition, step))
	}
}

// assistantStepErrorText is the redacted, bounded text of a failed step.
func assistantStepErrorText(err error) string {
	return boundAssistantDiagnostic(redactAssistantDiagnostic(strings.TrimSpace(err.Error())))
}
