package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

const detachedDevStartupFD = 3
const detachedDevStartupResultLimit = 64 * 1024

// Only the supervisor owns this pipe. Its normal stdout/stderr stay in the
// detached log, and close-on-exec excludes application children from the pipe.
type detachedDevStartupReporter struct {
	writer io.WriteCloser
}

type detachedDevStartupFailure struct {
	EventCount int              `json:"event_count"`
	OK         bool             `json:"ok"`
	ExitCode   int              `json:"exit_code"`
	Diagnostic graph.Diagnostic `json:"diagnostic"`
}

func openDetachedDevStartupReporter() (*detachedDevStartupReporter, error) {
	if !detachedDevChildMode() {
		return nil, nil
	}
	syscall.CloseOnExec(detachedDevStartupFD)
	file := os.NewFile(detachedDevStartupFD, "detached startup result")
	if file == nil {
		return nil, fmt.Errorf("detached startup result descriptor is unavailable")
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		_ = file.Close()
		return nil, fmt.Errorf("detached startup result descriptor is not a pipe")
	}
	return &detachedDevStartupReporter{writer: file}, nil
}

func (r *detachedDevStartupReporter) Close() error {
	if r == nil || r.writer == nil {
		return nil
	}
	err := r.writer.Close()
	r.writer = nil
	return err
}

func (r *detachedDevStartupReporter) Report(err error) error {
	if r == nil || r.writer == nil || err == nil {
		return err
	}
	err = preserveCLIDiagnostic(err)
	result := detachedDevStartupFailure{ExitCode: cliExitCode(err), Diagnostic: cliErrorDiagnostic(err)}
	// A parent using --wait registered may have already closed its reader.
	// Reporting must not change the supervisor's original failure.
	_ = newCLIEventWriter(r.writer).write("summary", true, result)
	_ = r.Close()
	return err
}

func readDetachedDevStartupResult(reader io.Reader) error {
	encoded, err := io.ReadAll(io.LimitReader(reader, detachedDevStartupResultLimit+1))
	if err != nil || len(encoded) > detachedDevStartupResultLimit {
		return detachedDevProtocolFailure()
	}
	if len(encoded) == 0 {
		return nil
	}
	envelope, err := machine.DecodeEvent[graph.Diagnostic](encoded, currentMachineSpecRevision())
	if err != nil || envelope.Event != "summary" || !envelope.Terminal || envelope.Sequence != 1 || envelope.Producer != cliProducer() {
		return detachedDevProtocolFailure()
	}
	payload, err := json.Marshal(envelope.Data)
	if err != nil {
		return detachedDevProtocolFailure()
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return detachedDevProtocolFailure()
	}
	for _, name := range []string{"event_count", "ok", "exit_code", "diagnostic"} {
		if value, ok := fields[name]; !ok || bytes.Equal(value, []byte("null")) {
			return detachedDevProtocolFailure()
		}
	}
	var result detachedDevStartupFailure
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.OK || result.EventCount != 0 || !validDetachedDevDiagnostic(result.Diagnostic, result.ExitCode) {
		return detachedDevProtocolFailure()
	}
	return &cliDiagnosticError{code: result.ExitCode, diagnostic: result.Diagnostic, startupReason: "child_failure"}
}

func validDetachedDevDiagnostic(diagnostic graph.Diagnostic, exitCode int) bool {
	definition, ok := spec.DiagnosticDefinitionFor(diagnostic.Code)
	if !ok || diagnostic.Severity != "error" || strings.TrimSpace(diagnostic.Message) == "" {
		return false
	}
	switch exitCode {
	case 1, 2, 3, 4, 5, 10:
	default:
		return false
	}
	if strings.HasPrefix(diagnostic.Code, "SCN90") {
		if diagnostic.Message != definition.Meaning || !strings.HasPrefix(diagnostic.ReportToken, "rpt_") || len(diagnostic.ReportToken) <= 4 {
			return false
		}
		for _, r := range strings.TrimPrefix(diagnostic.ReportToken, "rpt_") {
			if (r < 'a' || r > 'z') && (r < '2' || r > '7') {
				return false
			}
		}
	}
	return true
}

func detachedDevProtocolFailure() error {
	return &cliDiagnosticError{
		code: 3, startupReason: "protocol_error",
		diagnostic: compiler.TransportDiagnostic("failed_precondition", "The detached supervisor returned an invalid startup result."),
	}
}

// Cancellation interrupts in-flight session and route probes as soon as the
// supervisor fails. After exit, drain its pipe before choosing an exit-only
// error: the reader may still be decoding a result written just before exit.
func monitorDetachedDevStartup(ctx context.Context, cancel context.CancelCauseFunc, startup <-chan error, exited <-chan error) {
	devprocess.MonitorStartup(ctx, cancel, startup, exited, func(waitErr error) error {
		message := "The detached supervisor exited before the requested startup readiness."
		if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
			message = fmt.Sprintf("The detached supervisor exited before the requested startup readiness (exit code %d).", exitErr.ExitCode())
		}
		return &cliDiagnosticError{code: 3, startupReason: "child_exit", diagnostic: compiler.TransportDiagnostic("failed_precondition", message)}
	})
}

func detachedDevWaitFailure(err error, pid int, waitMode string, timeout time.Duration, logPath string) error {
	reported, ok := errors.AsType[*cliDiagnosticError](err)
	if !ok {
		if errors.Is(err, context.DeadlineExceeded) {
			reported = &cliDiagnosticError{code: 3, startupReason: "timeout", diagnostic: compiler.TransportDiagnostic("failed_precondition",
				fmt.Sprintf("The detached supervisor did not reach %s within %s.", waitMode, timeout))}
		} else {
			reported = &cliDiagnosticError{code: cliExitCode(err), startupReason: "wait_failure", diagnostic: cliErrorDiagnostic(err)}
		}
	}
	diagnostic := reported.diagnostic
	diagnostic.Details = maps.Clone(diagnostic.Details)
	if diagnostic.Details == nil {
		diagnostic.Details = make(map[string]any)
	}
	diagnostic.Details["detached_startup"] = map[string]any{
		"reason": reported.startupReason, "owner_pid": pid, "wait": waitMode, "log_path": logPath,
	}
	if logPath != "" {
		diagnostic.Suggestions = append(append([]string(nil), diagnostic.Suggestions...), "Inspect detached startup log: "+logPath)
	}
	publicErr := &cliDiagnosticError{code: reported.code, diagnostic: diagnostic}
	if logPath != "" {
		return fmt.Errorf("detached scenery up process %d failed: %w; see %s", pid, publicErr, logPath)
	}
	return fmt.Errorf("scenery up process %d did not reach the requested readiness: %w", pid, publicErr)
}

func stopDetachedDevChild(cmd *exec.Cmd, exited <-chan struct{}) {
	devprocess.StopDetached(cmd, exited)
}
