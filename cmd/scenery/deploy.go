package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
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
	BasePath      string `json:"base_path"`
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
	deployActivateRegistryCandidateFunc  = deployActivateRegistryCandidate
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
	return runDeployCommandWithStatusDependencies(stdout, args, liveDeployStatusDependencies())
}

func runDeployCommandWithStatusDependencies(stdout io.Writer, args []string, statusDeps deployStatusDependencies) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery deploy <ssh-target> [--app-root <path>] | setup|status|enable|disable|publish|resume|teardown [-o json]")
	}
	subcommand := args[0]
	if strings.HasPrefix(subcommand, "-") {
		return runDeploySSH(stdout, "", args, deploySSHTools{})
	}
	if subcommand == "plan" || subcommand == "apply" {
		return runDeployment(stdout, args)
	}
	if subcommand == "receive" {
		return deployReceiveCommand(stdout, args[1:])
	}
	if !isDeploySubcommand(subcommand) {
		if err := deployTargetTypoError(subcommand, configuredDeployTargets(args[1:])); err != nil {
			return err
		}
		return runDeploySSH(stdout, subcommand, args[1:], deploySSHTools{})
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
		return runDeployStatusWithDependencies(stdout, opts, statusDeps)
	case "setup":
		return runDeploySetup(stdout, opts)
	case "resume":
		return runDeployResume(stdout, opts)
	case "teardown":
		return runDeployTeardown(stdout, opts)
	default:
		return usageErrorf("unknown scenery deploy subcommand %q", subcommand)
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
		return deployOptions{}, usageErrorf("--acme-ca must be production or staging")
	}
	opts.Env = strings.TrimSpace(opts.Env)
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return deployOptions{}, usageErrorf("--env must not be empty")
	}
	if subcommand != "setup" && (opts.ACMEEmail != "" || opts.ACMECA != "") {
		return deployOptions{}, usageErrorf("--acme-email and --acme-ca are only supported by scenery deploy setup")
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
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	rootService := deployRootService(cfg)
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
	paths, err := commandAgentPaths()
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
		if registered, err := canonicalDeployAppRoot(registry.Targets[i].AppRoot); err != nil || registered != appRoot {
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

func runDeployStatusWithDependencies(stdout io.Writer, opts deployOptions, deps deployStatusDependencies) error {
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	status := buildDeployStatusWithDependencies(context.Background(), paths, registry, deps)
	if opts.JSON {
		return writeCLIJSON(stdout, status)
	}
	_, _ = fmt.Fprintf(stdout, "Deploy: %s\n", readyWord(status.Ready))
	for _, diag := range status.Diagnostics {
		_, _ = fmt.Fprintf(stdout, "- %s\n", diag)
	}
	for _, target := range status.Targets {
		state := "disabled"
		if target.Enabled {
			state = "enabled"
		}
		_, _ = fmt.Fprintf(stdout, "%s  %s  %s\n", target.Domain, state, target.AppRoot)
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
	paths, err := commandAgentPaths()
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
	_, _ = fmt.Fprintf(stdout, "configured public deploy edge (%s CA)\n", resp.ACME.CA)
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

