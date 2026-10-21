package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/deploydiag"
)

func runDeployResume(stdout io.Writer, opts deployOptions) error {
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	if err := deployEnsureAgentFunc(); err != nil {
		return err
	}
	edgeRestarted, err := deployEnsurePublicEdgeFunc(paths)
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	sessions := deploySessionsByAppRoot(paths)
	resp := deployResumeResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.resume"),
		RegistryPath:       paths.DeployPath,
		LogPath:            paths.DeployResumeLogPath,
		AgentReady:         true,
		EdgeRestarted:      edgeRestarted,
	}
	// Resume runs unattended at login, so it cannot sudo; it must still
	// refuse to report a quietly broken helper after an upgrade.
	if drift := deployHelperDriftStatusFunc(paths); drift.ActionRequired {
		resp.HelperDrift = &drift
	}
	for _, target := range registry.Targets {
		if !target.Enabled {
			continue
		}
		item := deployResumeTarget{Environment: firstNonEmpty(target.Environment, "unknown"), Domain: target.Domain, AppRoot: target.AppRoot}
		if _, err := os.Stat(target.AppRoot); err != nil {
			item.Action = "missing"
			item.Error = err.Error()
			resp.Targets = append(resp.Targets, item)
			continue
		}
		if session := sessions[filepath.Clean(target.AppRoot)]; session.SessionID != "" {
			item.Action = "already_running"
			item.SessionID = session.SessionID
			resp.Targets = append(resp.Targets, item)
			continue
		}
		if err := deployRunUpDetachFunc(target.AppRoot, target.Environment); err != nil {
			item.Action = "failed"
			item.Error = err.Error()
			resp.Targets = append(resp.Targets, item)
			continue
		}
		item.Action = "started"
		resp.Targets = append(resp.Targets, item)
	}
	_ = appendDeployResumeLog(paths, resp)
	if opts.JSON {
		return writeCLIJSON(stdout, resp)
	}
	if resp.HelperDrift != nil {
		_, _ = fmt.Fprintf(stdout, "helper drift: %s\n", resp.HelperDrift.Message)
		if resp.HelperDrift.SuggestedAction != "" {
			_, _ = fmt.Fprintf(stdout, "helper drift: %s\n", resp.HelperDrift.SuggestedAction)
		}
	}
	for _, target := range resp.Targets {
		if target.Error != "" {
			_, _ = fmt.Fprintf(stdout, "%s %s: %s\n", target.Domain, target.Action, target.Error)
		} else {
			_, _ = fmt.Fprintf(stdout, "%s %s\n", target.Domain, target.Action)
		}
	}
	return nil
}

// ensurePublicDeployEdge makes login resume idempotent. A healthy public edge
// is already the desired state, so reloading a RunAtLoad job must not tear it
// down. After an actual reboot the recorded process is gone and the same path
// falls through to the bounded edge restart.
func ensurePublicDeployEdge(paths localagent.Paths) (bool, error) {
	const attempts = 20
	var lastStatus edgeStatusResult
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		status, err := deployPublicEdgeStatusFunc(paths)
		lastStatus, lastErr = status, err
		if err == nil && edgeRestartReady(status, true) {
			return false, nil
		}
		if attempt+1 < attempts {
			time.Sleep(deployPublicEdgeRetryDelay)
		}
	}
	if lastErr != nil {
		fmt.Fprintf(os.Stderr, "deploy resume: public edge state could not be reacquired: %v; restarting\n", lastErr)
	} else {
		fmt.Fprintf(os.Stderr, "deploy resume: public edge unavailable after bounded reacquisition (edge=%s pid=%d helper=%s target_pid=%d agent=%s upstream=%s); restarting\n",
			lastStatus.Edge.State,
			lastStatus.Edge.PID,
			lastStatus.PrivilegedListener.State,
			lastStatus.PrivilegedListener.TargetPID,
			lastStatus.Edge.AgentRouter,
			lastStatus.Edge.Upstream,
		)
	}
	return true, deployEdgeRestartFunc()
}

