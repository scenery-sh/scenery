package generate

import (
	"encoding/json"
	"fmt"
	"go/format"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/scn"
)

type applicationAdapter struct {
	Address string
	// ContractRevision is the service contract revision the adapter records.
	ContractRevision string
	ImportPath       string
	PackageName      string
	RelativeDir      string
	Covered          []string
	PackageABI       string
	Implementation   string
	Contract         string
	Source           []byte
}

type RuntimeIntegrationPlan = generateapi.RuntimeIntegrationPlan

func goRuntimeServices(resources []Resource) []Resource {
	return slices.DeleteFunc(compiler.RuntimeServices(resources), func(service Resource) bool {
		return compiler.IsExternalWorkerRuntime(stringValue(service.Spec["runtime"]))
	})
}

// BuildRuntimeIntegrationPlan plans one Worker ABI v1 process per native Go
// service. An application without one has no Go worker to link.
func BuildRuntimeIntegrationPlan(result *Result) (RuntimeIntegrationPlan, error) {
	plan := RuntimeIntegrationPlan{ContractRevision: result.Manifest.ContractRevision}
	if len(goRuntimeServices(result.Manifest.Resources)) == 0 {
		return plan, nil
	}
	_, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		return RuntimeIntegrationPlan{}, err
	}
	adapters, err := planApplicationAdapters(result, generatedImport)
	if err != nil {
		return RuntimeIntegrationPlan{}, err
	}
	resources := resourcesByAddress(&Manifest{Resources: result.Manifest.Resources})
	for _, adapter := range adapters {
		plan.Services = append(plan.Services, generateapi.ServiceProcessPlan{
			Address: adapter.Address, Name: strings.TrimSuffix(adapter.RelativeDir, "_adapter"),
			ContractRevision: compiler.ServiceContractRevision(result.Manifest, resources[adapter.Address], adapter.Covered),
			AdapterImport:    adapter.ImportPath, RequiredAddresses: append([]string(nil), adapter.Covered...),
		})
	}
	return plan, nil
}

func generateApplicationArtifacts(result *Result, idx *resourceIndex, input projectionInput) ([]generatedFile, error) {
	if len(goRuntimeServices(result.Manifest.Resources)) == 0 {
		return nil, nil
	}
	generatedRoot, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		return nil, err
	}
	adapters, err := cachedApplicationAdapters(input, generatedImport, func() ([]applicationAdapter, error) {
		return renderApplicationAdapters(result, idx, generatedImport)
	})
	if err != nil {
		return nil, err
	}
	var files []generatedFile
	covered := map[string]bool{}
	packageABIs := map[string]string{}
	for _, adapter := range adapters {
		files = append(files, generatedFile{Path: filepath.Join(generatedRoot, filepath.FromSlash(adapter.RelativeDir), "adapter.gen.go"), Bytes: adapter.Source})
		for _, address := range adapter.Covered {
			covered[address] = true
		}
		packageABIs[adapter.Contract] = adapter.PackageABI
	}
	descriptor := addGeneratedArtifactIdentity(map[string]any{
		"artifact_kind":     "go_application_adapters",
		"contract_revision": result.Manifest.ContractRevision, "covered": sortedBoolKeys(covered),
		"implementation_revision":        result.ImplementationRevisions,
		"package_contract_abi_revisions": packageABIs, "runtime_abi": "scenery.go-runtime/v1", "runtime_abi_range": "scenery.go-runtime/v1",
		"provider_capability_abis": providerABIRanges(result.Manifest.Resources), "generator": "scenery.generate.go-application",
		"http_surface_revisions": result.HTTPSurfaceRevisions, "openapi_revisions": result.OpenAPIRevisions,
		"content_digest": artifactDigest(generatedRoot, files), "files": generatedFilePaths(generatedRoot, files),
	}, goApplicationDescriptorKind, goApplicationSchemaDescriptor, result.Manifest.SpecRevision)
	descriptorBytes, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, err
	}
	files = append(files, generatedFile{Path: filepath.Join(generatedRoot, "scenery.generated.json"), Bytes: append(descriptorBytes, '\n')})
	return files, nil
}

