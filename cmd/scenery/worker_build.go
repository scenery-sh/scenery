package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/auth"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/rustbuild"
	workerruntime "scenery.sh/runtime"
)

type workerBundle struct {
	Version          int                             `json:"version"`
	ContractRevision string                          `json:"contract_revision"`
	Host             workerruntime.ProcessHostConfig `json:"host"`
	Types            contract.ManifestTypes          `json:"types"`
	Programs         []workerProgram                 `json:"programs"`
	Frontend         *workerFrontend                 `json:"frontend,omitempty"`
	Auth             *auth.StandardConfig            `json:"auth,omitempty"`
	HostConfigKeys   []string                        `json:"host_config_keys,omitempty"`
	config           *workerruntime.ConfigSnapshot
}

type workerProgram struct {
	Service    string                `json:"service"`
	Process    string                `json:"process"`
	Binary     string                `json:"binary"`
	Digest     string                `json:"digest"`
	Runtime    string                `json:"runtime"`
	Identity   workerProcessIdentity `json:"identity"`
	ConfigKeys []string              `json:"config_keys,omitempty"`
	config     *workerruntime.ConfigSnapshot
}

func hasRustWorkers(result *compiler.Result) bool {
	return result != nil && result.Manifest != nil && compiler.UsesWorkerBundle(result.Manifest.Resources)
}

func projectRustBuilds(result *compiler.Result, plan []workerruntime.WorkerServicePlan, role string) (map[string]rustbuild.Service, error) {
	resources := cp.ResourcesByAddress(result.Manifest)
	modules := make(map[string]graph.Resource)
	for _, module := range result.Manifest.Resources {
		if module.Kind == "scenery.module" {
			modules[cp.ModuleInstancePath(module)] = module
		}
	}
	services := make(map[string]rustbuild.Service)
	for _, service := range plan {
		if cp.StringValue(resources[service.Address].Spec["runtime"]) != "rust" {
			continue
		}
		module := modules[service.Module]
		moduleDirectory := cp.StringValue(module.Spec["workspace_package_root"])
		packageSpec, _ := module.Spec["package"].(map[string]any)
		rustContract, _ := packageSpec["rust_contract"].(map[string]any)
		selected := rustbuild.Service{
			ModuleDirectory: moduleDirectory, Crate: cp.StringValue(rustContract["crate"]), ServiceName: service.Name,
		}
		for _, target := range result.Manifest.Resources {
			if target.Kind != "scenery.rust-target" || cp.StringValue(target.Spec["role"]) != role {
				continue
			}
			workspace := resources[workerReference(target, target.Spec["workspace"], "rust_workspace")]
			toolchain := resources[workerReference(target, target.Spec["toolchain"], "rust_toolchain")]
			root := cp.StringValue(workspace.Spec["root"])
			relative, err := filepath.Rel(root, moduleDirectory)
			if root == "" || err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			if selected.Workspace != "" && (root == selected.Workspace || len(root) <= len(selected.Workspace)) {
				continue
			}
			selected.Workspace, selected.Toolchain = root, cp.StringValue(toolchain.Spec["version"])
		}
		if selected.Workspace == "" || selected.Toolchain == "" || selected.Crate == "" {
			return nil, fmt.Errorf("service %s has no selected %s Cargo target", service.Address, role)
		}
		services[service.Address] = selected
	}
	return services, nil
}

func buildWorkerBundle(ctx context.Context, result *compiler.Result, role string, check bool) (workerBundle, error) {
	var bundle workerBundle
	bundle, selection, services, err := prepareWorkerBundle(ctx, result, role)
	if err != nil {
		return bundle, err
	}
	artifacts, err := rustbuild.Build(ctx, result.Root, services, role == "artifact", check)
	if err != nil || check {
		return bundle, err
	}
	bundle.addRustWorkers(result, selection, artifacts)
	if _, err := bundle.addGoWorkers(ctx, result, nil, nil); err != nil {
		return workerBundle{}, err
	}
	if err := bundle.projectConfig(result); err != nil {
		return workerBundle{}, err
	}
	return bundle, nil
}

