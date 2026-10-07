package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"scenery.sh/internal/envpolicy"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/feature"
)

type featureProbe struct {
	ctx         context.Context
	binary      string
	root        string
	primary     string
	remote      string
	common      string
	environment []string
	features    map[string]feature.Record
	assertions  []map[string]any
	processes   []*featureProbeProcess
}

type featureProbeResult struct {
	Action    string                 `json:"action"`
	Overview  *feature.Overview      `json:"overview"`
	Feature   *feature.Record        `json:"feature"`
	Candidate *feature.Candidate     `json:"candidate"`
	Receipts  []feature.CheckReceipt `json:"receipts"`
}

type featureProbeProcess struct {
	command  *exec.Cmd
	done     chan error
	output   string
	finished bool
}

func runHarnessFeatureProbeStep(parent context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: "local feature integration probe", Command: []string{"go", "run", "./scripts/verify", "--probe", "feature", "--summary", "--write"}}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	summary, err := runHarnessFeatureProbe(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = summary
	step.OK = err == nil
	if err != nil {
		step.Error = err.Error()
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error, SuggestedAction: "Fix the feature integration boundary and rerun `go run ./scripts/verify --probe feature --summary --write`."}}
	}
	return step
}

func runHarnessFeatureProbe(ctx context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	root, err := os.MkdirTemp("/tmp", "scenery-feature-")
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	p := &featureProbe{ctx: ctx, binary: harnessLocalSceneryBinaryPath(repoRoot), root: root, primary: filepath.Join(root, "main"), remote: filepath.Join(root, "origin.git"), features: map[string]feature.Record{}, assertions: []map[string]any{}}
	p.environment = envWithOverrides(envpolicy.Environ(), "SCENERY_AGENT_HOME="+filepath.Join(root, "agent"), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "gitconfig"), "GIT_AUTHOR_NAME=Feature Probe", "GIT_AUTHOR_EMAIL=feature-probe@example.invalid", "GIT_COMMITTER_NAME=Feature Probe", "GIT_COMMITTER_EMAIL=feature-probe@example.invalid")
	defer func() {
		cleanupErr := p.cleanup()
		returnErr = errors.Join(returnErr, cleanupErr)
		if summary == nil {
			summary = map[string]any{}
		}
		summary["assertions"], summary["assertion_count"], summary["cleanup_ok"] = p.assertions, len(p.assertions), cleanupErr == nil
		if returnErr != nil {
			summary["retained_fixture"] = root
		} else {
			returnErr = os.RemoveAll(root)
		}
	}()
	if err := p.setup(); err != nil {
		return nil, err
	}
	if err := p.checkpointScenario(); err != nil {
		return nil, err
	}
	if err := p.conflictScenario(); err != nil {
		return nil, err
	}
	if err := p.failureAndBatchScenario(); err != nil {
		return nil, err
	}
	if err := p.schedulingScenario(); err != nil {
		return nil, err
	}
	if err := p.watchScenario(); err != nil {
		return nil, err
	}
	if err := p.recoveryScenario(); err != nil {
		return nil, err
	}
	archive := filepath.Join(repoRoot, ".scenery", "harness", "feature", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := p.export(archive); err != nil {
		return nil, err
	}
	return map[string]any{"evidence_path": archive, "feature_count": len(p.features), "published_branch": "main"}, nil
}

func (p *featureProbe) run(root, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(p.ctx, command, args...)
	cmd.Dir, cmd.Env = root, p.environment
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %v: %w: %s", command, args, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (p *featureProbe) git(root string, args ...string) (string, error) {
	output, err := p.run(root, "git", args...)
	return strings.TrimSpace(string(output)), err
}

func (p *featureProbe) cli(args ...string) (featureProbeResult, error) {
	args = append(append([]string{"feature"}, args...), "--repo-root", p.primary, "-o", "json")
	output, err := p.run(p.primary, p.binary, args...)
	if err != nil {
		return featureProbeResult{}, err
	}
	var result featureProbeResult
	if err := decodeCLIJSON(output, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (p *featureProbe) refusal(contains string, args ...string) error {
	args = append(append([]string{"feature"}, args...), "--repo-root", p.primary, "-o", "json")
	output, err := p.run(p.primary, p.binary, args...)
	if err == nil || !strings.Contains(string(output), contains) {
		return fmt.Errorf("expected refusal %q for %v: %v %s", contains, args, err, output)
	}
	return nil
}

func (p *featureProbe) write(root, path, value string) error {
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value), 0o644)
}

