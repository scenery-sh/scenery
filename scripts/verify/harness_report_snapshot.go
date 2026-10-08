package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/rotatinglog"
	"scenery.sh/internal/telemetryreport"
)

type harnessReportFixture struct {
	root, home, cli, log string
	at                   time.Time
}

func newHarnessReportFixture() (*harnessReportFixture, error) {
	root, err := os.MkdirTemp("", "scenery-report-snapshot-probe-*")
	if err != nil {
		return nil, err
	}
	f := &harnessReportFixture{root: root, home: filepath.Join(root, "private-agent"), at: time.Now().UTC().Add(-time.Minute)}
	f.cli, f.log = filepath.Join(f.home, "telemetry.jsonl"), filepath.Join(f.home, "agent", "dev", "owned.log")
	if err := os.MkdirAll(filepath.Dir(f.log), 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return f, nil
}

func (f *harnessReportFixture) write(path string, input []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, input, 0o600)
}

func (f *harnessReportFixture) cliRow() []byte {
	return []byte(fmt.Sprintf(`{"at":%q,"command":"help","duration_ms":0,"exit_code":0,"version":"owned-fixture"}`, f.at.Format(time.RFC3339Nano)))
}

func (f *harnessReportFixture) supervisorRow() []byte {
	return []byte(fmt.Sprintf(`{"data":{"type":"build.step","time":%q,"app":{"root":"/owned","name":"fixture"},"data":{"operation_id":"owned","name":"build.request","ok":true,"reason":"source_rebuild","duration_ms":0}}}`, f.at.Format(time.RFC3339Nano)))
}

// Padding is ordinary JSON whitespace. Every byte, including ignored rows and
// the newline, must be counted by the product reader.
func harnessFixedReportRow(row []byte, size int) ([]byte, error) {
	if len(row) >= size {
		return nil, fmt.Errorf("fixture row %d exceeds %d-byte boundary", len(row), size)
	}
	return append(append(bytes.Clone(row), bytes.Repeat([]byte{' '}, size-len(row)-1)...), '\n'), nil
}

type harnessReportChild struct {
	command        *exec.Cmd
	stdout, stderr bytes.Buffer
	done           chan struct{}
	err            error
}

// Each human/JSON invocation gets its own fixture. The CLI's normal completion
// telemetry is deliberately allowed to append after the report is produced.
func (f *harnessReportFixture) run(ctx context.Context, repo, format string, transcripts bool, mutate func(*harnessReportChild) (map[string]any, error)) (telemetryreport.Report, map[string]any, error) {
	var report telemetryreport.Report
	args := []string{"telemetry", "report", "--since", "24h", "-o", format}
	if transcripts {
		args = append(args, "--agent-transcripts")
	}
	c := &harnessReportChild{command: exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repo), args...), done: make(chan struct{})}
	c.command.Dir, c.command.WaitDelay = f.root, 2*time.Second
	c.command.Env = envWithOverrides(envpolicy.Environ(), "HOME="+f.root, "SCENERY_AGENT_HOME="+f.home)
	c.command.Stdout, c.command.Stderr = &c.stdout, &c.stderr
	if err := c.command.Start(); err != nil {
		return report, nil, err
	}
	go func() { c.err = c.command.Wait(); close(c.done) }()
	proof := map[string]any{"pid": c.command.Process.Pid, "format": format}
	defer func() {
		select {
		case <-c.done:
		default:
			_ = harnessReportResume(c.command.Process)
			_ = c.command.Process.Kill()
			<-c.done
		}
		proof["child_exited"] = true
		proof["stdout"], proof["stderr"], proof["exit_code"] = c.stdout.String(), c.stderr.String(), c.command.ProcessState.ExitCode()
	}()
	if mutate != nil {
		mutation, err := mutate(c)
		proof["capture_before_mutation"] = mutation
		if err != nil {
			return report, proof, err
		}
	}
	select {
	case <-ctx.Done():
		return report, proof, ctx.Err()
	case <-c.done:
	}
	proof["stdout"], proof["stderr"], proof["exit_code"] = c.stdout.String(), c.stderr.String(), c.command.ProcessState.ExitCode()
	if c.err != nil {
		return report, proof, fmt.Errorf("snapshot report CLI failed: %w: %s", c.err, c.stderr.String())
	}
	if format == "json" {
		if err := decodeCLIJSON(c.stdout.Bytes(), &report); err != nil {
			return report, proof, err
		}
		var envelope map[string]any
		if err := json.Unmarshal(c.stdout.Bytes(), &envelope); err != nil {
			return report, proof, err
		}
		for name, data := range map[string]any{"scenery.cli": envelope, "scenery.telemetry.report": envelope["data"]} {
			if issues := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs", "schemas", name+".schema.json"), data); len(issues) != 0 {
				return report, proof, fmt.Errorf("snapshot %s schema: %s", name, strings.Join(issues, "; "))
			}
		}
		proof["producer"], proof["schema_valid"] = envelope["producer"], true
	}
	return report, proof, nil
}

