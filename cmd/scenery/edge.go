package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/toolchain"
)

const (
	defaultEdgePublicAddr = "127.0.0.1:443"
	defaultEdgeTargetAddr = "127.0.0.1:19443"
	defaultEdgeDNSDomain  = localagent.DefaultRouteBaseDomain
	defaultEdgeDNSListen  = "127.0.0.1:53535"
	defaultEdgeDNSAddress = "127.0.0.1"
	edgeHighPortMin       = 19000
	edgeHighPortMax       = 19999

	edgeHelperLabel      = "dev.scenery.edge-helper"
	edgeHelperBinaryPath = "/usr/local/libexec/scenery-edge-helper"
	edgeHelperPlistPath  = "/Library/LaunchDaemons/dev.scenery.edge-helper.plist"
	edgeHelperSupportDir = "/Library/Application Support/Scenery/edge-helper"
	edgeHelperLogPath    = "/Library/Application Support/Scenery/edge-helper/edge-helper.log"

	legacyOnlavaEdgeHelperLabel      = "dev.onlava.edge-helper"
	legacyOnlavaEdgeHelperBinaryPath = "/usr/local/libexec/onlava-edge-helper"
	legacyOnlavaEdgeHelperPlistPath  = "/Library/LaunchDaemons/dev.onlava.edge-helper.plist"
)

// caddyStartupSettle is how long a freshly started Caddy edge process must
// stay up before it is considered started. Tests shorten it.
var caddyStartupSettle = 1500 * time.Millisecond

var (
	edgeDNSResolverStatusFunc        = edgeDNSResolverStatus
	edgeDNSResolverServesDomainFunc  = edgeDNSResolverServesDomain
	edgeDNSResolverFunctionalTimeout = 300 * time.Millisecond
)

type edgeOptions struct {
	JSON   bool
	Domain string
}

func edgeCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge install|trust|status|restart|uninstall|dns|privileged [--json]")
	}
	cmd := args[0]
	if cmd == "dns" {
		return edgeDNSCommand(args[1:])
	}
	if cmd == "dns-helper" {
		return edgeDNSHelperCommand(args[1:])
	}
	if cmd == "privileged" {
		return edgePrivilegedCommand(args[1:])
	}
	if cmd == "privileged-helper" {
		return edgePrivilegedHelperCommand(args[1:])
	}
	opts, err := parseEdgeArgs(args[1:])
	if err != nil {
		return err
	}
	switch cmd {
	case "install", "restart":
		return edgeRestart(opts)
	case "trust":
		return edgeTrust(opts)
	case "status":
		return edgeStatus(opts)
	case "uninstall":
		return edgeUninstall(opts)
	default:
		return fmt.Errorf("unknown edge command %q", cmd)
	}
}

func parseEdgeArgs(args []string) (edgeOptions, error) {
	var opts edgeOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--domain":
			if i+1 >= len(args) {
				return edgeOptions{}, fmt.Errorf("missing value for --domain")
			}
			i++
			opts.Domain = normalizeRouteNamespaceHost(args[i])
			if opts.Domain == "" {
				return edgeOptions{}, fmt.Errorf("--domain must be a valid domain")
			}
		default:
			return edgeOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func edgeDNSCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge dns install|status|restart|uninstall [--domain <domain>] [--json]")
	}
	cmd := args[0]
	opts, err := parseEdgeArgs(args[1:])
	if err != nil {
		return err
	}
	if opts.Domain == "" {
		opts.Domain = defaultEdgeDNSDomain
	}
	switch cmd {
	case "install", "restart":
		return edgeDNSInstall(opts)
	case "status":
		return edgeDNSStatus(opts)
	case "uninstall":
		return edgeDNSUninstall(opts)
	default:
		return fmt.Errorf("unknown edge dns command %q", cmd)
	}
}

