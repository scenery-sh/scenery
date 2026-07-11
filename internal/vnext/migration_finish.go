package vnext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type MigrationFinishRequest struct {
	Caller                string            `json:"caller"`
	BaseWorkspaceRevision string            `json:"base_workspace_revision"`
	BaseContractRevision  string            `json:"base_contract_revision"`
	OperationalEvidence   map[string]string `json:"operational_evidence,omitempty"`
}

type MigrationFinishBlocker struct {
	Code    string `json:"code"`
	Service string `json:"service,omitempty"`
	Address string `json:"address,omitempty"`
	Message string `json:"message"`
}

type MigrationFinishPlan struct {
	APIVersion                 string                   `json:"api_version"`
	PlanID                     string                   `json:"plan_id"`
	Caller                     string                   `json:"caller"`
	BaseWorkspaceRevision      string                   `json:"base_workspace_revision"`
	BaseContractRevision       string                   `json:"base_contract_revision"`
	PredictedWorkspaceRevision string                   `json:"predicted_workspace_revision"`
	PredictedContractRevision  string                   `json:"predicted_contract_revision"`
	SourceEdits                []SourceEdit             `json:"source_edits"`
	Blockers                   []MigrationFinishBlocker `json:"blockers"`
	OperationalEvidence        map[string]string        `json:"operational_evidence"`
	OperationalStateRevision   string                   `json:"operational_state_revision"`
	ExpiresAt                  time.Time                `json:"expires_at"`
}

type MigrationFinishApplyOptions struct {
	Caller                    string
	ExpectedWorkspaceRevision string
	ExpectedContractRevision  string
}

type MigrationFinishReceipt struct {
	APIVersion               string            `json:"api_version"`
	PlanID                   string            `json:"plan_id"`
	Caller                   string            `json:"caller"`
	WorkspaceRevision        string            `json:"workspace_revision"`
	ContractRevision         string            `json:"contract_revision"`
	Mode                     string            `json:"mode"`
	OperationalEvidence      map[string]string `json:"operational_evidence"`
	OperationalStateRevision string            `json:"operational_state_revision"`
}

type migrationFinishOperationalState struct {
	Receipts []MigrationReceipt `json:"receipts"`
}

