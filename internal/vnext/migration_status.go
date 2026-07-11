package vnext

import (
	"fmt"
	"sort"
	"strings"
)

type MigrationStatus struct {
	APIVersion               string                     `json:"api_version"`
	Mode                     string                     `json:"mode"`
	Frontend                 string                     `json:"frontend,omitempty"`
	LegacyConfig             string                     `json:"legacy_config,omitempty"`
	Ready                    bool                       `json:"ready"`
	Services                 []MigrationService         `json:"services"`
	Constructs               []MigrationConstructStatus `json:"constructs"`
	WorkspaceRevision        string                     `json:"workspace_revision"`
	ContractRevision         string                     `json:"contract_revision,omitempty"`
	OperationalStateRevision string                     `json:"operational_state_revision,omitempty"`
	Diagnostics              []Diagnostic               `json:"diagnostics"`
}

type MigrationConstructStatus struct {
	Address                  string                             `json:"address"`
	Kind                     string                             `json:"kind"`
	Service                  string                             `json:"service"`
	State                    string                             `json:"state"`
	ActiveOwner              string                             `json:"active_owner"`
	ShadowOwner              string                             `json:"shadow_owner,omitempty"`
	GuaranteeClassification  string                             `json:"guarantee_classification"`
	MigrationDisposition     string                             `json:"migration_disposition"`
	LegacyCandidateDigest    string                             `json:"legacy_candidate_digest,omitempty"`
	NativeCandidateDigest    string                             `json:"native_candidate_digest,omitempty"`
	ComparisonDigest         string                             `json:"comparison_digest,omitempty"`
	RequiredProfiles         []string                           `json:"required_profiles"`
	MissingProfiles          []string                           `json:"missing_profiles"`
	CutoverClass             string                             `json:"cutover_class"`
	StatefulOperationalState MigrationStatefulOperationalStatus `json:"stateful_operational_state"`
	ExternalIdentities       []string                           `json:"external_identities"`
	ExternalAliases          []string                           `json:"external_aliases"`
	GeneratedArtifacts       []string                           `json:"generated_artifacts"`
	DeployedConsumerGates    []MigrationDeployedConsumerGate    `json:"deployed_consumer_gates"`
	CLIProtocolDependencies  []MigrationCLIProtocolDependency   `json:"cli_protocol_dependencies"`
	OperationalEvidence      map[string][]string                `json:"operational_evidence"`
	OperationalStateRevision string                             `json:"operational_state_revision"`
	OperationalReady         bool                               `json:"operational_ready"`
	SemanticBlocking         bool                               `json:"semantic_blocking"`
	RollbackSafety           string                             `json:"rollback_safety"`
	Blocking                 bool                               `json:"blocking"`
	Diagnostics              []Diagnostic                       `json:"diagnostics"`
}

type MigrationStatefulOperationalStatus struct {
	Drain  string `json:"drain"`
	Fence  string `json:"fence"`
	Cursor string `json:"cursor"`
}

type MigrationDeployedConsumerGate struct {
	Artifact string   `json:"artifact"`
	State    string   `json:"state"`
	Evidence []string `json:"evidence"`
}

type MigrationCLIProtocolDependency struct {
	APIVersion string `json:"api_version"`
	State      string `json:"state"`
}