func providerABIRanges(resources []Resource) map[string]any {
	ranges := map[string]any{}
	for _, resource := range resources {
		if resource.Kind != "scenery.provider" {
			continue
		}
		source := stringValue(resource.Spec["source"])
		if source == "" {
			continue
		}
		ranges[source] = map[string]any{
			"runtime": resource.Spec["runtime_abi"], "deployment": resource.Spec["deployment_abi"],
			"migration": resource.Spec["migration_abi"], "compile_descriptor_digest": resource.Spec["compile_descriptor_digest"],
		}
	}
	return ranges
}

func providerRuntimeABIs(resources []Resource) map[string]string {
	abis := map[string]string{}
	for _, resource := range resources {
		if resource.Kind != "scenery.provider" {
			continue
		}
		source, runtimeABI := stringValue(resource.Spec["source"]), stringValue(resource.Spec["runtime_abi"])
		if source == "" || runtimeABI == "" {
			continue
		}
		abis[source] = runtimeABI
	}
	return abis
}

func resolveApplicationGeneratedRoot(result *Result) (string, string, error) {
	relativeRoot := "internal/scenerygen"
	for _, source := range result.Sources {
		if source.Relative != scn.AppFilename {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type != "workspace" {
				continue
			}
			for _, candidate := range literalStringList(block, "managed_generated_roots") {
				candidate = filepath.ToSlash(filepath.Clean(candidate))
				if candidate == "internal/scenerygen" || strings.HasSuffix(candidate, "/internal/scenerygen") {
					relativeRoot = candidate
				}
			}
		}
	}
	absRoot := filepath.Join(result.Root, filepath.FromSlash(relativeRoot))
	type moduleMapping struct{ root, importPath string }
	var mappings []moduleMapping
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.go-module" {
			continue
		}
		rootPath, _ := resource.Spec["root"].(string)
		importPath, _ := resource.Spec["import_path"].(string)
		if rootPath == "" || importPath == "" {
			continue
		}
		mappings = append(mappings, moduleMapping{root: filepath.Clean(filepath.Join(result.Root, filepath.FromSlash(rootPath))), importPath: strings.TrimSuffix(importPath, "/")})
	}
	sort.Slice(mappings, func(i, j int) bool { return len(mappings[i].root) > len(mappings[j].root) })
	for _, mapping := range mappings {
		relative, err := filepath.Rel(mapping.root, absRoot)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		importPath := mapping.importPath
		if relative != "." {
			importPath += "/" + filepath.ToSlash(relative)
		}
		return absRoot, importPath, nil
	}
	return "", "", fmt.Errorf("native application adapters require a go_module mapping for %s", relativeRoot)
}

// applicationAdapterPlan is the rendering-independent identity of one native
// service adapter: its generated import and the contract resources it covers.
type applicationAdapterPlan struct {
	Address              string
	ImportPath           string
	PackageName          string
	RelativeDir          string
	Covered              []string
	PackageIdentity      string
	ImplementationImport string
	ContractImport       string
	Operations           []Resource
	Bindings             []Resource
	MCPBindings          []mcpToolTarget
}

