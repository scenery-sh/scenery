package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/deploydiag"
)

func stubDeployDiagnostics(t *testing.T, overrides func()) {
	t.Helper()
	oldListener := deployPortListenerFunc
	oldLANIP := deployLANIPFunc
	oldHTTPProbe := deployHTTPProbeFunc
	oldPublicIP := deployPublicIPFunc
	oldDNS := deployDNSLookupFunc
	oldPower := deployPowerStatusFunc
	oldFirewall := deployFirewallStatusFunc
	oldTLSProbe := edgeTLSProbeFunc
	t.Cleanup(func() {
		deployPortListenerFunc = oldListener
		deployLANIPFunc = oldLANIP
		deployHTTPProbeFunc = oldHTTPProbe
		deployPublicIPFunc = oldPublicIP
		deployDNSLookupFunc = oldDNS
		deployPowerStatusFunc = oldPower
		deployFirewallStatusFunc = oldFirewall
		edgeTLSProbeFunc = oldTLSProbe
	})
	edgeTLSProbeFunc = func(addr, serverName string, timeout time.Duration) edgeTLSProbeResult {
		return edgeTLSProbeResult{Outcome: edgeTLSProbeHandshakeOK}
	}
	deployPortListenerFunc = func(port int) (deploydiag.PortListenerInfo, bool, error) {
		return deploydiag.PortListenerInfo{
			Port:    port,
			PID:     123,
			Command: "/usr/local/libexec/scenery-edge-helper",
			Name:    fmt.Sprintf("TCP *:%d (LISTEN)", port),
		}, true, nil
	}
	deployLANIPFunc = func(ctx context.Context) (string, error) { return "192.168.1.20", nil }
	deployHTTPProbeFunc = func(ctx context.Context, url string) deploydiag.HTTPProbeResult {
		return deploydiag.HTTPProbeResult{OK: true, StatusCode: 200}
	}
	deployPublicIPFunc = func(ctx context.Context) (string, error) { return "203.0.113.10", nil }
	deployDNSLookupFunc = func(domain string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil }
	deployPowerStatusFunc = func(ctx context.Context) (deploydiag.PowerStatus, error) {
		return deploydiag.PowerStatus{Supported: true, SleepMinutes: 0, Raw: "sleep 0"}, nil
	}
	deployFirewallStatusFunc = func(ctx context.Context) (deploydiag.FirewallStatus, error) {
		return deploydiag.FirewallStatus{Supported: true, Enabled: false, Raw: "Firewall is disabled"}, nil
	}
	if overrides != nil {
		overrides()
	}
}

func deployChecksByID(checks []deploydiag.Check) map[string]deploydiag.Check {
	out := map[string]deploydiag.Check{}
	for _, check := range checks {
		out[check.ID] = check
	}
	return out
}

func TestDeployEnableDisableAndConflict(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	oldRefresh := deployRefreshEdgeAfterMutationFunc
	t.Cleanup(func() { deployRefreshEdgeAfterMutationFunc = oldRefresh })
	refreshes := 0
	deployRefreshEdgeAfterMutationFunc = func(paths localagent.Paths) error {
		refreshes++
		return nil
	}
	appRoot := writeDeployTestApp(t, "app-a", "onlv.dev", "web")

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"enable", "--app-root", appRoot, "-o", "json"}); err != nil {
		t.Fatalf("deploy enable: %v\n%s", err, out.String())
	}
	var payload deployMutationResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Action != "enable" || len(payload.Targets) != 1 || !payload.Targets[0].Enabled || payload.Targets[0].RootService != "web" {
		t.Fatalf("enable payload = %+v", payload)
	}
	if refreshes != 1 {
		t.Fatalf("refresh count after enable = %d, want 1", refreshes)
	}

	otherRoot := writeDeployTestApp(t, "app-b", "onlv.dev", "web")
	err := runDeployCommand(&bytes.Buffer{}, []string{"enable", "--app-root", otherRoot, "-o", "json"})
	if err == nil || !strings.Contains(err.Error(), "already enabled") {
		t.Fatalf("conflict error = %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh count after conflict = %d, want 1", refreshes)
	}

	out.Reset()
	if err := runDeployCommand(&out, []string{"disable", "--app-root", appRoot, "-o", "json"}); err != nil {
		t.Fatalf("deploy disable: %v\n%s", err, out.String())
	}
	payload = deployMutationResponse{}
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON disable: %v\n%s", err, out.String())
	}
	if len(payload.Targets) != 1 || payload.Targets[0].Enabled {
		t.Fatalf("disable payload = %+v", payload)
	}
	if refreshes != 2 {
		t.Fatalf("refresh count after disable = %d, want 2", refreshes)
	}
}

