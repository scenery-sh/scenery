package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// firstExecutionWarn is the extra time of a new executable's first start
// above which every development rebuild is noticeably slower.
const firstExecutionWarn = 150 * time.Millisecond

// FirstExecutionInfo is the measured start of one new executable and of the
// same executable again.
type FirstExecutionInfo struct {
	First  time.Duration
	Repeat time.Duration
}

var errFirstExecutionUnsupported = errors.New("first execution is measured only for an installed scenery on macOS")

// measureFirstExecution starts a new copy of this executable twice. macOS
// assesses an executable it has not started before; a development rebuild
// links a new executable for every changed service, so that assessment is
// part of every edit. The copy runs with its own empty agent home.
func measureFirstExecution(ctx context.Context, cacheRoot string) (FirstExecutionInfo, error) {
	// A test binary started again would run its tests, which would measure
	// again; only the installed command reports its version and exits.
	if runtime.GOOS != "darwin" || testing.Testing() {
		return FirstExecutionInfo{}, errFirstExecutionUnsupported
	}
	self, err := os.Executable()
	if err != nil {
		return FirstExecutionInfo{}, err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return FirstExecutionInfo{}, err
	}
	dir, err := os.MkdirTemp(cacheRoot, "doctor-first-execution-")
	if err != nil {
		return FirstExecutionInfo{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	candidate := filepath.Join(dir, "candidate")
	if err := copyExecutable(self, candidate); err != nil {
		return FirstExecutionInfo{}, err
	}
	start := func() (time.Duration, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, candidate, "version")
		command.Env = append(os.Environ(), "SCENERY_AGENT_HOME="+filepath.Join(dir, "home"))
		started := time.Now()
		err := command.Run()
		return time.Since(started), err
	}
	var info FirstExecutionInfo
	if info.First, err = start(); err != nil {
		return FirstExecutionInfo{}, err
	}
	if info.Repeat, err = start(); err != nil {
		return FirstExecutionInfo{}, err
	}
	return info, nil
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// FirstExecutionCheck reports how much longer a new executable takes to start
// than one the host has started before.
func FirstExecutionCheck(info FirstExecutionInfo, err error) Check {
	check := Check{
		ID:       "host.first_execution",
		Category: "host",
		Name:     "First execution of a new executable",
		Status:   StatusOK,
		Severity: SeverityInformational,
	}
	if err != nil {
		check.Status = StatusSkipped
		check.Message = "not measured: " + err.Error()
		return check
	}
	extra := max(info.First-info.Repeat, 0)
	check.Observed = map[string]any{"first_ms": info.First.Milliseconds(), "repeat_ms": info.Repeat.Milliseconds(), "extra_ms": extra.Milliseconds()}
	check.Message = fmt.Sprintf("a new executable starts in %d ms, %d ms when started again", info.First.Milliseconds(), info.Repeat.Milliseconds())
	if extra >= firstExecutionWarn {
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = fmt.Sprintf("a new executable takes %d ms longer to start than a repeated one; every development rebuild pays this for each service it relinks", extra.Milliseconds())
		check.SuggestedAction = "In System Settings, Privacy & Security, Developer Tools, enable the terminal or agent application that starts scenery, restart that application, and run scenery doctor again."
	}
	return check
}
