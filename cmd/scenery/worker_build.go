package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/rustbuild"
	"scenery.sh/internal/workerplan"
	workerruntime "scenery.sh/runtime"
)

type workerBundle struct {
	Version          int                      `json:"version"`
	ContractRevision string                   `json:"contract_revision"`
	Host             workerHostPlan           `json:"host"`
	Types            contract.ManifestTypes   `json:"types"`
	Programs         []workerProgram          `json:"programs"`
	Frontend         *workerFrontend          `json:"frontend,omitempty"`
	Auth             *workerplan.StandardAuth `json:"auth,omitempty"`
	HostConfigKeys   []string                 `json:"host_config_keys,omitempty"`
	Reports          workerReportIdentity     `json:"reports"`
	config           *workerruntime.ConfigSnapshot
	// goPlatform builds the Go services for another platform, as an OCI
	// image build does; nil builds them for this host.
	goPlatform *compiler.GoPlatform
}

// workerReportIdentity is the identity the Host stamps on the failure reports
// it records, so `scenery report` reads them like the CLI's own.
type workerReportIdentity struct {
	SchemaRevision  string `json:"schema_revision"`
	ProducerVersion string `json:"producer_version"`
}

func currentWorkerReportIdentity() workerReportIdentity {
	return workerReportIdentity{
		SchemaRevision:  newCLIPayloadIdentity(failureReportKind).SchemaRevision,
		ProducerVersion: buildVersionResponse().Version,
	}
}

type workerHostPlan struct {
	Name          string
	Observability workerplan.ObservabilityConfig
	Workers       []workerplan.WorkerServicePlan
	Events        []workerplan.WorkerEventPlan    `json:",omitempty"`
	MCPServers    map[string]mcpcontract.Manifest `json:",omitempty"`
	Assistants    []workerAssistantPlan           `json:",omitempty"`
}

type workerProgram struct {
	Service    string                `json:"service"`
	Process    string                `json:"process"`
	Binary     string                `json:"binary"`
	Digest     string                `json:"digest"`
	Runtime    string                `json:"runtime"`
	Identity   workerProcessIdentity `json:"identity"`
	Databases  map[string]string     `json:"databases,omitempty"`
	ConfigKeys []string              `json:"config_keys,omitempty"`
	config     *workerruntime.ConfigSnapshot
}