func TestDeployStatusReportsRegistryTargetsAndLiveSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	stubDeployDiagnostics(t, nil)
	appRoot := t.TempDir()
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	registry := localagent.EmptyDeployRegistry()
	registry.Targets = []localagent.DeployTarget{{
		Domain:      "onlv.dev",
		AppRoot:     appRoot,
		RootService: "web",
		Enabled:     true,
	}}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatalf("WriteDeployRegistry: %v", err)
	}
	sessions, err := localagent.OpenRegistry(paths.RegistryPath, localagent.RouterAddrFromEnv())
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	if _, err := sessions.Upsert(localagent.RegisterRequest{
		BaseAppID: "deployapp",
		AppRoot:   appRoot,
		SessionID: "deployapp",
		Status:    "running",
	}); err != nil {
		t.Fatalf("Upsert session: %v", err)
	}
	certPath := filepath.Join(paths.EdgeDir, "caddy-data", "certificates", "acme-staging-v02.api.letsencrypt.org-directory", "onlv.dev", "onlv.dev.crt")
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte("fake cert"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"status", "-o", "json"}); err != nil {
		t.Fatalf("deploy status: %v\n%s", err, out.String())
	}
	var status deployStatusResponse
	if err := decodeCLIJSON(out.Bytes(), &status); err != nil {
		t.Fatalf("decodeCLIJSON status: %v\n%s", err, out.String())
	}
	if status.Kind != "scenery.deploy.status" || status.SchemaRevision != newCLIPayloadIdentity("scenery.deploy.status").SchemaRevision || status.RegistryPath != paths.DeployPath {
		t.Fatalf("status metadata = %+v", status)
	}
	if len(status.Targets) != 1 || status.Targets[0].Domain != "onlv.dev" || !status.Targets[0].LiveSession || status.Targets[0].SessionID != "deployapp" || !status.Targets[0].CertPresent {
		t.Fatalf("status targets = %+v", status.Targets)
	}
}

func TestDeployStatusDiagnosticsReportReachabilityDNSPowerAndFirewall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	stubDeployDiagnostics(t, func() {
		deployHTTPProbeFunc = func(ctx context.Context, url string) deploydiag.HTTPProbeResult {
			if strings.Contains(url, "192.168.1.20") {
				return deploydiag.HTTPProbeResult{OK: true, StatusCode: 200}
			}
			return deploydiag.HTTPProbeResult{Error: "connection refused"}
		}
		deployDNSLookupFunc = func(domain string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("198.51.100.25")}, nil
		}
		deployPowerStatusFunc = func(ctx context.Context) (deploydiag.PowerStatus, error) {
			return deploydiag.PowerStatus{Supported: true, SleepMinutes: 15, Raw: "sleep 15"}, nil
		}
		deployFirewallStatusFunc = func(ctx context.Context) (deploydiag.FirewallStatus, error) {
			return deploydiag.FirewallStatus{Supported: true, Enabled: true, Raw: "Firewall is enabled"}, nil
		}
	})
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	registry := localagent.EmptyDeployRegistry()
	registry.Targets = []localagent.DeployTarget{{
		Domain:  "onlv.dev",
		AppRoot: t.TempDir(),
		Enabled: true,
	}}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatalf("WriteDeployRegistry: %v", err)
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"status", "-o", "json"}); err != nil {
		t.Fatalf("deploy status: %v\n%s", err, out.String())
	}
	var status deployStatusResponse
	if err := decodeCLIJSON(out.Bytes(), &status); err != nil {
		t.Fatalf("decodeCLIJSON status: %v\n%s", err, out.String())
	}
	if status.DiagnosticsDetail == nil || status.DiagnosticsDetail.LANIP != "192.168.1.20" || status.DiagnosticsDetail.PublicIP != "203.0.113.10" {
		t.Fatalf("diagnostics detail = %+v", status.DiagnosticsDetail)
	}
	checks := deployChecksByID(status.DiagnosticsDetail.Checks)
	for _, id := range []string{
		"deploy.reachability.public",
		"deploy.dns.onlv.dev",
		"deploy.power.sleep",
		"deploy.firewall",
		"deploy.cert.onlv.dev",
	} {
		if checks[id].Status != "warn" {
			t.Fatalf("%s = %+v, want warn", id, checks[id])
		}
	}
	if !strings.Contains(checks["deploy.dns.onlv.dev"].SuggestedAction, "203.0.113.10") {
		t.Fatalf("dns suggested action = %q", checks["deploy.dns.onlv.dev"].SuggestedAction)
	}
}

