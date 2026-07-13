package generate

import (
	"encoding/json"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	scenery "scenery.sh"
)

type applicationAdapter struct {
	Address        string
	ImportPath     string
	PackageName    string
	RelativeDir    string
	Covered        []string
	PackageABI     string
	Implementation string
	Contract       string
	Source         []byte
}

type RuntimeIntegrationPlan struct {
	CompositionImport string
}

func BuildRuntimeIntegrationPlan(result *Result) (RuntimeIntegrationPlan, error) {
	services := nativeApplicationServices(result)
	if len(services) == 0 {
		return RuntimeIntegrationPlan{}, nil
	}
	_, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		return RuntimeIntegrationPlan{}, err
	}
	return RuntimeIntegrationPlan{CompositionImport: generatedImport + "/composition"}, nil
}

func generateApplicationArtifacts(result *Result) ([]generatedFile, error) {
	services := nativeApplicationServices(result)
	if len(services) == 0 {
		return nil, nil
	}
	generatedRoot, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		return nil, err
	}
	modules := map[string]Resource{}
	for _, module := range localModuleInstances(result.Manifest.Resources) {
		modules[moduleInstancePath(module)] = module
	}
	var adapters []applicationAdapter
	for _, service := range services {
		module, ok := modules[service.Module]
		if !ok {
			return nil, fmt.Errorf("native service %s is not owned by a local module", service.Address)
		}
		adapter, err := renderApplicationAdapter(result, module, service, generatedImport)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, adapter)
	}
	sort.Slice(adapters, func(i, j int) bool { return adapters[i].Address < adapters[j].Address })
	var files []generatedFile
	for _, adapter := range adapters {
		files = append(files, generatedFile{Path: filepath.Join(generatedRoot, filepath.FromSlash(adapter.RelativeDir), "adapter.gen.go"), Bytes: adapter.Source})
	}
	composition, err := renderApplicationComposition(result.Manifest.ContractRevision, providerRuntimeABIs(result.Manifest.Resources), adapters)
	if err != nil {
		return nil, err
	}
	files = append(files, generatedFile{Path: filepath.Join(generatedRoot, "composition", "composition.gen.go"), Bytes: composition})
	covered := map[string]bool{}
	packageABIs := map[string]string{}
	for _, adapter := range adapters {
		for _, address := range adapter.Covered {
			covered[address] = true
		}
		packageABIs[adapter.Contract] = adapter.PackageABI
	}
	coveredAddresses := sortedBoolKeys(covered)
	descriptor := addGeneratedArtifactIdentity(map[string]any{
		"artifact_kind":     "go_application_adapters",
		"contract_revision": result.Manifest.ContractRevision, "covered": coveredAddresses,
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

func nativeApplicationServices(result *Result) []Resource {
	var services []Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.service" || resource.Origin.Kind != "authored" && !isProviderCRUDService(resource) {
			continue
		}
		implementation, _ := resource.Spec["implementation"].(map[string]any)
		if implementation == nil {
			continue
		}
		operations := serviceOperations(result.Manifest.Resources, resource)
		hasHandler := false
		for _, operation := range operations {
			handler, _ := operation.Spec["handler"].(map[string]any)
			if handler != nil {
				hasHandler = true
			}
		}
		if !hasHandler {
			continue
		}
		services = append(services, resource)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Address < services[j].Address })
	return services
}

func isProviderCRUDService(service Resource) bool {
	if service.Kind != "scenery.service" || stringValue(service.Spec["runtime"]) != "provider" {
		return false
	}
	implementation, _ := service.Spec["implementation"].(map[string]any)
	return stringValue(implementation["adapter"]) == "provider_crud_v1"
}

