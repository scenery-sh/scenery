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
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/doctor"
	"scenery.sh/internal/envpolicy"
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
	Checks      []doctor.Check     `json:"checks"`
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
			return &silentCLIError{err: fmt.Errorf("scenery doctor found %d preflight error(s)", resp.Summary.Errors), code: 3}
		}
		return nil
	}
	if err := writeDoctorText(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return &codedCLIError{err: fmt.Errorf("scenery doctor found %d preflight error(s)", resp.Summary.Errors), code: 3}
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
	if deps.FirstExecution != nil {
		if cacheRoot, err := deps.CacheRoot(); err != nil {
			resp.Checks = append(resp.Checks, doctor.FirstExecutionCheck(doctor.FirstExecutionInfo{}, err))
		} else {
			resp.Checks = append(resp.Checks, doctor.FirstExecutionCheck(deps.FirstExecution(ctx, cacheRoot)))
		}
	}
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
			if strings.TrimSpace(opts.AppRoot) != "" || !errors.Is(err, appcfg.ErrRootNotFound) {
				resp.Checks = append(resp.Checks, doctor.Check{
					ID:              "app.root",
					Category:        "app",
					Name:            "App root",
					Status:          doctor.StatusError,
					Severity:        doctor.SeverityRequired,
					Message:         "app root could not be discovered from " + appStart + ": " + err.Error(),
					SuggestedAction: "Resolve the app configuration error, or pass a directory inside an app that contains a valid `.scenery.json`.",
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

	managedSQL := false
	if resp.App != nil {
		var sqlErr error
		requirements, err := compileSQLRequirements(resp.App.Root)
		sqlErr = err
		if sqlErr == nil {
			env := envpolicy.Environ()
			bindings, supplyErr := resolveSQLSupply(requirements, env, true)
			sqlErr = supplyErr
			managedSQL = supplyErr == nil && len(bindings) > 0 && lookupEnvValue(env, appDatabaseURLEnv) == ""
		}
		if sqlErr != nil {
			resp.Checks = append(resp.Checks, doctor.Check{ID: "app.sql_requirements", Category: "database", Name: "SQL requirements", Status: doctor.StatusError, Severity: doctor.SeverityRequired,
				Message: sqlErr.Error(), SuggestedAction: "Resolve current source and selected-environment SQL supply before startup; no database was opened or provisioned."})
		}
	}
	features := doctor.Features(cfg, resp.App, managedSQL)
	resp.Checks = append(resp.Checks, doctor.DependencyChecks(ctx, deps, features, appFound)...)
	resp.Checks = append(resp.Checks, doctor.DockerChecks(ctx, deps)...)
	resp.Checks = append(resp.Checks, doctor.PostgresServerCheck(ctx, deps, features))
	if resp.App != nil {
		resp.Checks = append(resp.Checks, doctorReactChecks(resp.App.Root)...)
		resp.Checks = append(resp.Checks, doctorAssistantChecks(ctx, resp.App.Root, cfg, runtimeInfo)...)
		resp.Checks = append(resp.Checks, doctorBuildBlockCheck(resp.App.Root))
	}
	resp.Checks = append(resp.Checks, doctorProcessOwnershipCheck(ctx, deps))
	resp.Checks = append(resp.Checks, doctorAgentStartCheck(deps))
	resp.Checks = append(resp.Checks, doctorAgentSupervisorCheck(deps))

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
	checker := doctor.Check{ID: "typescript.react.checker", Category: "dependency", Name: "Native TypeScript checker", Status: doctor.StatusWarn, Severity: doctor.SeverityOptional, Message: "managed TypeScript 7 tsc will be installed on first React generation", SuggestedAction: "Run `scenery system toolchain sync --tool tsc -o json` to install it now."}
	if manifest, loadErr := toolchain.LoadBundledManifest(); loadErr == nil {
		if store, storeErr := toolchain.NewStore(toolchain.DefaultStoreDir(root), manifest); storeErr == nil {
			store.ManifestSHA256 = toolchain.BundledManifestSHA256()
			if status, pathErr := store.Path(context.Background(), "tsc", toolchain.CurrentPlatform()); pathErr == nil && status.Status == "installed" {
				checker.Status = doctor.StatusOK
				checker.Severity = doctor.SeverityInformational
				checker.Message = "checksummed TypeScript 7 tsc is installed"
				checker.SuggestedAction = ""
				checker.Observed = map[string]any{"path": status.ManagedPath, "version": status.Version}
			}
		}
	}
	return append(checks, checker)
}

// doctorAgentStartCheck reports the local agent's start incident: repeated
// failed starts of the same executable for the same cause, which a supervised
// agent contains instead of restarting forever.
func doctorAgentStartCheck(deps doctor.ProbeDeps) doctor.Check {
	check := doctor.Check{
		ID:       "runtime.agent_start",
		Category: "runtime",
		Name:     "Local agent start",
		Status:   doctor.StatusOK,
		Severity: doctor.SeverityInformational,
		Message:  "the local agent's last start succeeded or none failed",
	}
	home, err := deps.AgentHome()
	if err != nil {
		check.Status = doctor.StatusSkipped
		check.Message = "agent home is unavailable: " + err.Error()
		return check
	}
	incident, err := localagent.LoadStartIncident(localagent.PathsForHome(home))
	if errors.Is(err, os.ErrNotExist) {
		return check
	}
	if err != nil {
		check.Status = doctor.StatusWarn
		check.Severity = doctor.SeverityOptional
		check.Message = "the local agent's start incident is unreadable: " + err.Error()
		return check
	}
	check.Observed = map[string]any{
		"state": incident.State, "class": incident.Class, "cause": incident.Cause, "attempts": incident.Attempts,
		"first_at": incident.FirstAt.Format(time.RFC3339), "last_at": incident.LastAt.Format(time.RFC3339), "executable": incident.Executable,
	}
	if incident.State == "blocked" {
		check.Status = doctor.StatusError
		check.Severity = doctor.SeverityRequired
		check.Message = fmt.Sprintf("local agent BLOCKED after %d failed start(s) since %s (%s): %s", incident.Attempts, incident.FirstAt.Format(time.RFC3339), incident.Class, incident.Cause)
		check.SuggestedAction = "Fix the cause without deleting retained agent state, then run `scenery system agent restart`."
		return check
	}
	check.Status = doctor.StatusWarn
	check.Severity = doctor.SeverityOptional
	check.Message = fmt.Sprintf("local agent start failed %d time(s) since %s (%s), retrying: %s", incident.Attempts, incident.FirstAt.Format(time.RFC3339), incident.Class, incident.Cause)
	check.SuggestedAction = "Wait for the next start, or fix the cause and run `scenery system agent restart`."
	return check
}

// doctorAgentSupervisorJobFunc reads the installed supervisor job of an
// agent socket; tests replace it to keep the host's jobs out of reach.
var doctorAgentSupervisorJobFunc = localagent.InstalledSupervisorJob

// doctorAgentSupervisorCheck tells a supervised agent with start failure
// containment from one whose installed job predates it: launchd or systemd
// would restart the latter's failing start forever.
func doctorAgentSupervisorCheck(deps doctor.ProbeDeps) doctor.Check {
	check := doctor.Check{
		ID:       "runtime.agent_supervisor",
		Category: "runtime",
		Name:     "Local agent supervisor",
		Status:   doctor.StatusOK,
		Severity: doctor.SeverityInformational,
		Message:  "no launchd job or systemd unit supervises the local agent",
	}
	home, err := deps.AgentHome()
	if err != nil {
		check.Status = doctor.StatusSkipped
		check.Message = "agent home is unavailable: " + err.Error()
		return check
	}
	job, ok := doctorAgentSupervisorJobFunc(localagent.PathsForHome(home).SocketPath)
	if !ok {
		return check
	}
	check.Observed = map[string]any{"label": job.Label, "path": job.Path, "failure_containment": job.FailureContainment}
	if job.FailureContainment {
		check.Message = fmt.Sprintf("%s supervises the local agent with start failure containment", job.Label)
		return check
	}
	check.Status = doctor.StatusWarn
	check.Severity = doctor.SeverityOptional
	check.Message = fmt.Sprintf("%s supervises the local agent without start failure containment: a start that keeps failing would be restarted forever", job.Label)
	check.SuggestedAction = "Run `scenery system agent restart`, which updates the installed job."
	return check
}

// doctorProcessOwnershipCheck stays in cmd/scenery because it depends on
// CLI-owned edge process matching helpers (parseRuntimeProcesses,
// edgeAgentCommandMatches, managedCaddyCommandMatches, portFromAddr,
// defaultEdgeTargetAddr) that internal/doctor must not import.
// doctorBuildBlockCheck reports running sessions of the app whose builds are
// blocked by a cause no ordinary edit resolves.
func doctorBuildBlockCheck(root string) doctor.Check {
	check := doctor.Check{
		ID:       "runtime.build_block",
		Category: "runtime",
		Name:     "Development builds",
		Status:   doctor.StatusOK,
		Severity: doctor.SeverityInformational,
		Message:  "no running session of this app has blocked builds",
	}
	stateRoots, err := filepath.Glob(filepath.Join(root, ".scenery", "sessions", "*"))
	if err != nil {
		check.Status = doctor.StatusSkipped
		check.Message = "session state is unavailable: " + err.Error()
		return check
	}
	var blocks []sessionBuildBlock
	for _, stateRoot := range stateRoots {
		if block, ok := liveSessionBuildBlock(stateRoot); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return check
	}
	block := blocks[0]
	check.Status = doctor.StatusError
	check.Severity = doctor.SeverityRequired
	check.Observed = map[string]any{"session_id": block.SessionID, "reason": block.Reason, "cause": block.Cause, "since": block.Since.Format(time.RFC3339), "prevented_builds": block.PreventedBuilds, "blocked_sessions": len(blocks)}
	check.Message = fmt.Sprintf("session %s builds BLOCKED (%s) since %s, %d build(s) prevented; the runtime keeps serving its last good generation: %s", block.SessionID, block.Reason, block.Since.Format(time.RFC3339), block.PreventedBuilds, block.Cause)
	switch block.Reason {
	case buildBlockFrameworkMismatch:
		check.SuggestedAction = "Select framework source this Scenery build matches (`scenery framework use`), or restart `scenery up` with the executable it reports; edits resume building once go.mod changes."
	case buildBlockMigrationPending:
		check.SuggestedAction = "Run `scenery down`, `scenery db migrate`, then `scenery up`; a changed migration input also retries the build."
	}
	return check
}

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
	var agents, caddies []int
	for _, process := range parseRuntimeProcesses(string(out)) {
		if process.UID != os.Getuid() {
			continue
		}
		if edgeAgentCommandMatches(process.Command, routerAddr) {
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
	_, err := fmt.Fprintf(w, "\npreflight summary: %d ok, %d warnings, %d errors, %d skipped\nThis is not application runtime readiness; skipped checks and unprobed services remain unverified.\n", resp.Summary.OK, resp.Summary.Warnings, resp.Summary.Errors, resp.Summary.Skipped)
	return err
}