func edgeRestart(opts edgeOptions) error {
	ctx := context.Background()
	publicAddr := defaultEdgePublicAddr
	targetAddr := defaultEdgeTargetAddr
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge install`; run `scenery system edge privileged install` for the privileged listener")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
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
	upstreamAddr := localagent.RouterAddrFromEnv()
	adminSocket := filepath.Join(paths.RunDir, "caddy-admin.sock")
	config := caddyEdgeConfig(caddyEdgeConfigOptions{
		ListenAddr:  targetAddr,
		PublicPort:  "443",
		Upstream:    upstreamAddr,
		AskURL:      "http://" + upstreamAddr + "/v1/tls/allow",
		AdminSocket: adminSocket,
		Token:       token,
	})
	if err := os.MkdirAll(filepath.Dir(paths.EdgeConfigPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte(config), 0o600); err != nil {
		return err
	}
	if err := stopEdge(paths, 2*time.Second); err != nil {
		return err
	}
	if err := startCaddyEdge(caddyBin, paths, publicAddr, targetAddr, adminSocket, upstreamAddr); err != nil {
		_ = localagent.WriteEdgeState(paths.EdgeStatePath, localagent.EdgeState{
			Kind:         localagent.EdgeKindCaddy,
			Status:       localagent.EdgeStatusStopped,
			PublicAddr:   publicAddr,
			PublicScheme: "https",
			HTTPSListen:  targetAddr,
			UpstreamAddr: upstreamAddr,
			AdminSocket:  adminSocket,
			ConfigPath:   paths.EdgeConfigPath,
			LogPath:      paths.EdgeLogPath,
			Error:        err.Error(),
		})
		return err
	}
	if err := ensureEdgeAgent(upstreamAddr, true); err != nil {
		refreshErr := err
		_ = stopEdge(paths, 2*time.Second)
		_ = localagent.WriteEdgeState(paths.EdgeStatePath, localagent.EdgeState{
			Kind:         localagent.EdgeKindCaddy,
			Status:       localagent.EdgeStatusStopped,
			PublicAddr:   publicAddr,
			PublicScheme: "https",
			HTTPSListen:  targetAddr,
			UpstreamAddr: upstreamAddr,
			AdminSocket:  adminSocket,
			ConfigPath:   paths.EdgeConfigPath,
			LogPath:      paths.EdgeLogPath,
			Error:        refreshErr.Error(),
		})
		return fmt.Errorf("refresh scenery agent after edge start: %w", refreshErr)
	}
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeEdgeStatusJSON(edgeStatusForStateDomain(paths, state, opts.Domain))
	}
	status := edgeStatusForStateDomain(paths, state, opts.Domain)
	if status.Ready {
		fmt.Fprintf(os.Stdout, "scenery system edge running at https://%s\n", state.PublicAddr)
		return nil
	}
	if !status.DNS.Ready {
		fmt.Fprintln(os.Stdout, "scenery system edge Caddy is prepared, but wildcard local DNS is not installed or healthy.")
		fmt.Fprintln(os.Stdout, "Run:")
		fmt.Fprintln(os.Stdout, "  scenery system edge dns install")
		return fmt.Errorf("scenery system edge dns is required for browser HTTPS routes under %s", defaultEdgeDNSDomain)
	}
	fmt.Fprintln(os.Stdout, "scenery system edge Caddy is prepared, but the privileged port 443 listener is not installed or healthy.")
	fmt.Fprintln(os.Stdout, "Run:")
	fmt.Fprintln(os.Stdout, "  scenery system edge privileged install")
	fmt.Fprintln(os.Stdout, "Do not run:")
	fmt.Fprintln(os.Stdout, "  sudo scenery system edge install")
	return fmt.Errorf("scenery system edge privileged listener is required for browser HTTPS on 127.0.0.1:443")
}

func edgeStatus(opts edgeOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return err
	}
	if state.Kind == "" {
		state = localagent.EdgeState{
			SchemaVersion: localagent.EdgeSchemaVersion,
			Kind:          localagent.EdgeKindCaddy,
			Status:        localagent.EdgeStatusStopped,
			PublicAddr:    defaultEdgePublicAddr,
			PublicScheme:  "https",
			ConfigPath:    paths.EdgeConfigPath,
			LogPath:       paths.EdgeLogPath,
			UpdatedAt:     time.Now().UTC(),
		}
	} else if !localagent.EdgeStateRunning(state) {
		state.Status = localagent.EdgeStatusStopped
	}
	status := edgeStatusForStateDomain(paths, state, opts.Domain)
	if opts.JSON {
		return writeEdgeStatusJSON(status)
	}
	fmt.Fprintf(os.Stdout, "scenery system edge %s", state.Status)
	if state.PublicAddr != "" {
		fmt.Fprintf(os.Stdout, " https://%s", state.PublicAddr)
	}
	if !status.PrivilegedListener.Installed {
		fmt.Fprintf(os.Stdout, " (privileged listener missing; run `scenery system edge privileged install`)")
	} else if status.PrivilegedListener.State != "running" {
		fmt.Fprintf(os.Stdout, " (privileged listener %s)", status.PrivilegedListener.State)
	} else if !status.DNS.Ready {
		fmt.Fprintf(os.Stdout, " (dns %s; run `scenery system edge dns install`)", status.DNS.DNSMasq.State)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func edgeTrust(opts edgeOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	caddyBin, err := resolveCaddyBinary(context.Background(), paths, true)
	if err != nil {
		return err
	}
	if err := trustCaddyLocalCA(caddyBin); err != nil {
		return err
	}
	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"schema_version": localagent.EdgeSchemaVersion,
			"kind":           localagent.EdgeKindCaddy,
			"status":         "trusted",
		})
	}
	fmt.Fprintln(os.Stdout, "trusted scenery Caddy local CA")
	return nil
}

func edgeUninstall(opts edgeOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if err := stopEdge(paths, 5*time.Second); err != nil {
		return err
	}
	state := localagent.EdgeState{
		Kind:         localagent.EdgeKindCaddy,
		Status:       localagent.EdgeStatusStopped,
		PublicAddr:   defaultEdgePublicAddr,
		PublicScheme: "https",
		ConfigPath:   paths.EdgeConfigPath,
		LogPath:      paths.EdgeLogPath,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := localagent.WriteEdgeState(paths.EdgeStatePath, state); err != nil {
		return err
	}
	_ = os.Remove(paths.EdgeTargetPath)
	if opts.JSON {
		return writeEdgeStatusJSON(edgeStatusForStateDomain(paths, state, opts.Domain))
	}
	fmt.Fprintln(os.Stdout, "stopped scenery system edge")
	fmt.Fprintln(os.Stdout, "privileged listener is still installed if previously configured")
	fmt.Fprintln(os.Stdout, "To remove port 443 listener:")
	fmt.Fprintln(os.Stdout, "  scenery system edge privileged uninstall")
	return nil
}

type edgeDNSState struct {
	SchemaVersion string    `json:"schema_version"`
	Status        string    `json:"status"`
	PID           int       `json:"pid,omitempty"`
	Domain        string    `json:"domain"`
	Listen        string    `json:"listen"`
	Address       string    `json:"address"`
	Executable    string    `json:"executable,omitempty"`
	ConfigPath    string    `json:"config_path,omitempty"`
	LogPath       string    `json:"log_path,omitempty"`
	ResolverPath  string    `json:"resolver_path,omitempty"`
	Error         string    `json:"error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type edgeDNSStatusResult struct {
	SchemaVersion  string               `json:"schema_version"`
	Ready          bool                 `json:"ready"`
	Domain         string               `json:"domain"`
	Address        string               `json:"address"`
	DNSMasq        edgeDNSMasqStatus    `json:"dnsmasq"`
	Resolver       edgeDNSResolverState `json:"resolver"`
	InstallCommand string               `json:"install_command"`
}

type edgeDNSMasqStatus struct {
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
	Listen     string `json:"listen,omitempty"`
	Executable string `json:"executable,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
	LogPath    string `json:"log_path,omitempty"`
	Error      string `json:"error,omitempty"`
}

type edgeDNSResolverState struct {
	Installed  bool   `json:"installed"`
	State      string `json:"state"`
	Path       string `json:"path,omitempty"`
	Domain     string `json:"domain,omitempty"`
	Nameserver string `json:"nameserver,omitempty"`
	Port       string `json:"port,omitempty"`
	Message    string `json:"message,omitempty"`
}

func edgeDNSInstall(opts edgeOptions) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge dns install`; run it as your normal user")
	}
	ctx := context.Background()
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	dnsmasqBin, err := resolveDNSMasqBinary(ctx, paths, true)
	if err != nil {
		return err
	}
	if err := stopEdgeDNS(paths, 2*time.Second); err != nil {
		return err
	}
	configPath := edgeDNSConfigPath(paths)
	logPath := edgeDNSLogPath(paths)
	domains := edgeDNSConfigDomains(opts.Domain)
	if err := os.WriteFile(configPath, []byte(dnsmasqEdgeConfig(domains, defaultEdgeDNSListen, defaultEdgeDNSAddress)), 0o600); err != nil {
		return err
	}
	if err := startEdgeDNS(dnsmasqBin, paths, opts.Domain, defaultEdgeDNSListen, defaultEdgeDNSAddress); err != nil {
		_ = writeEdgeDNSState(paths, edgeDNSState{
			Status:       "stopped",
			Domain:       opts.Domain,
			Listen:       defaultEdgeDNSListen,
			Address:      defaultEdgeDNSAddress,
			Executable:   dnsmasqBin,
			ConfigPath:   configPath,
			LogPath:      logPath,
			ResolverPath: edgeDNSResolverPath(opts.Domain),
			Error:        err.Error(),
			UpdatedAt:    time.Now().UTC(),
		})
		return err
	}
	if err := edgeDNSInstallResolver(opts.Domain, defaultEdgeDNSListen); err != nil {
		_ = stopEdgeDNS(paths, 2*time.Second)
		return err
	}
	status := edgeDNSStatusFor(paths, opts.Domain)
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}
	fmt.Fprintf(os.Stdout, "scenery system edge dns running for %s at %s\n", opts.Domain, defaultEdgeDNSListen)
	return nil
}

