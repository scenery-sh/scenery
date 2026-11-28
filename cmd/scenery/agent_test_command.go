package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/modfile"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/pythonbuild"
)

type moduleTestResult struct {
	agentModule
	Command    []string                   `json:"command"`
	OK         bool                       `json:"ok"`
	DurationMS int64                      `json:"duration_ms"`
	Output     string                     `json:"output,omitempty"`
	Database   *testModuleDatabaseSummary `json:"database,omitempty"`
}

func runAgentTests(ctx context.Context, out io.Writer, args []string) error {
	flags := newCLIFlagSet("test")
	var root string
	var jsonOutput, withHost, branchDB bool
	flags.StringVar(&root, "app-root", "", "")
	flags.BoolVar(&withHost, "with-host", false, "")
	flags.BoolVar(&branchDB, "branch-db", false, "")
	registerJSONOutput(flags, &jsonOutput)
	targets, err := parseCLIFlags(flags, args)
	if err != nil {
		return usageErrorf("%v", err)
	}
	root, err = findContractRoot(root)
	if err != nil {
		return err
	}
	if canonical, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = canonical
	}
	var g agentGraph
	if withHost {
		g, err = loadAgentGraph(ctx, root)
	} else {
		g, err = loadAgentGraphDeclarations(ctx, root)
	}
	if err != nil {
		return err
	}
	modules, err := g.modules()
	if err != nil {
		return err
	}
	modules, err = selectTestModules(modules, targets)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return fmt.Errorf("failed_precondition: no application modules to test")
	}
	started := time.Now()
	// Publish all module contracts in the existing atomic transaction. Tests
	// can import generated clients belonging to a different module.
	_, generationErr := generate.GenerateSnapshotWithContext(ctx, g.Result, "contracts", false)
	baseEnv := envpolicy.Environ()
	var cfg appcfg.Config
	if _, discoveredCfg, cfgErr := appcfg.DiscoverRoot(root); cfgErr == nil {
		cfg = discoveredCfg
	} else if g.Manifest != nil {
		cfg.Name = g.Manifest.Application.Name
	}

	var dbHarness *testDatabaseHarness
	var hostBuilder *testHostBuilder
	var lastHostSummary *testHostSummary
	if generationErr == nil && (branchDB || (withHost && len(g.SQLRequirements) > 0)) {
		dbHarness, err = startTestDatabaseHarness(ctx, root, cfg, g.Result, baseEnv)
		if err != nil {
			return err
		}
		defer func() { _ = dbHarness.Close() }()
	}
	if generationErr == nil && withHost {
		hostBuilder, err = prepareTestHostBuilder(ctx, root, cfg, g.Result)
		if err != nil {
			return err
		}
	}

	var sharedHostStop func()
	var sharedHostEnv []string
	if hostBuilder != nil && dbHarness == nil {
		var startErr error
		lastHostSummary, sharedHostEnv, sharedHostStop, startErr = hostBuilder.startInstance(ctx, baseEnv)
		if startErr != nil {
			return startErr
		}
		defer sharedHostStop()
	}

	results := make([]moduleTestResult, len(modules))
	batched := make([]bool, len(modules))
	if generationErr == nil && dbHarness == nil && hostBuilder == nil {
		runBatchedGoModuleTests(ctx, g, modules, baseEnv, results, batched)
	}
	var remaining []int
	for i := range modules {
		if !batched[i] {
			remaining = append(remaining, i)
		}
	}
	var hostSummaryMu sync.Mutex
	runOne := func(i int) {
		module := modules[i]
		dir, command, err := g.testCommand(module)
		record := moduleTestResult{agentModule: module, Command: command}
		moduleStarted := time.Now()
		switch {
		case generationErr != nil:
			record.Output = boundedOutput("generate contracts: " + generationErr.Error())
		case err != nil:
			record.Output = boundedOutput(err.Error())
		default:
			modEnv := slicesCloneEnv(baseEnv)
			var cleanupDB func()
			if dbHarness != nil {
				dbSummary, dbEnv, dropClone, branchErr := dbHarness.branchForModule(ctx, module.Name)
				if branchErr != nil {
					record.Output = boundedOutput("branch test database: " + branchErr.Error())
					break
				}
				cleanupDB = dropClone
				record.Database = dbSummary
				modEnv = mergeEnvEntries(modEnv, dbEnv)
			}
			var stopModHost func()
			if hostBuilder != nil {
				if dbHarness != nil {
					hostSummary, hostEnv, stopFn, hostErr := hostBuilder.startInstance(ctx, modEnv)
					if hostErr != nil {
						if cleanupDB != nil {
							cleanupDB()
						}
						record.Output = boundedOutput("start test host: " + hostErr.Error())
						break
					}
					hostSummaryMu.Lock()
					lastHostSummary = hostSummary
					hostSummaryMu.Unlock()
					modEnv = hostEnv
					stopModHost = stopFn
				} else {
					modEnv = mergeEnvEntries(modEnv, sharedHostEnv)
				}
			}
			run := exec.CommandContext(ctx, command[0], command[1:]...)
			run.Dir, run.Env = dir, modEnv
			var output tailOutput
			run.Stdout, run.Stderr = &output, &output
			err := run.Run()
			if stopModHost != nil {
				stopModHost()
			}
			if cleanupDB != nil {
				cleanupDB()
			}
			record.OK = err == nil
			if err != nil {
				record.Output = strings.TrimSpace(err.Error() + "\n" + output.String())
			}
		}
		record.DurationMS = time.Since(moduleStarted).Milliseconds()
		results[i] = record
	}
	if len(remaining) == 1 {
		runOne(remaining[0])
	} else if len(remaining) > 1 {
		limit := min(8, max(2, runtime.GOMAXPROCS(0)/2), len(remaining))
		if hostBuilder != nil && dbHarness != nil {
			limit = min(4, len(remaining))
		}
		var next atomic.Int64
		var wg sync.WaitGroup
		for range limit {
			wg.Go(func() {
				for {
					pos := int(next.Add(1)) - 1
					if pos >= len(remaining) {
						return
					}
					runOne(remaining[pos])
				}
			})
		}
		wg.Wait()
	}
	success := true
	for _, record := range results {
		success = success && record.OK
	}
	if jsonOutput {
		data := map[string]any{
			"ok": success, "app_root": root, "modules": results, "duration_ms": time.Since(started).Milliseconds(),
		}
		if dbHarness != nil && dbHarness.summary != nil {
			data["database_template"] = dbHarness.summary
		}
		if lastHostSummary != nil {
			data["test_host"] = lastHostSummary
		}
		payload := withCLIPayloadIdentity("scenery.test.result", data)
		if err := writeCLIJSON(out, payload); err != nil {
			return err
		}
	} else {
		for _, result := range results {
			state := "passed"
			if !result.OK {
				state = "failed"
			}
			extra := ""
			if result.Database != nil {
				extra = fmt.Sprintf(" [db %s cloned in %dms]", result.Database.Name, result.Database.CloneMS)
			}
			_, _ = fmt.Fprintf(out, "%s (%s): %s in %dms%s\n", result.Name, result.Runtime, state, result.DurationMS, extra)
			if result.Output != "" {
				_, _ = fmt.Fprintln(out, result.Output)
			}
		}
	}
	if !success {
		return &silentCLIError{err: fmt.Errorf("module tests failed"), code: 3}
	}
	return nil
}