func (p *featureProbe) commit(root, message string, paths ...string) (string, error) {
	if _, err := p.git(root, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", err
	}
	if _, err := p.git(root, "commit", "--no-gpg-sign", "-m", message); err != nil {
		return "", err
	}
	return p.git(root, "rev-parse", "HEAD")
}

func (p *featureProbe) row(name string) (feature.Row, error) {
	result, err := p.cli("list")
	if err != nil {
		return feature.Row{}, err
	}
	for _, row := range result.Overview.Features {
		if row.Name == name {
			return row, nil
		}
	}
	return feature.Row{}, fmt.Errorf("feature %s missing from overview", name)
}

func (p *featureProbe) prepare(names ...string) (feature.Candidate, error) {
	result, err := p.cli(append([]string{"land"}, names...)...)
	if err != nil {
		return feature.Candidate{}, err
	}
	if result.Candidate == nil {
		return feature.Candidate{}, errors.New("missing prepared candidate")
	}
	return *result.Candidate, nil
}

func (p *featureProbe) land(c feature.Candidate) (feature.Candidate, error) {
	result, err := p.cli("land", "--candidate", c.ID, "--yes", "--expect-revision", c.Revision)
	if err != nil {
		return c, err
	}
	if result.Candidate == nil || result.Candidate.Status != "published" {
		return c, errors.New("landing did not publish a receipt")
	}
	remote, err := p.git(p.primary, "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		return c, err
	}
	main, err := p.git(p.primary, "rev-parse", "main")
	if err != nil || main != result.Candidate.Tip || !strings.HasPrefix(remote, main+"\t") {
		return c, fmt.Errorf("local/remote main differs from receipt: %v", err)
	}
	return *result.Candidate, nil
}

func (p *featureProbe) passed(id string, values map[string]any) {
	values["id"], values["passed"] = id, true
	p.assertions = append(p.assertions, values)
}

func (p *featureProbe) setup() error {
	if _, err := p.run(p.root, "git", "init", "--bare", "--initial-branch=main", p.remote); err != nil {
		return err
	}
	if _, err := p.run(p.root, "git", "init", "--initial-branch=main", p.primary); err != nil {
		return err
	}
	if _, err := p.git(p.primary, "remote", "add", "origin", p.remote); err != nil {
		return err
	}
	policy := feature.Policy{Version: 1, ProbeLimit: 1, Development: []feature.Check{{ID: "source", Command: "sh", Args: []string{"check.sh"}, Reuse: true}, {ID: "boundary", Command: "sh", Args: []string{"probe.sh"}, Expensive: true}}, Landing: []feature.Check{{ID: "source", Command: "sh", Args: []string{"check.sh"}, Reuse: true}, {ID: "boundary", Command: "sh", Args: []string{"probe.sh"}, Expensive: true}}}
	data, _ := json.MarshalIndent(policy, "", "  ")
	files := map[string]string{feature.PolicyFile: string(data) + "\n", ".gitignore": ".scenery/\n", "shared.txt": "base\n", ".scenery.json": `{"name":"feature-fixture","id":"feature-fixture"}` + "\n", "check.sh": `#!/bin/sh
set -eu
mkdir -p .scenery
echo executed >> .scenery/source-count
test ! -f fail.txt
`, "probe.sh": `#!/bin/sh
set -eu
common=$(git rev-parse --path-format=absolute --git-common-dir)
mkdir -p "$common/probe-active" .scenery
active="$common/probe-active/$$"
echo "$$" > "$active"
trap 'rm -f "$active"' EXIT INT TERM
while test -f "$common/probe-hold"; do sleep 0.05; done
echo executed >> .scenery/probe-count
`}
	for path, value := range files {
		if err := p.write(p.primary, path, value); err != nil {
			return err
		}
	}
	paths := []string{}
	for path := range files {
		paths = append(paths, path)
	}
	if _, err := p.commit(p.primary, "Initial feature fixture", paths...); err != nil {
		return err
	}
	if _, err := p.git(p.primary, "push", "origin", "main:refs/heads/main"); err != nil {
		return err
	}
	common, err := p.git(p.primary, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	p.common = common
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		args := []string{"create", name, "--purpose", "Feature " + name}
		if name == "delta" {
			args = append(args, "--depends-on", "alpha")
		}
		result, err := p.cli(args...)
		if err != nil {
			return err
		}
		p.features[name] = *result.Feature
	}
	alpha, beta, gamma, epsilon := p.features["alpha"].Path, p.features["beta"].Path, p.features["gamma"].Path, p.features["epsilon"].Path
	for _, item := range []struct{ root, path, value string }{{alpha, "shared.txt", "alpha\n"}, {beta, "shared.txt", "beta\n"}, {gamma, "gamma.txt", "independent\n"}, {epsilon, "epsilon.txt", "parked\n"}} {
		if err := p.write(item.root, item.path, item.value); err != nil {
			return err
		}
		if _, err := p.commit(item.root, "Feature checkpoint", item.path); err != nil {
			return err
		}
	}
	if _, err := p.cli("set", "alpha", "--stage", "ready"); err != nil {
		return err
	}
	if _, err := p.cli("set", "gamma", "--stage", "ready"); err != nil {
		return err
	}
	if _, err := p.cli("set", "epsilon", "--stage", "parked"); err != nil {
		return err
	}
	alphaRow, err := p.row("alpha")
	if err != nil {
		return err
	}
	deltaRow, err := p.row("delta")
	if err != nil {
		return err
	}
	if alphaRow.Status != "ready" || alphaRow.Purpose == "" || alphaRow.Runtime == "unknown" || len(alphaRow.Overlap) != 1 || alphaRow.Overlap[0].Feature != "beta" || deltaRow.Status != "blocked" {
		return errors.New("five-feature overview omitted readiness, runtime, purpose, overlap or dependency")
	}
	if err := p.refusal("parked", "land", "epsilon"); err != nil {
		return err
	}
	p.passed("F1", map[string]any{"features": 5, "purpose_dependencies_overlap_runtime": true, "parked_refused": true})
	return nil
}

