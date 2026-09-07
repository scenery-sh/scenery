package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

// Explicit domain configuration may lease forwarding from an already running
// compatible machine edge. Local serving never starts or repairs that edge.
func publishWorktreeDomain(ctx context.Context, paths localagent.Paths, session localagent.Session, domain, browserAddress string, claim bool) (domainURL string, cleanup func(), warning string) {
	cleanup = func() {}
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return "", cleanup, ""
	}
	fail := func(err error) (string, func(), string) {
		return "", cleanup, fmt.Sprintf("configured domain %s is unavailable; localhost remains usable: %v", domain, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)
	health, err := client.Health(ctx)
	if err != nil {
		return fail(err)
	}
	if err := localagent.ValidateControlHealth(health, paths.SocketPath); err != nil {
		return fail(err)
	}
	status, err := devDomainEdgeStatus(domain)
	if err != nil {
		return fail(err)
	}
	if !devDomainEdgeComponentsReady(status) {
		return fail(fmt.Errorf("the explicitly managed edge is not ready; inspect scenery system edge status"))
	}
	manifest := session.RouteManifest
	manifest.DomainHost, manifest.DomainURL = domain, "https://"+domain
	proxy := localagent.Backend{Network: "tcp", Addr: browserAddress}
	lease, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: session.BaseAppID, Environment: session.Environment, AppRoot: session.AppRoot,
		SessionID: session.SessionID, Branch: session.Branch, Status: "running", OwnerPID: session.OwnerPID, Owner: session.Owner,
		Backends: session.Backends, RouteNamespace: session.RouteNamespace, RouteManifest: manifest,
		WorktreeProxy: &proxy, ClaimOwner: true, ClaimAliases: claim,
	})
	if err != nil {
		return fail(err)
	}
	cleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		health, err := client.Health(cleanupCtx)
		if err != nil || localagent.ValidateControlHealth(health, paths.SocketPath) != nil {
			return
		}
		_, _, _ = client.DeleteOwnedSession(cleanupCtx, lease, false)
	}
	domainURL, warning = validateDevDomainURL(ctx, lease)
	return domainURL, cleanup, warning
}
