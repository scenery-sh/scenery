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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/modfile"

	appcfg "scenery.sh/internal/app"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
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
	var root, changedBase string
	var jsonOutput, withHost, branchDB, changed bool
	flags.StringVar(&root, "app-root", "", "")
	flags.BoolVar(&withHost, "with-host", false, "")
	flags.BoolVar(&branchDB, "branch-db", false, "")
	flags.BoolVar(&changed, "changed", false, "")
	flags.StringVar(&changedBase, "changed-base", "", "")
	registerJSONOutput(flags, &jsonOutput)
	targets, err := parseCLIFlags(flags, args)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if cliFlagSet(flags, "changed-base") {
		if !changed {
			return usageErrorf("--changed-base requires --changed")
		}
		changedBase = strings.TrimSpace(changedBase)
		if changedBase == "" {
			return usageErrorf("--changed-base must not be empty")
		}
	}
	if changed && len(targets) > 0 {
		return usageErrorf("--changed cannot be combined with explicit module targets")
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
	var changedSelection *changedTestSelection
	if changed {
		changedFiles, gitErr := gitChangedFiles(ctx, root, changedBase)
		if gitErr != nil {
			return gitErr
		}
		var sel changedTestSelection
		modules, sel = selectChangedTestModules(g, modules, changedFiles, changedBase)
		changedSelection = &sel
		if len(modules) == 0 {
			if jsonOutput {
				data := map[string]any{
					"ok":          true,
					"app_root":    root,
					"modules":     []moduleTestResult{},
					"duration_ms": int64(0),
					"changed":     changedSelection,
				}
				return writeCLIJSON(out, withCLIPayloadIdentity("scenery.test.result", data))
			}
			_, _ = fmt.Fprintln(out, "no changed modules to test")
			return nil
		}
	} else {
		modules, err = selectTestModules(modules, targets)
		if err != nil {
			return err
		}
		if len(modules) == 0 {
			return fmt.Errorf("failed_precondition: no application modules to test")
		}
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
	wroteOutput := false
	if jsonOutput {
		data := map[string]any{
			"ok": success, "app_root": root, "modules": results, "duration_ms": time.Since(started).Milliseconds(),
		}
		if changedSelection != nil {
			data["changed"] = changedSelection
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
		wroteOutput = true
	} else {
		for _, result := range results {
			state := "passed"
			if !result.OK {
				state = "failed"
			}
			extra := ""
			if result.Database != nil {
				verb := "cloned"
				if result.Database.Reused {
					verb = "reset"
				}
				extra = fmt.Sprintf(" [db %s %s in %dms]", result.Database.Name, verb, result.Database.CloneMS)
			}
			_, _ = fmt.Fprintf(out, "%s (%s): %s in %dms%s\n", result.Name, result.Runtime, state, result.DurationMS, extra)
			if result.Output != "" {
				_, _ = fmt.Fprintln(out, result.Output)
			}
			wroteOutput = true
		}
	}
	if !success {
		if wroteOutput {
			return &silentCLIError{err: fmt.Errorf("module tests failed"), code: 3}
		}
		return &codedCLIError{err: fmt.Errorf("module tests failed"), code: 3}
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

type changedTestSelection struct {
	Base            string   `json:"base,omitempty"`
	Files           []string `json:"files"`
	DirectModules   []string `json:"direct_modules"`
	AffectedModules []string `json:"affected_modules"`
	FallbackAll     bool     `json:"fallback_all,omitempty"`
	FallbackFiles   []string `json:"fallback_files,omitempty"`
}

var globalSharedTestFiles = map[string]bool{
	"app.scn":        true,
	"go.mod":         true,
	"go.sum":         true,
	"go.work":        true,
	"go.work.sum":    true,
	"scenery.lock":   true,
	".scenery.json":  true,
	"package.json":   true,
	"bun.lock":       true,
	"bun.lockb":      true,
	"Cargo.toml":     true,
	"Cargo.lock":     true,
	"pyproject.toml": true,
	"uv.lock":        true,
}

func gitChangedFiles(ctx context.Context, appRoot, base string) ([]string, error) {
	topOut, err := runGitOutput(ctx, appRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("failed_precondition: --changed requires a git repository: %w", err)
	}
	gitRoot := strings.TrimSpace(string(topOut))
	if canonical, evalErr := filepath.EvalSymlinks(gitRoot); evalErr == nil {
		gitRoot = canonical
	}
	canonicalAppRoot := appRoot
	if canonical, evalErr := filepath.EvalSymlinks(appRoot); evalErr == nil {
		canonicalAppRoot = canonical
	}

	var rawPaths []string
	if base == "" {
		statusOut, statusErr := runGitOutput(ctx, canonicalAppRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if statusErr != nil {
			return nil, fmt.Errorf("failed_precondition: inspect git working tree: %w", statusErr)
		}
		rawPaths = parseGitStatusPorcelainZ(statusOut)
	} else {
		if _, verifyErr := runGitOutput(ctx, canonicalAppRoot, "rev-parse", "--verify", "--quiet", base+"^{commit}"); verifyErr != nil {
			return nil, fmt.Errorf("failed_precondition: invalid --changed-base ref %q: %w", base, verifyErr)
		}
		diffBase := base
		if mbOut, mbErr := runGitOutput(ctx, canonicalAppRoot, "merge-base", base, "HEAD"); mbErr == nil {
			if mb := strings.TrimSpace(string(mbOut)); mb != "" {
				diffBase = mb
			}
		}
		diffOut, diffErr := runGitOutput(ctx, canonicalAppRoot, "diff", "--no-renames", "--name-only", "-z", diffBase)
		if diffErr != nil {
			return nil, fmt.Errorf("failed_precondition: diff against %q: %w", base, diffErr)
		}
		rawPaths = append(rawPaths, parseGitNulPaths(diffOut)...)

		untrackedOut, untrackedErr := runGitOutput(ctx, canonicalAppRoot, "ls-files", "--full-name", "--others", "--exclude-standard", "-z")
		if untrackedErr != nil {
			return nil, fmt.Errorf("failed_precondition: list untracked files: %w", untrackedErr)
		}
		rawPaths = append(rawPaths, parseGitNulPaths(untrackedOut)...)
	}

	seen := map[string]bool{}
	result := make([]string, 0, len(rawPaths))
	for _, gitRel := range rawPaths {
		absPath := filepath.Join(gitRoot, filepath.FromSlash(gitRel))
		appRel, relErr := filepath.Rel(canonicalAppRoot, absPath)
		if relErr != nil || appRel == ".." || strings.HasPrefix(appRel, ".."+string(filepath.Separator)) {
			continue
		}
		slashRel := filepath.ToSlash(filepath.Clean(appRel))
		if slashRel == "." || seen[slashRel] {
			continue
		}
		seen[slashRel] = true
		result = append(result, slashRel)
	}
	slices.Sort(result)
	return result, nil
}

func runGitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = envpolicy.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

func parseGitStatusPorcelainZ(data []byte) []string {
	var paths []string
	for len(data) > 0 {
		idx := bytes.IndexByte(data, 0)
		var entry []byte
		if idx < 0 {
			entry, data = data, nil
		} else {
			entry, data = data[:idx], data[idx+1:]
		}
		if len(entry) < 4 {
			continue
		}
		x, y := entry[0], entry[1]
		path := string(entry[3:])
		if path != "" {
			paths = append(paths, path)
		}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			if nextIdx := bytes.IndexByte(data, 0); nextIdx >= 0 {
				orig := string(data[:nextIdx])
				data = data[nextIdx+1:]
				if orig != "" {
					paths = append(paths, orig)
				}
			} else if len(data) > 0 {
				paths = append(paths, string(data))
				data = nil
			}
		}
	}
	return paths
}

func parseGitNulPaths(data []byte) []string {
	var paths []string
	for _, entry := range bytes.Split(data, []byte{0}) {
		if len(entry) > 0 {
			paths = append(paths, string(entry))
		}
	}
	return paths
}

func changedPathUnder(dir, relPath string) bool {
	dir = filepath.ToSlash(filepath.Clean(dir))
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if dir == "." {
		return relPath != ".." && !strings.HasPrefix(relPath, "../")
	}
	return relPath == dir || strings.HasPrefix(relPath, dir+"/")
}

// selectChangedTestModules maps changed app-relative paths to affected modules,
// expands through the reverse dependency graph, and falls back to all modules
// when shared or unowned files change.
func selectChangedTestModules(g agentGraph, modules []agentModule, changedFiles []string, base string) ([]agentModule, changedTestSelection) {
	selection := changedTestSelection{
		Base:            base,
		Files:           []string{},
		DirectModules:   []string{},
		AffectedModules: []string{},
	}
	if len(changedFiles) == 0 {
		return nil, selection
	}
	selection.Files = append([]string(nil), changedFiles...)

	moduleDirs := map[string]string{}
	if g.Result != nil && g.Manifest != nil {
		for _, resource := range g.Manifest.Resources {
			if resource.Kind != "scenery.module" {
				continue
			}
			dir := firstNonEmpty(inspectString(resource.Spec["workspace_package_root"]), inspectString(resource.Spec["source"]))
			if dir != "" {
				moduleDirs[cp.ModuleInstancePath(resource)] = filepath.ToSlash(filepath.Clean(dir))
			}
		}
	}
	for _, module := range modules {
		moduleDirs[module.Name] = filepath.ToSlash(filepath.Clean(module.Directory))
	}

	directSet := map[string]bool{}
	var fallbackFiles []string
	for _, file := range changedFiles {
		cleanFile := filepath.ToSlash(filepath.Clean(file))
		if globalSharedTestFiles[cleanFile] {
			fallbackFiles = append(fallbackFiles, cleanFile)
			continue
		}
		bestLen := -1
		var matched []string
		for name, dir := range moduleDirs {
			if dir == "." || !changedPathUnder(dir, cleanFile) {
				continue
			}
			if len(dir) > bestLen {
				bestLen = len(dir)
				matched = []string{name}
			} else if len(dir) == bestLen {
				matched = append(matched, name)
			}
		}
		if len(matched) == 0 {
			fallbackFiles = append(fallbackFiles, cleanFile)
			continue
		}
		for _, name := range matched {
			directSet[name] = true
		}
	}

	for name := range directSet {
		selection.DirectModules = append(selection.DirectModules, name)
	}
	slices.Sort(selection.DirectModules)

	if len(fallbackFiles) > 0 {
		selection.FallbackAll = true
		selection.FallbackFiles = fallbackFiles
		for _, module := range modules {
			selection.AffectedModules = append(selection.AffectedModules, module.Name)
		}
		return modules, selection
	}

	reverseDeps := moduleReverseDependencies(g)
	affected := map[string]bool{}
	queue := make([]string, 0, len(directSet))
	for name := range directSet {
		affected[name] = true
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, dep := range reverseDeps[curr] {
			if !affected[dep] {
				affected[dep] = true
				queue = append(queue, dep)
			}
		}
	}

	selected := make([]agentModule, 0, len(affected))
	for _, module := range modules {
		if affected[module.Name] {
			selected = append(selected, module)
			selection.AffectedModules = append(selection.AffectedModules, module.Name)
		}
	}
	return selected, selection
}

// moduleReverseDependencies returns a map from module name to the sorted list
// of modules that directly depend on it through manifest resource references.
func moduleReverseDependencies(g agentGraph) map[string][]string {
	if g.Result == nil || g.Manifest == nil {
		return nil
	}
	byAddress := make(map[string]graph.Resource, len(g.Manifest.Resources))
	for _, resource := range g.Manifest.Resources {
		byAddress[resource.Address] = resource
	}
	deps := map[string]map[string]bool{}
	for _, resource := range g.Manifest.Resources {
		owner := resource.Module
		if resource.Kind == "scenery.module" {
			owner = cp.ModuleInstancePath(resource)
		}
		if owner == "" {
			owner = "app"
		}
		graph.WalkReferences(resource.Spec, "/spec", func(_, reference string) {
			target, ok := resolveAgentGraphReference(byAddress, resource, reference)
			if !ok {
				return
			}
			targetModule := target.Module
			if target.Kind == "scenery.module" {
				targetModule = cp.ModuleInstancePath(target)
			}
			if targetModule == "" {
				targetModule = "app"
			}
			if targetModule == owner {
				return
			}
			if deps[targetModule] == nil {
				deps[targetModule] = map[string]bool{}
			}
			deps[targetModule][owner] = true
		})
	}
	reverse := make(map[string][]string, len(deps))
	for mod, dependents := range deps {
		list := make([]string, 0, len(dependents))
		for dep := range dependents {
			list = append(list, dep)
		}
		slices.Sort(list)
		reverse[mod] = list
	}
	return reverse
}

func resolveAgentGraphReference(byAddress map[string]graph.Resource, resource graph.Resource, reference string) (graph.Resource, bool) {
	if strings.Contains(reference, "/") {
		target, ok := byAddress[reference]
		return target, ok
	}
	blockType, name, ok := strings.Cut(reference, ".")
	if !ok || strings.Contains(name, ".") {
		return graph.Resource{}, false
	}
	if target, found := byAddress[graph.ResourceAddress(resource.Module, blockType, name)]; found {
		return target, true
	}
	target, found := byAddress[graph.ResourceAddress("app", blockType, name)]
	return target, found
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