func selectTestModules(modules []agentModule, targets []string) ([]agentModule, error) {
	if len(targets) == 0 {
		return modules, nil
	}
	selected := make(map[int]bool, len(targets))
	for _, target := range targets {
		match := -1
		for i, module := range modules {
			if !matchesModuleSelector(module, target) {
				continue
			}
			if match >= 0 {
				return nil, usageErrorf("ambiguous test module %q", target)
			}
			match = i
		}
		if match < 0 {
			return nil, usageErrorf("unknown test module %q", target)
		}
		selected[match] = true
	}
	result := make([]agentModule, 0, len(selected))
	for i, module := range modules {
		if selected[i] {
			result = append(result, module)
		}
	}
	return result, nil
}

func (g agentGraph) testCommand(module agentModule) (string, []string, error) {
	dir := filepath.Join(g.Root, module.Directory)
	switch module.Runtime {
	case "go":
		for parent := dir; pathUnder(g.Root, parent); parent = filepath.Dir(parent) {
			if _, err := os.Stat(filepath.Join(parent, "go.mod")); err == nil {
				relative, err := filepath.Rel(parent, dir)
				if err != nil {
					return "", []string{}, err
				}
				pattern := "./..."
				if relative != "." {
					pattern = "./" + filepath.ToSlash(relative) + "/..."
				}
				return parent, applyLinkedFrameworkGoCommand(g.Root, parent, []string{"go", "test", pattern}), nil
			}
			if parent == g.Root {
				break
			}
		}
		return dir, applyLinkedFrameworkGoCommand(g.Root, dir, []string{"go", "test", "./..."}), nil
	case "rust":
		for _, target := range g.Manifest.Resources {
			if target.Kind != "scenery.rust-target" || target.Spec["role"] != "development" {
				continue
			}
			workspace := g.ref(target, "workspace", "rust_workspace")
			if !pathUnder(inspectString(workspace.Spec["root"]), module.Directory) {
				continue
			}
			toolchain := g.ref(target, "toolchain", "rust_toolchain")
			version := inspectString(toolchain.Spec["version"])
			return dir, []string{"cargo", "+" + version, "test", "--locked"}, nil
		}
		return dir, []string{"cargo", "test"}, nil
	case "js", "ts":
		return dir, []string{"bun", "test", "--no-env-file"}, nil
	case "python":
		for _, resource := range g.Manifest.Resources {
			if resource.Kind != "scenery.service" || resource.Module != module.Name || !compiler.IsPythonRuntime(inspectString(resource.Spec["runtime"])) {
				continue
			}
			target, err := compiler.SelectPythonWorkerTarget(g.Result, resource, "development")
			if err != nil {
				return "", []string{}, err
			}
			interpreter, err := pythonbuild.ResolveInterpreter(context.Background(), g.Root, pythonbuild.Service{PythonWorkerTarget: target, Name: resource.Address})
			if err != nil {
				return "", []string{}, err
			}
			pytest := filepath.Join(g.Root, target.Workspace, ".venv", "bin", "pytest")
			if info, statErr := os.Lstat(pytest); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				return dir, []string{pytest}, nil
			}
			return dir, []string{interpreter, "-B", "-m", "unittest", "discover", "-s", ".", "-p", "test_*.py"}, nil
		}
		return "", []string{}, fmt.Errorf("missing Python service declaration for module %s", module.Name)
	default:
		return "", []string{}, fmt.Errorf("unsupported test runtime %q", module.Runtime)
	}
}