func TestDeploySetupWritesRegistryInstallsHelperAndRestartsEdge(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("deploy setup is macOS-only")
	}
	if os.Geteuid() == 0 {
		t.Skip("deploy setup refuses to run as root")
	}
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	oldPreflight := deploySetupPreflightFunc
	oldInstall := deployPrivilegedHelperInstallFunc
	oldInstallLaunchAgent := deployInstallResumeLaunchAgentFunc
	oldInstallSupervisor := deployInstallAgentSupervisorFunc
	oldRestart := deployEdgeRestartFunc
	oldReinstallNeeded := deployHelperReinstallNeededFunc
	t.Cleanup(func() {
		deploySetupPreflightFunc = oldPreflight
		deployPrivilegedHelperInstallFunc = oldInstall
		deployInstallResumeLaunchAgentFunc = oldInstallLaunchAgent
		deployInstallAgentSupervisorFunc = oldInstallSupervisor
		deployEdgeRestartFunc = oldRestart
		deployHelperReinstallNeededFunc = oldReinstallNeeded
	})
	deployHelperReinstallNeededFunc = func(paths localagent.Paths, currentVersion string) bool { return true }

	preflighted := false
	installed := false
	launchAgentInstalled := false
	supervisorInstalled := false
	restarted := false
	var installedVersion string
	deploySetupPreflightFunc = func(paths localagent.Paths) error {
		preflighted = true
		return nil
	}
	deployPrivilegedHelperInstallFunc = func(paths localagent.Paths, helperVersion string) error {
		installed = true
		installedVersion = helperVersion
		return nil
	}
	deployInstallResumeLaunchAgentFunc = func(paths localagent.Paths) error {
		launchAgentInstalled = true
		return nil
	}
	deployInstallAgentSupervisorFunc = func(paths localagent.Paths) error {
		supervisorInstalled = true
		return nil
	}
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	// Recovery from a broken helper must never delete certificates, deploy
	// targets, or app data: pre-seed all three and prove setup keeps them.
	seedPaths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	seedRegistry := localagent.EmptyDeployRegistry()
	seedRegistry.Targets = []localagent.DeployTarget{{Domain: "onlv.dev", AppRoot: t.TempDir(), Enabled: true}}
	if err := localagent.WriteDeployRegistry(seedPaths.DeployPath, seedRegistry); err != nil {
		t.Fatalf("WriteDeployRegistry: %v", err)
	}
	certPath := filepath.Join(seedPaths.EdgeDir, "caddy-data", "certificates", "acme-v02.api.letsencrypt.org-directory", "onlv.dev", "onlv.dev.crt")
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte("issued cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(seedPaths.EdgeTargetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := localagent.WriteEdgeTargetState(seedPaths.EdgeTargetPath, localagent.EdgeTargetState{
		Kind:       localagent.EdgeKindCaddy,
		TargetAddr: "127.0.0.1:19443",
	}); err != nil {
		t.Fatalf("WriteEdgeTargetState: %v", err)
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"setup", "--acme-email", "ops@example.com", "--acme-ca", "staging", "-o", "json"}); err != nil {
		t.Fatalf("deploy setup: %v\n%s", err, out.String())
	}
	if !preflighted || !installed || !launchAgentInstalled || !supervisorInstalled || !restarted || installedVersion == "" {
		t.Fatalf("setup hooks preflight=%v install=%v launchAgent=%v supervisor=%v restart=%v version=%q", preflighted, installed, launchAgentInstalled, supervisorInstalled, restarted, installedVersion)
	}
	var payload deploySetupResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON setup: %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.deploy.setup" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.deploy.setup").SchemaRevision || payload.ACME.Email != "ops@example.com" || payload.ACME.CA != "staging" || !payload.HelperPublic || !payload.HelperReinstalled || !payload.AgentSupervisorInstalled || !payload.LaunchAgentInstalled || !payload.EdgeRestarted {
		t.Fatalf("setup payload = %+v", payload)
	}
	registry, err := localagent.LoadDeployRegistry(seedPaths.DeployPath)
	if err != nil {
		t.Fatalf("LoadDeployRegistry: %v", err)
	}
	if registry.ACMEEmail != "ops@example.com" || registry.ACMECA != "staging" {
		t.Fatalf("registry ACME = %+v", registry)
	}
	if len(registry.Targets) != 1 || registry.Targets[0].Domain != "onlv.dev" || !registry.Targets[0].Enabled {
		t.Fatalf("setup dropped deploy targets: %+v", registry.Targets)
	}
	if data, err := os.ReadFile(certPath); err != nil || string(data) != "issued cert" {
		t.Fatalf("setup touched issued certificate: %q, %v", data, err)
	}
	if _, err := localagent.LoadEdgeTargetState(seedPaths.EdgeTargetPath); err != nil {
		t.Fatalf("setup broke edge target metadata: %v", err)
	}
}

