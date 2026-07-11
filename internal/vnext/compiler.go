package vnext

import (
	"encoding/json"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

var rootSingletons = map[string]bool{"language": true, "application": true, "workspace": true}

var rootResourceKinds = map[string]bool{
	"go_module": true, "go_toolchain": true, "go_target": true,
	"http_gateway": true, "authentication": true, "authorization": true,
	"workload_identity": true,
	"pipeline":          true, "provider": true, "data_source": true,
	"execution_engine": true, "event_bus": true, "secret_store": true,
	"secret": true, "deployment": true, "typescript_client": true,
	"patch": true,
}

var packageResourceKinds = map[string]bool{
	"service": true, "record": true, "enum": true, "union": true,
	"operation": true, "execution": true, "binding": true,
	"schedule": true, "event": true, "event_emission": true,
	"entity": true, "view": true, "crud": true, "fixture": true,
	"page": true, "renderer": true, "middleware": true,
}

func Compile(root string) (*Result, error) {
	return compile(root, false)
}

func compileDuringChangeTransaction(root string) (*Result, error) {
	return compile(root, true)
}

func compile(root string, allowActiveChangeTransaction bool) (*Result, error) {
	return compileResult(root, allowActiveChangeTransaction, true)
}

// compileContractGraph resolves and validates the canonical graph without
// repeating source implementation verification. Build and deployment phases
// consume an independently verified, content-addressed implementation revision.
func compileContractGraph(root string, allowActiveChangeTransaction bool) (*Result, error) {
	return compileResult(root, allowActiveChangeTransaction, false)
}

func compileResult(root string, allowActiveChangeTransaction, verifyImplementation bool) (*Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := recoverInterruptedChangeTransactionWithAccess(absRoot, false, allowActiveChangeTransaction); err != nil {
		return nil, err
	}
	result := &Result{Root: absRoot, ContractStatus: "invalid", ImplementationStatus: "not_requested"}
	paths, err := sourceFiles(absRoot, true)
	if err != nil {
		return nil, err
	}
	if !containsBase(paths, "scenery.scn") {
		return nil, fmt.Errorf("%s does not contain scenery.scn", absRoot)
	}
	for _, path := range paths {
		source, diagnostics := parseSource(absRoot, path)
		if source != nil {
			result.Sources = append(result.Sources, source)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}
	lockfile, lockDiagnostics, err := loadLockfile(absRoot)
	if err != nil {
		return nil, err
	}
	result.Diagnostics = append(result.Diagnostics, lockDiagnostics...)
	result.Diagnostics = append(result.Diagnostics, validateAuthoredBlockSchemas(result.Sources, false)...)
	result.Diagnostics = append(result.Diagnostics, validateStaticExpressions(result.Sources)...)
	migration, migrationDiags := parseMigration(absRoot)
	result.Migration = migration
	result.Diagnostics = append(result.Diagnostics, migrationDiags...)

	if hasErrors(result.Diagnostics) {
		workspaceRevision, revisionErr := computeWorkspaceRevision(absRoot, result.Sources, migration)
		if revisionErr != nil {
			return nil, revisionErr
		}
		result.WorkspaceRevision = workspaceRevision
		return result, nil
	}

	manifest, viewManifests, diagnostics, packageSources := compileSources(absRoot, result.Sources, migration, lockfile)
	result.Sources = append(result.Sources, packageSources...)
	diagnostics = append(diagnostics, validateStaticExpressions(packageSources)...)
	workspaceRevision, err := computeWorkspaceRevision(absRoot, result.Sources, migration)
	if err != nil {
		return nil, err
	}
	result.WorkspaceRevision = workspaceRevision
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	if manifest != nil && hasErrors(diagnostics) {
		result.PartialGraph = &PartialGraph{Deployable: false, Application: manifest.Application, Profiles: append([]string(nil), manifest.Profiles...), Resources: append([]Resource(nil), manifest.Resources...), SourceMap: manifest.SourceMap}
		return result, nil
	}
	result.Manifest = manifest
	result.ViewManifests = viewManifests
	if manifest != nil {
		result.ContractStatus = "valid"
		result.HTTPSurfaceRevisions, result.OpenAPIRevisions = computeHTTPProjectionRevisions(manifest)
		if verifyImplementation && hasNativeGoHandlers(manifest.Resources) {
			result.ImplementationStatus = "valid"
			implementationDiagnostics := verifyGoImplementation(result)
			if hasErrors(implementationDiagnostics) {
				result.ImplementationStatus = "invalid"
			}
			result.Diagnostics = append(result.Diagnostics, implementationDiagnostics...)
		}
		// The language compiler never approximates the Go build graph.
		// implementation_revision is produced only when a build supplies an
		// exact content-addressed input manifest for a verified target.
		result.ImplementationRevisions = nil
		// Deployment revisions are produced only by deployment planning, after
		// provider plans exist. Compilation deliberately has no provider plan.
		result.DeploymentRevisions = nil
		manifest.Diagnostics = append([]Diagnostic{}, result.Diagnostics...)
	}
	return result, nil
}

func validateStaticExpressions(sources []*Source) []Diagnostic {
	var diagnostics []Diagnostic
	var visit func(*Block, string)
	visit = func(block *Block, parent string) {
		for name, expression := range block.Attributes {
			if expression.Kind == "expression" && !expression.Static && !runtimeExpressionField(parent, block.Type, name) {
				rng := expression.Range
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1010", Severity: "error", Message: "dynamic HCL expression is forbidden in edition 2027", Range: &rng})
			}
		}
		for _, child := range block.Blocks {
			visit(child, block.Type)
		}
	}
	for _, source := range sources {
		for _, block := range source.Blocks {
			visit(block, "")
		}
	}
	return diagnostics
}

func runtimeExpressionField(parent, blockType, attribute string) bool {
	schema, ok := authoredSchemaForBlock(parent, blockType)
	if !ok {
		return false
	}
	field, ok := schema.Attributes[attribute]
	return ok && field.Phase == "runtime"
}

func authoredSchemaForBlock(parent, blockType string) (*authoredBlockSchema, bool) {
	if parent == "" {
		if schema, ok := authoredStructuralSchemas[blockType]; ok {
			return schema, true
		}
		return authoredResourceSourceSchema(blockType)
	}
	find := func(rootType string, root *authoredBlockSchema) (*authoredBlockSchema, bool) {
		var visit func(string, *authoredBlockSchema) (*authoredBlockSchema, bool)
		visit = func(currentType string, current *authoredBlockSchema) (*authoredBlockSchema, bool) {
			if currentType == parent {
				if child, ok := current.Children[blockType]; ok {
					return child.Schema, true
				}
			}
			for childType, child := range current.Children {
				if found, ok := visit(childType, child.Schema); ok {
					return found, true
				}
			}
			return nil, false
		}
		return visit(rootType, root)
	}
	for rootType := range authoredResourceChildren {
		root, ok := authoredResourceSourceSchema(rootType)
		if !ok {
			continue
		}
		if schema, ok := find(rootType, root); ok {
			return schema, true
		}
	}
	for rootType, root := range authoredStructuralSchemas {
		if schema, ok := find(rootType, root); ok {
			return schema, true
		}
	}
	return nil, false
}

func compileSources(root string, sources []*Source, migration *Migration, lockfile *Lockfile) (*Manifest, map[string]*Manifest, []Diagnostic, []*Source) {
	var diagnostics []Diagnostic
	var packageSources []*Source
	allBlocks := blocksFromSources(sources)
	counts := map[string]int{}
	var language, application *Block
	var modules []*Block
	resources := []Resource{}
	sourceResources := []Resource{}
	sourceMap := map[string]SourceRecord{}
	for _, source := range sources {
		sourceMap[source.ID] = SourceRecord{URI: source.Relative}
	}
	for _, item := range allBlocks {
		block := item.Block
		counts[block.Type]++
		switch {
		case rootSingletons[block.Type]:
			switch block.Type {
			case "language":
				language = block
			case "application":
				application = block
			}
		case block.Type == "module":
			modules = append(modules, block)
		case block.Type == "extension" || block.Type == "resource":
			diagnostic := diagnosticForBlock("SCN7001", "unsupported_profile: declarative extensions require scenery.declarative-extensions/v1", block)
			diagnostic.Details = map[string]any{"profile": "scenery.declarative-extensions/v1", "syntax": block.Type}
			diagnostics = append(diagnostics, diagnostic)
		case rootResourceKinds[block.Type]:
			resource, diag := resourceFromBlock("app", block, item.Source.ID)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			} else {
				resources = append(resources, resource)
				sourceResources = append(sourceResources, authoredResourceView(resource))
			}
		default:
			diagnostics = append(diagnostics, diagnosticForBlock("SCN1002", "unknown top-level block "+block.Type, block))
		}
	}
	for singleton := range rootSingletons {
		if singleton == "workspace" {
			continue
		}
		if counts[singleton] != 1 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1101", Severity: "error", Message: fmt.Sprintf("application requires exactly one %s block; found %d", singleton, counts[singleton])})
		}
	}
	if language != nil {
		edition, ok := literalString(language, "edition")
		if !ok || edition != Edition {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN1003", "language edition must be \"2027\"", language))
		}
	}
	appName, appVersion := "", ""
	if application != nil {
		if len(application.Labels) != 1 {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN1004", "application requires one label", application))
		} else {
			appName = application.Labels[0]
		}
		appVersion, _ = literalString(application, "version")
	}
	resources, providerDiagnostics := resolveLockedProviders(root, resources, lockfile)
	diagnostics = append(diagnostics, providerDiagnostics...)
	resources, providerDiagnostics = enrichProviderInstances(resources)
	diagnostics = append(diagnostics, providerDiagnostics...)

	seenModules := map[string]bool{}
	validModules := make([]*Block, 0, len(modules))
	for _, module := range modules {
		if len(module.Labels) != 1 {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN3001", "module requires one label", module))
			continue
		}
		name := module.Labels[0]
		if seenModules[name] {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN1102", "duplicate module "+name, module))
			continue
		}
		seenModules[name] = true
		validModules = append(validModules, module)
	}
	sort.Slice(validModules, func(i, j int) bool { return validModules[i].Labels[0] < validModules[j].Labels[0] })
	rootModuleExports := map[string]map[string]any{}
	pendingModules := append([]*Block(nil), validModules...)
	for len(pendingModules) > 0 {
		progress := false
		remaining := make([]*Block, 0, len(pendingModules))
		for _, module := range pendingModules {
			prepared, unresolved := prepareNestedModuleBlock(module, nil, rootModuleExports)
			if len(unresolved) > 0 {
				remaining = append(remaining, module)
				continue
			}
			compiled := compileModuleInstanceWithSource(root, root, "app", prepared, module, resources, lockfile, map[string]bool{})
			resources = append(resources, compiled.Resources...)
			sourceResources = append(sourceResources, compiled.SourceResources...)
			diagnostics = append(diagnostics, compiled.Diagnostics...)
			for _, source := range compiled.Sources {
				sourceMap[source.ID] = SourceRecord{URI: source.Relative}
				packageSources = append(packageSources, source)
			}
			if compiled.ModuleResource.Address != "" {
				exports, _ := compiled.ModuleResource.Spec["exports"].(map[string]any)
				rootModuleExports[module.Labels[0]] = cloneMapValue(exports)
			}
			progress = true
		}
		if !progress {
			for _, module := range remaining {
				diagnostics = append(diagnostics, diagnosticForBlock("SCN3009", "root module dependency cycle or unavailable export", module))
			}
			break
		}
		pendingModules = remaining
	}
	for index := range resources {
		if resources[index].Module != "app" || resources[index].Kind == "scenery.module/v1" {
			continue
		}
		before := cloneMapValue(resources[index].Spec)
		resolved, unresolved := substituteModuleExports(resources[index].Spec, rootModuleExports)
		resources[index].Spec, _ = resolved.(map[string]any)
		markResolvedReferenceProvenance(&resources[index], before, resources[index].Spec, "/spec", resources[index].Module, nil)
		for _, reference := range unresolved {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3010", Severity: "error", Message: "unknown root module export " + reference, Address: resources[index].Address})
		}
	}

	legacyResources, legacyDiagnostics := lowerLegacyResources(root, appName, migration, resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	diagnostics = append(diagnostics, validateNativeMigrationLegacyAbsence(root, appName, migration, resources)...)
	addLegacySourceRecords(sourceMap, legacyResources)
	sourceNative := cloneResourceView(sourceResources)
	sourceLegacy := cloneResourceView(legacyResources)
	preContextNative := cloneResourceView(resources)
	resources, legacyDiagnostics = contextualizeResourceScalars(resources)
	markContextualScalarProvenance(preContextNative, resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	preContextLegacy := cloneResourceView(legacyResources)
	legacyResources, legacyDiagnostics = contextualizeResourceScalars(legacyResources)
	markContextualScalarProvenance(preContextLegacy, legacyResources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	if migration == nil {
		diagnostics = append(diagnostics, validateNativeOnlyLegacyAbsence(root)...)
	}
	applyHTTPEffectiveDefaults(resources)
	applyAuthoredEffectiveDefaults(resources)
	applyHTTPEffectiveDefaults(legacyResources)
	applyAuthoredEffectiveDefaults(legacyResources)
	resources, legacyDiagnostics = applyPatches(resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	resources, legacyDiagnostics = enrichDataImplementationDigests(root, resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	resources, legacyDiagnostics = enrichUIImplementationDigests(root, resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	completeFieldProvenance(sourceNative, "source")
	completeFieldProvenance(sourceLegacy, "source")
	completeFieldProvenance(resources, "effective")
	completeFieldProvenance(legacyResources, "effective")
	effectiveNative := cloneResourceView(resources)
	effectiveLegacy := cloneResourceView(legacyResources)
	resources, legacyDiagnostics = expandDataResources(resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	resources, legacyDiagnostics = enrichDataImplementationDigests(root, resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	resources, legacyDiagnostics = enrichUIImplementationDigests(root, resources)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	applyHTTPEffectiveDefaults(resources)
	resources, legacyDiagnostics = linkMigrationResources(resources, legacyResources, migration)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	applyMigration(resources, migration)
	completeFieldProvenance(resources, "expanded")
	validateMigrationCandidateGraphs(root, resources, migration)
	diagnostics = append(diagnostics, validateResources(root, resources, migration)...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
	profiles := profilesFromLanguage(language)
	if len(profiles) == 0 {
		profiles = append([]string(nil), KernelProfiles...)
	}
	profiles = normalizeProfiles(profiles, resources)
	diagnostics = append(diagnostics, validateProfiles(profiles, resources)...)
	if containsExactString(profiles, "scenery.go-implementation/v1") {
		diagnostics = append(diagnostics, validateGoContractOwnership(application, effectiveNative)...)
	}
	revision, err := contractRevision(resources, append([]string(nil), profiles...), appName)
	if err != nil {
		diagnostics = append(diagnostics, internalDiagnostic("SCN9002", err.Error()))
	}
	manifest := &Manifest{APIVersion: ManifestVersion, Edition: Edition, DiagnosticCatalog: DiagnosticCatalog, Application: ApplicationIdentity{Name: appName, Version: appVersion}, Profiles: profiles, ContractRevision: revision, Resources: resources, SourceMap: sourceMap, Diagnostics: []Diagnostic{}}
	views := map[string]*Manifest{
		"source":    viewManifest(manifest, linkMigrationView(sourceNative, sourceLegacy, migration)),
		"effective": viewManifest(manifest, linkMigrationView(effectiveNative, effectiveLegacy, migration)),
		"expanded":  manifest,
	}
	return manifest, views, diagnostics, packageSources
}

func cloneResourceView(resources []Resource) []Resource {
	data, _ := json.Marshal(resources)
	var cloned []Resource
	_ = json.Unmarshal(data, &cloned)
	return cloned
}

func authoredResourceView(resource Resource) Resource {
	cloned := cloneResourceView([]Resource{resource})[0]
	schema, ok := authoredResourceSourceSchema(blockTypeForKind(resource.Kind))
	if !ok {
		return cloned
	}
	spec := map[string]any{}
	for name, value := range cloned.Spec {
		if _, ok := schema.Attributes[name]; ok {
			spec[name] = value
			continue
		}
		if _, ok := schema.Children[name]; ok {
			spec[name] = value
		}
	}
	cloned.Spec = spec
	return cloned
}

func linkMigrationView(native, legacy []Resource, migration *Migration) []Resource {
	if migration == nil {
		resources := cloneResourceView(native)
		sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
		return resources
	}
	viewMigration := *migration
	viewMigration.Services = append([]MigrationService(nil), migration.Services...)
	resources, _ := linkMigrationResources(cloneResourceView(native), cloneResourceView(legacy), &viewMigration)
	applyMigration(resources, &viewMigration)
	return resources
}

func viewManifest(expanded *Manifest, resources []Resource) *Manifest {
	if expanded == nil {
		return nil
	}
	return &Manifest{
		APIVersion: expanded.APIVersion, Edition: expanded.Edition, DiagnosticCatalog: expanded.DiagnosticCatalog,
		Application: expanded.Application, Profiles: append([]string(nil), expanded.Profiles...), ContractRevision: expanded.ContractRevision,
		Resources: resources, SourceMap: expanded.SourceMap, Diagnostics: append([]Diagnostic{}, expanded.Diagnostics...),
	}
}

func addLegacySourceRecords(sourceMap map[string]SourceRecord, resources []Resource) {
	for _, resource := range resources {
		if resource.Origin.Kind != "legacy_v0" || resource.Origin.SourceID == "" {
			continue
		}
		uri, _ := resource.Origin.LegacyIdentity["file"].(string)
		uri = filepath.ToSlash(strings.TrimSpace(uri))
		if uri == "" || filepath.IsAbs(uri) {
			uri = "scenery-legacy:///source/" + resource.Origin.SourceID
		}
		sourceMap[resource.Origin.SourceID] = SourceRecord{URI: uri}
	}
}

func packageInterfaceMetadata(sources []*Source) (map[string]any, map[string]any, map[string]any, map[string]any) {
	packageMetadata := map[string]any{}
	inputs := map[string]any{}
	exports := map[string]any{}
	exportMetadata := map[string]any{}
	for _, item := range blocksFromSources(sources) {
		block := item.Block
		if len(block.Labels) != 1 && block.Type != "package" {
			continue
		}
		switch block.Type {
		case "package":
			packageMetadata = blockSpec(block)
			if len(block.Labels) == 1 {
				packageMetadata["name"] = block.Labels[0]
			}
		case "input":
			inputs[block.Labels[0]] = blockSpec(block)
		case "export":
			spec := blockSpec(block)
			exports[block.Labels[0]] = spec["value"]
			exportMetadata[block.Labels[0]] = spec
		}
	}
	return packageMetadata, inputs, exports, exportMetadata
}

func packageVersionFromSources(sources []*Source) string {
	for _, source := range sources {
		for _, block := range source.Blocks {
			if block.Type == "package" {
				version, _ := literalString(block, "version")
				return version
			}
		}
	}
	return ""
}

func packageCompileDescriptorDigest(resources []Resource, sources []*Source) string {
	packageMetadata, inputs, exports, exportMetadata := packageInterfaceMetadata(sources)
	declarations := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		declarations = append(declarations, map[string]any{
			"kind": resource.Kind, "name": resource.Name, "spec": resource.Spec,
		})
	}
	sort.Slice(declarations, func(i, j int) bool {
		if declarations[i]["kind"].(string) != declarations[j]["kind"].(string) {
			return declarations[i]["kind"].(string) < declarations[j]["kind"].(string)
		}
		return declarations[i]["name"].(string) < declarations[j]["name"].(string)
	})
	return revisionHash("scenery.package-compile-descriptor.v1\x00", map[string]any{
		"package": packageMetadata, "inputs": inputs, "exports": exports,
		"export_metadata": exportMetadata, "resources": declarations,
	})
}

type sourcedBlock struct {
	Source *Source
	Block  *Block
}

func blocksFromSources(sources []*Source) []sourcedBlock {
	var result []sourcedBlock
	for _, source := range sources {
		for _, block := range source.Blocks {
			result = append(result, sourcedBlock{Source: source, Block: block})
		}
	}
	return result
}

func compilePackage(root, dir, module string) ([]Resource, []*Source, []Diagnostic) {
	return compilePackageLogical(root, dir, module, "")
}

func compilePackageLogical(root, dir, module, logicalBase string) ([]Resource, []*Source, []Diagnostic) {
	paths, err := sourceFiles(dir, false)
	if err != nil {
		return nil, nil, []Diagnostic{{Code: "SCN3004", Severity: "error", Message: err.Error()}}
	}
	if !containsBase(paths, "scenery.package.scn") {
		return nil, nil, []Diagnostic{{Code: "SCN3005", Severity: "error", Message: fmt.Sprintf("module %s is missing scenery.package.scn", module)}}
	}
	var sources []*Source
	var diagnostics []Diagnostic
	for _, path := range paths {
		var source *Source
		var diags []Diagnostic
		if logicalBase == "" {
			source, diags = parseSource(root, path)
		} else {
			relative, _ := filepath.Rel(dir, path)
			source, diags = parseSourceLogical(path, pathpkg.Join(logicalBase, filepath.ToSlash(relative)))
			if source != nil {
				source.External = true
			}
		}
		if source != nil {
			sources = append(sources, source)
		}
		diagnostics = append(diagnostics, diags...)
	}
	diagnostics = append(diagnostics, validateAuthoredBlockSchemas(sources, true)...)
	packageCount := 0
	seen := map[string]bool{}
	var resources []Resource
	for _, item := range blocksFromSources(sources) {
		block := item.Block
		switch {
		case block.Type == "package":
			packageCount++
		case block.Type == "input" || block.Type == "export":
			// Package interface declarations are represented in module metadata, not standalone resources.
		case block.Type == "module":
			// Nested package instances are compiled by compileModuleInstance after
			// this package's own inputs have been resolved.
		case packageResourceKinds[block.Type]:
			resource, diag := resourceFromBlock(module, block, item.Source.ID)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
				continue
			}
			if seen[resource.Address] {
				diagnostics = append(diagnostics, diagnosticForBlock("SCN1103", "duplicate resource "+resource.Address, block))
				continue
			}
			seen[resource.Address] = true
			resources = append(resources, resource)
		default:
			diagnostics = append(diagnostics, diagnosticForBlock("SCN1005", "unknown package block "+block.Type, block))
		}
	}
	if packageCount != 1 {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN3006", Severity: "error", Message: fmt.Sprintf("module %s requires exactly one package block; found %d", module, packageCount)})
	}
	resources, configDiagnostics := enrichPackageGoServiceSchemas(resources, sources)
	diagnostics = append(diagnostics, configDiagnostics...)
	return resources, sources, diagnostics
}

func resourceFromBlock(module string, block *Block, sourceID string) (Resource, *Diagnostic) {
	if len(block.Labels) != 1 {
		d := diagnosticForBlock("SCN1006", block.Type+" requires one label", block)
		return Resource{}, &d
	}
	name := block.Labels[0]
	spec := blockSpec(block)
	declarationRange := block.Range
	moduleChain := []string(nil)
	if module != "app" {
		moduleChain = moduleInstantiationChain(module)
	}
	attributeRanges := make(map[string]Range, len(block.AttributeRanges))
	for name, rng := range block.AttributeRanges {
		attributeRanges["/spec/"+escapeJSONPointer(name)] = rng
	}
	address := resourceAddress(module, block.Type, name)
	return Resource{Address: address, Kind: kindForBlock(block.Type), Name: name, Module: module, Spec: spec, Origin: Origin{
		Kind: "authored", SourceID: sourceID, DeclarationRange: &declarationRange, AttributeRanges: attributeRanges,
		ModuleChain: moduleChain, FieldProvenance: authoredFieldProvenance(block, "/spec", address, module),
	}}, nil
}

func moduleInstantiationChain(instance string) []string {
	parts := strings.Split(strings.Trim(instance, "/"), "/")
	chain := make([]string, 0, len(parts))
	parent := "app"
	for _, part := range parts {
		chain = append(chain, resourceAddress(parent, "module", part))
		if parent == "app" {
			parent = part
		} else {
			parent += "/" + part
		}
	}
	return chain
}

func blockSpec(block *Block) map[string]any {
	spec := make(map[string]any, len(block.Attributes)+len(block.Blocks))
	for name, expression := range block.Attributes {
		if runtimeKeyExpressionField(block.Type, name) {
			spec[name] = runtimeKeyExpressionValue(expression)
		} else {
			spec[name] = expressionValue(expression)
		}
	}
	for _, child := range block.Blocks {
		value := blockSpec(child)
		if len(child.Labels) > 0 {
			value["name"] = child.Labels[0]
		}
		if existing, ok := spec[child.Type]; ok {
			switch current := existing.(type) {
			case []any:
				spec[child.Type] = append(current, value)
			default:
				spec[child.Type] = []any{current, value}
			}
		} else {
			spec[child.Type] = value
		}
	}
	return spec
}

func runtimeKeyExpressionField(blockType, attribute string) bool {
	return (blockType == "idempotency" || blockType == "concurrency") && attribute == "key"
}

func runtimeKeyExpressionValue(expression Expression) any {
	if expression.Kind == "reference" {
		return map[string]any{"$expression": expression.Traversal}
	}
	if expression.Kind != "literal" {
		return map[string]any{"$expression": strings.TrimSpace(expression.Raw)}
	}
	values, ok := expression.Value.([]any)
	if !ok {
		return expression.Value
	}
	converted := make([]any, len(values))
	for index, value := range values {
		if reference := refString(value); reference != "" {
			converted[index] = map[string]any{"$expression": reference}
		} else {
			converted[index] = value
		}
	}
	return converted
}

func expressionValue(expression Expression) any {
	switch expression.Kind {
	case "reference":
		return map[string]any{"$ref": expression.Traversal}
	case "literal":
		return expression.Value
	default:
		return map[string]any{"$expression": strings.TrimSpace(expression.Raw)}
	}
}

func validateResources(root string, resources []Resource, migration *Migration) []Diagnostic {
	diagnostics := validateResourceSchemas(resources)
	diagnostics = append(diagnostics, validateTypeSystem(resources)...)
	diagnostics = append(diagnostics, validateConstraints(resources)...)
	diagnostics = append(diagnostics, validateGoServiceConfiguration(resources)...)
	diagnostics = append(diagnostics, validateGoGeneratedNames(resources)...)
	diagnostics = append(diagnostics, validateGoTargets(root, resources)...)
	diagnostics = append(diagnostics, validateSecurityResources(resources)...)
	diagnostics = append(diagnostics, validateReferences(resources)...)
	diagnostics = append(diagnostics, validateProfileResources(resources)...)
	diagnostics = append(diagnostics, validateDataSemantics(root, resources)...)
	diagnostics = append(diagnostics, validateDeploymentSemantics(&Manifest{Resources: resources})...)
	diagnostics = append(diagnostics, validateUISemantics(root, resources)...)
	byAddress := map[string]Resource{}
	for _, resource := range resources {
		if previous, ok := byAddress[resource.Address]; ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1104", Severity: "error", Message: "duplicate resource address " + resource.Address, Address: resource.Address, Related: []Related{{Address: previous.Address}}})
		}
		byAddress[resource.Address] = resource
	}
	diagnostics = append(diagnostics, validateHTTPResources(resources)...)
	diagnostics = append(diagnostics, validateTypeScriptResources(resources)...)
	if migration != nil {
		diagnostics = append(diagnostics, migration.validate(root, resources)...)
	}
	return diagnostics
}

func canonicalRoute(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") || (strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
			parts[i] = "{}"
		}
	}
	return strings.Join(parts, "/")
}

func refString(value any) string {
	m, _ := value.(map[string]any)
	ref, _ := m["$ref"].(string)
	return ref
}

func profilesFromLanguage(language *Block) []string {
	if language == nil {
		return nil
	}
	expression, ok := language.Attributes["require_profiles"]
	if !ok {
		return nil
	}
	values, ok := expression.Value.([]any)
	if !ok {
		return nil
	}
	var profiles []string
	for _, value := range values {
		if item, ok := value.(string); ok {
			profiles = append(profiles, item)
		}
	}
	sort.Strings(profiles)
	return profiles
}

func normalizeProfiles(profiles []string, resources []Resource) []string {
	set := map[string]bool{}
	for _, profile := range profiles {
		set[profile] = true
	}
	for _, resource := range resources {
		if resource.Kind == "scenery.typescript-client/v1" {
			set["scenery.typescript-client/v1"] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for profile := range set {
			for _, dependency := range ProfileDependencies[profile] {
				if !set[dependency] {
					set[dependency] = true
					changed = true
				}
			}
		}
	}
	result := make([]string, 0, len(set))
	for profile := range set {
		result = append(result, profile)
	}
	sort.Strings(result)
	return result
}

func validateProfiles(profiles []string, resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	active := map[string]bool{}
	for _, profile := range profiles {
		active[profile] = true
		if !SupportedProfiles[profile] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN7001", Severity: "error", Message: "unsupported_profile: " + profile})
		}
	}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.execution/v1":
			if mode, _ := resource.Spec["mode"].(string); mode == "durable" && !active["scenery.runtime-durable/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7002", Severity: "error", Message: "unsupported_profile: scenery.runtime-durable/v1", Address: resource.Address})
			} else if mode == "workflow" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7002", Severity: "error", Message: "unsupported_profile: workflow execution", Address: resource.Address})
			}
		case "scenery.data-source/v1", "scenery.entity/v1", "scenery.view/v1", "scenery.crud/v1", "scenery.fixture/v1":
			if !active["scenery.data/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7003", Severity: "error", Message: "unsupported_profile: scenery.data/v1", Address: resource.Address})
			}
		case "scenery.deployment/v1":
			if !active["scenery.deployment/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7004", Severity: "error", Message: "unsupported_profile: scenery.deployment/v1", Address: resource.Address})
			}
		case "scenery.event/v1", "scenery.event-emission/v1":
			if !active["scenery.events/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7005", Severity: "error", Message: "unsupported_profile: scenery.events/v1", Address: resource.Address})
			}
		case "scenery.page/v1", "scenery.renderer/v1":
			if !active["scenery.ui/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7006", Severity: "error", Message: "unsupported_profile: scenery.ui/v1", Address: resource.Address})
			}
		case "scenery.patch/v1":
			if !active["scenery.patches/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7007", Severity: "error", Message: "unsupported_profile: scenery.patches/v1", Address: resource.Address})
			}
		}
	}
	return diagnostics
}

