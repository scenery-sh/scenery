package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
)

// Exercise plain logs, whose retained identity selection differs from logs query.
// The absent child shares the parent's cwd but must never share its log scope.
func proveHarnessPlainLogsScope(p *worktreeRuntimeProbe, root string) (map[string]any, error) {
	proof := map[string]any{}
	parent, err := harnessLiveSession(p.ctx, p.home, root)
	if err != nil {
		return proof, err
	}
	readLogs := func() (int, error) {
		output, err := p.run(root, p.binary, "logs", "--app-root", root, "--grep", "observability-probe-log", "-o", "jsonl")
		if err != nil {
			return 0, err
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		count := 0
		for {
			var event struct {
				Event string `json:"event"`
				Data  struct {
					App     struct{ Root string } `json:"app"`
					Message string                `json:"message"`
				} `json:"data"`
			}
			if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
				return count, nil
			} else if err != nil {
				return count, err
			}
			if event.Event == "summary" {
				continue
			}
			if event.Event != "event" || event.Data.App.Root != parent.AppRoot || !bytes.Contains([]byte(event.Data.Message), []byte("observability-probe-log")) {
				return count, fmt.Errorf("plain logs returned an event outside the selected fixture root")
			}
			count++
		}
	}
	readback, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	var count int
	var readErr error
	if err := waitForHarnessCondition(readback, func() bool {
		count, readErr = readLogs()
		return readErr == nil && count > 0
	}); err != nil {
		return proof, fmt.Errorf("plain parent logs: %w: %v", err, readErr)
	}
	proof["parent_root"], proof["parent_events_before"] = parent.AppRoot, count
	child := filepath.Join(parent.AppRoot, ".scenery", "absent-logs-child")
	paths, err := localagent.PathsForWorktree(p.home, child)
	if err != nil {
		return proof, err
	}
	output, childErr := p.run(root, p.binary, "logs", "--app-root", child, "--grep", "observability-probe-log", "-o", "jsonl")
	proof["child_root"], proof["child_output"] = child, string(output)
	var exit *exec.ExitError
	refused := errors.As(childErr, &exit) && exit.ExitCode() == 2 && bytes.Contains(output, []byte("SCN8001")) && bytes.Contains(output, []byte(child)) && !bytes.Contains(output, []byte(`"event":"event"`))
	proof["child_refused"] = refused
	if _, err := os.Lstat(paths.Directory); !errors.Is(err, os.ErrNotExist) {
		return proof, fmt.Errorf("plain child logs created retained authority: %v", err)
	}
	proof["child_authority_absent"] = true
	count, err = readLogs()
	if err != nil || count == 0 {
		return proof, fmt.Errorf("plain parent logs after child request: %v (%d events)", err, count)
	}
	after, err := harnessLiveSession(p.ctx, p.home, root)
	if err != nil || after.SessionID != parent.SessionID || after.OwnerPID != parent.OwnerPID || after.Owner != parent.Owner || after.AppPID != parent.AppPID {
		return proof, fmt.Errorf("plain logs changed parent runtime identity: %v", err)
	}
	if err := localagent.VerifyOwner(after.Owner); err != nil {
		return proof, err
	}
	proof["parent_events_after"], proof["parent_owner"], proof["parent_app_pid"] = count, after.Owner, after.AppPID
	if !refused {
		return proof, fmt.Errorf("plain logs did not refuse the exact absent child: %v", childErr)
	}
	return proof, nil
}