func edgeDNSStatus(opts edgeOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	status := edgeDNSStatusFor(paths, opts.Domain)
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}
	fmt.Fprintf(os.Stdout, "scenery system edge dns %s for %s", status.DNSMasq.State, status.Domain)
	if status.DNSMasq.Listen != "" {
		fmt.Fprintf(os.Stdout, " at %s", status.DNSMasq.Listen)
	}
	if status.Resolver.State != "installed" {
		fmt.Fprintf(os.Stdout, " (resolver %s; run `%s`)", status.Resolver.State, status.InstallCommand)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func edgeDNSUninstall(opts edgeOptions) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge dns uninstall`; run it as your normal user")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if opts.Domain == "" {
		opts.Domain = defaultEdgeDNSDomain
	}
	if err := stopEdgeDNS(paths, 5*time.Second); err != nil {
		return err
	}
	if err := edgeDNSUninstallResolver(opts.Domain); err != nil {
		return err
	}
	_ = os.Remove(edgeDNSStatePath(paths))
	if opts.JSON {
		return edgeDNSStatus(edgeOptions{JSON: true, Domain: opts.Domain})
	}
	fmt.Fprintf(os.Stdout, "stopped scenery system edge dns for %s\n", opts.Domain)
	return nil
}

func resolveDNSMasqBinary(ctx context.Context, paths localagent.Paths, download bool) (string, error) {
	return resolveDNSMasqBinaryInStore(ctx, edgeToolchainStoreDir(paths), download)
}

func resolveDNSMasqBinaryInStore(ctx context.Context, storeDir string, download bool) (string, error) {
	if status, err := managedToolchainArtifactStatusInDir(storeDir, "dnsmasq"); err == nil && status.ManagedPath != "" && isExecutableFile(status.ManagedPath) {
		return status.ManagedPath, nil
	}
	if !download {
		return "", fmt.Errorf("managed dnsmasq is not installed in %s; run `scenery system edge dns install` with downloads enabled", storeDir)
	}
	status, err := syncManagedToolchainArtifactInDir(ctx, storeDir, "dnsmasq")
	if err != nil {
		return "", fmt.Errorf("managed dnsmasq is not installed and could not be synced: %w", err)
	}
	if status.ManagedPath == "" || !isExecutableFile(status.ManagedPath) {
		return "", fmt.Errorf("managed dnsmasq is not installed in %s; run `scenery system edge dns install` with downloads enabled", storeDir)
	}
	return status.ManagedPath, nil
}

func dnsmasqEdgeConfig(domains []string, listen, address string) string {
	host, port := splitHostPort(listen)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "53535"
	}
	domains = normalizeEdgeDNSDomains(domains)
	if len(domains) == 0 {
		domains = []string{defaultEdgeDNSDomain}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `no-daemon
bind-interfaces
listen-address=%s
port=%s
`, host, port)
	for _, domain := range domains {
		fmt.Fprintf(&b, "address=/%s/%s\n", domain, address)
	}
	b.WriteString(`domain-needed
bogus-priv
no-resolv
`)
	return b.String()
}

func edgeDNSConfigDomains(domain string) []string {
	domains := []string{defaultEdgeDNSDomain, domain}
	if runtime.GOOS == "darwin" {
		domains = append(domains, managedEdgeDNSResolverDomains()...)
	}
	return normalizeEdgeDNSDomains(domains)
}

func managedEdgeDNSResolverDomains() []string {
	entries, err := os.ReadDir("/etc/resolver")
	if err != nil {
		return nil
	}
	var domains []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		domain := normalizeRouteNamespaceHost(entry.Name())
		if domain == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/etc/resolver", entry.Name()))
		if err != nil || !strings.Contains(string(data), "Managed by scenery edge dns") {
			continue
		}
		domains = append(domains, domain)
	}
	return domains
}

func normalizeEdgeDNSDomains(domains []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, domain := range domains {
		domain = normalizeRouteNamespaceHost(domain)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}

func startEdgeDNS(dnsmasqBin string, paths localagent.Paths, domain, listen, address string) error {
	logPath := edgeDNSLogPath(paths)
	logOffset := fileSize(logPath)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(dnsmasqBin, "--keep-in-foreground", "--conf-file="+edgeDNSConfigPath(paths))
	cmd.Env = envpolicy.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureDetachedChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	if err := waitForEdgeDNSStartup(listen, exitCh, logPath, logOffset, caddyStartupSettle); err != nil {
		_ = signalPID(cmd.Process.Pid, syscall.SIGTERM)
		_ = logFile.Close()
		return err
	}
	if err := writeEdgeDNSState(paths, edgeDNSState{
		Status:       "running",
		PID:          cmd.Process.Pid,
		Domain:       domain,
		Listen:       listen,
		Address:      address,
		Executable:   dnsmasqBin,
		ConfigPath:   edgeDNSConfigPath(paths),
		LogPath:      logPath,
		ResolverPath: edgeDNSResolverPath(domain),
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		_ = signalPID(cmd.Process.Pid, syscall.SIGTERM)
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return nil
}

func waitForEdgeDNSStartup(listen string, exitCh <-chan error, logPath string, logOffset int64, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exitCh:
			tail := tailFileFromOffset(logPath, logOffset, 4096)
			if tail != "" {
				return fmt.Errorf("dnsmasq exited during startup: %s", tail)
			}
			if err != nil {
				return fmt.Errorf("dnsmasq exited during startup: %w", err)
			}
			return fmt.Errorf("dnsmasq exited during startup")
		case <-deadline.C:
			tail := tailFileFromOffset(logPath, logOffset, 4096)
			if tail != "" {
				return fmt.Errorf("dnsmasq did not listen on %s within %s: %s", listen, timeout, tail)
			}
			return fmt.Errorf("dnsmasq did not listen on %s within %s", listen, timeout)
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", listen, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func stopEdgeDNS(paths localagent.Paths, timeout time.Duration) error {
	state, _ := loadEdgeDNSState(edgeDNSStatePath(paths))
	if state.PID <= 0 || !processAliveForEdge(state.PID) {
		return nil
	}
	_ = signalPID(state.PID, syscall.SIGTERM)
	deadline := time.Now().Add(timeout)
	for processAliveForEdge(state.PID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processAliveForEdge(state.PID) {
		return signalPID(state.PID, syscall.SIGKILL)
	}
	return nil
}

func edgeDNSStatusFor(paths localagent.Paths, domain string) edgeDNSStatusResult {
	if domain == "" {
		domain = defaultEdgeDNSDomain
	}
	domain = normalizeRouteNamespaceHost(domain)
	state, _ := loadEdgeDNSState(edgeDNSStatePath(paths))
	if state.Domain == "" {
		state.Domain = domain
		state.Listen = defaultEdgeDNSListen
		state.Address = defaultEdgeDNSAddress
		state.ConfigPath = edgeDNSConfigPath(paths)
		state.LogPath = edgeDNSLogPath(paths)
		state.ResolverPath = edgeDNSResolverPath(domain)
	}
	dnsState := state.Status
	if dnsState == "" {
		dnsState = "stopped"
	}
	if state.PID <= 0 || !processAliveForEdge(state.PID) {
		dnsState = "stopped"
	}
	configServesDomain := edgeDNSConfigServesDomain(state.ConfigPath, domain)
	if dnsState == "running" && !configServesDomain {
		dnsState = "mismatch"
	}
	resolver := edgeDNSResolverStatusFunc(domain, state.Listen)
	if dnsState == "stopped" && resolver.State == "installed" && edgeDNSResolverServesDomainFunc(domain, resolver.Nameserver, resolver.Port, firstNonEmpty(state.Address, defaultEdgeDNSAddress)) {
		dnsState = "external"
		configServesDomain = true
	}
	status := edgeDNSStatusResult{
		SchemaVersion:  "scenery.edge.dns.status.v1",
		Ready:          (dnsState == "running" || dnsState == "external") && resolver.State == "installed" && configServesDomain,
		Domain:         domain,
		Address:        firstNonEmpty(state.Address, defaultEdgeDNSAddress),
		InstallCommand: edgeDNSInstallCommand(domain),
	}
	status.DNSMasq.State = dnsState
	status.DNSMasq.PID = state.PID
	status.DNSMasq.Listen = firstNonEmpty(state.Listen, defaultEdgeDNSListen)
	status.DNSMasq.Executable = state.Executable
	status.DNSMasq.ConfigPath = state.ConfigPath
	status.DNSMasq.LogPath = state.LogPath
	status.DNSMasq.Error = state.Error
	if dnsState == "mismatch" && state.Error == "" {
		status.DNSMasq.Error = "dnsmasq config does not serve " + domain
	}
	status.Resolver = resolver
	return status
}

func edgeDNSResolverServesDomain(domain, nameserver, port, address string) bool {
	domain = normalizeRouteNamespaceHost(domain)
	if domain == "" || nameserver == "" || port == "" {
		return false
	}
	target := net.JoinHostPort(nameserver, port)
	resolver := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, network, target)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), edgeDNSResolverFunctionalTimeout)
	defer cancel()
	hosts, err := resolver.LookupHost(ctx, "scenery-edge-probe."+domain)
	if err != nil {
		return false
	}
	for _, host := range hosts {
		if host == address {
			return true
		}
	}
	return false
}

func edgeDNSConfigServesDomain(path, domain string) bool {
	domain = normalizeRouteNamespaceHost(domain)
	if path == "" || domain == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	needle := "address=/" + domain + "/"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), needle) {
			return true
		}
	}
	return false
}

func edgeDNSInstallCommand(domain string) string {
	if domain == "" || domain == defaultEdgeDNSDomain {
		return "scenery system edge dns install"
	}
	return "scenery system edge dns install --domain " + domain
}

func writeEdgeDNSState(paths localagent.Paths, state edgeDNSState) error {
	state.SchemaVersion = "scenery.edge.dns.state.v1"
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(edgeDNSStatePath(paths)), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(edgeDNSStatePath(paths), append(data, '\n'), 0o600)
}

func loadEdgeDNSState(path string) (edgeDNSState, error) {
	var state edgeDNSState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func edgeDNSConfigPath(paths localagent.Paths) string {
	return filepath.Join(paths.EdgeDir, "dnsmasq.conf")
}

func edgeDNSLogPath(paths localagent.Paths) string {
	return filepath.Join(paths.EdgeDir, "dnsmasq.log")
}

func edgeDNSStatePath(paths localagent.Paths) string {
	return filepath.Join(paths.RunDir, "edge-dns.json")
}

func edgeDNSInstallResolver(domain, listen string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge dns install`; run it as your normal user")
	}
	host, port := splitHostPort(listen)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "53535"
	}
	if edgeDNSResolverStatus(domain, net.JoinHostPort(host, port)).State == "installed" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	run := exec.Command("sudo", exe, "system", "edge", "dns-helper", "install", "--domain", domain, "--nameserver", host, "--port", port)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Stdin = os.Stdin
	return run.Run()
}

