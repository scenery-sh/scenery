package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/deploydiag"
)

func buildDeployStatusWithContext(ctx context.Context, paths localagent.Paths, registry localagent.DeployRegistry) deployStatusResponse {
	return buildDeployStatusWithDependencies(ctx, paths, registry, liveDeployStatusDependencies())
}

func liveDeployStatusDependencies() deployStatusDependencies {
	return deployStatusDependencies{
		serviceManager: deployServiceManagerFunc,
		edgeStatus: func(paths localagent.Paths, state localagent.EdgeState) edgeStatusCaddy {
			return edgeStatusForStateDomain(paths, state, "").Edge
		},
		privilegedListenerStatus: privilegedListenerStatus,
		agentStatus:              deployAgentStatusFor,
		agentSupervisorStatus:    deployAgentSupervisorStatusFor,
		launchAgentStatus:        deployLaunchAgentStatusFor,
		systemdSnapshotOverlay:   deploySystemdSnapshotOverlay,
		diagnosticsReport: func(ctx context.Context, snapshot deploydiag.Snapshot) deploydiag.Report {
			return deploydiag.BuildReport(ctx, snapshot, deployDiagnosticsDeps())
		},
	}
}

type deployStatusDependencies struct {
	serviceManager           func() string
	edgeStatus               func(localagent.Paths, localagent.EdgeState) edgeStatusCaddy
	privilegedListenerStatus func(localagent.Paths) edgeStatusPrivilegedListener
	agentStatus              func(localagent.Paths) deployAgentStatus
	agentSupervisorStatus    func(localagent.Paths) deployAgentSupervisorStatus
	launchAgentStatus        func() deployLaunchAgentStatus
	systemdSnapshotOverlay   func(*deploydiag.Snapshot)
	diagnosticsReport        func(context.Context, deploydiag.Snapshot) deploydiag.Report
}

func buildDeployStatusWithDependencies(ctx context.Context, paths localagent.Paths, registry localagent.DeployRegistry, deps deployStatusDependencies) deployStatusResponse {
	edgeState, _ := localagent.LoadEdgeState(paths.EdgeStatePath)
	edgeStatus := deps.edgeStatus(paths, edgeState)
	helper := deps.privilegedListenerStatus(paths)
	agent := deps.agentStatus(paths)
	agentSupervisor := deps.agentSupervisorStatus(paths)
	launchAgent := deps.launchAgentStatus()
	sessions := deploySessionsByAppRoot(paths)
	targets := make([]deployTargetStatus, 0, len(registry.Targets))
	for _, target := range registry.Targets {
		session := sessions[filepath.Clean(target.AppRoot)]
		cert := deployCertStatusFor(paths, target.Domain)
		item := deployTargetStatus{
			Environment: firstNonEmpty(target.Environment, "unknown"),
			Domain:      target.Domain,
			AppRoot:     target.AppRoot,
			RootService: target.RootService,
			Enabled:     target.Enabled,
			LiveSession: session.SessionID != "",
			SessionID:   session.SessionID,
			CertPresent: cert.Present,
			Frontends:   deployTargetFrontendStatuses(target),
		}
		if !cert.NotAfter.IsZero() {
			item.CertNotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
		}
		if target.Enabled && session.SessionID == "" {
			item.Diagnostics = append(item.Diagnostics, "enabled target has no running scenery up session")
		}
		if cert.Present && cert.Error != "" {
			item.Diagnostics = append(item.Diagnostics, "certificate expiry could not be parsed: "+cert.Error)
		}
		for _, frontend := range item.Frontends {
			if target.Enabled && frontend.Mode != "caddy_static" {
				item.Diagnostics = append(item.Diagnostics, fmt.Sprintf("published frontend %q has no complete current artifact; serving falls back to the agent proxy", frontend.Name))
			}
		}
		targets = append(targets, item)
	}
	manager := deps.serviceManager()
	helperPublic := deploydiag.HelperHasPublicBinding(helper.Listen)
	status := deployStatusResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.status"),
		ServiceManager:     manager,
		RegistryPath:       paths.DeployPath,
		PrivilegedListener: helper,
		HelperPublic:       helperPublic,
		Edge:               edgeStatus,
		Agent:              agent,
		AgentSupervisor:    agentSupervisor,
		LaunchAgent:        launchAgent,
		ACME: deployACMEStatus{
			Email: registry.ACMEEmail,
			CA:    firstNonEmpty(registry.ACMECA, "production"),
		},
		Targets: targets,
	}
	if manager != "systemd" && (helper.State != "running" || !helperPublic) {
		status.Diagnostics = append(status.Diagnostics, "public privileged listener is not ready; run `scenery deploy setup`")
	}
	if edgeStatus.State != localagent.EdgeStatusRunning {
		status.Diagnostics = append(status.Diagnostics, "Caddy edge is not running")
	}
	if agent.State != "running" {
		status.Diagnostics = append(status.Diagnostics, "Scenery agent is not running")
	}
	// Supervision is part of deploy readiness: a unit or plist on disk the
	// service manager never loaded recovers nothing, so presence alone never
	// counts.
	supervisorWord := "LaunchAgent"
	if manager == "systemd" {
		supervisorWord = "systemd unit"
	}
	switch {
	case !agentSupervisor.Installed:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("scenery agent supervisor %s is not installed; run `scenery deploy setup`", supervisorWord))
	case !agentSupervisor.Loaded:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("scenery agent supervisor %s is installed but not loaded; run `scenery deploy setup`", supervisorWord))
	case !agentSupervisor.Running:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("scenery agent supervisor %s is loaded but its agent process is not running; run `scenery system agent restart`", supervisorWord))
	}
	switch {
	case !launchAgent.Installed:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("deploy resume %s is not installed", supervisorWord))
	case !launchAgent.Loaded:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("deploy resume %s exists but is not loaded; run `scenery deploy setup`", supervisorWord))
	case launchAgent.failed():
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("deploy resume %s completed with exit code %d; inspect its log and run `scenery deploy resume`", supervisorWord, *launchAgent.LastExitCode))
	}
	snapshot := deployDiagnosticsSnapshot(status)
	if manager == "systemd" {
		deps.systemdSnapshotOverlay(&snapshot)
	}
	diagnostics := deps.diagnosticsReport(ctx, snapshot)
	status.DiagnosticsDetail = &diagnostics
	for _, check := range diagnostics.Checks {
		if check.Status == "warn" || check.Status == "error" {
			status.Diagnostics = append(status.Diagnostics, check.Message)
		}
	}
	status.Ready = len(status.Diagnostics) == 0
	return status
}

