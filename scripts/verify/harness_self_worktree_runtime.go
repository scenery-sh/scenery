package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresname"
)

// This release-only probe exercises ordinary CLI ownership in real Git
// worktrees. Its private home is not a substitute for Docker ownership checks:
// cleanup follows each exact retained record and never enumerates by prefix.
type worktreeRuntimeProbe struct {
	ctx              context.Context
	binary           string
	repo             string
	root             string
	home             string
	env              []string
	roots            []string
	binaries         map[string]string
	victoriaBinaries map[string]string
	commands         []map[string]any
	cases            []map[string]any
	mu               sync.Mutex
}

func runHarnessWorktreeRuntimeProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: "worktree runtime and PostgreSQL acceptance", Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"}}
	step.Summary, step.Error = runHarnessWorktreeRuntimeProbe(ctx, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), step.Error == ""
	return step
}

func runHarnessWorktreeRuntimeProbe(parent context.Context, repoRoot string) (summary map[string]any, failure string) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Minute)
	defer cancel()
	summary = map[string]any{"acceptance_plan": "0167", "candidate": harnessLocalSceneryBinaryPath(repoRoot)}
	if !harnessDockerAvailable(ctx) {
		return summary, "Docker is unavailable; mandatory worktree managed-runtime acceptance did not run"
	}
	root, err := os.MkdirTemp("", "scn-worktrees-")
	if err != nil {
		return summary, err.Error()
	}
	p := &worktreeRuntimeProbe{ctx: ctx, repo: repoRoot, root: root, home: filepath.Join(root, "state"), binary: harnessLocalSceneryBinaryPath(repoRoot), binaries: map[string]string{}}
	p.env = envWithOverrides(envWithoutKeys(envpolicy.Environ(), "DATABASE_URL", "SCENERY_APP_ROOT", "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "SCENERY_DEV_DASHBOARD_ADDR", "GOWORK", "GOFLAGS", detachedDevChildEnv), "SCENERY_AGENT_HOME="+p.home, "SCENERY_DEV_VICTORIA=0", "SCENERY_DEV_VICTORIA_DOWNLOAD=0")
	// In-process observation and verified resource cleanup select the same
	// authority as the child CLIs. No user DSN is inherited by this probe.
	restore := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(p.home), "DATABASE_URL": nil})
	defer restore()
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cleanupCancel()
		var cleanupErr error
		for _, appRoot := range p.roots {
			_, downErr := p.runWithContext(cleanupCtx, p.repo, p.binaryForRoot(appRoot), "down", "--app-root", appRoot, "-o", "json")
			if downErr != nil {
				cleanupErr = errors.Join(cleanupErr, downErr)
				continue
			}
			record, recordErr := p.record(appRoot)
			if recordErr != nil {
				cleanupErr = errors.Join(cleanupErr, recordErr)
				continue
			}
			cleanupErr = errors.Join(cleanupErr, cleanupHarnessWorktreePostgres(cleanupCtx, appRoot, record.AppID))
		}
		if cleanupErr == nil && failure == "" {
			cleanupErr = os.RemoveAll(root)
		}
		if failure != "" {
			// Keep private records and logs for diagnosis even after verified
			// resource cleanup. They must not be copied into public summaries.
			summary["retained_probe_root"] = root
		}
		summary["commands"], summary["cases"] = p.commands, p.cases
		if cleanupErr != nil {
			summary["retained_probe_root"] = root
			failure = strings.TrimSpace(failure + "\nprobe cleanup: " + cleanupErr.Error())
		} else {
			summary["cleanup"] = "verified owned worktree clusters removed"
		}
	}()
	var a, b detachedDevResult
	var recordA, recordB localagent.WorktreeRecord
	rootA, rootB := filepath.Join(root, "a"), filepath.Join(root, "b")
	p.roots = []string{rootA, rootB}
	if err := p.scenario("A1", "fresh authored Git worktree serves managed SQL", func(e map[string]any) error {
		if err := p.prepareGitWorktrees(rootA, rootB); err != nil {
			return err
		}
		for _, name := range []string{".env", "library/scenerycontract", "internal/scenerygen"} {
			if _, err := os.Stat(filepath.Join(rootA, name)); !os.IsNotExist(err) {
				return fmt.Errorf("fresh fixture unexpectedly contains %s", name)
			}
		}
		var err error
		a, err = p.up(rootA)
		if err != nil {
			return err
		}
		e["base_url"], e["owner_pid"] = a.Session.RouteManifest.BaseURL, a.PID
		return p.get(worktreeProbeAPI(a) + "/books")
	}); err != nil {
		return summary, err.Error()
	}
	if err := p.scenario("A3", "two Git worktrees have independent runtime and SQL authority", func(e map[string]any) error {
		var err error
		b, err = p.up(rootB)
		if err != nil {
			return err
		}
		recordA, err = p.record(rootA)
		if err != nil {
			return err
		}
		recordB, err = p.record(rootB)
		if err != nil {
			return err
		}
		pa, pb := recordA.Postgres, recordB.Postgres
		if pa == nil || pb == nil || pa.InstanceID == pb.InstanceID || pa.ContainerID == pb.ContainerID || pa.Volume == pb.Volume || pa.Password == pb.Password || pa.Port == pb.Port || recordA.RouterAddress == recordB.RouterAddress || a.PID == b.PID || a.Session.RouteManifest.BaseURL == b.Session.RouteManifest.BaseURL {
			return fmt.Errorf("worktree ownership or endpoints were shared")
		}
		e["a"], e["b"], e["credentials_distinct"] = p.identity(recordA, a), p.identity(recordB, b), true
		return p.get(worktreeProbeAPI(b) + "/books")
	}); err != nil {
		return summary, err.Error()
	}
	if err := p.scenario("A4", "typed TypeScript client and ten two-borrower races per worktree", func(e map[string]any) error {
		for i, runtime := range []detachedDevResult{a, b} {
			out, err := p.verify(p.roots[i], runtime, "exercise")
			if err != nil {
				return err
			}
			e[fmt.Sprintf("worktree_%d", i+1)] = strings.TrimSpace(string(out))
		}
		return nil
	}); err != nil {
		return summary, err.Error()
	}
	if err := p.outage(rootA, rootB, a, b); err != nil {
		return summary, err.Error()
	}
	if err := p.lifecycle(rootA, &a); err != nil {
		return summary, err.Error()
	}
	if err := p.noSQL(); err != nil {
		return summary, err.Error()
	}
	if err := p.concurrentAcquisition(rootA); err != nil {
		return summary, err.Error()
	}
	if err := p.retainedConflicts(rootA, rootB); err != nil {
		return summary, err.Error()
	}
	if err := p.orphanCleanup(rootA, rootB); err != nil {
		return summary, err.Error()
	}
	if err := p.retainedCompatibility(rootA); err != nil {
		return summary, err.Error()
	}
	if err := p.externalShared(rootA, rootB); err != nil {
		return summary, err.Error()
	}
	if err := p.inertSnapshot(rootA); err != nil {
		return summary, err.Error()
	}
	if err := p.interruptedProvisioning(rootA); err != nil {
		return summary, err.Error()
	}
	if err := p.optionalVictoria(); err != nil {
		return summary, err.Error()
	}
	if err := p.edgeSeparation(rootA, rootB); err != nil {
		return summary, err.Error()
	}
	if err := p.mixedProtocol(rootA, rootB, b); err != nil {
		return summary, err.Error()
	}
	if err := p.legacyCoexistence(); err != nil {
		return summary, err.Error()
	}
	if err := p.resourceCosts(rootA); err != nil {
		return summary, err.Error()
	}
	// Rows are added only once their complete required evidence is available.
	// An unfinished matrix is a failure, never a silently skipped acceptance.
	summary["acceptance_rows"] = 18
	return summary, ""
}

