package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
)

func edgePrivilegedCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery system edge privileged install|status|uninstall [-o json]")
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
		return usageErrorf("unknown edge privileged command %q", cmd)
	}
}

func edgePrivilegedInstall() error {
	if runtime.GOOS != "darwin" {
		return unavailableErrorf("scenery system edge privileged install is currently supported on macOS")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery system edge privileged install`; run it as your normal user so Scenery can record the expected owner")
	}
	paths, err := commandAgentPaths()
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
	_, _ = fmt.Fprintln(os.Stdout, "installed scenery privileged edge listener for 127.0.0.1:443")
	return nil
}

func edgePrivilegedStatus(opts edgeOptions) error {
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	status := privilegedListenerStatus(paths)
	if opts.JSON {
		return writeCLIJSON(os.Stdout, status)
	}
	_, _ = fmt.Fprintf(os.Stdout, "scenery system edge privileged listener %s", status.State)
	if status.Target != "" {
		_, _ = fmt.Fprintf(os.Stdout, " -> %s", status.Target)
	}
	if !status.Installed {
		_, _ = fmt.Fprintf(os.Stdout, " (run `scenery system edge privileged install`)")
	}
	_, _ = fmt.Fprintln(os.Stdout)
	return nil
}

func edgePrivilegedUninstall() error {
	if runtime.GOOS != "darwin" {
		return unavailableErrorf("scenery system edge privileged uninstall is currently supported on macOS")
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
	target, err := localagent.LoadEdgeHelperTarget(targetPath)
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
