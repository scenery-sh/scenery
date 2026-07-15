package deploydiag

import (
	"net"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func checksByID(checks []Check) map[string]Check {
	out := map[string]Check{}
	for _, check := range checks {
		out[check.ID] = check
	}
	return out
}

func TestTLSHandshakeDiagnosticsGateReadiness(t *testing.T) {
	t.Parallel()

	baseSnapshot := func() Snapshot {
		return Snapshot{
			HelperPublic:    true,
			Helper:          HelperStatus{State: "running"},
			EdgeHTTPSListen: "127.0.0.1:19443",
			Targets: []Target{
				{Domain: "onlv.dev", Enabled: true},
				{Domain: "off.dev", Enabled: false},
			},
		}
	}
	probeFor := func(results map[string]TLSProbeResult) func(addr, serverName string) TLSProbeResult {
		return func(addr, serverName string) TLSProbeResult {
			return results[addr]
		}
	}

	// Healthy: the SNI handshake completes end to end through port 443.
	report := Report{}
	addTLSHandshakeDiagnostics(&report, baseSnapshot(), probeFor(map[string]TLSProbeResult{
		"127.0.0.1:443": {Outcome: TLSProbeHandshakeOK},
	}))
	checks := checksByID(report.Checks)
	if check := checks["deploy.tls_handshake.onlv.dev"]; check.Status != "ok" {
		t.Fatalf("healthy handshake check = %+v", check)
	}
	if _, probedDisabled := checks["deploy.tls_handshake.off.dev"]; probedDisabled {
		t.Fatalf("disabled target was probed: %+v", report.Checks)
	}

	// The observed outage: port 443 accepts TCP, the helper drops before any
	// TLS reply, and Caddy answers TLS directly. Status must go unhealthy
	// with the exact repair command instead of staying green.
	report = Report{}
	addTLSHandshakeDiagnostics(&report, baseSnapshot(), probeFor(map[string]TLSProbeResult{
		"127.0.0.1:443":   {Outcome: TLSProbeDropped, Error: "EOF"},
		"127.0.0.1:19443": {Outcome: TLSProbeHandshakeOK},
	}))
	check := checksByID(report.Checks)["deploy.tls_handshake.onlv.dev"]
	if check.Status != "error" || !strings.Contains(check.SuggestedAction, "deploy setup") {
		t.Fatalf("dropped handshake check = %+v", check)
	}

	// Both drop: the Caddy origin is the problem, not the helper.
	report = Report{}
	addTLSHandshakeDiagnostics(&report, baseSnapshot(), probeFor(map[string]TLSProbeResult{
		"127.0.0.1:443":   {Outcome: TLSProbeDropped, Error: "EOF"},
		"127.0.0.1:19443": {Outcome: TLSProbeUnreachable, Error: "connection refused"},
	}))
	check = checksByID(report.Checks)["deploy.tls_handshake.onlv.dev"]
	if check.Status != "warn" || strings.Contains(check.SuggestedAction, "deploy setup") {
		t.Fatalf("origin-down handshake check = %+v", check)
	}

	// Forwarding works but the handshake fails (for example no certificate
	// yet): warn toward certificate diagnostics.
	report = Report{}
	addTLSHandshakeDiagnostics(&report, baseSnapshot(), probeFor(map[string]TLSProbeResult{
		"127.0.0.1:443": {Outcome: TLSProbeForwarded, Error: "remote error: tls: internal error"},
	}))
	check = checksByID(report.Checks)["deploy.tls_handshake.onlv.dev"]
	if check.Status != "warn" || !strings.Contains(check.SuggestedAction, "certificate") {
		t.Fatalf("forwarded handshake check = %+v", check)
	}

	// Helper not running: the probe is skipped, never treated as healthy.
	stopped := baseSnapshot()
	stopped.Helper.State = "stopped"
	report = Report{}
	addTLSHandshakeDiagnostics(&report, stopped, probeFor(nil))
	if check := checksByID(report.Checks)["deploy.tls_handshake"]; check.Status != "skipped" {
		t.Fatalf("stopped helper handshake check = %+v", check)
	}
}

func TestDNSDiagnosticsAllowsCloudflareProxy(t *testing.T) {
	t.Parallel()

	lookup := func(domain string) ([]net.IP, error) {
		return []net.IP{
			net.ParseIP("104.21.1.153"),
			net.ParseIP("172.67.129.115"),
			net.ParseIP("2606:4700:3030::6815:199"),
		}, nil
	}
	report := Report{PublicIP: "217.112.163.198"}
	addDNSDiagnostics(&report, []Target{{
		Domain:  "local.clean.tech",
		Enabled: true,
	}}, lookup)
	check := checksByID(report.Checks)["deploy.dns.local.clean.tech"]
	if check.Status != "ok" || !strings.Contains(check.Message, "Cloudflare proxy IPs") {
		t.Fatalf("cloudflare DNS check = %+v", check)
	}
	if check.Observed["cloudflare_proxy"] != true {
		t.Fatalf("cloudflare proxy observation = %+v", check.Observed)
	}
}

func TestParseNetstatPortListenerDualStack(t *testing.T) {
	t.Parallel()

	output := `tcp46      0      0  *.443                  *.*                    LISTEN                 0            0  131072  131072 scenery-edge-hel:10536  00180 00000006 000000000066c994 00000000 00000800      1      0 000000`
	info, ok := parseNetstatPortListener(output, 443)
	if !ok || info.PID != 10536 || info.Command != "scenery-edge-hel" || info.Name != "*.443" {
		t.Fatalf("netstat listener = %+v, %v", info, ok)
	}
}

func TestRawIPHTTPSNeedsSNISkipsTLSInternalError(t *testing.T) {
	t.Parallel()

	if !rawIPHTTPSNeedsSNI("https://217.112.163.198/", "remote error: tls: internal error") {
		t.Fatal("expected raw-IP HTTPS TLS internal error to be skipped")
	}
	if rawIPHTTPSNeedsSNI("https://local.clean.tech/", "remote error: tls: internal error") {
		t.Fatal("domain HTTPS errors should not be skipped")
	}
}

func TestHelperDriftRequiresSetupOnlyForContractDrift(t *testing.T) {
	t.Parallel()

	// An installed helper without a stamped handoff contract predates the
	// tolerant reader: any target-metadata revision change makes it drop
	// every connection, so it must be flagged even though its version string
	// and its target metadata look plausible.
	helper := HelperStatus{
		Installed: true,
		Version:   "v1.0.0",
		Listen:    []string{"0.0.0.0:443", "[::]:443", "0.0.0.0:80", "[::]:80"},
	}
	drift := HelperDriftFor(helper, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.SuggestedAction, "deploy setup") {
		t.Fatalf("unstamped helper drift = %+v", drift)
	}
	if drift.ExpectedContract != localagent.EdgeHelperContractRevision {
		t.Fatalf("expected contract = %q", drift.ExpectedContract)
	}

	// A stamped but different contract also requires re-setup.
	helper.ContractRevision = "1"
	drift = HelperDriftFor(helper, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.Message, "handoff contract 1") {
		t.Fatalf("contract drift = %+v", drift)
	}

	// A loopback-only install points at the loopback installer instead.
	loopback := helper
	loopback.ContractRevision = ""
	loopback.Listen = []string{"127.0.0.1:443", "[::1]:443"}
	drift = HelperDriftFor(loopback, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.SuggestedAction, "system edge privileged install") {
		t.Fatalf("loopback drift = %+v", drift)
	}

	// Version drift with a matching contract stays informational: the frozen
	// handoff contract is exactly what makes scenery upgrades safe without a
	// sudo re-setup.
	helper.ContractRevision = localagent.EdgeHelperContractRevision
	drift = HelperDriftFor(helper, "v2.0.0")
	if drift.ActionRequired || !strings.Contains(drift.Message, "helper is v1.0.0") {
		t.Fatalf("version-only drift = %+v", drift)
	}

	// Matching version and contract is clean.
	helper.Version = "v2.0.0"
	drift = HelperDriftFor(helper, "v2.0.0")
	if drift.ActionRequired || !strings.Contains(drift.Message, "matches current binary") {
		t.Fatalf("clean drift = %+v", drift)
	}
}
