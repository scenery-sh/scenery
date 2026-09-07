package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
)

// The release generation probe owns this source-only external checkout. Raw
// Go commands run without workspace redirection and never start capabilities.
func runHarnessOrdinaryGoCheckout(ctx context.Context, repoRoot, root string) (summary map[string]any, err error) {
	bin := harnessLocalSceneryBinaryPath(repoRoot)
	commands := []map[string]any{}
	summary = map[string]any{"app_root": root, "candidate": bin, "framework_source": repoRoot, "dependency_lane": "explicit_local_replacement", "published_dependency_lane": "not_run"}
	defer func() { summary["commands"] = commands }()
	env := envWithoutKeys(envpolicy.Environ(), "GOWORK", "GOFLAGS")
	run := func(overrides []string, program string, args ...string) ([]byte, error) {
		started := time.Now()
		command := exec.CommandContext(ctx, program, args...)
		command.Dir = root
		command.Env = envWithOverrides(env, overrides...)
		output, runErr := command.CombinedOutput()
		exit := 0
		if runErr != nil {
			exit = -1
			if command.ProcessState != nil {
				exit = command.ProcessState.ExitCode()
			}
		}
		commands = append(commands, map[string]any{"cwd": root, "command": append([]string{program}, args...), "environment_overrides": overrides, "exit_code": exit, "duration_ms": time.Since(started).Milliseconds(), "output": string(output)})
		if runErr != nil {
			return output, fmt.Errorf("%s %s: %w\n%s", program, strings.Join(args, " "), runErr, output)
		}
		return output, nil
	}
	cli := func(args ...string) ([]byte, error) { return run(nil, bin, append(args, "-o", "json")...) }
	git := func(args ...string) ([]byte, error) {
		return run(nil, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Scenery release probe", "-c", "user.email=release-probe@example.invalid"}, args...)...)
	}
	write := func(name string, data []byte) error {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}
	for _, name := range []string{".scenery.json", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, "testdata/apps/basic", name))
		if err != nil {
			return summary, err
		}
		if name == "go.mod" {
			data = bytes.ReplaceAll(data, []byte("=> ../../.."), []byte("=> "+filepath.ToSlash(repoRoot)))
		}
		if err := write(name, data); err != nil {
			return summary, err
		}
	}
	if err := write(".gitignore", []byte("/.scenery/\n/service/scenerycontract/\n/internal/scenerygen/\n")); err != nil {
		return summary, err
	}
	if _, err := os.Stat(filepath.Join(root, "service/scenerycontract")); !os.IsNotExist(err) {
		return summary, fmt.Errorf("fresh checkout already contains generated Go")
	}
	if _, err := git("init", "--quiet"); err != nil {
		return summary, err
	}
	if _, err := git("add", "."); err != nil {
		return summary, err
	}
	if _, err := git("commit", "--quiet", "-m", "Authored source-only baseline"); err != nil {
		return summary, err
	}
	baseline, err := git("rev-parse", "HEAD")
	if err != nil {
		return summary, err
	}
	summary["authored_baseline"] = strings.TrimSpace(string(baseline))
	if out, err := run(nil, "go", "env", "GOWORK"); err != nil || len(bytes.TrimSpace(out)) != 0 {
		return summary, fmt.Errorf("ambient workspace is not empty: %q %v", out, err)
	}
	if _, err := cli("generate", "--target", "contracts", "--check"); err == nil {
		return summary, fmt.Errorf("missing projection passed check-only generation")
	}
	if _, err := os.Stat(filepath.Join(root, "service/scenerycontract")); !os.IsNotExist(err) {
		return summary, fmt.Errorf("check-only generation wrote the missing projection")
	}
	before, err := compiler.Compile(root)
	if err != nil {
		return summary, err
	}
	if _, err := cli("generate", "--target", "contracts"); err != nil {
		return summary, err
	}
	after, err := compiler.Compile(root)
	if err != nil {
		return summary, err
	}
	if before.WorkspaceRevision != after.WorkspaceRevision || before.Manifest.ContractRevision != after.Manifest.ContractRevision {
		return summary, fmt.Errorf("publication changed authored or contract revision")
	}
	summary["workspace_revision"] = after.WorkspaceRevision
	summary["contract_revision"] = after.Manifest.ContractRevision
	generated, err := compiler.GeneratedPaths(root)
	if err != nil {
		return summary, err
	}
	stamps := map[string]time.Time{}
	for path := range generated {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			return summary, err
		}
		stamps[path] = info.ModTime()
	}
	if _, err := cli("generate", "--target", "contracts"); err != nil {
		return summary, err
	}
	for path, stamp := range stamps {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || !info.ModTime().Equal(stamp) {
			return summary, fmt.Errorf("second generation changed mtime: %s %v", path, err)
		}
	}
	for _, path := range []string{"go.work", "go.work.sum", "service/scenerycontract/go.mod", "internal/scenerygen"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			return summary, fmt.Errorf("unexpected generated module/workspace artifact %s", path)
		}
	}
	if _, err := git("check-ignore", "service/scenerycontract/types.gen.go"); err != nil {
		return summary, err
	}
	if status, err := git("status", "--short"); err != nil || len(status) != 0 {
		return summary, fmt.Errorf("generation changed authored Git state: %s %v", status, err)
	}
	const contractImport = "example.com/basicapp/service/scenerycontract"
	if _, err := run(nil, "go", "doc", contractImport); err != nil {
		return summary, err
	}
	listing, err := run(nil, "go", "list", "-json", contractImport)
	if err != nil {
		return summary, err
	}
	var resolved struct {
		Dir    string
		Module struct{ Path, Dir string }
	}
	if err := json.Unmarshal(listing, &resolved); err != nil {
		return summary, err
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return summary, err
	}
	if resolved.Dir != filepath.Join(physicalRoot, "service/scenerycontract") || resolved.Module.Path != "example.com/basicapp" || resolved.Module.Dir != physicalRoot {
		return summary, fmt.Errorf("ordinary module resolution differs: %+v", resolved)
	}
	summary["resolved_package"] = resolved
	if _, err := run(nil, "go", "mod", "tidy"); err != nil {
		return summary, err
	}
	dependencyDiff, err := git("diff", "--", "go.mod", "go.sum")
	if err != nil {
		return summary, err
	}
	summary["first_tidy_dependency_diff"] = string(dependencyDiff)
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return summary, err
	}
	goSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		return summary, err
	}
	if bytes.Contains(goMod, []byte("/scenerycontract ")) || bytes.Contains(goMod, []byte("/scenerylib_")) {
		return summary, fmt.Errorf("tidy introduced a generated-module dependency")
	}
	if _, err := run(nil, "go", "test", "./..."); err != nil {
		return summary, err
	}
	for _, args := range [][]string{{"doc", contractImport}, {"mod", "tidy"}, {"test", "./..."}} {
		if _, err := run([]string{"GOWORK=off"}, "go", args...); err != nil {
			return summary, err
		}
	}
	// Dependencies are now explicitly prewarmed by the real tidy/test lane.
	if _, err := run([]string{"GOWORK=off", "GOPROXY=off"}, "go", "mod", "tidy"); err != nil {
		return summary, err
	}
	for name, before := range map[string][]byte{"go.mod": goMod, "go.sum": goSum} {
		after, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(before, after) {
			return summary, fmt.Errorf("second tidy changed %s: %v", name, err)
		}
	}
	if _, err := git("add", "go.mod", "go.sum"); err != nil {
		return summary, err
	}
	if _, err := git("commit", "--quiet", "--allow-empty", "-m", "Record legitimate dependency normalization"); err != nil {
		return summary, err
	}
	for _, args := range [][]string{{"generate", "--target", "contracts", "--check"}, {"check"}, {"build", "--target", "development", "--output", ".scenery/proof/app"}} {
		if _, err := cli(args...); err != nil {
			return summary, err
		}
	}
	if err := os.Remove(filepath.Join(root, "service/scenerycontract/types.gen.go")); err != nil {
		return summary, err
	}
	if _, err := cli("build", "--target", "development", "--output", ".scenery/proof/app"); err != nil {
		return summary, err
	}
	if _, err := cli("generate", "--target", "contracts", "--check"); err != nil {
		return summary, err
	}
	if err := os.Remove(filepath.Join(root, "service/scenerycontract/types.gen.go")); err != nil {
		return summary, err
	}
	if _, err := run(nil, bin, "test", "./..."); err != nil {
		return summary, err
	}
	if err := proveOrdinaryGoBranchDrift(root, cli, git); err != nil {
		return summary, err
	}
	if status, err := git("status", "--short"); err != nil || len(status) != 0 {
		return summary, fmt.Errorf("automatic preparation changed authored Git state: %s %v", status, err)
	}
	summary["proof"] = "source_only_external_checkout_raw_go_and_automatic_preparation_passed"
	return summary, nil
}

