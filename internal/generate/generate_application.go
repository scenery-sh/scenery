package generate

import (
	"encoding/json"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"scenery.sh/internal/compiler"
	scenery "scenery.sh/internal/contract"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/scn"
)

type applicationAdapterMetadata struct {
	PackageIdentity string
	Address         string
	ImportPath      string
	PackageName     string
	RelativeDir     string
	Covered         []string
	PackageABI      string
	Implementation  string
	Contract        string
}

type applicationAdapter struct {
	applicationAdapterMetadata
	Source []byte
}

type RuntimeIntegrationPlan = generateapi.RuntimeIntegrationPlan

func BuildRuntimeIntegrationPlan(result *Result) (RuntimeIntegrationPlan, error) {
	services := compiler.RuntimeServices(result.Manifest.Resources)
	assistants := canonicalAssistantResources(result.Manifest.Resources)
	mcpServers := canonicalMCPServers(result.Manifest.Resources)
	if len(services) == 0 && len(assistants) == 0 && len(mcpServers) == 0 {
		return RuntimeIntegrationPlan{}, nil
	}
	_, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		return RuntimeIntegrationPlan{}, err
	}
	return RuntimeIntegrationPlan{CompositionImport: generatedImport + "/composition"}, nil
}

func generateApplicationArtifacts(result *Result, idx *resourceIndex, input projectionInput) ([]generatedFile, error) {
	services := compiler.RuntimeServices(result.Manifest.Resources)
	assistants := canonicalAssistantResources(result.Manifest.Resources)
	mcpServers := canonicalMCPServers(result.Manifest.Resources)
	if len(services) == 0 && len(assistants) == 0 && len(mcpServers) == 0 {
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
	for _, adapter := range adapters {
		files = append(files, generatedFile{Path: filepath.Join(generatedRoot, filepath.FromSlash(adapter.RelativeDir), "adapter.gen.go"), Bytes: adapter.Source})
	}
	composition, err := renderApplicationComposition(result, providerRuntimeABIs(result.Manifest.Resources), adapters, assistants)
	if err != nil {
		return nil, err
	}
	files = append(files, generatedFile{Path: filepath.Join(generatedRoot, "composition", "composition.gen.go"), Bytes: composition})
	// Keep the provider-neutral assets package present in every generated
	// workspace. Development and worker lanes receive an empty registry; the
	// production build lane replaces this artifact set with verified embeds
	// before compiling the binary.
	if len(assistants) > 0 {
		emptyAssets, err := RenderAssistantAssetRegistry(result, nil)
		if err != nil {
			return nil, err
		}
		for relative, contents := range emptyAssets {
			files = append(files, generatedFile{Path: filepath.Join(result.Root, filepath.FromSlash(relative)), Bytes: contents})
		}
	}
	covered := map[string]bool{}
	packageABIs := map[string]string{}
	for _, adapter := range adapters {
		for _, address := range adapter.Covered {
			covered[address] = true
		}
		packageABIs[adapter.Contract] = adapter.PackageABI
	}
	for _, assistant := range assistants {
		covered[assistant.Address] = true
	}
	federations, err := mcpFederationTargets(result)
	if err != nil {
		return nil, err
	}
	for _, federation := range federations {
		for _, address := range federation.CoveredAddresses {
			covered[address] = true
		}
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

func prepareApplicationAdapter(result *Result, idx *resourceIndex, module, service Resource, generatedImport string) (applicationAdapterMetadata, error) {
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
		return applicationAdapterMetadata{}, fmt.Errorf("native service %s has no go_contract import path", service.Address)
	}
	moduleResources := idx.moduleResources(moduleInstancePath(module))
	packageABI, err := packageABIRevision(implementationImport, moduleResources, idx)
	if err != nil {
		return applicationAdapterMetadata{}, err
	}
	operations := compiler.ServiceOperations(result.Manifest.Resources, service)
	if compiler.IsProviderCRUDService(service) {
		bindings := serviceHTTPBindings(result.Manifest.Resources, operations)
		internalBindings := internalBindingsForOperations(result.Manifest.Resources, operations)
		mcpBindings := mcpBindingsForService(result.Manifest.Resources, service, operations)
		covered := []string{service.Address}
		covered = append(covered, resourceAddresses(operations)...)
		covered = append(covered, resourceAddresses(bindings)...)
		covered = append(covered, resourceAddresses(internalBindings)...)
		mcpResources := mcpBindingResources(mcpBindings)
		covered = append(covered, resourceAddresses(mcpResources)...)
		covered = append(covered, pageOwnedResourceAddresses(result.Manifest.Resources, operations)...)
		allBindings := append(append(append([]Resource(nil), bindings...), internalBindings...), mcpResources...)
		covered = append(covered, referencedExecutions(result.Manifest.Resources, allBindings)...)
		covered = canonicalStrings(covered)
		dirName := semanticPathName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
		packageName := goPackageName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
		contractImport := implementationImport + "/scenerycontract"
		adapterImport := generatedImport + "/" + dirName
		return applicationAdapterMetadata{PackageIdentity: packageIdentity, Address: service.Address, ImportPath: adapterImport, PackageName: packageName, RelativeDir: dirName, Covered: covered, PackageABI: packageABI, Implementation: "scenery.sh/datasource", Contract: contractImport}, nil
	}
	bindings := serviceHTTPBindings(result.Manifest.Resources, operations)
	internalBindings := internalBindingsForOperations(result.Manifest.Resources, operations)
	mcpBindings := mcpBindingsForService(result.Manifest.Resources, service, operations)
	eventBindings := eventBindingsForOperations(result.Manifest.Resources, operations)
	schedules := schedulesForOperations(result.Manifest.Resources, operations)
	emissions := eventEmissionsForOperations(result.Manifest.Resources, operations)
	covered := []string{service.Address}
	covered = append(covered, resourceAddresses(operations)...)
	covered = append(covered, resourceAddresses(bindings)...)
	covered = append(covered, resourceAddresses(internalBindings)...)
	mcpResources := mcpBindingResources(mcpBindings)
	covered = append(covered, resourceAddresses(mcpResources)...)
	covered = append(covered, resourceAddresses(eventBindings)...)
	covered = append(covered, resourceAddresses(schedules)...)
	covered = append(covered, resourceAddresses(emissions)...)
	covered = append(covered, pageOwnedResourceAddresses(result.Manifest.Resources, operations)...)
	allBindings := append(append(append(append([]Resource(nil), bindings...), internalBindings...), mcpResources...), eventBindings...)
	covered = append(covered, referencedExecutions(result.Manifest.Resources, allBindings)...)
	covered = canonicalStrings(covered)
	dirName := semanticPathName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
	packageName := goPackageName(moduleInstancePath(module) + "_" + service.Name + "_adapter")
	contractImport := implementationImport + "/scenerycontract"
	adapterImport := generatedImport + "/" + dirName
	return applicationAdapterMetadata{
		PackageIdentity: packageIdentity,
		Address:         service.Address, ImportPath: adapterImport, PackageName: packageName, RelativeDir: dirName,
		Covered: covered, PackageABI: packageABI, Implementation: implementationImport, Contract: contractImport,
	}, nil
}

// Ordinary source is rendered only after the shared metadata has been validated.
// The worker consumes applicationAdapterMetadata directly and never calls this.
func renderApplicationAdapter(result *Result, idx *resourceIndex, metadata applicationAdapterMetadata) (applicationAdapter, error) {
	service := idx.byAddress[metadata.Address]
	operations := compiler.ServiceOperations(result.Manifest.Resources, service)
	bindings := serviceHTTPBindings(result.Manifest.Resources, operations)
	mcpBindings := mcpBindingsForService(result.Manifest.Resources, service, operations)
	var source []byte
	var err error
	if compiler.IsProviderCRUDService(service) {
		source, err = renderProviderCRUDAdapterSource(result.Manifest.ContractRevision, metadata.PackageIdentity, metadata.PackageABI, metadata.Contract, metadata.PackageName, service, operations, bindings, mcpBindings, result.Manifest.Resources, metadata.Covered, providerRuntimeABIs(result.Manifest.Resources))
	} else {
		source, err = renderApplicationAdapterSource(result.Manifest.ContractRevision, metadata.PackageIdentity, metadata.PackageABI, metadata.Implementation, metadata.Contract, metadata.PackageName, service, operations, bindings, mcpBindings, result.Manifest.Resources, idx, metadata.Covered, providerRuntimeABIs(result.Manifest.Resources))
	}
	if err != nil {
		return applicationAdapter{}, err
	}
	return applicationAdapter{applicationAdapterMetadata: metadata, Source: source}, nil
}

func renderApplicationAdapterSource(contractRevision, packageIdentity, packageABI, implementationImport, contractImport, packageName string, service Resource, operations, bindings []Resource, mcpBindings []mcpToolTarget, resources []Resource, idx *resourceIndex, covered []string, providerABIs map[string]string) ([]byte, error) {
	b, err := renderNativeAdapterPreamble(contractRevision, packageIdentity, packageABI, implementationImport, contractImport, packageName, service, operations, bindings, idx, "scenery.sh/runtime/host", true)
	if err != nil {
		return nil, err
	}
	if err := renderDurableDispatchOptionHelpers(b, operations, resources); err != nil {
		return nil, err
	}
	renderCLIOutcomeHelpers(b, resources, operations)
	b.WriteString("func Register(registry scenery.Registry) error {\n")
	fmt.Fprintf(b, "\treturn registry.Register(%q, sceneryruntime.ContractRegistration{\n", service.Address+"/adapter")
	fmt.Fprintf(b, "\t\tContractRevision: ContractRevision, PackageContractABIRevision: PackageContractABIRevision, RuntimeABI: sceneryruntime.ContractRuntimeABI,\n\t\tProviderABIs: %s, CoveredAddresses: %#v,\n", goStringStringMap(providerABIs), covered)
	b.WriteString("\t\tApply: func() error {\n")
	fmt.Fprintf(b, "\t\t\tif contract.PackageIdentity != PackageIdentity { return fmt.Errorf(\"package identity mismatch\") }\n")
	fmt.Fprintf(b, "\t\t\tif contract.PackageContractABIRevision != PackageContractABIRevision { return fmt.Errorf(\"package contract ABI mismatch\") }\n")
	if err := renderNativeServiceInitialization(b, idx, service); err != nil {
		return nil, err
	}
	if err := renderDurableExecutionRegistrations(b, service, operations, resources); err != nil {
		return nil, err
	}
	if err := renderMCPToolRegistrations(b, contractRevision, service, mcpBindings, resources); err != nil {
		return nil, err
	}
	if err := renderScheduleAndEventRegistrations(b, operations, resources); err != nil {
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
		switch delivery {
		case "enqueue":
			execution, ok := executionForBinding(resourcesByAddress(&Manifest{Resources: resources}), binding)
			if !ok || stringValue(execution.Spec["mode"]) != "durable" {
				return nil, fmt.Errorf("enqueue binding %s does not select a durable execution", binding.Address)
			}
			fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: %q, Visibility: %q, Package: %q, Policy: %s, %s Invoke: func(ctx context.Context, _ any, input any) (any, error) { typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"internal binding input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(ctx, %q, copied, options) }}); err != nil { return err }\n", binding.Address, visibility, binding.Module, internalPolicy, jsonCodecs, operationName, operationName, durableDispatchOptionsFunction(execution), execution.Address)
		case "wait":
			execution, ok := executionForBinding(resourcesByAddress(&Manifest{Resources: resources}), binding)
			if !ok || stringValue(execution.Spec["mode"]) != "durable" {
				return nil, fmt.Errorf("wait binding %s does not select a durable execution", binding.Address)
			}
			fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: %q, Visibility: %q, Package: %q, Policy: %s, %s Invoke: func(ctx context.Context, _ any, input any) (any, error) { typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"internal binding input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; data, err := sceneryruntime.DispatchAndWaitContractDurableExecutionWithOptions(ctx, %q, copied, options); if err != nil { return nil, err }; return contract.Unmarshal%sOutcome(data) }}); err != nil { return err }\n", binding.Address, visibility, binding.Module, internalPolicy, jsonCodecs, operationName, operationName, durableDispatchOptionsFunction(execution), execution.Address, operationName)
		default:
			fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: %q, Visibility: %q, Package: %q, Policy: %s, %s Invoke: func(ctx context.Context, _ any, input any) (any, error) { if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"internal binding input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; outcome, err := service.%s(ctx, copied); if err != nil { if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; if err := sceneryruntime.PublishContractOperationOutcome(ctx, %q, cloned); err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return cloned, nil }}); err != nil { return err }\n", binding.Address, visibility, binding.Module, internalPolicy, jsonCodecs, operationName, operationName, method, operationName, operation.Address)
		}
	}
	if err := renderCLIBindingRegistrations(b, resources, service, operations); err != nil {
		return nil, err
	}
	if err := renderPageRegistrations(b, resources, operations); err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		operation := operationForBinding(operations, binding)
		if operation == nil {
			return nil, fmt.Errorf("binding %s references an unknown service operation", binding.Address)
		}
		if err := renderHTTPBindingRegistration(b, resources, service, *operation, binding); err != nil {
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

func renderApplicationComposition(result *Result, providerABIs map[string]string, adapters []applicationAdapter, assistants []Resource) ([]byte, error) {
	covered := map[string]bool{}
	federations, err := mcpFederationTargets(result)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage composition\n\nimport (\n\tscenery \"scenery.sh\"\n")
	for index, adapter := range adapters {
		fmt.Fprintf(&b, "\tadapter%d %q\n", index, adapter.ImportPath)
		for _, address := range adapter.Covered {
			covered[address] = true
		}
	}
	assistants = canonicalAssistantResources(assistants)
	assetsImport := ""
	if len(assistants) > 0 {
		_, generatedImport, importErr := resolveApplicationGeneratedRoot(result)
		if importErr != nil {
			return nil, importErr
		}
		assetsImport = generatedImport + "/assets"
	}
	if len(assistants) > 0 || len(federations) > 0 {
		b.WriteString("\tsceneryruntime \"scenery.sh/runtime/host\"\n")
		if assetsImport != "" {
			fmt.Fprintf(&b, "\tsceneryassets %q\n", assetsImport)
		}
		for _, assistant := range assistants {
			covered[assistant.Address] = true
		}
		for _, federation := range federations {
			for _, address := range federation.CoveredAddresses {
				covered[address] = true
			}
		}
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "const ContractRevision = %q\n\n", result.Manifest.ContractRevision)
	fmt.Fprintf(&b, "var RequiredAddresses = %#v\n\n", sortedBoolKeys(covered))
	fmt.Fprintf(&b, "var RequiredProviderABIs = %s\n\n", goStringStringMap(providerABIs))
	b.WriteString("func Register(registry scenery.Registry) error {\n")
	for index := range adapters {
		fmt.Fprintf(&b, "\tif err := adapter%d.Register(registry); err != nil { return err }\n", index)
	}
	if len(assistants) > 0 {
		resources := resourcesByAddress(&Manifest{Resources: result.Manifest.Resources})
		b.WriteString("\tif err := registry.Register(\"scenery/assistants\", sceneryruntime.ContractRegistration{\n")
		fmt.Fprintf(&b, "\t\tContractRevision: ContractRevision, PackageContractABIRevision: ContractRevision, RuntimeABI: sceneryruntime.ContractRuntimeABI, CoveredAddresses: %#v,\n", resourceAddresses(assistants))
		b.WriteString("\t\tApply: func() error {\n")
		for _, assistant := range assistants {
			registration, err := renderAssistantRegistration(result, resources, assistant)
			if err != nil {
				return nil, err
			}
			b.WriteString("\t\t\t")
			b.WriteString(registration)
		}
		b.WriteString("\t\tembeddedAssets := sceneryassets.Assets()\n")
		b.WriteString("\t\truntimeAssets := make([]sceneryruntime.AssistantEmbeddedAsset, 0, len(embeddedAssets))\n")
		b.WriteString("\t\tfor _, asset := range embeddedAssets {\n")
		b.WriteString("\t\t\truntimeAssets = append(runtimeAssets, sceneryruntime.AssistantEmbeddedAsset{\n")
		b.WriteString("\t\t\tDescriptor: sceneryruntime.AssistantAssetDescriptor{Kind: asset.Descriptor.Kind, SchemaRevision: asset.Descriptor.SchemaRevision, AssistantAddress: asset.Descriptor.AssistantAddress, Target: asset.Descriptor.Target, RuntimeRevision: asset.Descriptor.RuntimeRevision, CapabilityRevision: asset.Descriptor.CapabilityRevision, NodeArchiveDigest: asset.Descriptor.NodeArchiveDigest, NodeTreeDigest: asset.Descriptor.NodeTreeDigest, CapsuleArchiveDigest: asset.Descriptor.CapsuleArchiveDigest, CapsuleTreeDigest: asset.Descriptor.CapsuleTreeDigest, CapsuleEntry: asset.Descriptor.CapsuleEntry, PackageLockDigest: asset.Descriptor.PackageLockDigest}, DescriptorJSON: asset.DescriptorJSON, NodeArchive: asset.NodeArchive, NodeDescriptorJSON: asset.NodeDescriptorJSON, CapsuleArchive: asset.CapsuleArchive, CapsuleDescriptorJSON: asset.CapsuleDescriptorJSON})\n")
		b.WriteString("\t\t}\n")
		fmt.Fprintf(&b, "\t\tif err := sceneryruntime.RegisterEmbeddedAssistantAssets(sceneryruntime.AssistantProductionOptions{ApplicationID: %q}, runtimeAssets); err != nil { return err }\n", result.Manifest.Application.Name)
		b.WriteString("\t\t\treturn nil\n\t\t},\n\t}); err != nil { return err }\n")
	}
	if err := renderMCPFederationRegistrations(result, &b); err != nil {
		return nil, err
	}
	b.WriteString("\treturn nil\n}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format application composition: %w\n%s", err, b.String())
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