// deployDiagHelperStatus converts the CLI privileged-listener payload into
// the deploydiag helper snapshot.
func deployDiagHelperStatus(helper edgeStatusPrivilegedListener) deploydiag.HelperStatus {
	return deploydiag.HelperStatus{
		Installed:        helper.Installed,
		State:            helper.State,
		PID:              helper.PID,
		Listen:           helper.Listen,
		Target:           helper.Target,
		Version:          helper.Version,
		ContractRevision: helper.ContractRevision,
	}
}

// deployDiagnosticsSnapshot converts the CLI deploy status payload into the
// snapshot the diagnostics engine consumes. Status targets mirror the deploy
// registry targets one to one, so they carry both the registry enablement and
// the certificate observations.
func deployDiagnosticsSnapshot(status deployStatusResponse) deploydiag.Snapshot {
	targets := make([]deploydiag.Target, 0, len(status.Targets))
	for _, target := range status.Targets {
		targets = append(targets, deploydiag.Target{
			Domain:       target.Domain,
			Enabled:      target.Enabled,
			CertPresent:  target.CertPresent,
			CertNotAfter: target.CertNotAfter,
		})
	}
	return deploydiag.Snapshot{
		Helper:          deployDiagHelperStatus(status.PrivilegedListener),
		HelperPublic:    status.HelperPublic,
		EdgeHTTPSListen: status.Edge.HTTPSListen,
		Targets:         targets,
		CurrentVersion:  buildVersionResponse().Version,
	}
}

// deployDiagnosticsDeps wires the injectable probe functions into the
// diagnostics engine at call time so tests can stub the package variables.
func deployDiagnosticsDeps() deploydiag.Deps {
	return deploydiag.Deps{
		PortListener:   deployPortListenerFunc,
		LANIP:          deployLANIPFunc,
		HTTPProbe:      deployHTTPProbeFunc,
		PublicIP:       deployPublicIPFunc,
		DNSLookup:      deployDNSLookupFunc,
		PowerStatus:    deployPowerStatusFunc,
		FirewallStatus: deployFirewallStatusFunc,
		TLSProbe: func(addr, serverName string) deploydiag.TLSProbeResult {
			result := edgeTLSProbeFunc(addr, serverName, edgeTLSProbeTimeout)
			return deploydiag.TLSProbeResult{Outcome: deploydiag.TLSProbeOutcome(result.Outcome), Error: result.Error}
		},
	}
}

func deployAgentStatusFor(paths localagent.Paths) deployAgentStatus {
	status := deployAgentStatus{
		State:      "stopped",
		StatePath:  paths.StatePath,
		SocketPath: paths.SocketPath,
	}
	state, err := localagent.LoadState(paths.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		status.State = "unhealthy"
		status.Message = err.Error()
		return status
	}
	status.PID = state.PID
	status.RouterAddr = state.RouterAddr
	if processAliveForEdge(state.PID) {
		status.State = "running"
	} else {
		status.State = "stale"
	}
	return status
}

func deployAgentSupervisorStatusFor(paths localagent.Paths) deployAgentSupervisorStatus {
	if deployServiceManagerFunc() == "systemd" {
		return deployAgentSupervisorStatusFromService(localagent.AgentSystemdStatusForSocket(paths.SocketPath))
	}
	return deployAgentSupervisorStatusFromService(agentSupervisorStatusFunc(paths.SocketPath))
}

func deployAgentSupervisorStatusFromService(status localagent.LaunchdAgentStatus) deployAgentSupervisorStatus {
	return deployAgentSupervisorStatus{
		Installed: status.PlistPresent && status.SupervisesSocket,
		Loaded:    status.Loaded,
		Running:   status.Running,
		PID:       status.PID,
		Label:     status.Label,
		Path:      status.PlistPath,
	}
}

func deploySessionsByAppRoot(paths localagent.Paths) map[string]localagent.Session {
	out := map[string]localagent.Session{}
	registry, err := localagent.OpenRegistry(paths.RegistryPath, localagent.RouterAddrFromEnv())
	if err != nil {
		return out
	}
	for _, session := range registry.List() {
		if session.Status != "running" {
			continue
		}
		root := filepath.Clean(session.AppRoot)
		if _, ok := out[root]; !ok {
			out[root] = session
		}
	}
	return out
}

func deployCertStatusFor(paths localagent.Paths, domain string) deployCertStatus {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return deployCertStatus{}
	}
	matches, _ := filepath.Glob(filepath.Join(paths.EdgeDir, "caddy-data", "certificates", "*", domain, "*.crt"))
	if len(matches) == 0 {
		return deployCertStatus{}
	}
	sort.Strings(matches)
	status := deployCertStatus{Present: true, Path: matches[0]}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		status.Error = err.Error()
		return status
	}
	block, _ := pem.Decode(data)
	if block == nil {
		status.Error = "certificate PEM block not found"
		return status
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.NotAfter = cert.NotAfter
	return status
}