func (p *featureProbe) cleanup() error {
	if p.remote != "" {
		_ = os.Remove(filepath.Join(p.remote, "push-hold"))
	}
	if p.common != "" {
		_ = os.Remove(filepath.Join(p.common, "probe-hold"))
	}
	var failures []error
	for _, process := range p.processes {
		if process.finished {
			continue
		}
		if process.command.Process != nil {
			_ = process.command.Process.Signal(os.Interrupt)
		}
		select {
		case <-process.done:
			process.finished = true
		case <-time.After(3 * time.Second):
			failures = append(failures, fmt.Errorf("owned feature command %d did not stop", process.command.Process.Pid))
		}
	}
	return errors.Join(failures...)
}

func (p *featureProbe) export(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	state := filepath.Join(p.common, "scenery-features")
	for _, name := range []string{"features", "candidates", "landings", "checks", "development"} {
		root := filepath.Join(state, name)
		if err := filepath.WalkDir(root, func(source string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(state, source)
			if err != nil {
				return err
			}
			target := filepath.Join(path, rel)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o700)
			}
			data, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o600)
		}); err != nil {
			return err
		}
	}
	data, _ := json.MarshalIndent(p.assertions, "", "  ")
	return os.WriteFile(filepath.Join(path, "assertions.json"), append(data, '\n'), 0o600)
}

func (p *featureProbe) start(args ...string) (*featureProbeProcess, error) {
	output := filepath.Join(p.root, fmt.Sprintf("process-%d.jsonl", len(p.processes)))
	file, err := os.Create(output)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(p.ctx, p.binary, append(append([]string{"feature"}, args...), "--repo-root", p.primary)...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = p.primary, p.environment, file, file
	if err := cmd.Start(); err != nil {
		_ = file.Close()
		return nil, err
	}
	process := &featureProbeProcess{command: cmd, done: make(chan error, 1), output: output}
	go func() { err := cmd.Wait(); _ = file.Close(); process.done <- err }()
	p.processes = append(p.processes, process)
	return process, nil
}

func (p *featureProbe) await(process *featureProbeProcess) error {
	select {
	case err := <-process.done:
		process.finished = true
		if err != nil {
			data, _ := os.ReadFile(process.output)
			return fmt.Errorf("feature process: %w: %s", err, data)
		}
		return nil
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

func (p *featureProbe) wait(check func() bool) error {
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			return errors.New("feature probe observation deadline exceeded")
		}
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil
}

func (p *featureProbe) active() int {
	files, _ := filepath.Glob(filepath.Join(p.common, "probe-active", "*"))
	return len(files)
}

func containsPaths(paths []string, expected ...string) bool {
	for _, path := range expected {
		if !slices.Contains(paths, path) {
			return false
		}
	}
	return true
}
