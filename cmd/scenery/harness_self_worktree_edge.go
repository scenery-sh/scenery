package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) edgeSeparation(root, sibling string) error {
	return p.scenario("A17", "explicit machine-domain lease keeps local developer and published operator routes separate", func(e map[string]any) error {
		session, err := p.liveSession(root)
		if err != nil {
			return err
		}
		before, err := p.record(root)
		if err != nil {
			return err
		}
		machineRoot := filepath.Join(p.root, "explicit-machine-edge")
		server, err := localagent.NewServer(localagent.RunOptions{Home: machineRoot, SocketPath: filepath.Join(p.root, "edge.sock"), RouterAddr: "127.0.0.1:0"})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(p.ctx)
		done := make(chan error, 1)
		go func() { done <- server.Run(ctx) }()
		defer func() { cancel(); _ = server.Close(); <-done }()
		client := localagent.NewClient(server.Paths().SocketPath)
		defer client.CloseIdleConnections()
		health, err := client.Health(p.ctx)
		if err != nil || localagent.ValidateControlHealth(health, server.Paths().SocketPath) != nil {
			return fmt.Errorf("explicit machine owner protocol is unavailable: %v", err)
		}
		browser, err := url.Parse(session.RouteManifest.BaseURL)
		if err != nil {
			return err
		}
		manifest := session.RouteManifest
		manifest.DomainHost, manifest.DomainURL, manifest.PublicRoutes = "worktree-edge.example.test", "https://worktree-edge.example.test", []string{"api"}
		request := localagent.RegisterRequest{BaseAppID: session.BaseAppID, Environment: session.Environment, AppRoot: session.AppRoot, SessionID: session.SessionID, Branch: session.Branch, Status: "running", OwnerPID: session.OwnerPID, Owner: session.Owner, Backends: session.Backends, RouteNamespace: session.RouteNamespace, RouteManifest: manifest, WorktreeProxy: &localagent.Backend{Network: "tcp", Addr: browser.Host}, ClaimOwner: true}
		// The browser URL is localhost, while edge leases require an explicit
		// numeric loopback address to avoid name-resolution ownership ambiguity.
		request.WorktreeProxy.Addr = "127.0.0.1:" + browser.Port()
		lease, err := client.Register(p.ctx, request)
		if err != nil || lease.StateRoot != "" || lease.DomainHostConflict != nil {
			return fmt.Errorf("explicit domain lease was not registered without runtime ownership: %v", err)
		}
		for _, check := range []struct {
			path string
			want int
		}{{"/api/books", 200}, {"/console/", 404}, {"/runtime/health", 404}, {"/__scenery", 404}} {
			req, err := http.NewRequestWithContext(p.ctx, http.MethodGet, "http://"+server.RouterAddr()+check.path, nil)
			if err != nil {
				return err
			}
			req.Host = manifest.DomainHost
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			_ = response.Body.Close()
			if response.StatusCode != check.want {
				return fmt.Errorf("explicit domain %s returned %d, expected %d", check.path, response.StatusCode, check.want)
			}
		}
		if err := p.get(session.RouteManifest.BaseURL + "/console/"); err != nil {
			return err
		}
		if response, err := p.dashboardRPC(root, "postgres/tables", map[string]string{"app_id": session.SessionID}); err != nil || response.Error != nil {
			return fmt.Errorf("local developer WebSocket was lost after domain publication: %v", err)
		}
		other, err := p.liveSession(sibling)
		if err != nil {
			return err
		}
		request.AppRoot, request.SessionID, request.Owner, request.OwnerPID = other.AppRoot, other.SessionID, other.Owner, other.OwnerPID
		conflict, err := client.Register(p.ctx, request)
		if err == nil && conflict.DomainHostConflict == nil {
			return fmt.Errorf("second worktree stole the explicit domain without a claim")
		}
		after, err := p.record(root)
		if err != nil || !sameWorktreeCluster(before, after) {
			return fmt.Errorf("machine domain lease mutated worktree SQL authority: %v", err)
		}
		if _, _, err := client.DeleteOwnedSession(p.ctx, lease, false); err != nil {
			return err
		}
		if err := p.unavailableDomain(root); err != nil {
			return err
		}
		paths, err := commandAgentPaths()
		if err != nil {
			return err
		}
		if _, err := resolveCaddyBinary(p.ctx, paths, true); err != nil {
			return err
		}
		published, err := runHarnessEdgeStaticFrontendProbe(p.ctx, filepath.Join(p.root, "published-edge"))
		if err != nil {
			return err
		}
		e["explicit_domain_api_http"], e["local_console_http"], e["local_websocket"] = 200, 200, "verified"
		e["domain_hidden_routes"] = []string{"console", "runtime", "__scenery"}
		e["domain_ownership_conflict"], e["unavailable_edge_not_advertised"] = true, true
		e["published_caddy_contract"] = published
		return nil
	})
}

func (p *worktreeRuntimeProbe) unavailableDomain(source string) error {
	root := filepath.Join(p.root, "unavailable-domain")
	if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-unavailable-domain", root); err != nil {
		return err
	}
	p.roots = append(p.roots, root)
	configPath := filepath.Join(root, ".scenery.json")
	encoded, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(encoded, &config); err != nil {
		return err
	}
	config["envs"].(map[string]any)["local"].(map[string]any)["domain"] = "worktree-probe.invalid"
	encoded, err = json.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		return err
	}
	runtime, err := p.up(root)
	if err != nil {
		return err
	}
	if runtime.Session.RouteManifest.DomainURL != "" {
		return fmt.Errorf("unavailable machine edge was advertised as a public URL")
	}
	log, err := os.ReadFile(runtime.LogPath)
	if err != nil || !bytes.Contains(log, []byte("localhost remains usable")) {
		return fmt.Errorf("unavailable domain lacks a visible localhost fallback diagnostic: %v", err)
	}
	return p.get(worktreeProbeAPI(runtime) + "/books")
}
