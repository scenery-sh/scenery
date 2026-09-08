package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) outage(rootA, rootB string, a, b detachedDevResult) error {
	return p.scenario("A5", "verified A-only PostgreSQL outage leaves B continuously serving", func(e map[string]any) error {
		beforeA, err := p.record(rootA)
		if err != nil {
			return err
		}
		beforeB, err := p.record(rootB)
		if err != nil {
			return err
		}
		start := time.Now().UTC()
		stop, done := make(chan struct{}), make(chan [2]int, 1)
		go func() {
			var counts [2]int
			for {
				counts[0]++
				if err := p.get(worktreeProbeAPI(b) + "/books"); err != nil {
					counts[1]++
				}
				select {
				case <-stop:
					done <- counts
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		}()
		mutationErr := p.verifiedContainerCommand(beforeA, "stop", "--time", "10")
		if mutationErr == nil {
			mutationErr = p.verifiedContainerCommand(beforeA, "start")
		}
		if mutationErr == nil {
			mutationErr = p.waitServing(worktreeProbeAPI(a) + "/books")
		}
		close(stop)
		counts := <-done
		e["started_at"], e["ended_at"] = start.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)
		e["sentinel_requests"], e["sentinel_failures"] = counts[0], counts[1]
		if mutationErr != nil {
			return mutationErr
		}
		if counts[0] < 2 || counts[1] != 0 {
			return fmt.Errorf("worktree B sentinel completed %d requests with %d failures", counts[0], counts[1])
		}
		afterB, err := p.record(rootB)
		if err != nil {
			return err
		}
		sessionB, err := p.liveSession(rootB)
		if err != nil {
			return err
		}
		if sessionB.OwnerPID != b.PID || !sameWorktreeCluster(beforeB, afterB) {
			return fmt.Errorf("worktree A outage changed B ownership")
		}
		e["b_ownership_unchanged"] = true
		_, err = p.verify(rootB, b, "persisted")
		return err
	})
}

func (p *worktreeRuntimeProbe) verifiedContainerCommand(record localagent.WorktreeRecord, action string, args ...string) error {
	if err := p.verifyRetainedContainer(record); err != nil {
		return err
	}
	argv := append([]string{"--host", record.Postgres.DaemonEndpoint, action}, args...)
	argv = append(argv, record.Postgres.ContainerID)
	_, err := p.run(record.AppRoot, "docker", argv...)
	return err
}

func sameWorktreeCluster(a, b localagent.WorktreeRecord) bool {
	x, y := a.Postgres, b.Postgres
	return x != nil && y != nil && a.AppRoot == b.AppRoot && a.AppID == b.AppID && x.InstanceID == y.InstanceID && x.DaemonID == y.DaemonID && x.ContainerID == y.ContainerID && x.Volume == y.Volume && x.VolumeCreatedAt == y.VolumeCreatedAt && x.SystemID == y.SystemID && x.User == y.User && x.Password == y.Password
}

func (p *worktreeRuntimeProbe) liveSession(root string) (localagent.Session, error) {
	paths, err := localagent.PathsForWorktree(p.home, root)
	if err != nil {
		return localagent.Session{}, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	health, err := client.Health(p.ctx)
	if err != nil {
		return localagent.Session{}, err
	}
	if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
		return localagent.Session{}, err
	}
	sessions, err := client.List(p.ctx, paths.AppRoot)
	if err != nil {
		return localagent.Session{}, err
	}
	if len(sessions) != 1 || sessions[0].AppRoot != paths.AppRoot {
		return localagent.Session{}, fmt.Errorf("expected one exact-root live session")
	}
	return sessions[0], nil
}

func (p *worktreeRuntimeProbe) waitServing(url string) error {
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()
	var last error
	for {
		if last = p.get(url); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("serving recovery: %w: %v", ctx.Err(), last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (p *worktreeRuntimeProbe) lifecycle(root string, runtime *detachedDevResult) error {
	return p.scenario("A6", "rebuild, down/up, supervisor crash and PostgreSQL restart retain data", func(e map[string]any) error {
		before, err := p.record(root)
		if err != nil {
			return err
		}
		session, err := p.liveSession(root)
		if err != nil {
			return err
		}
		sourcePath := filepath.Join(root, "library/service.go")
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(sourcePath, append(source, []byte("\n// Release probe rebuild boundary.\n")...), 0o644); err != nil {
			return err
		}
		deadline := time.Now().Add(45 * time.Second)
		for {
			current, readErr := p.liveSession(root)
			if readErr == nil && current.AppPID != "" && current.AppPID != session.AppPID && current.Status == "running" {
				e["rebuild_old_app_pid"], e["rebuild_new_app_pid"] = session.AppPID, current.AppPID
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("source edit did not produce a serving replacement app process")
			}
			select {
			case <-p.ctx.Done():
				return p.ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if _, err := p.verify(root, *runtime, "persisted"); err != nil {
			return err
		}
		if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		*runtime, err = p.up(root)
		if err != nil {
			return err
		}
		if _, err := p.verify(root, *runtime, "persisted"); err != nil {
			return err
		}
		session, err = p.liveSession(root)
		if err != nil {
			return err
		}
		if session.OwnerPID != runtime.PID || localagent.VerifyOwner(session.Owner) != nil {
			return fmt.Errorf("supervisor fingerprint changed before crash injection")
		}
		if _, err := p.run(root, "kill", "-KILL", fmt.Sprint(runtime.PID)); err != nil {
			return err
		}
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		deadline = time.Now().Add(10 * time.Second)
		for {
			held, lockErr := paths.ProbeLiveLock()
			if lockErr != nil {
				return lockErr
			}
			if !held {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("crashed supervisor retained the owner lock")
			}
			time.Sleep(25 * time.Millisecond)
		}
		*runtime, err = p.up(root)
		if err != nil {
			return err
		}
		if _, err := p.verify(root, *runtime, "persisted"); err != nil {
			return err
		}
		after, err := p.record(root)
		if err != nil {
			return err
		}
		if !sameWorktreeCluster(before, after) {
			return fmt.Errorf("runtime lifecycle replaced retained cluster identity or credentials")
		}
		if err := p.verifiedContainerCommand(after, "restart", "--time", "10"); err != nil {
			return err
		}
		if err := p.waitServing(worktreeProbeAPI(*runtime) + "/books"); err != nil {
			return err
		}
		e["cluster_identity_retained"], e["recovered_owner_pid"] = true, runtime.PID
		_, err = p.verify(root, *runtime, "persisted")
		return err
	})
}
