package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) optionalVictoria() error {
	return p.scenario("A15", "optional real Victoria failure and recovery do not gate app serving", func(e map[string]any) error {
		root := filepath.Join(p.root, "basic")
		if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		originalEnv := p.env
		defer func() { p.env = originalEnv }()
		p.env = envWithOverrides(p.env, "SCENERY_TOOLCHAIN_DIR="+filepath.Join(p.repo, ".scenery/harness/worktree-runtime/victoria-toolchain"))
		binaries := map[string]string{}
		for _, name := range []string{"metrics", "logs", "traces"} {
			output, err := p.run(root, p.binary, "system", "toolchain", "sync", "--tool", "victoria-"+name, "-o", "json")
			if err != nil {
				return err
			}
			var status struct {
				Artifacts []struct {
					Name        string `json:"name"`
					ManagedPath string `json:"managed_path"`
				} `json:"artifacts"`
			}
			if err := decodeCLIJSON(output, &status); err != nil || len(status.Artifacts) != 1 || status.Artifacts[0].ManagedPath == "" {
				return fmt.Errorf("victoria managed tool sync did not identify one binary: %v", err)
			}
			binaries[name] = status.Artifacts[0].ManagedPath
		}
		p.victoriaBinaries = binaries
		links := filepath.Join(p.root, "victoria-fault")
		if err := os.MkdirAll(links, 0o700); err != nil {
			return err
		}
		p.env = envWithOverrides(p.env, "SCENERY_DEV_VICTORIA=1", "SCENERY_DEV_VICTORIA_DOWNLOAD=0")
		p.env = envWithOverrides(p.env,
			"SCENERY_VICTORIA_LOGS_BIN="+filepath.Join(links, "logs"),
			"SCENERY_VICTORIA_METRICS_BIN="+filepath.Join(links, "metrics"),
			"SCENERY_VICTORIA_TRACES_BIN="+filepath.Join(links, "traces"),
		)
		started := time.Now()
		runtime, err := p.up(root)
		if err != nil {
			return err
		}
		e["serving_with_all_optional_binaries_absent_ms"] = time.Since(started).Milliseconds()
		if err := p.get(runtime.Session.RouteManifest.BaseURL + "/console/"); err != nil {
			return err
		}
		stopSentinel := p.startSentinel(runtime.Session.RouteManifest.BaseURL + "/console/")
		defer stopSentinel()
		deadline := time.Now().Add(15 * time.Second)
		for {
			log, err := os.ReadFile(runtime.LogPath)
			if err == nil && strings.Contains(string(log), "Victoria observability recovery failed") {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("optional failure was not visible in runtime diagnostics")
			}
			time.Sleep(50 * time.Millisecond)
		}
		for name, target := range binaries {
			if err := os.Symlink(target, filepath.Join(links, name)); err != nil {
				return err
			}
		}
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		client := localagent.NewClient(paths.Socket)
		defer client.CloseIdleConnections()
		first, err := p.waitVictoriaReady(client, 0)
		if err != nil {
			return err
		}
		owner := first.Owners["metrics"]
		if owner.PID != first.PIDs["metrics"] || localagent.VerifyOwner(owner) != nil {
			return fmt.Errorf("victoria fault target does not have verified ownership")
		}
		if _, err := p.run(root, "kill", "-KILL", fmt.Sprint(owner.PID)); err != nil {
			return err
		}
		second, err := p.waitVictoriaReady(client, owner.PID)
		if err != nil {
			return err
		}
		record, err := p.record(root)
		if err != nil || record.Postgres != nil {
			return fmt.Errorf("optional observability or console allocated PostgreSQL: %v", err)
		}
		counts := stopSentinel()
		if counts[0] < 2 || counts[1] != 0 {
			return fmt.Errorf("optional recovery caused %d serving failures in %d requests", counts[1], counts[0])
		}
		e["sentinel_requests"], e["sentinel_failures"] = counts[0], counts[1]
		e["old_metrics_pid"], e["new_metrics_pid"], e["postgres_allocated"] = owner.PID, second.PIDs["metrics"], false
		return nil
	})
}

func (p *worktreeRuntimeProbe) waitVictoriaReady(client *localagent.Client, previousPID int) (localagent.Substrate, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		current, err := client.GetSubstrate(p.ctx, localagent.SubstrateVictoria)
		if err == nil && current.Status == "ready" && len(current.PIDs) == 3 && current.PIDs["metrics"] != previousPID {
			verified := true
			for name, pid := range current.PIDs {
				owner := current.Owners[name]
				verified = verified && owner.PID == pid && localagent.VerifyOwner(owner) == nil
			}
			if verified {
				return current, nil
			}
		}
		if time.Now().After(deadline) {
			return current, fmt.Errorf("real Victoria stack did not recover three verified components: %v", err)
		}
		select {
		case <-p.ctx.Done():
			return current, p.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
