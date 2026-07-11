package vnext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

type MigrationPlanRequest struct {
	Action                   string            `json:"action"`
	Service                  string            `json:"service"`
	Caller                   string            `json:"caller"`
	BaseWorkspaceRevision    string            `json:"base_workspace_revision"`
	BaseContractRevision     string            `json:"base_contract_revision"`
	ApprovedComparisonDigest string            `json:"approved_comparison_digest,omitempty"`
	ActivationReceiptPlanID  string            `json:"activation_receipt_plan_id,omitempty"`
	OperationalEvidence      map[string]string `json:"operational_evidence,omitempty"`
	AllowSkipShadow          bool              `json:"allow_skip_shadow,omitempty"`
}

type MigrationPlan struct {
	APIVersion                 string            `json:"api_version"`
	PlanID                     string            `json:"plan_id"`
	Action                     string            `json:"action"`
	Service                    string            `json:"service"`
	Caller                     string            `json:"caller"`
	BaseWorkspaceRevision      string            `json:"base_workspace_revision"`
	BaseContractRevision       string            `json:"base_contract_revision"`
	PredictedWorkspaceRevision string            `json:"predicted_workspace_revision"`
	PredictedContractRevision  string            `json:"predicted_contract_revision"`
	LegacyCandidateDigest      string            `json:"legacy_candidate_digest,omitempty"`
	NativeCandidateDigest      string            `json:"native_candidate_digest,omitempty"`
	ComparisonDigest           string            `json:"comparison_digest,omitempty"`
	OwnershipKeys              []string          `json:"ownership_keys"`
	CutoverClasses             []string          `json:"cutover_classes"`
	OperationalEvidence        map[string]string `json:"operational_evidence"`
	RuntimePlanDiff            SemanticDiff      `json:"runtime_plan_diff"`
	Edits                      []SourceEdit      `json:"source_edits"`
	RequiredApprovals          []string          `json:"required_approvals"`
	ExpiresAt                  time.Time         `json:"expires_at"`
}

type MigrationApplyOptions struct {
	ExpectedWorkspaceRevision string
	ExpectedContractRevision  string
	Caller                    string
	ApprovalTokens            []ApprovalToken
	VerifyApproval            ApprovalVerifier
}

type MigrationReceipt struct {
	APIVersion            string            `json:"api_version"`
	PlanID                string            `json:"plan_id"`
	Action                string            `json:"action"`
	Service               string            `json:"service"`
	Active                string            `json:"active"`
	WorkspaceRevision     string            `json:"workspace_revision"`
	ContractRevision      string            `json:"contract_revision"`
	OwnershipKeys         []string          `json:"ownership_keys"`
	CutoverClasses        []string          `json:"cutover_classes"`
	OperationalEvidence   map[string]string `json:"operational_evidence"`
	RollbackSafety        string            `json:"rollback_safety"`
	ReverseAction         string            `json:"reverse_action,omitempty"`
	LegacyCandidateDigest string            `json:"legacy_candidate_digest,omitempty"`
	NativeCandidateDigest string            `json:"native_candidate_digest,omitempty"`
	ComparisonDigest      string            `json:"comparison_digest,omitempty"`
}