func resolveApplicationGeneratedRoot(result *Result) (string, string, error) {
	relativeRoot := "internal/scenerygen"
	for _, source := range result.Sources {
		if source.Relative != "scenery.scn" {
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

func renderApplicationAdapter(result *Result, module, service Resource, generatedImport string) (applicationAdapter, error) {
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
		return applicationAdapter{}, fmt.Errorf("native service %s has no go_contract import path", service.Address)
	}
	moduleResources := moduleResources(result.Manifest.Resources, moduleInstancePath(module))
	packageABI, err := packageABIRevision(implementationImport, moduleResources, result.Manifest.Resources)
	if err != nil {
		return applicationAdapter{}, err
	}
	operations := serviceOperations(result.Manifest.Resources, service)
	if isProviderCRUDService(service) {
		bindings := serviceHTTPBindings(result.Manifest.Resources, operations)
		internalBindings := internalBindingsForOperations(result.Manifest.Resources, operations)
		covered := []string{service.Address}
		covered = append(covered, resourceAddresses(operations)...)
		covered = append(covered, resourceAddresses(bindings)...)
		covered = append(covered, resourceAddresses(internalBindings)...)
		covered = append(covered, pageOwnedResourceAddresses(result.Manifest.Resources, operations)...)
		allBindings := append(append([]Resource(nil), bindings...), internalBindings...)
		covered = append(covered, referencedExecutions(result.Manifest.Resources, allBindings)...)
		covered = canonicalStrings(covered)
		dirName := semanticPathName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
		packageName := goPackageName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
		contractImport := implementationImport + "/scenerycontract"
		adapterImport := generatedImport + "/" + dirName
		source, renderErr := renderProviderCRUDAdapterSource(result.Manifest.ContractRevision, packageIdentity, packageABI, contractImport, packageName, service, operations, bindings, result.Manifest.Resources, covered, providerRuntimeABIs(result.Manifest.Resources))
		if renderErr != nil {
			return applicationAdapter{}, renderErr
		}
		return applicationAdapter{Address: service.Address, ImportPath: adapterImport, PackageName: packageName, RelativeDir: dirName, Covered: covered, PackageABI: packageABI, Implementation: "scenery.sh/datasource", Contract: contractImport, Source: source}, nil
	}
	bindings := serviceHTTPBindings(result.Manifest.Resources, operations)
	internalBindings := internalBindingsForOperations(result.Manifest.Resources, operations)
	eventBindings := eventBindingsForOperations(result.Manifest.Resources, operations)
	schedules := schedulesForOperations(result.Manifest.Resources, operations)
	emissions := eventEmissionsForOperations(result.Manifest.Resources, operations)
	covered := []string{service.Address}
	covered = append(covered, resourceAddresses(operations)...)
	covered = append(covered, resourceAddresses(bindings)...)
	covered = append(covered, resourceAddresses(internalBindings)...)
	covered = append(covered, resourceAddresses(eventBindings)...)
	covered = append(covered, resourceAddresses(schedules)...)
	covered = append(covered, resourceAddresses(emissions)...)
	covered = append(covered, pageOwnedResourceAddresses(result.Manifest.Resources, operations)...)
	allBindings := append(append(append([]Resource(nil), bindings...), internalBindings...), eventBindings...)
	covered = append(covered, referencedExecutions(result.Manifest.Resources, allBindings)...)
	covered = canonicalStrings(covered)
	dirName := semanticPathName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
	packageName := goPackageName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
	contractImport := implementationImport + "/scenerycontract"
	adapterImport := generatedImport + "/" + dirName
	source, err := renderApplicationAdapterSource(result.Manifest.ContractRevision, packageIdentity, packageABI, implementationImport, contractImport, packageName, service, operations, bindings, result.Manifest.Resources, covered, providerRuntimeABIs(result.Manifest.Resources))
	if err != nil {
		return applicationAdapter{}, err
	}
	return applicationAdapter{
		Address: service.Address, ImportPath: adapterImport, PackageName: packageName, RelativeDir: dirName,
		Covered: covered, PackageABI: packageABI, Implementation: implementationImport, Contract: contractImport, Source: source,
	}, nil
}

func renderApplicationAdapterSource(contractRevision, packageIdentity, packageABI, implementationImport, contractImport, packageName string, service Resource, operations, bindings, resources []Resource, covered []string, providerABIs map[string]string) ([]byte, error) {
	implementation, _ := service.Spec["implementation"].(map[string]any)
	constructor, _ := implementation["constructor"].(string)
	if constructor == "" {
		return nil, fmt.Errorf("native service %s has no constructor", service.Address)
	}
	dependencies, err := serviceGoDependencies(resources, service)
	if err != nil {
		return nil, err
	}
	clients, err := serviceGoClients(resources, service)
	if err != nil {
		return nil, err
	}
	serviceName := goName(service.Name)
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage " + packageName + "\n\n")
	b.WriteString("import (\n\t\"context\"\n\t\"fmt\"\n")
	if len(bindings) > 0 {
		b.WriteString("\t\"net/http\"\n")
	}
	fmt.Fprintf(&b, "\tscenery %q\n", "scenery.sh")
	fmt.Fprintf(&b, "\tsceneryruntime %q\n", "scenery.sh/runtime")
	dependencyImports := map[string]bool{}
	for _, dependency := range dependencies {
		if dependencyImports[dependency.Resolver] {
			continue
		}
		dependencyImports[dependency.Resolver] = true
		switch dependency.Resolver {
		case "sql":
			fmt.Fprintf(&b, "\tscenerydb %q\n", "scenery.sh/db")
		case "object":
			fmt.Fprintf(&b, "\tscenerystorage %q\n", "scenery.sh/storage")
		}
	}
	clientImports := map[string]string{}
	for _, client := range clients {
		if client.ContractAlias == "" {
			continue
		}
		if existing := clientImports[client.ContractAlias]; existing != "" && existing != client.ContractImport {
			return nil, fmt.Errorf("generated client import alias %s resolves to both %s and %s", client.ContractAlias, existing, client.ContractImport)
		}
		clientImports[client.ContractAlias] = client.ContractImport
	}
	clientAliases := make([]string, 0, len(clientImports))
	for alias := range clientImports {
		clientAliases = append(clientAliases, alias)
	}
	sort.Strings(clientAliases)
	for _, alias := range clientAliases {
		fmt.Fprintf(&b, "\t%s %q\n", alias, clientImports[alias])
	}
	fmt.Fprintf(&b, "\timplementation %q\n", implementationImport)
	fmt.Fprintf(&b, "\tcontract %q\n", contractImport)
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "const ContractRevision = %q\nconst PackageIdentity = %q\nconst PackageContractABIRevision = %q\n\n", contractRevision, packageIdentity, packageABI)
	b.WriteString("type serviceImplementation interface {\n")
	for _, operation := range operations {
		handler, _ := operation.Spec["handler"].(map[string]any)
		method, _ := handler["method"].(string)
		fmt.Fprintf(&b, "\t%s(context.Context, contract.%sInput) (contract.%sOutcome, error)\n", method, goName(operation.Name), goName(operation.Name))
	}
	lifecycle, _ := service.Spec["lifecycle"].(map[string]any)
	if method := stringValue(lifecycle["start"]); method != "" {
		fmt.Fprintf(&b, "\t%s(context.Context) error\n", method)
	}
	if method := stringValue(lifecycle["stop"]); method != "" {
		fmt.Fprintf(&b, "\t%s(context.Context) error\n", method)
	}
	b.WriteString("}\n\ntype serviceAdapter struct { native serviceImplementation }\n\nvar service *serviceAdapter\n\n")
	if err := renderServiceOperationAdapters(&b, operations); err != nil {
		return nil, err
	}
	for _, client := range clients {
		operationName := goName(client.Operation.Name)
		clientType := semanticPathName(client.Name) + "InternalClient"
		contractQualifier := "contract."
		if client.ContractAlias != "" {
			contractQualifier = client.ContractAlias + "."
		}
		fmt.Fprintf(&b, "type %s struct{}\n", clientType)
		if client.Delivery == "enqueue" {
			fmt.Fprintf(&b, "func (%s) Enqueue(ctx context.Context, invocation scenery.Invocation, input %s%sInput) (scenery.ExecutionReceipt, error) { copied, err := %sClone%sInput(input); if err != nil { return scenery.ExecutionReceipt{}, fmt.Errorf(\"copy internal client input: %%w\", err) }; value, err := sceneryruntime.InvokeContractBindingFrom(ctx, %q, %q, invocation, copied); if err != nil { return scenery.ExecutionReceipt{}, err }; typed, ok := value.(scenery.ExecutionReceipt); if !ok { return scenery.ExecutionReceipt{}, fmt.Errorf(\"binding returned %%T, want scenery.ExecutionReceipt\", value) }; return typed, nil }\n\n", clientType, contractQualifier, operationName, contractQualifier, operationName, client.Binding.Address, service.Module)
		} else {
			fmt.Fprintf(&b, "func (%s) Invoke(ctx context.Context, invocation scenery.Invocation, input %s%sInput) (%s%sOutcome, error) { copied, err := %sClone%sInput(input); if err != nil { return nil, fmt.Errorf(\"copy internal client input: %%w\", err) }; value, err := sceneryruntime.InvokeContractBindingFrom(ctx, %q, %q, invocation, copied); if err != nil { return nil, err }; typed, ok := value.(%s%sOutcome); if !ok { return nil, fmt.Errorf(\"binding returned %%T, want %s%sOutcome\", value) }; cloned, err := %sClone%sOutcome(typed); if err != nil { return nil, fmt.Errorf(\"copy internal client outcome: %%w\", err) }; return cloned, nil }\n\n", clientType, contractQualifier, operationName, contractQualifier, operationName, contractQualifier, operationName, client.Binding.Address, service.Module, contractQualifier, operationName, contractQualifier, operationName, contractQualifier, operationName)
		}
	}
	if err := renderDurableDispatchOptionHelpers(&b, operations, resources); err != nil {
		return nil, err
	}
	renderCLIOutcomeHelpers(&b, resources, operations)
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
			fmt.Fprintf(&b, "\t\t\t\t%s, err := scenerystorage.Named(ctx, %q); if err != nil { return fmt.Errorf(\"resolve dependency %s: %%w\", err) }; input.Dependencies.%s = %s\n", variable, dependency.RuntimeName, dependency.Name, dependency.Field, variable)
		}
	}
	config, _ := service.Spec["config"].(map[string]any)
	for _, field := range namedChildren(service.Spec, "config_schema") {
		name, typeExpression := stringValue(field["name"]), stringValue(field["type"])
		value, exists := config[name]
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
	if err := renderDurableExecutionRegistrations(&b, service, operations, resources); err != nil {
		return nil, err
	}
	if err := renderScheduleAndEventRegistrations(&b, operations, resources); err != nil {
		return nil, err
	}
	for _, binding := range internalBindingsForOperations(resources, operations) {
		operation := operationForBinding(operations, binding)
		if operation == nil {
			return nil, fmt.Errorf("internal binding %s references an unknown service operation", binding.Address)
		}
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := stringValue(handler["method"])
		operationName := goName(operation.Name)
		delivery := stringValue(binding.Spec["delivery"])
		internal, _ := binding.Spec["internal"].(map[string]any)
		visibility := stringValue(internal["visibility"])
		internalPolicy := renderContractInternalPolicy(resourcesByAddress(&Manifest{Resources: resources}), binding)
		jsonCodecs := renderInternalBindingJSONCodecs(*operation, delivery)
		if delivery == "enqueue" {
			execution, ok := executionForBinding(resourcesByAddress(&Manifest{Resources: resources}), binding)
			if !ok || stringValue(execution.Spec["mode"]) != "durable" {
				return nil, fmt.Errorf("enqueue binding %s does not select a durable execution", binding.Address)
			}
			fmt.Fprintf(&b, "\t\t\tif err := sceneryruntime.RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: %q, Visibility: %q, Package: %q, Policy: %s, %s Invoke: func(ctx context.Context, _ any, input any) (any, error) { typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"internal binding input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(ctx, %q, copied, options) }}); err != nil { return err }\n", binding.Address, visibility, binding.Module, internalPolicy, jsonCodecs, operationName, operationName, durableDispatchOptionsFunction(execution), execution.Address)
		} else if delivery == "wait" {
			execution, ok := executionForBinding(resourcesByAddress(&Manifest{Resources: resources}), binding)
			if !ok || stringValue(execution.Spec["mode"]) != "durable" {
				return nil, fmt.Errorf("wait binding %s does not select a durable execution", binding.Address)
			}
			fmt.Fprintf(&b, "\t\t\tif err := sceneryruntime.RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: %q, Visibility: %q, Package: %q, Policy: %s, %s Invoke: func(ctx context.Context, _ any, input any) (any, error) { typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"internal binding input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; data, err := sceneryruntime.DispatchAndWaitContractDurableExecutionWithOptions(ctx, %q, copied, options); if err != nil { return nil, err }; return contract.Unmarshal%sOutcome(data) }}); err != nil { return err }\n", binding.Address, visibility, binding.Module, internalPolicy, jsonCodecs, operationName, operationName, durableDispatchOptionsFunction(execution), execution.Address, operationName)
		} else {
			fmt.Fprintf(&b, "\t\t\tif err := sceneryruntime.RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: %q, Visibility: %q, Package: %q, Policy: %s, %s Invoke: func(ctx context.Context, _ any, input any) (any, error) { if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"internal binding input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; outcome, err := service.%s(ctx, copied); if err != nil { if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; if err := sceneryruntime.PublishContractOperationOutcome(ctx, %q, cloned); err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return cloned, nil }}); err != nil { return err }\n", binding.Address, visibility, binding.Module, internalPolicy, jsonCodecs, operationName, operationName, method, operationName, operation.Address)
		}
	}
	if err := renderCLIBindingRegistrations(&b, resources, service, operations); err != nil {
		return nil, err
	}
	if err := renderPageRegistrations(&b, resources, operations); err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		operation := operationForBinding(operations, binding)
		if operation == nil {
			return nil, fmt.Errorf("binding %s references an unknown service operation", binding.Address)
		}
		if err := renderHTTPBindingRegistration(&b, resources, service, *operation, binding); err != nil {
			return nil, err
		}
	}
	b.WriteString("\t\t\treturn nil\n\t\t},\n\t})\n}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format application adapter for %s: %w\n%s", service.Address, err, b.String())
	}
	return formatted, nil
}