func edgeDNSUninstallResolver(domain string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge dns uninstall`; run it as your normal user")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	run := exec.Command("sudo", exe, "system", "edge", "dns-helper", "uninstall", "--domain", domain)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Stdin = os.Stdin
	return run.Run()
}

func edgeDNSResolverStatus(domain, listen string) edgeDNSResolverState {
	status := edgeDNSResolverState{
		State:  "unsupported",
		Domain: domain,
	}
	if runtime.GOOS != "darwin" {
		status.Message = "scoped resolver configuration is currently managed on macOS"
		return status
	}
	status.Path = edgeDNSResolverPath(domain)
	host, port := splitHostPort(listen)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "53535"
	}
	status.Nameserver = host
	status.Port = port
	data, err := os.ReadFile(status.Path)
	if err != nil {
		status.State = "missing"
		status.Message = "run `scenery system edge dns install`"
		return status
	}
	fields := parseResolverFile(string(data))
	if fields["domain"] == domain && fields["nameserver"] == host && fields["port"] == port {
		status.Installed = true
		status.State = "installed"
		return status
	}
	status.State = "mismatch"
	status.Message = "resolver file exists but does not match scenery system edge dns"
	return status
}

func parseResolverFile(data string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			fields[parts[0]] = parts[1]
		}
	}
	return fields
}

func edgeDNSResolverPath(domain string) string {
	domain = normalizeRouteNamespaceHost(domain)
	if domain == "" {
		domain = defaultEdgeDNSDomain
	}
	return filepath.Join("/etc/resolver", domain)
}

type edgeDNSHelperOptions struct {
	Domain     string
	Nameserver string
	Port       string
}

func edgeDNSHelperCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge dns-helper install|uninstall --domain <domain> [--nameserver <ip>] [--port <port>]")
	}
	cmd := args[0]
	opts, err := parseEdgeDNSHelperArgs(args[1:])
	if err != nil {
		return err
	}
	switch cmd {
	case "install":
		return edgeDNSHelperInstall(opts)
	case "uninstall":
		return edgeDNSHelperUninstall(opts)
	default:
		return fmt.Errorf("unknown edge dns-helper command %q", cmd)
	}
}

func parseEdgeDNSHelperArgs(args []string) (edgeDNSHelperOptions, error) {
	opts := edgeDNSHelperOptions{Nameserver: "127.0.0.1", Port: "53535"}
	for i := 0; i < len(args); i++ {
		value := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		switch args[i] {
		case "--domain":
			raw, err := value(args[i])
			if err != nil {
				return edgeDNSHelperOptions{}, err
			}
			opts.Domain = normalizeRouteNamespaceHost(raw)
		case "--nameserver":
			raw, err := value(args[i])
			if err != nil {
				return edgeDNSHelperOptions{}, err
			}
			opts.Nameserver = strings.TrimSpace(raw)
		case "--port":
			raw, err := value(args[i])
			if err != nil {
				return edgeDNSHelperOptions{}, err
			}
			opts.Port = strings.TrimSpace(raw)
		default:
			return edgeDNSHelperOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if opts.Domain == "" {
		return edgeDNSHelperOptions{}, fmt.Errorf("--domain is required")
	}
	if net.ParseIP(opts.Nameserver) == nil {
		return edgeDNSHelperOptions{}, fmt.Errorf("--nameserver must be an IP address")
	}
	if _, err := strconv.Atoi(opts.Port); err != nil || opts.Port == "" {
		return edgeDNSHelperOptions{}, fmt.Errorf("--port must be an integer")
	}
	return opts, nil
}

func edgeDNSHelperInstall(opts edgeDNSHelperOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery system edge dns-helper install is currently supported on macOS")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery system edge dns-helper install must run as root; use `scenery system edge dns install`")
	}
	if err := os.MkdirAll("/etc/resolver", 0o755); err != nil {
		return err
	}
	content := edgeDNSResolverFile(opts.Domain, opts.Nameserver, opts.Port)
	if err := os.WriteFile(edgeDNSResolverPath(opts.Domain), []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "installed scenery resolver for %s\n", opts.Domain)
	return nil
}

func edgeDNSHelperUninstall(opts edgeDNSHelperOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery system edge dns-helper uninstall is currently supported on macOS")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery system edge dns-helper uninstall must run as root; use `scenery system edge dns uninstall`")
	}
	path := edgeDNSResolverPath(opts.Domain)
	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), "Managed by scenery edge dns") {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stdout, "removed scenery resolver for %s\n", opts.Domain)
	return nil
}

func edgeDNSResolverFile(domain, nameserver, port string) string {
	return fmt.Sprintf(`# Managed by scenery edge dns
domain %s
nameserver %s
port %s
`, domain, nameserver, port)
}

func writeEdgeStatusJSON(status edgeStatusResult) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(status)
}

type edgeStatusResult struct {
	SchemaVersion      string                       `json:"schema_version"`
	Ready              bool                         `json:"ready"`
	PublicBase         string                       `json:"public_base"`
	Edge               edgeStatusCaddy              `json:"edge"`
	DNS                edgeDNSStatusResult          `json:"dns"`
	PrivilegedListener edgeStatusPrivilegedListener `json:"privileged_listener"`
}

type edgeStatusCaddy struct {
	Kind        string `json:"kind"`
	State       string `json:"state"`
	PID         int    `json:"pid,omitempty"`
	UID         int    `json:"uid,omitempty"`
	HTTPSListen string `json:"https_listen,omitempty"`
	Upstream    string `json:"upstream,omitempty"`
	AgentRouter string `json:"agent_router,omitempty"`
	Admin       string `json:"admin,omitempty"`
	ConfigPath  string `json:"config_path,omitempty"`
	LogPath     string `json:"log_path,omitempty"`
	Error       string `json:"error,omitempty"`
}

type edgeStatusPrivilegedListener struct {
	Strategy                 string   `json:"strategy"`
	Installed                bool     `json:"installed"`
	State                    string   `json:"state"`
	PID                      int      `json:"pid,omitempty"`
	Listen                   []string `json:"listen,omitempty"`
	Target                   string   `json:"target,omitempty"`
	OwnerUID                 int      `json:"owner_uid,omitempty"`
	OwnerGID                 int      `json:"owner_gid,omitempty"`
	Version                  string   `json:"version,omitempty"`
	RequiredForPortlessHTTPS bool     `json:"required_for_portless_https,omitempty"`
	InstallCommand           string   `json:"install_command,omitempty"`
	Message                  string   `json:"message,omitempty"`
}

func edgeStatusForStateDomain(paths localagent.Paths, state localagent.EdgeState, domain string) edgeStatusResult {
	edgeState := state.Status
	if edgeState == "" {
		edgeState = localagent.EdgeStatusStopped
	}
	if !localagent.EdgeStateRunning(state) {
		edgeState = localagent.EdgeStatusStopped
	}
	caddyUID, _ := processUID(state.PID)
	helper := privilegedListenerStatus(paths)
	agentRouter := liveAgentRouterAddr(paths)
	dns := edgeDNSStatusFor(paths, domain)
	return edgeStatusResult{
		SchemaVersion: "scenery.edge.status.v1",
		Ready:         edgeState == localagent.EdgeStatusRunning && dns.Ready && helper.State == "running" && helper.Target == state.HTTPSListen && agentRouter == state.UpstreamAddr,
		PublicBase:    publicBaseForEdge(state.PublicAddr),
		Edge: edgeStatusCaddy{
			Kind:        localagent.EdgeKindCaddy,
			State:       edgeState,
			PID:         state.PID,
			UID:         caddyUID,
			HTTPSListen: state.HTTPSListen,
			Upstream:    state.UpstreamAddr,
			AgentRouter: agentRouter,
			Admin:       unixURL(state.AdminSocket),
			ConfigPath:  state.ConfigPath,
			LogPath:     state.LogPath,
			Error:       state.Error,
		},
		DNS:                dns,
		PrivilegedListener: helper,
	}
}

func liveAgentRouterAddr(paths localagent.Paths) string {
	client := localagent.NewClient(paths.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	health, err := client.Health(ctx)
	if err != nil {
		return ""
	}
	return health.RouterAddr
}

func publicBaseForEdge(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultEdgePublicAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "https://" + addr
	}
	if port == "443" {
		return "https://" + host
	}
	return "https://" + addr
}

func unixURL(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return "unix://" + path
}

func resolveCaddyBinary(ctx context.Context, paths localagent.Paths, download bool) (string, error) {
	return resolveCaddyBinaryInStore(ctx, edgeToolchainStoreDir(paths), download)
}

func resolveCaddyBinaryInStore(ctx context.Context, storeDir string, download bool) (string, error) {
	if status, err := managedToolchainArtifactStatusInDir(storeDir, "caddy"); err == nil && status.ManagedPath != "" && isExecutableFile(status.ManagedPath) {
		return status.ManagedPath, nil
	}
	if !download {
		return "", fmt.Errorf("managed Caddy is not installed in %s; system PATH binaries are not used for managed toolchain artifacts; run `scenery system edge install` with downloads enabled", storeDir)
	}
	status, err := syncManagedToolchainArtifactInDir(ctx, storeDir, "caddy")
	if err != nil {
		return "", fmt.Errorf("managed Caddy is not installed and could not be synced: %w", err)
	}
	if status.ManagedPath == "" || !isExecutableFile(status.ManagedPath) {
		return "", fmt.Errorf("managed Caddy is not installed in %s; run `scenery system edge install` with downloads enabled", storeDir)
	}
	return status.ManagedPath, nil
}

func edgeToolchainStoreDir(paths localagent.Paths) string {
	if strings.TrimSpace(envpolicy.Get("SCENERY_TOOLCHAIN_DIR")) != "" {
		return toolchainStoreDirForStateRoot("")
	}
	if strings.TrimSpace(paths.Home) == "" {
		return toolchain.DefaultStoreDir("")
	}
	return filepath.Join(paths.Home, "toolchain")
}

func trustCaddyLocalCA(caddyBin string) error {
	dir, err := os.MkdirTemp("", "scenery-caddy-trust-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	adminSocket := filepath.Join(dir, "admin.sock")
	configPath := filepath.Join(dir, "Caddyfile")
	logPath := filepath.Join(dir, "caddy.log")
	if err := os.WriteFile(configPath, []byte(caddyTrustConfig(adminSocket)), 0o600); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	run := exec.Command(caddyBin, "run", "--config", configPath, "--adapter", "caddyfile")
	run.Env = envpolicy.Environ()
	run.Stdout = logFile
	run.Stderr = logFile
	run.Stdin = nil
	configureChildProcess(run)
	if err := run.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- run.Wait()
	}()
	if err := waitForCaddyAdminSocket(adminSocket, exitCh, logPath, 15*time.Second); err != nil {
		_ = logFile.Close()
		return err
	}
	trust := exec.Command(caddyBin, "trust", "--config", configPath, "--adapter", "caddyfile")
	trust.Env = envpolicy.Environ()
	trust.Stdout = os.Stdout
	trust.Stderr = os.Stderr
	err = trust.Run()
	if stopErr := stopStartedCaddy(run.Process.Pid, exitCh, 2*time.Second); err == nil {
		err = stopErr
	}
	_ = logFile.Close()
	return err
}

func caddyTrustConfig(adminSocket string) string {
	return fmt.Sprintf(`{
	local_certs
	admin unix//%s
}
`, adminSocket)
}

func waitForCaddyAdminSocket(adminSocket string, exitCh <-chan error, logPath string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exitCh:
			tail := tailFile(logPath, 4096)
			if tail != "" {
				return fmt.Errorf("temporary Caddy trust server exited during startup: %s", tail)
			}
			if err != nil {
				return fmt.Errorf("temporary Caddy trust server exited during startup: %w", err)
			}
			return fmt.Errorf("temporary Caddy trust server exited during startup")
		case <-deadline.C:
			tail := tailFile(logPath, 4096)
			if tail != "" {
				return fmt.Errorf("temporary Caddy trust server did not expose admin socket %s within %s: %s", adminSocket, timeout, tail)
			}
			return fmt.Errorf("temporary Caddy trust server did not expose admin socket %s within %s", adminSocket, timeout)
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", adminSocket, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func stopStartedCaddy(pid int, exitCh <-chan error, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	_ = signalPID(pid, syscall.SIGTERM)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-exitCh:
		return err
	case <-timer.C:
		_ = signalPID(pid, syscall.SIGKILL)
		return fmt.Errorf("timed out waiting for temporary Caddy trust server pid %d to stop", pid)
	}
}

func ensureEdgeToken(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func ensureEdgeAgent(routerAddr string, force bool) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	client := localagent.NewClient(paths.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	health, running := currentAgentHealth(ctx, client)
	if running && !force && health.RouterAddr == routerAddr && health.RouterScheme == "http" {
		return nil
	}
	if running && health.PID > 0 {
		if err := signalAgentPID(health.PID); err != nil {
			return fmt.Errorf("stop scenery agent pid %d: %w", health.PID, err)
		}
		if err := waitForAgentStop(ctx, client, health.PID); err != nil {
			return err
		}
	}
	if err := stopStaleEdgeAgentProcesses(paths.SocketPath, routerAddr, health.PID, 2*time.Second); err != nil {
		return err
	}
	if err := waitForTCPAddrFree(ctx, routerAddr); err != nil {
		return err
	}
	logOffset := fileSize(paths.LogPath)
	if err := localagent.StartProcess(paths, localagent.StartOptions{
		RouterAddr: routerAddr,
		RouterHTTP: true,
	}); err != nil {
		return err
	}
	started, err := waitForAgentStart(ctx, client, health.PID, paths.LogPath, logOffset)
	if err != nil {
		return err
	}
	return validateEdgeAgentHealth(started, routerAddr)
}

func validateEdgeAgentHealth(health localagent.HealthResponse, routerAddr string) error {
	if health.RouterAddr != routerAddr {
		return fmt.Errorf("restarted scenery agent listened on %s, want %s for edge upstream; free %s and rerun `scenery system edge install`", health.RouterAddr, routerAddr, routerAddr)
	}
	return nil
}

func stopStaleEdgeAgentProcesses(socketPath, routerAddr string, skipPID int, timeout time.Duration) error {
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		command := strings.Join(fields[2:], " ")
		if pidErr != nil || uidErr != nil || uid != os.Getuid() || pid <= 0 || pid == skipPID {
			continue
		}
		if edgeAgentCommandMatches(command, socketPath, routerAddr) {
			pids = append(pids, pid)
		}
	}
	for _, pid := range pids {
		if err := signalPID(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale scenery system edge agent pid %d: %w", pid, err)
		}
	}
	deadline := time.Now().Add(timeout)
	for _, pid := range pids {
		for processAliveForEdge(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAliveForEdge(pid) {
			if err := signalPID(pid, syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill stale scenery system edge agent pid %d: %w", pid, err)
			}
		}
	}
	return nil
}

func edgeAgentCommandMatches(command, socketPath, routerAddr string) bool {
	return strings.Contains(command, "scenery system agent") &&
		strings.Contains(command, "--socket "+socketPath) &&
		strings.Contains(command, "--router-listen "+routerAddr)
}

func waitForTCPAddrFree(ctx context.Context, addr string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for edge upstream %s to become available: %w", addr, lastErr)
		case <-ticker.C:
		}
	}
}

type caddyEdgeConfigOptions struct {
	ListenAddr  string
	PublicPort  string
	Upstream    string
	AskURL      string
	AdminSocket string
	Token       string
}

func caddyEdgeConfig(opts caddyEdgeConfigOptions) string {
	host, listenPort := splitHostPort(opts.ListenAddr)
	if host == "" {
		host = "127.0.0.1"
	}
	if listenPort == "" {
		listenPort = "19443"
	}
	publicPort := strings.TrimSpace(opts.PublicPort)
	if publicPort == "" {
		publicPort = "443"
	}
	site := "https://:" + listenPort
	return fmt.Sprintf(`{
	default_bind %s
	auto_https disable_redirects
	local_certs
	on_demand_tls {
		ask %s
	}
	admin unix//%s
	servers {
		strict_sni_host on
	}
}

%s {
	tls internal {
		on_demand
	}
	reverse_proxy %s {
		flush_interval -1
		header_up Host {host}
		header_up X-Forwarded-Proto https
		header_up X-Forwarded-Port %s
		header_up X-Scenery-Edge-Token %s
	}
}
`, host, opts.AskURL, opts.AdminSocket, site, opts.Upstream, publicPort, opts.Token)
}

func startCaddyEdge(caddyBin string, paths localagent.Paths, publicAddr, targetAddr, adminSocket, upstreamAddr string) error {
	logOffset := fileSize(paths.EdgeLogPath)
	logFile, err := os.OpenFile(paths.EdgeLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(caddyBin, "run", "--config", paths.EdgeConfigPath, "--adapter", "caddyfile")
	cmd.Env = envpolicy.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureDetachedChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	if err := waitForCaddyEdgeStartup(exitCh, paths.EdgeLogPath, logOffset, caddyStartupSettle); err != nil {
		_ = os.Remove(adminSocket)
		_ = logFile.Close()
		return err
	}
	startedAt, _ := processStartTime(cmd.Process.Pid)
	if err := localagent.WriteEdgeTargetState(paths.EdgeTargetPath, localagent.EdgeTargetState{
		Kind:         localagent.EdgeKindCaddy,
		TargetAddr:   targetAddr,
		PID:          cmd.Process.Pid,
		OwnerUID:     os.Getuid(),
		OwnerGID:     os.Getgid(),
		ProcessStart: startedAt,
		Executable:   caddyBin,
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		_ = signalPID(cmd.Process.Pid, syscall.SIGTERM)
		_ = logFile.Close()
		return err
	}
	state := localagent.EdgeState{
		Kind:         localagent.EdgeKindCaddy,
		Status:       localagent.EdgeStatusRunning,
		PID:          cmd.Process.Pid,
		PublicAddr:   publicAddr,
		PublicScheme: "https",
		HTTPSListen:  targetAddr,
		UpstreamAddr: upstreamAddr,
		AdminSocket:  adminSocket,
		ConfigPath:   paths.EdgeConfigPath,
		LogPath:      paths.EdgeLogPath,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := localagent.WriteEdgeState(paths.EdgeStatePath, state); err != nil {
		_ = signalPID(cmd.Process.Pid, syscall.SIGTERM)
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return nil
}

func edgePrivilegedCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge privileged install|status|uninstall [--json]")
	}
	cmd := args[0]
	opts, err := parseEdgeArgs(args[1:])
	if err != nil {
		return err
	}
	switch cmd {
	case "install":
		return edgePrivilegedInstall()
	case "status":
		return edgePrivilegedStatus(opts)
	case "uninstall":
		return edgePrivilegedUninstall()
	default:
		return fmt.Errorf("unknown edge privileged command %q", cmd)
	}
}

func edgePrivilegedInstall() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery system edge privileged install is currently supported on macOS")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge privileged install`; run it as your normal user so Scenery can record the expected owner")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{
		exe, "system", "edge", "privileged-helper", "install",
		"--owner-uid", strconv.Itoa(os.Getuid()),
		"--owner-gid", strconv.Itoa(os.Getgid()),
		"--owner-home", paths.Home,
		"--helper-target-state", paths.EdgeTargetPath,
		"--router-addr", localagent.RouterAddrFromEnv(),
	}
	run := exec.Command("sudo", args...)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Stdin = os.Stdin
	if err := run.Run(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "installed scenery privileged edge listener for 127.0.0.1:443")
	return nil
}

