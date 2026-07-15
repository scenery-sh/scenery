package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/deploydiag"
	"scenery.sh/internal/edge"
)

// deployServiceManager selects the host service manager for public deploy
// lifecycles: launchd on macOS, systemd on Linux hosts running systemd.
var deployServiceManagerFunc = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "launchd"
	case "linux":
		if localagent.SystemdSupported() {
			return "systemd"
		}
		return ""
	default:
		return ""
	}
}

// edgeSystemdManaged reports whether the managed Caddy edge on this host is
// owned by the scenery-edge systemd unit. When true, every edge restart path
// must converge through systemd instead of spawning an unsupervised Caddy
// that races the unit's Restart=always respawn.
func edgeSystemdManaged() bool {
	return runtime.GOOS == "linux" && edge.SystemdEdgeUnitInstalled()
}

// runDeploySetupLinux converges a single-user Linux/systemd public edge:
// managed Caddy binary, Scenery-rendered and validated Caddyfile, the
// scenery-agent and scenery-edge units, and the boot-time deploy resume.
func runDeploySetupLinux(stdout io.Writer, opts deployOptions) error {
	if !localagent.SystemdSupported() {
		return fmt.Errorf("scenery deploy setup on Linux requires systemd (no /run/systemd/system); only systemd hosts are supported")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery deploy setup on Linux must run as root: it installs system units under /etc/systemd/system and binds ports 80/443")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	if opts.ACMEEmail != "" {
		registry.ACMEEmail = strings.TrimSpace(opts.ACMEEmail)
	}
	if opts.ACMECA != "" {
		registry.ACMECA = opts.ACMECA
	}
	if err := deployPreflightPublicPortsLinux(); err != nil {
		return err
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		return err
	}
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	if err := installDeployAgentSupervisorSystemd(exe, paths); err != nil {
		return err
	}
	if err := edgeRestartSystemd(paths); err != nil {
		return err
	}
	if _, err := localagent.InstallDeployResumeSystemd(exe, paths); err != nil {
		return err
	}
	resp := deploySetupResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.setup"),
		RegistryPath:       paths.DeployPath,
		ACME: deployACMEStatus{
			Email: registry.ACMEEmail,
			CA:    firstNonEmpty(registry.ACMECA, "production"),
		},
		ServiceManager:           "systemd",
		AgentSupervisorInstalled: true,
		EdgeRestarted:            true,
	}
	if opts.JSON {
		return writeCLIJSON(stdout, resp)
	}
	fmt.Fprintf(stdout, "configured public deploy edge under systemd (%s CA)\n", resp.ACME.CA)
	return nil
}

// installDeployAgentSupervisorSystemd hands the running agent over to
// systemd ownership: any unsupervised agent is stopped first so the
// Restart=always unit acquires the agent lock instead of crash-looping
// against it.
func installDeployAgentSupervisorSystemd(exe string, paths localagent.Paths) error {
	if deployExecutableIsHarness(exe) {
		return fmt.Errorf("refusing to install scenery agent systemd unit from harness binary %s", exe)
	}
	client := localagent.NewClient(paths.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	health, running := currentAgentHealth(ctx, client)
	logOffset := fileSize(paths.LogPath)
	if running && health.PID > 0 && !localagent.AgentSystemdStatusForSocket(paths.SocketPath).Running {
		if err := signalAgentPID(health.PID); err != nil {
			return fmt.Errorf("stop scenery agent pid %d: %w", health.PID, err)
		}
		if err := waitForAgentStop(ctx, client, health.PID); err != nil {
			return err
		}
	}
	if _, err := localagent.InstallAgentSystemd(exe, paths, localagent.StartOptions{RouterHTTP: true, RouterAddr: localagent.RouterAddrFromEnv()}); err != nil {
		return err
	}
	if _, err := waitForAgentStart(ctx, client, health.PID, paths.LogPath, logOffset); err != nil {
		return fmt.Errorf("supervised scenery agent did not become ready after systemd start: %w", err)
	}
	return nil
}

// runDeployTeardownLinux removes the Scenery-owned units and stops public
// serving. Published deploy artifacts and ACME state stay under the agent
// home; teardown never deletes app data.
func runDeployTeardownLinux(stdout io.Writer, opts deployOptions) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery deploy teardown on Linux must run as root to remove systemd units")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	edgeRemoved, err := edge.RemoveSystemdEdgeService()
	if err != nil {
		return err
	}
	agentRemoved, err := localagent.RemoveAgentSystemd()
	if err != nil {
		return err
	}
	resp := deployTeardownResponse{
		cliPayloadIdentity:     newCLIPayloadIdentity("scenery.deploy.teardown"),
		RegistryPath:           paths.DeployPath,
		ServiceManager:         "systemd",
		AgentSupervisorRemoved: agentRemoved,
		EdgeRestarted:          edgeRemoved,
	}
	if opts.JSON {
		return writeCLIJSON(stdout, resp)
	}
	fmt.Fprintln(stdout, "disabled the public deploy edge and removed Scenery systemd units")
	return nil
}