func renderApplicationComposition(contractRevision string, providerABIs map[string]string, adapters []applicationAdapter) ([]byte, error) {
	covered := map[string]bool{}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage composition\n\nimport (\n\tscenery \"scenery.sh\"\n")
	for index, adapter := range adapters {
		fmt.Fprintf(&b, "\tadapter%d %q\n", index, adapter.ImportPath)
		for _, address := range adapter.Covered {
			covered[address] = true
		}
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "const ContractRevision = %q\n\n", contractRevision)
	fmt.Fprintf(&b, "var RequiredAddresses = %#v\n\n", sortedBoolKeys(covered))
	fmt.Fprintf(&b, "var RequiredProviderABIs = %s\n\n", goStringStringMap(providerABIs))
	b.WriteString("func Register(registry scenery.Registry) error {\n")
	for index := range adapters {
		fmt.Fprintf(&b, "\tif err := adapter%d.Register(registry); err != nil { return err }\n", index)
	}
	b.WriteString("\treturn nil\n}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format application composition: %w\n%s", err, b.String())
	}
	return formatted, nil
}

func serviceOperations(resources []Resource, service Resource) []Resource {
	var operations []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.operation" && resource.Module == service.Module && resolveResourceRef(resource, refString(resource.Spec["service"]), "service") == service.Address {
			operations = append(operations, resource)
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Address < operations[j].Address })
	return operations
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

