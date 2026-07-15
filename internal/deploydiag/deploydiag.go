// Package deploydiag builds the deploy diagnostics report behind
// `scenery deploy status` and `scenery doctor`: it turns one snapshot of the
// public edge (privileged helper, Caddy edge, deploy targets) plus injectable
// network probes into an ordered list of ok/warn/error/skipped checks. The
// package is pure with respect to Scenery state: callers convert their status
// payloads into Snapshot and wire real or stubbed probes through Deps.
package deploydiag

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

// defaultEdgeTargetAddr mirrors cmd/scenery's default Caddy HTTPS listen
// address, used when the snapshot reports neither an edge listen address nor
// a helper target.
const defaultEdgeTargetAddr = "127.0.0.1:19443"

// edgeHelperLabel mirrors the launchd label of the privileged edge helper
// installed by cmd/scenery; port listeners whose command contains it count as
// Scenery-owned.
const edgeHelperLabel = "dev.scenery.edge-helper"

// Report is the deploy diagnostics report exposed under the
// `diagnostics_detail` field of `scenery deploy status -o json` and the
// `diagnostics` field of the doctor deploy payload.
type Report struct {
	LANIP    string  `json:"lan_ip,omitempty"`
	PublicIP string  `json:"public_ip,omitempty"`
	Checks   []Check `json:"checks,omitempty"`
}

// Check is one diagnostic result. Status is "ok", "warn", "error", or
// "skipped".
type Check struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	Message         string         `json:"message"`
	SuggestedAction string         `json:"suggested_action,omitempty"`
	Observed        map[string]any `json:"observed,omitempty"`
}

// HTTPProbeResult reports one HTTP reachability self-probe.
type HTTPProbeResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

// PowerStatus reports the host's on-power sleep configuration.
type PowerStatus struct {
	Supported    bool
	SleepMinutes int
	Raw          string
}

// FirewallStatus reports the host application firewall state.
type FirewallStatus struct {
	Supported bool
	Enabled   bool
	Raw       string
}

// PortListenerInfo describes the process currently listening on a TCP port.
type PortListenerInfo struct {
	Port    int
	PID     int
	Command string
	Name    string
}

// IsSceneryHelper reports whether the listener looks like the Scenery
// privileged edge helper.
func (info PortListenerInfo) IsSceneryHelper() bool {
	command := strings.ToLower(info.Command)
	return strings.Contains(command, "scenery-edge-helper") || strings.Contains(command, edgeHelperLabel)
}

// String renders the listener as "command (pid N)" for diagnostics text.
func (info PortListenerInfo) String() string {
	command := strings.TrimSpace(info.Command)
	if command == "" {
		command = "unknown process"
	}
	if info.PID > 0 {
		return fmt.Sprintf("%s (pid %d)", command, info.PID)
	}
	return command
}

// TLSProbeOutcome classifies one loopback TLS reachability probe. The values
// mirror cmd/scenery's edge TLS probe outcomes byte for byte because they are
// recorded in check Observed maps.
type TLSProbeOutcome string

const (
	// TLSProbeUnreachable: the TCP dial itself failed.
	TLSProbeUnreachable TLSProbeOutcome = "unreachable"
	// TLSProbeDropped: TCP connected but the connection closed or timed out
	// without any TLS-level reply.
	TLSProbeDropped TLSProbeOutcome = "dropped"
	// TLSProbeForwarded: the far end answered at the TLS layer even though
	// the handshake for this server name did not complete.
	TLSProbeForwarded TLSProbeOutcome = "forwarded"
	// TLSProbeHandshakeOK: a full TLS handshake completed.
	TLSProbeHandshakeOK TLSProbeOutcome = "handshake_ok"
)

// TLSProbeResult is the outcome of one TLS handshake probe.
type TLSProbeResult struct {
	Outcome TLSProbeOutcome
	Error   string
}

// ReachedTLSServer reports whether the probe got any TLS-level reply.
func (r TLSProbeResult) ReachedTLSServer() bool {
	return r.Outcome == TLSProbeHandshakeOK || r.Outcome == TLSProbeForwarded
}

