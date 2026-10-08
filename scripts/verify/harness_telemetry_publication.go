package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"scenery.sh/internal/envpolicy"
)

type harnessBundleProcess struct {
	command *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	done    chan struct{}
	err     error
}

func startHarnessBundleProcess(ctx context.Context, repo, root, input, output string) (*harnessBundleProcess, error) {
	p := &harnessBundleProcess{done: make(chan struct{})}
	p.command = exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repo), "telemetry", "export", "--app-root", root, "--include", input, "--output", output, "-o", "json")
	p.command.Dir = root
	p.command.Env = envWithOverrides(envpolicy.Environ(), "SCENERY_AGENT_HOME="+filepath.Join(root, "private-agent"))
	p.command.Stdout, p.command.Stderr = &p.stdout, &p.stderr
	p.command.WaitDelay = 2 * time.Second
	if err := p.command.Start(); err != nil {
		return nil, err
	}
	go func() {
		p.err = p.command.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *harnessBundleProcess) result(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-p.done:
	}
	if p.err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(p.err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, p.err
}

func verifyHarnessBundleCollision(p *harnessBundleProcess, exit int) error {
	var envelope struct {
		OK          bool `json:"ok"`
		Diagnostics []struct {
			Code        string `json:"code"`
			ReportToken string `json:"report_token"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(p.stdout.Bytes(), &envelope); err != nil {
		return fmt.Errorf("publication refusal omitted JSON diagnostic: %w", err)
	}
	if exit != 3 || envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" || envelope.Diagnostics[0].ReportToken != "" {
		return fmt.Errorf("publication collision is not SCN8003/exit3: exit=%d output=%s", exit, p.stdout.String())
	}
	return nil
}

func verifyHarnessCompleteBundle(p *harnessBundleProcess, output string, payload []byte, completeEvidence bool) error {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Kind string `json:"kind"`
			OK   bool   `json:"ok"`
		} `json:"data"`
	}
	if err := json.Unmarshal(p.stdout.Bytes(), &envelope); err != nil || envelope.OK != completeEvidence || envelope.Data.OK != completeEvidence || envelope.Data.Kind != "scenery.telemetry.export" {
		return fmt.Errorf("completed export omitted its success identity: %v output=%s", err, p.stdout.String())
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		return err
	}
	defer func() { _ = archive.Close() }()
	expectedFiles := 1
	if completeEvidence {
		expectedFiles = 2
	}
	if len(archive.File) != expectedFiles {
		return fmt.Errorf("publication winner has %d files, want %d", len(archive.File), expectedFiles)
	}
	seen := map[string]bool{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 64*1024+1))
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if len(data) > 64*1024 {
			return errors.New("publication fixture archive entry exceeded its expected bound")
		}
		if seen[file.Name] {
			return fmt.Errorf("publication winner duplicates %s", file.Name)
		}
		seen[file.Name] = true
		switch file.Name {
		case "selected/000-selected-evidence.txt":
			if !bytes.Equal(data, payload) {
				return errors.New("publication winner lost or mixed selected payload")
			}
		case "manifest.json":
			var manifest struct {
				Kind   string `json:"kind"`
				OK     bool   `json:"ok"`
				Output string `json:"output"`
				Files  []struct {
					ArchivePath   string `json:"archive_path"`
					SHA256        string `json:"sha256"`
					Bytes         int64  `json:"bytes"`
					MissingReason string `json:"missing_reason"`
				} `json:"files"`
			}
			if err := json.Unmarshal(data, &manifest); err != nil || manifest.OK != completeEvidence || manifest.Kind != "scenery.telemetry.export" || manifest.Output != output {
				return fmt.Errorf("publication winner has incomplete manifest: %v", err)
			}
			if len(manifest.Files) != 1 || manifest.Files[0].ArchivePath != "selected/000-selected-evidence.txt" {
				return errors.New("publication manifest lost its selected evidence record")
			}
			if completeEvidence && (manifest.Files[0].Bytes != int64(len(payload)) || manifest.Files[0].SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(payload))) {
				return errors.New("publication winner manifest does not match its selected payload")
			}
			if !completeEvidence && manifest.Files[0].MissingReason == "" {
				return errors.New("incomplete export omitted its missing evidence reason")
			}
		default:
			return fmt.Errorf("publication winner has unexpected entry %s", file.Name)
		}
	}
	return nil
}

func waitHarnessBundleStaging(ctx context.Context, directory string, count int, processes []*harnessBundleProcess) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		staging, err := filepath.Glob(filepath.Join(directory, ".telemetry-export-*.zip"))
		if err != nil {
			return err
		}
		if len(staging) == count {
			return nil
		}
		if len(staging) > count {
			return errors.New("unexpected telemetry publication staging inventory")
		}
		for _, p := range processes {
			select {
			case <-p.done:
				return fmt.Errorf("telemetry export exited before all writers staged: %v", p.err)
			default:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// FIFOs coordinate genuine CLI writers after their initial checks. This proves
// no-replace capability and complete publication, not ordinary race frequency.
func proveHarnessTelemetryPublication(parent context.Context, repo string) (proof map[string]any, err error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	root, err := os.MkdirTemp("", "scenery-telemetry-publication-*")
	if err != nil {
		cancel()
		return nil, err
	}
	proof = map[string]any{"coordination": "owned selected FIFOs; no production incidence or regular-file race-frequency claim"}
	var processes []*harnessBundleProcess
	var fifoWriters []*os.File
	defer func() {
		cancel()
		for _, writer := range fifoWriters {
			_ = writer.Close()
		}
		for _, p := range processes {
			<-p.done
		}
		cleanupErr := os.RemoveAll(root)
		if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
			cleanupErr = errors.Join(cleanupErr, errors.New("owned publication fixture cleanup failed"))
		}
		err = errors.Join(err, cleanupErr)
		if cleanupErr == nil {
			proof["cleanup"] = "owned children exited; staging absent; disposable root removed"
		} else {
			proof["cleanup"] = "failed"
		}
	}()
	start := func(app, input, output string) (*harnessBundleProcess, error) {
		p, err := startHarnessBundleProcess(ctx, repo, app, input, output)
		if err == nil {
			processes = append(processes, p)
		}
		return p, err
	}
	noStaging := func(directory string) error {
		files, err := filepath.Glob(filepath.Join(directory, ".telemetry-export-*.zip"))
		if err != nil || len(files) != 0 {
			return fmt.Errorf("publication left staging files: %v %v", files, err)
		}
		return nil
	}
	for _, scenario := range []string{"fresh", "incomplete-evidence", "existing-file", "existing-directory", "live-symlink", "dangling-symlink", "competing-file", "two-writers"} {
		directory := filepath.Join(root, scenario)
		if err := os.Mkdir(directory, 0700); err != nil {
			return proof, err
		}
		output := filepath.Join(directory, "result.zip")
		writers := 1
		if scenario == "two-writers" {
			writers = 2
		}
		var jobs []*harnessBundleProcess
		var inputs []string
		var payloads [][]byte
		var original os.FileInfo
		sentinel := []byte("owned competing destination\n")
		switch scenario {
		case "existing-file":
			if err := os.WriteFile(output, sentinel, 0600); err != nil {
				return proof, err
			}
		case "existing-directory":
			if err := os.Mkdir(output, 0700); err != nil {
				return proof, err
			}
		case "live-symlink":
			if err := os.WriteFile(filepath.Join(directory, "existing-target"), sentinel, 0600); err != nil {
				return proof, err
			}
			if err := os.Symlink("existing-target", output); err != nil {
				return proof, err
			}
		case "dangling-symlink":
			if err := os.Symlink("missing-target", output); err != nil {
				return proof, err
			}
		}
		if scenario == "existing-file" || scenario == "existing-directory" || scenario == "live-symlink" || scenario == "dangling-symlink" {
			original, err = os.Lstat(output)
			if err != nil {
				return proof, err
			}
		}
		for index := range writers {
			app := filepath.Join(directory, fmt.Sprintf("app-%d", index))
			if err := os.Mkdir(app, 0700); err != nil {
				return proof, err
			}
			input := filepath.Join(app, "selected-evidence.txt")
			payload := []byte(fmt.Sprintf("complete selected evidence from writer %d\n", index))
			if scenario == "competing-file" || scenario == "two-writers" {
				err = makeHarnessTelemetryFIFO(input)
			} else if scenario != "incomplete-evidence" {
				err = os.WriteFile(input, payload, 0600)
			}
			if err != nil {
				return proof, err
			}
			p, err := start(app, input, output)
			if err != nil {
				return proof, err
			}
			jobs, inputs, payloads = append(jobs, p), append(inputs, input), append(payloads, payload)
		}
		if scenario == "competing-file" || scenario == "two-writers" {
			if err := waitHarnessBundleStaging(ctx, directory, writers, jobs); err != nil {
				return proof, err
			}
			var rendezvous []*os.File
			for index, p := range jobs {
				writer, err := openHarnessTelemetryFIFO(ctx, inputs[index], p.done)
				if err != nil {
					return proof, err
				}
				fifoWriters = append(fifoWriters, writer)
				rendezvous = append(rendezvous, writer)
			}
			if scenario == "competing-file" {
				file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return proof, err
				}
				_, writeErr := file.Write(sentinel)
				if err := errors.Join(writeErr, file.Close()); err != nil {
					return proof, err
				}
				original, err = os.Lstat(output)
				if err != nil {
					return proof, err
				}
			}
			for index, writer := range rendezvous {
				n, writeErr := writer.Write(payloads[index])
				if writeErr == nil && n != len(payloads[index]) {
					writeErr = io.ErrShortWrite
				}
				if err := errors.Join(writeErr, writer.Close()); err != nil {
					return proof, err
				}
			}
		}
		winner := -1
		exits := make([]int, len(jobs))
		for index, p := range jobs {
			exit, err := p.result(ctx)
			if err != nil {
				return proof, err
			}
			exits[index] = exit
			if exit == 0 || (scenario == "incomplete-evidence" && exit == 1) {
				if winner != -1 || (scenario != "fresh" && scenario != "two-writers" && scenario != "incomplete-evidence") {
					return proof, errors.New("telemetry publication replaced a competing destination")
				}
				winner = index
				if err := verifyHarnessCompleteBundle(p, output, payloads[index], scenario != "incomplete-evidence"); err != nil {
					return proof, err
				}
			} else if err := verifyHarnessBundleCollision(p, exit); err != nil {
				return proof, err
			}
		}
		if (scenario == "fresh" || scenario == "two-writers" || scenario == "incomplete-evidence") && winner == -1 {
			return proof, errors.New("telemetry publication has no complete winner")
		}
		if original != nil {
			after, err := os.Lstat(output)
			if err != nil || !os.SameFile(original, after) {
				return proof, errors.New("telemetry publication changed competing destination identity")
			}
			if scenario == "dangling-symlink" {
				if target, err := os.Readlink(output); err != nil || target != "missing-target" {
					return proof, errors.New("telemetry publication changed dangling symlink")
				}
			} else if scenario == "existing-directory" {
				if entries, err := os.ReadDir(output); err != nil || len(entries) != 0 {
					return proof, errors.New("telemetry publication changed existing directory contents")
				}
			} else if data, err := os.ReadFile(output); err != nil || !bytes.Equal(data, sentinel) {
				return proof, errors.New("telemetry publication changed competing destination bytes")
			}
		}
		if err := noStaging(directory); err != nil {
			return proof, err
		}
		pids := make([]int, len(jobs))
		for index, job := range jobs {
			pids[index] = job.command.Process.Pid
		}
		result := map[string]any{"exit_codes": exits, "writer_pids": pids, "winner": winner, "staging_remaining": 0}
		if scenario == "competing-file" || scenario == "two-writers" {
			result["staged_before_release"] = writers
			result["input_rendezvous_before_release"] = writers
		}
		if winner >= 0 {
			result["evidence_complete"] = scenario != "incomplete-evidence"
			if scenario != "incomplete-evidence" {
				result["selected_payload_sha256"] = fmt.Sprintf("%x", sha256.Sum256(payloads[winner]))
			}
			result["complete_zip_and_manifest_verified"] = true
		}
		proof[scenario] = result
	}
	return proof, nil
}