func operationForBinding(operations []Resource, binding Resource) *Resource {
	address := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
	for index := range operations {
		if operations[index].Address == address {
			return &operations[index]
		}
	}
	return nil
}

func renderInternalBindingJSONCodecs(operation Resource, delivery string) string {
	name := goName(operation.Name)
	decode := fmt.Sprintf("DecodeInput: func(data []byte) (any, error) { return contract.Unmarshal%sInput(data) },", name)
	if delivery == "enqueue" {
		return decode + " EncodeOutput: func(value any) ([]byte, error) { typed, ok := value.(scenery.ExecutionReceipt); if !ok { return nil, fmt.Errorf(\"internal binding output has type %T\", value) }; return scenery.MarshalContractValue(typed, \"std.type.execution_receipt\") },"
	}
	return decode + fmt.Sprintf(" EncodeOutput: func(value any) ([]byte, error) { typed, ok := value.(contract.%sOutcome); if !ok { return nil, fmt.Errorf(\"internal binding output has type %%T\", value) }; return contract.Marshal%sOutcome(typed) },", name, name)
}

func referencedExecutions(resources, bindings []Resource) []string {
	known := resourcesByAddress(&Manifest{Resources: resources})
	set := map[string]bool{}
	for _, binding := range bindings {
		address := resolveResourceRef(binding, refString(binding.Spec["execution"]), "execution")
		if known[address].Address != "" {
			set[address] = true
		}
	}
	return sortedBoolKeys(set)
}

