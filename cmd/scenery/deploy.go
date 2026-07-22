package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/deploydiag"
)

type deployOptions struct {
	AppRoot   string
	Env       string
	JSON      bool
	ACMEEmail string
	ACMECA    string
}

type deployMutationResponse struct {
	cliPayloadIdentity
	Action       string                    `json:"action"`
	RegistryPath string                    `json:"registry_path"`
	Targets      []localagent.DeployTarget `json:"targets"`
}

type deployStatusResponse struct {
	cliPayloadIdentity
	Ready              bool                         `json:"ready"`
	ServiceManager     string                       `json:"service_manager,omitempty"`
	RegistryPath       string                       `json:"registry_path"`
	PrivilegedListener edgeStatusPrivilegedListener `json:"privileged_listener"`
	HelperPublic       bool                         `json:"helper_public"`
	Edge               edgeStatusCaddy              `json:"edge"`
	Agent              deployAgentStatus            `json:"agent"`
	AgentSupervisor    deployAgentSupervisorStatus  `json:"agent_supervisor"`
	LaunchAgent        deployLaunchAgentStatus      `json:"launch_agent"`
	ACME               deployACMEStatus             `json:"acme"`
	Targets            []deployTargetStatus         `json:"targets"`
	Diagnostics        []string                     `json:"diagnostics,omitempty"`
	DiagnosticsDetail  *deploydiag.Report           `json:"diagnostics_detail,omitempty"`
}

type deploySetupResponse struct {
	cliPayloadIdentity
	RegistryPath             string           `json:"registry_path"`
	ServiceManager           string           `json:"service_manager,omitempty"`
	ACME                     deployACMEStatus `json:"acme"`
	HelperVersion            string           `json:"helper_version"`
	HelperPublic             bool             `json:"helper_public"`
	HelperReinstalled        bool             `json:"helper_reinstalled"`
	AgentSupervisorInstalled bool             `json:"agent_supervisor_installed"`
	LaunchAgentInstalled     bool             `json:"launch_agent_installed"`
	EdgeRestarted            bool             `json:"edge_restarted"`
}

type deployTeardownResponse struct {
	cliPayloadIdentity
	RegistryPath           string `json:"registry_path"`
	ServiceManager         string `json:"service_manager,omitempty"`
	HelperVersion          string `json:"helper_version"`
	HelperPublic           bool   `json:"helper_public"`
	AgentSupervisorRemoved bool   `json:"agent_supervisor_removed"`
	LaunchAgentRemoved     bool   `json:"launch_agent_removed"`
	EdgeRestarted          bool   `json:"edge_restarted"`
}

type deployResumeResponse struct {
	cliPayloadIdentity
	RegistryPath  string                  `json:"registry_path"`
	LogPath       string                  `json:"log_path"`
	AgentReady    bool                    `json:"agent_ready"`
	EdgeRestarted bool                    `json:"edge_restarted"`
	HelperDrift   *deploydiag.HelperDrift `json:"helper_drift,omitempty"`
	Targets       []deployResumeTarget    `json:"targets"`
}

