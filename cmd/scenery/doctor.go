package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/deploydiag"
	"scenery.sh/internal/doctor"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/toolchain"
	"scenery.sh/internal/tscheck"
)

const doctorResultKind = "scenery.doctor.result"

type doctorOptions struct {
	AppRoot string
	JSON    bool
}

type doctorResponse struct {
	cliPayloadIdentity
	OK          bool               `json:"ok"`
	Summary     doctor.Summary     `json:"summary"`
	Scenery     versionResponse    `json:"scenery"`
	App         *doctor.AppInfo    `json:"app,omitempty"`
	Environment doctor.Environment `json:"environment"`
	Deploy      *doctorDeployInfo  `json:"deploy,omitempty"`
	Checks      []doctor.Check     `json:"checks"`
}

type doctorDeployInfo struct {
	cliPayloadIdentity
	Ready        bool                 `json:"ready"`
	RegistryPath string               `json:"registry_path"`
	Targets      []deployTargetStatus `json:"targets"`
	Diagnostics  deploydiag.Report    `json:"diagnostics"`
}

func doctorCommand(args []string) error {
	return runSceneryDoctor(context.Background(), os.Stdout, args)
}

func runSceneryDoctor(ctx context.Context, stdout io.Writer, args []string) error {
	return runSceneryDoctorWithDeps(ctx, stdout, args, doctor.DefaultProbeDeps())
}

func runSceneryDoctorWithDeps(ctx context.Context, stdout io.Writer, args []string, deps doctor.ProbeDeps) error {
	opts, err := parseDoctorArgs(args)
	if err != nil {
		return err
	}
	resp := buildDoctorResponse(ctx, opts, deps)
	if opts.JSON {
		if err := writeDoctorJSON(stdout, resp); err != nil {
			return err
		}
		if !resp.OK {
			return &silentCLIError{err: fmt.Errorf("scenery doctor found %d error(s)", resp.Summary.Errors)}
		}
		return nil
	}
	if err := writeDoctorText(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("scenery doctor found %d error(s)", resp.Summary.Errors)
	}
	return nil
}

func parseDoctorArgs(args []string) (doctorOptions, error) {
	opts := doctorOptions{}
	flags := newCLIFlagSet("doctor")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return doctorOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return doctorOptions{}, err
	}
	return opts, nil
}