func BuildMigrationStatus(result *Result) MigrationStatus {
	status := MigrationStatus{APIVersion: "scenery.migrate.status.v1", Mode: "native_only", Ready: result != nil && result.Valid(), Services: []MigrationService{}, Constructs: []MigrationConstructStatus{}, Diagnostics: []Diagnostic{}}
	if result == nil {
		status.Ready = false
		status.Diagnostics = []Diagnostic{internalDiagnostic("SCN9003", "missing compilation result")}
		return status
	}
	status.WorkspaceRevision = result.WorkspaceRevision
	status.Diagnostics = append(status.Diagnostics, result.Diagnostics...)
	if result.Manifest != nil {
		status.ContractRevision = result.Manifest.ContractRevision
	}
	if result.Migration != nil {
		status.Mode = "mixed"
		status.Frontend = result.Migration.Frontend
		status.LegacyConfig = result.Migration.LegacyConfig
		operationalState, operationalRevision, operationalErr := readMigrationFinishOperationalState(result.Root)
		status.OperationalStateRevision = operationalRevision
		operationalAvailable := operationalErr == nil
		if operationalErr != nil {
			status.Ready = false
			status.Diagnostics = append(status.Diagnostics, Diagnostic{Code: "SCN5600", Severity: "error", Message: "migration operational state is unavailable: " + operationalErr.Error()})
		}
		status.Services = append(status.Services, result.Migration.Services...)
		for index := range status.Services {
			service := &status.Services[index]
			service.LifecycleAdapter, service.RemainingOperationBridgeCount = migrationAdapterStatus(result.Migration, *service)
			service.AdapterRetirementReady = service.State == "native" && service.Active == "native" && service.LifecycleAdapter == "native_go_v1" && service.RemainingOperationBridgeCount == 0
			service.CutoverClasses = append([]string{}, service.CutoverClasses...)
			if service.CandidateDiagnostics == nil {
				service.CandidateDiagnostics = map[string][]Diagnostic{}
			}
			for key, items := range service.CandidateDiagnostics {
				service.CandidateDiagnostics[key] = append([]Diagnostic{}, items...)
			}
			if len(result.Migration.LegacyCandidates[service.Name]) > 0 && len(result.Migration.NativeCandidates[service.Name]) > 0 {
				if comparison, err := CompareMigrationService(result, service.Name); err == nil {
					service.ComparisonDigest = comparison.ComparisonDigest
				}
			}
			constructs := migrationConstructStatuses(result, *service, operationalState, operationalRevision, operationalAvailable)
			status.Constructs = append(status.Constructs, constructs...)
			for _, construct := range constructs {
				if construct.Blocking {
					status.Ready = false
				}
			}
			if service.Active == "legacy" || service.State == "shadow" || service.GuaranteeClassification != "verified" {
				status.Ready = false
			}
		}
		sort.Slice(status.Constructs, func(i, j int) bool { return status.Constructs[i].Address < status.Constructs[j].Address })
	}
	return status
}

func migrationAdapterStatus(migration *Migration, service MigrationService) (string, int) {
	if migration == nil {
		return "native_go_v1", 0
	}
	resources := migration.NativeCandidates[service.Name]
	if len(resources) == 0 {
		resources = migration.LegacyCandidates[service.Name]
	}
	lifecycle := "legacy_go_v0"
	remaining := 0
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.service/v1":
			implementation, _ := resource.Spec["implementation"].(map[string]any)
			if stringValue(implementation["adapter"]) != "legacy_go_v0" {
				lifecycle = "native_go_v1"
			}
		case "scenery.operation/v1":
			handler, _ := resource.Spec["handler"].(map[string]any)
			if stringValue(handler["adapter"]) == "legacy_go_v0" {
				remaining++
			}
		}
	}
	return lifecycle, remaining
}