func edgePrivilegedStatus(opts edgeOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	status := privilegedListenerStatus(paths)
	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	fmt.Fprintf(os.Stdout, "scenery system edge privileged listener %s", status.State)
	if status.Target != "" {
		fmt.Fprintf(os.Stdout, " -> %s", status.Target)
	}
	if !status.Installed {
		fmt.Fprintf(os.Stdout, " (run `scenery system edge privileged install`)")
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func edgePrivilegedUninstall() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery system edge privileged uninstall is currently supported on macOS")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge privileged uninstall`; run it as your normal user")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	run := exec.Command("sudo", exe, "system", "edge", "privileged-helper", "uninstall")
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Stdin = os.Stdin
	return run.Run()
}

type edgeHelperOptions struct {
	OwnerUID          int
	OwnerGID          int
	OwnerHome         string
	HelperTargetState string
	RouterAddr        string
}

func edgePrivilegedHelperCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge privileged-helper install|run|uninstall")
	}
	cmd := args[0]
	opts, err := parseEdgeHelperArgs(args[1:])
	if err != nil {
		return err
	}
	switch cmd {
	case "install":
		return edgePrivilegedHelperInstall(opts)
	case "run":
		return edgePrivilegedHelperRun(opts)
	case "uninstall":
		return edgePrivilegedHelperUninstall()
	default:
		return fmt.Errorf("unknown edge privileged-helper command %q", cmd)
	}
}