func harnessReportExtent(snapshot *int64, read, want int64) bool {
	return snapshot != nil && *snapshot == want && read == want
}

// Actual process controls cover physical empty/unknown extents, final JSON
// without a newline, duplicate inode inventory, a genuine writer split, and
// opt-in transcript aggregation. No external process is hidden in Go tests.
func proveHarnessReportSnapshots(parent context.Context, repo string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	proofs := map[string]any{}
	for _, name := range []string{"empty", "unknown", "final-no-newline", "duplicate", "writer-split", "transcripts"} {
		f, err := newHarnessReportFixture()
		if err != nil {
			return nil, err
		}
		proof, err := proveHarnessReportControl(ctx, repo, f, name)
		cleanupErr := os.RemoveAll(f.root)
		_, absence := os.Lstat(f.root)
		if err = errors.Join(err, cleanupErr); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !os.IsNotExist(absence) {
			return nil, fmt.Errorf("%s fixture root remains: %v", name, absence)
		}
		proof["cleanup"] = "actual child exited; private fixture removed and absence verified"
		proofs[name] = proof
	}
	for _, name := range []string{"active-cli-append", "archived-cli-append", "supervisor-truncate", "claude-append", "codex-truncate", "writer-rotate"} {
		for _, format := range []string{"json", "human"} {
			proof, err := proveHarnessReportMutation(ctx, repo, name, format)
			if err != nil {
				proofs[name+"/"+format] = proof
				return map[string]any{"cases": proofs, "failed_case": name + "/" + format}, fmt.Errorf("%s/%s: %w", name, format, err)
			}
			proofs[name+"/"+format] = proof
		}
	}
	late, err := proveHarnessReportLateObservation(ctx, repo)
	proofs["late-read-rejected"] = late
	if err != nil {
		return map[string]any{"cases": proofs, "failed_case": "late-read-rejected"}, err
	}
	return map[string]any{"cases": proofs, "proof": "actual_report_cli_descriptor_bound_extents_and_physical_byte_coverage", "limits": "owned synthetic input and schedules; no production incidence, atomic contents, total-memory, or cross-platform execution guarantee"}, nil
}

