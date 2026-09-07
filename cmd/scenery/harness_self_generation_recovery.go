package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/workspacetx"
)

func runHarnessGenerationRecovery(ctx context.Context, repoRoot, root string) (map[string]any, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "interrupted publication probe uses POSIX process suspension"}, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	// Use the same physical app root as the candidate's working directory;
	// macOS exposes its temporary directory through both /var and /private/var.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	const packages = 64
	var app strings.Builder
	app.WriteString("application \"recovery\" {}\ngo_module \"application\" {\n  root = \".\"\n  import_path = \"example.com/recovery\"\n}\nworkspace {\n  managed_generated_roots = [\n")
	for i := range packages {
		fmt.Fprintf(&app, "    \"pkg%d/scenerycontract\",\n", i)
	}
	app.WriteString("  ]\n}\n")
	write := func(relative, contents string) error {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(contents), 0o644)
	}
	for i := range packages {
		fmt.Fprintf(&app, "module \"pkg%d\" { source = \"./pkg%d\" }\n", i, i)
		if err := write(fmt.Sprintf("pkg%d/package.scn", i), fmt.Sprintf("package \"pkg%d\" {\n  go_contract { import_path = \"example.com/recovery/pkg%d\" }\n}\nrecord \"value\" {\n  field \"name\" { type = string }\n}\nexport \"value\" { value = record.value }\n", i, i)); err != nil {
			return nil, err
		}
	}
	if err := write("app.scn", app.String()); err != nil {
		return nil, err
	}
	if err := write("go.mod", "module example.com/recovery\n\ngo 1.27.0\n"); err != nil {
		return nil, err
	}
	bin := harnessLocalSceneryBinaryPath(repoRoot)
	command := func(check bool) *exec.Cmd {
		args := []string{"generate", "--target", "contracts", "-o", "json"}
		if check {
			args = append(args, "--check")
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = root
		cmd.Env = envWithoutKeys(envpolicy.Environ(), "GOWORK", "GOFLAGS")
		return cmd
	}
	if output, err := command(false).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("prepare recovery fixture: %w\n%s", err, output)
	}
	paths, err := compiler.GeneratedPaths(root)
	if err != nil {
		return nil, err
	}
	before := map[string][]byte{}
	for relative := range paths {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			return nil, err
		}
		before[relative] = data
	}
	for i := range packages {
		path := filepath.Join(root, fmt.Sprintf("pkg%d/package.scn", i))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data = bytes.Replace(data, []byte("record \"value\" {\n"), []byte("record \"value\" {\n  field \"revision\" { type = int64 }\n"), 1)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
	}
	child := command(false)
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		return nil, err
	}
	owner := localagent.CaptureOwner(child.Process.Pid, "generation interruption probe")
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	finished := false
	defer func() {
		if !finished {
			if localagent.VerifyOwner(owner) == nil {
				_ = child.Process.Kill()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	interrupted := false
	for !interrupted {
		select {
		case err := <-done:
			finished = true
			return nil, fmt.Errorf("publication exited before a partial journal was observed: %v\n%s", err, output.String())
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("timed out observing partial generated publication")
		case <-ticker.C:
			data, err := os.ReadFile(filepath.Join(root, ".scenery/transactions/change-apply.json"))
			if err != nil {
				continue
			}
			var journal workspacetx.Journal
			if json.Unmarshal(data, &journal) != nil {
				continue
			}
			for _, entry := range journal.Entries {
				if _, err := os.Stat(entry.Backup); err != nil {
					continue
				}
				if err := localagent.VerifyOwner(owner); err != nil {
					return nil, err
				}
				if err := exec.CommandContext(ctx, "kill", "-STOP", strconv.Itoa(child.Process.Pid)).Run(); err != nil {
					return nil, err
				}
				interrupted = true
				break
			}
		}
	}
	if err := localagent.VerifyOwner(owner); err != nil {
		return nil, err
	}
	if err := child.Process.Kill(); err != nil {
		return nil, err
	}
	<-done
	finished = true
	compiled, err := compiler.Compile(root)
	if err != nil || !compiled.Valid() {
		return nil, fmt.Errorf("ordinary compiler recovery failed: %v", err)
	}
	for relative, data := range before {
		restored, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || !bytes.Equal(data, restored) {
			return nil, fmt.Errorf("interruption did not restore complete prior artifact set: %s %v", relative, err)
		}
	}
	if output, err := command(false).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("regenerate after recovery: %w\n%s", err, output)
	}
	// Separate candidate CLI processes contend on the same publication boundary.
	first, second := command(false), command(false)
	var firstOutput, secondOutput bytes.Buffer
	first.Stdout, first.Stderr = &firstOutput, &firstOutput
	second.Stdout, second.Stderr = &secondOutput, &secondOutput
	if err := first.Start(); err != nil {
		return nil, err
	}
	if err := second.Start(); err != nil {
		_ = first.Process.Kill()
		_ = first.Wait()
		return nil, err
	}
	firstErr, secondErr := first.Wait(), second.Wait()
	if firstErr != nil || secondErr != nil {
		return nil, fmt.Errorf("concurrent generation failed: %v %v\n%s\n%s", firstErr, secondErr, firstOutput.String(), secondOutput.String())
	}
	if output, err := command(true).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("concurrent set is not current: %w\n%s", err, output)
	}
	return map[string]any{"proof": "partial_publication_killed_prior_set_recovered_and_two_cli_publishers_serialized", "packages": packages, "generated_files": len(before), "interrupted_owner": owner}, nil
}