func publicDeployEdgeStatus(paths localagent.Paths) (edgeStatusResult, error) {
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return edgeStatusResult{}, err
	}
	return edgeStatusForStateDomain(paths, state, ""), nil
}

func runDeployTeardown(stdout io.Writer, opts deployOptions) error {
	if runtime.GOOS == "linux" {
		return runDeployTeardownLinux(stdout, opts)
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery deploy teardown is supported on macOS (launchd) and Linux (systemd)")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery deploy teardown`; run it as your normal user so Scenery can record the expected owner")
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	helperVersion := buildVersionResponse().Version
	if err := deployLoopbackHelperInstallFunc(paths, helperVersion); err != nil {
		return err
	}
	removed, err := deployRemoveResumeLaunchAgentFunc()
	if err != nil {
		return err
	}
	supervisorRemoved, err := deployRemoveAgentSupervisorFunc()
	if err != nil {
		return err
	}
	if err := deployEdgeRestartFunc(); err != nil {
		return err
	}
	resp := deployTeardownResponse{
		cliPayloadIdentity:     newCLIPayloadIdentity("scenery.deploy.teardown"),
		RegistryPath:           paths.DeployPath,
		ServiceManager:         "launchd",
		HelperVersion:          helperVersion,
		HelperPublic:           false,
		AgentSupervisorRemoved: supervisorRemoved,
		LaunchAgentRemoved:     removed,
		EdgeRestarted:          true,
	}
	if opts.JSON {
		return writeCLIJSON(stdout, resp)
	}
	_, _ = fmt.Fprintln(stdout, "disabled public deploy edge; local HTTPS remains available")
	return nil
}

func writeDeployMutation(stdout io.Writer, jsonMode bool, resp deployMutationResponse) error {
	if jsonMode {
		return writeCLIJSON(stdout, resp)
	}
	for _, target := range resp.Targets {
		_, _ = fmt.Fprintf(stdout, "%s %s for %s\n", resp.Action, target.Domain, target.AppRoot)
	}
	return nil
}

func upsertDeployTarget(registry *localagent.DeployRegistry, next localagent.DeployTarget) error {
	next.Domain = strings.ToLower(strings.TrimSpace(next.Domain))
	next.AppRoot = filepath.Clean(strings.TrimSpace(next.AppRoot))
	next.RootService = strings.TrimSpace(next.RootService)
	now := time.Now().UTC()
	next.UpdatedAt = now
	for i, existing := range registry.Targets {
		if existing.Domain == next.Domain && existing.Enabled && filepath.Clean(existing.AppRoot) != next.AppRoot {
			return fmt.Errorf("deploy domain %s is already enabled for %s; run `scenery deploy disable --app-root %s` first", next.Domain, existing.AppRoot, existing.AppRoot)
		}
		if existing.Domain == next.Domain {
			if !existing.CreatedAt.IsZero() {
				next.CreatedAt = existing.CreatedAt
			}
			if next.CreatedAt.IsZero() {
				next.CreatedAt = now
			}
			registry.Targets[i] = next
			return nil
		}
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = now
	}
	registry.Targets = append(registry.Targets, next)
	return nil
}

func deployRootService(cfg appcfg.Config) string {
	return cfg.RootFrontend()
}

func resolveDeployEnv(cfg appcfg.Config, name string) (appcfg.ResolvedEnv, error) {
	if strings.TrimSpace(name) != "" {
		env, err := cfg.ResolveEnv(name)
		if err != nil {
			return appcfg.ResolvedEnv{}, err
		}
		if !env.Deployable() {
			return appcfg.ResolvedEnv{}, fmt.Errorf("environment %q is not deployable", env.Name)
		}
		return env, nil
	}
	var found appcfg.ResolvedEnv
	for envName, raw := range cfg.Envs {
		if raw.Deploy == nil {
			continue
		}
		if found.Name != "" {
			return appcfg.ResolvedEnv{}, fmt.Errorf("multiple deployable environments; pass --env <name>")
		}
		found, _ = cfg.ResolveEnv(envName)
	}
	if found.Name == "" {
		return appcfg.ResolvedEnv{}, fmt.Errorf("no deployable environment is configured")
	}
	return found, nil
}