// deployPreflightPublicPortsLinux allows re-runs while our own managed Caddy
// holds 80/443 but refuses to fight a foreign server for the ports.
func deployPreflightPublicPortsLinux() error {
	unit := edge.SystemdEdgeStatus()
	for _, port := range []int{80, 443} {
		listener, ok, err := deployPortListenerFunc(port)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if unit.PID > 0 && listener.PID == unit.PID {
			continue
		}
		if strings.Contains(strings.ToLower(listener.Command), "caddy") && unit.Installed {
			continue
		}
		return fmt.Errorf("port %d is already in use by %s; stop it before running scenery deploy setup", port, listener.String())
	}
	return nil
}

// edgeRestartSystemd renders and validates the registry Caddyfile, then
// converges the scenery-edge unit onto it and records edge state so reload
// and status paths address the systemd-owned Caddy through its admin socket.
func edgeRestartSystemd(paths localagent.Paths) error {
	ctx := context.Background()
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	caddyBin, err := resolveCaddyBinary(ctx, paths, true)
	if err != nil {
		return err
	}
	token, err := ensureEdgeToken(paths.EdgeTokenPath)
	if err != nil {
		return err
	}
	targetAddr := defaultEdgeTargetAddr
	httpTargetAddr := defaultEdgeHTTPTargetAddr
	upstreamAddr := localagent.RouterAddrFromEnv()
	adminSocket := filepath.Join(paths.RunDir, "caddy-admin.sock")
	config, err := edge.CaddyConfigForRegistry(paths, targetAddr, httpTargetAddr, upstreamAddr, adminSocket, token)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.EdgeConfigPath), 0o700); err != nil {
		return err
	}
	candidate := paths.EdgeConfigPath + ".next"
	if err := os.WriteFile(candidate, []byte(config), 0o600); err != nil {
		return err
	}
	if err := edge.ValidateCaddyConfig(caddyBin, candidate); err != nil {
		_ = os.Remove(candidate)
		return err
	}
	if err := os.Rename(candidate, paths.EdgeConfigPath); err != nil {
		return err
	}
	if err := edge.InstallSystemdEdgeService(caddyBin, paths, adminSocket); err != nil {
		return err
	}
	status := edge.SystemdEdgeStatus()
	deadline := time.Now().Add(5 * time.Second)
	for !status.Active && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		status = edge.SystemdEdgeStatus()
	}
	if !status.Active {
		return fmt.Errorf("scenery-edge systemd unit did not become active; inspect `journalctl -u %s`", edge.SystemdEdgeUnitName)
	}
	state := localagent.EdgeState{
		Kind:         localagent.EdgeKindCaddy,
		Status:       localagent.EdgeStatusRunning,
		PID:          status.PID,
		PublicAddr:   "0.0.0.0:443",
		PublicScheme: "https",
		HTTPSListen:  targetAddr,
		UpstreamAddr: upstreamAddr,
		AdminSocket:  adminSocket,
		ConfigPath:   paths.EdgeConfigPath,
		LogPath:      paths.EdgeLogPath,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := localagent.WriteEdgeState(paths.EdgeStatePath, state); err != nil {
		return err
	}
	return ensureEdgeAgent(upstreamAddr, false)
}

// deployTargetFrontendStatuses reports serving-mode truth for one target's
// published production frontends.
func deployTargetFrontendStatuses(target localagent.DeployTarget) []deployTargetFrontendStatus {
	out := make([]deployTargetFrontendStatus, 0, len(target.Frontends))
	for _, frontend := range target.Frontends {
		item := deployTargetFrontendStatus{
			Name:         frontend.Name,
			Route:        "/" + frontend.Name + "/",
			Mode:         "agent_proxy",
			ArtifactPath: frontend.Path,
			ReleaseID:    frontend.ReleaseID,
		}
		if frontend.Root {
			item.Route = "/"
		}
		if releaseDir, entryPresent, err := edge.CurrentPublishedRelease(frontend.Path); err == nil && entryPresent {
			item.Mode = "caddy_static"
			item.EntryDocument = true
			item.ReleaseID = filepath.Base(releaseDir)
		}
		out = append(out, item)
	}
	return out
}

// deploySystemdSnapshotOverlay fills the systemd-specific diagnostics inputs.
func deploySystemdSnapshotOverlay(snapshot *deploydiag.Snapshot) {
	snapshot.ServiceManager = "systemd"
	snapshot.EdgeUnitActive = edge.SystemdEdgeStatus().Active
	// The direct edge terminates public TLS on 443 rather than behind the
	// macOS loopback forwarder.
	snapshot.EdgeHTTPSListen = "127.0.0.1:443"
}