func TestDeployTeardownInstallsLoopbackHelperRemovesLaunchAgentAndRestartsEdge(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("deploy teardown is macOS-only")
	}
	if os.Geteuid() == 0 {
		t.Skip("deploy teardown refuses to run as root")
	}
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	oldInstall := deployLoopbackHelperInstallFunc
	oldRemove := deployRemoveResumeLaunchAgentFunc
	oldRemoveSupervisor := deployRemoveAgentSupervisorFunc
	oldRestart := deployEdgeRestartFunc
	t.Cleanup(func() {
		deployLoopbackHelperInstallFunc = oldInstall
		deployRemoveResumeLaunchAgentFunc = oldRemove
		deployRemoveAgentSupervisorFunc = oldRemoveSupervisor
		deployEdgeRestartFunc = oldRestart
	})

	installed := false
	removed := false
	supervisorRemoved := false
	restarted := false
	var installedVersion string
	deployLoopbackHelperInstallFunc = func(paths localagent.Paths, helperVersion string) error {
		installed = true
		installedVersion = helperVersion
		return nil
	}
	deployRemoveResumeLaunchAgentFunc = func() (bool, error) {
		removed = true
		return true, nil
	}
	deployRemoveAgentSupervisorFunc = func() (bool, error) {
		supervisorRemoved = true
		return true, nil
	}
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"teardown", "-o", "json"}); err != nil {
		t.Fatalf("deploy teardown: %v\n%s", err, out.String())
	}
	if !installed || !removed || !supervisorRemoved || !restarted || installedVersion == "" {
		t.Fatalf("teardown hooks install=%v remove=%v supervisorRemove=%v restart=%v version=%q", installed, removed, supervisorRemoved, restarted, installedVersion)
	}
	var payload deployTeardownResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON teardown: %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.deploy.teardown" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.deploy.teardown").SchemaRevision || payload.HelperPublic || !payload.LaunchAgentRemoved || !payload.AgentSupervisorRemoved || !payload.EdgeRestarted {
		t.Fatalf("teardown payload = %+v", payload)
	}
}