func parseEdgeHelperArgs(args []string) (edgeHelperOptions, error) {
	var opts edgeHelperOptions
	for i := 0; i < len(args); i++ {
		value := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		switch args[i] {
		case "--owner-uid":
			raw, err := value(args[i])
			if err != nil {
				return edgeHelperOptions{}, err
			}
			uid, err := strconv.Atoi(raw)
			if err != nil {
				return edgeHelperOptions{}, fmt.Errorf("--owner-uid must be an integer")
			}
			opts.OwnerUID = uid
		case "--owner-gid":
			raw, err := value(args[i])
			if err != nil {
				return edgeHelperOptions{}, err
			}
			gid, err := strconv.Atoi(raw)
			if err != nil {
				return edgeHelperOptions{}, fmt.Errorf("--owner-gid must be an integer")
			}
			opts.OwnerGID = gid
		case "--owner-home":
			raw, err := value(args[i])
			if err != nil {
				return edgeHelperOptions{}, err
			}
			opts.OwnerHome = raw
		case "--helper-target-state":
			raw, err := value(args[i])
			if err != nil {
				return edgeHelperOptions{}, err
			}
			opts.HelperTargetState = raw
		case "--router-addr":
			raw, err := value(args[i])
			if err != nil {
				return edgeHelperOptions{}, err
			}
			opts.RouterAddr = raw
		default:
			return edgeHelperOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func requireEdgeHelperOwnerOptions(opts edgeHelperOptions) error {
	if opts.OwnerUID <= 0 {
		return fmt.Errorf("--owner-uid is required")
	}
	if opts.OwnerGID <= 0 {
		return fmt.Errorf("--owner-gid is required")
	}
	if strings.TrimSpace(opts.OwnerHome) == "" {
		return fmt.Errorf("--owner-home is required")
	}
	if strings.TrimSpace(opts.HelperTargetState) == "" {
		return fmt.Errorf("--helper-target-state is required")
	}
	return nil
}

func edgePrivilegedHelperInstall(opts edgeHelperOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery system edge privileged helper install is currently supported on macOS")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery system edge privileged-helper install must run as root; use `scenery system edge privileged install`")
	}
	if err := requireEdgeHelperOwnerOptions(opts); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(edgeHelperBinaryPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(edgeHelperSupportDir, 0o755); err != nil {
		return err
	}
	if err := copyRootHelperBinary(exe, edgeHelperBinaryPath); err != nil {
		return err
	}
	stopLegacyOnlavaEdgeHelper()
	if err := stopStaleRootCaddyEdge(opts.OwnerHome, 2*time.Second); err != nil {
		return err
	}
	if err := stopStaleRootSceneryEdgeAgent(opts.OwnerHome, opts.RouterAddr, 2*time.Second); err != nil {
		return err
	}
	plist := edgeHelperPlist(opts)
	if err := os.WriteFile(edgeHelperPlistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", "system/"+edgeHelperLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", "system", edgeHelperPlistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("launchctl", "kickstart", "-k", "system/"+edgeHelperLabel).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl kickstart: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func stopLegacyOnlavaEdgeHelper() {
	_ = exec.Command("launchctl", "bootout", "system/"+legacyOnlavaEdgeHelperLabel).Run()
	_ = os.Remove(legacyOnlavaEdgeHelperPlistPath)
	_ = os.Remove(legacyOnlavaEdgeHelperBinaryPath)
}

func edgePrivilegedHelperUninstall() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery system edge privileged helper uninstall is currently supported on macOS")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery system edge privileged-helper uninstall must run as root; use `scenery system edge privileged uninstall`")
	}
	_ = exec.Command("launchctl", "bootout", "system/"+edgeHelperLabel).Run()
	_ = os.Remove(edgeHelperPlistPath)
	_ = os.Remove(edgeHelperBinaryPath)
	fmt.Fprintln(os.Stdout, "removed scenery privileged edge listener")
	return nil
}

func edgePrivilegedHelperRun(opts edgeHelperOptions) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("scenery system edge privileged-helper run must run as root")
	}
	if err := requireEdgeHelperOwnerOptions(opts); err != nil {
		return err
	}
	listeners := make([]net.Listener, 0, 2)
	for _, addr := range []string{"127.0.0.1:443", "[::1]:443"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			return fmt.Errorf("listen %s: %w", addr, err)
		}
		listeners = append(listeners, ln)
	}
	errCh := make(chan error, len(listeners))
	for _, ln := range listeners {
		go acceptEdgeHelperLoop(ln, opts, errCh)
	}
	return <-errCh
}