func PlanMigrationTransition(root string, request MigrationPlanRequest) (MigrationPlan, error) {
	base, err := compileContractGraph(root, false)
	if err != nil {
		return MigrationPlan{}, err
	}
	if !base.Valid() || base.Migration == nil || base.Manifest == nil {
		return MigrationPlan{}, fmt.Errorf("failed_precondition: migration transition requires a valid mixed-mode graph")
	}
	if request.BaseWorkspaceRevision != base.WorkspaceRevision || request.BaseContractRevision != base.Manifest.ContractRevision {
		return MigrationPlan{}, fmt.Errorf("revision_conflict: migration base revisions changed")
	}
	service, err := BuildMigrationStatus(base).Service(request.Service)
	if err != nil {
		return MigrationPlan{}, err
	}
	action := strings.TrimSpace(request.Action)
	if action != "shadow" && action != "activate_native" && action != "activate_legacy" && action != "retire" {
		return MigrationPlan{}, fmt.Errorf("invalid_request: unsupported migration transition %q", action)
	}
	caller := strings.TrimSpace(request.Caller)
	if caller == "" {
		caller = "local"
	}
	var comparison MigrationComparison
	if len(base.Migration.LegacyCandidates[service.Name]) > 0 && len(base.Migration.NativeCandidates[service.Name]) > 0 {
		comparison, err = CompareMigrationService(base, service.Name)
		if err != nil {
			return MigrationPlan{}, err
		}
	}
	requiredApprovals := []string{}
	if action == "activate_native" {
		if service.Active == "native" {
			plan := idempotentMigrationPlan(base, service, action, caller, comparison)
			if err := retainIssuedPlan(root, issuedMigrationTransitionPlan, plan.PlanID, plan); err != nil {
				return MigrationPlan{}, err
			}
			return plan, nil
		}
		if service.State != "shadow" {
			if !request.AllowSkipShadow {
				return MigrationPlan{}, fmt.Errorf("failed_precondition: native activation must pass through shadow state")
			}
			requiredApprovals = append(requiredApprovals, "risk_skip_shadow")
		}
		if !comparison.StaticContractComplete {
			return MigrationPlan{}, fmt.Errorf("failed_precondition: candidate comparison is incomplete")
		}
		if !comparison.StaticContractEqual {
			if request.ApprovedComparisonDigest == "" || request.ApprovedComparisonDigest != comparison.ComparisonDigest {
				return MigrationPlan{}, fmt.Errorf("failed_precondition: candidate comparison differs; approve the exact comparison digest to continue")
			}
			requiredApprovals = append(requiredApprovals, "risk_migration_difference")
		}
		if !comparison.BehavioralEvidenceComplete {
			requiredApprovals = append(requiredApprovals, "risk_advisory_migration_evidence")
		}
	}
	if action == "retire" && service.Active != "native" {
		return MigrationPlan{}, fmt.Errorf("failed_precondition: only an active native shadow may retire its legacy candidate")
	}
	if action == "activate_legacy" {
		if service.State != "shadow" || service.Active != "native" {
			return MigrationPlan{}, fmt.Errorf("failed_precondition: rollback requires a shadow service with native active")
		}
		activation, err := readMigrationReceipt(root, request.ActivationReceiptPlanID)
		if err != nil {
			return MigrationPlan{}, err
		}
		if activation.Service != service.Name || activation.Action != "activate_native" || activation.ReverseAction != "activate_legacy" {
			return MigrationPlan{}, fmt.Errorf("failed_precondition: receipt does not authorize rollback for %s", service.Name)
		}
		if activation.RollbackSafety != "safe" && activation.RollbackSafety != "conditional" {
			return MigrationPlan{}, fmt.Errorf("failed_precondition: activation receipt reports rollback unavailable")
		}
		if activation.LegacyCandidateDigest != service.LegacyCandidateDigest || activation.NativeCandidateDigest != service.NativeCandidateDigest || activation.ComparisonDigest != comparison.ComparisonDigest {
			return MigrationPlan{}, fmt.Errorf("revision_conflict: rollback candidates or comparison changed")
		}
		if !comparison.StaticContractComplete {
			return MigrationPlan{}, fmt.Errorf("failed_precondition: rollback comparison is incomplete")
		}
		if !comparison.StaticContractEqual && request.ApprovedComparisonDigest != comparison.ComparisonDigest {
			return MigrationPlan{}, fmt.Errorf("failed_precondition: rollback comparison differs; approve the exact comparison digest")
		}
		requiredApprovals = append(requiredApprovals, "risk_operational_rollback")
		if !comparison.BehavioralEvidenceComplete {
			requiredApprovals = append(requiredApprovals, "risk_advisory_migration_evidence")
		}
	}

	cutoverClasses := append([]string(nil), service.CutoverClasses...)
	if action == "activate_native" || action == "activate_legacy" {
		for _, class := range cutoverClasses {
			if class == "stateless_route" {
				continue
			}
			evidenceKey := class
			if action == "activate_legacy" {
				evidenceKey = "rollback_" + class
			}
			if strings.TrimSpace(request.OperationalEvidence[evidenceKey]) == "" {
				return MigrationPlan{}, fmt.Errorf("failed_precondition: %s requires %s operational evidence", action, evidenceKey)
			}
		}
	}

	temp, err := cloneWorkspace(root)
	if err != nil {
		return MigrationPlan{}, err
	}
	defer os.RemoveAll(temp)
	if err := mutateMigrationOwnership(temp, service.Name, action); err != nil {
		return MigrationPlan{}, err
	}
	transitionID := revisionHash("scenery.migration-transition.v1\x00", map[string]any{
		"action": action, "service": service.Name, "base_workspace": base.WorkspaceRevision,
		"legacy": service.LegacyCandidateDigest, "native": service.NativeCandidateDigest,
		"comparison": comparison.ComparisonDigest, "caller": caller, "evidence": request.OperationalEvidence,
	})
	ledger := map[string]any{
		"api_version": "scenery.migration-ledger/v1", "transition_id": transitionID, "action": action, "service": service.Name,
		"base_workspace_revision": base.WorkspaceRevision, "base_contract_revision": base.Manifest.ContractRevision,
		"legacy_candidate_digest": service.LegacyCandidateDigest, "native_candidate_digest": service.NativeCandidateDigest,
		"comparison_digest": comparison.ComparisonDigest, "caller": caller, "operational_evidence": cloneStringMap(request.OperationalEvidence),
	}
	ledgerBytes, _ := json.MarshalIndent(ledger, "", "  ")
	ledgerPath := filepath.Join(temp, "scenery.migration.ledger", strings.TrimPrefix(transitionID, "sha256:")+".json")
	if err := atomicWrite(ledgerPath, append(ledgerBytes, '\n')); err != nil {
		return MigrationPlan{}, err
	}
	if _, err := Format(temp, false); err != nil {
		return MigrationPlan{}, err
	}
	predicted, err := Compile(temp)
	if err != nil || !predicted.Valid() || predicted.Manifest == nil {
		if err != nil {
			return MigrationPlan{}, err
		}
		return MigrationPlan{}, fmt.Errorf("failed_precondition: migration transition does not compile: %s", firstError(predicted.Diagnostics))
	}
	if _, err := generateGoContractsFromResult(predicted, false); err != nil {
		return MigrationPlan{}, fmt.Errorf("failed_precondition: migration Go artifacts are invalid: %w", err)
	}
	if _, err := generateTypeScriptClientsFromResult(predicted, "", false); err != nil {
		return MigrationPlan{}, fmt.Errorf("failed_precondition: migration client artifacts are invalid: %w", err)
	}
	if err := refreshWorkspaceRevision(predicted); err != nil {
		return MigrationPlan{}, err
	}
	edits, err := changedWorkspaceFiles(root, temp)
	if err != nil {
		return MigrationPlan{}, err
	}
	runtimeDiff := CompareManifests(base.Manifest, predicted.Manifest, CompareOptions{View: "expanded", Scope: service.Name})
	plan := MigrationPlan{
		APIVersion: "scenery.migrate.activation-plan.v1", Action: action, Service: service.Name, Caller: caller,
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision,
		PredictedWorkspaceRevision: predicted.WorkspaceRevision, PredictedContractRevision: predicted.Manifest.ContractRevision,
		LegacyCandidateDigest: service.LegacyCandidateDigest, NativeCandidateDigest: service.NativeCandidateDigest, ComparisonDigest: comparison.ComparisonDigest,
		OwnershipKeys: migrationOwnershipKeys(base.Migration.NativeCandidates[service.Name]), CutoverClasses: cutoverClasses,
		OperationalEvidence: cloneStringMap(request.OperationalEvidence), RuntimePlanDiff: runtimeDiff, Edits: edits,
		RequiredApprovals: canonicalStrings(requiredApprovals), ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	}
	plan.PlanID = migrationPlanID(plan)
	if err := retainIssuedPlan(root, issuedMigrationTransitionPlan, plan.PlanID, plan); err != nil {
		return MigrationPlan{}, err
	}
	return plan, nil
}

