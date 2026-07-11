package vnext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
)

type Migration struct {
	Frontend         string                `json:"frontend"`
	LegacyConfig     string                `json:"legacy_config,omitempty"`
	LegacyGateways   []MigrationGateway    `json:"legacy_gateways,omitempty"`
	Services         []MigrationService    `json:"services"`
	Source           *Source               `json:"-"`
	LegacyCandidates map[string][]Resource `json:"-"`
	NativeCandidates map[string][]Resource `json:"-"`
}

type MigrationGateway struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type MigrationService struct {
	Name                          string                  `json:"name"`
	State                         string                  `json:"state"`
	Active                        string                  `json:"active"`
	Package                       string                  `json:"package,omitempty"`
	Module                        string                  `json:"module,omitempty"`
	LegacyTarget                  string                  `json:"legacy_target,omitempty"`
	Namespace                     string                  `json:"namespace,omitempty"`
	LegacyCandidateDigest         string                  `json:"legacy_candidate_digest,omitempty"`
	NativeCandidateDigest         string                  `json:"native_candidate_digest,omitempty"`
	ComparisonDigest              string                  `json:"comparison_digest,omitempty"`
	RollbackSafety                string                  `json:"rollback_safety,omitempty"`
	GuaranteeClassification       string                  `json:"guarantee_classification,omitempty"`
	MigrationDisposition          string                  `json:"migration_disposition,omitempty"`
	CutoverClasses                []string                `json:"cutover_classes,omitempty"`
	LifecycleAdapter              string                  `json:"lifecycle_adapter,omitempty"`
	RemainingOperationBridgeCount int                     `json:"remaining_operation_bridge_count"`
	AdapterRetirementReady        bool                    `json:"adapter_retirement_ready"`
	LegacyCandidateValid          bool                    `json:"legacy_candidate_valid"`
	NativeCandidateValid          bool                    `json:"native_candidate_valid"`
	CandidateDiagnostics          map[string][]Diagnostic `json:"candidate_diagnostics,omitempty"`
}

func parseMigration(root string) (*Migration, []Diagnostic) {
	path := filepath.Join(root, "scenery.migration.scn")
	info, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		return nil, nil
	}
	if statErr != nil {
		return nil, []Diagnostic{{Code: "SCN5002", Severity: "error", Message: statErr.Error()}}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, []Diagnostic{{Code: "SCN5002", Severity: "error", Message: "scenery.migration.scn must be a regular non-symlink file"}}
	}
	source, diagnostics := parseSource(root, path)
	if source == nil || len(source.Blocks) == 0 {
		return nil, diagnostics
	}
	if len(source.Blocks) != 1 || source.Blocks[0].Type != "migration" {
		return nil, append(diagnostics, Diagnostic{Code: "SCN5003", Severity: "error", Message: "scenery.migration.scn requires exactly one migration block"})
	}
	diagnostics = append(diagnostics, validateMigrationAuthoredSchema(source)...)
	block := source.Blocks[0]
	migration := &Migration{Source: source}
	migration.Frontend, _ = literalString(block, "frontend")
	migration.LegacyConfig, _ = literalString(block, "legacy_config")
	if migration.Frontend != "scenery.legacy.v0" {
		diagnostics = append(diagnostics, diagnosticForBlock("SCN5004", "migration frontend must be \"scenery.legacy.v0\"", block))
	}
	for _, child := range block.Blocks {
		if child.Type == "legacy_gateway" {
			if len(child.Labels) != 1 {
				diagnostics = append(diagnostics, diagnosticForBlock("SCN5006", "legacy_gateway requires one label", child))
				continue
			}
			gateway := MigrationGateway{Name: child.Labels[0]}
			if expression, ok := child.Attributes["target"]; ok {
				gateway.Target = expression.Traversal
			}
			migration.LegacyGateways = append(migration.LegacyGateways, gateway)
			continue
		}
		if child.Type != "legacy_service" && child.Type != "shadow_service" && child.Type != "native_service" {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN5005", "unknown migration block "+child.Type, child))
			continue
		}
		if len(child.Labels) != 1 {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN5006", child.Type+" requires one service label", child))
			continue
		}
		service := MigrationService{Name: child.Labels[0]}
		switch child.Type {
		case "legacy_service":
			service.State, service.Active = "legacy", "legacy"
		case "shadow_service":
			service.State = "shadow"
			service.Active, _ = literalString(child, "active")
			if service.Active != "legacy" && service.Active != "native" {
				diagnostics = append(diagnostics, diagnosticForBlock("SCN5007", "shadow_service active must be legacy or native", child))
			}
		case "native_service":
			service.State, service.Active = "native", "native"
		}
		service.Package, _ = literalString(child, "package")
		service.Namespace, _ = literalString(child, "namespace")
		if service.Namespace == "" {
			service.Namespace = service.Name
		}
		if expression, ok := child.Attributes["module"]; ok {
			service.Module = expression.Traversal
		}
		if expression, ok := child.Attributes["target"]; ok {
			service.LegacyTarget = expression.Traversal
		}
		if expression, ok := child.Attributes["legacy_target"]; ok {
			service.LegacyTarget = expression.Traversal
		}
		migration.Services = append(migration.Services, service)
	}
	sort.Slice(migration.Services, func(i, j int) bool { return migration.Services[i].Name < migration.Services[j].Name })
	sort.Slice(migration.LegacyGateways, func(i, j int) bool { return migration.LegacyGateways[i].Name < migration.LegacyGateways[j].Name })
	return migration, diagnostics
}