// HelperStatus is the snapshot of the privileged public listener the
// diagnostics engine consumes; callers convert their own helper status type
// into it.
type HelperStatus struct {
	Installed        bool
	State            string
	PID              int
	Listen           []string
	Target           string
	Version          string
	ContractRevision string
}

// Target is the snapshot of one registered deploy target.
type Target struct {
	Domain       string
	Enabled      bool
	CertPresent  bool
	CertNotAfter string
}

// Snapshot is the point-in-time deploy state the report is built from.
type Snapshot struct {
	Helper          HelperStatus
	HelperPublic    bool
	EdgeHTTPSListen string
	Targets         []Target
	// CurrentVersion is the running scenery binary version, compared against
	// the installed helper for drift.
	CurrentVersion string
	// ServiceManager is "launchd" on macOS or "systemd" on Linux deploy
	// hosts. Under systemd there is no privileged loopback helper: the
	// managed Caddy edge binds 80/443 directly, so helper diagnostics are
	// replaced by direct edge-listener checks.
	ServiceManager string
	// EdgeUnitActive reports the systemd managed-edge unit state when
	// ServiceManager is "systemd".
	EdgeUnitActive bool
}

// Deps injects every network and host probe the engine runs, so tests and
// callers can stub them.
type Deps struct {
	PortListener   func(port int) (PortListenerInfo, bool, error)
	LANIP          func(ctx context.Context) (string, error)
	HTTPProbe      func(ctx context.Context, url string) HTTPProbeResult
	PublicIP       func(ctx context.Context) (string, error)
	DNSLookup      func(domain string) ([]net.IP, error)
	PowerStatus    func(ctx context.Context) (PowerStatus, error)
	FirewallStatus func(ctx context.Context) (FirewallStatus, error)
	// TLSProbe performs one TLS handshake probe against addr with the given
	// SNI server name; the caller owns the probe timeout.
	TLSProbe func(addr, serverName string) TLSProbeResult
}

// HelperDrift compares the installed privileged helper against the current
// binary. It appears in deploy resume and upgrade JSON payloads.
type HelperDrift struct {
	HelperInstalled  bool   `json:"helper_installed"`
	ActionRequired   bool   `json:"action_required"`
	HelperVersion    string `json:"helper_version,omitempty"`
	CurrentVersion   string `json:"current_version,omitempty"`
	HelperContract   string `json:"helper_contract,omitempty"`
	ExpectedContract string `json:"expected_contract"`
	Message          string `json:"message,omitempty"`
	SuggestedAction  string `json:"suggested_action,omitempty"`
}

// BuildReport builds the deploy diagnostics report for one status snapshot.
// Helper, listener, and certificate checks always run; the network
// reachability, DNS, power, and firewall probes run only when at least one
// deploy target is enabled.
func BuildReport(ctx context.Context, snap Snapshot, deps Deps) Report {
	report := Report{}
	if snap.ServiceManager == "systemd" {
		addSystemdEdgeDiagnostics(&report, snap, deps.PortListener)
	} else {
		addHelperDiagnostics(&report, snap.Helper, snap.CurrentVersion)
		addListenerDiagnostics(&report, snap.Helper, deps.PortListener)
	}
	addCertDiagnostics(&report, snap.Targets)
	if !hasEnabledTarget(snap.Targets) {
		return report
	}
	addTLSHandshakeDiagnostics(&report, snap, deps.TLSProbe)
	lanOK := addLANDiagnostics(ctx, &report, deps)
	publicOK := addPublicIPDiagnostics(ctx, &report, deps, lanOK)
	addDNSDiagnostics(&report, snap.Targets, deps.DNSLookup)
	if !publicOK && lanOK {
		report.addCheck(Check{
			ID:              "deploy.reachability.public",
			Status:          "warn",
			Message:         "LAN self-probe works but the public IP self-probe failed; verify router forwarding for TCP 80/443 to this Mac",
			SuggestedAction: "Forward public TCP 80/443 to the reported LAN IP. If the router WAN IP differs from the reported public IP or is in 100.64.0.0/10, ask the ISP for a public address because CGNAT can block inbound traffic.",
		})
	}
	addPowerDiagnostic(ctx, &report, deps.PowerStatus)
	addFirewallDiagnostic(ctx, &report, deps.FirewallStatus)
	return report
}