func acceptEdgeHelperLoop(ln net.Listener, opts edgeHelperOptions, errCh chan<- error) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		go handleEdgeHelperConn(conn, opts)
	}
}

func handleEdgeHelperConn(client net.Conn, opts edgeHelperOptions) {
	defer client.Close()
	targetAddr, err := validateEdgeTarget(opts.HelperTargetState, opts.OwnerUID, opts.OwnerGID)
	if err != nil {
		return
	}
	upstream, err := net.DialTimeout("tcp", targetAddr, 2*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		_ = client.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	<-done
}

func validateEdgeTarget(path string, ownerUID, ownerGID int) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("edge target metadata %s must not be group/world writable", path)
	}
	if err := validateEdgeTargetOwner(path, info, ownerUID, ownerGID); err != nil {
		return "", err
	}
	state, err := localagent.LoadEdgeTargetState(path)
	if err != nil {
		return "", err
	}
	if state.SchemaVersion != localagent.EdgeTargetSchemaVersion || state.Kind != localagent.EdgeKindCaddy {
		return "", fmt.Errorf("edge target metadata has unexpected kind")
	}
	host, portRaw, err := net.SplitHostPort(state.TargetAddr)
	if err != nil {
		return "", fmt.Errorf("edge target address %q is invalid: %w", state.TargetAddr, err)
	}
	if !isLoopbackHost(host) {
		return "", fmt.Errorf("edge target address must be loopback")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < edgeHighPortMin || port > edgeHighPortMax {
		return "", fmt.Errorf("edge target port must be in %d-%d", edgeHighPortMin, edgeHighPortMax)
	}
	if !processAliveForEdge(state.PID) {
		return "", fmt.Errorf("edge target pid %d is not running", state.PID)
	}
	uid, err := processUID(state.PID)
	if err != nil || uid != ownerUID {
		return "", fmt.Errorf("edge target pid %d owner mismatch", state.PID)
	}
	if state.ProcessStart != "" {
		startedAt, err := processStartTime(state.PID)
		if err != nil || startedAt != state.ProcessStart {
			return "", fmt.Errorf("edge target pid %d start time mismatch", state.PID)
		}
	}
	if state.Executable != "" {
		command, err := processCommand(state.PID)
		if err != nil {
			return "", fmt.Errorf("edge target pid %d command check failed: %w", state.PID, err)
		}
		if !strings.Contains(command, state.Executable) && filepath.Base(state.Executable) != "caddy" {
			return "", fmt.Errorf("edge target pid %d is not the managed Caddy process", state.PID)
		}
	}
	return state.TargetAddr, nil
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func copyRootHelperBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chown(tmp, 0, 0); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func edgeHelperPlist(opts edgeHelperOptions) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>system</string>
		<string>edge</string>
		<string>privileged-helper</string>
		<string>run</string>
		<string>--owner-uid</string>
		<string>%d</string>
		<string>--owner-gid</string>
		<string>%d</string>
		<string>--owner-home</string>
		<string>%s</string>
		<string>--helper-target-state</string>
		<string>%s</string>
		<string>--router-addr</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, escapePlistString(edgeHelperLabel), escapePlistString(edgeHelperBinaryPath), opts.OwnerUID, opts.OwnerGID, escapePlistString(opts.OwnerHome), escapePlistString(opts.HelperTargetState), escapePlistString(firstNonEmpty(opts.RouterAddr, localagent.RouterAddrFromEnv())), escapePlistString(edgeHelperLogPath), escapePlistString(edgeHelperLogPath))
}