func (m *Migration) validate(root string, resources []Resource) []Diagnostic {
	if m == nil {
		return nil
	}
	var diagnostics []Diagnostic
	if m.LegacyConfig == "" {
		discoveredRoot, _, err := appcfg.DiscoverRoot(root)
		if err == nil && samePath(discoveredRoot, root) || err != nil && !errors.Is(err, appcfg.ErrRootNotFound) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5106", Severity: "error", Message: "legacy_config may be omitted only after the workspace app config has been removed"})
		}
	} else {
		cleanConfig := filepath.ToSlash(filepath.Clean(filepath.FromSlash(m.LegacyConfig)))
		configPath := filepath.Join(root, filepath.FromSlash(cleanConfig))
		configInfo, configErr := os.Lstat(configPath)
		if m.LegacyConfig != cleanConfig || filepath.IsAbs(m.LegacyConfig) || strings.Contains(m.LegacyConfig, "\\") || cleanConfig == "." || cleanConfig == ".." || strings.HasPrefix(cleanConfig, "../") || !pathWithin(root, configPath) || configErr != nil || configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.Mode().IsRegular() {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5106", Severity: "error", Message: "legacy_config must identify the normalized regular non-symlink app config selected for this workspace"})
		} else if discoveredRoot, config, err := appcfg.DiscoverRoot(root); err != nil || !samePath(discoveredRoot, root) || filepath.ToSlash(config.SourceRelPath(root)) != cleanConfig {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5106", Severity: "error", Message: "legacy_config must identify the app config selected for this workspace"})
		}
	}
	resourcesByAddress := resourcesByAddress(&Manifest{Resources: resources})
	seenGateways := map[string]bool{}
	defaultGateway := false
	for _, gateway := range m.LegacyGateways {
		if seenGateways[gateway.Name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5107", Severity: "error", Message: "duplicate legacy gateway " + gateway.Name})
			continue
		}
		seenGateways[gateway.Name] = true
		defaultGateway = defaultGateway || gateway.Name == "default"
		address := migrationGatewayAddress(gateway.Target)
		resource, ok := resourcesByAddress[address]
		if gateway.Target == "" || address == gateway.Target || !ok || resource.Kind != "scenery.http-gateway/v1" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5107", Severity: "error", Message: "legacy gateway " + gateway.Name + " must target a declared root http_gateway"})
		}
	}
	legacyServices := false
	for _, service := range m.Services {
		legacyServices = legacyServices || service.State != "native"
	}
	if legacyServices && !defaultGateway {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN5107", Severity: "error", Message: "mixed mode requires a legacy_gateway \"default\" target"})
	}
	seen := map[string]bool{}
	seenNamespaces := map[string]bool{}
	modules := map[string]bool{}
	for _, resource := range resources {
		if resource.Kind == "scenery.module/v1" {
			modules[resource.Name] = true
		}
	}
	for _, service := range m.Services {
		if seen[service.Name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5101", Severity: "error", Message: "duplicate migration service " + service.Name})
			continue
		}
		seen[service.Name] = true
		namespace := migrationServiceNamespace(service)
		if !validSemanticName(namespace) || seenNamespaces[namespace] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5108", Severity: "error", Message: "migration namespace must be a unique semantic name: " + namespace})
		}
		seenNamespaces[namespace] = true
		if service.State != "legacy" && !modules[service.Name] && strings.TrimPrefix(service.Module, "module.") != service.Name {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5102", Severity: "error", Message: "native migration service " + service.Name + " has no installed module"})
		}
		if service.State != "native" && strings.TrimSpace(service.Package) == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5103", Severity: "error", Message: "legacy migration service " + service.Name + " requires package"})
		}
		if service.State != "native" && strings.TrimSpace(service.LegacyTarget) == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5104", Severity: "error", Message: "legacy migration service " + service.Name + " requires an explicit Go target"})
		}
		if service.State != "native" {
			clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(service.Package)))
			canonical := "./" + strings.TrimPrefix(clean, "./")
			path := filepath.Join(root, filepath.FromSlash(clean))
			if service.Package != canonical || strings.Contains(service.Package, "\\") || filepath.IsAbs(service.Package) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !pathWithin(root, path) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN5105", Severity: "error", Message: "legacy migration package must be workspace-relative and confined: " + service.Name})
			} else if info, err := os.Stat(path); err != nil || !info.IsDir() {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN5105", Severity: "error", Message: "legacy migration package is unavailable: " + service.Name})
			} else if err := rejectPathSymlinks(root, path); err != nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN5105", Severity: "error", Message: "legacy migration package is not symlink-safe: " + service.Name})
			}
		}
	}
	return diagnostics
}