func buildDoctorResponse(ctx context.Context, opts doctorOptions, deps doctor.ProbeDeps) doctorResponse {
	deps = doctor.FillProbeDeps(deps)
	resp := doctorResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(doctorResultKind),
		OK:                 true,
		Scenery:            buildVersionResponse(),
	}

	runtimeInfo := deps.ResourceProbe.Runtime()
	resp.Environment.GOOS = runtimeInfo.GOOS
	resp.Environment.GOARCH = runtimeInfo.GOARCH
	resp.Environment.NumCPU = runtimeInfo.NumCPU

	resp.Checks = append(resp.Checks, doctor.RuntimeCheck(runtimeInfo))
	resp.Checks = append(resp.Checks, doctor.CPUCheck(runtimeInfo.NumCPU))
	if memory, err := deps.ResourceProbe.Memory(ctx); err != nil {
		resp.Checks = append(resp.Checks, doctor.Check{
			ID:              "resource.memory",
			Category:        "resource",
			Name:            "System memory",
			Status:          doctor.StatusSkipped,
			Severity:        doctor.SeverityInformational,
			Message:         "total physical memory could not be determined: " + err.Error(),
			SuggestedAction: "Continue if other checks are healthy, or verify machine memory manually.",
		})
	} else {
		resp.Environment.TotalMemoryBytes = memory.TotalBytes
		resp.Checks = append(resp.Checks, doctor.MemoryCheck(memory))
	}

	var cfg appcfg.Config
	var appFound bool
	appStart := opts.AppRoot
	if strings.TrimSpace(appStart) == "" {
		if cwd, err := deps.Getwd(); err == nil {
			appStart = cwd
		} else {
			resp.Checks = append(resp.Checks, doctor.Check{
				ID:              "path.cwd",
				Category:        "path",
				Name:            "Current directory",
				Status:          doctor.StatusWarn,
				Severity:        doctor.SeverityOptional,
				Message:         "current directory could not be resolved: " + err.Error(),
				SuggestedAction: "Run `scenery doctor` from a readable directory.",
			})
		}
	}
	if strings.TrimSpace(appStart) != "" {
		app, discoveredCfg, ok, err := deps.DiscoverApp(appStart)
		if err != nil {
			if strings.TrimSpace(opts.AppRoot) != "" {
				resp.Checks = append(resp.Checks, doctor.Check{
					ID:              "app.root",
					Category:        "app",
					Name:            "App root",
					Status:          doctor.StatusError,
					Severity:        doctor.SeverityRequired,
					Message:         "app root could not be discovered from " + appStart + ": " + err.Error(),
					SuggestedAction: "Pass a directory inside an app that contains `.scenery.json`.",
				})
			}
		} else if ok {
			resp.App = &app
			cfg = discoveredCfg
			appFound = true
			resp.Checks = append(resp.Checks, doctor.Check{
				ID:       "app.root",
				Category: "app",
				Name:     "App root",
				Status:   doctor.StatusOK,
				Severity: doctor.SeverityInformational,
				Message:  fmt.Sprintf("%s at %s", app.Name, app.Root),
				Observed: map[string]any{
					"root":        app.Root,
					"config_path": app.ConfigPath,
					"name":        app.Name,
					"id":          app.ID,
				},
			})
		}
	}

	diskPaths := doctor.DiskPaths(opts.AppRoot, resp.App, deps)
	for _, path := range diskPaths {
		resp.Checks = append(resp.Checks, doctor.DiskCheck(ctx, deps.ResourceProbe, path, &resp.Environment)...)
	}
	resp.Checks = append(resp.Checks, doctor.StorageSizeChecks(ctx, deps)...)

	features := doctor.Features(cfg, resp.App)
	resp.Checks = append(resp.Checks, doctor.DependencyChecks(ctx, deps, features, appFound)...)
	resp.Checks = append(resp.Checks, doctor.DockerChecks(ctx, deps)...)
	resp.Checks = append(resp.Checks, doctor.PostgresServerCheck(ctx, deps, features))
	if resp.App != nil {
		resp.Checks = append(resp.Checks, doctor.EditorWorkspaceCheck(resp.App.Root))
		resp.Checks = append(resp.Checks, doctorContractFilenameChecks(resp.App.Root)...)
		resp.Checks = append(resp.Checks, doctorReactChecks(resp.App.Root)...)
	}
	resp.Checks = append(resp.Checks, doctorProcessOwnershipCheck(ctx, deps))
	if deployInfo, deployChecks := doctorDeployDiagnostics(ctx, deps); deployInfo != nil {
		resp.Deploy = deployInfo
		resp.Checks = append(resp.Checks, deployChecks...)
	}

	resp.Summary = doctor.Summarize(resp.Checks)
	resp.OK = resp.Summary.Errors == 0
	return resp
}