func hasEnabledTarget(targets []Target) bool {
	for _, target := range targets {
		if target.Enabled {
			return true
		}
	}
	return false
}

// addSystemdEdgeDiagnostics replaces the macOS helper checks on Linux: the
// managed edge unit must be active and ports 80/443 must be bound by the
// managed Caddy edge itself.
func addSystemdEdgeDiagnostics(report *Report, snap Snapshot, portListener func(port int) (PortListenerInfo, bool, error)) {
	unitCheck := Check{
		ID:       "deploy.edge.unit",
		Status:   "ok",
		Message:  "managed edge systemd unit is active",
		Observed: map[string]any{"active": snap.EdgeUnitActive},
	}
	if !snap.EdgeUnitActive {
		unitCheck.Status = "warn"
		unitCheck.Message = "managed edge systemd unit is not active"
		unitCheck.SuggestedAction = "Run `scenery deploy setup` to install and start the scenery-edge systemd unit."
	}
	report.addCheck(unitCheck)
	for _, port := range []int{80, 443} {
		listener, ok, err := portListener(port)
		check := Check{
			ID:      fmt.Sprintf("deploy.listener.%d", port),
			Status:  "ok",
			Message: fmt.Sprintf("port %d is bound by the managed Caddy edge", port),
		}
		switch {
		case err != nil:
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d listener could not be inspected: %v", port, err)
		case !ok:
			check.Status = "warn"
			check.Message = fmt.Sprintf("no process is listening on port %d", port)
			check.SuggestedAction = "Run `scenery deploy setup` to start the managed edge."
		case !strings.Contains(strings.ToLower(listener.Command), "caddy"):
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d is bound by %s, not the managed Caddy edge", port, listener.String())
			check.SuggestedAction = fmt.Sprintf("Stop that process, then run `scenery deploy setup` so Scenery can bind TCP %d.", port)
		}
		report.addCheck(check)
	}
}

func addHelperDiagnostics(report *Report, helper HelperStatus, currentVersion string) {
	status := "ok"
	message := "public privileged helper is installed and running"
	action := ""
	if !helper.Installed || helper.State != "running" {
		status = "warn"
		message = "public privileged helper is not running"
		action = "Run `scenery deploy setup` to install and start the public edge helper."
	}
	report.addCheck(Check{
		ID:              "deploy.helper",
		Status:          status,
		Message:         message,
		SuggestedAction: action,
		Observed: map[string]any{
			"installed": helper.Installed,
			"state":     helper.State,
			"pid":       helper.PID,
			"listen":    helper.Listen,
		},
	})
	drift := HelperDriftFor(helper, currentVersion)
	versionStatus := "ok"
	if drift.ActionRequired {
		versionStatus = "warn"
	}
	report.addCheck(Check{
		ID:              "deploy.helper_version",
		Status:          versionStatus,
		Message:         drift.Message,
		SuggestedAction: drift.SuggestedAction,
		Observed: map[string]any{
			"helper_version":    drift.HelperVersion,
			"current_version":   drift.CurrentVersion,
			"helper_contract":   drift.HelperContract,
			"expected_contract": drift.ExpectedContract,
		},
	})
}