func prepareWorkerBundle(ctx context.Context, result *compiler.Result, role string) (workerBundle, map[string]rustbuild.Service, []rustbuild.Service, error) {
	plan, err := projectWorkerServices(result.Manifest)
	if err != nil {
		return workerBundle{}, nil, nil, err
	}
	selection, err := projectRustBuilds(result, plan, role)
	if err != nil {
		return workerBundle{}, nil, nil, err
	}
	if _, err := generate.GenerateSnapshotWithContext(ctx, result, "contracts", false); err != nil {
		return workerBundle{}, nil, nil, err
	}
	var services []rustbuild.Service
	for _, service := range plan {
		if selected, exists := selection[service.Address]; exists {
			services = append(services, selected)
		}
	}
	bundle := workerBundle{
		Version: 1, ContractRevision: result.Manifest.ContractRevision,
		Host:  workerruntime.ProcessHostConfig{Name: result.Manifest.Application.Name, Workers: plan},
		Types: projectWorkerTypes(result.Manifest),
	}
	return bundle, selection, services, nil
}

func (bundle *workerBundle) addRustWorkers(result *compiler.Result, selection map[string]rustbuild.Service, artifacts map[rustbuild.Service]rustbuild.Artifact) {
	resources := cp.ResourcesByAddress(result.Manifest)
	for _, service := range bundle.Host.Workers {
		artifact, exists := artifacts[selection[service.Address]]
		if !exists {
			continue
		}
		bundle.Programs = append(bundle.Programs, workerProgram{
			Service: service.Address, Process: service.Process, Binary: artifact.Path, Digest: artifact.Digest, Runtime: "rust",
			Identity: workerProcessIdentity{
				ContractRevision:       compiler.ServiceContractRevision(result.Manifest, resources[service.Address], nil),
				ImplementationRevision: artifact.Digest, BuildInputDigest: artifact.Digest, Target: "rust",
			},
		})
	}
}

func (bundle *workerBundle) addGoWorkers(ctx context.Context, compiled *compiler.Result, preparation *build.Result, retained []string) (*build.Result, error) {
	goProcesses := make(map[string]string)
	resources := cp.ResourcesByAddress(compiled.Manifest)
	for _, service := range bundle.Host.Workers {
		if cp.StringValue(resources[service.Address].Spec["runtime"]) == "go" {
			goProcesses[service.Address] = service.Process
		}
	}
	if len(goProcesses) == 0 {
		return nil, nil
	}
	root, cfg, err := app.DiscoverRoot(compiled.Root)
	if err != nil {
		return nil, err
	}
	reused := false
	if preparation != nil {
		copy := *preparation
		copy.Contract = compiled
		preparation = &copy
		reused, err = build.PrepareCachedWorkspaceHeldContext(ctx, root, cfg, preparation, nil)
		if err != nil {
			return nil, err
		}
	}
	if !reused {
		preparation, err = build.PrepareForCompileWithContractSnapshotContext(ctx, root, cfg, nil, compiled)
		if err != nil {
			return nil, err
		}
	}
	defer preparation.ReleaseWorkspace()
	processes, join, err := build.BuildDevelopmentWorkersContext(ctx, preparation, retained)
	if err != nil {
		return nil, err
	}
	if err := join(); err != nil {
		return nil, err
	}
	for _, process := range processes.Services {
		identity := process.Identity
		bundle.Programs = append(bundle.Programs, workerProgram{
			Service: process.Service, Process: goProcesses[process.Service], Binary: process.Binary, Digest: process.ArtifactDigest, Runtime: "go",
			Identity: workerProcessIdentity{
				ContractRevision: identity.ContractRevision, ImplementationRevision: identity.ImplementationRevision,
				BuildInputDigest: identity.BuildInputDigest, Target: identity.GoTarget,
			},
		})
	}
	return preparation, nil
}