func migrationGatewayAddress(reference string) string {
	parts := strings.Split(reference, ".")
	if len(parts) != 2 || parts[0] != "http_gateway" || parts[1] == "" {
		return reference
	}
	return resourceAddress("app", parts[0], parts[1])
}

func samePath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}

func (m *Migration) defaultLegacyGatewayAddress() string {
	if m == nil {
		return ""
	}
	for _, gateway := range m.LegacyGateways {
		if gateway.Name == "default" {
			return migrationGatewayAddress(gateway.Target)
		}
	}
	return ""
}

func linkMigrationResources(native, legacy []Resource, migration *Migration) ([]Resource, []Diagnostic) {
	if migration == nil {
		return native, nil
	}
	migration.NativeCandidates = map[string][]Resource{}
	migration.LegacyCandidates = map[string][]Resource{}
	moduleToService := map[string]string{}
	namespaceToService := map[string]string{}
	for _, service := range migration.Services {
		module := strings.TrimPrefix(service.Module, "module.")
		if module == "" {
			module = service.Name
		}
		moduleToService[module] = service.Name
		namespaceToService[migrationServiceNamespace(service)] = service.Name
	}
	active := make([]Resource, 0, len(native)+len(legacy))
	sharedPackageTypes := migrationSharedPackageTypes(native)
	var diagnostics []Diagnostic
	for _, resource := range native {
		if resource.Module == "app" || resource.Kind == "scenery.module/v1" || sharedPackageTypes[resource.Address] {
			active = append(active, resource)
			continue
		}
		serviceName := moduleToService[resource.Module]
		if serviceName == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5301", Severity: "error", Message: "native service module " + resource.Module + " is absent from the migration ownership inventory", Address: resource.Address})
			continue
		}
		migration.NativeCandidates[serviceName] = append(migration.NativeCandidates[serviceName], resource)
	}
	for _, resource := range legacy {
		if resource.Module == "app" {
			active = append(active, resource)
			continue
		}
		serviceName := namespaceToService[resource.Module]
		if serviceName == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5301", Severity: "error", Message: "legacy namespace " + resource.Module + " is absent from the migration ownership inventory", Address: resource.Address})
			continue
		}
		migration.LegacyCandidates[serviceName] = append(migration.LegacyCandidates[serviceName], resource)
	}
	for index := range migration.Services {
		service := &migration.Services[index]
		nativeCandidate, legacyCandidate := migration.NativeCandidates[service.Name], migration.LegacyCandidates[service.Name]
		if service.State == "shadow" && (len(nativeCandidate) == 0 || len(legacyCandidate) == 0) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5302", Severity: "error", Message: "shadow service " + service.Name + " requires complete legacy and native candidates"})
		}
		if service.Active == "legacy" && len(legacyCandidate) == 0 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5303", Severity: "error", Message: "active legacy service " + service.Name + " has no legacy candidate"})
		}
		if service.Active == "native" && len(nativeCandidate) == 0 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5304", Severity: "error", Message: "active native service " + service.Name + " has no native candidate"})
		}
		service.LegacyCandidateDigest = migrationCandidateDigest("legacy", legacyCandidate)
		service.NativeCandidateDigest = migrationCandidateDigest("native", nativeCandidate)
		service.RollbackSafety = "safe"
		service.CutoverClasses = migrationCutoverClasses(nativeCandidate)
		service.GuaranteeClassification = "verified"
		service.MigrationDisposition = "native_equivalent"
		if len(legacyCandidate) > 0 && !migrationCandidateContractComplete(legacyCandidate) {
			service.GuaranteeClassification = "advisory"
			service.MigrationDisposition = "advisory"
		}
		if service.State == "native" {
			service.RollbackSafety = "unavailable"
		}
		if service.Active == "legacy" {
			active = append(active, legacyCandidate...)
		} else {
			active = append(active, nativeCandidate...)
		}
	}
	active = pruneInactiveMigrationExports(active)
	sort.Slice(active, func(i, j int) bool { return active[i].Address < active[j].Address })
	return active, diagnostics
}

