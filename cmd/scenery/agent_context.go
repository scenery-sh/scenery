package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/appconfig"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

type agentGraph struct {
	*compiler.Result
	resources map[string]graph.Resource
}

func loadAgentGraph(ctx context.Context, root string) (agentGraph, error) {
	result, err := compiler.CompileContext(ctx, root)
	if err != nil {
		return agentGraph{}, err
	}
	if !result.Valid() {
		return agentGraph{}, fmt.Errorf("failed_precondition: %s", firstCompilerDiagnostic(result.Diagnostics))
	}
	resources := map[string]graph.Resource{}
	for _, resource := range inspectProjectionResources(result) {
		resources[resource.Address] = resource
	}
	return agentGraph{result, resources}, nil
}

func (g agentGraph) ref(resource graph.Resource, field, kind string) graph.Resource {
	return g.resources[workerReference(resource, resource.Spec[field], kind)]
}

type agentModule struct {
	Name      string `json:"module"`
	Directory string `json:"directory"`
	Runtime   string `json:"runtime"`
}

func (g agentGraph) modules() ([]agentModule, error) {
	roots := map[string]string{"app": "."}
	for _, resource := range g.Manifest.Resources {
		if resource.Kind == "scenery.module" {
			roots[cp.ModuleInstancePath(resource)] = firstNonEmpty(inspectString(resource.Spec["workspace_package_root"]), inspectString(resource.Spec["source"]))
		}
	}
	modules := []agentModule{}
	seen := map[string]string{}
	for _, resource := range g.Manifest.Resources {
		if resource.Kind != "scenery.service" {
			continue
		}
		runtime := firstNonEmpty(inspectString(resource.Spec["runtime"]), "go")
		if previous, ok := seen[resource.Module]; ok {
			if previous != runtime {
				return nil, fmt.Errorf("failed_precondition: module %s declares multiple service runtimes", resource.Module)
			}
			continue
		}
		seen[resource.Module] = runtime
		dir := roots[resource.Module]
		if dir == "" {
			for _, source := range g.Sources {
				if source.ID == resource.Origin.SourceID {
					dir = filepath.Dir(source.Relative)
					break
				}
			}
		}
		modules = append(modules, agentModule{resource.Module, filepath.Clean(dir), runtime})
	}
	slices.SortFunc(modules, func(a, b agentModule) int { return strings.Compare(a.Name, b.Name) })
	return modules, nil
}

type agentResource struct {
	Address string         `json:"address"`
	Kind    string         `json:"kind"`
	Spec    map[string]any `json:"spec"`
}

func agentResourceBrief(resource graph.Resource) agentResource {
	return agentResource{resource.Address, resource.Kind, resource.Spec}
}

