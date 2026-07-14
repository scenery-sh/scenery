package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestCaddyEdgeConfigUsesStableAgentRouterContract(t *testing.T) {
	t.Parallel()

	config := caddyEdgeConfig(caddyEdgeConfigOptions{
		ListenAddr:  defaultEdgeTargetAddr,
		PublicPort:  "443",
		Upstream:    "127.0.0.1:9440",
		AskURL:      "http://127.0.0.1:9440/v1/tls/allow",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
	})
	for _, want := range []string{
		"default_bind 127.0.0.1",
		"auto_https disable_redirects",
		"local_certs",
		"ask http://127.0.0.1:9440/v1/tls/allow",
		"admin unix///tmp/scenery-caddy.sock",
		"strict_sni_host on",
		"https://:19443 {",
		"reverse_proxy 127.0.0.1:9440",
		"flush_interval -1",
		"lb_try_duration 5s",
		"lb_try_interval 250ms",
		"header_up Host {host}",
		"header_up X-Forwarded-Proto https",
		"header_up X-Forwarded-Port 443",
		"header_up X-Scenery-Edge-Token secret-token",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("Caddy config missing %q:\n%s", want, config)
		}
	}
}

func TestCaddyEdgeConfigUsesPrivateListenPortAndPublicForwardedPort(t *testing.T) {
	t.Parallel()

	config := caddyEdgeConfig(caddyEdgeConfigOptions{
		ListenAddr:  "127.0.0.1:19555",
		PublicPort:  "443",
		Upstream:    "127.0.0.1:9440",
		AskURL:      "http://127.0.0.1:9440/v1/tls/allow",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
	})
	for _, want := range []string{
		"default_bind 127.0.0.1",
		"https://:19555 {",
		"header_up X-Forwarded-Port 443",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("private listener config missing %q:\n%s", want, config)
		}
	}
}

