package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/netprobe"
	"scenery.sh/internal/toolchain"
)

const (
	defaultEdgePublicAddr     = "127.0.0.1:443"
	defaultEdgeTargetAddr     = "127.0.0.1:19443"
	defaultEdgeHTTPTargetAddr = "127.0.0.1:19080"
	defaultEdgeDNSDomain      = localagent.DefaultRouteBaseDomain
	defaultEdgeDNSListen      = "127.0.0.1:53535"
	defaultEdgeDNSAddress     = "127.0.0.1"
	edgeHighPortMin           = 19000
	edgeHighPortMax           = 19999

	edgeHelperLabel      = "dev.scenery.edge-helper"
	edgeHelperBinaryPath = "/usr/local/libexec/scenery-edge-helper"
	edgeHelperPlistPath  = "/Library/LaunchDaemons/dev.scenery.edge-helper.plist"
	edgeHelperSupportDir = "/Library/Application Support/Scenery/edge-helper"
	edgeHelperLogPath    = "/Library/Application Support/Scenery/edge-helper/edge-helper.log"
)

// caddyStartupSettle is how long a freshly started Caddy edge process must
// stay up before it is considered started. Tests shorten it.
var caddyStartupSettle = 1500 * time.Millisecond

var (
	edgeDNSResolverStatusFunc       = edgelifecycle.DNSResolverStatus
	edgeDNSResolverServesDomainFunc = edgelifecycle.DNSResolverServesDomain
	edgeHelperLaunchStatusFunc      = edgeHelperLaunchStatus
	edgeHelperPlistOptionsFunc      = installedEdgeHelperOptions
	reloadCaddyEdgeConfigFunc       = reloadCaddyEdgeConfig
	validateCaddyEdgeConfigFunc     = edgelifecycle.ValidateCaddyConfig
)

type edgeOptions struct {
	JSON   bool
	Domain string
	Deploy bool
}

func edgeCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge install|trust|status|restart|uninstall|dns|privileged [-o json]")
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
	flags := newCLIFlagSet("system edge")
	registerJSONOutput(flags, &opts.JSON)
	flags.StringVar(&opts.Domain, "domain", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return edgeOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return edgeOptions{}, err
	}
	if opts.Domain != "" {
		opts.Domain = normalizeRouteNamespaceHost(opts.Domain)
		if opts.Domain == "" {
			return edgeOptions{}, fmt.Errorf("--domain must be a valid domain")
		}
	}
	return opts, nil
}

func edgeDNSCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge dns install|status|restart|uninstall [--domain <domain>] [-o json]")
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
	// A systemd-owned edge must converge through its unit: spawning an
	// unsupervised Caddy would race the unit's Restart=always respawn for
	// the public ports.
	if edgeSystemdManaged() {
		paths, err := localagent.DefaultPaths()
		if err != nil {
			return err
		}
		return edgeRestartSystemd(paths)
	}
	ctx := context.Background()
	publicAddr := defaultEdgePublicAddr
	targetAddr := defaultEdgeTargetAddr
	httpTargetAddr := defaultEdgeHTTPTargetAddr
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
	operationLock, err := localagent.AcquireProcessLock(paths.EdgeLockPath + ".operation")
	if err != nil {
		return fmt.Errorf("another edge operation is running: %w", err)
	}
	defer operationLock.Release()
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
	config, err := edgelifecycle.CaddyConfigForRegistry(paths, targetAddr, httpTargetAddr, upstreamAddr, adminSocket, token)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.EdgeConfigPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte(config), 0o600); err != nil {
		return err
	}
	if err := stopEdge(paths, 2*time.Second); err != nil {
		return err
	}
	if err := stopStaleUserCaddyEdges(paths, 2*time.Second); err != nil {
		return err
	}
	if err := startCaddyEdge(caddyBin, paths, publicAddr, targetAddr, httpTargetAddr, adminSocket, upstreamAddr); err != nil {
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
	target, err := localagent.LoadEdgeTargetState(paths.EdgeTargetPath)
	if err != nil {
		_ = stopEdge(paths, 2*time.Second)
		return fmt.Errorf("load edge helper target metadata after Caddy start: %w", err)
	}
	if err := publishEdgeTargetForHelper(paths, target); err != nil {
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
	fmt.Fprintf(os.Stdout, "  %s\n", edgePrivilegedInstallCommand(opts.Deploy))
	fmt.Fprintln(os.Stdout, "Do not run:")
	fmt.Fprintln(os.Stdout, "  sudo scenery system edge install")
	return fmt.Errorf("scenery system edge privileged listener is required for browser HTTPS on 127.0.0.1:443")
}

func edgePrivilegedInstallCommand(deploy bool) string {
	if deploy {
		return "scenery deploy setup"
	}
	return "scenery system edge privileged install"
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
			ArtifactIdentity: localagent.NewEdgeStateIdentity(),
			Kind:             localagent.EdgeKindCaddy,
			Status:           localagent.EdgeStatusStopped,
			PublicAddr:       defaultEdgePublicAddr,
			PublicScheme:     "https",
			ConfigPath:       paths.EdgeConfigPath,
			LogPath:          paths.EdgeLogPath,
			UpdatedAt:        time.Now().UTC(),
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
	if err := edgelifecycle.TrustLocalCA(caddyBin, os.Stdout, os.Stderr); err != nil {
		return err
	}
	if opts.JSON {
		return writeCLIJSON(os.Stdout, withCLIPayloadIdentity("scenery.edge.trust", map[string]any{
			"edge_kind": localagent.EdgeKindCaddy,
			"status":    "trusted",
		}))
	}
	fmt.Fprintln(os.Stdout, "trusted scenery Caddy local CA")
	return nil
}

func edgeUninstall(opts edgeOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	previousState, _ := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err := stopEdge(paths, 5*time.Second); err != nil {
		return err
	}
	removePublishedEdgeTargetForHelper(paths, previousState)
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

type edgeDNSStatusResult struct {
	cliPayloadIdentity
	Ready          bool                           `json:"ready"`
	Domain         string                         `json:"domain"`
	Address        string                         `json:"address"`
	DNSMasq        edgeDNSMasqStatus              `json:"dnsmasq"`
	Resolver       edgelifecycle.DNSResolverState `json:"resolver"`
	InstallCommand string                         `json:"install_command"`
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
	configPath := edgelifecycle.DNSConfigPath(paths)
	logPath := edgelifecycle.DNSLogPath(paths)
	domains := edgelifecycle.DNSConfigDomains(opts.Domain)
	if err := os.WriteFile(configPath, []byte(edgelifecycle.DNSMasqConfig(domains, defaultEdgeDNSListen, defaultEdgeDNSAddress)), 0o600); err != nil {
		return err
	}
	if err := startEdgeDNS(dnsmasqBin, paths, opts.Domain, defaultEdgeDNSListen, defaultEdgeDNSAddress); err != nil {
		_ = edgelifecycle.WriteDNSState(paths, edgelifecycle.DNSState{
			Status:       "stopped",
			Domain:       opts.Domain,
			Listen:       defaultEdgeDNSListen,
			Address:      defaultEdgeDNSAddress,
			Executable:   dnsmasqBin,
			ConfigPath:   configPath,
			LogPath:      logPath,
			ResolverPath: edgelifecycle.DNSResolverPath(opts.Domain),
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
		return writeCLIJSON(os.Stdout, status)
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
		return writeCLIJSON(os.Stdout, status)
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
	_ = os.Remove(edgelifecycle.DNSStatePath(paths))
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

func startEdgeDNS(dnsmasqBin string, paths localagent.Paths, domain, listen, address string) error {
	logPath := edgelifecycle.DNSLogPath(paths)
	logOffset := fileSize(logPath)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(dnsmasqBin, "--keep-in-foreground", "--conf-file="+edgelifecycle.DNSConfigPath(paths))
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
	if err := edgelifecycle.WriteDNSState(paths, edgelifecycle.DNSState{
		Status:       "running",
		PID:          cmd.Process.Pid,
		Domain:       domain,
		Listen:       listen,
		Address:      address,
		Executable:   dnsmasqBin,
		ConfigPath:   edgelifecycle.DNSConfigPath(paths),
		LogPath:      logPath,
		ResolverPath: edgelifecycle.DNSResolverPath(domain),
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
	listening := false
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
			if listening {
				return nil
			}
			tail := tailFileFromOffset(logPath, logOffset, 4096)
			if tail != "" {
				return fmt.Errorf("dnsmasq did not listen on %s within %s: %s", listen, timeout, tail)
			}
			return fmt.Errorf("dnsmasq did not listen on %s within %s", listen, timeout)
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", listen, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				listening = true
			}
		}
	}
}

func stopEdgeDNS(paths localagent.Paths, timeout time.Duration) error {
	state, _ := edgelifecycle.LoadDNSState(edgelifecycle.DNSStatePath(paths))
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
	state, _ := edgelifecycle.LoadDNSState(edgelifecycle.DNSStatePath(paths))
	if state.Domain == "" {
		state.Domain = domain
		state.Listen = defaultEdgeDNSListen
		state.Address = defaultEdgeDNSAddress
		state.ConfigPath = edgelifecycle.DNSConfigPath(paths)
		state.LogPath = edgelifecycle.DNSLogPath(paths)
		state.ResolverPath = edgelifecycle.DNSResolverPath(domain)
	}
	dnsState := state.Status
	if dnsState == "" {
		dnsState = "stopped"
	}
	if state.PID <= 0 || !processAliveForEdge(state.PID) {
		dnsState = "stopped"
	}
	configServesDomain := edgelifecycle.DNSConfigServesDomain(state.ConfigPath, domain)
	if dnsState == "running" && !configServesDomain {
		dnsState = "mismatch"
	}
	resolver := edgeDNSResolverStatusFunc(domain, state.Listen)
	if dnsState == "stopped" && resolver.State == "installed" && edgeDNSResolverServesDomainFunc(domain, resolver.Nameserver, resolver.Port, firstNonEmpty(state.Address, defaultEdgeDNSAddress)) {
		dnsState = "external"
		configServesDomain = true
	}
	status := edgeDNSStatusResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.edge.dns.status"),
		Ready:              (dnsState == "running" || dnsState == "external") && resolver.State == "installed" && configServesDomain,
		Domain:             domain,
		Address:            firstNonEmpty(state.Address, defaultEdgeDNSAddress),
		InstallCommand:     edgelifecycle.DNSInstallCommand(domain),
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
	if edgelifecycle.DNSResolverStatus(domain, net.JoinHostPort(host, port)).State == "installed" {
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
	flags := newCLIFlagSet("system edge dns-helper")
	flags.StringVar(&opts.Domain, "domain", "", "")
	flags.StringVar(&opts.Nameserver, "nameserver", opts.Nameserver, "")
	flags.StringVar(&opts.Port, "port", opts.Port, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return edgeDNSHelperOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return edgeDNSHelperOptions{}, err
	}
	opts.Domain = normalizeRouteNamespaceHost(opts.Domain)
	opts.Nameserver, opts.Port = strings.TrimSpace(opts.Nameserver), strings.TrimSpace(opts.Port)
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
	content := edgelifecycle.DNSResolverFile(opts.Domain, opts.Nameserver, opts.Port)
	if err := os.WriteFile(edgelifecycle.DNSResolverPath(opts.Domain), []byte(content), 0o644); err != nil {
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
	path := edgelifecycle.DNSResolverPath(opts.Domain)
	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), "Managed by scenery edge dns") {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stdout, "removed scenery resolver for %s\n", opts.Domain)
	return nil
}

func writeEdgeStatusJSON(status edgeStatusResult) error {
	return writeCLIJSON(os.Stdout, status)
}

type edgeStatusResult struct {
	cliPayloadIdentity
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
	TargetPath               string   `json:"target_path,omitempty"`
	TargetPID                int      `json:"target_pid,omitempty"`
	OwnerUID                 int      `json:"owner_uid,omitempty"`
	OwnerGID                 int      `json:"owner_gid,omitempty"`
	Version                  string   `json:"version,omitempty"`
	ContractRevision         string   `json:"contract_revision,omitempty"`
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
	helperTargetsState := helper.Target == state.HTTPSListen && helper.TargetPID == state.PID
	return edgeStatusResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.edge.status"),
		Ready:              edgeState == localagent.EdgeStatusRunning && dns.Ready && helper.State == "running" && helperTargetsState && agentRouter == state.UpstreamAddr,
		PublicBase:         publicBaseForEdge(state.PublicAddr),
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
	// Under launchd supervision a SIGTERM respawn immediately rebinds the
	// router port, so the stop/wait-for-free dance below would race the
	// supervisor; restart through launchd instead.
	if started, supervised, err := restartAgentViaSupervisor(ctx, client, paths, health, running); supervised {
		if err != nil {
			return err
		}
		return validateEdgeAgentHealth(started, routerAddr)
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
		strings.Contains(command, "--router-listen "+routerAddr)
}

func waitForTCPAddrFree(ctx context.Context, addr string) error {
	if err := netprobe.WaitBindFree(ctx, addr, 50*time.Millisecond); err != nil {
		return fmt.Errorf("timed out waiting for edge upstream %s to become available: %w", addr, err)
	}
	return nil
}

func startCaddyEdge(caddyBin string, paths localagent.Paths, publicAddr, targetAddr, httpTargetAddr, adminSocket, upstreamAddr string) error {
	return edgelifecycle.Start(edgelifecycle.StartConfig{
		Binary:         caddyBin,
		Paths:          paths,
		PublicAddr:     publicAddr,
		TargetAddr:     targetAddr,
		HTTPTargetAddr: httpTargetAddr,
		AdminSocket:    adminSocket,
		UpstreamAddr:   upstreamAddr,
		StartupSettle:  caddyStartupSettle,
	})
}

func edgeReloadFromRegistry(paths localagent.Paths, state localagent.EdgeState) error {
	caddyBin, err := resolveCaddyBinary(context.Background(), paths, true)
	if err != nil {
		return err
	}
	token, err := ensureEdgeToken(paths.EdgeTokenPath)
	if err != nil {
		return err
	}
	targetAddr := firstNonEmpty(state.HTTPSListen, defaultEdgeTargetAddr)
	httpTargetAddr := defaultEdgeHTTPTargetAddr
	upstreamAddr := firstNonEmpty(state.UpstreamAddr, localagent.RouterAddrFromEnv())
	adminSocket := firstNonEmpty(state.AdminSocket, filepath.Join(paths.RunDir, "caddy-admin.sock"))
	config, err := edgelifecycle.CaddyConfigForRegistry(paths, targetAddr, httpTargetAddr, upstreamAddr, adminSocket, token)
	if err != nil {
		return err
	}
	nextPath := paths.EdgeConfigPath + ".next"
	if err := os.MkdirAll(filepath.Dir(nextPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(nextPath, []byte(config), 0o600); err != nil {
		return err
	}
	// Never hand Caddy an unvalidated candidate: a reload of a broken
	// config must fail here with the previous config still active.
	if err := validateCaddyEdgeConfigFunc(caddyBin, nextPath); err != nil {
		_ = os.Remove(nextPath)
		return err
	}
	if err := reloadCaddyEdgeConfigFunc(caddyBin, nextPath, adminSocket); err != nil {
		_ = os.Remove(nextPath)
		return edgeRestart(edgeOptions{})
	}
	if err := os.Rename(nextPath, paths.EdgeConfigPath); err != nil {
		return err
	}
	return refreshEdgeTargetMetadata(paths, state, targetAddr, httpTargetAddr)
}

func reloadCaddyEdgeConfig(caddyBin, configPath, adminSocket string) error {
	return edgelifecycle.Reload(caddyBin, configPath, adminSocket)
}

func refreshEdgeTargetMetadata(paths localagent.Paths, state localagent.EdgeState, targetAddr, httpTargetAddr string) error {
	target, _ := localagent.LoadEdgeTargetState(paths.EdgeTargetPath)
	if target.Kind == "" {
		target.Kind = localagent.EdgeKindCaddy
	}
	if target.PID == 0 {
		target.PID = state.PID
	}
	if target.OwnerUID == 0 {
		target.OwnerUID = os.Getuid()
	}
	if target.OwnerGID == 0 {
		target.OwnerGID = os.Getgid()
	}
	if target.ProcessStart == "" && target.PID > 0 {
		target.ProcessStart, _ = processStartTime(target.PID)
	}
	target.TargetAddr = targetAddr
	target.HTTPTargetAddr = httpTargetAddr
	target.UpdatedAt = time.Now().UTC()
	if err := localagent.WriteEdgeTargetState(paths.EdgeTargetPath, target); err != nil {
		return err
	}
	return publishEdgeTargetForHelper(paths, target)
}

func edgePrivilegedCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery system edge privileged install|status|uninstall [-o json]")
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
		return writeCLIJSON(os.Stdout, status)
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
	targetPath := paths.EdgeTargetPath
	helperOpts, helperOptsErr := edgeHelperPlistOptionsFunc()
	if helperOptsErr == nil {
		status.Listen = edgeHelperListenAddrs(edgeHelperListenSpecs(helperOpts))
		status.Version = strings.TrimSpace(helperOpts.HelperVersion)
		status.ContractRevision = strings.TrimSpace(helperOpts.HelperContract)
	}
	if helperOptsErr == nil && strings.TrimSpace(helperOpts.HelperTargetState) != "" {
		targetPath = filepath.Clean(helperOpts.HelperTargetState)
	}
	status.TargetPath = targetPath
	target, err := localagent.LoadEdgeTargetState(targetPath)
	if err == nil && target.TargetAddr != "" {
		status.Target = target.TargetAddr
		status.TargetPID = target.PID
		status.OwnerUID = target.OwnerUID
		status.OwnerGID = target.OwnerGID
	}
	if !status.Installed {
		return status
	}
	launchState, pid, err := edgeHelperLaunchStatusFunc()
	if err == nil {
		status.PID = pid
		if launchState != "" {
			status.State = launchState
		}
	}
	if helperOptsErr == nil && strings.TrimSpace(helperOpts.HelperTargetState) != "" && filepath.Clean(helperOpts.HelperTargetState) != filepath.Clean(paths.EdgeTargetPath) {
		status.Message = fmt.Sprintf("privileged helper target metadata is %s; current agent home target is %s", filepath.Clean(helperOpts.HelperTargetState), filepath.Clean(paths.EdgeTargetPath))
	}
	if status.State == "running" {
		if helperOptsErr != nil {
			status.State = "unhealthy"
			status.Message = fmt.Sprintf("privileged helper install metadata is unreadable: %v", helperOptsErr)
			return status
		}
		if _, err := validateEdgeTarget(targetPath, helperOpts.OwnerUID, helperOpts.OwnerGID); err != nil {
			status.State = "unhealthy"
			status.Message = fmt.Sprintf("privileged helper target metadata %s is not healthy: %v", targetPath, err)
			return status
		}
		if helperOpts.Public {
			if _, err := validateEdgeTargetForPort(targetPath, helperOpts.OwnerUID, helperOpts.OwnerGID, true); err != nil {
				status.State = "unhealthy"
				status.Message = fmt.Sprintf("privileged helper HTTP target metadata %s is not healthy: %v", targetPath, err)
				return status
			}
		}
	}
	if status.State == "running" {
		applyEdgeHelperForwardingProbe(&status, edgeProbeServerName(paths))
	}
	return status
}

// applyEdgeHelperForwardingProbe downgrades a launchd-running helper to
// unhealthy when it does not actually forward TLS. A live PID and a bound
// listener are not readiness: the helper accepts TCP and then drops the
// connection whenever it cannot validate its target metadata, which is
// exactly how an outdated helper fails after a scenery upgrade. Only a
// TLS-level reply from the Caddy target through port 443 counts as
// forwarding; when the helper drops while Caddy answers TLS directly, the
// helper itself is the fault and the fix is a reinstall.
func applyEdgeHelperForwardingProbe(status *edgeStatusPrivilegedListener, serverName string) {
	through := edgeTLSProbeFunc("127.0.0.1:443", serverName, edgeTLSProbeTimeout)
	switch through.Outcome {
	case edgeTLSProbeHandshakeOK, edgeTLSProbeForwarded:
		return
	case edgeTLSProbeUnreachable:
		status.State = "unhealthy"
		status.Message = "port 443 did not accept a TCP connection: " + through.Error
		return
	}
	direct := edgeTLSProbeResult{Outcome: edgeTLSProbeUnreachable}
	if status.Target != "" {
		direct = edgeTLSProbeFunc(status.Target, serverName, edgeTLSProbeTimeout)
	}
	if direct.reachedTLSServer() {
		status.State = "unhealthy"
		status.Message = fmt.Sprintf("port 443 accepts TCP but the running privileged helper drops connections before TLS reaches Caddy (%s answers TLS directly); the installed helper likely predates the current handoff contract. Run `scenery deploy setup` (public) or `scenery system edge privileged install` (loopback) to replace and restart it.", status.Target)
		return
	}
	if status.Message == "" {
		status.Message = "privileged helper is fail-closed because the Caddy edge target did not answer TLS"
	}
}

func edgeHelperListenAddrs(specs []edgeHelperListenSpec) []string {
	addrs := make([]string, 0, len(specs))
	for _, spec := range specs {
		addrs = append(addrs, spec.Addr)
	}
	return addrs
}

func edgeHelperLaunchStatus() (string, int, error) {
	out, err := exec.Command("launchctl", "print", "system/"+edgeHelperLabel).CombinedOutput()
	if err != nil {
		return "", 0, err
	}
	return edgelifecycle.ParseHelperLaunchStatus(string(out))
}

func installedEdgeHelperOptions() (edgeHelperOptions, error) {
	data, err := os.ReadFile(edgeHelperPlistPath)
	if err != nil {
		return edgeHelperOptions{}, err
	}
	return parseEdgeHelperPlistOptions(data)
}

func parseEdgeHelperPlistOptions(data []byte) (edgeHelperOptions, error) {
	args, err := edgelifecycle.ParseHelperPlistProgramArguments(data)
	if err != nil {
		return edgeHelperOptions{}, err
	}
	return parseEdgeHelperProgramArguments(args)
}

func parseEdgeHelperProgramArguments(args []string) (edgeHelperOptions, error) {
	runArgs, err := edgelifecycle.HelperRunArguments(args)
	if err != nil {
		return edgeHelperOptions{}, err
	}
	opts, err := parseEdgeHelperArgs(runArgs)
	if err != nil {
		return edgeHelperOptions{}, err
	}
	if err := requireEdgeHelperOwnerOptions(opts); err != nil {
		return edgeHelperOptions{}, err
	}
	return opts, nil
}

func helperTargetStatePath(paths localagent.Paths) (string, bool) {
	opts, err := edgeHelperPlistOptionsFunc()
	if err != nil || strings.TrimSpace(opts.HelperTargetState) == "" {
		return paths.EdgeTargetPath, false
	}
	return filepath.Clean(opts.HelperTargetState), true
}

func publishEdgeTargetForHelper(paths localagent.Paths, target localagent.EdgeTargetState) error {
	helperTargetPath, ok := helperTargetStatePath(paths)
	if !ok || helperTargetPath == filepath.Clean(paths.EdgeTargetPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(helperTargetPath), 0o700); err != nil {
		return fmt.Errorf("prepare privileged edge helper target metadata %s: %w", helperTargetPath, err)
	}
	if err := localagent.WriteEdgeTargetState(helperTargetPath, target); err != nil {
		return fmt.Errorf("publish privileged edge helper target metadata %s: %w", helperTargetPath, err)
	}
	return nil
}

func removePublishedEdgeTargetForHelper(paths localagent.Paths, state localagent.EdgeState) {
	helperTargetPath, ok := helperTargetStatePath(paths)
	if !ok || helperTargetPath == filepath.Clean(paths.EdgeTargetPath) {
		return
	}
	target, err := localagent.LoadEdgeTargetState(helperTargetPath)
	if err != nil {
		return
	}
	if target.PID == state.PID && target.TargetAddr == state.HTTPSListen {
		_ = os.Remove(helperTargetPath)
	}
}