type agentRoute struct {
	Binding        string `json:"binding"`
	Operation      string `json:"operation"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	Authentication string `json:"authentication"`
	Authorization  string `json:"authorization"`
}

func (g agentGraph) route(binding graph.Resource) agentRoute {
	spec, _ := binding.Spec["http"].(map[string]any)
	gateway := g.ref(binding, "gateway", "http_gateway")
	return agentRoute{binding.Address, g.ref(binding, "operation", "operation").Address,
		strings.ToUpper(inspectString(spec["method"])), inspectJoinPath(inspectString(gateway.Spec["base_path"]), inspectString(spec["path"])),
		inspectReference(binding.Spec["authentication"]), inspectReference(binding.Spec["authorization"])}
}

func runAgentContext(ctx context.Context, out io.Writer, args []string) error {
	flags := newCLIFlagSet("context")
	var root, target string
	var jsonOutput bool
	flags.StringVar(&root, "app-root", "", "")
	flags.StringVar(&target, "for", "", "")
	registerJSONOutput(flags, &jsonOutput)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if target == "" || len(positionals) != 0 {
		return usageErrorf("usage: scenery context --for <module|service|route|file>")
	}
	root, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		return err
	}
	g, err := loadAgentGraph(ctx, root)
	if err != nil {
		return err
	}
	modules, err := g.modules()
	if err != nil {
		return err
	}
	selected, err := g.contextModule(target, modules)
	if err != nil {
		return err
	}
	data, err := g.contextData(selected, configFrameworkOptions(cfg))
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeCLIJSON(out, data)
	}
	return printAgentContext(out, data, selected)
}

func printAgentContext(out io.Writer, data map[string]any, module agentModule) error {
	var briefing strings.Builder
	fmt.Fprintf(&briefing, "%s (%s) in %s\nSources: %s\nContracts: %s\n", module.Name, module.Runtime, data["directory"],
		strings.Join(data["sources"].([]string), ", "), data["generated_contract_root"])
	for _, operation := range data["operations"].([]agentResource) {
		fmt.Fprintf(&briefing, "Operation %s (%s)\n", operation.Address, inspectTypeExpression(operation.Spec["input"]))
	}
	for _, route := range data["routes"].([]agentRoute) {
		fmt.Fprintf(&briefing, "%s %s -> %s\n", route.Method, route.Path, route.Operation)
	}
	for _, key := range []string{"internal_bindings", "durable_executions", "schedules"} {
		for _, resource := range data[key].([]agentResource) {
			fmt.Fprintln(&briefing, resource.Address)
		}
	}
	fmt.Fprintf(&briefing, "Commands:\n  %s\n", strings.Join(data["commands"].([]string), "\n  "))
	_, err := io.WriteString(out, briefing.String())
	return err
}

func matchesModuleSelector(module agentModule, target string) bool {
	return target == module.Name || target != "" && filepath.IsLocal(target) && filepath.Clean(target) == module.Directory
}

func (g agentGraph) contextModule(target string, modules []agentModule) (agentModule, error) {
	file := target
	if filepath.IsAbs(file) {
		file, _ = filepath.Rel(g.Root, file)
	}
	resourceModules := map[string]bool{}
	for _, resource := range g.Manifest.Resources {
		switch resource.Kind {
		case "scenery.service":
			if target == resource.Address || target == resource.Name {
				resourceModules[resource.Module] = true
			}
		case "scenery.binding":
			if inspectString(resource.Spec["protocol"]) != "http" {
				continue
			}
			route := g.route(resource)
			if target == route.Path || target == route.Method+" "+route.Path {
				resourceModules[resource.Module] = true
			}
		}
	}
	var matches []agentModule
	for _, module := range modules {
		if matchesModuleSelector(module, target) || target == "module."+module.Name || graph.ModuleResourceAddress(module.Name) == target || resourceModules[module.Name] {
			matches = append(matches, module)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		if _, err := fs.Stat(os.DirFS(g.Root), filepath.ToSlash(filepath.Clean(file))); err == nil {
			best := -1
			for _, module := range modules {
				if !pathUnder(module.Directory, file) || len(module.Directory) < best {
					continue
				}
				if len(module.Directory) > best {
					matches, best = nil, len(module.Directory)
				}
				matches = append(matches, module)
			}
		}
	}
	if len(matches) != 1 {
		return agentModule{}, usageErrorf("target %q must identify exactly one module (matched %d)", target, len(matches))
	}
	return matches[0], nil
}

func (g agentGraph) contextData(module agentModule, options appconfig.FrameworkOptions) (map[string]any, error) {
	generated, err := compiler.ManagedGeneratedRoots(g.Result)
	if err != nil {
		return nil, err
	}
	sources := []string{}
	scope := workerInputScope{root: g.Root, generated: generated}
	exts := map[string][]string{"go": {".go"}, "rust": {".rs"}, "ts": {".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs"}, "js": {".js", ".jsx", ".mjs", ".cjs"}, "python": {".py", ".pyi"}}
	if err := filepath.WalkDir(filepath.Join(g.Root, module.Directory), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if scope.skip(path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() && (filepath.Ext(path) == ".scn" || slices.Contains(exts[module.Runtime], filepath.Ext(path))) {
			rel, _ := filepath.Rel(g.Root, path)
			sources = append(sources, rel)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	services, operations, bindings := []agentResource{}, []agentResource{}, []agentResource{}
	durable, schedules, policies, types := []agentResource{}, []agentResource{}, []agentResource{}, []agentResource{}
	routes := []agentRoute{}
	clients := []map[string]string{}
	auth := map[string]bool{}
	commands := []string{"scenery test " + module.Name + " -o json", "scenery check -o json"}
	for _, resource := range g.Manifest.Resources {
		if resource.Module != module.Name {
			continue
		}
		owned[resource.Address] = true
		switch resource.Kind {
		case "scenery.service":
			services = append(services, agentResourceBrief(resource))
			for _, client := range cp.NamedChildren(resource.Spec, "client") {
				binding := g.resources[workerReference(resource, client["binding"], "binding")]
				operation := g.ref(binding, "operation", "operation")
				service := g.ref(operation, "service", "service")
				clients = append(clients, map[string]string{"service": resource.Address, "name": inspectString(client["name"]),
					"binding": binding.Address, "callee": service.Address, "runtime": firstNonEmpty(inspectString(service.Spec["runtime"]), "go")})
			}
		case "scenery.operation":
			operations = append(operations, agentResourceBrief(resource))
		case "scenery.binding":
			for _, key := range []string{"authentication", "authorization"} {
				auth[workerReference(resource, resource.Spec[key], key)] = true
			}
			switch inspectString(resource.Spec["protocol"]) {
			case "http":
				routes = append(routes, g.route(resource))
				commands = append(commands, "scenery call "+resource.Address+" -o json")
			case "internal":
				bindings = append(bindings, agentResourceBrief(resource))
				commands = append(commands, "scenery call "+resource.Address+" -o json")
			}
		case "scenery.execution":
			if resource.Spec["mode"] == "durable" {
				durable = append(durable, agentResourceBrief(resource))
			}
		case "scenery.record", "scenery.alias", "scenery.enum", "scenery.union", "scenery.scalar":
			types = append(types, agentResourceBrief(resource))
		case "scenery.schedule":
			schedules = append(schedules, agentResourceBrief(resource))
		}
	}
	for _, resource := range inspectProjectionResources(g.Result) {
		if auth[resource.Address] {
			policies = append(policies, agentResourceBrief(resource))
		}
	}
	sql := []map[string]any{}
	for _, requirement := range g.SQLRequirements {
		if slices.ContainsFunc(requirement.Consumers, func(address string) bool { return owned[address] }) {
			sql = append(sql, map[string]any{"kind": requirement.Kind, "address": requirement.Address,
				"schema": requirement.Schema, "name": requirement.Name, "provider": requirement.Provider})
		}
	}
	catalog, err := appconfig.BuildCatalog(g.Manifest, options)
	if err != nil {
		return nil, err
	}
	inputs := []string{}
	for _, input := range catalog.Inputs {
		if slices.ContainsFunc(input.Consumers, func(consumer appconfig.Consumer) bool { return owned[consumer.Service] }) {
			inputs = append(inputs, input.Key)
		}
	}
	return withCLIPayloadIdentity("scenery.context", map[string]any{
		"module": module.Name, "directory": filepath.Join(g.Root, module.Directory), "runtime": module.Runtime,
		"sources": sources, "generated_contract_root": filepath.Join(g.Root, module.Directory, "scenerycontract"),
		"services": services, "operations": operations, "types": types, "routes": routes, "internal_bindings": bindings, "durable_executions": durable, "schedules": schedules,
		"dependencies": map[string]any{"clients": clients, "sql": sql, "config_inputs": inputs, "auth_policies": policies},
		"commands":     commands,
	}), nil
}