func doctorReactChecks(root string) []doctor.Check {
	if _, err := os.Stat(filepath.Join(root, scn.AppFilename)); err != nil {
		return nil
	}
	result, err := compiler.Compile(root)
	if err != nil || result.Manifest == nil {
		return nil
	}
	var checks []doctor.Check
	hasReact := false
	for _, target := range result.Manifest.Resources {
		react, ok := target.Spec["react"].(map[string]any)
		if target.Kind != "scenery.typescript-client" || !ok {
			continue
		}
		hasReact = true
		tsconfig := strings.TrimSpace(fmt.Sprint(react["tsconfig"]))
		configPath := filepath.Join(root, filepath.FromSlash(tsconfig))
		check := doctor.Check{ID: "typescript.react.dependencies." + target.Name, Category: "dependency", Name: "React generation dependencies (" + target.Name + ")", Status: doctor.StatusOK, Severity: doctor.SeverityRequired, Message: "declared tsconfig and node_modules are available", Observed: map[string]any{"tsconfig": tsconfig}}
		if info, statErr := os.Stat(configPath); statErr != nil || !info.Mode().IsRegular() {
			check.Status = doctor.StatusError
			check.Message = "declared React generation tsconfig is unavailable"
			check.SuggestedAction = "Create " + tsconfig + " or update the typescript_client react block."
		} else if modules := tscheck.NodeModulesPath(filepath.Dir(configPath), root); modules == "" {
			check.Status = doctor.StatusError
			check.Message = "node_modules is unavailable for React generation"
			check.SuggestedAction = "Install the application's frontend dependencies before generating the React client."
		} else {
			check.Observed["node_modules"] = modules
		}
		checks = append(checks, check)
	}
	if !hasReact {
		return nil
	}
	checker := doctor.Check{ID: "typescript.react.checker", Category: "dependency", Name: "Native TypeScript checker", Status: doctor.StatusWarn, Severity: doctor.SeverityOptional, Message: "managed tsgo will be installed on first React generation", SuggestedAction: "Run `scenery system toolchain sync --tool tsgo -o json` to install it now."}
	if manifest, loadErr := toolchain.LoadBundledManifest(); loadErr == nil {
		if store, storeErr := toolchain.NewStore(toolchain.DefaultStoreDir(root), manifest); storeErr == nil {
			store.ManifestSHA256 = toolchain.BundledManifestSHA256()
			if status, pathErr := store.Path(context.Background(), "tsgo", toolchain.CurrentPlatform()); pathErr == nil && status.Status == "installed" {
				checker.Status = doctor.StatusOK
				checker.Severity = doctor.SeverityInformational
				checker.Message = "checksummed native tsgo is installed"
				checker.SuggestedAction = ""
				checker.Observed = map[string]any{"path": status.ManagedPath, "version": status.Version}
			}
		}
	}
	return append(checks, checker)
}

func doctorContractFilenameChecks(root string) []doctor.Check {
	result, err := compiler.Compile(root)
	if err != nil || result == nil {
		return nil
	}
	var checks []doctor.Check
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code != "SCN1021" {
			continue
		}
		check := doctor.Check{
			ID:              "source.contract_filename",
			Category:        "source",
			Name:            "Contract filenames",
			Status:          doctor.StatusError,
			Severity:        doctor.SeverityRequired,
			Message:         diagnostic.Message,
			SuggestedAction: "Rename the contract file to its current role-named filename.",
			Observed:        map[string]any{"path": diagnostic.Path, "details": diagnostic.Details},
		}
		if len(diagnostic.Suggestions) > 0 {
			check.SuggestedAction = diagnostic.Suggestions[0]
		}
		checks = append(checks, check)
	}
	return checks
}

