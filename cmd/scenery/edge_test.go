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
	edgelifecycle "scenery.sh/internal/edge"
)

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

func TestEdgeRestartReadyIgnoresLocalDNSOnlyForDeploy(t *testing.T) {
	t.Parallel()
	status := edgeStatusResult{
		Ready: false,
		Edge: edgeStatusCaddy{
			State:       localagent.EdgeStatusRunning,
			PID:         42,
			HTTPSListen: "127.0.0.1:19443",
			Upstream:    "127.0.0.1:9440",
			AgentRouter: "127.0.0.1:9440",
		},
		DNS: edgeDNSStatusResult{Ready: false},
		PrivilegedListener: edgeStatusPrivilegedListener{
			State:     "running",
			Target:    "127.0.0.1:19443",
			TargetPID: 42,
		},
	}
	if !edgeRestartReady(status, true) {
		t.Fatal("public deploy restart must reach target recovery when optional local DNS is down")
	}
	if edgeRestartReady(status, false) {
		t.Fatal("ordinary local edge restart must still require wildcard DNS")
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
	edgeDNSResolverStatusFunc = func(domain, listen string) edgelifecycle.DNSResolverState {
		return edgelifecycle.DNSResolverState{
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