const agentOutputLimit = 12 << 10

func boundedOutput(value string) string {
	if len(value) > agentOutputLimit {
		return strings.ToValidUTF8(value[:agentOutputLimit], "�") + "\n[output truncated]"
	}
	return strings.ToValidUTF8(value, "�")
}

// tailOutput bounds retained native test output while preserving the failure summary.
type tailOutput []byte

func (tail *tailOutput) Write(data []byte) (int, error) {
	size := len(data)
	data = data[max(0, size-agentOutputLimit):]
	if excess := len(*tail) + len(data) - agentOutputLimit; excess > 0 {
		copy(*tail, (*tail)[excess:])
		*tail = (*tail)[:len(*tail)-excess]
	}
	*tail = append(*tail, data...)
	return size, nil
}

func (tail tailOutput) String() string { return strings.ToValidUTF8(string(tail), "�") }

type goTestJSONEvent struct {
	Action      string  `json:"Action"`
	Package     string  `json:"Package"`
	ImportPath  string  `json:"ImportPath"`
	Output      string  `json:"Output"`
	Elapsed     float64 `json:"Elapsed"`
	FailedBuild string  `json:"FailedBuild"`
}

// runBatchedGoModuleTests groups non-overlapping Go modules that share a go.mod
// directory into a single `go test -json` invocation so Go compiles and runs
// packages in parallel without spawning one `go` process per module.
func runBatchedGoModuleTests(ctx context.Context, g agentGraph, modules []agentModule, baseEnv []string, results []moduleTestResult, batched []bool) {
	type member struct {
		index     int
		module    agentModule
		command   []string
		pattern   string
		pkgPrefix string
		relDir    string
	}
	groups := map[string][]member{}
	var groupOrder []string
	modulePaths := map[string]string{}

	for i, module := range modules {
		if module.Runtime != "go" {
			continue
		}
		dir, command, err := g.testCommand(module)
		if err != nil || len(command) < 3 {
			continue
		}
		pattern := command[len(command)-1]
		if pattern == "./..." || !strings.HasPrefix(pattern, "./") || !strings.HasSuffix(pattern, "/...") {
			continue
		}
		relDir := strings.TrimSuffix(strings.TrimPrefix(pattern, "./"), "/...")
		if relDir == "" || relDir == "." {
			continue
		}
		modPath, ok := modulePaths[dir]
		if !ok {
			data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
			if readErr != nil {
				modulePaths[dir] = ""
				continue
			}
			modPath = modfile.ModulePath(data)
			modulePaths[dir] = modPath
		}
		if modPath == "" {
			continue
		}
		if _, exists := groups[dir]; !exists {
			groupOrder = append(groupOrder, dir)
		}
		groups[dir] = append(groups[dir], member{
			index:     i,
			module:    module,
			command:   command,
			pattern:   pattern,
			pkgPrefix: modPath + "/" + relDir,
			relDir:    relDir,
		})
	}

	for _, dir := range groupOrder {
		members := groups[dir]
		if len(members) < 2 {
			continue
		}
		// Only batch when no two selected modules overlap in directory scope.
		overlaps := false
		for i := range members {
			for j := i + 1; j < len(members); j++ {
				if pathUnder(members[i].relDir, members[j].relDir) || pathUnder(members[j].relDir, members[i].relDir) {
					overlaps = true
					break
				}
			}
			if overlaps {
				break
			}
		}
		if overlaps {
			continue
		}
		patterns := make([]string, len(members))
		for i, m := range members {
			patterns[i] = m.pattern
		}
		args := append([]string{"go", "test", "-json"}, patterns...)
		cmdArgs := applyLinkedFrameworkGoCommand(g.Root, dir, args)
		started := time.Now()
		run := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		run.Dir, run.Env = dir, baseEnv
		var stdout, stderr bytes.Buffer
		run.Stdout = &stdout
		run.Stderr = &stderr
		runErr := run.Run()
		batchDurationMS := time.Since(started).Milliseconds()

		type modState struct {
			failed    bool
			elapsedMS int64
			output    tailOutput
		}
		states := make([]modState, len(members))
		buildOutputs := map[string]*tailOutput{}
		var globalOutput tailOutput

		matchMember := func(pkg string) int {
			best, bestLen := -1, -1
			for i, m := range members {
				if pkg == m.pkgPrefix || strings.HasPrefix(pkg, m.pkgPrefix+"/") {
					if len(m.pkgPrefix) > bestLen {
						best, bestLen = i, len(m.pkgPrefix)
					}
				}
			}
			return best
		}

		scanner := bufio.NewScanner(&stdout)
		scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			var ev goTestJSONEvent
			if json.Unmarshal(line, &ev) != nil {
				_, _ = globalOutput.Write(line)
				_, _ = globalOutput.Write([]byte("\n"))
				continue
			}
			if ev.Action == "build-output" && ev.ImportPath != "" && ev.Output != "" {
				buf := buildOutputs[ev.ImportPath]
				if buf == nil {
					buf = &tailOutput{}
					buildOutputs[ev.ImportPath] = buf
				}
				_, _ = buf.Write([]byte(ev.Output))
			}
			target := ev.Package
			if target == "" {
				target = ev.ImportPath
			}
			idx := matchMember(target)
			if idx < 0 {
				if ev.Output != "" {
					_, _ = globalOutput.Write([]byte(ev.Output))
				}
				continue
			}
			st := &states[idx]
			if ev.Output != "" {
				_, _ = st.output.Write([]byte(ev.Output))
			}
			switch ev.Action {
			case "pass":
				if ev.Elapsed > 0 {
					st.elapsedMS += int64(ev.Elapsed * 1000)
				}
			case "fail", "build-fail":
				st.failed = true
				if ev.Elapsed > 0 {
					st.elapsedMS += int64(ev.Elapsed * 1000)
				}
				if ev.FailedBuild != "" {
					if buf := buildOutputs[ev.FailedBuild]; buf != nil {
						_, _ = st.output.Write([]byte(buf.String()))
					}
				}
			}
		}
		if stderr.Len() > 0 {
			_, _ = globalOutput.Write(stderr.Bytes())
		}

		anyFailed := false
		for _, st := range states {
			if st.failed {
				anyFailed = true
				break
			}
		}

		for i, m := range members {
			st := states[i]
			failed := st.failed || (runErr != nil && !anyFailed)
			durationMS := st.elapsedMS
			if durationMS == 0 {
				durationMS = batchDurationMS
			}
			record := moduleTestResult{
				agentModule: m.module,
				Command:     m.command,
				OK:          !failed,
				DurationMS:  durationMS,
			}
			if failed {
				outText := strings.TrimSpace(st.output.String())
				if outText == "" {
					outText = strings.TrimSpace(globalOutput.String())
				}
				if runErr != nil {
					record.Output = strings.TrimSpace(runErr.Error() + "\n" + outText)
				} else {
					record.Output = outText
				}
			}
			results[m.index] = record
			batched[m.index] = true
		}
	}
}