func PlanMigrationFinish(root string, request MigrationFinishRequest) (MigrationFinishPlan, error) {
	base, err := Compile(root)
	if err != nil {
		return MigrationFinishPlan{}, err
	}
	if !base.Valid() || base.Manifest == nil || base.Migration == nil {
		return MigrationFinishPlan{}, fmt.Errorf("failed_precondition: migration finish requires a valid mixed-mode graph")
	}
	if request.BaseWorkspaceRevision != base.WorkspaceRevision || request.BaseContractRevision != base.Manifest.ContractRevision {
		return MigrationFinishPlan{}, fmt.Errorf("revision_conflict: migration finish base revisions changed")
	}
	caller := strings.TrimSpace(request.Caller)
	if caller == "" {
		caller = "local"
	}
	operationalState, operationalStateRevision, err := readMigrationFinishOperationalState(root)
	if err != nil {
		return MigrationFinishPlan{}, err
	}
	blockers := migrationFinishBlockers(base, request.OperationalEvidence, operationalState)
	if len(blockers) > 0 {
		encoded, _ := json.Marshal(blockers)
		return MigrationFinishPlan{}, fmt.Errorf("failed_precondition: migration cannot finish: %s", encoded)
	}

	temp, err := cloneWorkspace(root)
	if err != nil {
		return MigrationFinishPlan{}, err
	}
	defer os.RemoveAll(temp)
	if err := os.Remove(filepath.Join(temp, "scenery.migration.scn")); err != nil {
		return MigrationFinishPlan{}, err
	}
	if err := removeMigrationProfileRequirement(temp); err != nil {
		return MigrationFinishPlan{}, err
	}
	if err := removeLegacyBridgeGeneratedArtifacts(temp, migrationFinishPackageRoots(base)); err != nil {
		return MigrationFinishPlan{}, err
	}
	if _, err := Format(temp, false); err != nil {
		return MigrationFinishPlan{}, err
	}
	predicted, err := Compile(temp)
	if err != nil || !predicted.Valid() || predicted.Manifest == nil || predicted.Migration != nil {
		if err != nil {
			return MigrationFinishPlan{}, err
		}
		return MigrationFinishPlan{}, fmt.Errorf("failed_precondition: native-only graph after finish is invalid: %s", firstError(predicted.Diagnostics))
	}
	if _, err := GenerateGoContracts(temp, false); err != nil {
		return MigrationFinishPlan{}, fmt.Errorf("failed_precondition: native-only Go artifacts are invalid: %w", err)
	}
	if _, err := GenerateTypeScriptClients(temp, "", false); err != nil {
		return MigrationFinishPlan{}, fmt.Errorf("failed_precondition: native-only client artifacts are invalid: %w", err)
	}
	predicted, err = Check(temp)
	if err != nil || !predicted.Valid() || predicted.Manifest == nil {
		return MigrationFinishPlan{}, fmt.Errorf("failed_precondition: generated native-only graph is invalid")
	}
	edits, err := changedWorkspaceFiles(root, temp)
	if err != nil {
		return MigrationFinishPlan{}, err
	}
	plan := MigrationFinishPlan{
		APIVersion: "scenery.migrate.finish-plan.v1", Caller: caller,
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision,
		PredictedWorkspaceRevision: predicted.WorkspaceRevision, PredictedContractRevision: predicted.Manifest.ContractRevision,
		SourceEdits: edits, Blockers: []MigrationFinishBlocker{}, OperationalEvidence: cloneStringMap(request.OperationalEvidence),
		OperationalStateRevision: operationalStateRevision, ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	}
	plan.PlanID = migrationFinishPlanID(plan)
	return plan, nil
}

func ApplyMigrationFinish(root string, plan MigrationFinishPlan, options MigrationFinishApplyOptions) (MigrationFinishReceipt, error) {
	if time.Now().UTC().After(plan.ExpiresAt) || migrationFinishPlanID(plan) != plan.PlanID {
		return MigrationFinishReceipt{}, fmt.Errorf("failed_precondition: migration finish plan expired or identity changed")
	}
	if options.Caller != plan.Caller || options.ExpectedWorkspaceRevision != plan.BaseWorkspaceRevision || options.ExpectedContractRevision != plan.BaseContractRevision {
		return MigrationFinishReceipt{}, fmt.Errorf("revision_conflict: migration finish plan binding changed")
	}
	if pathExists(migrationFinishReceiptPath(root, plan.PlanID)) {
		return MigrationFinishReceipt{}, fmt.Errorf("failed_precondition: migration finish plan was already applied")
	}
	current, err := Compile(root)
	if err != nil || current.Manifest == nil || current.WorkspaceRevision != plan.BaseWorkspaceRevision || current.Manifest.ContractRevision != plan.BaseContractRevision {
		return MigrationFinishReceipt{}, fmt.Errorf("revision_conflict: migration source changed")
	}
	operationalState, operationalStateRevision, err := readMigrationFinishOperationalState(root)
	if err != nil {
		return MigrationFinishReceipt{}, err
	}
	if operationalStateRevision != plan.OperationalStateRevision {
		return MigrationFinishReceipt{}, fmt.Errorf("revision_conflict: migration operational receipt state changed")
	}
	if blockers := migrationFinishBlockers(current, plan.OperationalEvidence, operationalState); len(blockers) > 0 {
		encoded, _ := json.Marshal(blockers)
		return MigrationFinishReceipt{}, fmt.Errorf("failed_precondition: migration cannot finish: %s", encoded)
	}
	staged, err := cloneWorkspace(root)
	if err != nil {
		return MigrationFinishReceipt{}, err
	}
	defer os.RemoveAll(staged)
	if err := applyPlannedEdits(staged, plan.SourceEdits, true); err != nil {
		return MigrationFinishReceipt{}, err
	}
	checked, err := Check(staged)
	if err != nil || !checked.Valid() || checked.Migration != nil || checked.WorkspaceRevision != plan.PredictedWorkspaceRevision || checked.Manifest.ContractRevision != plan.PredictedContractRevision {
		return MigrationFinishReceipt{}, fmt.Errorf("failed_precondition: staged migration finish no longer validates")
	}
	rollback, finalize, err := commitPlannedEdits(root, plan.SourceEdits, migrationFinishReceiptPath(root, plan.PlanID))
	if err != nil {
		return MigrationFinishReceipt{}, err
	}
	actual, err := checkDuringChangeTransaction(root)
	if err != nil || !actual.Valid() || actual.Migration != nil || actual.WorkspaceRevision != plan.PredictedWorkspaceRevision || actual.Manifest.ContractRevision != plan.PredictedContractRevision {
		rollback()
		return MigrationFinishReceipt{}, fmt.Errorf("internal: applied migration finish revisions differ from plan")
	}
	receipt := MigrationFinishReceipt{
		APIVersion: "scenery.migrate.finish-receipt.v1", PlanID: plan.PlanID, Caller: plan.Caller,
		WorkspaceRevision: actual.WorkspaceRevision, ContractRevision: actual.Manifest.ContractRevision, Mode: "native_only",
		OperationalEvidence: cloneStringMap(plan.OperationalEvidence), OperationalStateRevision: plan.OperationalStateRevision,
	}
	encoded, _ := json.MarshalIndent(receipt, "", "  ")
	if err := atomicWriteSynced(migrationFinishReceiptPath(root, plan.PlanID), append(encoded, '\n'), 0o644); err != nil {
		rollback()
		return MigrationFinishReceipt{}, err
	}
	finalize()
	return receipt, nil
}

