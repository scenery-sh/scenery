package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/rustbuild"
	workerruntime "scenery.sh/runtime"
)

type workerBuildCommand struct {
	Process     string   `json:"process"`
	Module      string   `json:"module"`
	Directory   string   `json:"directory"`
	Workspace   string   `json:"workspace,omitempty"`
	Program     string   `json:"program"`
	Args        []string `json:"args"`
	Environment []string `json:"environment"`
	Roots       []string `json:"roots,omitempty"`
	Owners      []string `json:"owners,omitempty"`
}

type workerRebuildPlan struct {
	Generated     []string              `json:"generated,omitempty"`
	Declarations  map[string]bool       `json:"declarations"`
	Commands      []workerBuildCommand  `json:"commands"`
	Programs      []workerLaunchProgram `json:"programs"`
	SourceDigest  string                `json:"source_digest"`
	GoInputs      []string              `json:"go_inputs,omitempty"`
	GoWorkspace   string                `json:"go_workspace,omitempty"`
	RustWorkspace string                `json:"rust_workspace,omitempty"`
	HostConfig    json.RawMessage       `json:"host_config"`
}

func (dev *workerDevelopment) project(bundle workerBundle) (workerRebuildPlan, error) {
	plan := workerRebuildPlan{
		Generated: dev.watch.generated, Declarations: dev.declarations, SourceDigest: workerSourceDigest(dev.applied),
		Commands: []workerBuildCommand{}, Programs: []workerLaunchProgram{},
	}
	var err error
	plan.HostConfig, err = json.Marshal(struct {
		Host     workerHostPlan
		Auth     *workerAuthPlan
		Keys     []string
		Snapshot *workerruntime.ConfigSnapshot
	}{bundle.Host, bundle.Auth, bundle.HostConfigKeys, projectWorkerConfig(bundle.config, "host", bundle.HostConfigKeys)})
	if err != nil {
		return plan, err
	}
	if workspace := dev.goWorkspace; workspace != nil {
		plan.GoInputs = workspace.SourceFiles
		plan.GoWorkspace, err = filepath.EvalSymlinks(workspace.Dir)
		if err != nil {
			return plan, err
		}
	}
	if len(dev.rust.services) != 0 {
		plan.RustWorkspace = filepath.Join(dev.watch.root, ".scenery", "rust", "snapshot")
	}
	bundle.setWorkerConfig()
	for _, program := range bundle.Programs {
		launch, err := projectWorkerLaunch(program)
		if err != nil {
			return plan, err
		}
		plan.Programs = append(plan.Programs, launch)
		service := slices.IndexFunc(bundle.Host.Workers, func(service workerruntime.WorkerServicePlan) bool { return service.Address == program.Service })
		if service < 0 {
			return plan, fmt.Errorf("missing worker service %s", program.Service)
		}
		command := workerBuildCommand{Process: program.Process, Module: bundle.Host.Workers[service].Module}
		switch program.Runtime {
		case "go":
			command.Program, command.Directory, command.Environment = "go", plan.GoWorkspace, dev.goWorkspace.GoEnvironment
			command.Args, command.Roots, err = build.DevelopmentWorkerCommand(dev.goWorkspace, program.Service, program.Identity.ContractRevision)
		case "rust":
			command.Program = "cargo"
			target := dev.rust.selection[program.Service]
			command.Workspace = target.Workspace
			command.Directory, command.Args, command.Environment, command.Roots, command.Owners, err =
				dev.rust.inputs.DevelopmentCommand(dev.watch.root, target)
		case "js", "ts":
			index := slices.IndexFunc(dev.js, func(item workerJSBuild) bool { return item.program.Service == program.Service })
			target := dev.js[index].service
			command.Program = "bun"
			command.Directory, command.Workspace = target.Directory, target.Workspace
			command.Environment = envpolicy.Environ()
			entry := filepath.Join(dev.watch.root, target.Directory, "scenerycontract", "main.ts")
			command.Args = []string{"build", "--no-env-file", "--target=bun", "--packages=bundle", "--outfile={output}", "--metafile={metafile}",
				"--compile", "--no-compile-autoload-dotenv", "--no-compile-autoload-bunfig", entry}
		}
		if err != nil {
			return plan, err
		}
		command.Program, err = exec.LookPath(command.Program)
		if err != nil {
			return plan, err
		}
		plan.Commands = append(plan.Commands, command)
	}
	return plan, nil
}

// Refresh is invoked only for declarations and build configuration changes.
func refreshWorkerRebuildPlan(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("invalid rebuild plan request")
	}
	root, cfg, err := app.DiscoverRoot(args[0])
	if err != nil {
		return err
	}
	ctx := context.Background()
	compiled, err := compiler.CompileContext(ctx, root)
	if err != nil {
		return err
	}
	if compiled.Valid() {
		if !compiled.UsesWorkerBundle() {
			return fmt.Errorf("application host changed; restart the application to load its new runtime")
		}
		if compiled.Manifest.ContractRevision != args[1] {
			return fmt.Errorf("application contract changed; restart the application to load its new manifest")
		}
	}
	generated, err := compiler.ManagedGeneratedRoots(compiled)
	if err != nil {
		return err
	}
	scope := &workerInputScope{root: root, generated: generated}
	inputs, err := scope.capture()
	if err != nil {
		return err
	}
	dev := &workerDevelopment{watch: scope, applied: inputs}
	bundle, err := dev.prepare(ctx, compiled)
	if err != nil {
		return err
	}
	hasGo := false
	for _, service := range bundle.Host.Workers {
		program := workerProgram{Service: service.Address, Process: service.Process, Runtime: service.Runtime}
		for _, js := range dev.js {
			if js.program.Service == service.Address {
				program = js.program
				break
			}
		}
		bundle.Programs = append(bundle.Programs, program)
		hasGo = hasGo || service.Runtime == "go"
	}
	if hasGo {
		dev.goWorkspace, err = build.PrepareForCompileWithContractSnapshotContext(ctx, root, cfg, nil, compiled)
		if err != nil {
			return err
		}
		if err := build.DiscoverDevelopmentWorkerInputs(ctx, dev.goWorkspace); err != nil {
			return err
		}
	}
	if len(dev.rust.services) != 0 {
		dev.rust.inputs, err = rustbuild.DiscoverDevelopmentInputs(ctx, root, dev.rust.services)
		if err != nil {
			return err
		}
	}
	if err := bundle.projectConfig(compiled); err != nil {
		return err
	}
	if err := bundle.resolveDevelopmentConfig(ctx, dev.compiled, cfg, args[2]); err != nil {
		return err
	}
	plan, err := dev.project(bundle)
	if err != nil {
		return err
	}
	return writePrivateWorkerJSON(args[3], plan)
}