// doctorProcessOwnershipCheck stays in cmd/scenery because it depends on
// CLI-owned edge process matching helpers (parseRuntimeProcesses,
// edgeAgentCommandMatches, managedCaddyCommandMatches, portFromAddr,
// defaultEdgeTargetAddr) that internal/doctor must not import.
func doctorProcessOwnershipCheck(ctx context.Context, deps doctor.ProbeDeps) doctor.Check {
	check := doctor.Check{
		ID:       "runtime.process_ownership",
		Category: "runtime",
		Name:     "Scenery process ownership",
		Status:   doctor.StatusOK,
		Severity: doctor.SeverityInformational,
		Message:  "no duplicate Scenery agent or edge owners detected",
	}
	runtimeInfo := deps.ResourceProbe.Runtime()
	if runtimeInfo.GOOS != "darwin" && runtimeInfo.GOOS != "linux" {
		check.Status = doctor.StatusSkipped
		check.Message = "process ownership inspection is unavailable on " + runtimeInfo.GOOS
		return check
	}
	out, err := deps.RunCommand(ctx, "ps", "-axo", "pid=,uid=,command=")
	if err != nil {
		check.Status = doctor.StatusSkipped
		check.Message = "process ownership inspection failed: " + err.Error()
		return check
	}
	home, _ := deps.AgentHome()
	paths := localagent.PathsForHome(home)
	routerAddr := localagent.RouterAddrFromEnv()
	configs := []string{paths.EdgeConfigPath}
	if userHome, err := os.UserHomeDir(); err == nil {
		configs = append(configs, filepath.Join(userHome, ".onlava", "agent", "edge", "Caddyfile"))
	}
	var agents, caddies []int
	for _, process := range parseRuntimeProcesses(string(out)) {
		if process.UID != os.Getuid() {
			continue
		}
		if edgeAgentCommandMatches(process.Command, paths.SocketPath, routerAddr) {
			agents = append(agents, process.PID)
		}
		if managedCaddyCommandMatches(process.Command, configs) {
			caddies = append(caddies, process.PID)
		}
	}
	routerListeners := doctorListenerPIDs(ctx, deps, "TCP", portFromAddr(routerAddr), true)
	edgeTCP := doctorListenerPIDs(ctx, deps, "TCP", portFromAddr(defaultEdgeTargetAddr), true)
	edgeUDP := doctorListenerPIDs(ctx, deps, "UDP", portFromAddr(defaultEdgeTargetAddr), false)
	unknownRouter := pidsWithout(routerListeners, agents)
	unknownEdge := pidsWithout(append(edgeTCP, edgeUDP...), caddies)
	check.Observed = map[string]any{
		"agent_pids": agents, "edge_pids": caddies,
		"router_listener_pids": routerListeners, "edge_listener_pids": uniqueInts(append(edgeTCP, edgeUDP...)),
	}
	if len(agents) > 1 || len(caddies) > 1 || len(unknownRouter) > 0 || len(unknownEdge) > 0 {
		check.Status = doctor.StatusWarn
		check.Severity = doctor.SeverityOptional
		check.Message = "duplicate or unowned processes are using Scenery runtime ports"
		check.SuggestedAction = "Stop orphaned `scenery system agent` or Caddy processes, then run `scenery system agent restart` and `scenery system edge restart`."
		check.Observed["unknown_router_pids"] = unknownRouter
		check.Observed["unknown_edge_pids"] = unknownEdge
	}
	return check
}

func doctorListenerPIDs(ctx context.Context, deps doctor.ProbeDeps, network, port string, listen bool) []int {
	if port == "" {
		return nil
	}
	args := []string{"-nP", "-t", "-i" + network + ":" + port}
	if listen {
		args = append(args, "-sTCP:LISTEN")
	}
	out, err := deps.RunCommand(ctx, "lsof", args...)
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return uniqueInts(pids)
}

func pidsWithout(values, allowed []int) []int {
	set := map[int]bool{}
	for _, pid := range allowed {
		set[pid] = true
	}
	var out []int
	for _, pid := range uniqueInts(values) {
		if !set[pid] {
			out = append(out, pid)
		}
	}
	return out
}

func uniqueInts(values []int) []int {
	set := map[int]bool{}
	var out []int
	for _, value := range values {
		if value > 0 && !set[value] {
			set[value] = true
			out = append(out, value)
		}
	}
	sort.Ints(out)
	return out
}

