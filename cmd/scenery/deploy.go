package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
)

type deployOptions struct {
	AppRoot   string
	JSON      bool
	ACMEEmail string
	ACMECA    string
}

const deployHelperContractVersion = localagent.EdgeTargetSchemaVersion

type deployMutationResponse struct {
	SchemaVersion string                    `json:"schema_version"`
	Action        string                    `json:"action"`
	RegistryPath  string                    `json:"registry_path"`
	Targets       []localagent.DeployTarget `json:"targets"`
}

type deployStatusResponse struct {
	SchemaVersion      string                       `json:"schema_version"`
	Ready              bool                         `json:"ready"`
	RegistryPath       string                       `json:"registry_path"`
	PrivilegedListener edgeStatusPrivilegedListener `json:"privileged_listener"`
	HelperPublic       bool                         `json:"helper_public"`
	Edge               edgeStatusCaddy              `json:"edge"`
	Agent              deployAgentStatus            `json:"agent"`
	LaunchAgent        deployLaunchAgentStatus      `json:"launch_agent"`
	ACME               deployACMEStatus             `json:"acme"`
	Targets            []deployTargetStatus         `json:"targets"`
	Diagnostics        []string                     `json:"diagnostics,omitempty"`
	DiagnosticsDetail  *deployDiagnosticReport      `json:"diagnostics_detail,omitempty"`
}

type deploySetupResponse struct {
	SchemaVersion        string           `json:"schema_version"`
	RegistryPath         string           `json:"registry_path"`
	ACME                 deployACMEStatus `json:"acme"`
	HelperVersion        string           `json:"helper_version"`
	HelperPublic         bool             `json:"helper_public"`
	LaunchAgentInstalled bool             `json:"launch_agent_installed"`
	EdgeRestarted        bool             `json:"edge_restarted"`
}

type deployTeardownResponse struct {
	SchemaVersion      string `json:"schema_version"`
	RegistryPath       string `json:"registry_path"`
	HelperVersion      string `json:"helper_version"`
	HelperPublic       bool   `json:"helper_public"`
	LaunchAgentRemoved bool   `json:"launch_agent_removed"`
	EdgeRestarted      bool   `json:"edge_restarted"`
}

type deployResumeResponse struct {
	SchemaVersion string               `json:"schema_version"`
	RegistryPath  string               `json:"registry_path"`
	LogPath       string               `json:"log_path"`
	AgentReady    bool                 `json:"agent_ready"`
	EdgeRestarted bool                 `json:"edge_restarted"`
	Targets       []deployResumeTarget `json:"targets"`
}