func ApplyMigrationPlan(root string, plan MigrationPlan, options MigrationApplyOptions) (MigrationReceipt, error) {
	if err := requireIssuedPlan(root, issuedMigrationTransitionPlan, plan.PlanID, plan); err != nil {
		return MigrationReceipt{}, err
	}
	if time.Now().UTC().After(plan.ExpiresAt) {
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: migration plan expired")
	}
	if migrationPlanID(plan) != plan.PlanID {
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: migration plan identity mismatch")
	}
	if options.Caller != plan.Caller || options.ExpectedWorkspaceRevision != plan.BaseWorkspaceRevision || options.ExpectedContractRevision != plan.BaseContractRevision {
		return MigrationReceipt{}, fmt.Errorf("revision_conflict: migration plan binding changed")
	}
	if pathExists(migrationReceiptPath(root, plan.PlanID)) {
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: migration plan was already applied")
	}
	if err := validateApprovals(ChangePlan{PlanID: plan.PlanID, Caller: plan.Caller, RequiredApprovals: plan.RequiredApprovals}, ApplyOptions{Caller: options.Caller, ApprovalTokens: options.ApprovalTokens, VerifyApproval: options.VerifyApproval}); err != nil {
		return MigrationReceipt{}, err
	}
	current, err := compileContractGraph(root, false)
	if err != nil || current.Manifest == nil || current.WorkspaceRevision != plan.BaseWorkspaceRevision || current.Manifest.ContractRevision != plan.BaseContractRevision {
		return MigrationReceipt{}, fmt.Errorf("revision_conflict: migration source changed")
	}
	service, err := BuildMigrationStatus(current).Service(plan.Service)
	if err != nil || service.LegacyCandidateDigest != plan.LegacyCandidateDigest || service.NativeCandidateDigest != plan.NativeCandidateDigest {
		return MigrationReceipt{}, fmt.Errorf("revision_conflict: migration candidates changed")
	}
	if plan.ComparisonDigest != "" {
		comparison, compareErr := CompareMigrationService(current, plan.Service)
		if compareErr != nil || comparison.ComparisonDigest != plan.ComparisonDigest {
			return MigrationReceipt{}, fmt.Errorf("revision_conflict: migration comparison changed")
		}
	}
	staged, err := cloneWorkspace(root)
	if err != nil {
		return MigrationReceipt{}, err
	}
	defer os.RemoveAll(staged)
	if err := applyPlannedEdits(staged, plan.Edits, true); err != nil {
		return MigrationReceipt{}, err
	}
	checked, checkedFiles, err := validateStagedWorkspace(staged, true)
	if err != nil || !checked.Valid() || checked.WorkspaceRevision != plan.PredictedWorkspaceRevision || checked.Manifest.ContractRevision != plan.PredictedContractRevision {
		detail := ""
		if err != nil {
			detail = ": " + err.Error()
		} else if !checked.Valid() {
			detail = ": " + firstError(checked.Diagnostics)
		} else {
			detail = fmt.Sprintf(": predicted workspace=%s contract=%s, got workspace=%s contract=%s", plan.PredictedWorkspaceRevision, plan.PredictedContractRevision, checked.WorkspaceRevision, checked.Manifest.ContractRevision)
		}
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: staged migration plan no longer validates%s", detail)
	}
	rollback, finalize, err := commitPlannedEdits(root, plan.Edits, migrationReceiptPath(root, plan.PlanID))
	if err != nil {
		return MigrationReceipt{}, err
	}
	actual, err := revalidateCommittedResult(root, checked, checkedFiles)
	if err != nil || !actual.Valid() || actual.WorkspaceRevision != plan.PredictedWorkspaceRevision || actual.Manifest.ContractRevision != plan.PredictedContractRevision {
		rollback()
		return MigrationReceipt{}, fmt.Errorf("internal: applied migration revisions differ from plan")
	}
	active := "legacy"
	if transitioned, transitionErr := BuildMigrationStatus(actual).Service(plan.Service); transitionErr == nil {
		active = transitioned.Active
	}
	receipt := MigrationReceipt{
		APIVersion: "scenery.migrate.activation-receipt.v1", PlanID: plan.PlanID, Action: plan.Action, Service: plan.Service, Active: active,
		WorkspaceRevision: actual.WorkspaceRevision, ContractRevision: actual.Manifest.ContractRevision, OwnershipKeys: plan.OwnershipKeys,
		CutoverClasses: plan.CutoverClasses, OperationalEvidence: cloneStringMap(plan.OperationalEvidence), RollbackSafety: migrationRollbackSafety(plan.CutoverClasses),
		LegacyCandidateDigest: plan.LegacyCandidateDigest, NativeCandidateDigest: plan.NativeCandidateDigest, ComparisonDigest: plan.ComparisonDigest,
	}
	if plan.Action == "activate_native" && (receipt.RollbackSafety == "safe" || receipt.RollbackSafety == "conditional") {
		receipt.ReverseAction = "activate_legacy"
	} else if plan.Action == "activate_legacy" {
		receipt.ReverseAction = "activate_native"
	} else if plan.Action == "retire" {
		receipt.RollbackSafety = "unavailable"
	}
	encoded, _ := json.MarshalIndent(receipt, "", "  ")
	if err := atomicWriteSynced(migrationReceiptPath(root, plan.PlanID), append(encoded, '\n'), 0o644); err != nil {
		rollback()
		return MigrationReceipt{}, err
	}
	finalize()
	return receipt, nil
}