type deployResumeTarget struct {
	Environment string `json:"environment,omitempty"`
	Domain      string `json:"domain"`
	AppRoot     string `json:"app_root"`
	Action      string `json:"action"`
	SessionID   string `json:"session_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

type deployAgentStatus struct {
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
	StatePath  string `json:"state_path"`
	SocketPath string `json:"socket_path"`
	RouterAddr string `json:"router_addr,omitempty"`
	Message    string `json:"message,omitempty"`
}

// deployLaunchAgentStatus distinguishes plist presence (Installed) from a job
// launchd actually loaded (Loaded); only the latter can recover anything.
type deployLaunchAgentStatus struct {
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	State        string `json:"state,omitempty"`
	LastExitCode *int   `json:"last_exit_code,omitempty"`
	Path         string `json:"path"`
}

func (status deployLaunchAgentStatus) failed() bool {
	return status.Loaded && status.State != "running" && status.LastExitCode != nil && *status.LastExitCode != 0
}

// deployAgentSupervisorStatus reports the launchd job that continuously owns
// the scenery agent. Deploy readiness requires it: without supervision the
// public edge upstream has no recovery owner.
type deployAgentSupervisorStatus struct {
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Label     string `json:"label"`
	Path      string `json:"path"`
}

type deployACMEStatus struct {
	Email string `json:"email,omitempty"`
	CA    string `json:"ca"`
}

type deployTargetStatus struct {
	Environment  string                       `json:"environment,omitempty"`
	Domain       string                       `json:"domain"`
	AppRoot      string                       `json:"app_root"`
	RootService  string                       `json:"root_service,omitempty"`
	Enabled      bool                         `json:"enabled"`
	LiveSession  bool                         `json:"live_session"`
	SessionID    string                       `json:"session_id,omitempty"`
	CertPresent  bool                         `json:"cert_present"`
	CertNotAfter string                       `json:"cert_not_after,omitempty"`
	Frontends    []deployTargetFrontendStatus `json:"frontends,omitempty"`
	Diagnostics  []string                     `json:"diagnostics,omitempty"`
}

// deployTargetFrontendStatus reports how one production frontend is served
// publicly: "caddy_static" when its published artifact currently resolves,
// otherwise "agent_proxy".
type deployTargetFrontendStatus struct {
	Environment   string `json:"environment,omitempty"`
	Name          string `json:"name"`
	Route         string `json:"route"`
	Mode          string `json:"mode"`
	ArtifactPath  string `json:"artifact_path,omitempty"`
	ReleaseID     string `json:"release_id,omitempty"`
	EntryDocument bool   `json:"entry_document"`
}

type deployCertStatus struct {
	Present  bool
	Path     string
	NotAfter time.Time
	Error    string
}

var (
	deploySetupPreflightFunc             = deployPreflightPublicPorts
	deployPrivilegedHelperInstallFunc    = deployPrivilegedHelperInstall
	deployLoopbackHelperInstallFunc      = deployLoopbackHelperInstall
	deployInstallResumeLaunchAgentFunc   = installDeployResumeLaunchAgent
	deployRemoveResumeLaunchAgentFunc    = removeDeployResumeLaunchAgent
	deployInstallAgentSupervisorFunc     = installDeployAgentSupervisor
	deployRemoveAgentSupervisorFunc      = localagent.RemoveAgentLaunchd
	deployResumeLaunchAgentStatusFunc    = deployResumeLaunchAgentStatus
	deployHelperReinstallNeededFunc      = deploySetupNeedsHelperReinstall
	deployLaunchctlFunc                  = runDeployLaunchctl
	deployEnsureAgentFunc                = func() error { return ensureEdgeAgent(localagent.RouterAddrFromEnv(), false) }
	deployEdgeRestartFunc                = func() error { return edgeRestart(edgeOptions{Deploy: true, Quiet: true}) }
	deployPublicEdgeStatusFunc           = publicDeployEdgeStatus
	deployEnsurePublicEdgeFunc           = ensurePublicDeployEdge
	deployPublicEdgeRetryDelay           = 100 * time.Millisecond
	deployRefreshEdgeAfterMutationFunc   = deployRefreshEdgeAfterMutation
	deployRunUpDetachFunc                = deployRunUpDetach
	deployPortListenerFunc               = deploydiag.DefaultPortListener
	deployPrivilegedHelperExecutableFunc = os.Executable
	deployHelperDriftStatusFunc          = func(paths localagent.Paths) deploydiag.HelperDrift {
		return deploydiag.HelperDriftFor(deployDiagHelperStatus(privilegedListenerStatus(paths)), buildVersionResponse().Version)
	}
	deployLANIPFunc          = deploydiag.DefaultLANIP
	deployHTTPProbeFunc      = deploydiag.DefaultHTTPProbe
	deployPublicIPFunc       = deploydiag.DefaultPublicIP
	deployDNSLookupFunc      = net.LookupIP
	deployPowerStatusFunc    = deploydiag.DefaultPowerStatus
	deployFirewallStatusFunc = deploydiag.DefaultFirewallStatus
)

func deployCommand(args []string) error {
	return runDeployCommand(os.Stdout, args)
}

func runDeployCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery deploy <ssh-target> [--app-root <path>] | setup|status|enable|disable|publish|resume|teardown [-o json]")
	}
	subcommand := args[0]
	if strings.HasPrefix(subcommand, "-") {
		return runDeploySSH(stdout, "", args)
	}
	if subcommand == "plan" || subcommand == "apply" {
		return runDeployment(stdout, args)
	}
	if !isDeploySubcommand(subcommand) {
		return runDeploySSH(stdout, subcommand, args[1:])
	}
	opts, err := parseDeployOptions(subcommand, args[1:])
	if err != nil {
		return err
	}
	switch subcommand {
	case "enable":
		return runDeployEnable(stdout, opts)
	case "disable":
		return runDeployDisable(stdout, opts)
	case "publish":
		return runDeployPublish(stdout, opts)
	case "status":
		return runDeployStatus(stdout, opts)
	case "setup":
		return runDeploySetup(stdout, opts)
	case "resume":
		return runDeployResume(stdout, opts)
	case "teardown":
		return runDeployTeardown(stdout, opts)
	default:
		return fmt.Errorf("unknown scenery deploy subcommand %q", subcommand)
	}
}

func isDeploySubcommand(value string) bool {
	switch value {
	case "setup", "status", "enable", "disable", "publish", "resume", "teardown":
		return true
	default:
		return false
	}
}

func parseDeployOptions(subcommand string, args []string) (deployOptions, error) {
	var opts deployOptions
	flags := newCLIFlagSet("deploy " + subcommand)
	registerJSONOutput(flags, &opts.JSON)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Env, "env", "", "")
	flags.StringVar(&opts.ACMEEmail, "acme-email", "", "")
	flags.StringVar(&opts.ACMECA, "acme-ca", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return deployOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return deployOptions{}, err
	}
	if opts.ACMECA != "" && opts.ACMECA != "production" && opts.ACMECA != "staging" {
		return deployOptions{}, fmt.Errorf("--acme-ca must be production or staging")
	}
	opts.Env = strings.TrimSpace(opts.Env)
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return deployOptions{}, fmt.Errorf("--env must not be empty")
	}
	if subcommand != "setup" && (opts.ACMEEmail != "" || opts.ACMECA != "") {
		return deployOptions{}, fmt.Errorf("--acme-email and --acme-ca are only supported by scenery deploy setup")
	}
	return opts, nil
}

func runDeployEnable(stdout io.Writer, opts deployOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	env, err := resolveDeployEnv(cfg, opts.Env)
	if err != nil {
		return err
	}
	domain := strings.TrimSpace(env.Domain)
	if domain == "" {
		return fmt.Errorf("envs.%s has no domain; add one before running scenery deploy enable", env.Name)
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	rootService := deployRootService(cfg, env)
	if err := upsertDeployTarget(&registry, localagent.DeployTarget{
		Environment: env.Name,
		Domain:      domain,
		AppRoot:     filepath.Clean(appRoot),
		RootService: rootService,
		Enabled:     true,
	}); err != nil {
		return err
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		return err
	}
	if err := deployRefreshEdgeAfterMutationFunc(paths); err != nil {
		return err
	}
	return writeDeployMutation(stdout, opts.JSON, deployMutationResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.target"),
		Action:             "enable",
		RegistryPath:       paths.DeployPath,
		Targets:            registry.Targets,
	})
}

func runDeployDisable(stdout io.Writer, opts deployOptions) error {
	appRoot, err := absoluteDeployAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	found := false
	now := time.Now().UTC()
	for i := range registry.Targets {
		if filepath.Clean(registry.Targets[i].AppRoot) != appRoot {
			continue
		}
		found = true
		registry.Targets[i].Enabled = false
		registry.Targets[i].UpdatedAt = now
	}
	if !found {
		return fmt.Errorf("no deploy target is registered for app root %s", appRoot)
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		return err
	}
	if err := deployRefreshEdgeAfterMutationFunc(paths); err != nil {
		return err
	}
	return writeDeployMutation(stdout, opts.JSON, deployMutationResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.target"),
		Action:             "disable",
		RegistryPath:       paths.DeployPath,
		Targets:            registry.Targets,
	})
}

func runDeployStatus(stdout io.Writer, opts deployOptions) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	status := buildDeployStatus(paths, registry)
	if opts.JSON {
		return writeCLIJSON(stdout, status)
	}
	fmt.Fprintf(stdout, "Deploy: %s\n", readyWord(status.Ready))
	for _, diag := range status.Diagnostics {
		fmt.Fprintf(stdout, "- %s\n", diag)
	}
	for _, target := range status.Targets {
		state := "disabled"
		if target.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(stdout, "%s  %s  %s\n", target.Domain, state, target.AppRoot)
	}
	return nil
}

func runDeploySetup(stdout io.Writer, opts deployOptions) error {
	if runtime.GOOS == "linux" {
		return runDeploySetupLinux(stdout, opts)
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery deploy setup is supported on macOS (launchd) and Linux (systemd)")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("do not run `sudo scenery deploy setup`; run it as your normal user so Scenery can record the expected owner")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	if opts.ACMEEmail != "" {
		registry.ACMEEmail = strings.TrimSpace(opts.ACMEEmail)
	}
	if opts.ACMECA != "" {
		registry.ACMECA = opts.ACMECA
	}
	if err := deploySetupPreflightFunc(paths); err != nil {
		return err
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		return err
	}
	helperVersion := buildVersionResponse().Version
	// Setup must stay re-runnable without sudo so launchd supervision can be
	// repaired unattended: reinstall the privileged helper only when the
	// installed one is missing, drifted, or not publicly bound.
	helperReinstall := deployHelperReinstallNeededFunc(paths, helperVersion)
	if helperReinstall {
		if err := deployPrivilegedHelperInstallFunc(paths, helperVersion); err != nil {
			return err
		}
	}
	if err := deployInstallAgentSupervisorFunc(paths); err != nil {
		return err
	}
	if err := deployEdgeRestartFunc(); err != nil {
		return err
	}
	if err := deployInstallResumeLaunchAgentFunc(paths); err != nil {
		return err
	}
	resp := deploySetupResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.setup"),
		RegistryPath:       paths.DeployPath,
		ServiceManager:     "launchd",
		ACME: deployACMEStatus{
			Email: registry.ACMEEmail,
			CA:    firstNonEmpty(registry.ACMECA, "production"),
		},
		HelperVersion:            helperVersion,
		HelperPublic:             true,
		HelperReinstalled:        helperReinstall,
		AgentSupervisorInstalled: true,
		LaunchAgentInstalled:     true,
		EdgeRestarted:            true,
	}
	if opts.JSON {
		return writeCLIJSON(stdout, resp)
	}
	fmt.Fprintf(stdout, "configured public deploy edge (%s CA)\n", resp.ACME.CA)
	return nil
}

// deploySetupNeedsHelperReinstall keeps the sudo escalation out of setup
// re-runs whose installed helper already matches the current handoff
// contract: the helper is designed to survive scenery upgrades, so only a
// missing, non-public, stopped, or contract-drifted helper forces sudo.
func deploySetupNeedsHelperReinstall(paths localagent.Paths, currentVersion string) bool {
	helper := privilegedListenerStatus(paths)
	if !helper.Installed || helper.State != "running" || !deploydiag.HelperHasPublicBinding(helper.Listen) {
		return true
	}
	return deploydiag.HelperDriftFor(deployDiagHelperStatus(helper), currentVersion).ActionRequired
}

func runDeployResume(stdout io.Writer, opts deployOptions) error {
	paths, err := localagent.DefaultPaths()
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
		fmt.Fprintf(stdout, "helper drift: %s\n", resp.HelperDrift.Message)
		if resp.HelperDrift.SuggestedAction != "" {
			fmt.Fprintf(stdout, "helper drift: %s\n", resp.HelperDrift.SuggestedAction)
		}
	}
	for _, target := range resp.Targets {
		if target.Error != "" {
			fmt.Fprintf(stdout, "%s %s: %s\n", target.Domain, target.Action, target.Error)
		} else {
			fmt.Fprintf(stdout, "%s %s\n", target.Domain, target.Action)
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
	paths, err := localagent.DefaultPaths()
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
	fmt.Fprintln(stdout, "disabled public deploy edge; local HTTPS remains available")
	return nil
}

func writeDeployMutation(stdout io.Writer, jsonMode bool, resp deployMutationResponse) error {
	if jsonMode {
		return writeCLIJSON(stdout, resp)
	}
	for _, target := range resp.Targets {
		fmt.Fprintf(stdout, "%s %s for %s\n", resp.Action, target.Domain, target.AppRoot)
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

func deployRootService(cfg appcfg.Config, env appcfg.ResolvedEnv) string {
	if env.Deploy != nil {
		if root := strings.TrimSpace(env.Deploy.Root); root != "" {
			return root
		}
	}
	if len(cfg.Frontends) != 1 {
		return ""
	}
	names := make([]string, 0, len(cfg.Frontends))
	for name := range cfg.Frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
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
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func buildDeployStatus(paths localagent.Paths, registry localagent.DeployRegistry) deployStatusResponse {
	return buildDeployStatusWithContext(context.Background(), paths, registry)
}

func buildDeployStatusWithContext(ctx context.Context, paths localagent.Paths, registry localagent.DeployRegistry) deployStatusResponse {
	edgeState, _ := localagent.LoadEdgeState(paths.EdgeStatePath)
	edgeStatus := edgeStatusForStateDomain(paths, edgeState, "").Edge
	helper := privilegedListenerStatus(paths)
	agent := deployAgentStatusFor(paths)
	agentSupervisor := deployAgentSupervisorStatusFor(paths)
	launchAgent := deployLaunchAgentStatusFor()
	sessions := deploySessionsByAppRoot(paths)
	targets := make([]deployTargetStatus, 0, len(registry.Targets))
	for _, target := range registry.Targets {
		session := sessions[filepath.Clean(target.AppRoot)]
		cert := deployCertStatusFor(paths, target.Domain)
		item := deployTargetStatus{
			Environment: firstNonEmpty(target.Environment, "unknown"),
			Domain:      target.Domain,
			AppRoot:     target.AppRoot,
			RootService: target.RootService,
			Enabled:     target.Enabled,
			LiveSession: session.SessionID != "",
			SessionID:   session.SessionID,
			CertPresent: cert.Present,
			Frontends:   deployTargetFrontendStatuses(target),
		}
		if !cert.NotAfter.IsZero() {
			item.CertNotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
		}
		if target.Enabled && session.SessionID == "" {
			item.Diagnostics = append(item.Diagnostics, "enabled target has no running scenery up session")
		}
		if cert.Present && cert.Error != "" {
			item.Diagnostics = append(item.Diagnostics, "certificate expiry could not be parsed: "+cert.Error)
		}
		for _, frontend := range item.Frontends {
			if target.Enabled && frontend.Mode != "caddy_static" {
				item.Diagnostics = append(item.Diagnostics, fmt.Sprintf("published frontend %q has no complete current artifact; serving falls back to the agent proxy", frontend.Name))
			}
		}
		targets = append(targets, item)
	}
	manager := deployServiceManagerFunc()
	helperPublic := deploydiag.HelperHasPublicBinding(helper.Listen)
	status := deployStatusResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.status"),
		ServiceManager:     manager,
		RegistryPath:       paths.DeployPath,
		PrivilegedListener: helper,
		HelperPublic:       helperPublic,
		Edge:               edgeStatus,
		Agent:              agent,
		AgentSupervisor:    agentSupervisor,
		LaunchAgent:        launchAgent,
		ACME: deployACMEStatus{
			Email: registry.ACMEEmail,
			CA:    firstNonEmpty(registry.ACMECA, "production"),
		},
		Targets: targets,
	}
	if manager != "systemd" && (helper.State != "running" || !helperPublic) {
		status.Diagnostics = append(status.Diagnostics, "public privileged listener is not ready; run `scenery deploy setup`")
	}
	if edgeStatus.State != localagent.EdgeStatusRunning {
		status.Diagnostics = append(status.Diagnostics, "Caddy edge is not running")
	}
	if agent.State != "running" {
		status.Diagnostics = append(status.Diagnostics, "Scenery agent is not running")
	}
	// Supervision is part of deploy readiness: a unit or plist on disk the
	// service manager never loaded recovers nothing, so presence alone never
	// counts.
	supervisorWord := "LaunchAgent"
	if manager == "systemd" {
		supervisorWord = "systemd unit"
	}
	switch {
	case !agentSupervisor.Installed:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("scenery agent supervisor %s is not installed; run `scenery deploy setup`", supervisorWord))
	case !agentSupervisor.Loaded:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("scenery agent supervisor %s is installed but not loaded; run `scenery deploy setup`", supervisorWord))
	case !agentSupervisor.Running:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("scenery agent supervisor %s is loaded but its agent process is not running; run `scenery system agent restart`", supervisorWord))
	}
	switch {
	case !launchAgent.Installed:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("deploy resume %s is not installed", supervisorWord))
	case !launchAgent.Loaded:
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("deploy resume %s exists but is not loaded; run `scenery deploy setup`", supervisorWord))
	case launchAgent.failed():
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("deploy resume %s completed with exit code %d; inspect its log and run `scenery deploy resume`", supervisorWord, *launchAgent.LastExitCode))
	}
	snapshot := deployDiagnosticsSnapshot(status)
	if manager == "systemd" {
		deploySystemdSnapshotOverlay(&snapshot)
	}
	diagnostics := deploydiag.BuildReport(ctx, snapshot, deployDiagnosticsDeps())
	status.DiagnosticsDetail = &diagnostics
	for _, check := range diagnostics.Checks {
		if check.Status == "warn" || check.Status == "error" {
			status.Diagnostics = append(status.Diagnostics, check.Message)
		}
	}
	status.Ready = len(status.Diagnostics) == 0
	return status
}

// deployDiagHelperStatus converts the CLI privileged-listener payload into
// the deploydiag helper snapshot.
func deployDiagHelperStatus(helper edgeStatusPrivilegedListener) deploydiag.HelperStatus {
	return deploydiag.HelperStatus{
		Installed:        helper.Installed,
		State:            helper.State,
		PID:              helper.PID,
		Listen:           helper.Listen,
		Target:           helper.Target,
		Version:          helper.Version,
		ContractRevision: helper.ContractRevision,
	}
}

// deployDiagnosticsSnapshot converts the CLI deploy status payload into the
// snapshot the diagnostics engine consumes. Status targets mirror the deploy
// registry targets one to one, so they carry both the registry enablement and
// the certificate observations.
func deployDiagnosticsSnapshot(status deployStatusResponse) deploydiag.Snapshot {
	targets := make([]deploydiag.Target, 0, len(status.Targets))
	for _, target := range status.Targets {
		targets = append(targets, deploydiag.Target{
			Domain:       target.Domain,
			Enabled:      target.Enabled,
			CertPresent:  target.CertPresent,
			CertNotAfter: target.CertNotAfter,
		})
	}
	return deploydiag.Snapshot{
		Helper:          deployDiagHelperStatus(status.PrivilegedListener),
		HelperPublic:    status.HelperPublic,
		EdgeHTTPSListen: status.Edge.HTTPSListen,
		Targets:         targets,
		CurrentVersion:  buildVersionResponse().Version,
	}
}

// deployDiagnosticsDeps wires the injectable probe functions into the
// diagnostics engine at call time so tests can stub the package variables.
func deployDiagnosticsDeps() deploydiag.Deps {
	return deploydiag.Deps{
		PortListener:   deployPortListenerFunc,
		LANIP:          deployLANIPFunc,
		HTTPProbe:      deployHTTPProbeFunc,
		PublicIP:       deployPublicIPFunc,
		DNSLookup:      deployDNSLookupFunc,
		PowerStatus:    deployPowerStatusFunc,
		FirewallStatus: deployFirewallStatusFunc,
		TLSProbe: func(addr, serverName string) deploydiag.TLSProbeResult {
			result := edgeTLSProbeFunc(addr, serverName, edgeTLSProbeTimeout)
			return deploydiag.TLSProbeResult{Outcome: deploydiag.TLSProbeOutcome(result.Outcome), Error: result.Error}
		},
	}
}

func deployAgentStatusFor(paths localagent.Paths) deployAgentStatus {
	status := deployAgentStatus{
		State:      "stopped",
		StatePath:  paths.StatePath,
		SocketPath: paths.SocketPath,
	}
	state, err := localagent.LoadState(paths.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		status.State = "unhealthy"
		status.Message = err.Error()
		return status
	}
	status.PID = state.PID
	status.RouterAddr = state.RouterAddr
	if processAliveForEdge(state.PID) {
		status.State = "running"
	} else {
		status.State = "stale"
	}
	return status
}

func deployAgentSupervisorStatusFor(paths localagent.Paths) deployAgentSupervisorStatus {
	if deployServiceManagerFunc() == "systemd" {
		status := localagent.AgentSystemdStatusForSocket(paths.SocketPath)
		return deployAgentSupervisorStatus{
			Installed: status.PlistPresent && status.SupervisesSocket,
			Loaded:    status.Loaded,
			Running:   status.Running,
			PID:       status.PID,
			Label:     status.Label,
			Path:      status.PlistPath,
		}
	}
	status := agentSupervisorStatusFunc(paths.SocketPath)
	return deployAgentSupervisorStatus{
		Installed: status.PlistPresent && status.SupervisesSocket,
		Loaded:    status.Loaded,
		Running:   status.Running,
		PID:       status.PID,
		Label:     status.Label,
		Path:      status.PlistPath,
	}
}

func deploySessionsByAppRoot(paths localagent.Paths) map[string]localagent.Session {
	out := map[string]localagent.Session{}
	registry, err := localagent.OpenRegistry(paths.RegistryPath, localagent.RouterAddrFromEnv())
	if err != nil {
		return out
	}
	for _, session := range registry.List() {
		if session.Status != "running" {
			continue
		}
		root := filepath.Clean(session.AppRoot)
		if _, ok := out[root]; !ok {
			out[root] = session
		}
	}
	return out
}

func deployCertStatusFor(paths localagent.Paths, domain string) deployCertStatus {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return deployCertStatus{}
	}
	matches, _ := filepath.Glob(filepath.Join(paths.EdgeDir, "caddy-data", "certificates", "*", domain, "*.crt"))
	if len(matches) == 0 {
		return deployCertStatus{}
	}
	sort.Strings(matches)
	status := deployCertStatus{Present: true, Path: matches[0]}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		status.Error = err.Error()
		return status
	}
	block, _ := pem.Decode(data)
	if block == nil {
		status.Error = "certificate PEM block not found"
		return status
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.NotAfter = cert.NotAfter
	return status
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
	defer file.Close()
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
