package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

type configuredEdgeRouteProber func(context.Context, string) error

var probeConfiguredEdgeRoute configuredEdgeRouteProber = defaultConfiguredEdgeRouteProbe

// devDomainEdgeStatus loads the current edge component status for a dev
// domain host check without turning unreadiness into an error.
func devDomainEdgeStatus(domain string) (edgeStatusResult, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return edgeStatusResult{}, err
	}
	state, err := localagent.ReadCurrentEdgeState(paths.EdgeStatePath)
	if err != nil {
		return edgeStatusResult{}, err
	}
	if state.Kind == "" {
		state = localagent.EdgeState{
			Kind:         localagent.EdgeKindCaddy,
			Status:       localagent.EdgeStatusStopped,
			PublicAddr:   defaultEdgePublicAddr,
			PublicScheme: "https",
			ConfigPath:   paths.EdgeConfigPath,
			LogPath:      paths.EdgeLogPath,
		}
	}
	return edgeStatusForStateDomain(paths, state, domain), nil
}

// devDomainEdgeComponentsReady reports whether managed Caddy, the privileged
// loopback listener forwarding to it, and the live agent router as its
// upstream can serve a dev domain host right now. Managed DNS is
// intentionally excluded: dev domain resolution is operator-owned public DNS
// (wildcard to 127.0.0.1), so the end-to-end HTTPS probe is the DNS truth.
func devDomainEdgeComponentsReady(status edgeStatusResult) bool {
	return status.Edge.State == localagent.EdgeStatusRunning &&
		status.PrivilegedListener.State == "running" &&
		status.PrivilegedListener.Target == status.Edge.HTTPSListen &&
		status.PrivilegedListener.TargetPID == status.Edge.PID &&
		status.Edge.AgentRouter != "" &&
		status.Edge.AgentRouter == status.Edge.Upstream
}

// validateDevDomainURL returns the session's dev domain base URL when the
// edge components are ready and the end-to-end HTTPS probe of the domain
// host succeeds, plus a warning explaining why the URL is not being
// advertised otherwise. Localhost URLs always stay valid: a missing edge or
// failed probe degrades output, never the dev session.
func validateDevDomainURL(ctx context.Context, session localagent.Session) (string, string) {
	if conflict := session.DomainHostConflict; conflict != nil {
		return "", fmt.Sprintf("dev domain https://%s is live under %s; this worktree keeps localhost URLs", conflict.Host, aliasConflictOwnerLabel(*conflict))
	}
	domainURL := strings.TrimSpace(session.RouteManifest.DomainURL)
	if domainURL == "" {
		return "", ""
	}
	status, err := devDomainEdgeStatus(session.RouteManifest.DomainHost)
	if err != nil {
		return "", fmt.Sprintf("dev domain %s not advertised; edge status unavailable: %v", domainURL, err)
	}
	if !devDomainEdgeComponentsReady(status) {
		return "", fmt.Sprintf("dev domain %s not advertised; the local edge is not ready. Fix:\n  scenery system edge install\n  scenery system edge trust\n  scenery system edge restart", domainURL)
	}
	if err := probeConfiguredEdgeRoute(ctx, domainURL+localagent.PathModeRuntimePrefix+"/health"); err != nil {
		return "", fmt.Sprintf("dev domain %s not advertised; HTTPS probe failed: %v\nCheck that wildcard DNS for the domain resolves to 127.0.0.1 and run `scenery system edge status`.", domainURL, err)
	}
	return domainURL, ""
}

// The probe is the first TLS handshake ever made for the session's hostname,
// so it exercises Caddy's full on-demand pipeline: the TLS allow callback to
// the agent plus certificate issuance from the local CA. That pipeline has
// transient failure modes (a just-restarted Caddy, certificate storage lock
// contention with renewal maintenance), so a single failed attempt must not
// fail `scenery up`; retry with backoff before giving up.
var (
	edgeProbeRetryWindow   = 10 * time.Second
	edgeProbeRetryInterval = 500 * time.Millisecond
)

func defaultConfiguredEdgeRouteProbe(ctx context.Context, rawURL string) error {
	if err := probeConfiguredEdgeRouteOnce(ctx, rawURL); err == nil {
		return nil
	}
	deadline := time.Now().Add(edgeProbeRetryWindow)
	var lastErr error
	for {
		lastErr = probeConfiguredEdgeRouteOnce(ctx, rawURL)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(edgeProbeRetryInterval):
		}
	}
}

func probeConfiguredEdgeRouteOnce(ctx context.Context, rawURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	return nil
}