func migrationFinishBlockers(result *Result, evidence map[string]string, operationalState migrationFinishOperationalState) []MigrationFinishBlocker {
	var blockers []MigrationFinishBlocker
	status := BuildMigrationStatus(result)
	for _, service := range status.Services {
		if service.State != "native" || service.Active != "native" {
			blockers = append(blockers, MigrationFinishBlocker{Code: "service_not_retired", Service: service.Name, Message: "service still has active or shadow legacy ownership"})
		}
	}
	blockers = append(blockers, migrationFinishEvidenceBlockers(status, evidence)...)
	blockers = append(blockers, migrationFinishReceiptBlockers(operationalState)...)
	for _, construct := range status.Constructs {
		if construct.SemanticBlocking {
			blockers = append(blockers, MigrationFinishBlocker{Code: "construct_blocked", Service: construct.Service, Address: construct.Address, Message: "construct remains opaque, advisory, unsupported, rewrite-required, or profile-incomplete"})
		}
	}
	if result.Manifest != nil {
		for _, resource := range result.Manifest.Resources {
			if resource.Origin.Kind == "legacy_v0" {
				blockers = append(blockers, MigrationFinishBlocker{Code: "legacy_owner", Address: resource.Address, Message: "active canonical graph still contains a legacy owner"})
			}
			if resource.Kind == "scenery.service/v1" {
				implementation, _ := resource.Spec["implementation"].(map[string]any)
				if stringValue(implementation["adapter"]) == "legacy_go_v0" {
					blockers = append(blockers, MigrationFinishBlocker{Code: "legacy_adapter", Address: resource.Address, Message: "service still uses the legacy Go compatibility adapter"})
				}
			}
			if resource.Kind == "scenery.operation/v1" {
				handler, _ := resource.Spec["handler"].(map[string]any)
				if stringValue(handler["adapter"]) == "legacy_go_v0" {
					blockers = append(blockers, MigrationFinishBlocker{Code: "legacy_adapter", Address: resource.Address, Message: "operation still uses the legacy Go compatibility adapter"})
				}
			}
			if resource.Compatibility != nil && (resource.Compatibility.Contract == "opaque" || resource.Compatibility.Contract == "unsupported" || resource.Compatibility.Contract == "advisory" || resource.Compatibility.MigrationDisposition == "rewrite_required") {
				blockers = append(blockers, MigrationFinishBlocker{Code: "compatibility_blocker", Address: resource.Address, Message: "resource retains a non-retirable compatibility disposition"})
			}
		}
	}
	sort.Slice(blockers, func(i, j int) bool {
		left, right := blockers[i].Code+"\x00"+blockers[i].Service+"\x00"+blockers[i].Address, blockers[j].Code+"\x00"+blockers[j].Service+"\x00"+blockers[j].Address
		return left < right
	})
	return blockers
}