func escapePlistString(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	).Replace(value)
}

func privilegedListenerStatus(paths localagent.Paths) edgeStatusPrivilegedListener {
	status := edgeStatusPrivilegedListener{
		Strategy:                 "helper",
		State:                    "missing",
		Listen:                   []string{"127.0.0.1:443", "[::1]:443"},
		RequiredForPortlessHTTPS: true,
		InstallCommand:           "scenery system edge privileged install",
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(edgeHelperPlistPath); err == nil {
			status.Installed = true
			status.State = "stopped"
		}
	} else {
		status.Message = "privileged edge helper is currently supported on macOS"
		return status
	}
	target, err := localagent.LoadEdgeTargetState(paths.EdgeTargetPath)
	if err == nil && target.TargetAddr != "" {
		status.Target = target.TargetAddr
		status.OwnerUID = target.OwnerUID
		status.OwnerGID = target.OwnerGID
	}
	if !status.Installed {
		return status
	}
	launchState, pid, err := edgeHelperLaunchStatus()
	if err == nil {
		status.PID = pid
		if launchState != "" {
			status.State = launchState
		}
	}
	if status.State == "running" && edgePortReachable("127.0.0.1:443") {
		status.State = "running"
	} else if status.State == "running" {
		status.State = "unhealthy"
	}
	return status
}

func edgeHelperLaunchStatus() (string, int, error) {
	out, err := exec.Command("launchctl", "print", "system/"+edgeHelperLabel).CombinedOutput()
	if err != nil {
		return "", 0, err
	}
	return parseEdgeHelperLaunchStatus(string(out))
}

func parseEdgeHelperLaunchStatus(output string) (string, int, error) {
	var state string
	var pid int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if state == "" && strings.HasPrefix(line, "state = ") {
			state = strings.TrimSpace(strings.TrimPrefix(line, "state = "))
		}
		if pid == 0 && strings.HasPrefix(line, "pid = ") {
			parsed, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
			if err == nil {
				pid = parsed
			}
		}
	}
	return state, pid, nil
}

func stopStaleRootCaddyEdge(ownerHome string, timeout time.Duration) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("stale root Caddy cleanup must run as root")
	}
	configPath := filepath.Join(ownerHome, "agent", "edge", "Caddyfile")
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		command := strings.Join(fields[2:], " ")
		if pidErr != nil || uidErr != nil || uid != 0 || pid <= 0 {
			continue
		}
		if !strings.Contains(command, "caddy run") || !strings.Contains(command, "--config "+configPath) {
			continue
		}
		if err := signalPID(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale root Caddy edge pid %d: %w", pid, err)
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if !processAliveForEdge(pid) {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := signalPID(pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("kill stale root Caddy edge pid %d: %w", pid, err)
		}
		return nil
	}
	return nil
}

func stopStaleRootSceneryEdgeAgent(ownerHome, routerAddr string, timeout time.Duration) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("stale root agent cleanup must run as root")
	}
	socketPath := filepath.Join(ownerHome, "run", "agent.sock")
	routerAddr = strings.TrimSpace(routerAddr)
	if routerAddr == "" {
		routerAddr = localagent.RouterAddrFromEnv()
	}
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		command := strings.Join(fields[2:], " ")
		if pidErr != nil || uidErr != nil || uid != 0 || pid <= 0 {
			continue
		}
		if !edgeAgentCommandMatches(command, socketPath, routerAddr) {
			continue
		}
		if err := signalPID(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale root scenery system edge agent pid %d: %w", pid, err)
		}
		deadline := time.Now().Add(timeout)
		for processAliveForEdge(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAliveForEdge(pid) {
			if err := signalPID(pid, syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill stale root scenery system edge agent pid %d: %w", pid, err)
			}
		}
	}
	return nil
}

func edgePortReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForCaddyEdgeStartup(exitCh <-chan error, logPath string, logOffset int64, settle time.Duration) error {
	timer := time.NewTimer(settle)
	defer timer.Stop()
	select {
	case err := <-exitCh:
		tail := tailFileFromOffset(logPath, logOffset, 4096)
		if tail != "" {
			return fmt.Errorf("Caddy edge exited during startup: %s", tail)
		}
		if err != nil {
			return fmt.Errorf("Caddy edge exited during startup: %w", err)
		}
		return fmt.Errorf("Caddy edge exited during startup")
	case <-timer.C:
		return nil
	}
}

func stopEdge(paths localagent.Paths, timeout time.Duration) error {
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return err
	}
	if state.PID <= 0 || !processAliveForEdge(state.PID) {
		return nil
	}
	if err := signalPID(state.PID, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAliveForEdge(state.PID) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for Caddy edge pid %d to stop", state.PID)
}

func signalPID(pid int, signal os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func processAliveForEdge(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	return !processZombieForEdge(pid)
}

func processZombieForEdge(pid int) bool {
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

func processUID(pid int) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("pid must be positive")
	}
	out, err := exec.Command("ps", "-o", "uid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func processStartTime(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive")
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func processCommand(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive")
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func tailFile(path string, limit int64) string {
	return tailFileFromOffset(path, 0, limit)
}

func tailFileFromOffset(path string, offset, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err == nil {
		if offset < 0 {
			offset = 0
		}
		if offset > info.Size() {
			offset = info.Size()
		}
		if info.Size()-offset > limit {
			offset = info.Size() - limit
		}
		_, _ = file.Seek(offset, io.SeekStart)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", ""
	}
	return host, port
}