type deployResumeTarget struct {
	Domain    string `json:"domain"`
	AppRoot   string `json:"app_root"`
	Action    string `json:"action"`
	SessionID string `json:"session_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type deployAgentStatus struct {
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
	StatePath  string `json:"state_path"`
	SocketPath string `json:"socket_path"`
	RouterAddr string `json:"router_addr,omitempty"`
	Message    string `json:"message,omitempty"`
}

type deployLaunchAgentStatus struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
}

type deployACMEStatus struct {
	Email string `json:"email,omitempty"`
	CA    string `json:"ca"`
}

type deployTargetStatus struct {
	Domain       string   `json:"domain"`
	AppRoot      string   `json:"app_root"`
	RootService  string   `json:"root_service,omitempty"`
	Enabled      bool     `json:"enabled"`
	LiveSession  bool     `json:"live_session"`
	SessionID    string   `json:"session_id,omitempty"`
	CertPresent  bool     `json:"cert_present"`
	CertNotAfter string   `json:"cert_not_after,omitempty"`
	Diagnostics  []string `json:"diagnostics,omitempty"`
}

type deployDiagnosticReport struct {
	LANIP    string                  `json:"lan_ip,omitempty"`
	PublicIP string                  `json:"public_ip,omitempty"`
	Checks   []deployDiagnosticCheck `json:"checks,omitempty"`
}

type deployDiagnosticCheck struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	Message         string         `json:"message"`
	SuggestedAction string         `json:"suggested_action,omitempty"`
	Observed        map[string]any `json:"observed,omitempty"`
}

type deployHTTPProbeResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

type deployPowerStatus struct {
	Supported    bool
	SleepMinutes int
	Raw          string
}

type deployFirewallStatus struct {
	Supported bool
	Enabled   bool
	Raw       string
}

type deployCertStatus struct {
	Present  bool
	Path     string
	NotAfter time.Time
	Error    string
}

type deployHelperDrift struct {
	HelperInstalled bool   `json:"helper_installed"`
	ActionRequired  bool   `json:"action_required"`
	HelperVersion   string `json:"helper_version,omitempty"`
	CurrentVersion  string `json:"current_version,omitempty"`
	TargetSchema    string `json:"target_schema,omitempty"`
	ExpectedSchema  string `json:"expected_schema"`
	Message         string `json:"message,omitempty"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

type deployPortListenerInfo struct {
	Port    int
	PID     int
	Command string
	Name    string
}

var (
	deploySetupPreflightFunc             = deployPreflightPublicPorts
	deployPrivilegedHelperInstallFunc    = deployPrivilegedHelperInstall
	deployLoopbackHelperInstallFunc      = deployLoopbackHelperInstall
	deployInstallResumeLaunchAgentFunc   = installDeployResumeLaunchAgent
	deployRemoveResumeLaunchAgentFunc    = removeDeployResumeLaunchAgent
	deployEnsureAgentFunc                = func() error { return ensureEdgeAgent(localagent.RouterAddrFromEnv(), false) }
	deployEdgeRestartFunc                = func() error { return edgeRestart(edgeOptions{Deploy: true}) }
	deployRefreshEdgeAfterMutationFunc   = deployRefreshEdgeAfterMutation
	deployRunUpDetachFunc                = deployRunUpDetach
	deployPortListenerFunc               = defaultDeployPortListener
	deployPrivilegedHelperExecutableFunc = os.Executable
	deployLANIPFunc                      = defaultDeployLANIP
	deployHTTPProbeFunc                  = defaultDeployHTTPProbe
	deployPublicIPFunc                   = defaultDeployPublicIP
	deployDNSLookupFunc                  = net.LookupIP
	deployPowerStatusFunc                = defaultDeployPowerStatus
	deployFirewallStatusFunc             = defaultDeployFirewallStatus
)

func deployCommand(args []string) error {
	return runDeployCommand(os.Stdout, args)
}

func runDeployCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery deploy setup|status|enable|disable|resume|teardown [--json]")
	}
	subcommand := args[0]
	if subcommand == "plan" || subcommand == "apply" {
		return runVNextDeploy(stdout, args)
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

func parseDeployOptions(subcommand string, args []string) (deployOptions, error) {
	var opts deployOptions
	flags := newCLIFlagSet("deploy " + subcommand)
	flags.BoolVar(&opts.JSON, "json", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
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
	domain := strings.TrimSpace(cfg.Deploy.Domain)
	if domain == "" {
		return fmt.Errorf("%s has no deploy.domain; add deploy.domain before running scenery deploy enable", cfg.SourcePath(appRoot))
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	rootService := deployRootService(cfg)
	if err := upsertDeployTarget(&registry, localagent.DeployTarget{
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
		SchemaVersion: "scenery.deploy.target.v1",
		Action:        "enable",
		RegistryPath:  paths.DeployPath,
		Targets:       registry.Targets,
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
		SchemaVersion: "scenery.deploy.target.v1",
		Action:        "disable",
		RegistryPath:  paths.DeployPath,
		Targets:       registry.Targets,
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
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
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
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery deploy setup is currently supported on macOS")
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
	if err := deployPrivilegedHelperInstallFunc(paths, helperVersion); err != nil {
		return err
	}
	if err := deployInstallResumeLaunchAgentFunc(paths); err != nil {
		return err
	}
	if err := deployEdgeRestartFunc(); err != nil {
		return err
	}
	resp := deploySetupResponse{
		SchemaVersion: "scenery.deploy.setup.v1",
		RegistryPath:  paths.DeployPath,
		ACME: deployACMEStatus{
			Email: registry.ACMEEmail,
			CA:    firstNonEmpty(registry.ACMECA, "production"),
		},
		HelperVersion:        helperVersion,
		HelperPublic:         true,
		LaunchAgentInstalled: true,
		EdgeRestarted:        true,
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}
	fmt.Fprintf(stdout, "configured public deploy edge (%s CA)\n", resp.ACME.CA)
	return nil
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
	if err := deployEdgeRestartFunc(); err != nil {
		return err
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	sessions := deploySessionsByAppRoot(paths)
	resp := deployResumeResponse{
		SchemaVersion: "scenery.deploy.resume.v1",
		RegistryPath:  paths.DeployPath,
		LogPath:       paths.DeployResumeLogPath,
		AgentReady:    true,
		EdgeRestarted: true,
	}
	for _, target := range registry.Targets {
		if !target.Enabled {
			continue
		}
		item := deployResumeTarget{Domain: target.Domain, AppRoot: target.AppRoot}
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
		if err := deployRunUpDetachFunc(target.AppRoot); err != nil {
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
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
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

func runDeployTeardown(stdout io.Writer, opts deployOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("scenery deploy teardown is currently supported on macOS")
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
	if err := deployEdgeRestartFunc(); err != nil {
		return err
	}
	resp := deployTeardownResponse{
		SchemaVersion:      "scenery.deploy.teardown.v1",
		RegistryPath:       paths.DeployPath,
		HelperVersion:      helperVersion,
		HelperPublic:       false,
		LaunchAgentRemoved: removed,
		EdgeRestarted:      true,
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}
	fmt.Fprintln(stdout, "disabled public deploy edge; local HTTPS remains available")
	return nil
}

func writeDeployMutation(stdout io.Writer, jsonMode bool, resp deployMutationResponse) error {
	if jsonMode {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
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

func deployRootService(cfg appcfg.Config) string {
	if root := strings.TrimSpace(cfg.Deploy.Root); root != "" {
		return root
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
	launchAgent := deployLaunchAgentStatusFor()
	sessions := deploySessionsByAppRoot(paths)
	targets := make([]deployTargetStatus, 0, len(registry.Targets))
	for _, target := range registry.Targets {
		session := sessions[filepath.Clean(target.AppRoot)]
		cert := deployCertStatusFor(paths, target.Domain)
		item := deployTargetStatus{
			Domain:      target.Domain,
			AppRoot:     target.AppRoot,
			RootService: target.RootService,
			Enabled:     target.Enabled,
			LiveSession: session.SessionID != "",
			SessionID:   session.SessionID,
			CertPresent: cert.Present,
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
		targets = append(targets, item)
	}
	helperPublic := deployHelperHasPublicBinding(helper.Listen)
	status := deployStatusResponse{
		SchemaVersion:      "scenery.deploy.status.v1",
		RegistryPath:       paths.DeployPath,
		PrivilegedListener: helper,
		HelperPublic:       helperPublic,
		Edge:               edgeStatus,
		Agent:              agent,
		LaunchAgent:        launchAgent,
		ACME: deployACMEStatus{
			Email: registry.ACMEEmail,
			CA:    firstNonEmpty(registry.ACMECA, "production"),
		},
		Targets: targets,
	}
	if helper.State != "running" || !helperPublic {
		status.Diagnostics = append(status.Diagnostics, "public privileged listener is not ready; run `scenery deploy setup`")
	}
	if edgeStatus.State != localagent.EdgeStatusRunning {
		status.Diagnostics = append(status.Diagnostics, "Caddy edge is not running")
	}
	if agent.State != "running" {
		status.Diagnostics = append(status.Diagnostics, "Scenery agent is not running")
	}
	if !launchAgent.Installed {
		status.Diagnostics = append(status.Diagnostics, "deploy resume LaunchAgent is not installed")
	}
	diagnostics := buildDeployDiagnostics(ctx, paths, registry, status)
	status.DiagnosticsDetail = &diagnostics
	for _, check := range diagnostics.Checks {
		if check.Status == "warn" || check.Status == "error" {
			status.Diagnostics = append(status.Diagnostics, check.Message)
		}
	}
	status.Ready = len(status.Diagnostics) == 0
	return status
}

func deployAgentStatusFor(paths localagent.Paths) deployAgentStatus {
	status := deployAgentStatus{
		State:      "stopped",
		StatePath:  paths.StatePath,
		SocketPath: paths.SocketPath,
	}
	data, err := os.ReadFile(paths.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		status.State = "unhealthy"
		status.Message = err.Error()
		return status
	}
	var state localagent.State
	if err := json.Unmarshal(data, &state); err != nil {
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

func deployLaunchAgentStatusFor() deployLaunchAgentStatus {
	path := deployResumeLaunchAgentPath()
	_, err := os.Stat(path)
	return deployLaunchAgentStatus{Installed: err == nil, Path: path}
}

func deployResumeLaunchAgentPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/Library/LaunchAgents/dev.scenery.deploy-resume.plist"
	}
	return filepath.Join(home, "Library", "LaunchAgents", "dev.scenery.deploy-resume.plist")
}

func installDeployResumeLaunchAgent(paths localagent.Paths) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	if deployExecutableIsHarness(exe) {
		return fmt.Errorf("refusing to install deploy resume LaunchAgent from harness binary %s", exe)
	}
	plistPath := deployResumeLaunchAgentPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(plistPath, []byte(deployResumeLaunchAgentPlist(exe, paths.DeployResumeLogPath)), 0o644)
}

func deployExecutableIsHarness(exe string) bool {
	return strings.Contains(filepath.Clean(exe), string(os.PathSeparator)+".scenery"+string(os.PathSeparator)+"harness"+string(os.PathSeparator))
}

func deployResumeLaunchAgentPlist(exe, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.scenery.deploy-resume</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>deploy</string>
		<string>resume</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, escapePlistString(exe), escapePlistString(logPath), escapePlistString(logPath))
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

func deployHelperHasPublicBinding(listen []string) bool {
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

func buildDeployDiagnostics(ctx context.Context, paths localagent.Paths, registry localagent.DeployRegistry, status deployStatusResponse) deployDiagnosticReport {
	report := deployDiagnosticReport{}
	addDeployHelperDiagnostics(&report, paths, status.PrivilegedListener)
	addDeployListenerDiagnostics(&report, status.PrivilegedListener)
	addDeployCertDiagnostics(&report, status.Targets)
	if !deployHasEnabledTarget(registry) {
		return report
	}
	lanOK := addDeployLANDiagnostics(ctx, &report)
	publicOK := addDeployPublicIPDiagnostics(ctx, &report, lanOK)
	addDeployDNSDiagnostics(&report, registry)
	if !publicOK && lanOK {
		report.addCheck(deployDiagnosticCheck{
			ID:              "deploy.reachability.public",
			Status:          "warn",
			Message:         "LAN self-probe works but the public IP self-probe failed; verify router forwarding for TCP 80/443 to this Mac",
			SuggestedAction: "Forward public TCP 80/443 to the reported LAN IP. If the router WAN IP differs from the reported public IP or is in 100.64.0.0/10, ask the ISP for a public address because CGNAT can block inbound traffic.",
		})
	}
	addDeployPowerDiagnostic(ctx, &report)
	addDeployFirewallDiagnostic(ctx, &report)
	return report
}

func deployHasEnabledTarget(registry localagent.DeployRegistry) bool {
	for _, target := range registry.Targets {
		if target.Enabled {
			return true
		}
	}
	return false
}

func addDeployHelperDiagnostics(report *deployDiagnosticReport, paths localagent.Paths, helper edgeStatusPrivilegedListener) {
	status := "ok"
	message := "public privileged helper is installed and running"
	action := ""
	if !helper.Installed || helper.State != "running" {
		status = "warn"
		message = "public privileged helper is not running"
		action = "Run `scenery deploy setup` to install and start the public edge helper."
	}
	report.addCheck(deployDiagnosticCheck{
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
	drift := deployHelperDriftFor(paths, helper, buildVersionResponse().Version)
	versionStatus := "ok"
	if drift.ActionRequired {
		versionStatus = "warn"
	}
	report.addCheck(deployDiagnosticCheck{
		ID:              "deploy.helper_version",
		Status:          versionStatus,
		Message:         drift.Message,
		SuggestedAction: drift.SuggestedAction,
		Observed: map[string]any{
			"helper_version":        drift.HelperVersion,
			"current_version":       drift.CurrentVersion,
			"target_schema_version": drift.TargetSchema,
			"expected_schema":       drift.ExpectedSchema,
		},
	})
}

func deployHelperDriftFor(paths localagent.Paths, helper edgeStatusPrivilegedListener, currentVersion string) deployHelperDrift {
	targetPath := strings.TrimSpace(helper.TargetPath)
	if targetPath == "" {
		targetPath = paths.EdgeTargetPath
	}
	targetState, _ := localagent.LoadEdgeTargetState(targetPath)
	drift := deployHelperDrift{
		HelperInstalled: helper.Installed,
		HelperVersion:   strings.TrimSpace(helper.Version),
		CurrentVersion:  strings.TrimSpace(currentVersion),
		TargetSchema:    strings.TrimSpace(targetState.SchemaVersion),
		ExpectedSchema:  deployHelperContractVersion,
	}
	switch {
	case !helper.Installed:
		drift.Message = "privileged helper is not installed"
	case drift.TargetSchema != "" && drift.TargetSchema != deployHelperContractVersion:
		drift.ActionRequired = true
		drift.Message = fmt.Sprintf("edge target metadata is %s; current binary expects %s", drift.TargetSchema, deployHelperContractVersion)
		drift.SuggestedAction = "Run `scenery deploy setup` to update the privileged listener (asks for sudo)."
	case drift.HelperVersion == "":
		drift.Message = "helper version is not recorded"
	case drift.CurrentVersion != "" && drift.HelperVersion != drift.CurrentVersion:
		drift.Message = fmt.Sprintf("helper is %s and current binary is %s", drift.HelperVersion, drift.CurrentVersion)
	default:
		drift.Message = fmt.Sprintf("helper version %s matches current binary", drift.HelperVersion)
	}
	return drift
}

func addDeployListenerDiagnostics(report *deployDiagnosticReport, helper edgeStatusPrivilegedListener) {
	for _, port := range []int{80, 443} {
		listener, ok, err := deployPortListenerFunc(port)
		check := deployDiagnosticCheck{
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
		if !listener.isSceneryHelper() {
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d is bound by %s, not the Scenery public helper", port, listener.String())
			check.SuggestedAction = fmt.Sprintf("Stop that process, then run `scenery deploy setup` so Scenery can bind TCP %d.", port)
		} else if !deployListenerIsWildcard(listener, helper.Listen, port) {
			check.Status = "warn"
			check.Message = fmt.Sprintf("port %d is not bound on a wildcard address", port)
			check.SuggestedAction = "Run `scenery deploy setup` to reinstall the helper with 0.0.0.0/[::] public listeners."
		}
		report.addCheck(check)
	}
}

func addDeployCertDiagnostics(report *deployDiagnosticReport, targets []deployTargetStatus) {
	now := time.Now()
	for _, target := range targets {
		if !target.Enabled {
			continue
		}
		check := deployDiagnosticCheck{
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

func addDeployLANDiagnostics(ctx context.Context, report *deployDiagnosticReport) bool {
	lanIP, err := deployLANIPFunc(ctx)
	if err != nil || strings.TrimSpace(lanIP) == "" {
		message := "LAN IP could not be determined"
		if err != nil {
			message += ": " + err.Error()
		}
		report.addCheck(deployDiagnosticCheck{
			ID:              "deploy.lan_ip",
			Status:          "warn",
			Message:         message,
			SuggestedAction: "Connect to a LAN interface or pass this Mac's LAN IP to your router forwarding rules manually.",
		})
		return false
	}
	report.LANIP = strings.TrimSpace(lanIP)
	report.addCheck(deployDiagnosticCheck{
		ID:      "deploy.lan_ip",
		Status:  "ok",
		Message: "LAN IP is " + report.LANIP,
		Observed: map[string]any{
			"lan_ip": report.LANIP,
		},
	})
	httpOK := addDeployHTTPProbeCheck(ctx, report, "deploy.probe.lan_http", "LAN HTTP self-probe", deployURLForHost("http", report.LANIP))
	httpsOK := addDeployHTTPProbeCheck(ctx, report, "deploy.probe.lan_https", "LAN HTTPS self-probe", deployURLForHost("https", report.LANIP))
	return httpOK && httpsOK
}

func addDeployPublicIPDiagnostics(ctx context.Context, report *deployDiagnosticReport, lanOK bool) bool {
	publicIP, err := deployPublicIPFunc(ctx)
	if err != nil || strings.TrimSpace(publicIP) == "" {
		message := "public IP could not be discovered"
		if err != nil {
			message += ": " + err.Error()
		}
		report.addCheck(deployDiagnosticCheck{
			ID:              "deploy.public_ip",
			Status:          "warn",
			Message:         message,
			SuggestedAction: "Check internet connectivity, then rerun `scenery deploy status`.",
		})
		return false
	}
	report.PublicIP = strings.TrimSpace(publicIP)
	report.addCheck(deployDiagnosticCheck{
		ID:      "deploy.public_ip",
		Status:  "ok",
		Message: "public IP is " + report.PublicIP,
		Observed: map[string]any{
			"public_ip": report.PublicIP,
		},
	})
	httpOK := addDeployHTTPProbeCheck(ctx, report, "deploy.probe.public_http", "public HTTP self-probe", deployURLForHost("http", report.PublicIP))
	httpsOK := addDeployHTTPProbeCheck(ctx, report, "deploy.probe.public_https", "public HTTPS self-probe", deployURLForHost("https", report.PublicIP))
	if lanOK && !httpOK && !httpsOK && deployIPIsCGNATHint(report.PublicIP) {
		report.addCheck(deployDiagnosticCheck{
			ID:              "deploy.reachability.cgnat",
			Status:          "warn",
			Message:         "public IP looks like carrier-grade NAT, so inbound router forwarding may not be possible",
			SuggestedAction: "Ask the ISP for a public IPv4 address or use an IPv6 AAAA record that reaches this Mac directly.",
			Observed:        map[string]any{"public_ip": report.PublicIP},
		})
	}
	return httpOK && httpsOK
}

func addDeployDNSDiagnostics(report *deployDiagnosticReport, registry localagent.DeployRegistry) {
	for _, target := range registry.Targets {
		if !target.Enabled {
			continue
		}
		ips, err := deployDNSLookupFunc(target.Domain)
		check := deployDiagnosticCheck{
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
		ipStrings := deployIPStrings(ips)
		check.Observed["ips"] = ipStrings
		check.Observed["public_ip"] = report.PublicIP
		if report.PublicIP == "" {
			check.Status = "warn"
			check.Message = fmt.Sprintf("DNS for %s resolves, but public IP discovery failed", target.Domain)
			check.SuggestedAction = "Fix public IP discovery, then confirm DNS matches that address."
		} else if !deployIPsContain(ips, report.PublicIP) {
			if deployIPsAreCloudflareProxy(ips) {
				check.Message = fmt.Sprintf("DNS for %s resolves to Cloudflare proxy IPs; verify the Cloudflare origin record points to %s", target.Domain, report.PublicIP)
				check.Observed["cloudflare_proxy"] = true
				report.addCheck(check)
				continue
			}
			check.Status = "warn"
			check.Message = fmt.Sprintf("DNS for %s resolves to %s, not this public IP %s", target.Domain, strings.Join(ipStrings, ", "), report.PublicIP)
			check.SuggestedAction = fmt.Sprintf("Set the A/AAAA record for %s to %s, then wait for DNS propagation.", target.Domain, report.PublicIP)
		} else {
			check.Message = fmt.Sprintf("DNS for %s resolves to this public IP", target.Domain)
		}
		report.addCheck(check)
	}
}

func addDeployPowerDiagnostic(ctx context.Context, report *deployDiagnosticReport) {
	power, err := deployPowerStatusFunc(ctx)
	if err != nil {
		report.addCheck(deployDiagnosticCheck{
			ID:      "deploy.power.sleep",
			Status:  "warn",
			Message: "power sleep settings could not be inspected: " + err.Error(),
		})
		return
	}
	if !power.Supported {
		report.addCheck(deployDiagnosticCheck{
			ID:      "deploy.power.sleep",
			Status:  "skipped",
			Message: "power sleep diagnostics are supported on macOS",
		})
		return
	}
	check := deployDiagnosticCheck{
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

func addDeployFirewallDiagnostic(ctx context.Context, report *deployDiagnosticReport) {
	firewall, err := deployFirewallStatusFunc(ctx)
	if err != nil {
		report.addCheck(deployDiagnosticCheck{
			ID:      "deploy.firewall",
			Status:  "warn",
			Message: "application firewall state could not be inspected: " + err.Error(),
		})
		return
	}
	if !firewall.Supported {
		report.addCheck(deployDiagnosticCheck{
			ID:      "deploy.firewall",
			Status:  "skipped",
			Message: "application firewall diagnostics are supported on macOS",
		})
		return
	}
	check := deployDiagnosticCheck{
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

func addDeployHTTPProbeCheck(ctx context.Context, report *deployDiagnosticReport, id, name, url string) bool {
	result := deployHTTPProbeFunc(ctx, url)
	check := deployDiagnosticCheck{
		ID:      id,
		Status:  "ok",
		Message: name + " reached " + url,
		Observed: map[string]any{
			"url":         url,
			"status_code": result.StatusCode,
		},
	}
	if !result.OK {
		if deployRawIPHTTPSNeedsSNI(url, result.Error) {
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

func deployRawIPHTTPSNeedsSNI(rawURL, errText string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || net.ParseIP(u.Hostname()) == nil {
		return false
	}
	return strings.Contains(errText, "tls: internal error") || strings.Contains(errText, "tlsv1 alert internal error")
}

func (report *deployDiagnosticReport) addCheck(check deployDiagnosticCheck) {
	report.Checks = append(report.Checks, check)
}

func defaultDeployLANIP(ctx context.Context) (string, error) {
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

func defaultDeployHTTPProbe(ctx context.Context, rawURL string) deployHTTPProbeResult {
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
		return deployHTTPProbeResult{Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return deployHTTPProbeResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	return deployHTTPProbeResult{OK: true, StatusCode: resp.StatusCode}
}

func defaultDeployPublicIP(ctx context.Context) (string, error) {
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

func defaultDeployPowerStatus(ctx context.Context) (deployPowerStatus, error) {
	if runtime.GOOS != "darwin" {
		return deployPowerStatus{Supported: false}, nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "pmset", "-g").CombinedOutput()
	if err != nil {
		return deployPowerStatus{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	raw := string(out)
	return deployPowerStatus{Supported: true, SleepMinutes: parsePMSetSleepMinutes(raw), Raw: strings.TrimSpace(raw)}, nil
}

func defaultDeployFirewallStatus(ctx context.Context) (deployFirewallStatus, error) {
	if runtime.GOOS != "darwin" {
		return deployFirewallStatus{Supported: false}, nil
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
		return deployFirewallStatus{Supported: true, Enabled: enabled, Raw: raw}, nil
	}
	return deployFirewallStatus{}, lastErr
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

func deployURLForHost(scheme, host string) string {
	host = strings.TrimSpace(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + "/"
}

func deployListenerIsWildcard(listener deployPortListenerInfo, configured []string, port int) bool {
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

func deployIPStrings(ips []net.IP) []string {
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

func deployIPsContain(ips []net.IP, want string) bool {
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

func deployIPsAreCloudflareProxy(ips []net.IP) bool {
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

func deployIPIsCGNATHint(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip)).To4()
	if parsed == nil {
		return false
	}
	return parsed[0] == 100 && parsed[1] >= 64 && parsed[1] <= 127
}

func deployPreflightPublicPorts(paths localagent.Paths) error {
	_ = paths
	for _, port := range []int{80, 443} {
		listener, ok, err := deployPortListenerFunc(port)
		if err != nil {
			return err
		}
		if !ok || listener.isSceneryHelper() {
			continue
		}
		return fmt.Errorf("port %d is already in use by %s; stop it before running scenery deploy setup", port, listener.String())
	}
	return nil
}

func deployRefreshEdgeAfterMutation(paths localagent.Paths) error {
	helper := privilegedListenerStatus(paths)
	if !helper.Installed || !deployHelperHasPublicBinding(helper.Listen) {
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

func defaultDeployPortListener(port int) (deployPortListenerInfo, bool, error) {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").CombinedOutput()
	if err == nil {
		if info, ok := parseDeployLsofPortListener(string(out), port); ok {
			return deployHydratePortListenerCommand(info), true, nil
		}
	} else {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return deployPortListenerInfo{}, false, err
		}
	}

	out, err = exec.Command("netstat", "-anv", "-p", "tcp").CombinedOutput()
	if err != nil {
		return deployPortListenerInfo{}, false, nil
	}
	if info, ok := parseDeployNetstatPortListener(string(out), port); ok {
		return deployHydratePortListenerCommand(info), true, nil
	}
	return deployPortListenerInfo{}, false, nil
}

func parseDeployLsofPortListener(output string, port int) (deployPortListenerInfo, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "COMMAND" {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		info := deployPortListenerInfo{
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
	return deployPortListenerInfo{}, false
}

func parseDeployNetstatPortListener(output string, port int) (deployPortListenerInfo, bool) {
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
			return deployPortListenerInfo{
				Port:    port,
				PID:     pid,
				Command: name,
				Name:    fields[3],
			}, true
		}
	}
	return deployPortListenerInfo{}, false
}

func deployHydratePortListenerCommand(info deployPortListenerInfo) deployPortListenerInfo {
	if info.PID > 0 {
		if cmdline, err := exec.Command("ps", "-p", strconv.Itoa(info.PID), "-o", "command=").Output(); err == nil && strings.TrimSpace(string(cmdline)) != "" {
			info.Command = strings.TrimSpace(string(cmdline))
		}
	}
	return info
}

func (info deployPortListenerInfo) isSceneryHelper() bool {
	command := strings.ToLower(info.Command)
	return strings.Contains(command, "scenery-edge-helper") || strings.Contains(command, edgeHelperLabel)
}

func (info deployPortListenerInfo) String() string {
	command := strings.TrimSpace(info.Command)
	if command == "" {
		command = "unknown process"
	}
	if info.PID > 0 {
		return fmt.Sprintf("%s (pid %d)", command, info.PID)
	}
	return command
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

func deployRunUpDetach(appRoot string) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	out, err := exec.Command(exe, "up", "--detach", "--app-root", appRoot).CombinedOutput()
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

func removeDeployResumeLaunchAgent() (bool, error) {
	err := os.Remove(deployResumeLaunchAgentPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func readyWord(ok bool) string {
	if ok {
		return "ready"
	}
	return "not ready"
}
