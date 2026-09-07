package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) noSQL() error {
	return p.scenario("A2", "ordinary no-SQL runtime and console allocate no PostgreSQL", func(e map[string]any) error {
		root := filepath.Join(p.root, "basic")
		p.roots = append(p.roots, root)
		for _, name := range []string{".scenery.json", ".gitignore", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn"} {
			data, err := os.ReadFile(filepath.Join(p.repo, "testdata/apps/basic", name))
			if err != nil {
				return err
			}
			if name == "go.mod" {
				data = bytes.ReplaceAll(data, []byte("=> ../../.."), []byte("=> "+filepath.ToSlash(p.repo)))
			}
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
		}
		runtime, err := p.up(root)
		if err != nil {
			return err
		}
		if err := p.get(runtime.Session.RouteManifest.BaseURL + "/console/"); err != nil {
			return err
		}
		response, err := p.dashboardRPC(root, "postgres/tables", map[string]string{"app_id": runtime.Session.SessionID})
		if err != nil {
			return err
		}
		if response.Error == nil || !strings.Contains(response.Error.Message, "no postgres database") {
			return fmt.Errorf("SQL-backed explorer did not report its absent capability: %+v", response.Error)
		}
		record, err := p.record(root)
		if err != nil {
			return err
		}
		if record.Postgres != nil {
			return fmt.Errorf("no-SQL runtime or console allocated managed PostgreSQL")
		}
		e["base_url"], e["console_http"], e["postgres_allocated"] = runtime.Session.RouteManifest.BaseURL, 200, false
		e["auxiliary_capability_error"] = response.Error.Message
		return nil
	})
}

func (p *worktreeRuntimeProbe) concurrentAcquisition(rootA string) error {
	return p.scenario("A7", "same-root concurrent acquisition and environment conflict", func(e map[string]any) error {
		root := filepath.Join(p.root, "concurrent")
		p.roots = append(p.roots, root)
		if _, err := p.run(rootA, "git", "worktree", "add", "--quiet", "-b", "probe-concurrent", root); err != nil {
			return err
		}
		type result struct {
			runtime detachedDevResult
			err     error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				runtime, err := p.up(root)
				results <- result{runtime, err}
			}()
		}
		first, second := <-results, <-results
		if first.err != nil || second.err != nil {
			return fmt.Errorf("concurrent acquisitions: %v; %v", first.err, second.err)
		}
		if first.runtime.PID != second.runtime.PID || first.runtime.PID <= 0 || first.runtime.AlreadyRunning == second.runtime.AlreadyRunning {
			return fmt.Errorf("concurrent startup did not converge to exactly one new owner")
		}
		before, err := p.record(root)
		if err != nil {
			return err
		}
		output, conflictErr := p.run(root, p.binary, "up", "--app-root", root, "--env", "preview", "--detach", "-o", "json")
		if conflictErr == nil {
			return fmt.Errorf("different selected environment was treated as idempotent acquisition")
		}
		if !bytes.Contains(output, []byte("environment")) {
			return fmt.Errorf("environment conflict lacked an actionable diagnostic")
		}
		after, err := p.record(root)
		if err != nil {
			return err
		}
		session, err := p.liveSession(root)
		if err != nil {
			return err
		}
		if !sameWorktreeCluster(before, after) || session.OwnerPID != first.runtime.PID {
			return fmt.Errorf("rejected environment selection mutated current ownership")
		}
		e["single_owner_pid"], e["single_resource_id"] = session.OwnerPID, after.Postgres.InstanceID
		e["different_environment_rejected"] = true
		e["same_root_protocol_conflict_evidence"] = "A8"
		return p.get(worktreeProbeAPI(first.runtime) + "/books")
	})
}

func (p *worktreeRuntimeProbe) dashboardRPC(root, method string, params any) (rpcResponse, error) {
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	paths, err := localagent.PathsForWorktree(p.home, root)
	if err != nil {
		return rpcResponse{}, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	health, err := client.Health(ctx)
	if err != nil {
		return rpcResponse{}, err
	}
	if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
		return rpcResponse{}, err
	}
	connection, response, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+health.DashboardBackend.Addr+"/__scenery", nil)
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		return rpcResponse{}, err
	}
	defer func() { _ = connection.Close() }()
	deadline, _ := ctx.Deadline()
	if err := connection.SetReadDeadline(deadline); err != nil {
		return rpcResponse{}, err
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		return rpcResponse{}, err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return rpcResponse{}, err
	}
	if err := connection.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: "probe", Method: method, Params: encoded}); err != nil {
		return rpcResponse{}, err
	}
	for {
		var response rpcResponse
		if err := connection.ReadJSON(&response); err != nil {
			return response, err
		}
		if response.ID == "probe" {
			return response, nil
		}
	}
}