// planApplicationAdapter plans one service's adapter; byAddress indexes the
// result's resources.
func planApplicationAdapter(result *Result, byAddress map[string]Resource, module, service Resource, generatedImport string) (applicationAdapterPlan, error) {
	moduleSource, _ := module.Spec["workspace_package_root"].(string)
	if moduleSource == "" {
		moduleSource, _ = module.Spec["source"].(string)
	}
	packageBlock := findPackageBlock(result.Sources, moduleSource)
	implementationImport := ""
	packageIdentity := module.Name
	if packageBlock != nil {
		if len(packageBlock.Labels) > 0 && strings.TrimSpace(packageBlock.Labels[0]) != "" {
			packageIdentity = packageBlock.Labels[0]
		}
		for _, child := range packageBlock.Blocks {
			if child.Type == "go_contract" {
				implementationImport, _ = literalString(child, "import_path")
			}
		}
	}
	if implementationImport == "" {
		return applicationAdapterPlan{}, fmt.Errorf("native service %s has no go_contract import path", service.Address)
	}
	resources := result.Manifest.Resources
	operations := compiler.ServiceOperations(resources, service)
	bindings := serviceHTTPBindings(resources, operations)
	internalBindings := internalBindingsForOperations(resources, operations)
	mcpBindings := mcpBindingsForService(resources, byAddress, service, operations)
	mcpResources := mcpBindingResources(mcpBindings)
	covered := []string{service.Address}
	covered = append(covered, resourceAddresses(operations)...)
	covered = append(covered, resourceAddresses(bindings)...)
	covered = append(covered, resourceAddresses(internalBindings)...)
	covered = append(covered, resourceAddresses(mcpResources)...)
	allBindings := append(append(append([]Resource(nil), bindings...), internalBindings...), mcpResources...)
	covered = append(covered, resourceAddresses(schedulesForOperations(resources, operations))...)
	covered = append(covered, pageOwnedResourceAddresses(resources, byAddress, operations)...)
	covered = append(covered, referencedExecutions(byAddress, allBindings)...)
	dirName := semanticPathName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
	return applicationAdapterPlan{
		Address: service.Address, ImportPath: generatedImport + "/" + dirName,
		PackageName: goPackageName(moduleInstancePath(module) + "_" + service.Name + "_adapter"), RelativeDir: dirName,
		Covered: canonicalStrings(covered), PackageIdentity: packageIdentity, ImplementationImport: implementationImport,
		ContractImport: implementationImport + "/scenerycontract", Operations: operations, Bindings: bindings, MCPBindings: mcpBindings,
	}, nil
}

func renderApplicationAdapter(result *Result, idx *resourceIndex, module, service Resource, generatedImport string) (applicationAdapter, error) {
	plan, err := planApplicationAdapter(result, idx.byAddress, module, service, generatedImport)
	if err != nil {
		return applicationAdapter{}, err
	}
	moduleResources := idx.moduleResources(moduleInstancePath(module))
	packageABI, err := packageABIRevision(plan.ImplementationImport, moduleResources, idx)
	if err != nil {
		return applicationAdapter{}, err
	}
	adapter := applicationAdapter{
		Address: plan.Address, ContractRevision: compiler.ServiceContractRevision(result.Manifest, service, plan.Covered),
		ImportPath: plan.ImportPath, PackageName: plan.PackageName, RelativeDir: plan.RelativeDir,
		Covered: plan.Covered, PackageABI: packageABI, Implementation: plan.ImplementationImport, Contract: plan.ContractImport,
	}
	adapter.Source, err = renderApplicationAdapterSource(adapter.ContractRevision, plan.PackageIdentity, packageABI, plan.ImplementationImport, plan.ContractImport, plan.PackageName, service, plan.Operations, plan.Bindings, plan.MCPBindings, result.Manifest.Resources, idx, plan.Covered, providerRuntimeABIs(result.Manifest.Resources))
	if err != nil {
		return applicationAdapter{}, err
	}
	return adapter, nil
}

