package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

type configuredEdgeReadinessChecker func(context.Context, *localagent.Client, string) (edgeStatusResult, error)
type configuredEdgeRouteProber func(context.Context, string) error

var checkConfiguredEdgeReadiness configuredEdgeReadinessChecker = defaultConfiguredEdgeReadinessCheck
var probeConfiguredEdgeRoute configuredEdgeRouteProber = defaultConfiguredEdgeRouteProbe

func configRequiresPortlessEdge(cfg app.Config) bool {
	return strings.TrimSpace(cfg.Proxy.RouteBaseDomain) != ""
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
	b.WriteString("  onlava system edge restart\n")
	b.WriteString("  onlava system edge status\n\n")
	b.WriteString("If setup is missing:\n")
	b.WriteString("  onlava system edge install\n")
	b.WriteString("  onlava system edge trust\n")
	b.WriteString("  onlava system edge restart")
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
		return fmt.Errorf("%w\n\nHTTPS probe: %s", configuredEdgeNotReadyError(baseDomain, status), err)
	}
	return nil
}

func rejectInternalRouterRoutesForConfiguredEdge(baseDomain string, session localagent.Session) error {
	for route, raw := range session.Routes {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" {
			continue
		}
		port := u.Port()
		if u.Scheme == "https" && port != "" && port != "443" {
			return fmt.Errorf("Edge is not ready; refusing to publish portless %s URLs.\n\nSession route %s resolved to internal/diagnostic router URL %s.\n\nFix:\n  onlava system edge restart\n  onlava system edge status", firstNonEmpty(normalizeRouteNamespaceHost(baseDomain), localagent.DefaultRouteBaseDomain), route, raw)
		}
	}
	return nil
}

func defaultConfiguredEdgeRouteProbe(ctx context.Context, rawURL string) error {
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