func proveHarnessReportControl(ctx context.Context, repo string, f *harnessReportFixture, name string) (map[string]any, error) {
	cli, log := f.cliRow(), f.supervisorRow()
	if err := f.write(f.cli, nil); err != nil {
		return nil, err
	}
	wantCLI, wantLog, wantSegments := int64(0), int64(0), 1
	switch name {
	case "empty":
		if err := f.write(f.log, nil); err != nil {
			return nil, err
		}
	case "unknown":
		if err := os.Remove(f.cli); err != nil {
			return nil, err
		}
		if err := os.Mkdir(f.cli, 0o700); err != nil {
			return nil, err
		}
	case "final-no-newline", "duplicate":
		if err := f.write(f.cli, cli); err != nil {
			return nil, err
		}
		if err := f.write(f.log, log); err != nil {
			return nil, err
		}
		wantCLI, wantLog = int64(len(cli)), int64(len(log))
		if name == "duplicate" {
			for _, path := range []string{f.log + ".1", f.log + ".2"} {
				if err := os.Link(f.log, path); err != nil {
					return nil, err
				}
			}
		}
	case "writer-split":
		writer, err := rotatinglog.Open(f.log)
		if err != nil {
			return nil, err
		}
		block := append(bytes.Repeat([]byte{'x'}, 1023), '\n')
		target := rotatinglog.SegmentBytes - 32
		input := bytes.Repeat(block, target/len(block))
		input = append(append(input, bytes.Repeat([]byte{'x'}, target%len(block)-1)...), '\n')
		input = append(append(input, log...), '\n')
		n, writeErr := writer.Write(input)
		if err := errors.Join(writeErr, writer.Close()); err != nil || n != len(input) {
			return nil, fmt.Errorf("genuine split write %d: %w", n, err)
		}
		wantLog, wantSegments = int64(len(input)), 2
	case "transcripts":
		for _, kind := range []string{"claude", "codex"} {
			path, input := f.transcript(kind, "owned")
			if err := f.write(path, input); err != nil {
				return nil, err
			}
		}
	}
	r, proof, err := f.run(ctx, repo, "json", name == "transcripts", nil)
	if err != nil {
		return proof, err
	}
	if len(r.Sources.CLI) != 1 {
		return proof, errors.New("CLI source inventory changed")
	}
	c := r.Sources.CLI[0]
	if name == "unknown" {
		if c.Status != "unreadable" || c.SnapshotBytes != nil || c.ReadBytes != 0 {
			return proof, errors.New("unknown extent became known empty")
		}
	} else if !harnessReportExtent(c.SnapshotBytes, c.ReadBytes, wantCLI) || c.Status != "complete" {
		return proof, fmt.Errorf("CLI extent %+v", c)
	}
	if name == "transcripts" {
		_, a := f.transcript("claude", "owned")
		_, b := f.transcript("codex", "owned")
		if !harnessReportExtent(r.Sources.Transcripts.SnapshotBytes, r.Sources.Transcripts.ReadBytes, int64(len(a)+len(b))) || r.Sources.Transcripts.Read != 2 || r.Agents.SceneryCommands != 2 || r.Agents.SceneryOutcomeUnknown != 0 {
			return proof, fmt.Errorf("transcript source/command evidence %+v / %+v", r.Sources.Transcripts, r.Agents)
		}
	} else if name != "unknown" {
		if len(r.Sources.Supervisor) != 1 {
			return proof, errors.New("supervisor logical source missing")
		}
		s := r.Sources.Supervisor[0]
		wantRows := 1
		if name == "empty" {
			wantRows = 0
		}
		if !harnessReportExtent(s.SnapshotBytes, s.ReadBytes, wantLog) || s.Segments != wantSegments || s.Records != wantRows {
			return proof, fmt.Errorf("supervisor extent %+v", s)
		}
		if name == "duplicate" && (s.Status != "partial" || r.Sources.SupervisorLogsPartial != 1) {
			return proof, errors.New("one duplicate logical source was not counted partial exactly once")
		}
		if name != "duplicate" && s.Status != "complete" {
			return proof, errors.New("intact source was marked partial")
		}
	}
	return proof, nil
}

func (f *harnessReportFixture) transcript(kind, id string) (string, []byte) {
	at := f.at.Format(time.RFC3339Nano)
	var records []any
	path := filepath.Join(f.root, ".claude", "projects", "owned", "session.jsonl")
	if kind == "claude" {
		records = []any{
			map[string]any{"type": "assistant", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "scenery help"}}}}},
			map[string]any{"type": "user", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": false, "content": "ok"}}}},
		}
	} else {
		path = filepath.Join(f.root, ".codex", "sessions", "owned", "session.jsonl")
		records = []any{
			map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": id, "arguments": `{"cmd":"scenery help"}`}},
			map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call_output", "call_id": id, "output": "Process exited with code 0\nOutput:\nok"}},
		}
	}
	var input bytes.Buffer
	for _, record := range records {
		_ = json.NewEncoder(&input).Encode(record)
	}
	return path, input.Bytes()
}
