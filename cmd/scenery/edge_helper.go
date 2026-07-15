package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
)

// The privileged helper is a security boundary shared across scenery
// versions: the root LaunchDaemon keeps running while unprivileged scenery is
// upgraded and rewrites the target metadata file. Everything the helper reads
// from that file goes through localagent.LoadEdgeHelperTarget (the frozen,
// tolerant handoff contract) so metadata written by newer scenery never makes
// an installed helper drop connections. See
// localagent.EdgeHelperContractRevision before changing helper validation.

type edgeHelperOptions struct {
	OwnerUID          int
	OwnerGID          int
	OwnerHome         string
	HelperTargetState string
	RouterAddr        string
	Public            bool
	HelperVersion     string
	HelperContract    string
}

type edgeHelperListenSpec struct {
	Addr       string
	HTTPPort80 bool
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
	flags := newCLIFlagSet("system edge privileged-helper")
	flags.IntVar(&opts.OwnerUID, "owner-uid", 0, "")
	flags.IntVar(&opts.OwnerGID, "owner-gid", 0, "")
	flags.StringVar(&opts.OwnerHome, "owner-home", "", "")
	flags.StringVar(&opts.HelperTargetState, "helper-target-state", "", "")
	flags.StringVar(&opts.RouterAddr, "router-addr", "", "")
	flags.BoolVar(&opts.Public, "public", false, "")
	flags.StringVar(&opts.HelperVersion, "helper-version", "", "")
	flags.StringVar(&opts.HelperContract, "helper-contract", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return edgeHelperOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return edgeHelperOptions{}, err
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
	// The plist and the copied binary always change together, so the stamped
	// contract revision is always this binary's revision regardless of what
	// the caller passed.
	opts.HelperContract = localagent.EdgeHelperContractRevision
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
	if err := retryEdgeHelperLaunchctl(edgeHelperLaunchctlRetryWindow, time.Sleep, runEdgeHelperLaunchctl, "bootstrap", "system", edgeHelperPlistPath); err != nil {
		return err
	}
	if err := retryEdgeHelperLaunchctl(edgeHelperLaunchctlRetryWindow, time.Sleep, runEdgeHelperLaunchctl, "kickstart", "-k", "system/"+edgeHelperLabel); err != nil {
		return err
	}
	return nil
}

// edgeHelperLaunchctlRetryWindow bounds retries of launchctl bootstrap and
// kickstart during install. `launchctl bootout` returns before launchd has
// finished tearing the old service down, so an immediate bootstrap of the
// replacement can fail transiently with exit status 5 (EIO); one re-run of
// `scenery deploy setup` used to be the workaround.
const edgeHelperLaunchctlRetryWindow = 10 * time.Second

func runEdgeHelperLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

func retryEdgeHelperLaunchctl(window time.Duration, sleep func(time.Duration), run func(...string) ([]byte, error), args ...string) error {
	deadline := time.Now().Add(window)
	for {
		out, err := run(args...)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("launchctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		sleep(250 * time.Millisecond)
	}
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
	specs := edgeHelperListenSpecs(opts)
	listeners := make([]net.Listener, 0, len(specs))
	for _, spec := range specs {
		ln, err := listenEdgeHelperSpec(spec)
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			return fmt.Errorf("listen %s: %w", spec.Addr, err)
		}
		listeners = append(listeners, ln)
	}
	errCh := make(chan error, len(listeners))
	for i, ln := range listeners {
		go acceptEdgeHelperLoop(ln, opts, specs[i], errCh)
	}
	return <-errCh
}

func listenEdgeHelperSpec(spec edgeHelperListenSpec) (net.Listener, error) {
	listener := net.ListenConfig{}
	network := "tcp"
	if strings.HasPrefix(spec.Addr, "[") && !strings.HasPrefix(spec.Addr, "[::]:") {
		network = "tcp6"
		listener.Control = func(network, address string, conn syscall.RawConn) error {
			var sockErr error
			if err := conn.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 1)
			}); err != nil {
				return err
			}
			return sockErr
		}
	}
	return listener.Listen(context.Background(), network, spec.Addr)
}

func edgeHelperListenSpecs(opts edgeHelperOptions) []edgeHelperListenSpec {
	if opts.Public {
		return []edgeHelperListenSpec{
			{Addr: "[::]:443"},
			{Addr: "[::]:80", HTTPPort80: true},
		}
	}
	return []edgeHelperListenSpec{
		{Addr: "127.0.0.1:443"},
		{Addr: "[::1]:443"},
	}
}

func acceptEdgeHelperLoop(ln net.Listener, opts edgeHelperOptions, spec edgeHelperListenSpec, errCh chan<- error) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		go handleEdgeHelperConn(conn, opts, spec)
	}
}

// edgeHelperDropLog rate-limits helper-side connection-drop logging so a
// broken target file under public traffic explains itself in the launchd log
// without flooding it. launchd redirects helper stderr to edgeHelperLogPath.
var (
	edgeHelperDropLog             = newEdgeHelperFailureLog(30*time.Second, time.Now)
	edgeHelperLogWriter io.Writer = os.Stderr
)