func idempotentMigrationPlan(base *Result, service MigrationService, action, caller string, comparison MigrationComparison) MigrationPlan {
	plan := MigrationPlan{
		APIVersion: "scenery.migrate.activation-plan.v1", Action: action, Service: service.Name, Caller: caller,
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision,
		PredictedWorkspaceRevision: base.WorkspaceRevision, PredictedContractRevision: base.Manifest.ContractRevision,
		LegacyCandidateDigest: service.LegacyCandidateDigest, NativeCandidateDigest: service.NativeCandidateDigest,
		ComparisonDigest: comparison.ComparisonDigest, OwnershipKeys: []string{}, CutoverClasses: []string{}, OperationalEvidence: map[string]string{}, Edits: []SourceEdit{}, RequiredApprovals: []string{}, ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	}
	plan.PlanID = migrationPlanID(plan)
	return plan
}

func mutateMigrationOwnership(root, service, action string) error {
	path := filepath.Join(root, "scenery.migration.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	block, err := migrationSyntaxService(data, service)
	if err != nil {
		return err
	}
	switch action {
	case "shadow":
		if block.Type == "legacy_service" {
			data = replaceSourceRange(data, block.TypeRange, []byte("shadow_service"))
		}
		data, err = setMigrationSyntaxAttribute(data, service, "module", map[string]any{"$ref": "module." + service}, false)
		if err == nil {
			data, err = setMigrationSyntaxAttribute(data, service, "active", "legacy", false)
		}
	case "activate_native":
		data, err = setMigrationSyntaxAttribute(data, service, "active", "native", true)
	case "activate_legacy":
		data, err = setMigrationSyntaxAttribute(data, service, "active", "legacy", true)
	case "retire":
		if block.Type != "shadow_service" {
			return fmt.Errorf("failed_precondition: retire requires a shadow service")
		}
		data = replaceSourceRange(data, block.TypeRange, []byte("native_service"))
		for _, name := range []string{"package", "target", "legacy_target", "active"} {
			data, err = removeMigrationSyntaxAttribute(data, service, name)
			if err != nil {
				return err
			}
		}
	}
	if err != nil {
		return err
	}
	formatted, err := canonicalFormatSource(data, "scenery.migration.scn")
	if err != nil {
		return err
	}
	return atomicWrite(path, formatted)
}

func migrationSyntaxService(data []byte, service string) (*hclsyntax.Block, error) {
	file, diagnostics := hclsyntax.ParseConfig(data, "scenery.migration.scn", hcl.InitialPos)
	if diagnostics.HasErrors() || file == nil {
		return nil, fmt.Errorf("parse migration ownership: %s", diagnostics.Error())
	}
	for _, root := range file.Body.(*hclsyntax.Body).Blocks {
		if root.Type != "migration" {
			continue
		}
		for _, block := range root.Body.Blocks {
			if len(block.Labels) == 1 && block.Labels[0] == service {
				return block, nil
			}
		}
	}
	return nil, fmt.Errorf("migration service %q not found", service)
}

func setMigrationSyntaxAttribute(data []byte, service, name string, value any, requireExisting bool) ([]byte, error) {
	block, err := migrationSyntaxService(data, service)
	if err != nil {
		return nil, err
	}
	tokens, err := changeTokens(value)
	if err != nil {
		return nil, err
	}
	if attribute := block.Body.Attributes[name]; attribute != nil {
		return replaceSourceRange(data, attribute.Expr.Range(), tokens.Bytes()), nil
	}
	if requireExisting {
		return nil, fmt.Errorf("failed_precondition: migration service %s has no %s attribute", service, name)
	}
	return insertBodyAttribute(data, block.CloseBraceRange.Start, name, tokens.Bytes())
}

func removeMigrationSyntaxAttribute(data []byte, service, name string) ([]byte, error) {
	block, err := migrationSyntaxService(data, service)
	if err != nil {
		return nil, err
	}
	if attribute := block.Body.Attributes[name]; attribute != nil {
		return removeSourceRangeLine(data, attribute.Range()), nil
	}
	return data, nil
}

func migrationCutoverClasses(resources []Resource) []string {
	classes := map[string]bool{}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.binding/v1":
			if stringValue(resource.Spec["protocol"]) == "event" {
				classes["event_consumer"] = true
			} else if stringValue(resource.Spec["protocol"]) == "http" {
				classes["stateless_route"] = true
				if stringValue(resource.Spec["exposure"]) == "internet" || stringValue(resource.Spec["exposure"]) == "application" {
					classes["generated_client"] = true
				}
			}
		case "scenery.service/v1":
			classes["stateful_direct_service"] = true
		case "scenery.execution/v1":
			if stringValue(resource.Spec["mode"]) == "durable" {
				classes["durable_execution"] = true
			}
			if stringValue(resource.Spec["external_name"]) != "" {
				classes["external_identity"] = true
			}
		case "scenery.schedule/v1":
			classes["schedule"] = true
		case "scenery.entity/v1", "scenery.view/v1", "scenery.fixture/v1":
			classes["schema_owner"] = true
		case "scenery.event-emission/v1":
			classes["event_consumer"] = true
		}
	}
	return sortedBoolKeys(classes)
}

