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

	localagent "scenery.sh/internal/agent"
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
	t.Cleanup(func() {
		deployPortListenerFunc = oldListener
		deployLANIPFunc = oldLANIP
		deployHTTPProbeFunc = oldHTTPProbe
		deployPublicIPFunc = oldPublicIP
		deployDNSLookupFunc = oldDNS
		deployPowerStatusFunc = oldPower
		deployFirewallStatusFunc = oldFirewall
	})
	deployPortListenerFunc = func(port int) (deployPortListenerInfo, bool, error) {
		return deployPortListenerInfo{
			Port:    port,
			PID:     123,
			Command: "/usr/local/libexec/scenery-edge-helper",
			Name:    fmt.Sprintf("TCP *:%d (LISTEN)", port),
		}, true, nil
	}
	deployLANIPFunc = func(ctx context.Context) (string, error) { return "192.168.1.20", nil }
	deployHTTPProbeFunc = func(ctx context.Context, url string) deployHTTPProbeResult {
		return deployHTTPProbeResult{OK: true, StatusCode: 200}
	}
	deployPublicIPFunc = func(ctx context.Context) (string, error) { return "203.0.113.10", nil }
	deployDNSLookupFunc = func(domain string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil }
	deployPowerStatusFunc = func(ctx context.Context) (deployPowerStatus, error) {
		return deployPowerStatus{Supported: true, SleepMinutes: 0, Raw: "sleep 0"}, nil
	}
	deployFirewallStatusFunc = func(ctx context.Context) (deployFirewallStatus, error) {
		return deployFirewallStatus{Supported: true, Enabled: false, Raw: "Firewall is disabled"}, nil
	}
	if overrides != nil {
		overrides()
	}
}