func projectWorkerTypes(manifest *graph.Manifest) contract.ManifestTypes {
	types := make(contract.ManifestTypes)
	for _, resource := range manifest.Resources {
		shape := contract.ManifestShape{Open: resource.Spec["open"] == true}
		switch resource.Kind {
		case "scenery.record":
			shape.Kind = "record"
			shape.UnknownFields = cp.StringValue(resource.Spec["unknown_fields"])
			for _, field := range cp.NamedChildren(resource.Spec, "field") {
				shape.Fields = append(shape.Fields, projectWorkerField(resource, field))
			}
		case "scenery.enum":
			shape.Kind = "enum"
			for _, value := range cp.NamedChildren(resource.Spec, "value") {
				shape.Values = append(shape.Values, cp.WireName(value, cp.StringValue(value["name"])))
			}
		case "scenery.union":
			shape.Kind = "union"
			shape.Discriminator = cp.StringValue(resource.Spec["discriminator"])
			shape.Variants = make(map[string]string)
			for _, variant := range cp.NamedChildren(resource.Spec, "variant") {
				shape.Variants[cp.WireName(variant, cp.StringValue(variant["name"]))] = workerTypeExpression(resource.Module, variant["type"])
			}
		default:
			continue
		}
		types[resource.Address] = shape
	}
	return types
}

func projectWorkerField(resource graph.Resource, field map[string]any) contract.ManifestField {
	constraints := contract.ContractConstraints{
		Pattern: cp.StringValue(field["pattern"]), Format: cp.StringValue(field["format"]), UniqueItems: field["unique_items"] == true,
	}
	for key, target := range map[string]**int64{
		"min_length": &constraints.MinLength, "max_length": &constraints.MaxLength,
		"min_items": &constraints.MinItems, "max_items": &constraints.MaxItems,
	} {
		if value, ok := cp.IntegerValue(field[key]); ok {
			*target = new(int64(value))
		}
	}
	if value := cp.StringValue(field["minimum"]); value != "" {
		constraints.Minimum = new(value)
	}
	if value := cp.StringValue(field["maximum"]); value != "" {
		constraints.Maximum = new(value)
	}
	var defaultValue []byte
	if value, exists := field["default"]; exists {
		if scalar, ok := value.(map[string]any); ok && scalar["$scalar"] == "int" {
			value = json.Number(cp.StringValue(scalar["value"]))
		}
		defaultValue, _ = json.Marshal(value)
	}
	return contract.ManifestField{
		Name: cp.WireName(field, cp.StringValue(field["name"])), Type: workerTypeExpression(resource.Module, field["type"]),
		Default: defaultValue, Constraints: constraints,
	}
}

func writeWorkerBundle(bundle workerBundle, destination string) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("bundle destination already exists or cannot be inspected: %s", destination)
	}
	staging, err := os.MkdirTemp(parent, ".worker-bundle-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.Mkdir(filepath.Join(staging, "services"), 0o755); err != nil {
		return err
	}
	if bundle.Frontend != nil {
		frontend := *bundle.Frontend
		relative := filepath.Join("frontends", frontend.Name)
		if err := os.CopyFS(filepath.Join(staging, relative), os.DirFS(frontend.Root)); err != nil {
			return err
		}
		frontend.Root = filepath.ToSlash(relative)
		bundle.Frontend = &frontend
	}
	for i, program := range bundle.Programs {
		relative := filepath.ToSlash(filepath.Join("services", program.Process))
		if err := copyWorkerExecutable(program.Binary, filepath.Join(staging, filepath.FromSlash(relative))); err != nil {
			return err
		}
		bundle.Programs[i].Binary = relative
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err := copyWorkerExecutable(executable, filepath.Join(staging, "scenery-host")); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(staging, destination)
}

func copyWorkerExecutable(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o755)
}
