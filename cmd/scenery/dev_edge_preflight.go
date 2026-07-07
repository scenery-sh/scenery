package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

type configuredEdgeReadinessChecker func(context.Context, *localagent.Client, string) (edgeStatusResult, error)
type configuredEdgeRouteProber func(context.Context, string) error

var checkConfiguredEdgeReadiness configuredEdgeReadinessChecker = defaultConfiguredEdgeReadinessCheck
var probeConfiguredEdgeRoute configuredEdgeRouteProber = defaultConfiguredEdgeRouteProbe

func configRequiresPortlessEdge(cfg app.Config) bool {
	_ = cfg
	return false
}

func defaultConfiguredEdgeReadinessCheck(ctx context.Context, client *localagent.Client, baseDomain string) (edgeStatusResult, error) {
	_ = ctx
	_ = client
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return edgeStatusResult{}, err
	}
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
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
	status := edgeStatusForStateDomain(paths, state, baseDomain)
	if status.Ready {
		return status, nil
	}
	return status, configuredEdgeNotReadyError(baseDomain, status)
}

func configuredEdgeNotReadyError(baseDomain string, status edgeStatusResult) error {
	baseDomain = normalizeRouteNamespaceHost(baseDomain)
	if baseDomain == "" {
		baseDomain = localagent.DefaultRouteBaseDomain
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Edge is not ready; refusing to publish portless %s URLs.\n\n", baseDomain)
	fmt.Fprintf(&b, "DNS: %s\n", edgeReadyState(status.DNS.Ready, status.DNS.DNSMasq.State))
	fmt.Fprintf(&b, "Privileged listener: %s\n", edgeReadyState(status.PrivilegedListener.State == "running", status.PrivilegedListener.State))
	fmt.Fprintf(&b, "Caddy: %s", firstNonEmpty(status.Edge.State, localagent.EdgeStatusStopped))
	if status.Edge.Upstream != "" && status.Edge.AgentRouter != "" && status.Edge.Upstream != status.Edge.AgentRouter {
		fmt.Fprintf(&b, " (upstream %s, want router %s)", status.Edge.Upstream, status.Edge.AgentRouter)
	}
	b.WriteByte('\n')
	if status.Edge.AgentRouter != "" {
		fmt.Fprintf(&b, "Router: ready at %s (internal/diagnostic)\n\n", status.Edge.AgentRouter)
	} else {
		b.WriteString("Router: unavailable\n\n")
	}
	b.WriteString("Fix:\n")
	b.WriteString("  scenery system edge restart\n")
	b.WriteString("  scenery system edge status\n\n")
	b.WriteString("If setup is missing:\n")
	b.WriteString("  scenery system edge install\n")
	b.WriteString("  scenery system edge trust\n")
	b.WriteString("  scenery system edge restart")
	return fmt.Errorf("%s", b.String())
}

func edgeReadyState(ready bool, state string) string {
	if ready {
		return "ready"
	}
	state = strings.TrimSpace(state)
	if state == "" {
		return "missing"
	}
	return state
}

func verifyConfiguredEdgeSessionRoute(ctx context.Context, client *localagent.Client, session localagent.Session, baseDomain string, probe bool) error {
	if err := rejectInternalRouterRoutesForConfiguredEdge(baseDomain, session); err != nil {
		return err
	}
	if !probe {
		return nil
	}
	dashboardURL := strings.TrimSpace(session.Routes[localagent.RouteDashboard])
	if dashboardURL == "" {
		return nil
	}
	if err := probeConfiguredEdgeRoute(ctx, dashboardURL); err != nil {
		status, checkErr := checkConfiguredEdgeReadiness(ctx, client, baseDomain)
		if checkErr != nil {
			return checkErr
		}
		if status.Ready {
			return fmt.Errorf("%w\n\nHTTPS probe: %s", configuredEdgeProbeFailedError(baseDomain, dashboardURL), err)
		}
		return fmt.Errorf("%w\n\nHTTPS probe: %s", configuredEdgeNotReadyError(baseDomain, status), err)
	}
	return nil
}

// configuredEdgeProbeFailedError reports the case where every edge component
// is ready but the end-to-end HTTPS probe still failed — usually a transient
// on-demand TLS issuance problem rather than missing setup, so the generic
// "Edge is not ready" component listing would point users at the wrong fix.
func configuredEdgeProbeFailedError(baseDomain, probeURL string) error {
	baseDomain = normalizeRouteNamespaceHost(baseDomain)
	if baseDomain == "" {
		baseDomain = localagent.DefaultRouteBaseDomain
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Edge components are ready, but the HTTPS probe of %s kept failing; refusing to publish portless %s URLs.\n\n", probeURL, baseDomain)
	b.WriteString("This is usually transient on-demand TLS issuance (a just-restarted Caddy, certificate storage contention, or the TLS allow endpoint rejecting the route host).\n\n")
	b.WriteString("Diagnose:\n")
	b.WriteString("  scenery system edge status\n")
	fmt.Fprintf(&b, "  curl -s \"http://127.0.0.1:9440/v1/tls/allow?domain=%s\" -o /dev/null -w '%%{http_code}\\n'\n", normalizeRouteNamespaceHost(probeURL))
	b.WriteString("  tail ~/.scenery/agent/edge/caddy.log\n\n")
	b.WriteString("Then retry, or restart the edge:\n")
	b.WriteString("  scenery system edge restart")
	return fmt.Errorf("%s", b.String())
}

func rejectInternalRouterRoutesForConfiguredEdge(baseDomain string, session localagent.Session) error {
	for route, raw := range session.Routes {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" {
			continue
		}
		port := u.Port()
		if u.Scheme == "https" && port != "" && port != "443" {
			return fmt.Errorf("Edge is not ready; refusing to publish portless %s URLs.\n\nApp route %s resolved to internal/diagnostic router URL %s.\n\nFix:\n  scenery system edge restart\n  scenery system edge status", firstNonEmpty(normalizeRouteNamespaceHost(baseDomain), localagent.DefaultRouteBaseDomain), route, raw)
		}
	}
	return nil
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
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	return nil
}