func migrationConstructStatuses(result *Result, service MigrationService, operationalState migrationFinishOperationalState, operationalRevision string, operationalAvailable bool) []MigrationConstructStatus {
	legacy, native := result.Migration.LegacyCandidates[service.Name], result.Migration.NativeCandidates[service.Name]
	legacyByAddress, nativeByAddress := map[string]Resource{}, map[string]Resource{}
	addresses := map[string]bool{}
	for _, resource := range legacy {
		legacyByAddress[resource.Address], addresses[resource.Address] = resource, true
	}
	for _, resource := range native {
		nativeByAddress[resource.Address], addresses[resource.Address] = resource, true
	}
	activeProfiles := map[string]bool{}
	if result.Manifest != nil {
		for _, profile := range result.Manifest.Profiles {
			activeProfiles[profile] = true
		}
	}
	statuses := make([]MigrationConstructStatus, 0, len(addresses))
	operationalEvidence := migrationServiceOperationalEvidence(operationalState, service.Name)
	for _, address := range sortedBoolKeys(addresses) {
		legacyResource, hasLegacy := legacyByAddress[address]
		nativeResource, hasNative := nativeByAddress[address]
		resource := nativeResource
		if service.Active == "legacy" && hasLegacy || !hasNative {
			resource = legacyResource
		}
		guarantee, disposition := "verified", "native_equivalent"
		if hasLegacy && legacyResource.Compatibility != nil {
			guarantee = legacyResource.Compatibility.Contract
			disposition = legacyResource.Compatibility.MigrationDisposition
		}
		required := migrationResourceProfiles(resource)
		missing := []string{}
		for _, profile := range required {
			if !activeProfiles[profile] {
				missing = append(missing, profile)
			}
		}
		diagnostics := append([]Diagnostic{}, migrationResourceDiagnostics(service, address)...)
		semanticBlocking := guarantee != "verified" || disposition == "unsupported" || disposition == "opaque" || disposition == "rewrite_required" || disposition == "advisory" || len(missing) > 0 || hasErrorDiagnostic(diagnostics)
		shadowOwner := ""
		if service.State == "shadow" {
			if service.Active == "legacy" && hasNative {
				shadowOwner = "native"
			} else if service.Active == "native" && hasLegacy {
				shadowOwner = "legacy"
			}
		}
		classes := migrationCutoverClasses([]Resource{resource})
		retired := service.State == "native"
		stateful := migrationStatefulOperationalStatus(classes, operationalEvidence, operationalAvailable)
		operationalReady := migrationOperationalEvidenceReady(classes, operationalEvidence, operationalAvailable)
		if retired {
			stateful = migrationStatefulOperationalStatus(nil, nil, true)
			operationalReady = true
		}
		activeIdentities := append([]string{}, migrationResourceExternalIdentities(resource)...)
		shadowResource := legacyResource
		if service.Active == "legacy" {
			shadowResource = nativeResource
		}
		externalAliases := stringDifference(migrationResourceExternalIdentities(shadowResource), activeIdentities)
		artifacts := append([]string{}, migrationResourceGeneratedArtifacts(resource, service.Active, guarantee)...)
		gates := migrationDeployedConsumerGates(classes, artifacts, operationalEvidence, operationalAvailable)
		if retired {
			gates = []MigrationDeployedConsumerGate{}
		}
		for _, gate := range gates {
			if gate.State == "unavailable" {
				operationalReady = false
			}
		}
		if !operationalReady {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5601", Severity: "error", Message: "required migration operational evidence is unavailable", Address: address})
		}
		statuses = append(statuses, MigrationConstructStatus{
			Address: address, Kind: resource.Kind, Service: service.Name, State: service.State, ActiveOwner: service.Active, ShadowOwner: shadowOwner,
			GuaranteeClassification: guarantee, MigrationDisposition: disposition, LegacyCandidateDigest: service.LegacyCandidateDigest, NativeCandidateDigest: service.NativeCandidateDigest, ComparisonDigest: service.ComparisonDigest,
			RequiredProfiles: required, MissingProfiles: missing, CutoverClass: migrationResourceCutoverClass(resource), StatefulOperationalState: stateful,
			ExternalIdentities: activeIdentities, ExternalAliases: externalAliases, GeneratedArtifacts: artifacts, DeployedConsumerGates: gates,
			CLIProtocolDependencies: migrationCLIProtocolDependencies(), OperationalEvidence: cloneMigrationOperationalEvidence(operationalEvidence), OperationalStateRevision: operationalRevision,
			OperationalReady: operationalReady, SemanticBlocking: semanticBlocking, RollbackSafety: service.RollbackSafety, Blocking: semanticBlocking || !operationalReady, Diagnostics: diagnostics,
		})
	}
	return statuses
}

func migrationServiceOperationalEvidence(state migrationFinishOperationalState, service string) map[string][]string {
	evidence := map[string][]string{}
	for _, receipt := range state.Receipts {
		if receipt.Service != service {
			continue
		}
		for key, value := range receipt.OperationalEvidence {
			if strings.TrimSpace(value) == "" {
				continue
			}
			evidence[key] = append(evidence[key], receipt.PlanID+":"+value)
		}
	}
	for key := range evidence {
		evidence[key] = canonicalStrings(evidence[key])
	}
	return evidence
}

func migrationStatefulOperationalStatus(classes []string, evidence map[string][]string, available bool) MigrationStatefulOperationalStatus {
	return MigrationStatefulOperationalStatus{
		Drain:  migrationOperationalFacetState(classes, []string{"durable_execution"}, evidence, available),
		Fence:  migrationOperationalFacetState(classes, []string{"durable_execution", "schedule", "schema_owner", "event_consumer"}, evidence, available),
		Cursor: migrationOperationalFacetState(classes, []string{"schedule", "event_consumer"}, evidence, available),
	}
}

func migrationOperationalFacetState(classes, relevant []string, evidence map[string][]string, available bool) string {
	required := false
	for _, class := range classes {
		if migrationContainsString(relevant, class) {
			required = true
			if !available || len(evidence[class]) == 0 {
				return "unavailable"
			}
		}
	}
	if !required {
		return "not_applicable"
	}
	return "recorded"
}

func migrationOperationalEvidenceReady(classes []string, evidence map[string][]string, available bool) bool {
	if !available {
		return false
	}
	for _, class := range classes {
		if class != "stateless_route" && len(evidence[class]) == 0 {
			return false
		}
	}
	return true
}