func TestCaddyEdgeConfigAddsPublicACMESites(t *testing.T) {
	t.Parallel()

	config := caddyEdgeConfig(caddyEdgeConfigOptions{
		ListenAddr:     "127.0.0.1:19443",
		PublicPort:     "443",
		Upstream:       "127.0.0.1:9440",
		AskURL:         "http://127.0.0.1:9440/v1/tls/allow",
		AdminSocket:    "/tmp/scenery-caddy.sock",
		Token:          "secret-token",
		PublicDomains:  []publicDomainSite{{Domain: "z.onlv.dev"}, {Domain: "onlv.dev"}, {Domain: "onlv.dev"}},
		ACMEEmail:      "ops@example.com",
		ACMECA:         "staging",
		StorageDir:     "/tmp/scenery-caddy-data",
		HTTPListenPort: "19080",
	})
	for _, want := range []string{
		"storage file_system /tmp/scenery-caddy-data",
		"email ops@example.com",
		"http_port 19080",
		"https_port 19443",
		"onlv.dev:19443 {",
		"z.onlv.dev:19443 {",
		"issuer acme {",
		"ca https://acme-staging-v02.api.letsencrypt.org/directory",
		"header_up X-Scenery-Public-Edge 1",
		"http://onlv.dev:19080 {",
		"redir https://{host}{uri} 308",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("public Caddy config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "local_certs") {
		t.Fatalf("public Caddy config should keep internal issuer per-site, not global local_certs:\n%s", config)
	}
	if strings.Count(config, "\nonlv.dev:19443 {") != 1 {
		t.Fatalf("public Caddy config should de-duplicate domains:\n%s", config)
	}
	if strings.Index(config, "onlv.dev:19443 {") > strings.Index(config, "z.onlv.dev:19443 {") {
		t.Fatalf("public Caddy domains should be sorted:\n%s", config)
	}
}

func TestPublicDomainSitesForDeployRegistryUsesEnabledTargets(t *testing.T) {
	t.Parallel()

	sites := publicDomainSitesForDeployRegistry(localagent.DeployRegistry{
		Targets: []localagent.DeployTarget{
			{Domain: "z.onlv.dev", Enabled: true},
			{Domain: "off.onlv.dev", Enabled: false},
			{Domain: "onlv.dev", Enabled: true},
			{Domain: "onlv.dev", Enabled: true},
		},
	})
	if len(sites) != 2 || sites[0].Domain != "onlv.dev" || sites[1].Domain != "z.onlv.dev" {
		t.Fatalf("sites = %+v", sites)
	}
}

func TestCaddyEdgeConfigForRegistryUsesDeployTargets(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	registry := localagent.EmptyDeployRegistry()
	registry.ACMEEmail = "ops@example.com"
	registry.ACMECA = "staging"
	registry.Targets = []localagent.DeployTarget{
		{Domain: "onlv.dev", Enabled: true},
		{Domain: "off.onlv.dev", Enabled: false},
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatal(err)
	}
	config, err := caddyEdgeConfigForRegistry(paths, defaultEdgeTargetAddr, defaultEdgeHTTPTargetAddr, "127.0.0.1:9440", "/tmp/scenery-caddy.sock", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"storage file_system " + filepath.Join(paths.EdgeDir, "caddy-data"),
		"email ops@example.com",
		"onlv.dev:19443 {",
		"http://onlv.dev:19080 {",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("registry Caddy config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "off.onlv.dev") {
		t.Fatalf("registry Caddy config included disabled target:\n%s", config)
	}
}

func TestParseEdgeArgsRejectsPublicAddrOverride(t *testing.T) {
	t.Parallel()

	_, err := parseEdgeArgs([]string{"-o", "json", "--addr", "127.0.0.1:8443"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseEdgeArgs(--addr) err = %v, want unknown flag", err)
	}
	opts, err := parseEdgeArgs([]string{"-o", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseEdgeHelperLaunchStatusUsesTopLevelState(t *testing.T) {
	t.Parallel()

	state, pid, err := parseEdgeHelperLaunchStatus(`system/dev.scenery.edge-helper = {
	state = spawn scheduled

	resource coalition = {
		state = active
	}
	pid = 1234
}`)
	if err != nil {
		t.Fatal(err)
	}
	if state != "spawn scheduled" || pid != 1234 {
		t.Fatalf("parseEdgeHelperLaunchStatus() = %q, %d", state, pid)
	}
}

func TestEdgeHelperPlistUsesSystemEdgeRoute(t *testing.T) {
	t.Parallel()

	plist := edgeHelperPlist(edgeHelperOptions{
		OwnerUID:          501,
		OwnerGID:          20,
		OwnerHome:         "/Users/test/.scenery",
		HelperTargetState: "/Users/test/.scenery/run/edge-target.json",
		RouterAddr:        "127.0.0.1:9440",
	})
	want := strings.Join([]string{
		"<string>system</string>",
		"<string>edge</string>",
		"<string>privileged-helper</string>",
		"<string>run</string>",
	}, "\n\t\t")
	if !strings.Contains(plist, want) {
		t.Fatalf("edge helper plist route missing system edge helper command:\n%s", plist)
	}
	removedTopLevelRoute := strings.Join([]string{
		"<string>/usr/local/libexec/scenery-edge-helper</string>",
		"<string>edge</string>",
		"<string>privileged-helper</string>",
		"<string>run</string>",
	}, "\n\t\t")
	if strings.Contains(plist, removedTopLevelRoute) {
		t.Fatalf("edge helper plist uses removed top-level edge command:\n%s", plist)
	}
}

func TestParseEdgeHelperArgsAcceptsPublicAndVersion(t *testing.T) {
	t.Parallel()

	opts, err := parseEdgeHelperArgs([]string{
		"--public",
		"--helper-version", "1.2.3",
		"--owner-uid", "501",
		"--owner-gid", "20",
		"--owner-home", "/Users/test/.scenery",
		"--helper-target-state", "/Users/test/.scenery/run/edge-target.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Public || opts.HelperVersion != "1.2.3" || opts.OwnerUID != 501 || opts.OwnerGID != 20 {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestEdgeHelperListenSpecsSwitchToPublicPorts(t *testing.T) {
	t.Parallel()

	local := edgeHelperListenSpecs(edgeHelperOptions{})
	if got := strings.Join(edgeHelperListenAddrs(local), ","); got != "127.0.0.1:443,[::1]:443" {
		t.Fatalf("local listen = %s", got)
	}
	public := edgeHelperListenSpecs(edgeHelperOptions{Public: true})
	if got := strings.Join(edgeHelperListenAddrs(public), ","); got != "[::]:443,[::]:80" {
		t.Fatalf("public listen = %s", got)
	}
	if !public[1].HTTPPort80 {
		t.Fatalf("port 80 specs should use HTTP target: %+v", public)
	}
}

func TestListenEdgeHelperPublicWildcardAcceptsIPv4(t *testing.T) {
	t.Parallel()

	ln, err := listenEdgeHelperSpec(edgeHelperListenSpec{Addr: "[::]:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		close(done)
	}()
	conn, err := net.Dial("tcp4", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	<-done
}

func TestEdgePrivilegedInstallCommandUsesDeploySetupForDeploy(t *testing.T) {
	t.Parallel()

	if got := edgePrivilegedInstallCommand(false); got != "scenery system edge privileged install" {
		t.Fatalf("local command = %q", got)
	}
	if got := edgePrivilegedInstallCommand(true); got != "scenery deploy setup" {
		t.Fatalf("deploy command = %q", got)
	}
}

func TestParseEdgeHelperPlistOptionsExtractsProgramArguments(t *testing.T) {
	t.Parallel()

	plist := edgeHelperPlist(edgeHelperOptions{
		OwnerUID:          501,
		OwnerGID:          20,
		OwnerHome:         "/Users/test/Scenery & Dev",
		HelperTargetState: "/Users/test/Scenery & Dev/run/edge-target.json",
		RouterAddr:        "127.0.0.1:9440",
		Public:            true,
		HelperVersion:     "1.2.3",
	})
	opts, err := parseEdgeHelperPlistOptions([]byte(plist))
	if err != nil {
		t.Fatal(err)
	}
	if opts.OwnerUID != 501 || opts.OwnerGID != 20 || opts.OwnerHome != "/Users/test/Scenery & Dev" || opts.HelperTargetState != "/Users/test/Scenery & Dev/run/edge-target.json" || opts.RouterAddr != "127.0.0.1:9440" || !opts.Public || opts.HelperVersion != "1.2.3" {
		t.Fatalf("parseEdgeHelperPlistOptions() = %+v", opts)
	}
}

func TestEdgeTargetAddrForPortUsesHTTPMetadata(t *testing.T) {
	t.Parallel()

	state := localagent.EdgeHelperTarget{
		TargetAddr:     "127.0.0.1:19443",
		HTTPTargetAddr: "127.0.0.1:19080",
	}
	if got, err := edgeTargetAddrForPort(state, false); err != nil || got != "127.0.0.1:19443" {
		t.Fatalf("https target = %q, %v", got, err)
	}
	if got, err := edgeTargetAddrForPort(state, true); err != nil || got != "127.0.0.1:19080" {
		t.Fatalf("http target = %q, %v", got, err)
	}
	state.HTTPTargetAddr = ""
	if _, err := edgeTargetAddrForPort(state, true); err == nil || !strings.Contains(err.Error(), "no HTTP target") {
		t.Fatalf("missing HTTP target err = %v", err)
	}
}

func TestPublishEdgeTargetForInstalledHelperUsesHelperTargetPath(t *testing.T) {
	paths := localagent.PathsForHome(filepath.Join(t.TempDir(), "isolated"))
	helperTargetPath := filepath.Join(t.TempDir(), "default-home", "run", "edge-target.json")
	oldOptions := edgeHelperPlistOptionsFunc
	edgeHelperPlistOptionsFunc = func() (edgeHelperOptions, error) {
		return edgeHelperOptions{
			OwnerUID:          os.Getuid(),
			OwnerGID:          os.Getgid(),
			OwnerHome:         filepath.Dir(filepath.Dir(helperTargetPath)),
			HelperTargetState: helperTargetPath,
			RouterAddr:        "127.0.0.1:9440",
		}, nil
	}
	t.Cleanup(func() { edgeHelperPlistOptionsFunc = oldOptions })

	target := localagent.EdgeTargetState{
		Kind:       localagent.EdgeKindCaddy,
		TargetAddr: "127.0.0.1:19443",
		PID:        12345,
		OwnerUID:   os.Getuid(),
		OwnerGID:   os.Getgid(),
	}
	if err := publishEdgeTargetForHelper(paths, target); err != nil {
		t.Fatal(err)
	}
	got, err := localagent.LoadEdgeTargetState(helperTargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetAddr != target.TargetAddr || got.PID != target.PID {
		t.Fatalf("helper target = %+v, want %+v", got, target)
	}
	if _, err := os.Stat(paths.EdgeTargetPath); !os.IsNotExist(err) {
		t.Fatalf("local target path stat err = %v, want not exist", err)
	}

	removePublishedEdgeTargetForHelper(paths, localagent.EdgeState{PID: 999, HTTPSListen: target.TargetAddr})
	if _, err := os.Stat(helperTargetPath); err != nil {
		t.Fatalf("helper target removed for mismatched pid: %v", err)
	}
	removePublishedEdgeTargetForHelper(paths, localagent.EdgeState{PID: target.PID, HTTPSListen: target.TargetAddr})
	if _, err := os.Stat(helperTargetPath); !os.IsNotExist(err) {
		t.Fatalf("helper target stat err = %v, want removed", err)
	}
}

func TestValidateEdgeAgentHealthRejectsFallbackRouterAddr(t *testing.T) {
	t.Parallel()

	err := validateEdgeAgentHealth(localagent.HealthResponse{
		RouterAddr:   "127.0.0.1:58090",
		RouterScheme: "http",
	}, "127.0.0.1:9440")
	if err == nil || !strings.Contains(err.Error(), "want 127.0.0.1:9440 for edge upstream") {
		t.Fatalf("validateEdgeAgentHealth() err = %v, want fallback router rejection", err)
	}
	if err := validateEdgeAgentHealth(localagent.HealthResponse{
		RouterAddr:   "127.0.0.1:9440",
		RouterScheme: "https",
	}, "127.0.0.1:9440"); err != nil {
		t.Fatalf("validateEdgeAgentHealth() err = %v", err)
	}
}

func TestEdgeAgentCommandMatchesSameRouter(t *testing.T) {
	t.Parallel()

	command := "/Users/petrbrazdil/go/bin/scenery system agent --socket /Users/petrbrazdil/.scenery/run/agent.sock --router-listen 127.0.0.1:9440 --router-http"
	if !edgeAgentCommandMatches(command, "/Users/petrbrazdil/.scenery/run/agent.sock", "127.0.0.1:9440") {
		t.Fatal("expected exact edge agent command to match")
	}
	if !edgeAgentCommandMatches(command, "/tmp/other.sock", "127.0.0.1:9440") {
		t.Fatal("same router should match even when a stale process has another socket")
	}
	if edgeAgentCommandMatches(command, "/Users/petrbrazdil/.scenery/run/agent.sock", "127.0.0.1:9555") {
		t.Fatal("different router should not match")
	}
	if edgeAgentCommandMatches("/usr/bin/other --socket /Users/petrbrazdil/.scenery/run/agent.sock --router-listen 127.0.0.1:9440", "/Users/petrbrazdil/.scenery/run/agent.sock", "127.0.0.1:9440") {
		t.Fatal("non-scenery agent command should not match")
	}
}

func TestRuntimeProcessParsingAndManagedCaddyMatch(t *testing.T) {
	t.Parallel()
	processes := parseRuntimeProcesses(" 42 501 /opt/caddy run --config /Users/petr/.onlava/agent/edge/Caddyfile --adapter caddyfile\ninvalid\n")
	if len(processes) != 1 || processes[0].PID != 42 || processes[0].UID != 501 {
		t.Fatalf("processes = %+v", processes)
	}
	if !managedCaddyCommandMatches(processes[0].Command, []string{"/Users/petr/.onlava/agent/edge/Caddyfile"}) {
		t.Fatal("legacy managed Caddy did not match")
	}
	if managedCaddyCommandMatches(processes[0].Command, []string{"/Users/petr/.scenery/agent/edge/Caddyfile"}) {
		t.Fatal("unrelated Caddy matched")
	}
}

func TestResolveCaddyBinaryUsesManagedToolchain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Parallel()
	storeDir := filepath.Join(t.TempDir(), "toolchain")
	caddy := filepath.Join(storeDir, "artifacts", "caddy", "2.11.4", currentPlatformDirForTest(), "bin", "caddy")
	if err := os.MkdirAll(filepath.Dir(caddy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caddy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCaddyBinaryInStore(context.Background(), storeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != caddy {
		t.Fatalf("resolveCaddyBinary() = %q, want %q", got, caddy)
	}
}

func TestResolveDNSMasqBinaryUsesManagedToolchain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Parallel()
	storeDir := filepath.Join(t.TempDir(), "toolchain")
	dnsmasq := filepath.Join(storeDir, "artifacts", "dnsmasq", "2.93", currentPlatformDirForTest(), "bin", "dnsmasq")
	if err := os.MkdirAll(filepath.Dir(dnsmasq), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dnsmasq, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveDNSMasqBinaryInStore(context.Background(), storeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != dnsmasq {
		t.Fatalf("resolveDNSMasqBinary() = %q, want %q", got, dnsmasq)
	}
}

func TestDNSMasqEdgeConfigUsesWildcardDevDomain(t *testing.T) {
	t.Parallel()

	config := dnsmasqEdgeConfig([]string{"local.dev"}, "127.0.0.1:53535", "127.0.0.1")
	for _, want := range []string{
		"bind-interfaces",
		"listen-address=127.0.0.1",
		"port=53535",
		"address=/local.dev/127.0.0.1",
		"no-resolv",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("dnsmasq config missing %q:\n%s", want, config)
		}
	}
}

func TestDNSMasqEdgeConfigSupportsMultipleDomains(t *testing.T) {
	t.Parallel()

	config := dnsmasqEdgeConfig([]string{"onlv.dev", "local.dev", "onlv.dev"}, "127.0.0.1:53535", "127.0.0.1")
	for _, want := range []string{
		"address=/local.dev/127.0.0.1",
		"address=/onlv.dev/127.0.0.1",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("dnsmasq config missing %q:\n%s", want, config)
		}
	}
	if strings.Count(config, "address=/onlv.dev/127.0.0.1") != 1 {
		t.Fatalf("dnsmasq config should de-duplicate domains:\n%s", config)
	}
}

func TestEdgeDNSConfigServesDomain(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dnsmasq.conf")
	if err := os.WriteFile(path, []byte(dnsmasqEdgeConfig([]string{"local.dev", "onlv.dev"}, "127.0.0.1:53535", "127.0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}
	if !edgeDNSConfigServesDomain(path, "onlv.dev") {
		t.Fatal("expected config to serve onlv.dev")
	}
	if edgeDNSConfigServesDomain(path, "other.dev") {
		t.Fatal("did not expect config to serve other.dev")
	}
}

func TestEdgeDNSStatusAcceptsFunctionalExternalResolver(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	oldResolverStatus := edgeDNSResolverStatusFunc
	oldResolverServes := edgeDNSResolverServesDomainFunc
	t.Cleanup(func() {
		edgeDNSResolverStatusFunc = oldResolverStatus
		edgeDNSResolverServesDomainFunc = oldResolverServes
	})
	edgeDNSResolverStatusFunc = func(domain, listen string) edgeDNSResolverState {
		return edgeDNSResolverState{
			Installed:  true,
			State:      "installed",
			Domain:     domain,
			Nameserver: "127.0.0.1",
			Port:       "53535",
		}
	}
	edgeDNSResolverServesDomainFunc = func(domain, nameserver, port, address string) bool {
		return domain == "onlv.dev" && nameserver == "127.0.0.1" && port == "53535" && address == "127.0.0.1"
	}

	status := edgeDNSStatusFor(paths, "onlv.dev")
	if !status.Ready {
		t.Fatalf("status.Ready = false, want true: %+v", status)
	}
	if status.DNSMasq.State != "external" {
		t.Fatalf("dnsmasq state = %q, want external", status.DNSMasq.State)
	}
}

func TestWaitForEdgeDNSStartupDetectsProcessExitBehindExistingListener(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	logPath := filepath.Join(t.TempDir(), "dnsmasq.log")
	if err := os.WriteFile(logPath, []byte("dnsmasq: failed to create listening socket for 127.0.0.1: Address already in use\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCh := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		exitCh <- os.ErrPermission
	}()

	err = waitForEdgeDNSStartup(listener.Addr().String(), exitCh, logPath, 0, 200*time.Millisecond)
	if err == nil {
		t.Fatal("waitForEdgeDNSStartup returned nil for a dnsmasq process that exited during startup")
	}
	if !strings.Contains(err.Error(), "dnsmasq exited during startup") || !strings.Contains(err.Error(), "Address already in use") {
		t.Fatalf("waitForEdgeDNSStartup() err = %v, want startup exit with log tail", err)
	}
}

func TestEdgeDNSHelperArgsNormalizeDomain(t *testing.T) {
	t.Parallel()

	opts, err := parseEdgeDNSHelperArgs([]string{"--domain", "HTTPS://LOCAL.DEV/path", "--nameserver", "127.0.0.1", "--port", "53535"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Domain != "local.dev" || opts.Nameserver != "127.0.0.1" || opts.Port != "53535" {
		t.Fatalf("helper opts = %+v", opts)
	}
}

func TestEdgeDNSResolverFile(t *testing.T) {
	t.Parallel()

	got := edgeDNSResolverFile("local.dev", "127.0.0.1", "53535")
	for _, want := range []string{
		"Managed by scenery edge dns",
		"domain local.dev",
		"nameserver 127.0.0.1",
		"port 53535",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolver file missing %q:\n%s", want, got)
		}
	}
}

func TestResolveCaddyBinaryDoesNotUseSystemPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("SCENERY_TOOLCHAIN_DIR", "")
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, "caddy"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveCaddyBinary(context.Background(), paths, false)
	if err == nil || !strings.Contains(err.Error(), "system PATH binaries are not used") {
		t.Fatalf("resolveCaddyBinary() err = %v", err)
	}
}

func TestLoadEdgeDNSStateMigratesResolverOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.json")
	legacy := []byte(`{"schema_version":"scenery.edge.dns.state.v1","status":"running","pid":42,"domain":"local.dev","listen":"127.0.0.1:53535","address":"127.0.0.1","resolver_path":"/etc/resolver/local.dev","updated_at":"2026-07-13T00:00:00Z"}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := loadEdgeDNSState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != "scenery.edge.dns-state" || state.PID != 42 || state.ResolverPath != "/etc/resolver/local.dev" {
		t.Fatalf("migrated state = %+v", state)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}
