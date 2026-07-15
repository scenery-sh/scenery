package deploydiag

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultLANIP discovers this host's LAN IP via `ipconfig getifaddr` on the
// primary interfaces.
func DefaultLANIP(ctx context.Context) (string, error) {
	if runtime.GOOS == "darwin" {
		for _, iface := range []string{"en0", "en1"} {
			cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			out, err := exec.CommandContext(cmdCtx, "ipconfig", "getifaddr", iface).Output()
			cancel()
			if err == nil && strings.TrimSpace(string(out)) != "" {
				return strings.TrimSpace(string(out)), nil
			}
		}
		return "", fmt.Errorf("ipconfig getifaddr en0/en1 returned no address")
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip := ipNet.IP.To4(); ip != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no non-loopback IPv4 interface address found")
}

// DefaultHTTPProbe performs one GET reachability probe without following
// redirects and without TLS trust validation.
func DefaultHTTPProbe(ctx context.Context, rawURL string) HTTPProbeResult {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local reachability probe, not trust validation.
	client := http.Client{
		Timeout:   3 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return HTTPProbeResult{Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return HTTPProbeResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	return HTTPProbeResult{OK: true, StatusCode: resp.StatusCode}
}

// DefaultPublicIP discovers this host's public IP via api.ipify.org.
func DefaultPublicIP(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("api.ipify.org returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(data))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("api.ipify.org returned %q", ip)
	}
	return ip, nil
}

// DefaultPowerStatus inspects the macOS on-power sleep setting via `pmset`.
// On other platforms it reports Supported false.
func DefaultPowerStatus(ctx context.Context) (PowerStatus, error) {
	if runtime.GOOS != "darwin" {
		return PowerStatus{Supported: false}, nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "pmset", "-g").CombinedOutput()
	if err != nil {
		return PowerStatus{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	raw := string(out)
	return PowerStatus{Supported: true, SleepMinutes: parsePMSetSleepMinutes(raw), Raw: strings.TrimSpace(raw)}, nil
}

// DefaultFirewallStatus inspects the macOS application firewall global state
// via `socketfilterfw`. On other platforms it reports Supported false.
func DefaultFirewallStatus(ctx context.Context) (FirewallStatus, error) {
	if runtime.GOOS != "darwin" {
		return FirewallStatus{Supported: false}, nil
	}
	candidates := []string{"/usr/libexec/ApplicationFirewall/socketfilterfw", "socketfilterfw"}
	var lastErr error
	for _, name := range candidates {
		cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, err := exec.CommandContext(cmdCtx, name, "--getglobalstate").CombinedOutput()
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
			continue
		}
		raw := strings.TrimSpace(string(out))
		enabled := strings.Contains(strings.ToLower(raw), "enabled")
		return FirewallStatus{Supported: true, Enabled: enabled, Raw: raw}, nil
	}
	return FirewallStatus{}, lastErr
}

// DefaultPortListener reports the process listening on a local TCP port using
// lsof, falling back to netstat when lsof yields nothing.
func DefaultPortListener(port int) (PortListenerInfo, bool, error) {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").CombinedOutput()
	if err == nil {
		if info, ok := parseLsofPortListener(string(out), port); ok {
			return hydratePortListenerCommand(info), true, nil
		}
	} else {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return PortListenerInfo{}, false, err
		}
	}

	out, err = exec.Command("netstat", "-anv", "-p", "tcp").CombinedOutput()
	if err != nil {
		return PortListenerInfo{}, false, nil
	}
	if info, ok := parseNetstatPortListener(string(out), port); ok {
		return hydratePortListenerCommand(info), true, nil
	}
	return PortListenerInfo{}, false, nil
}

func parseLsofPortListener(output string, port int) (PortListenerInfo, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "COMMAND" {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		info := PortListenerInfo{
			Port:    port,
			PID:     pid,
			Command: fields[0],
			Name:    fields[len(fields)-1],
		}
		if len(fields) > 8 {
			info.Name = strings.Join(fields[8:], " ")
		}
		return info, true
	}
	return PortListenerInfo{}, false
}

func parseNetstatPortListener(output string, port int) (PortListenerInfo, bool) {
	portSuffix := "." + strconv.Itoa(port)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "Proto" || fields[5] != "LISTEN" || !strings.HasSuffix(fields[3], portSuffix) {
			continue
		}
		for _, field := range fields {
			name, rawPID, ok := strings.Cut(field, ":")
			if !ok {
				continue
			}
			pid, err := strconv.Atoi(rawPID)
			if err != nil || pid <= 0 {
				continue
			}
			return PortListenerInfo{
				Port:    port,
				PID:     pid,
				Command: name,
				Name:    fields[3],
			}, true
		}
	}
	return PortListenerInfo{}, false
}

func hydratePortListenerCommand(info PortListenerInfo) PortListenerInfo {
	if info.PID > 0 {
		if cmdline, err := exec.Command("ps", "-p", strconv.Itoa(info.PID), "-o", "command=").Output(); err == nil && strings.TrimSpace(string(cmdline)) != "" {
			info.Command = strings.TrimSpace(string(cmdline))
		}
	}
	return info
}

func parsePMSetSleepMinutes(raw string) int {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "sleep" {
			minutes, _ := strconv.Atoi(fields[1])
			return minutes
		}
	}
	return 0
}

func rawIPHTTPSNeedsSNI(rawURL, errText string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || net.ParseIP(u.Hostname()) == nil {
		return false
	}
	return strings.Contains(errText, "tls: internal error") || strings.Contains(errText, "tlsv1 alert internal error")
}

func urlForHost(scheme, host string) string {
	host = strings.TrimSpace(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + "/"
}

func listenerIsWildcard(listener PortListenerInfo, configured []string, port int) bool {
	name := strings.ToLower(listener.Name)
	portSuffix := ":" + strconv.Itoa(port)
	for _, token := range []string{"*" + portSuffix, "0.0.0.0" + portSuffix, "[::]" + portSuffix, "::" + portSuffix} {
		if strings.Contains(name, strings.ToLower(token)) {
			return true
		}
	}
	for _, addr := range configured {
		if strings.TrimSpace(addr) == "0.0.0.0"+portSuffix || strings.TrimSpace(addr) == "[::]"+portSuffix {
			return true
		}
	}
	return false
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

func ipsContain(ips []net.IP, want string) bool {
	parsed := net.ParseIP(strings.TrimSpace(want))
	if parsed == nil {
		return false
	}
	for _, ip := range ips {
		if ip.Equal(parsed) {
			return true
		}
	}
	return false
}

func ipsAreCloudflareProxy(ips []net.IP) bool {
	if len(ips) == 0 {
		return false
	}
	// ponytail: static Cloudflare ranges; refresh from https://www.cloudflare.com/ips/ if they change.
	cidrs := []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22", "2400:cb00::/32",
		"2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
		"2a06:98c0::/29", "2c0f:f248::/32",
	}
	for _, ip := range ips {
		if ip == nil {
			return false
		}
		matched := false
		for _, cidr := range cidrs {
			_, network, err := net.ParseCIDR(cidr)
			if err == nil && network.Contains(ip) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func ipIsCGNATHint(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip)).To4()
	if parsed == nil {
		return false
	}
	return parsed[0] == 100 && parsed[1] >= 64 && parsed[1] <= 127
}
