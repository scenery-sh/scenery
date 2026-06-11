package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestCaddyEdgeConfigUsesStableAgentRouterContract(t *testing.T) {
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

func TestParseEdgeArgsRejectsPublicAddrOverride(t *testing.T) {
	_, err := parseEdgeArgs([]string{"--json", "--addr", "127.0.0.1:8443"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseEdgeArgs(--addr) err = %v, want unknown flag", err)
	}
	opts, err := parseEdgeArgs([]string{"--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseEdgeHelperLaunchStatusUsesTopLevelState(t *testing.T) {
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

func TestValidateEdgeAgentHealthRejectsFallbackRouterAddr(t *testing.T) {
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

func TestEdgeAgentCommandMatchesSameSocketAndRouterOnly(t *testing.T) {
	command := "/Users/petrbrazdil/go/bin/scenery system agent --socket /Users/petrbrazdil/.scenery/run/agent.sock --router-listen 127.0.0.1:9440 --router-http"
	if !edgeAgentCommandMatches(command, "/Users/petrbrazdil/.scenery/run/agent.sock", "127.0.0.1:9440") {
		t.Fatal("expected exact edge agent command to match")
	}
	if edgeAgentCommandMatches(command, "/tmp/other.sock", "127.0.0.1:9440") {
		t.Fatal("different socket should not match")
	}
	if edgeAgentCommandMatches(command, "/Users/petrbrazdil/.scenery/run/agent.sock", "127.0.0.1:9555") {
		t.Fatal("different router should not match")
	}
	if edgeAgentCommandMatches("/usr/bin/other --socket /Users/petrbrazdil/.scenery/run/agent.sock --router-listen 127.0.0.1:9440", "/Users/petrbrazdil/.scenery/run/agent.sock", "127.0.0.1:9440") {
		t.Fatal("non-scenery agent command should not match")
	}
}

func TestResolveCaddyBinaryUsesManagedToolchain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Parallel()
	storeDir := filepath.Join(t.TempDir(), "toolchain")
	caddy := filepath.Join(storeDir, "artifacts", "caddy", "2.11.3", currentPlatformDirForTest(), "bin", "caddy")
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
	dnsmasq := filepath.Join(storeDir, "artifacts", "dnsmasq", "2.92", currentPlatformDirForTest(), "bin", "dnsmasq")
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

func TestEdgeDNSHelperArgsNormalizeDomain(t *testing.T) {
	opts, err := parseEdgeDNSHelperArgs([]string{"--domain", "HTTPS://LOCAL.DEV/path", "--nameserver", "127.0.0.1", "--port", "53535"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Domain != "local.dev" || opts.Nameserver != "127.0.0.1" || opts.Port != "53535" {
		t.Fatalf("helper opts = %+v", opts)
	}
}

func TestEdgeDNSResolverFile(t *testing.T) {
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

func TestCaddyTrustConfigUsesAdminOnlyLocalCA(t *testing.T) {
	config := caddyTrustConfig("/tmp/scenery-trust.sock")
	for _, want := range []string{
		"local_certs",
		"admin unix///tmp/scenery-trust.sock",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("trust config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "https://") || strings.Contains(config, "reverse_proxy") {
		t.Fatalf("trust config should not bind HTTPS routes:\n%s", config)
	}
}

func TestEdgeTrustUsesTemporaryCaddyAdmin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for fake Unix-socket Caddy fixture")
	}
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("SCENERY_TOOLCHAIN_DIR", "")
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	caddy := filepath.Join(edgeToolchainStoreDir(paths), "artifacts", "caddy", "2.11.3", currentPlatformDirForTest(), "bin", "caddy")
	if err := os.MkdirAll(filepath.Dir(caddy), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	writeFakeTrustCaddy(t, caddy, marker)
	t.Setenv("SCENERY_FAKE_CADDY_MARKER", marker)

	if err := edgeTrust(edgeOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "run\n") || !strings.Contains(got, "trust\n") {
		t.Fatalf("fake Caddy marker = %q, want run and trust", got)
	}
}

func writeFakeTrustCaddy(t *testing.T, path, marker string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
cmd="$1"
shift
config=""
while [ "$#" -gt 0 ]; do
	if [ "$1" = "--config" ]; then
		shift
		config="$1"
	fi
	shift || true
done
if [ "$cmd" = "run" ]; then
	echo run >> "$SCENERY_FAKE_CADDY_MARKER"
	sock=$(sed -n 's/.*admin unix\/\/\(.*\)$/\1/p' "$config" | head -n 1)
	exec python3 - "$sock" <<'PY'
import os
import signal
import socket
import sys
import time

sock_path = sys.argv[1]
try:
    os.unlink(sock_path)
except FileNotFoundError:
    pass
server = socket.socket(socket.AF_UNIX)
server.bind(sock_path)
server.listen(1)
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
while True:
    time.sleep(0.1)
PY
fi
if [ "$cmd" = "trust" ]; then
	echo trust >> "$SCENERY_FAKE_CADDY_MARKER"
	exit 0
fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCENERY_FAKE_CADDY_MARKER", marker)
}

func TestStartCaddyEdgeReportsFastStartupExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Parallel()
	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	caddy := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(caddy, []byte("#!/bin/sh\necho 'listen tcp 127.0.0.1:443: bind: permission denied' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeLogPath, []byte("old caddy log line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := startCaddyEdge(caddy, paths, defaultEdgePublicAddr, defaultEdgeTargetAddr, filepath.Join(paths.RunDir, "caddy-admin.sock"), "127.0.0.1:9440")
	if err == nil || !strings.Contains(err.Error(), "Caddy edge exited during startup") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("startCaddyEdge() err = %v, want startup exit with log tail", err)
	}
	if strings.Contains(err.Error(), "old caddy log line") {
		t.Fatalf("startCaddyEdge() included stale log line: %v", err)
	}
	state, stateErr := localagent.LoadEdgeState(paths.EdgeStatePath)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if localagent.EdgeStateRunning(state) {
		t.Fatalf("edge state = %+v, want not running", state)
	}
}

func TestStartCaddyEdgeWritesRunningStateAndStopsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Parallel()
	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	caddy := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(caddy, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adminSocket := filepath.Join(paths.RunDir, "caddy-admin.sock")
	if err := startCaddyEdge(caddy, paths, defaultEdgePublicAddr, defaultEdgeTargetAddr, adminSocket, "127.0.0.1:9440"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stopEdge(paths, 2*time.Second) }()
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != localagent.EdgeKindCaddy || state.Status != localagent.EdgeStatusRunning || state.PID <= 0 {
		t.Fatalf("edge state = %+v, want running caddy with pid", state)
	}
	if state.PublicAddr != defaultEdgePublicAddr || state.UpstreamAddr != "127.0.0.1:9440" || state.AdminSocket != adminSocket {
		t.Fatalf("edge state addresses = %+v", state)
	}
	if state.HTTPSListen != defaultEdgeTargetAddr {
		t.Fatalf("edge state https listener = %q, want %q", state.HTTPSListen, defaultEdgeTargetAddr)
	}
	target, err := localagent.LoadEdgeTargetState(paths.EdgeTargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if target.TargetAddr != defaultEdgeTargetAddr || target.PID != state.PID || target.OwnerUID != os.Getuid() {
		t.Fatalf("edge target state = %+v", target)
	}
	if err := stopEdge(paths, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}