func pruneInactiveMigrationExports(resources []Resource) []Resource {
	available := map[string]bool{}
	for _, resource := range resources {
		available[resource.Address] = true
	}
	result := append([]Resource(nil), resources...)
	for index := range result {
		if result[index].Kind != "scenery.module/v1" {
			continue
		}
		result[index].Spec = cloneMapValue(result[index].Spec)
		for _, field := range []string{"exports", "export_metadata"} {
			values, ok := result[index].Spec[field].(map[string]any)
			if !ok {
				continue
			}
			for name, value := range values {
				missing := false
				walkRefs(value, "/spec/"+field+"/"+escapeJSONPointer(name), func(_ string, reference string) {
					if strings.Contains(reference, "/") && !available[reference] {
						missing = true
					}
				})
				if missing {
					delete(values, name)
				}
			}
			result[index].Spec[field] = values
		}
	}
	return result
}

func migrationCandidateDigest(frontend string, resources []Resource) string {
	if len(resources) == 0 {
		return ""
	}
	projected := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		copy := Resource{Address: resource.Address, Kind: resource.Kind, Name: resource.Name, Module: resource.Module, Spec: resource.Spec}
		projected = append(projected, copy)
	}
	return revisionHash("scenery.migration-candidate.v1\x00", map[string]any{"frontend": frontend, "resources": projected})
}

func validateMigrationCandidateGraphs(root string, active []Resource, migration *Migration) {
	if migration == nil {
		return
	}
	shared := make([]Resource, 0)
	sharedPackageTypes := migrationSharedPackageTypes(active)
	for _, resource := range active {
		if resource.Module == "app" || resource.Kind == "scenery.module/v1" || sharedPackageTypes[resource.Address] {
			shared = append(shared, resource)
		}
	}
	for index := range migration.Services {
		service := &migration.Services[index]
		service.CandidateDiagnostics = map[string][]Diagnostic{}
		if candidate := migration.LegacyCandidates[service.Name]; len(candidate) > 0 {
			diagnostics := validateResources(root, append(append([]Resource(nil), shared...), candidate...), nil)
			service.CandidateDiagnostics["legacy"] = diagnostics
			service.LegacyCandidateValid = !hasErrors(diagnostics)
		}
		if candidate := migration.NativeCandidates[service.Name]; len(candidate) > 0 {
			diagnostics := validateResources(root, append(append([]Resource(nil), shared...), candidate...), nil)
			service.CandidateDiagnostics["native"] = diagnostics
			service.NativeCandidateValid = !hasErrors(diagnostics)
		}
	}
}