// HelperDriftFor compares the handoff-contract revision stamped into the
// installed helper LaunchDaemon against the current binary's contract.
// Version drift alone stays informational: the helper contract is designed to
// survive scenery upgrades, so only a contract mismatch (or an unstamped
// helper from before the tolerant handoff reader) requires a sudo re-setup.
func HelperDriftFor(helper HelperStatus, currentVersion string) HelperDrift {
	drift := HelperDrift{
		HelperInstalled:  helper.Installed,
		HelperVersion:    strings.TrimSpace(helper.Version),
		CurrentVersion:   strings.TrimSpace(currentVersion),
		HelperContract:   strings.TrimSpace(helper.ContractRevision),
		ExpectedContract: localagent.EdgeHelperContractRevision,
	}
	reinstall := "Run `scenery deploy setup` to update the privileged listener (asks for sudo)."
	if !HelperHasPublicBinding(helper.Listen) {
		reinstall = "Run `scenery system edge privileged install` to update the privileged listener (asks for sudo)."
	}
	switch {
	case !helper.Installed:
		drift.Message = "privileged helper is not installed"
	case drift.HelperContract == "":
		drift.ActionRequired = true
		drift.Message = fmt.Sprintf("installed privileged helper predates handoff contract %s and can stop forwarding when target metadata changes", drift.ExpectedContract)
		drift.SuggestedAction = reinstall
	case drift.HelperContract != drift.ExpectedContract:
		drift.ActionRequired = true
		drift.Message = fmt.Sprintf("installed privileged helper uses handoff contract %s; current binary expects %s", drift.HelperContract, drift.ExpectedContract)
		drift.SuggestedAction = reinstall
	case drift.HelperVersion == "":
		drift.Message = "helper version is not recorded"
	case drift.CurrentVersion != "" && drift.HelperVersion != drift.CurrentVersion:
		drift.Message = fmt.Sprintf("helper is %s and current binary is %s; the handoff contract matches, so no action is needed", drift.HelperVersion, drift.CurrentVersion)
	default:
		drift.Message = fmt.Sprintf("helper version %s matches current binary", drift.HelperVersion)
	}
	return drift
}

// HelperHasPublicBinding reports whether the helper listen addresses include
// wildcard bindings for both port 80 and port 443.
func HelperHasPublicBinding(listen []string) bool {
	has80 := false
	has443 := false
	for _, addr := range listen {
		switch strings.TrimSpace(addr) {
		case "0.0.0.0:80", "[::]:80":
			has80 = true
		case "0.0.0.0:443", "[::]:443":
			has443 = true
		}
	}
	return has80 && has443
}

// addTLSHandshakeDiagnostics proves each enabled domain end to end: a TLS
// handshake with that domain's SNI must complete through public port 443
// (helper → Caddy). A bound listener or a live helper PID never counts as
// ready on its own — a helper that cannot validate its target metadata still
// accepts TCP and then resets, which upstream proxies surface as errors like
// Cloudflare 525 while naive status checks stay green.
func addTLSHandshakeDiagnostics(report *Report, snap Snapshot, probe func(addr, serverName string) TLSProbeResult) {
	if !snap.HelperPublic || snap.Helper.State != "running" {
		report.addCheck(Check{
			ID:      "deploy.tls_handshake",
			Status:  "skipped",
			Message: "end-to-end TLS handshake probe skipped because the public privileged helper is not running",
		})
		return
	}
	directAddr := firstNonEmpty(snap.EdgeHTTPSListen, snap.Helper.Target, defaultEdgeTargetAddr)
	for _, target := range snap.Targets {
		if !target.Enabled {
			continue
		}
		through := probe("127.0.0.1:443", target.Domain)
		check := Check{
			ID:     "deploy.tls_handshake." + target.Domain,
			Status: "ok",
			Observed: map[string]any{
				"domain":  target.Domain,
				"outcome": string(through.Outcome),
			},
		}
		if through.Error != "" {
			check.Observed["error"] = through.Error
		}
		switch through.Outcome {
		case TLSProbeHandshakeOK:
			check.Message = fmt.Sprintf("end-to-end TLS handshake for %s succeeded through port 443", target.Domain)
		case TLSProbeUnreachable:
			check.Status = "warn"
			check.Message = fmt.Sprintf("port 443 did not accept a TCP connection for the %s handshake probe", target.Domain)
			check.SuggestedAction = "Run `scenery deploy setup` and check the port 80/443 listener diagnostics."
		case TLSProbeForwarded:
			check.Status = "warn"
			check.Message = fmt.Sprintf("traffic reaches Caddy but the TLS handshake for %s did not complete: %s", target.Domain, through.Error)
			check.SuggestedAction = "Check certificate issuance for this domain in the Caddy edge log and the certificate diagnostics above."
		case TLSProbeDropped:
			direct := probe(directAddr, target.Domain)
			check.Observed["direct_outcome"] = string(direct.Outcome)
			if direct.ReachedTLSServer() {
				check.Status = "error"
				check.Message = fmt.Sprintf("port 443 accepts TCP but the TLS handshake for %s never reaches Caddy, while Caddy at %s answers TLS directly; the running privileged helper cannot validate its target metadata", target.Domain, directAddr)
				check.SuggestedAction = "Run `scenery deploy setup` to replace and restart the privileged helper (asks for sudo)."
			} else {
				check.Status = "warn"
				check.Message = fmt.Sprintf("neither public port 443 nor the Caddy edge at %s completed a TLS handshake for %s", directAddr, target.Domain)
				check.SuggestedAction = "Start the Caddy edge (`scenery deploy resume` or `scenery system edge install`), then re-run `scenery deploy status`."
			}
		}
		report.addCheck(check)
	}
}