func migrationOwnershipKeys(resources []Resource) []string {
	keys := make([]string, 0, len(resources))
	for _, resource := range resources {
		keys = append(keys, resource.Address)
		if resource.Kind == "scenery.binding/v1" && stringValue(resource.Spec["protocol"]) == "http" {
			httpSpec, _ := resource.Spec["http"].(map[string]any)
			keys = append(keys, "route:"+stringValue(httpSpec["method"])+":"+stringValue(httpSpec["path"]))
		}
	}
	return canonicalStrings(keys)
}

func migrationRollbackSafety(classes []string) string {
	for _, class := range classes {
		if class != "stateless_route" {
			return "conditional"
		}
	}
	return "safe"
}

func readMigrationReceipt(root, planID string) (MigrationReceipt, error) {
	if strings.TrimSpace(planID) == "" {
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: rollback requires an activation receipt")
	}
	data, err := os.ReadFile(migrationReceiptPath(root, planID))
	if err != nil {
		if os.IsNotExist(err) {
			return MigrationReceipt{}, fmt.Errorf("failed_precondition: activation receipt %s was not found", planID)
		}
		return MigrationReceipt{}, err
	}
	var receipt MigrationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: activation receipt is invalid: %w", err)
	}
	if receipt.PlanID != planID {
		return MigrationReceipt{}, fmt.Errorf("failed_precondition: activation receipt identity mismatch")
	}
	return receipt, nil
}

func migrationPlanID(plan MigrationPlan) string {
	copy := plan
	copy.PlanID = ""
	return revisionHash("scenery.migration-plan.v1\x00", copy)
}

func migrationReceiptPath(root, planID string) string {
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return filepath.Join(root, ".scenery", "migrations", "receipts", name)
}