func (p *worktreeRuntimeProbe) scenario(id, name string, fn func(map[string]any) error) error {
	started := time.Now()
	evidence := map[string]any{"id": id, "name": name}
	err := fn(evidence)
	evidence["ok"], evidence["duration_ms"] = err == nil, time.Since(started).Milliseconds()
	if err != nil {
		evidence["error"] = err.Error()
	}
	p.cases = append(p.cases, evidence)
	return err
}

func (p *worktreeRuntimeProbe) run(root, program string, args ...string) ([]byte, error) {
	return p.runWithContext(p.ctx, root, program, args...)
}

func (p *worktreeRuntimeProbe) runWithContext(ctx context.Context, root, program string, args ...string) ([]byte, error) {
	started := time.Now()
	command := exec.CommandContext(ctx, program, args...)
	command.Dir, command.Env = root, p.env
	output, err := command.CombinedOutput()
	entry := map[string]any{"cwd": root, "argv": append([]string{program}, args...), "duration_ms": time.Since(started).Milliseconds(), "ok": err == nil}
	if err != nil {
		entry["output_tail"] = tailString(string(output), 8192)
	}
	p.mu.Lock()
	p.commands = append(p.commands, entry)
	p.mu.Unlock()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", program, strings.Join(args, " "), err, tailString(string(output), 8192))
	}
	return output, nil
}