func addListenerDiagnostics(report *Report, helper HelperStatus, portListener func(port int) (PortListenerInfo, bool, error)) {
	for _, port := range []int{80, 443} {
		listener, ok, err := portListener(port)
		check := Check{
			ID:      fmt.Sprintf("deploy.listener.%d", port),
			Status:  "ok",
			Message: fmt.Sprintf("port %d is bound by the Scenery public helper", port),
			Observed: map[string]any{
				"port":              port,
				"configured_listen": helper.Listen,
			},
		}
		if err != nil {
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d listener could not be inspected: %v", port, err)
			check.SuggestedAction = "Run `scenery deploy setup` and verify the privileged helper with launchctl or lsof."
			report.addCheck(check)
			continue
		}
		if !ok {
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d is not listening", port)
			check.SuggestedAction = "Run `scenery deploy setup` and allow the privileged helper to bind public ports."
			report.addCheck(check)
			continue
		}
		check.Observed["pid"] = listener.PID
		check.Observed["command"] = listener.Command
		check.Observed["name"] = listener.Name
		if !listener.IsSceneryHelper() {
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d is bound by %s, not the Scenery public helper", port, listener.String())
			check.SuggestedAction = fmt.Sprintf("Stop that process, then run `scenery deploy setup` so Scenery can bind TCP %d.", port)
		} else if !listenerIsWildcard(listener, helper.Listen, port) {
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d is not bound on a wildcard address", port)
			check.SuggestedAction = "Run `scenery deploy setup` to reinstall the helper with 0.0.0.0/[::] public listeners."
		}
		report.addCheck(check)
	}
}

func addCertDiagnostics(report *Report, targets []Target) {
	now := time.Now()
	for _, target := range targets {
		if !target.Enabled {
			continue
		}
		check := Check{
			ID:     "deploy.cert." + target.Domain,
			Status: "ok",
			Observed: map[string]any{
				"domain":       target.Domain,
				"cert_present": target.CertPresent,
			},
		}
		if target.CertNotAfter != "" {
			check.Observed["not_after"] = target.CertNotAfter
		}
		switch {
		case !target.CertPresent:
			check.Status = "warn"
			check.Message = fmt.Sprintf("no Caddy certificate is present for %s", target.Domain)
			check.SuggestedAction = "Verify DNS and public reachability, then let Caddy issue the certificate on first HTTPS traffic."
		case target.CertNotAfter == "":
			check.Status = "warn"
			check.Message = fmt.Sprintf("certificate for %s is present but expiry could not be parsed", target.Domain)
			check.SuggestedAction = "If HTTPS fails, remove the invalid certificate directory under the pinned Caddy storage and retry after fixing reachability."
		default:
			notAfter, err := time.Parse(time.RFC3339, target.CertNotAfter)
			if err == nil && now.After(notAfter) {
				check.Status = "warn"
				check.Message = fmt.Sprintf("certificate for %s expired at %s", target.Domain, target.CertNotAfter)
				check.SuggestedAction = "Fix reachability and let Caddy renew the certificate, or remove the expired cached certificate after confirming storage."
			} else {
				check.Message = fmt.Sprintf("certificate for %s is present until %s", target.Domain, target.CertNotAfter)
			}
		}
		report.addCheck(check)
	}
}