func migrationDeployedConsumerGates(classes, artifacts []string, evidence map[string][]string, available bool) []MigrationDeployedConsumerGate {
	if !migrationContainsString(classes, "generated_client") {
		return []MigrationDeployedConsumerGate{}
	}
	state := "recorded"
	if !available || len(evidence["generated_client"]) == 0 {
		state = "unavailable"
	}
	gates := make([]MigrationDeployedConsumerGate, 0, len(artifacts))
	for _, artifact := range artifacts {
		gates = append(gates, MigrationDeployedConsumerGate{Artifact: artifact, State: state, Evidence: append([]string{}, evidence["generated_client"]...)})
	}
	return gates
}

func migrationCLIProtocolDependencies() []MigrationCLIProtocolDependency {
	return []MigrationCLIProtocolDependency{
		{APIVersion: "scenery.cli.v0", State: "bridge_compatibility"},
		{APIVersion: "scenery.cli.v1", State: "current"},
	}
}

func stringDifference(values, excluded []string) []string {
	seen := map[string]bool{}
	for _, value := range excluded {
		seen[value] = true
	}
	var result []string
	for _, value := range values {
		if value != "" && !seen[value] {
			result = append(result, value)
		}
	}
	return canonicalStrings(result)
}

func cloneMigrationOperationalEvidence(value map[string][]string) map[string][]string {
	result := make(map[string][]string, len(value))
	for key, items := range value {
		result[key] = append([]string(nil), items...)
	}
	return result
}

func migrationContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func migrationResourceProfiles(resource Resource) []string {
	profiles := map[string]bool{"scenery.compiler-core/v1": true, "scenery.legacy-bridge/v1": true}
	switch resource.Kind {
	case "scenery.binding/v1":
		protocol := stringValue(resource.Spec["protocol"])
		if protocol == "http" {
			profiles["scenery.runtime-http/v1"], profiles["scenery.http-codec/v1"] = true, true
		} else if protocol == "event" {
			profiles["scenery.events/v1"] = true
		}
	case "scenery.execution/v1":
		if stringValue(resource.Spec["mode"]) == "durable" {
			profiles["scenery.runtime-durable/v1"] = true
		}
	case "scenery.schedule/v1", "scenery.event/v1", "scenery.event-emission/v1":
		profiles["scenery.events/v1"] = true
	case "scenery.entity/v1", "scenery.view/v1", "scenery.crud/v1", "scenery.fixture/v1", "scenery.data-source/v1":
		profiles["scenery.data/v1"] = true
	case "scenery.page/v1", "scenery.renderer/v1":
		profiles["scenery.ui/v1"] = true
	case "scenery.deployment/v1":
		profiles["scenery.deployment/v1"] = true
	}
	return sortedBoolKeys(profiles)
}

func migrationResourceDiagnostics(service MigrationService, address string) []Diagnostic {
	var diagnostics []Diagnostic
	for _, candidate := range []string{"legacy", "native"} {
		for _, diagnostic := range service.CandidateDiagnostics[candidate] {
			if diagnostic.Address == address || diagnostic.Address == "" {
				diagnostics = append(diagnostics, diagnostic)
			}
		}
	}
	return diagnostics
}

func hasErrorDiagnostic(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func migrationResourceCutoverClass(resource Resource) string {
	classes := migrationCutoverClasses([]Resource{resource})
	if len(classes) == 0 {
		return "stateless_contract"
	}
	return strings.Join(classes, "+")
}

func migrationResourceExternalIdentities(resource Resource) []string {
	var identities []string
	switch resource.Kind {
	case "scenery.binding/v1":
		if httpSpec, ok := resource.Spec["http"].(map[string]any); ok {
			identities = append(identities, strings.ToUpper(stringValue(httpSpec["method"]))+" "+stringValue(httpSpec["path"]))
		}
	case "scenery.execution/v1":
		if name := stringValue(resource.Spec["external_name"]); name != "" {
			identities = append(identities, name)
		}
	case "scenery.schedule/v1":
		identities = append(identities, resource.Address)
	case "scenery.entity/v1":
		if mapping, ok := resource.Spec["mapping"].(map[string]any); ok {
			if relation := stringValue(mapping["relation"]); relation != "" {
				identities = append(identities, relation)
			}
		}
	}
	return canonicalStrings(identities)
}

func migrationResourceGeneratedArtifacts(resource Resource, activeOwner, guarantee string) []string {
	if resource.Kind != "scenery.binding/v1" || stringValue(resource.Spec["protocol"]) != "http" {
		return []string{}
	}
	if activeOwner == "legacy" && guarantee != "verified" {
		return []string{"legacy_typescript_client"}
	}
	return []string{"native_typescript_client"}
}

func (s MigrationStatus) Service(name string) (MigrationService, error) {
	for _, service := range s.Services {
		if service.Name == name {
			return service, nil
		}
	}
	return MigrationService{}, fmt.Errorf("migration service %q not found", name)
}
