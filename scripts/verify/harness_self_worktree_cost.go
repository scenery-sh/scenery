package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

type worktreeCostCohort struct {
	roots    []string
	runtimes []detachedDevResult
	records  []localagent.WorktreeRecord
	pids     []int
}

func (p *worktreeRuntimeProbe) resourceCosts(source string) error {
	return p.scenario("A18", "repeated 1/5/10-worktree default runtime startup and steady resource measurements", func(e map[string]any) error {
		sourceBefore, err := p.sourceDigest()
		if err != nil {
			return err
		}
		e["authored_source_sha256"] = sourceBefore
		for _, root := range p.roots {
			if _, err := p.run(p.repo, p.binaryForRoot(root), "down", "--app-root", root, "-o", "json"); err != nil {
				return err
			}
		}
		originalEnv := p.env
		defer func() { p.env = originalEnv }()
		p.env = envWithOverrides(p.env, "SCENERY_DEV_VICTORIA=1", "SCENERY_DEV_VICTORIA_DOWNLOAD=0")
		if len(p.victoriaBinaries) != 3 {
			return fmt.Errorf("default-profile measurements require all three actual Victoria binaries")
		}
		p.env = envWithOverrides(p.env,
			"SCENERY_VICTORIA_LOGS_BIN="+p.victoriaBinaries["logs"],
			"SCENERY_VICTORIA_METRICS_BIN="+p.victoriaBinaries["metrics"],
			"SCENERY_VICTORIA_TRACES_BIN="+p.victoriaBinaries["traces"],
		)
		profileEnv := p.env
		hardware := map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "logical_cpus": runtime.NumCPU()}
		if runtime.GOOS == "darwin" {
			out, err := p.run(p.repo, "sysctl", "-n", "hw.model", "hw.ncpu", "hw.memsize")
			if err != nil {
				return err
			}
			hardware["sysctl_model_cpu_memory"] = strings.Fields(string(out))
		} else {
			out, err := p.run(p.repo, "uname", "-srm")
			if err != nil {
				return err
			}
			hardware["uname"] = strings.TrimSpace(string(out))
		}
		out, err := p.run(p.repo, "docker", "info", "--format", `{"id":{{json .ID}},"cpus":{{json .NCPU}},"memory_bytes":{{json .MemTotal}},"server_version":{{json .ServerVersion}},"os":{{json .OperatingSystem}}}`)
		if err != nil {
			return err
		}
		hardware["docker_info"] = strings.TrimSpace(string(out))
		e["hardware"] = hardware
		e["profile"] = "managed PostgreSQL plus three real optional Victoria components per worktree; API-only lending app"
		e["cold_definition"] = "fresh authored Git checkout, fresh cohort-private GOCACHE, no generated application files or cluster; module downloads, toolchain binaries and host filesystem cache may be warm"
		e["warm_definition"] = "same roots after ordinary down/up; retained SQL and observability data, generated artifacts and same cohort Go cache"
		e["limitations"] = []string{"not an OS-cache-cold benchmark", "developer background workloads are not stopped", "aggregate RSS counts shared pages per process, not PSS", "Docker stats memory is container accounting, not host RSS", "no frontend dev server in this fixture", "no inferred capacity ceiling"}
		var runs []map[string]any
		for _, count := range []int{1, 5, 10} {
			for repetition := 1; repetition <= 3; repetition++ {
				cache := filepath.Join(p.root, fmt.Sprintf("cost-cache-%d-%d", count, repetition))
				p.env = envWithOverrides(profileEnv, "GOCACHE="+cache)
				cohort := worktreeCostCohort{}
				for i := 0; i < count; i++ {
					name := fmt.Sprintf("cost-%d-%d-%d", count, repetition, i+1)
					root := filepath.Join(p.root, name)
					if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-"+name, root); err != nil {
						return err
					}
					p.roots = append(p.roots, root)
					cohort.roots = append(cohort.roots, root)
				}
				cold, err := p.startCostCohort(&cohort)
				if err != nil {
					return err
				}
				coldRecords := append([]localagent.WorktreeRecord(nil), cohort.records...)
				for _, root := range cohort.roots {
					if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
						return err
					}
				}
				warm, err := p.startCostCohort(&cohort)
				if err != nil {
					return err
				}
				for i, record := range cohort.records {
					if !sameWorktreeCluster(coldRecords[i], record) {
						return fmt.Errorf("warm startup replaced a measured worktree cluster")
					}
				}
				idle, err := p.measureCostPhase(cohort, false)
				if err != nil {
					return err
				}
				load, err := p.measureCostPhase(cohort, true)
				if err != nil {
					return err
				}
				disk, err := p.costDisk(cohort, cache)
				if err != nil {
					return err
				}
				runs = append(runs, map[string]any{"worktrees": count, "repetition": repetition, "cold": cold, "warm": warm, "idle": idle, "load": load, "disk": disk})
				e["runs"] = runs
				for i, root := range cohort.roots {
					if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
						return err
					}
					if err := cleanupHarnessWorktreePostgres(p.ctx, root, cohort.records[i].AppID); err != nil {
						return err
					}
					if _, err := p.run(source, "git", "worktree", "remove", "--force", root); err != nil {
						return err
					}
				}
				p.roots = p.roots[:len(p.roots)-len(cohort.roots)]
				if err := os.RemoveAll(cache); err != nil {
					return err
				}
			}
		}
		sourceAfter, err := p.sourceDigest()
		if err != nil {
			return err
		}
		if sourceBefore != sourceAfter {
			return fmt.Errorf("authored source changed during resource measurement; rerun against frozen inputs")
		}
		e["authored_source_unchanged"] = true
		return nil
	})
}

