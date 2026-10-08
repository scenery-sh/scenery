package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/rotatinglog"
)

type harnessReportDescriptor struct {
	Path   string `json:"path"`
	FD     string `json:"fd"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Inode  string `json:"inode"`
}

// A positive descriptor position proves capture occurred in the unmodified
// product before mutation. Stop only the exec.Cmd child owned by this probe.
func stopHarnessReportAtRead(ctx context.Context, c *harnessReportChild, paths []string) (map[string]any, []harnessReportDescriptor, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		descriptors, err := observeHarnessReportDescriptors(ctx, c.command.Process.Pid, paths)
		if err != nil {
			return nil, nil, err
		}
		started := false
		for _, d := range descriptors {
			started = started || d.Offset > 0
		}
		if len(descriptors) == len(paths) && started {
			owner := localagent.CaptureOwner(c.command.Process.Pid, "owned report snapshot probe")
			if owner.StartedAt == "" || owner.Exe == "" || owner.CmdlineHash == "" {
				return nil, nil, errors.New("owned report child fingerprint unavailable")
			}
			executable, err := filepath.EvalSymlinks(c.command.Path)
			if err != nil || executable != owner.Exe {
				return nil, nil, errors.New("owned report child executable differs from prepared binary")
			}
			if err := harnessReportStop(c.command.Process); err != nil {
				return nil, nil, err
			}
			for {
				state, ok := devprocess.Inspect(owner.PID)
				if ok && strings.HasPrefix(state.State, "T") {
					break
				}
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-c.done:
					return nil, nil, errors.New("owned child exited before confirmed stop")
				case <-ticker.C:
				}
			}
			stopped := localagent.CaptureOwner(owner.PID, "")
			if owner.PID != stopped.PID || owner.StartedAt != stopped.StartedAt || owner.Exe != stopped.Exe || owner.CmdlineHash != stopped.CmdlineHash {
				return nil, nil, errors.New("owned report child fingerprint changed")
			}
			descriptors, err = observeHarnessReportDescriptors(ctx, owner.PID, paths)
			if err != nil || len(descriptors) != len(paths) {
				return nil, nil, fmt.Errorf("stopped descriptor inventory changed: %w", err)
			}
			return map[string]any{"owner": owner, "observed_stopped_owner": stopped, "stopped_state_confirmed": true, "descriptors": descriptors}, descriptors, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-c.done:
			return nil, nil, errors.New("owned report exited before descriptor read rendezvous; mutation unproved")
		case <-ticker.C:
		}
	}
}

func proveHarnessReportMutation(ctx context.Context, repo, name, format string) (proof map[string]any, resultErr error) {
	f, err := newHarnessReportFixture()
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupErr := os.RemoveAll(f.root)
		_, absence := os.Lstat(f.root)
		if !os.IsNotExist(absence) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("snapshot fixture remains: %v", absence))
		}
		resultErr = errors.Join(resultErr, cleanupErr)
		if proof != nil && cleanupErr == nil {
			proof["cleanup"] = "actual child exited; owned fixture removed and root absence verified"
		}
	}()
	const size = 64 << 20
	rowSize := 512
	path, row := f.cli, f.cliRow()
	transcripts := name == "claude-append" || name == "codex-truncate"
	var prefix, appended []byte
	if err := f.write(f.cli, nil); err != nil {
		return nil, err
	}
	switch name {
	case "archived-cli-append":
		path = filepath.Join(f.home, "telemetry-archive", "owned", "telemetry.jsonl")
	case "supervisor-truncate":
		path, row = f.log, f.supervisorRow()
	case "writer-rotate":
		path, row, rowSize = f.log, f.supervisorRow(), 256
	case "claude-append", "codex-truncate":
		kind := strings.Split(name, "-")[0]
		path, prefix = f.transcript(kind, "before")
		_, appended = f.transcript(kind, "after")
		var padded bytes.Buffer
		for _, line := range bytes.Split(bytes.TrimSpace(prefix), []byte{'\n'}) {
			fixed, err := harnessFixedReportRow(line, rowSize)
			if err != nil {
				return nil, err
			}
			padded.Write(fixed)
		}
		prefix = padded.Bytes()
		// Exercise the actual decoder's ordinary non-tool path rather than its
		// fast byte filter, keeping the owned descriptor observable without
		// product hooks, fake clocks, sleeps, or unmatched tool calls.
		marker := "call"
		if kind == "claude" {
			marker = "tool_use"
		}
		row = []byte(fmt.Sprintf(`{"type":"ignored","marker":%q,"padding":[%s0]}`, marker, strings.Repeat("1,", 180)))
	}
	row, err = harnessFixedReportRow(row, rowSize)
	if err != nil {
		return nil, err
	}
	wantSize := int64(size)
	var writer *rotatinglog.Writer
	if name == "writer-rotate" {
		wantSize = int64((rotatinglog.Backups + 1) * rotatinglog.SegmentBytes)
		writer, err = rotatinglog.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { resultErr = errors.Join(resultErr, writer.Close()) }()
		input := bytes.Repeat(row, int(wantSize)/rowSize)
		if n, err := writer.Write(input); err != nil || n != len(input) {
			return nil, fmt.Errorf("genuine rotation write %d: %w", n, err)
		}
	} else {
		input := append(bytes.Clone(prefix), bytes.Repeat(row, (size-len(prefix))/rowSize)...)
		if err := f.write(path, input); err != nil {
			return nil, err
		}
	}
	paths := []string{path}
	if writer != nil {
		paths = rotatinglog.Segments(path)
	}
	infos := make([]os.FileInfo, len(paths))
	for i, selected := range paths {
		infos[i], err = os.Stat(selected)
		if err != nil {
			return nil, err
		}
	}
	readBytes := wantSize
	mutate := func(c *harnessReportChild) (map[string]any, error) {
		captured, descriptors, err := stopHarnessReportAtRead(ctx, c, paths)
		if err != nil {
			return captured, err
		}
		if strings.HasSuffix(name, "truncate") {
			readBytes = descriptors[0].Offset
			if readBytes <= int64(len(prefix)) || readBytes >= wantSize {
				return captured, errors.New("truncation rendezvous has no remaining selected bytes")
			}
			if err := os.Truncate(path, 0); err != nil {
				return captured, err
			}
			captured["mutation"] = "same opened inode truncated to zero"
		} else {
			if appended == nil {
				appended = bytes.Repeat(row, 64)
			}
			if writer != nil {
				if n, err := writer.Write(appended); err != nil || n != len(appended) {
					return captured, fmt.Errorf("genuine live rotation %d: %w", n, err)
				}
				captured["mutation"] = "genuine writer rotates/unlinks names after all four descriptors were captured"
			} else {
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					return captured, err
				}
				n, writeErr := file.Write(appended)
				if err := errors.Join(writeErr, file.Close()); err != nil || n != len(appended) {
					return captured, fmt.Errorf("owned append %d: %w", n, err)
				}
				captured["mutation"] = "appended after capture; appended rows must remain outside report extent"
			}
		}
		if writer == nil {
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(infos[0], after) {
				return captured, errors.New("fixture mutation did not preserve captured inode")
			}
			captured["same_inode"], captured["after_size"] = true, after.Size()
		} else {
			after, err := os.Stat(path)
			if err != nil || os.SameFile(infos[len(infos)-1], after) {
				return captured, errors.New("genuine writer did not rotate active inode")
			}
			captured["active_inode_changed"] = true
		}
		captured["selected_bytes"], captured["expected_read_bytes"] = wantSize, readBytes
		return captured, harnessReportResume(c.command.Process)
	}
	r, proof, err := f.run(ctx, repo, format, transcripts, mutate)
	if err != nil {
		return proof, err
	}
	if format == "human" {
		if !strings.Contains(proof["stdout"].(string), fmt.Sprintf("read %d / captured %d bytes", readBytes, wantSize)) {
			return proof, errors.New("human physical coverage differs from captured source extent")
		}
		if strings.HasSuffix(name, "truncate") && !strings.Contains(proof["stdout"].(string), "read in part") {
			return proof, errors.New("human partial evidence missing")
		}
		return proof, nil
	}
	switch name {
	case "active-cli-append", "archived-cli-append":
		found := false
		for _, source := range r.Sources.CLI {
			if source.Path == path {
				found = harnessReportExtent(source.SnapshotBytes, source.ReadBytes, wantSize) && source.Records == int(wantSize)/rowSize && source.Status == "complete" && source.Archived == (name == "archived-cli-append")
			}
		}
		if !found || r.CLI.Records != int(wantSize)/rowSize {
			return proof, errors.New("CLI append crossed captured active/archive boundary")
		}
	case "supervisor-truncate", "writer-rotate":
		if len(r.Sources.Supervisor) != 1 {
			return proof, errors.New("supervisor source missing")
		}
		source := r.Sources.Supervisor[0]
		if source.SnapshotBytes == nil || *source.SnapshotBytes != wantSize || source.ReadBytes != readBytes || source.Records != int(readBytes)/rowSize || source.Invalid != 0 {
			return proof, fmt.Errorf("supervisor physical/prefix evidence %+v, expected %d", source, readBytes)
		}
		if name == "supervisor-truncate" {
			finding := false
			for _, item := range r.Findings {
				finding = finding || item.Code == "sources.incomplete"
			}
			if source.Status != "partial" || r.Sources.SupervisorLogsPartial != 1 || !finding {
				return proof, fmt.Errorf("truncated source failed to expose one logical partial: %+v / %+v", r.Sources, r.Findings)
			}
		} else if source.Status != "complete" || source.Segments != 4 || r.Sources.SupervisorRotated != 1 {
			return proof, errors.New("captured rotated descriptors lost intact stream")
		}
	case "claude-append", "codex-truncate":
		source := r.Sources.Transcripts
		if source.SnapshotBytes == nil || *source.SnapshotBytes != wantSize || source.ReadBytes != readBytes || r.Agents == nil || r.Agents.SceneryCommands != 1 || r.Agents.SceneryOutcomeUnknown != 0 || source.InvalidRecords != 0 {
			return proof, fmt.Errorf("transcript physical/original-command evidence %+v / %+v", source, r.Agents)
		}
		if name == "codex-truncate" && (source.Partial != 1 || source.Read != 0) {
			return proof, errors.New("truncated transcript was reported complete")
		}
		if name == "claude-append" && (source.Partial != 0 || source.Read != 1) {
			return proof, errors.New("intact captured transcript marked partial")
		}
	}
	return proof, nil
}