func deployChecksByID(checks []deployDiagnosticCheck) map[string]deployDiagnosticCheck {
	out := map[string]deployDiagnosticCheck{}
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
	if status.SchemaVersion != "scenery.deploy.status.v1" || status.RegistryPath != paths.DeployPath {
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
		deployHTTPProbeFunc = func(ctx context.Context, url string) deployHTTPProbeResult {
			if strings.Contains(url, "192.168.1.20") {
				return deployHTTPProbeResult{OK: true, StatusCode: 200}
			}
			return deployHTTPProbeResult{Error: "connection refused"}
		}
		deployDNSLookupFunc = func(domain string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("198.51.100.25")}, nil
		}
		deployPowerStatusFunc = func(ctx context.Context) (deployPowerStatus, error) {
			return deployPowerStatus{Supported: true, SleepMinutes: 15, Raw: "sleep 15"}, nil
		}
		deployFirewallStatusFunc = func(ctx context.Context) (deployFirewallStatus, error) {
			return deployFirewallStatus{Supported: true, Enabled: true, Raw: "Firewall is enabled"}, nil
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

func TestDeployDNSDiagnosticsAllowsCloudflareProxy(t *testing.T) {
	oldDNS := deployDNSLookupFunc
	t.Cleanup(func() { deployDNSLookupFunc = oldDNS })
	deployDNSLookupFunc = func(domain string) ([]net.IP, error) {
		return []net.IP{
			net.ParseIP("104.21.1.153"),
			net.ParseIP("172.67.129.115"),
			net.ParseIP("2606:4700:3030::6815:199"),
		}, nil
	}
	report := deployDiagnosticReport{PublicIP: "217.112.163.198"}
	addDeployDNSDiagnostics(&report, localagent.DeployRegistry{
		Targets: []localagent.DeployTarget{{
			Domain:  "local.clean.tech",
			AppRoot: t.TempDir(),
			Enabled: true,
		}},
	})
	check := deployChecksByID(report.Checks)["deploy.dns.local.clean.tech"]
	if check.Status != "ok" || !strings.Contains(check.Message, "Cloudflare proxy IPs") {
		t.Fatalf("cloudflare DNS check = %+v", check)
	}
	if check.Observed["cloudflare_proxy"] != true {
		t.Fatalf("cloudflare proxy observation = %+v", check.Observed)
	}
}

func TestParseDeployNetstatPortListenerDualStack(t *testing.T) {
	t.Parallel()

	output := `tcp46      0      0  *.443                  *.*                    LISTEN                 0            0  131072  131072 scenery-edge-hel:10536  00180 00000006 000000000066c994 00000000 00000800      1      0 000000`
	info, ok := parseDeployNetstatPortListener(output, 443)
	if !ok || info.PID != 10536 || info.Command != "scenery-edge-hel" || info.Name != "*.443" {
		t.Fatalf("netstat listener = %+v, %v", info, ok)
	}
}

func TestDeployRawIPHTTPSNeedsSNISkipsTLSInternalError(t *testing.T) {
	t.Parallel()

	if !deployRawIPHTTPSNeedsSNI("https://217.112.163.198/", "remote error: tls: internal error") {
		t.Fatal("expected raw-IP HTTPS TLS internal error to be skipped")
	}
	if deployRawIPHTTPSNeedsSNI("https://local.clean.tech/", "remote error: tls: internal error") {
		t.Fatal("domain HTTPS errors should not be skipped")
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
	oldRestart := deployEdgeRestartFunc
	t.Cleanup(func() {
		deploySetupPreflightFunc = oldPreflight
		deployPrivilegedHelperInstallFunc = oldInstall
		deployInstallResumeLaunchAgentFunc = oldInstallLaunchAgent
		deployEdgeRestartFunc = oldRestart
	})

	preflighted := false
	installed := false
	launchAgentInstalled := false
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
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"setup", "--acme-email", "ops@example.com", "--acme-ca", "staging", "-o", "json"}); err != nil {
		t.Fatalf("deploy setup: %v\n%s", err, out.String())
	}
	if !preflighted || !installed || !launchAgentInstalled || !restarted || installedVersion == "" {
		t.Fatalf("setup hooks preflight=%v install=%v launchAgent=%v restart=%v version=%q", preflighted, installed, launchAgentInstalled, restarted, installedVersion)
	}
	var payload deploySetupResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON setup: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.deploy.setup.v1" || payload.ACME.Email != "ops@example.com" || payload.ACME.CA != "staging" || !payload.HelperPublic || !payload.LaunchAgentInstalled || !payload.EdgeRestarted {
		t.Fatalf("setup payload = %+v", payload)
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		t.Fatalf("LoadDeployRegistry: %v", err)
	}
	if registry.ACMEEmail != "ops@example.com" || registry.ACMECA != "staging" {
		t.Fatalf("registry ACME = %+v", registry)
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
	oldRestart := deployEdgeRestartFunc
	t.Cleanup(func() {
		deployLoopbackHelperInstallFunc = oldInstall
		deployRemoveResumeLaunchAgentFunc = oldRemove
		deployEdgeRestartFunc = oldRestart
	})

	installed := false
	removed := false
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
	deployEdgeRestartFunc = func() error {
		restarted = true
		return nil
	}

	var out bytes.Buffer
	if err := runDeployCommand(&out, []string{"teardown", "-o", "json"}); err != nil {
		t.Fatalf("deploy teardown: %v\n%s", err, out.String())
	}
	if !installed || !removed || !restarted || installedVersion == "" {
		t.Fatalf("teardown hooks install=%v remove=%v restart=%v version=%q", installed, removed, restarted, installedVersion)
	}
	var payload deployTeardownResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON teardown: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.deploy.teardown.v1" || payload.HelperPublic || !payload.LaunchAgentRemoved || !payload.EdgeRestarted {
		t.Fatalf("teardown payload = %+v", payload)
	}
}

func TestDeployResumeStartsMissingTargetsAndSkipsLiveSessions(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	oldEnsureAgent := deployEnsureAgentFunc
	oldRestart := deployEdgeRestartFunc
	oldRunUp := deployRunUpDetachFunc
	t.Cleanup(func() {
		deployEnsureAgentFunc = oldEnsureAgent
		deployEdgeRestartFunc = oldRestart
		deployRunUpDetachFunc = oldRunUp
	})
	deployEnsureAgentFunc = func() error { return nil }
	deployEdgeRestartFunc = func() error { return nil }
	var started []string
	deployRunUpDetachFunc = func(appRoot string) error {
		started = append(started, appRoot)
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
	deployPortListenerFunc = func(port int) (deployPortListenerInfo, bool, error) {
		if port == 80 {
			return deployPortListenerInfo{Port: port, PID: 123, Command: "/usr/sbin/httpd"}, true, nil
		}
		return deployPortListenerInfo{}, false, nil
	}
	err := deployPreflightPublicPorts(paths)
	if err == nil || !strings.Contains(err.Error(), "port 80 is already in use") {
		t.Fatalf("preflight err = %v", err)
	}

	deployPortListenerFunc = func(port int) (deployPortListenerInfo, bool, error) {
		return deployPortListenerInfo{Port: port, PID: 456, Command: "/usr/local/libexec/scenery-edge-helper"}, true, nil
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
		"deploy": { "domain": "`+domain+`" },
		"frontends": {
			"`+frontend+`": { "root": "`+frontend+`" }
		}
	}`)
	return root
}