func TestDeployResumeStartsMissingTargetsAndSkipsLiveSessions(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	oldEnsureAgent := deployEnsureAgentFunc
	oldRestart := deployEdgeRestartFunc
	oldRunUp := deployRunUpDetachFunc
	oldDrift := deployHelperDriftStatusFunc
	oldStatus := deployPublicEdgeStatusFunc
	oldDelay := deployPublicEdgeRetryDelay
	t.Cleanup(func() {
		deployEnsureAgentFunc = oldEnsureAgent
		deployEdgeRestartFunc = oldRestart
		deployRunUpDetachFunc = oldRunUp
		deployHelperDriftStatusFunc = oldDrift
		deployPublicEdgeStatusFunc = oldStatus
		deployPublicEdgeRetryDelay = oldDelay
	})
	deployPublicEdgeRetryDelay = 0
	deployPublicEdgeStatusFunc = func(localagent.Paths) (edgeStatusResult, error) {
		return edgeStatusResult{}, nil
	}
	deployEnsureAgentFunc = func() error { return nil }
	deployEdgeRestartFunc = func() error { return nil }
	// The installed helper predates the current handoff contract: resume
	// cannot sudo, so it must surface the drift instead of reporting a
	// quietly broken edge.
	deployHelperDriftStatusFunc = func(paths localagent.Paths) deploydiag.HelperDrift {
		return deploydiag.HelperDrift{
			HelperInstalled:  true,
			ActionRequired:   true,
			HelperContract:   "",
			ExpectedContract: localagent.EdgeHelperContractRevision,
			Message:          "installed privileged helper predates handoff contract " + localagent.EdgeHelperContractRevision,
			SuggestedAction:  "Run `scenery deploy setup` to update the privileged listener (asks for sudo).",
		}
	}
	var started []string
	deployRunUpDetachFunc = func(appRoot, envName string) error {
		started = append(started, appRoot)
		if envName != "" {
			started = append(started, "env:"+envName)
		}
		return nil
	}

	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	liveRoot := t.TempDir()
	startRoot := t.TempDir()
	missingRoot := filepath.Join(t.TempDir(), "missing")
	registry := localagent.EmptyDeployRegistry()
	registry.Targets = []localagent.DeployTarget{
		{Domain: "live.dev", AppRoot: liveRoot, Enabled: true},
		{Domain: "start.dev", AppRoot: startRoot, Enabled: true},
		{Domain: "missing.dev", AppRoot: missingRoot, Enabled: true},
		{Domain: "off.dev", AppRoot: t.TempDir(), Enabled: false},
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatalf("WriteDeployRegistry: %v", err)
	}
	sessions, err := localagent.OpenRegistry(paths.RegistryPath, localagent.RouterAddrFromEnv())
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	if _, err := sessions.Upsert(localagent.RegisterRequest{
		BaseAppID: "live",
		AppRoot:   liveRoot,
		SessionID: "live",
		Status:    "running",
	}); err != nil {
		t.Fatalf("Upsert session: %v", err)
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"resume", "-o", "json"}); err != nil {
		t.Fatalf("deploy resume: %v\n%s", err, out.String())
	}
	var payload deployResumeResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON resume: %v\n%s", err, out.String())
	}
	if !payload.AgentReady || !payload.EdgeRestarted || payload.LogPath != paths.DeployResumeLogPath {
		t.Fatalf("resume payload metadata = %+v", payload)
	}
	if payload.HelperDrift == nil || !payload.HelperDrift.ActionRequired || !strings.Contains(payload.HelperDrift.SuggestedAction, "deploy setup") {
		t.Fatalf("resume helper drift = %+v", payload.HelperDrift)
	}
	actions := map[string]string{}
	for _, target := range payload.Targets {
		actions[target.Domain] = target.Action
	}
	if actions["live.dev"] != "already_running" || actions["start.dev"] != "started" || actions["missing.dev"] != "missing" {
		t.Fatalf("resume actions = %+v", actions)
	}
	if len(started) != 1 || started[0] != startRoot {
		t.Fatalf("started = %+v, want %s", started, startRoot)
	}
	if _, err := os.Stat(paths.DeployResumeLogPath); err != nil {
		t.Fatalf("resume log missing: %v", err)
	}
}