func projectRustBuilds(result *compiler.Result, plan []workerplan.WorkerServicePlan, role string) (map[string]rustbuild.Service, error) {
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

func validateWorkerBundleTargetPlatform(result *compiler.Result, role, goTarget string) error {
	if result == nil || result.Manifest == nil {
		return nil
	}
	hasGoService := false
	for _, service := range compiler.RuntimeServices(result.Manifest.Resources) {
		if firstNonEmpty(cp.StringValue(service.Spec["runtime"]), "go") == "go" {
			hasGoService = true
			break
		}
	}
	if goTarget == "" && !hasGoService {
		return nil
	}
	target, err := compiler.ResolveGoBuildTarget(result, goTarget, role)
	if err != nil {
		return err
	}
	if target.Context.GOOS != goruntime.GOOS || target.Context.GOARCH != goruntime.GOARCH {
		return preconditionErrorf("worker bundle target %q (%s/%s) does not match host platform %s/%s; worker bundles package host-built executables", target.Name, target.Context.GOOS, target.Context.GOARCH, goruntime.GOOS, goruntime.GOARCH)
	}
	return nil
}

// buildWorkerBundle builds every worker of the application for role. goTarget
// names the go_target of its Go services; empty selects the one with that role.
func buildWorkerBundle(ctx context.Context, result *compiler.Result, role, goTarget string, check bool) (workerBundle, error) {
	if !check {
		if err := validateWorkerBundleTargetPlatform(result, role, goTarget); err != nil {
			return workerBundle{}, err
		}
	}
	var bundle workerBundle
	bundle, selection, services, err := prepareWorkerBundle(ctx, result, role, true)
	if err != nil {
		return bundle, err
	}
	js, err := projectJSBuilds(result, bundle, role)
	if err != nil {
		return bundle, err
	}
	for i := range js {
		program, _, err := (&js[i]).run(ctx, result.Root, check)
		if err != nil {
			return bundle, err
		}
		if !check {
			bundle.Programs = append(bundle.Programs, program)
		}
	}
	pythonBuilds, err := projectPythonBuilds(result, bundle, role)
	if err != nil {
		return bundle, err
	}
	for i := range pythonBuilds {
		program, _, err := (&pythonBuilds[i]).run(ctx, result.Root, check)
		if err != nil {
			return bundle, err
		}
		if !check {
			bundle.Programs = append(bundle.Programs, program)
		}
	}
	artifacts, err := rustbuild.Build(ctx, result.Root, services, role == "artifact", check)
	if err != nil || check {
		return bundle, err
	}
	bundle.addRustWorkers(result, selection, artifacts)
	if _, err := bundle.addGoWorkers(ctx, result, role, goTarget, nil, nil, nil); err != nil {
		return workerBundle{}, err
	}
	if err := bundle.projectConfig(result); err != nil {
		return workerBundle{}, err
	}
	return bundle, nil
}

func prepareWorkerBundle(ctx context.Context, result *compiler.Result, role string, generateContracts bool) (workerBundle, map[string]rustbuild.Service, []rustbuild.Service, error) {
	plan, err := projectWorkerServices(result.Manifest)
	if err != nil {
		return workerBundle{}, nil, nil, err
	}
	selection, err := projectRustBuilds(result, plan, role)
	if err != nil {
		return workerBundle{}, nil, nil, err
	}
	if generateContracts {
		if _, err := generate.GenerateSnapshotWithContext(ctx, result, "contracts", false); err != nil {
			return workerBundle{}, nil, nil, err
		}
	}
	var services []rustbuild.Service
	for _, service := range plan {
		if selected, exists := selection[service.Address]; exists {
			services = append(services, selected)
		}
	}
	types, err := projectWorkerTypes(result.Manifest)
	if err != nil {
		return workerBundle{}, nil, nil, err
	}
	events, err := projectWorkerEvents(result.Manifest, plan)
	if err != nil {
		return workerBundle{}, nil, nil, err
	}
	bundle := workerBundle{
		Version: 1, ContractRevision: result.Manifest.ContractRevision,
		Host:  workerHostPlan{Name: result.Manifest.Application.Name, Workers: plan, Events: events},
		Types: types,
		// An application without native services still has a program list.
		Programs: []workerProgram{},
		Reports:  currentWorkerReportIdentity(),
	}
	if err := bundle.projectAssistants(result); err != nil {
		return workerBundle{}, nil, nil, err
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

// addGoWorkers links the Go services from the named go_target, or from the
// target with the given role when goTarget is empty. A retained preparation
// keeps the target it was prepared for.
func (bundle *workerBundle) addGoWorkers(ctx context.Context, compiled *compiler.Result, role, goTarget string, preparation *build.Result, retained []string, snapshot *build.SourceSnapshot) (*build.Result, error) {
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
	if preparation == nil && bundle.goPlatform == nil && goTarget == "" && role == "development" {
		if loaded, ok, loadErr := build.LoadPrimedWorkspace(root, cfg, compiled); loadErr == nil && ok {
			preparation = loaded
		}
	}
	if preparation != nil {
		copy := *preparation
		copy.Contract = compiled
		preparation = &copy
		reused, err = build.PrepareCachedWorkspaceHeldContext(ctx, root, cfg, preparation, snapshot)
		if err != nil {
			return nil, err
		}
	}
	if !reused {
		if bundle.goPlatform != nil {
			preparation, err = build.PrepareForPlatformContext(ctx, root, cfg, compiled, goTarget, role, *bundle.goPlatform)
		} else {
			preparation, err = build.PrepareForTargetWithContractSnapshotContext(ctx, root, cfg, snapshot, compiled, goTarget, role)
		}
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

func projectWorkerTypes(manifest *graph.Manifest) (contract.ManifestTypes, error) {
	types := make(contract.ManifestTypes)
	resources := cp.ResourcesByAddress(manifest)
	for _, resource := range manifest.Resources {
		shape := contract.ManifestShape{Open: resource.Spec["open"] == true}
		switch resource.Kind {
		case "scenery.record":
			shape.Kind = "record"
			shape.UnknownFields = cp.StringValue(resource.Spec["unknown_fields"])
			for _, field := range cp.NamedChildren(resource.Spec, "field") {
				projected := projectWorkerField(resource, field)
				if _, exists := field["default"]; exists {
					wire, err := compiler.StaticFieldDefaultJSON(resources, resource, field)
					if err != nil {
						return nil, fmt.Errorf("record %s field %s default: %w", resource.Address, projected.Name, err)
					}
					projected.Default = wire
				}
				shape.Fields = append(shape.Fields, projected)
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
	return types, nil
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
	return contract.ManifestField{
		Name: cp.WireName(field, cp.StringValue(field["name"])), Type: workerTypeExpression(resource.Module, field["type"]),
		Constraints: constraints,
	}
}

func writeWorkerBundle(ctx context.Context, bundle workerBundle, destination string) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if _, err := workerBundleDestinationExists(destination); err != nil {
		return err
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
	host, err := rustbuild.Host(ctx)
	if err != nil {
		return err
	}
	if err := copyWorkerExecutable(host, filepath.Join(staging, "scenery-kernel")); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return publishWorkerBundle(staging, destination)
}

// publishWorkerBundle moves a staged bundle to destination. A rebuild
// replaces the previous bundle there; anything else is never removed.
func publishWorkerBundle(staging, destination string) error {
	exists, err := workerBundleDestinationExists(destination)
	if err != nil {
		return err
	}
	if !exists {
		return os.Rename(staging, destination)
	}
	retired := staging + ".retired"
	if err := os.Rename(destination, retired); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return errors.Join(err, os.Rename(retired, destination))
	}
	return os.RemoveAll(retired)
}

// workerBundleDestinationExists reports whether destination holds a previous
// bundle and refuses any other existing entry.
func workerBundleDestinationExists(destination string) (bool, error) {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, name := range []string{"manifest.json", "scenery-kernel"} {
		if entry, err := os.Lstat(filepath.Join(destination, name)); !info.IsDir() || err != nil || !entry.Mode().IsRegular() {
			return false, preconditionErrorf("bundle destination %s exists and is not an application bundle", destination)
		}
	}
	return true, nil
}

func copyWorkerExecutable(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o755)
}