func addLANDiagnostics(ctx context.Context, report *Report, deps Deps) bool {
	lanIP, err := deps.LANIP(ctx)
	if err != nil || strings.TrimSpace(lanIP) == "" {
		message := "LAN IP could not be determined"
		if err != nil {
			message += ": " + err.Error()
		}
		report.addCheck(Check{
			ID:              "deploy.lan_ip",
			Status:          "warn",
			Message:         message,
			SuggestedAction: "Connect to a LAN interface or pass this Mac's LAN IP to your router forwarding rules manually.",
		})
		return false
	}
	report.LANIP = strings.TrimSpace(lanIP)
	report.addCheck(Check{
		ID:      "deploy.lan_ip",
		Status:  "ok",
		Message: "LAN IP is " + report.LANIP,
		Observed: map[string]any{
			"lan_ip": report.LANIP,
		},
	})
	httpOK := addHTTPProbeCheck(ctx, report, deps.HTTPProbe, "deploy.probe.lan_http", "LAN HTTP self-probe", urlForHost("http", report.LANIP))
	httpsOK := addHTTPProbeCheck(ctx, report, deps.HTTPProbe, "deploy.probe.lan_https", "LAN HTTPS self-probe", urlForHost("https", report.LANIP))
	return httpOK && httpsOK
}

func addPublicIPDiagnostics(ctx context.Context, report *Report, deps Deps, lanOK bool) bool {
	publicIP, err := deps.PublicIP(ctx)
	if err != nil || strings.TrimSpace(publicIP) == "" {
		message := "public IP could not be discovered"
		if err != nil {
			message += ": " + err.Error()
		}
		report.addCheck(Check{
			ID:              "deploy.public_ip",
			Status:          "warn",
			Message:         message,
			SuggestedAction: "Check internet connectivity, then rerun `scenery deploy status`.",
		})
		return false
	}
	report.PublicIP = strings.TrimSpace(publicIP)
	report.addCheck(Check{
		ID:      "deploy.public_ip",
		Status:  "ok",
		Message: "public IP is " + report.PublicIP,
		Observed: map[string]any{
			"public_ip": report.PublicIP,
		},
	})
	httpOK := addHTTPProbeCheck(ctx, report, deps.HTTPProbe, "deploy.probe.public_http", "public HTTP self-probe", urlForHost("http", report.PublicIP))
	httpsOK := addHTTPProbeCheck(ctx, report, deps.HTTPProbe, "deploy.probe.public_https", "public HTTPS self-probe", urlForHost("https", report.PublicIP))
	if lanOK && !httpOK && !httpsOK && ipIsCGNATHint(report.PublicIP) {
		report.addCheck(Check{
			ID:              "deploy.reachability.cgnat",
			Status:          "warn",
			Message:         "public IP looks like carrier-grade NAT, so inbound router forwarding may not be possible",
			SuggestedAction: "Ask the ISP for a public IPv4 address or use an IPv6 AAAA record that reaches this Mac directly.",
			Observed:        map[string]any{"public_ip": report.PublicIP},
		})
	}
	return httpOK && httpsOK
}

func addDNSDiagnostics(report *Report, targets []Target, lookup func(domain string) ([]net.IP, error)) {
	for _, target := range targets {
		if !target.Enabled {
			continue
		}
		ips, err := lookup(target.Domain)
		check := Check{
			ID:     "deploy.dns." + target.Domain,
			Status: "ok",
			Observed: map[string]any{
				"domain": target.Domain,
			},
		}
		if err != nil {
			check.Status = "warn"
			check.Message = fmt.Sprintf("DNS lookup for %s failed: %v", target.Domain, err)
			check.SuggestedAction = "Create A/AAAA records for this domain at your DNS provider."
			report.addCheck(check)
			continue
		}
		resolved := ipStrings(ips)
		check.Observed["ips"] = resolved
		check.Observed["public_ip"] = report.PublicIP
		if report.PublicIP == "" {
			check.Status = "warn"
			check.Message = fmt.Sprintf("DNS for %s resolves, but public IP discovery failed", target.Domain)
			check.SuggestedAction = "Fix public IP discovery, then confirm DNS matches that address."
		} else if !ipsContain(ips, report.PublicIP) {
			if ipsAreCloudflareProxy(ips) {
				check.Message = fmt.Sprintf("DNS for %s resolves to Cloudflare proxy IPs; verify the Cloudflare origin record points to %s", target.Domain, report.PublicIP)
				check.Observed["cloudflare_proxy"] = true
				report.addCheck(check)
				continue
			}
			check.Status = "warn"
			check.Message = fmt.Sprintf("DNS for %s resolves to %s, not this public IP %s", target.Domain, strings.Join(resolved, ", "), report.PublicIP)
			check.SuggestedAction = fmt.Sprintf("Set the A/AAAA record for %s to %s, then wait for DNS propagation.", target.Domain, report.PublicIP)
		} else {
			check.Message = fmt.Sprintf("DNS for %s resolves to this public IP", target.Domain)
		}
		report.addCheck(check)
	}
}

