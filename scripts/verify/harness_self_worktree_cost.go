package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

type worktreeCostCohort struct {
	roots    []string
	runtimes []detachedDevResult
	records  []localagent.WorktreeRecord
	pids     []int
	owners   map[int]localagent.Owner
	roles    map[string]int
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
		for _, tool := range []string{"go", "bun", "node"} {
			flag := "--version"
			if tool == "go" {
				flag = "version"
			}
			output, err := p.run(p.repo, tool, flag)
			if err != nil {
				return err
			}
			hardware[tool+"_version"] = strings.TrimSpace(string(output))
		}
		e["profile"] = "managed PostgreSQL, three real Victoria components, a minimal managed Bun frontend and a real Eve helper with a deterministic model per worktree"
		e["cold_definition"] = "fresh authored Git checkout, fresh cohort-private GOCACHE, no generated Go runtime files or cluster; authored TypeScript client, module downloads, toolchain binaries and host filesystem cache may be warm"
		e["warm_definition"] = "same roots after ordinary down/up; retained SQL and observability data, generated artifacts and same cohort Go cache"
		e["limitations"] = []string{"not an OS-cache-cold benchmark", "developer background workloads are not stopped", "aggregate RSS counts shared pages per process, not PSS", "Docker stats memory is container accounting, not host RSS", "minimal frontend excludes bundler/HMR and browser cost; deterministic assistant excludes remote model/network cost", "startup peak is a sampled native RSS maximum; Docker startup peaks are not measured", "no inferred capacity ceiling or percentile from three cohort repetitions"}
		var runs []map[string]any
		for _, count := range []int{1, 5, 10} {
			for repetition := 1; repetition <= 3; repetition++ {
				cache := filepath.Join(p.root, fmt.Sprintf("cost-cache-%d-%d", count, repetition))
				sceneryCache := filepath.Join(p.root, fmt.Sprintf("cost-scenery-cache-%d-%d", count, repetition))
				p.env = envWithOverrides(profileEnv, "GOCACHE="+cache, "SCENERY_DEV_CACHE_DIR="+sceneryCache)
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
				row := map[string]any{"worktrees": count, "repetition": repetition, "complete": false}
				runs = append(runs, row)
				e["runs"] = runs
				cold, err := p.startCostCohort(&cohort, true)
				row["cold"] = cold
				if err != nil {
					return err
				}
				coldRecords := append([]localagent.WorktreeRecord(nil), cohort.records...)
				for _, root := range cohort.roots {
					if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
						return err
					}
				}
				warm, err := p.startCostCohort(&cohort, false)
				row["warm"] = warm
				if err != nil {
					return err
				}
				for i, record := range cohort.records {
					if !sameWorktreeCluster(coldRecords[i], record) {
						return fmt.Errorf("warm startup replaced a measured worktree cluster")
					}
				}
				idle, err := p.measureCostPhase(cohort, false)
				row["idle"] = idle
				if err != nil {
					return err
				}
				load, err := p.measureCostPhase(cohort, true)
				row["load"] = load
				if err != nil {
					return err
				}
				churn, err := p.churnCostCohort(&cohort)
				row["churn"] = churn
				if err != nil {
					return err
				}
				disk, err := p.costDisk(cohort, cache, sceneryCache)
				row["disk"], row["process_roles"] = disk, cohort.roles
				if err != nil {
					return err
				}
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
				row["complete"], row["cleanup"] = true, "verified owned cohort clusters and worktrees removed"
				if err := os.RemoveAll(cache); err != nil {
					return err
				}
				if err := os.RemoveAll(sceneryCache); err != nil {
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

func (p *worktreeRuntimeProbe) startCostCohort(cohort *worktreeCostCohort, requireSharedComposition bool) (map[string]any, error) {
	type started struct {
		index   int
		runtime detachedDevResult
		elapsed int64
		err     error
	}
	begin := time.Now()
	peak := p.startCostPeakSampler(cohort.roots)
	defer peak()
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
	shared, err := p.sharedCompositionEvidence(cohort.runtimes, requireSharedComposition)
	if err != nil {
		return nil, err
	}
	evidence["shared_composition"] = shared
	cohort.records = nil
	var assistantProofs []map[string]any
	for i, root := range cohort.roots {
		runtime := cohort.runtimes[i]
		if err := p.get(worktreeProbeAPI(runtime) + "/books"); err != nil {
			return nil, err
		}
		if err := p.get(strings.TrimRight(runtime.Session.RouteManifest.BaseURL, "/") + "/client.js"); err != nil {
			return nil, err
		}
		proof, err := p.verifyCostAssistant(runtime)
		if err != nil {
			return nil, err
		}
		proof["app_root"] = root
		assistantProofs = append(assistantProofs, proof)
		record, err := p.record(root)
		if err != nil {
			return nil, err
		}
		cohort.records = append(cohort.records, record)
	}
	ownersStarted := time.Now()
	if err := p.captureCostOwners(cohort); err != nil {
		return nil, err
	}
	peakResult := peak()
	if peakResult["error"] != nil {
		return nil, fmt.Errorf("native startup peak: %v", peakResult["error"])
	}
	evidence["native_startup_peak"] = peakResult
	evidence["assistant_completion"] = assistantProofs
	evidence["ownership_sampling_setup_ms"] = time.Since(ownersStarted).Milliseconds()
	evidence["all_optional_ready_boundary"] = "required serving through frontend module response, deterministic assistant completion, Victoria readiness and verified settled process ownership; includes inspection work"
	evidence["process_roles"] = cohort.roles
	evidence["all_optional_ready_wall_ms"], evidence["native_processes"], evidence["postgres_containers"] = time.Since(begin).Milliseconds(), len(cohort.pids), len(cohort.records)
	return evidence, nil
}

func (p *worktreeRuntimeProbe) sharedCompositionEvidence(runtimes []detachedDevResult, required bool) (map[string]any, error) {
	misses, hits := 0, 0
	perWorktree := make([]map[string]any, 0, len(runtimes))
	for _, runtime := range runtimes {
		events, err := harnessWatchEvents(runtime.LogPath, 0)
		if err != nil {
			return nil, err
		}
		entry := map[string]any{"app_root": runtime.Session.AppRoot, "cache": "missing"}
		for _, event := range events {
			if event.Type != "build.step" || event.Data.Name != "workspace.render" || !event.Data.OK {
				continue
			}
			switch event.Data.Reason {
			case "rendered_and_published":
				misses++
				entry["cache"] = "miss"
			case "shared_content_artifact", "joined_shared_content_artifact":
				hits++
				entry["cache"] = "hit"
			}
		}
		perWorktree = append(perWorktree, entry)
	}
	if !required && misses == 0 && hits == 0 {
		return map[string]any{"workspace_preparation_reused": true}, nil
	}
	if misses != 1 || hits != len(runtimes)-1 {
		return nil, fmt.Errorf("cross-worktree composition reuse = %d misses and %d hits; want one publish and %d immutable hits", misses, hits, len(runtimes)-1)
	}
	return map[string]any{
		"artifact_publishes": misses, "artifact_hits": hits,
		"isolated_mutable_workspaces": len(runtimes), "per_worktree": perWorktree,
	}, nil
}