func absoluteDeployAppRoot(appRootOpt string) (string, error) {
	start, err := resolveAppRoot(appRootOpt)
	if err != nil {
		return "", err
	}
	return canonicalDeployAppRoot(start)
}

// canonicalDeployAppRoot names a registered root as enable records it, with
// symbolic links resolved; a root that no longer exists keeps its clean
// absolute spelling so its target can still be disabled.
func canonicalDeployAppRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(abs), nil
	}
	return canonical, err
}

func deployPreflightPublicPorts(paths localagent.Paths) error {
	_ = paths
	for _, port := range []int{80, 443} {
		listener, ok, err := deployPortListenerFunc(port)
		if err != nil {
			return err
		}
		if !ok || listener.IsSceneryHelper() {
			continue
		}
		return fmt.Errorf("port %d is already in use by %s; stop it before running scenery deploy setup", port, listener.String())
	}
	return nil
}

func deployRefreshEdgeAfterMutation(paths localagent.Paths) error {
	if edgeSystemdManaged() {
		state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
		if err != nil || !localagent.EdgeStateRunning(state) {
			return edgeRestartSystemd(paths)
		}
		return edgeReloadFromRegistry(paths, state)
	}
	helper := privilegedListenerStatus(paths)
	if !helper.Installed || !deploydiag.HelperHasPublicBinding(helper.Listen) {
		return nil
	}
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return err
	}
	if !localagent.EdgeStateRunning(state) {
		return nil
	}
	return edgeReloadFromRegistry(paths, state)
}

func deployActivateRegistryCandidate(paths localagent.Paths, registry localagent.DeployRegistry) error {
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return err
	}
	if !localagent.EdgeStateRunning(state) {
		return fmt.Errorf("managed edge is not running")
	}
	return edgeReloadForDeployRegistry(paths, state, registry, false)
}

func deployPrivilegedHelperInstall(paths localagent.Paths, helperVersion string) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	args := deployPrivilegedHelperInstallArgs(exe, paths, helperVersion)
	return runDeploySudo(args)
}

func deployLoopbackHelperInstall(paths localagent.Paths, helperVersion string) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	args := deployLoopbackHelperInstallArgs(exe, paths, helperVersion)
	return runDeploySudo(args)
}

func deployRunUpDetach(appRoot, envName string) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	args := []string{"up", "--detach", "--app-root", appRoot}
	if strings.TrimSpace(envName) != "" {
		args = append(args, "--env", envName)
	}
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("scenery up --detach --app-root %s: %w: %s", appRoot, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func appendDeployResumeLog(paths localagent.Paths, resp deployResumeResponse) error {
	if err := os.MkdirAll(filepath.Dir(paths.DeployResumeLogPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(paths.DeployResumeLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return json.NewEncoder(file).Encode(resp)
}

func runDeploySudo(args []string) error {
	run := exec.Command("sudo", args...)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Stdin = os.Stdin
	return run.Run()
}

func deployPrivilegedHelperInstallArgs(exe string, paths localagent.Paths, helperVersion string) []string {
	return deployHelperInstallArgs(exe, paths, helperVersion, true)
}

func deployLoopbackHelperInstallArgs(exe string, paths localagent.Paths, helperVersion string) []string {
	return deployHelperInstallArgs(exe, paths, helperVersion, false)
}

func deployHelperInstallArgs(exe string, paths localagent.Paths, helperVersion string, public bool) []string {
	args := []string{
		exe, "system", "edge", "privileged-helper", "install",
	}
	if public {
		args = append(args, "--public")
	}
	if strings.TrimSpace(helperVersion) != "" {
		args = append(args, "--helper-version", strings.TrimSpace(helperVersion))
	}
	args = append(args,
		"--owner-uid", strconv.Itoa(os.Getuid()),
		"--owner-gid", strconv.Itoa(os.Getgid()),
		"--owner-home", paths.Home,
		"--helper-target-state", paths.EdgeTargetPath,
		"--router-addr", localagent.RouterAddrFromEnv(),
	)
	return args
}

func readyWord(ok bool) string {
	if ok {
		return "ready"
	}
	return "not ready"
}