func (p *worktreeRuntimeProbe) up(root string) (detachedDevResult, error) {
	var result detachedDevResult
	output, err := p.run(root, p.binaryForRoot(root), "up", "--app-root", root, "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return result, err
	}
	err = decodeCLIJSON(output, &result)
	return result, err
}

func (p *worktreeRuntimeProbe) binaryForRoot(root string) string {
	if binary := p.binaries[root]; binary != "" {
		return binary
	}
	return p.binary
}

func (p *worktreeRuntimeProbe) record(root string) (localagent.WorktreeRecord, error) {
	paths, err := localagent.PathsForWorktree(p.home, root)
	if err != nil {
		return localagent.WorktreeRecord{}, err
	}
	return paths.LoadRecord("")
}

func (p *worktreeRuntimeProbe) identity(record localagent.WorktreeRecord, runtime detachedDevResult) map[string]any {
	pg := record.Postgres
	paths, _ := localagent.PathsForWorktree(p.home, record.AppRoot)
	return map[string]any{"app_root": record.AppRoot, "owner_pid": runtime.PID, "control_socket": paths.Socket, "database": postgresname.DatabaseNameFor(record.AppID, record.AppRoot), "router": record.RouterAddress, "base_url": runtime.Session.RouteManifest.BaseURL, "resource_id": pg.InstanceID, "container_id": pg.ContainerID, "volume": pg.Volume, "system_id": pg.SystemID, "database_port": pg.Port}
}

func (p *worktreeRuntimeProbe) get(url string) error {
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d", url, response.StatusCode)
	}
	return nil
}

func (p *worktreeRuntimeProbe) verify(root string, runtime detachedDevResult, phase string) ([]byte, error) {
	return p.run(root, "bun", "-e", "import { verify } from './client/verify.ts'; await verify(process.argv[1], process.argv[2], process.argv[3]);", worktreeProbeAPI(runtime), "acceptance-book", phase)
}

func worktreeProbeAPI(runtime detachedDevResult) string {
	return strings.TrimRight(runtime.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/")
}

func (p *worktreeRuntimeProbe) prepareGitWorktrees(rootA, rootB string) error {
	for _, name := range []string{".scenery.json", ".gitignore", "app.scn", "app.lock.scn", "go.mod", "go.sum", "library/service.go", "library/package.scn", "cmd/schema/main.go", "client/verify.ts"} {
		data, err := os.ReadFile(filepath.Join(p.repo, "testdata/apps/worktree-postgres", name))
		if err != nil {
			return err
		}
		if name == "go.mod" {
			data = bytes.ReplaceAll(data, []byte("=> ../../.."), []byte("=> "+filepath.ToSlash(p.repo)))
		}
		path := filepath.Join(rootA, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	if err := os.CopyFS(filepath.Join(rootA, "client/generated"), os.DirFS(filepath.Join(p.repo, "testdata/apps/worktree-postgres/client/generated"))); err != nil {
		return err
	}
	git := func(args ...string) error {
		_, err := p.run(rootA, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Scenery release probe", "-c", "user.email=release-probe@example.invalid"}, args...)...)
		return err
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "."}, {"commit", "--quiet", "-m", "Authored worktree runtime fixture"}, {"worktree", "add", "--quiet", "-b", "probe-b", rootB}} {
		if err := git(args...); err != nil {
			return err
		}
	}
	return nil
}
