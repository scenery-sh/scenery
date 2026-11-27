package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	results := make([]moduleTestResult, 0, len(modules))
	success := true
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

	for _, module := range modules {
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
					lastHostSummary = hostSummary
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
		success = success && record.OK
		results = append(results, record)
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