func migrationSharedPackageTypes(resources []Resource) map[string]bool {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	shared := map[string]bool{}
	for _, module := range resources {
		if module.Kind != "scenery.module/v1" {
			continue
		}
		exports, _ := module.Spec["exports"].(map[string]any)
		for _, value := range exports {
			collectABITypeReferences(value, moduleInstancePath(module), byAddress, shared)
		}
	}
	for changed := true; changed; {
		changed = false
		for address := range shared {
			resource, ok := byAddress[address]
			if !ok || !isNamedContractType(resource) {
				continue
			}
			for _, value := range contractTypeValues([]Resource{resource}) {
				before := len(shared)
				collectABITypeReferences(value, resource.Module, byAddress, shared)
				changed = changed || before != len(shared)
			}
		}
	}
	return shared
}

func applyMigration(resources []Resource, migration *Migration) {
	if migration == nil {
		return
	}
	states := map[string]MigrationService{}
	for _, service := range migration.Services {
		states[service.Name] = service
		states[migrationServiceNamespace(service)] = service
	}
	for i := range resources {
		resource := &resources[i]
		lookup := resource.Module
		if resource.Kind == "scenery.module/v1" {
			lookup = moduleInstancePath(*resource)
		}
		service, ok := states[lookup]
		if !ok {
			for _, candidate := range migration.Services {
				module := strings.TrimPrefix(candidate.Module, "module.")
				if module != "" && module == lookup {
					service, ok = candidate, true
					break
				}
			}
		}
		if !ok {
			continue
		}
		resource.Migration = &MigrationMeta{State: service.State, Active: service.Active}
		if service.State == "shadow" {
			resource.Migration.NativeCandidate = resource.Address
		}
	}
}

func pathExists(path string) bool { _, err := os.Stat(path); return err == nil }

type MigrationDifference struct {
	Dimension      string `json:"dimension"`
	Address        string `json:"address"`
	Path           string `json:"path,omitempty"`
	Legacy         any    `json:"legacy,omitempty"`
	Native         any    `json:"native,omitempty"`
	Classification string `json:"classification"`
}

type MigrationComparison struct {
	APIVersion            string                `json:"api_version"`
	Service               string                `json:"service"`
	State                 string                `json:"state"`
	Active                string                `json:"active"`
	EvidenceMode          string                `json:"evidence_mode"`
	LegacyCandidateDigest string                `json:"legacy_candidate_digest,omitempty"`
	NativeCandidateDigest string                `json:"native_candidate_digest,omitempty"`
	ComparisonDigest      string                `json:"comparison_digest"`
	Equal                 bool                  `json:"equal"`
	Complete              bool                  `json:"complete"`
	Differences           []MigrationDifference `json:"differences"`
	SemanticDiff          SemanticDiff          `json:"semantic_diff"`
}

