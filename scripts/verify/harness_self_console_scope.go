package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

// A standard-library PTY driver exercises the actual raw terminal branch.
// It quits after a rendered fixture event and joins its exact child even on
// failure; pipes alone would exercise the console's plain-logs fallback.
const harnessConsolePTY = `
import fcntl, json, os, pty, select, struct, subprocess, sys, termios, time
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 160, 0, 0))
env = os.environ.copy()
env.pop("CI", None)
env["TERM"] = "xterm-256color"
output = bytearray()
proc = None
proof = {"stdin_tty": os.isatty(slave), "stdout_tty": os.isatty(slave)}
try:
    proc = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=slave,
                            env=env, start_new_session=True)
    proof["pid"] = proc.pid
    os.close(slave)
    slave = None
    deadline = time.monotonic() + 12
    quit_sent = False
    while time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], 0.1)
        if ready:
            try:
                chunk = os.read(master, 65536)
            except OSError:
                break
            if not chunk:
                break
            output.extend(chunk)
            if len(output) > 1048576:
                proof["error"] = "console output exceeded probe bound"
                break
            if not quit_sent and b"\x1b[?1049h" in output and b"observability-probe-log" in output:
                os.write(master, b"q")
                quit_sent = True
        elif proc.poll() is not None:
            break
    if proc.poll() is None:
        try:
            proc.wait(timeout=1)
        except subprocess.TimeoutExpired:
            proof["error"] = "console did not finish within probe bound"
finally:
    if proc is not None:
        if proc.poll() is None:
            proc.kill()
        proof["exit_code"] = proc.wait(timeout=2)
        proof["process_joined"] = True
    os.close(master)
    if slave is not None:
        os.close(slave)
    proof["pty_closed"] = True
    proof["output"] = output.decode("utf-8", "replace")
    proof["raw_terminal"] = b"\x1b[?1049h" in output
    proof["fixture_rendered"] = b"observability-probe-log" in output
    print(json.dumps(proof))
`

func proveHarnessConsoleScope(p *worktreeRuntimeProbe, root string) (map[string]any, error) {
	proof := map[string]any{}
	parent, err := harnessLiveSession(p.ctx, p.home, root)
	if err != nil {
		return proof, err
	}
	run := func(selected string) (map[string]any, error) {
		ctx, cancel := context.WithTimeout(p.ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "python3", "-c", harnessConsolePTY, p.binary, "console", "--app-root", selected, "--grep", "observability-probe-log", "-o", "jsonl")
		cmd.Dir, cmd.Env = root, p.env
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("console PTY driver: %w: %s", err, output)
		}
		var result map[string]any
		if err := json.Unmarshal(output, &result); err != nil {
			return nil, err
		}
		if result["process_joined"] != true || result["pty_closed"] != true || result["error"] != nil {
			return result, fmt.Errorf("console PTY driver did not finish cleanly: %v", result["error"])
		}
		return result, nil
	}
	before, err := run(parent.AppRoot)
	proof["parent_before"] = before
	if err != nil || before["raw_terminal"] != true || before["fixture_rendered"] != true || before["exit_code"] != float64(0) {
		return proof, fmt.Errorf("parent console did not render the fixture through a raw terminal: %v", err)
	}
	child := filepath.Join(parent.AppRoot, ".scenery", "absent-console-child")
	childProof, childErr := run(child)
	proof["child_root"], proof["child"] = child, childProof
	paths, err := localagent.PathsForWorktree(p.home, child)
	if err != nil {
		return proof, err
	}
	if _, err := os.Lstat(paths.Directory); !errors.Is(err, os.ErrNotExist) {
		return proof, fmt.Errorf("console created child authority: %v", err)
	}
	proof["child_authority_absent"] = true
	after, err := run(parent.AppRoot)
	proof["parent_after"] = after
	if err != nil || after["raw_terminal"] != true || after["fixture_rendered"] != true || after["exit_code"] != float64(0) {
		return proof, fmt.Errorf("parent console after child request: %v", err)
	}
	current, err := harnessLiveSession(p.ctx, p.home, root)
	if err != nil || current.SessionID != parent.SessionID || current.Owner != parent.Owner || current.OwnerPID != parent.OwnerPID || current.AppPID != parent.AppPID {
		return proof, fmt.Errorf("console changed parent runtime identity: %v", err)
	}
	if err := localagent.VerifyOwner(current.Owner); err != nil {
		return proof, err
	}
	proof["parent_root"], proof["parent_owner"], proof["parent_app_pid"] = parent.AppRoot, current.Owner, current.AppPID
	output, _ := childProof["output"].(string)
	refused := childErr == nil && childProof["exit_code"] == float64(2) && strings.Contains(output, "SCN8001") && strings.Contains(output, child) && childProof["raw_terminal"] == false && childProof["fixture_rendered"] == false
	proof["child_refused"] = refused
	if !refused {
		return proof, fmt.Errorf("interactive console did not refuse the exact absent child: %v", childErr)
	}
	return proof, nil
}