func migrationFinishEvidenceBlockers(status MigrationStatus, evidence map[string]string) []MigrationFinishBlocker {
	required := map[string]string{
		"v0_cli_consumers": "declared v0 CLI consumers have not been cleared or moved to a separately supported compatibility product",
	}
	for _, service := range status.Services {
		for _, class := range service.CutoverClasses {
			if class == "stateless_route" {
				continue
			}
			key := migrationFinishEvidenceKey(class)
			required[key] = migrationFinishEvidenceMessage(class)
		}
	}
	keys := make([]string, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	blockers := make([]MigrationFinishBlocker, 0, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(evidence[key]) == "" {
			blockers = append(blockers, MigrationFinishBlocker{Code: "operational_evidence_missing", Address: key, Message: required[key]})
		}
	}
	return blockers
}

func migrationFinishEvidenceKey(class string) string {
	if class == "generated_client" {
		return "legacy_generated_client_consumers"
	}
	return "retired_" + class
}

func migrationFinishEvidenceMessage(class string) string {
	switch class {
	case "stateful_direct_service":
		return "state compatibility windows and direct-service legacy ownership have not been closed"
	case "durable_execution":
		return "legacy durable work has not been drained, migrated, or fenced to zero outstanding work"
	case "schedule":
		return "legacy schedule ownership and cursor state have not been retired"
	case "schema_owner":
		return "legacy schema or migration ownership has not crossed its recorded retirement barrier"
	case "event_consumer":
		return "legacy event subscription or offset ownership has not been retired"
	case "generated_client":
		return "legacy generated-client consumers have not accepted a native surface or completed versioned coexistence"
	case "external_identity":
		return "legacy external identities or aliases have not been retired"
	default:
		return "legacy operational ownership has not been retired for cutover class " + class
	}
}

func migrationFinishReceiptBlockers(state migrationFinishOperationalState) []MigrationFinishBlocker {
	retired := map[string]bool{}
	for _, receipt := range state.Receipts {
		if receipt.Action == "retire" {
			retired[receipt.Service] = true
		}
	}
	var blockers []MigrationFinishBlocker
	seen := map[string]bool{}
	for _, receipt := range state.Receipts {
		if receipt.Action != "activate_native" || receipt.ReverseAction == "" || retired[receipt.Service] || seen[receipt.Service] {
			continue
		}
		seen[receipt.Service] = true
		blockers = append(blockers, MigrationFinishBlocker{
			Code: "rollback_ownership_open", Service: receipt.Service, Address: receipt.PlanID,
			Message: "native activation still has an ownership receipt authorizing rollback; apply a retire transition before finish",
		})
	}
	return blockers
}

func readMigrationFinishOperationalState(root string) (migrationFinishOperationalState, string, error) {
	state := migrationFinishOperationalState{Receipts: []MigrationReceipt{}}
	directory := filepath.Join(root, ".scenery", "migrations", "receipts")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return state, revisionHash("scenery.migrate.finish-operational-state.v1\x00", state), nil
	}
	if err != nil {
		return state, "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".finish.json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			return state, "", readErr
		}
		var envelope struct {
			APIVersion string `json:"api_version"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return state, "", fmt.Errorf("failed_precondition: migration receipt %s is invalid: %w", entry.Name(), err)
		}
		if envelope.APIVersion != "scenery.migrate.activation-receipt.v1" {
			continue
		}
		var receipt MigrationReceipt
		if err := json.Unmarshal(data, &receipt); err != nil || receipt.PlanID == "" || receipt.Service == "" || receipt.Action == "" {
			return state, "", fmt.Errorf("failed_precondition: migration receipt %s is invalid", entry.Name())
		}
		state.Receipts = append(state.Receipts, receipt)
	}
	sort.Slice(state.Receipts, func(i, j int) bool { return state.Receipts[i].PlanID < state.Receipts[j].PlanID })
	return state, revisionHash("scenery.migrate.finish-operational-state.v1\x00", state), nil
}

func removeLegacyBridgeGeneratedArtifacts(root string, packageRoots []string) error {
	for _, relativeRoot := range packageRoots {
		boundedRoot := filepath.Join(root, filepath.FromSlash(relativeRoot))
		if !pathExists(boundedRoot) {
			continue
		}
		if err := filepath.WalkDir(boundedRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == ".scenery" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Name() != "scenery.legacy-bridge-generated.v1.json" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var descriptor struct {
				Files []string `json:"files"`
			}
			if err := json.Unmarshal(data, &descriptor); err != nil {
				return err
			}
			for _, relative := range descriptor.Files {
				artifact := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(relative)))
				if !pathWithin(filepath.Dir(path), artifact) {
					return fmt.Errorf("legacy bridge descriptor file escapes root")
				}
				if err := os.Remove(artifact); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return os.Remove(path)
		}); err != nil {
			return err
		}
	}
	return nil
}

func migrationFinishPackageRoots(result *Result) []string {
	var roots []string
	if result == nil || result.Manifest == nil {
		return roots
	}
	for _, module := range localModules(result.Manifest.Resources) {
		root := stringValue(module.Spec["workspace_package_root"])
		if root == "" {
			root = stringValue(module.Spec["source"])
		}
		root = filepath.ToSlash(filepath.Clean(root))
		if root != "" && root != "." && !filepath.IsAbs(root) && !strings.HasPrefix(root, "..") {
			roots = append(roots, root)
		}
	}
	return canonicalStrings(roots)
}

func migrationFinishPlanID(plan MigrationFinishPlan) string {
	copy := plan
	copy.PlanID = ""
	return revisionHash("scenery.migrate.finish-plan.v1\x00", copy)
}

func removeMigrationProfileRequirement(root string) error {
	path := filepath.Join(root, "scenery.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, diagnostics := hclsyntax.ParseConfig(data, "scenery.scn", hcl.InitialPos)
	if diagnostics.HasErrors() || file == nil {
		return fmt.Errorf("parse scenery.scn for migration profile removal: %s", diagnostics.Error())
	}
	for _, block := range file.Body.(*hclsyntax.Body).Blocks {
		if block.Type != "language" {
			continue
		}
		attribute := block.Body.Attributes["require_profiles"]
		if attribute == nil {
			return nil
		}
		value, valueDiagnostics := attribute.Expr.Value(nil)
		if valueDiagnostics.HasErrors() || !value.CanIterateElements() {
			return fmt.Errorf("language.require_profiles must be a static list")
		}
		var profiles []any
		for _, item := range value.AsValueSlice() {
			if item.Type() != cty.String || item.AsString() == "scenery.legacy-bridge/v1" {
				continue
			}
			profiles = append(profiles, item.AsString())
		}
		tokens, err := changeTokens(profiles)
		if err != nil {
			return err
		}
		data = replaceSourceRange(data, attribute.Expr.Range(), tokens.Bytes())
		formatted, err := canonicalFormatSource(data, "scenery.scn")
		if err != nil {
			return err
		}
		return atomicWrite(path, formatted)
	}
	return nil
}

func migrationFinishReceiptPath(root, planID string) string {
	name := strings.TrimPrefix(planID, "sha256:") + ".finish.json"
	return filepath.Join(root, ".scenery", "migrations", "receipts", name)
}