func addPowerDiagnostic(ctx context.Context, report *Report, powerStatus func(ctx context.Context) (PowerStatus, error)) {
	power, err := powerStatus(ctx)
	if err != nil {
		report.addCheck(Check{
			ID:      "deploy.power.sleep",
			Status:  "warn",
			Message: "power sleep settings could not be inspected: " + err.Error(),
		})
		return
	}
	if !power.Supported {
		report.addCheck(Check{
			ID:      "deploy.power.sleep",
			Status:  "skipped",
			Message: "power sleep diagnostics are supported on macOS",
		})
		return
	}
	check := Check{
		ID:      "deploy.power.sleep",
		Status:  "ok",
		Message: "system sleep is disabled while on power",
		Observed: map[string]any{
			"sleep_minutes": power.SleepMinutes,
			"raw":           power.Raw,
		},
	}
	if power.SleepMinutes > 0 {
		check.Status = "warn"
		check.Message = fmt.Sprintf("system sleep is set to %d minute(s); sleeping takes the public site down", power.SleepMinutes)
		check.SuggestedAction = "Run `sudo pmset -c sleep 0` if this Mac should keep serving public traffic on power."
	}
	report.addCheck(check)
}

func addFirewallDiagnostic(ctx context.Context, report *Report, firewallStatus func(ctx context.Context) (FirewallStatus, error)) {
	firewall, err := firewallStatus(ctx)
	if err != nil {
		report.addCheck(Check{
			ID:      "deploy.firewall",
			Status:  "warn",
			Message: "application firewall state could not be inspected: " + err.Error(),
		})
		return
	}
	if !firewall.Supported {
		report.addCheck(Check{
			ID:      "deploy.firewall",
			Status:  "skipped",
			Message: "application firewall diagnostics are supported on macOS",
		})
		return
	}
	check := Check{
		ID:      "deploy.firewall",
		Status:  "ok",
		Message: "macOS application firewall is disabled",
		Observed: map[string]any{
			"enabled": firewall.Enabled,
			"raw":     firewall.Raw,
		},
	}
	if firewall.Enabled {
		check.Status = "warn"
		check.Message = "macOS application firewall is enabled"
		check.SuggestedAction = "Allow `/usr/local/libexec/scenery-edge-helper` if inbound public traffic is blocked."
	}
	report.addCheck(check)
}

func addHTTPProbeCheck(ctx context.Context, report *Report, probe func(ctx context.Context, url string) HTTPProbeResult, id, name, url string) bool {
	result := probe(ctx, url)
	check := Check{
		ID:      id,
		Status:  "ok",
		Message: name + " reached " + url,
		Observed: map[string]any{
			"url":         url,
			"status_code": result.StatusCode,
		},
	}
	if !result.OK {
		if rawIPHTTPSNeedsSNI(url, result.Error) {
			check.Status = "skipped"
			check.Message = name + " skipped for raw IP because public HTTPS requires domain SNI"
			check.Observed["error"] = result.Error
			report.addCheck(check)
			return true
		}
		check.Status = "warn"
		check.Message = name + " failed for " + url
		check.SuggestedAction = "Verify the edge helper, Caddy, and router forwarding for TCP 80/443."
		if result.Error != "" {
			check.Observed["error"] = result.Error
		}
	}
	report.addCheck(check)
	return result.OK
}

func (report *Report) addCheck(check Check) {
	report.Checks = append(report.Checks, check)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