func CompareMigrationService(result *Result, serviceName string) (MigrationComparison, error) {
	if result == nil || result.Migration == nil {
		return MigrationComparison{}, fmt.Errorf("migration service %q is unavailable outside mixed mode", serviceName)
	}
	service, err := migrationService(result.Migration, serviceName)
	if err != nil {
		return MigrationComparison{}, err
	}
	legacy := result.Migration.LegacyCandidates[serviceName]
	native := result.Migration.NativeCandidates[serviceName]
	if len(legacy) == 0 || len(native) == 0 {
		return MigrationComparison{}, fmt.Errorf("migration service %s does not have both candidate graphs", serviceName)
	}
	diff := CompareManifests(
		&Manifest{Resources: migrationComparisonResources(legacy)},
		&Manifest{Resources: migrationComparisonResources(native)},
		CompareOptions{View: "expanded", Scope: serviceName},
	)
	differences := make([]MigrationDifference, 0)
	for _, change := range diff.Changes {
		added := false
		for _, dimension := range diff.Dimensions {
			classification := change.Classifications[dimension]
			if !classification.Applicable || classification.Result == CompatibilityCompatible || classification.Relation == SecurityEqual {
				continue
			}
			resultValue := classification.Result
			if resultValue == "" {
				resultValue = classification.Relation
			}
			differences = append(differences, MigrationDifference{Dimension: dimension, Address: change.Address, Path: change.Path, Legacy: change.Base, Native: change.Target, Classification: resultValue})
			added = true
		}
		if !added {
			differences = append(differences, MigrationDifference{Dimension: "source", Address: change.Address, Path: change.Path, Legacy: change.Base, Native: change.Target, Classification: CompatibilityUnknown})
		}
	}
	complete := service.LegacyCandidateValid && service.NativeCandidateValid && migrationCandidateContractComplete(legacy) && migrationCandidateContractComplete(native) && migrationDifferencesComplete(differences)
	comparison := MigrationComparison{
		APIVersion: "scenery.migrate.compare.v1", Service: serviceName, State: service.State, Active: service.Active, EvidenceMode: "static_contract",
		LegacyCandidateDigest: service.LegacyCandidateDigest, NativeCandidateDigest: service.NativeCandidateDigest,
		Equal: complete && len(differences) == 0, Complete: complete, Differences: differences, SemanticDiff: diff,
	}
	comparison.ComparisonDigest = revisionHash("scenery.migration-comparison.v1\x00", map[string]any{"service": serviceName, "legacy": comparison.LegacyCandidateDigest, "native": comparison.NativeCandidateDigest, "mode": comparison.EvidenceMode, "differences": differences, "complete": complete})
	return comparison, nil
}

func migrationComparisonResources(resources []Resource) []Resource {
	projected := cloneResourceView(resources)
	for index := range projected {
		projected[index].Spec, _ = migrationComparisonValue(projected[index], projected[index].Spec).(map[string]any)
	}
	return projected
}

func migrationComparisonValue(resource Resource, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if reference := refString(typed); reference != "" {
			return canonicalMigrationReference(resource, reference)
		}
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized := migrationComparisonValue(resource, item)
			if migrationComparisonEmpty(normalized) {
				continue
			}
			result[key] = normalized
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = migrationComparisonValue(resource, item)
		}
		return result
	default:
		return value
	}
}

func migrationComparisonEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func canonicalMigrationReference(resource Resource, reference string) string {
	if strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 || (!rootResourceKinds[parts[0]] && !packageResourceKinds[parts[0]] && parts[0] != "module") {
		return reference
	}
	module := resource.Module
	if rootResourceKinds[parts[0]] || parts[0] == "module" {
		module = "app"
	}
	return resourceAddress(module, parts[0], parts[1])
}

func migrationDifferencesComplete(differences []MigrationDifference) bool {
	for _, difference := range differences {
		if difference.Classification == CompatibilityUnknown || difference.Classification == SecurityUnknown || difference.Classification == "opaque" {
			return false
		}
	}
	return true
}

func migrationCandidateContractComplete(resources []Resource) bool {
	for _, resource := range resources {
		if resource.Compatibility != nil && resource.Compatibility.Contract != "verified" {
			return false
		}
		if semanticValueContains(resource.Spec, "legacy.type.advisory") || semanticValueContains(resource.Spec, "opaque") || semanticValueContainsExceptAdvisoryHTTPGuarantee(resource.Spec, "advisory") || semanticValueContains(resource.Spec, "unsupported") {
			return false
		}
	}
	return true
}

func semanticValueContainsExceptAdvisoryHTTPGuarantee(value any, target string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "guarantee" && item == "advisory" {
				continue
			}
			if semanticValueContainsExceptAdvisoryHTTPGuarantee(item, target) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if semanticValueContainsExceptAdvisoryHTTPGuarantee(item, target) {
				return true
			}
		}
	case string:
		return typed == target
	}
	return false
}

func semanticValueContains(value any, target string) bool {
	switch typed := value.(type) {
	case string:
		return typed == target
	case map[string]any:
		for _, item := range typed {
			if semanticValueContains(item, target) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if semanticValueContains(item, target) {
				return true
			}
		}
	}
	return false
}

func migrationService(migration *Migration, name string) (MigrationService, error) {
	if migration != nil {
		for _, service := range migration.Services {
			if service.Name == name {
				return service, nil
			}
		}
	}
	return MigrationService{}, fmt.Errorf("migration service %q not found", name)
}