func (p *worktreeRuntimeProbe) startCostCohort(cohort *worktreeCostCohort) (map[string]any, error) {
	type started struct {
		index   int
		runtime detachedDevResult
		elapsed int64
		err     error
	}
	begin := time.Now()
	done := make(chan started, len(cohort.roots))
	for i, root := range cohort.roots {
		go func() {
			start := time.Now()
			runtime, err := p.up(root)
			done <- started{i, runtime, time.Since(start).Milliseconds(), err}
		}()
	}
	cohort.runtimes = make([]detachedDevResult, len(cohort.roots))
	elapsed := make([]int64, len(cohort.roots))
	var startErr error
	for range cohort.roots {
		item := <-done
		cohort.runtimes[item.index], elapsed[item.index] = item.runtime, item.elapsed
		if item.err != nil {
			startErr = item.err
		}
	}
	if startErr != nil {
		return nil, startErr
	}
	evidence := map[string]any{"required_serving_ms": elapsed, "cohort_required_serving_wall_ms": time.Since(begin).Milliseconds()}
	cohort.records, cohort.pids = nil, nil
	for i, root := range cohort.roots {
		runtime := cohort.runtimes[i]
		if err := p.get(worktreeProbeAPI(runtime) + "/books"); err != nil {
			return nil, err
		}
		record, err := p.record(root)
		if err != nil {
			return nil, err
		}
		cohort.records = append(cohort.records, record)
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return nil, err
		}
		client := localagent.NewClient(paths.Socket)
		stack, err := p.waitVictoriaReady(client, 0)
		client.CloseIdleConnections()
		if err != nil {
			return nil, err
		}
		session, err := p.liveSession(root)
		if err != nil {
			return nil, err
		}
		appPID, err := strconv.Atoi(session.AppPID)
		if err != nil || appPID <= 0 {
			return nil, fmt.Errorf("cost cohort lacks a current app process")
		}
		cohort.pids = append(cohort.pids, runtime.PID, appPID)
		for _, pid := range stack.PIDs {
			cohort.pids = append(cohort.pids, pid)
		}
	}
	evidence["all_optional_ready_wall_ms"], evidence["native_processes"], evidence["postgres_containers"] = time.Since(begin).Milliseconds(), len(cohort.pids), len(cohort.records)
	return evidence, nil
}