func renderApplicationAdapterSource(contractRevision, packageIdentity, packageABI, implementationImport, contractImport, packageName string, service Resource, operations, bindings []Resource, mcpBindings []mcpToolTarget, resources []Resource, idx *resourceIndex, covered []string, providerABIs map[string]string) ([]byte, error) {
	implementation, _ := service.Spec["implementation"].(map[string]any)
	constructor, _ := implementation["constructor"].(string)
	if constructor == "" {
		return nil, fmt.Errorf("native service %s has no constructor", service.Address)
	}
	dependencies, err := compiler.ServiceGoDependencies(idx.byAddress, service)
	if err != nil {
		return nil, err
	}
	clients, err := serviceGoClients(idx, service)
	if err != nil {
		return nil, err
	}
	serviceName := goName(service.Name)
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage " + packageName + "\n\n")
	b.WriteString("import (\n\t\"context\"\n\t\"fmt\"\n")
	fmt.Fprintf(&b, "\tscenery %q\n", "scenery.sh")
	fmt.Fprintf(&b, "\tsceneryruntime %q\n", "scenery.sh/runtime")
	dependencyImports := map[string]bool{}
	for _, dependency := range dependencies {
		if dependencyImports[dependency.Resolver] {
			continue
		}
		dependencyImports[dependency.Resolver] = true
		if dependency.Resolver == "sql" {
			fmt.Fprintf(&b, "\tscenerydb %q\n", "scenery.sh/db")
		}
	}
	fmt.Fprintf(&b, "\timplementation %q\n", implementationImport)
	fmt.Fprintf(&b, "\tcontract %q\n", contractImport)
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "const ContractRevision = %q\nconst PackageIdentity = %q\nconst PackageContractABIRevision = %q\n\n", contractRevision, packageIdentity, packageABI)
	fmt.Fprintf(&b, "// ContractRevisions names the contract revision of each registration.\nvar ContractRevisions = map[string]string{%q: ContractRevision}\n\n", service.Address+"/adapter")
	b.WriteString("type serviceImplementation interface {\n")
	for _, operation := range operations {
		handler, _ := operation.Spec["handler"].(map[string]any)
		method, _ := handler["method"].(string)
		if operationUsesHTTPStream(operation, bindings) {
			fmt.Fprintf(&b, "\t%s(context.Context, contract.%sInput) (contract.%sOutcome, scenery.ByteStream, error)\n", method, goName(operation.Name), goName(operation.Name))
		} else {
			fmt.Fprintf(&b, "\t%s(context.Context, contract.%sInput) (contract.%sOutcome, error)\n", method, goName(operation.Name), goName(operation.Name))
		}
	}
	lifecycle, _ := service.Spec["lifecycle"].(map[string]any)
	if method := stringValue(lifecycle["start"]); method != "" {
		fmt.Fprintf(&b, "\t%s(context.Context) error\n", method)
	}
	if method := stringValue(lifecycle["stop"]); method != "" {
		fmt.Fprintf(&b, "\t%s(context.Context) error\n", method)
	}
	b.WriteString("}\n\ntype serviceAdapter struct { native serviceImplementation }\n\nvar service *serviceAdapter\n\n")
	if err := renderServiceOperationAdapters(&b, operations, bindings); err != nil {
		return nil, err
	}
	for _, client := range clients {
		renderWireGoClient(&b, client)
	}
	b.WriteString("func Register(registry scenery.Registry) error {\n")
	fmt.Fprintf(&b, "\treturn registry.Register(%q, sceneryruntime.ContractRegistration{\n", service.Address+"/adapter")
	fmt.Fprintf(&b, "\t\tContractRevision: ContractRevision, PackageContractABIRevision: PackageContractABIRevision, RuntimeABI: sceneryruntime.ContractRuntimeABI,\n\t\tProviderABIs: %s, CoveredAddresses: %#v,\n", goStringStringMap(providerABIs), covered)
	b.WriteString("\t\tApply: func() error {\n")
	fmt.Fprintf(&b, "\t\t\tif contract.PackageIdentity != PackageIdentity { return fmt.Errorf(\"package identity mismatch\") }\n")
	fmt.Fprintf(&b, "\t\t\tif contract.PackageContractABIRevision != PackageContractABIRevision { return fmt.Errorf(\"package contract ABI mismatch\") }\n")
	fmt.Fprintf(&b, "\t\t\tif err := sceneryruntime.RegisterNativeService(sceneryruntime.NativeServiceRegistration{Address: %q, Initialize: func(ctx context.Context) error {\n", service.Address)
	fmt.Fprintf(&b, "\t\t\t\tinput := contract.%sConstructorInput{}\n", serviceName)
	for index, dependency := range dependencies {
		variable := fmt.Sprintf("dependency%d", index)
		switch dependency.Resolver {
		case "sql":
			fmt.Fprintf(&b, "\t\t\t\t%s, err := scenerydb.Get(ctx, %q); if err != nil { return fmt.Errorf(\"resolve dependency %s: %%w\", err) }; input.Dependencies.%s = %s\n", variable, dependency.RuntimeName, dependency.Name, dependency.Field, variable)
		case "object":
			fmt.Fprintf(&b, "\t\t\t\tinput.Dependencies.%s = sceneryruntime.WorkerStorage(%q)\n", dependency.Field, dependency.Name)
		}
	}
	config, _ := service.Spec["config"].(map[string]any)
	for _, field := range namedChildren(service.Spec, "config_schema") {
		name, typeExpression := stringValue(field["name"]), stringValue(field["type"])
		value, exists := config[name]
		// Environment-configurable deployment inputs come from the process's
		// runtime configuration snapshot, so a configured value never changes
		// generated code or the executable.
		if compiler.ConfigurableDeploymentInput(stringValue(field["phase"]), typeExpression, field["sensitive"] == true) && stringValue(field["input"]) != "" {
			key := compiler.ConfigurationKey(service.Module, stringValue(field["input"]))
			if typeExpression == `resource_ref("secret")` {
				if !exists {
					fmt.Fprintf(&b, "\t\t\t\tinput.Config.%s = sceneryruntime.ConfigSecretRef(%q)\n", goName(name), key)
					continue
				}
			} else {
				optional := field["optional"] == true || strings.HasPrefix(strings.TrimSpace(typeExpression), "optional(")
				fmt.Fprintf(&b, "\t\t\t\tif err := sceneryruntime.ResolveDeploymentConfig(%q, &input.Config.%s, %q, %t); err != nil { return fmt.Errorf(\"resolve config %s: %%w\", err) }\n", key, goName(name), typeExpression, optional, name)
				continue
			}
		}
		if !exists {
			return nil, fmt.Errorf("native service %s config %s has no resolved value", service.Address, name)
		}
		if typeExpression == `resource_ref("secret")` {
			address := resolveResourceRef(service, refString(value), "secret")
			if address == "" {
				return nil, fmt.Errorf("native service %s sensitive config %s requires a secret reference", service.Address, name)
			}
			fmt.Fprintf(&b, "\t\t\t\tinput.Config.%s = scenery.SecretRef{Address: %q}\n", goName(name), address)
			continue
		}
		wire, err := goConfigWireJSON(value, typeExpression)
		if err != nil {
			return nil, fmt.Errorf("native service %s config %s: %w", service.Address, name, err)
		}
		fmt.Fprintf(&b, "\t\t\t\tif err := scenery.UnmarshalContractValue([]byte(%q), &input.Config.%s, %q); err != nil { return fmt.Errorf(\"resolve config %s: %%w\", err) }\n", string(wire), goName(name), typeExpression, name)
	}
	for _, client := range clients {
		fmt.Fprintf(&b, "\t\t\t\tinput.Clients.%s = %s{}\n", client.Field, semanticPathName(client.Name)+"InternalClient")
	}
	fmt.Fprintf(&b, "\t\t\t\tvalue, err := implementation.%s(ctx, input); if err != nil { return err }; if value == nil { return fmt.Errorf(\"constructor returned nil service\") }; service = &serviceAdapter{native: value}\n", constructor)
	if method := stringValue(lifecycle["start"]); method != "" {
		fmt.Fprintf(&b, "\t\t\t\tif err := service.native.%s(ctx); err != nil { service = nil; return err }\n", method)
	}
	b.WriteString("\t\t\t\treturn nil\n\t\t\t}")
	if method := stringValue(lifecycle["stop"]); method != "" {
		fmt.Fprintf(&b, ", Shutdown: func(ctx context.Context) error { if service == nil || service.native == nil { return nil }; return service.native.%s(ctx) }", method)
	}
	b.WriteString("}); err != nil { return err }\n")
	renderWorkerOperations(&b, operations, bindings)
	b.WriteString("\t\t\treturn nil\n\t\t},\n\t})\n}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format application adapter for %s: %w\n%s", service.Address, err, b.String())
	}
	return formatted, nil
}

func serviceHTTPBindings(resources, operations []Resource) []Resource {
	owned := map[string]bool{}
	for _, operation := range operations {
		owned[operation.Address] = true
	}
	var bindings []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.binding" || resource.Spec["protocol"] != "http" {
			continue
		}
		address := resolveResourceRef(resource, refString(resource.Spec["operation"]), "operation")
		if owned[address] {
			bindings = append(bindings, resource)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func referencedExecutions(known map[string]Resource, bindings []Resource) []string {
	set := map[string]bool{}
	for _, binding := range bindings {
		address := resolveResourceRef(binding, refString(binding.Spec["execution"]), "execution")
		if known[address].Address != "" {
			set[address] = true
		}
	}
	return sortedBoolKeys(set)
}

func semanticPathName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			b.WriteRune(char)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func goPackageName(value string) string {
	return strings.ReplaceAll(semanticPathName(value), "_", "")
}

func goConfigWireJSON(value any, typeExpression string) ([]byte, error) {
	return compiler.ConfigWireJSON(value, typeExpression)
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