func responseMappings(httpSpec map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, response := range namedChildren(httpSpec, "response") {
		result[refOrString(response["when"])] = response
	}
	return result
}

func runtimeAccess(binding Resource) string {
	exposure := stringValue(binding.Spec["exposure"])
	if exposure == "application" || exposure == "local" {
		return "sceneryruntime.Private"
	}
	authentication := refOrString(binding.Spec["authentication"])
	if authentication == "std.authentication.none" {
		return "sceneryruntime.Public"
	}
	return "sceneryruntime.Auth"
}

func runtimeHTTPPath(path string) string {
	path = httpPathTailPattern.ReplaceAllString(path, "*$1")
	return httpPathParameterPattern.ReplaceAllString(path, ":$1")
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
	typeExpression = strings.TrimSpace(typeExpression)
	if scalar, ok := value.(map[string]any); ok && stringValue(scalar["$scalar"]) != "" {
		kind := stringValue(scalar["$scalar"])
		switch kind {
		case "int":
			text := stringValue(scalar["value"])
			switch typeExpression {
			case "int32", "uint32", "float32", "float64":
				return []byte(text), nil
			default:
				return json.Marshal(text)
			}
		case "decimal":
			return json.Marshal(stringValue(scalar))
		case "duration":
			duration, err := scenery.ParseDuration(stringValue(scalar["nanoseconds"]) + "ns")
			if err != nil {
				return nil, err
			}
			return json.Marshal(duration.String())
		case "size":
			return json.Marshal(stringValue(scalar["bytes"]))
		case "bytes":
			return nil, fmt.Errorf("bytes config requires an explicit generated wire value")
		default:
			return json.Marshal(stringValue(scalar["value"]))
		}
	}
	if reference := refString(value); reference != "" {
		return nil, fmt.Errorf("config type %s does not accept resource reference %s", typeExpression, reference)
	}
	switch typeExpression {
	case "int", "int64", "uint64", "decimal", "size":
		return json.Marshal(fmt.Sprint(value))
	case "int32", "uint32", "float32", "float64":
		text := fmt.Sprint(value)
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return nil, err
		}
		return []byte(text), nil
	default:
		return json.Marshal(value)
	}
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