func proveOrdinaryGoBranchDrift(root string, cli, git func(...string) ([]byte, error)) error {
	typesPath := filepath.Join(root, "service/scenerycontract/types.gen.go")
	before, err := os.ReadFile(typesPath)
	if err != nil {
		return err
	}
	if _, err := git("branch", "ordinary-a"); err != nil {
		return err
	}
	if _, err := git("switch", "--quiet", "-c", "ordinary-b"); err != nil {
		return err
	}
	sourcePath := filepath.Join(root, "service/package.scn")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	changed := bytes.Replace(source, []byte("record \"echo_input\" {\n"), []byte("record \"echo_input\" {\n  field \"extra\" { type = string }\n"), 1)
	if bytes.Equal(changed, source) {
		return fmt.Errorf("branch drift fixture shape changed")
	}
	if err := os.WriteFile(sourcePath, changed, 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"generate", "--target", "contracts", "--check"}, {"check"}} {
		output, err := cli(args...)
		if err == nil || !bytes.Contains(output, []byte("SCN6204")) {
			return fmt.Errorf("contract drift was not classified by %v: %s %v", args, output, err)
		}
		current, err := os.ReadFile(typesPath)
		if err != nil || !bytes.Equal(before, current) {
			return fmt.Errorf("check-only command repaired source output: %v", err)
		}
	}
	if _, err := cli("generate", "--target", "contracts"); err != nil {
		return err
	}
	if _, err := cli("check"); err != nil {
		return err
	}
	current, err := os.ReadFile(typesPath)
	if err != nil || !bytes.Contains(current, []byte("Extra")) {
		return fmt.Errorf("branch B projection is missing its new field: %v", err)
	}
	if _, err := git("add", "service/package.scn"); err != nil {
		return err
	}
	if _, err := git("commit", "--quiet", "-m", "Branch B contract"); err != nil {
		return err
	}
	if _, err := git("switch", "--quiet", "ordinary-a"); err != nil {
		return err
	}
	if _, err := cli("generate", "--target", "contracts"); err != nil {
		return err
	}
	if _, err := cli("check"); err != nil {
		return err
	}
	current, err = os.ReadFile(typesPath)
	if err != nil || !bytes.Equal(before, current) {
		return fmt.Errorf("A-to-B-to-A left obsolete contract bytes: %v", err)
	}
	return nil
}