type edgeHelperFailureLog struct {
	mu        sync.Mutex
	component string
	interval  time.Duration
	now       func() time.Time
	last      map[string]time.Time
	dropped   map[string]int
}

func newEdgeHelperFailureLog(interval time.Duration, now func() time.Time) *edgeHelperFailureLog {
	return newComponentFailureLog("edge-helper", interval, now)
}

func newComponentFailureLog(component string, interval time.Duration, now func() time.Time) *edgeHelperFailureLog {
	return &edgeHelperFailureLog{
		component: component,
		interval:  interval,
		now:       now,
		last:      map[string]time.Time{},
		dropped:   map[string]int{},
	}
}

func (l *edgeHelperFailureLog) report(w io.Writer, listenAddr, reason string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.last) > 128 {
		l.last = map[string]time.Time{}
		l.dropped = map[string]int{}
	}
	key := listenAddr + "|" + reason + "|" + err.Error()
	now := l.now()
	if last, ok := l.last[key]; ok && now.Sub(last) < l.interval {
		l.dropped[key]++
		return
	}
	suppressed := l.dropped[key]
	delete(l.dropped, key)
	l.last[key] = now
	if suppressed > 0 {
		fmt.Fprintf(w, "%s %s %s on %s: %v (%d similar suppressed in the last %s)\n", now.UTC().Format(time.RFC3339), l.component, reason, listenAddr, err, suppressed, l.interval)
		return
	}
	fmt.Fprintf(w, "%s %s %s on %s: %v\n", now.UTC().Format(time.RFC3339), l.component, reason, listenAddr, err)
}

func handleEdgeHelperConn(client net.Conn, opts edgeHelperOptions, spec edgeHelperListenSpec) {
	defer client.Close()
	targetAddr, err := validateEdgeTargetForPort(opts.HelperTargetState, opts.OwnerUID, opts.OwnerGID, spec.HTTPPort80)
	if err != nil {
		edgeHelperDropLog.report(edgeHelperLogWriter, spec.Addr, "refusing connection (target metadata validation failed)", err)
		return
	}
	upstream, err := net.DialTimeout("tcp", targetAddr, 2*time.Second)
	if err != nil {
		edgeHelperDropLog.report(edgeHelperLogWriter, spec.Addr, "dropping connection (edge target unreachable)", err)
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
	return validateEdgeTargetForPort(path, ownerUID, ownerGID, false)
}

func validateEdgeTargetForPort(path string, ownerUID, ownerGID int, httpPort80 bool) (string, error) {
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
	state, err := localagent.LoadEdgeHelperTarget(path)
	if err != nil {
		return "", err
	}
	if state.Kind != localagent.EdgeKindCaddy {
		return "", fmt.Errorf("edge target metadata has unexpected kind")
	}
	targetAddr, err := edgeTargetAddrForPort(state, httpPort80)
	if err != nil {
		return "", err
	}
	host, portRaw, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return "", fmt.Errorf("edge target address %q is invalid: %w", targetAddr, err)
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
	return targetAddr, nil
}

func edgeTargetAddrForPort(state localagent.EdgeHelperTarget, httpPort80 bool) (string, error) {
	if httpPort80 {
		if strings.TrimSpace(state.HTTPTargetAddr) == "" {
			return "", fmt.Errorf("edge target metadata has no HTTP target")
		}
		return strings.TrimSpace(state.HTTPTargetAddr), nil
	}
	return strings.TrimSpace(state.TargetAddr), nil
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
	args := []string{
		edgeHelperBinaryPath,
		"system",
		"edge",
		"privileged-helper",
		"run",
	}
	if opts.Public {
		args = append(args, "--public")
	}
	if strings.TrimSpace(opts.HelperVersion) != "" {
		args = append(args, "--helper-version", strings.TrimSpace(opts.HelperVersion))
	}
	if strings.TrimSpace(opts.HelperContract) != "" {
		args = append(args, "--helper-contract", strings.TrimSpace(opts.HelperContract))
	}
	args = append(args,
		"--owner-uid", strconv.Itoa(opts.OwnerUID),
		"--owner-gid", strconv.Itoa(opts.OwnerGID),
		"--owner-home", opts.OwnerHome,
		"--helper-target-state", opts.HelperTargetState,
		"--router-addr", firstNonEmpty(opts.RouterAddr, localagent.RouterAddrFromEnv()),
	)
	var argLines strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&argLines, "\t\t<string>%s</string>\n", edgelifecycle.EscapePlistString(arg))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s
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
`, edgelifecycle.EscapePlistString(edgeHelperLabel), strings.TrimRight(argLines.String(), "\n"), edgelifecycle.EscapePlistString(edgeHelperLogPath), edgelifecycle.EscapePlistString(edgeHelperLogPath))
}