func TestEnsurePublicDeployEdgeReusesHealthyManagedEdge(t *testing.T) {
	oldStatus := deployPublicEdgeStatusFunc
	oldRestart := deployEdgeRestartFunc
	oldDelay := deployPublicEdgeRetryDelay
	t.Cleanup(func() {
		deployPublicEdgeStatusFunc = oldStatus
		deployEdgeRestartFunc = oldRestart
		deployPublicEdgeRetryDelay = oldDelay
	})
	deployPublicEdgeRetryDelay = 0

	deployPublicEdgeStatusFunc = func(localagent.Paths) (edgeStatusResult, error) {
		return edgeStatusResult{
			Edge: edgeStatusCaddy{
				State:       localagent.EdgeStatusRunning,
				PID:         42,
				HTTPSListen: "127.0.0.1:19443",
				Upstream:    "127.0.0.1:9440",
				AgentRouter: "127.0.0.1:9440",
			},
			PrivilegedListener: edgeStatusPrivilegedListener{
				State:     "running",
				Target:    "127.0.0.1:19443",
				TargetPID: 42,
			},
		}, nil
	}
	restarted := false
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	didRestart, err := ensurePublicDeployEdge(localagent.Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if didRestart || restarted {
		t.Fatal("healthy public edge was reported or observed as restarted")
	}
}

func TestEnsurePublicDeployEdgeRestartsUnavailableEdge(t *testing.T) {
	oldStatus := deployPublicEdgeStatusFunc
	oldRestart := deployEdgeRestartFunc
	oldDelay := deployPublicEdgeRetryDelay
	t.Cleanup(func() {
		deployPublicEdgeStatusFunc = oldStatus
		deployEdgeRestartFunc = oldRestart
		deployPublicEdgeRetryDelay = oldDelay
	})
	deployPublicEdgeRetryDelay = 0

	deployPublicEdgeStatusFunc = func(localagent.Paths) (edgeStatusResult, error) {
		return edgeStatusResult{}, nil
	}
	restarted := false
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	didRestart, err := ensurePublicDeployEdge(localagent.Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if !didRestart || !restarted {
		t.Fatal("unavailable public edge was not restarted")
	}
}

func TestEnsurePublicDeployEdgeWaitsForTransientAgentHealth(t *testing.T) {
	oldStatus := deployPublicEdgeStatusFunc
	oldRestart := deployEdgeRestartFunc
	oldDelay := deployPublicEdgeRetryDelay
	t.Cleanup(func() {
		deployPublicEdgeStatusFunc = oldStatus
		deployEdgeRestartFunc = oldRestart
		deployPublicEdgeRetryDelay = oldDelay
	})
	deployPublicEdgeRetryDelay = 0

	checks := 0
	deployPublicEdgeStatusFunc = func(localagent.Paths) (edgeStatusResult, error) {
		checks++
		if checks == 1 {
			return edgeStatusResult{}, nil
		}
		return edgeStatusResult{
			Edge: edgeStatusCaddy{
				State:       localagent.EdgeStatusRunning,
				PID:         42,
				HTTPSListen: "127.0.0.1:19443",
				Upstream:    "127.0.0.1:9440",
				AgentRouter: "127.0.0.1:9440",
			},
			PrivilegedListener: edgeStatusPrivilegedListener{
				State:     "running",
				Target:    "127.0.0.1:19443",
				TargetPID: 42,
			},
		}, nil
	}
	restarted := false
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	didRestart, err := ensurePublicDeployEdge(localagent.Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if checks != 2 || didRestart || restarted {
		t.Fatalf("checks=%d reported_restart=%v restarted=%v, want bounded reacquisition without restart", checks, didRestart, restarted)
	}
}

func TestDeployResumeReacquiresHealthyEdgeWithoutRestart(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	oldEnsureAgent := deployEnsureAgentFunc
	oldStatus := deployPublicEdgeStatusFunc
	oldRestart := deployEdgeRestartFunc
	oldDrift := deployHelperDriftStatusFunc
	oldDelay := deployPublicEdgeRetryDelay
	t.Cleanup(func() {
		deployEnsureAgentFunc = oldEnsureAgent
		deployPublicEdgeStatusFunc = oldStatus
		deployEdgeRestartFunc = oldRestart
		deployHelperDriftStatusFunc = oldDrift
		deployPublicEdgeRetryDelay = oldDelay
	})
	deployPublicEdgeRetryDelay = 0
	deployEnsureAgentFunc = func() error { return nil }
	deployHelperDriftStatusFunc = func(localagent.Paths) deploydiag.HelperDrift { return deploydiag.HelperDrift{} }
	deployPublicEdgeStatusFunc = func(localagent.Paths) (edgeStatusResult, error) {
		return edgeStatusResult{
			Edge: edgeStatusCaddy{
				State:       localagent.EdgeStatusRunning,
				PID:         42,
				HTTPSListen: "127.0.0.1:19443",
				Upstream:    "127.0.0.1:9440",
				AgentRouter: "127.0.0.1:9440",
			},
			PrivilegedListener: edgeStatusPrivilegedListener{
				State:     "running",
				Target:    "127.0.0.1:19443",
				TargetPID: 42,
			},
		}, nil
	}
	restarted := false
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	var out bytes.Buffer
	if err := runDeployResume(&out, deployOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Fatal("deploy resume restarted a healthy public edge")
	}
	var payload deployResumeResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.AgentReady || payload.EdgeRestarted {
		t.Fatalf("resume payload = %+v", payload)
	}
}

func TestDeployResumeLaunchAgentPlist(t *testing.T) {
	plist := deployResumeLaunchAgentPlist("/usr/local/bin/scenery", "/tmp/deploy-resume.log")
	for _, want := range []string{
		"<string>dev.scenery.deploy-resume</string>",
		"<string>/usr/local/bin/scenery</string>",
		"<string>deploy</string>",
		"<string>resume</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<false/>",
		"<string>/tmp/deploy-resume.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if !deployExecutableIsHarness(filepath.Join(t.TempDir(), ".scenery", "harness", "bin", "scenery")) {
		t.Fatal("expected harness binary path to be rejected")
	}
}

func TestDeployPreflightRejectsNonSceneryPortListener(t *testing.T) {
	oldListener := deployPortListenerFunc
	t.Cleanup(func() { deployPortListenerFunc = oldListener })
	paths := localagent.PathsForHome(t.TempDir())
	deployPortListenerFunc = func(port int) (deploydiag.PortListenerInfo, bool, error) {
		if port == 80 {
			return deploydiag.PortListenerInfo{Port: port, PID: 123, Command: "/usr/sbin/httpd"}, true, nil
		}
		return deploydiag.PortListenerInfo{}, false, nil
	}
	err := deployPreflightPublicPorts(paths)
	if err == nil || !strings.Contains(err.Error(), "port 80 is already in use") {
		t.Fatalf("preflight err = %v", err)
	}

	deployPortListenerFunc = func(port int) (deploydiag.PortListenerInfo, bool, error) {
		return deploydiag.PortListenerInfo{Port: port, PID: 456, Command: "/usr/local/libexec/scenery-edge-helper"}, true, nil
	}
	if err := deployPreflightPublicPorts(paths); err != nil {
		t.Fatalf("preflight should allow existing Scenery helper: %v", err)
	}
}

func TestDeployRefreshEdgeAfterMutationSkipsWhenSetupAbsent(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	if err := deployRefreshEdgeAfterMutation(paths); err != nil {
		t.Fatalf("deployRefreshEdgeAfterMutation without setup: %v", err)
	}
}

func TestDeployPrivilegedHelperInstallArgsUsePublicHelper(t *testing.T) {
	paths := localagent.PathsForHome(filepath.Join(t.TempDir(), "agent-home"))
	args := deployPrivilegedHelperInstallArgs("/bin/scenery", paths, "1.2.3")
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"/bin/scenery",
		"system",
		"edge",
		"privileged-helper",
		"install",
		"--public",
		"--helper-version",
		"1.2.3",
		"--owner-home",
		paths.Home,
		"--helper-target-state",
		paths.EdgeTargetPath,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("install args missing %q: %#v", want, args)
		}
	}
}

func TestDeployLoopbackHelperInstallArgsDoNotUsePublicFlag(t *testing.T) {
	paths := localagent.PathsForHome(filepath.Join(t.TempDir(), "agent-home"))
	args := deployLoopbackHelperInstallArgs("/bin/scenery", paths, "1.2.3")
	joined := strings.Join(args, "\n")
	if strings.Contains(joined, "--public") {
		t.Fatalf("loopback install args should not include --public: %#v", args)
	}
	for _, want := range []string{
		"/bin/scenery",
		"system",
		"edge",
		"privileged-helper",
		"install",
		"--helper-version",
		"1.2.3",
		"--helper-target-state",
		paths.EdgeTargetPath,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("loopback install args missing %q: %#v", want, args)
		}
	}
}

func writeDeployTestApp(t *testing.T, name, domain, frontend string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "`+name+`",
		"frontends": {
			"`+frontend+`": { "root": "`+frontend+`" }
		},
		"envs": {
			"local": {"default": true, "frontends": {"`+frontend+`": {"serve": "development"}}},
			"production": {
				"domain": "`+domain+`",
				"frontends": {"`+frontend+`": {"serve": "production"}},
				"deploy": {}
			}
		}
	}`)
	return root
}
