package vnext

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var rootSingletons = map[string]bool{"language": true, "application": true, "workspace": true}

var rootResourceKinds = map[string]bool{
	"go_module": true, "go_toolchain": true, "go_target": true,
	"http_gateway": true, "authentication": true, "authorization": true,
	"pipeline": true, "provider": true, "data_source": true,
	"execution_engine": true, "event_bus": true, "secret_store": true,
	"secret": true, "deployment": true, "typescript_client": true,
}

var packageResourceKinds = map[string]bool{
	"service": true, "record": true, "enum": true, "union": true,
	"operation": true, "execution": true, "binding": true,
	"schedule": true, "event": true, "event_emission": true,
	"entity": true, "view": true, "crud": true, "fixture": true,
	"page": true, "renderer": true, "middleware": true,
}

func Compile(root string) (*Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	result := &Result{Root: absRoot}
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

	manifest, diagnostics, packageSources := compileSources(absRoot, result.Sources, migration)
	result.Sources = append(result.Sources, packageSources...)
	workspaceRevision, err := computeWorkspaceRevision(absRoot, result.Sources, migration)
	if err != nil {
		return nil, err
	}
	result.WorkspaceRevision = workspaceRevision
	result.Manifest = manifest
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	if manifest != nil {
		manifest.Diagnostics = append([]Diagnostic(nil), result.Diagnostics...)
	}
	return result, nil
}

func compileSources(root string, sources []*Source, migration *Migration) (*Manifest, []Diagnostic, []*Source) {
	var diagnostics []Diagnostic
	var packageSources []*Source
	allBlocks := blocksFromSources(sources)
	counts := map[string]int{}
	var language, application *Block
	var modules []*Block
	resources := []Resource{}
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
		case rootResourceKinds[block.Type]:
			resource, diag := resourceFromBlock("app", block, item.Source.ID)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			} else {
				resources = append(resources, resource)
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

	seenModules := map[string]bool{}
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
		sourcePath, err := requireLiteralString(module, "source")
		if err != nil {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN3002", err.Error(), module))
			continue
		}
		if filepath.IsAbs(sourcePath) || strings.HasPrefix(filepath.Clean(sourcePath), "..") {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN3003", "local module source must be workspace-relative", module))
			continue
		}
		moduleDir := filepath.Join(root, filepath.FromSlash(sourcePath))
		moduleResources, moduleSources, moduleDiags := compilePackage(root, moduleDir, name)
		resources = append(resources, moduleResources...)
		diagnostics = append(diagnostics, moduleDiags...)
		for _, source := range moduleSources {
			sourceMap[source.ID] = SourceRecord{URI: source.Relative}
			packageSources = append(packageSources, source)
		}
		moduleResource, diag := resourceFromBlock("app", module, sourceIDForRange(module.Range))
		if diag != nil {
			diagnostics = append(diagnostics, *diag)
		} else {
			resources = append(resources, moduleResource)
		}
	}

	applyMigration(resources, migration)
	legacyResources, legacyDiagnostics := lowerLegacyResources(root, migration, resources)
	resources = append(resources, legacyResources...)
	diagnostics = append(diagnostics, legacyDiagnostics...)
	diagnostics = append(diagnostics, validateResources(resources, migration)...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
	profiles := profilesFromLanguage(language)
	if len(profiles) == 0 {
		profiles = append([]string(nil), KernelProfiles...)
	}
	profiles = normalizeProfiles(profiles, resources)
	diagnostics = append(diagnostics, validateProfiles(profiles, resources)...)
	revision, err := contractRevision(resources, append([]string(nil), profiles...), appName)
	if err != nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN9002", Severity: "error", Message: err.Error()})
	}
	manifest := &Manifest{APIVersion: ManifestVersion, Edition: Edition, DiagnosticCatalog: DiagnosticCatalog, Application: ApplicationIdentity{Name: appName, Version: appVersion}, Profiles: profiles, ContractRevision: revision, Resources: resources, SourceMap: sourceMap, Diagnostics: []Diagnostic{}}
	return manifest, diagnostics, packageSources
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
		source, diags := parseSource(root, path)
		if source != nil {
			sources = append(sources, source)
		}
		diagnostics = append(diagnostics, diags...)
	}
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
	return resources, sources, diagnostics
}

