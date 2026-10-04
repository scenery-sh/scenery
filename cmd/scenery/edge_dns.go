package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
	"scenery.sh/internal/envpolicy"
)

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
	paths, err := commandAgentPaths()
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
	_, _ = fmt.Fprintf(os.Stdout, "scenery system edge dns running for %s at %s\n", opts.Domain, defaultEdgeDNSListen)
	return nil
}

func edgeDNSStatus(opts edgeOptions) error {
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	status := edgeDNSStatusFor(paths, opts.Domain)
	if opts.JSON {
		return writeCLIJSON(os.Stdout, status)
	}
	_, _ = fmt.Fprintf(os.Stdout, "scenery system edge dns %s for %s", status.DNSMasq.State, status.Domain)
	if status.DNSMasq.Listen != "" {
		_, _ = fmt.Fprintf(os.Stdout, " at %s", status.DNSMasq.Listen)
	}
	if status.Resolver.State != "installed" {
		_, _ = fmt.Fprintf(os.Stdout, " (resolver %s; run `%s`)", status.Resolver.State, status.InstallCommand)
	}
	_, _ = fmt.Fprintln(os.Stdout)
	return nil
}

func edgeDNSUninstall(opts edgeOptions) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge dns uninstall`; run it as your normal user")
	}
	paths, err := commandAgentPaths()
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
	_, _ = fmt.Fprintf(os.Stdout, "stopped scenery system edge dns for %s\n", opts.Domain)
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
		return usageErrorf("usage: scenery system edge dns-helper install|uninstall --domain <domain> [--nameserver <ip>] [--port <port>]")
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
		return usageErrorf("unknown edge dns-helper command %q", cmd)
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
		return edgeDNSHelperOptions{}, usageErrorf("--domain is required")
	}
	if net.ParseIP(opts.Nameserver) == nil {
		return edgeDNSHelperOptions{}, usageErrorf("--nameserver must be an IP address")
	}
	if _, err := strconv.Atoi(opts.Port); err != nil || opts.Port == "" {
		return edgeDNSHelperOptions{}, usageErrorf("--port must be an integer")
	}
	return opts, nil
}

func edgeDNSHelperInstall(opts edgeDNSHelperOptions) error {
	if runtime.GOOS != "darwin" {
		return unavailableErrorf("scenery system edge dns-helper install is currently supported on macOS")
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
	_, _ = fmt.Fprintf(os.Stdout, "installed scenery resolver for %s\n", opts.Domain)
	return nil
}

func edgeDNSHelperUninstall(opts edgeDNSHelperOptions) error {
	if runtime.GOOS != "darwin" {
		return unavailableErrorf("scenery system edge dns-helper uninstall is currently supported on macOS")
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
	_, _ = fmt.Fprintf(os.Stdout, "removed scenery resolver for %s\n", opts.Domain)
	return nil
}

func edgeDNSCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery system edge dns install|status|restart|uninstall [--domain <domain>] [-o json]")
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
		return usageErrorf("unknown edge dns command %q", cmd)
	}
}