func literalStringList(block *Block, name string) []string {
	expression, ok := block.Attributes[name]
	if !ok {
		return nil
	}
	values, ok := expression.Value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			result = append(result, item)
		}
	}
	return result
}

func matchesAnyGlob(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchGlobSegments(strings.Split(filepath.ToSlash(pattern), "/"), strings.Split(filepath.ToSlash(value), "/")) {
			return true
		}
	}
	return false
}

func matchGlobSegments(pattern, value []string) bool {
	if len(pattern) == 0 {
		return len(value) == 0
	}
	if pattern[0] == "**" {
		for index := 0; index <= len(value); index++ {
			if matchGlobSegments(pattern[1:], value[index:]) {
				return true
			}
		}
		return false
	}
	if len(value) == 0 {
		return false
	}
	return matchGlobSegment(pattern[0], value[0]) && matchGlobSegments(pattern[1:], value[1:])
}

func matchGlobSegment(pattern, value string) bool {
	patternRunes, valueRunes := []rune(pattern), []rune(value)
	type state struct{ pattern, value int }
	memo := map[state]bool{}
	visited := map[state]bool{}
	var match func(int, int) bool
	match = func(patternIndex, valueIndex int) bool {
		key := state{patternIndex, valueIndex}
		if visited[key] {
			return memo[key]
		}
		visited[key] = true
		matched := false
		switch {
		case patternIndex == len(patternRunes):
			matched = valueIndex == len(valueRunes)
		case patternRunes[patternIndex] == '*':
			matched = match(patternIndex+1, valueIndex) || valueIndex < len(valueRunes) && match(patternIndex, valueIndex+1)
		case valueIndex < len(valueRunes) && (patternRunes[patternIndex] == '?' || patternRunes[patternIndex] == valueRunes[valueIndex]):
			matched = match(patternIndex+1, valueIndex+1)
		}
		memo[key] = matched
		return matched
	}
	return match(0, 0)
}

func containsBase(paths []string, base string) bool {
	for _, path := range paths {
		if filepath.Base(path) == base {
			return true
		}
	}
	return false
}
func hasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
func sourceIDForRange(rng Range) string { return rng.SourceID }
func diagnosticForBlock(code, message string, block *Block) Diagnostic {
	rng := block.Range
	return Diagnostic{Code: code, Severity: "error", Message: message, Range: &rng}
}