// doctorDeployDiagnostics stays in cmd/scenery because it composes the
// CLI-owned deploy status response (buildDeployStatusWithContext,
// deployTargetStatus) and CLI payload identities.
func doctorDeployDiagnostics(ctx context.Context, deps doctor.ProbeDeps) (*doctorDeployInfo, []doctor.Check) {
	home, err := deps.AgentHome()
	if err != nil {
		message := "deploy registry path could not be resolved: " + err.Error()
		info := &doctorDeployInfo{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.doctor.deploy"),
			RegistryPath:       "",
			Diagnostics: deploydiag.Report{Checks: []deploydiag.Check{{
				ID:      "deploy.registry",
				Status:  doctor.StatusSkipped,
				Message: message,
			}}},
		}
		checks := []doctor.Check{{
			ID:              "deploy.registry",
			Category:        "deploy",
			Name:            "Deploy registry",
			Status:          doctor.StatusSkipped,
			Severity:        doctor.SeverityInformational,
			Message:         message,
			SuggestedAction: "Run `scenery deploy status -o json` for deploy-specific diagnostics.",
		}}
		return info, checks
	}
	paths := localagent.PathsForHome(home)
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		info := &doctorDeployInfo{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.doctor.deploy"),
			RegistryPath:       paths.DeployPath,
			Diagnostics: deploydiag.Report{Checks: []deploydiag.Check{{
				ID:      "deploy.registry",
				Status:  doctor.StatusWarn,
				Message: "deploy registry could not be read: " + err.Error(),
			}}},
		}
		return info, []doctor.Check{{
			ID:              "deploy.registry",
			Category:        "deploy",
			Name:            "Deploy registry",
			Status:          doctor.StatusWarn,
			Severity:        doctor.SeverityOptional,
			Message:         "deploy registry could not be read: " + err.Error(),
			SuggestedAction: "Inspect or remove the registry at " + paths.DeployPath + ".",
		}}
	}
	if len(registry.Targets) == 0 {
		if _, err := os.Stat(paths.DeployPath); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
	}
	status := buildDeployStatusWithContext(ctx, paths, registry)
	diagnostics := deploydiag.Report{}
	if status.DiagnosticsDetail != nil {
		diagnostics = *status.DiagnosticsDetail
	}
	info := &doctorDeployInfo{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.doctor.deploy"),
		Ready:              status.Ready,
		RegistryPath:       status.RegistryPath,
		Targets:            status.Targets,
		Diagnostics:        diagnostics,
	}
	checks := make([]doctor.Check, 0, len(diagnostics.Checks))
	for _, check := range diagnostics.Checks {
		checks = append(checks, deployDiagnosticDoctorCheck(check))
	}
	return info, checks
}

func deployDiagnosticDoctorCheck(check deploydiag.Check) doctor.Check {
	status := check.Status
	switch status {
	case doctor.StatusOK, doctor.StatusWarn, doctor.StatusError, doctor.StatusSkipped:
	default:
		status = doctor.StatusWarn
	}
	return doctor.Check{
		ID:              check.ID,
		Category:        "deploy",
		Name:            deployDiagnosticName(check.ID),
		Status:          status,
		Severity:        doctor.SeverityOptional,
		Message:         check.Message,
		SuggestedAction: check.SuggestedAction,
		Observed:        check.Observed,
	}
}

func deployDiagnosticName(id string) string {
	id = strings.TrimPrefix(id, "deploy.")
	id = strings.ReplaceAll(id, "_", " ")
	id = strings.ReplaceAll(id, ".", " ")
	if id == "" {
		return "Deploy"
	}
	return "Deploy " + id
}

func writeDoctorJSON(w io.Writer, resp doctorResponse) error {
	return writeCLIJSON(w, resp)
}

func writeDoctorText(w io.Writer, resp doctorResponse) error {
	if _, err := fmt.Fprintln(w, "scenery doctor"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, check := range resp.Checks {
		status := check.Status
		if status == doctor.StatusWarn {
			status = "warn"
		}
		if _, err := fmt.Fprintf(w, "%-7s %-28s %s\n", status, check.ID, check.Message); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nsummary: %d ok, %d warnings, %d errors, %d skipped\n", resp.Summary.OK, resp.Summary.Warnings, resp.Summary.Errors, resp.Summary.Skipped)
	return err
}