func resourceFromBlock(module string, block *Block, sourceID string) (Resource, *Diagnostic) {
	if len(block.Labels) != 1 {
		d := diagnosticForBlock("SCN1006", block.Type+" requires one label", block)
		return Resource{}, &d
	}
	name := block.Labels[0]
	spec := blockSpec(block)
	return Resource{Address: resourceAddress(module, block.Type, name), Kind: kindForBlock(block.Type), Name: name, Module: module, Spec: spec, Origin: Origin{Kind: "authored", SourceID: sourceID}}, nil
}

func blockSpec(block *Block) map[string]any {
	spec := make(map[string]any, len(block.Attributes)+len(block.Blocks))
	for name, expression := range block.Attributes {
		spec[name] = expressionValue(expression)
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

func validateResources(resources []Resource, migration *Migration) []Diagnostic {
	var diagnostics []Diagnostic
	byAddress := map[string]Resource{}
	routes := map[string]string{}
	for _, resource := range resources {
		if previous, ok := byAddress[resource.Address]; ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1104", Severity: "error", Message: "duplicate resource address " + resource.Address, Address: resource.Address, Related: []Related{{Address: previous.Address}}})
		}
		byAddress[resource.Address] = resource
		if resource.Kind != "scenery.binding/v1" {
			continue
		}
		protocol, _ := resource.Spec["protocol"].(string)
		if protocol != "http" {
			continue
		}
		httpSpec, _ := resource.Spec["http"].(map[string]any)
		method, _ := httpSpec["method"].(string)
		path, _ := httpSpec["path"].(string)
		gateway := refString(resource.Spec["gateway"])
		if method == "" || path == "" || gateway == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2001", Severity: "error", Message: "HTTP binding requires gateway, method, and path", Address: resource.Address})
			continue
		}
		key := strings.ToUpper(method) + " " + gateway + " " + canonicalRoute(path)
		if owner, ok := routes[key]; ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2002", Severity: "error", Message: "duplicate HTTP route " + key, Address: resource.Address, Related: []Related{{Address: owner}}})
		} else {
			routes[key] = resource.Address
		}
	}
	if migration != nil {
		diagnostics = append(diagnostics, migration.validate(resources)...)
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
	if set["scenery.runtime-http/v1"] {
		set["scenery.http-codec/v1"] = true
	}
	for _, resource := range resources {
		if resource.Kind == "scenery.typescript-client/v1" {
			set["scenery.typescript-client/v1"] = true
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
			if mode, _ := resource.Spec["mode"].(string); mode != "" && mode != "direct" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7002", Severity: "error", Message: "unsupported_profile: execution mode " + mode, Address: resource.Address})
			}
		case "scenery.data-source/v1", "scenery.entity/v1", "scenery.view/v1", "scenery.crud/v1", "scenery.fixture/v1":
			if !active["scenery.data/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7003", Severity: "error", Message: "unsupported_profile: scenery.data/v1", Address: resource.Address})
			}
		case "scenery.deployment/v1":
			if !active["scenery.deployment/v1"] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN7004", Severity: "error", Message: "unsupported_profile: scenery.deployment/v1", Address: resource.Address})
			}
		}
	}
	return diagnostics
}

func computeWorkspaceRevision(root string, sources []*Source, migration *Migration) (string, error) {
	entries := map[string][]byte{}
	for _, source := range sources {
		entries[source.Relative] = source.Bytes
	}
	if migration != nil {
		b, err := os.ReadFile(filepath.Join(root, "scenery.migration.scn"))
		if err != nil {
			return "", err
		}
		entries["scenery.migration.scn"] = b
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	_, _ = h.Write([]byte("scenery.workspace-revision.v1\x00"))
	for _, path := range paths {
		_ = binary.Write(h, binary.BigEndian, uint64(len([]byte(path))))
		_, _ = h.Write([]byte(path))
		_ = binary.Write(h, binary.BigEndian, uint64(len(entries[path])))
		_, _ = h.Write(entries[path])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
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

func MarshalCanonical(value any) ([]byte, error) {
	return json.Marshal(value)
}
