package vnext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	scenery "scenery.sh"
	localagent "scenery.sh/internal/agent"
)

const deploymentProviderABI = "scenery.deployment-provider/v1"

type DeploymentPlanRequest struct {
	Deployment              string            `json:"deployment"`
	BaseWorkspaceRevision   string            `json:"base_workspace_revision,omitempty"`
	BaseContractRevision    string            `json:"base_contract_revision,omitempty"`
	ImplementationRevisions map[string]string `json:"implementation_revision,omitempty"`
	Caller                  string            `json:"caller,omitempty"`
	Capabilities            []string          `json:"capabilities,omitempty"`
}

type DeploymentProviderRequest struct {
	Application             string                                  `json:"application"`
	Deployment              string                                  `json:"deployment"`
	Environment             string                                  `json:"environment"`
	ProviderAddress         string                                  `json:"provider_address"`
	ProviderSource          string                                  `json:"provider_source"`
	Instances               []string                                `json:"instances"`
	Resources               map[string]DeploymentResourceProjection `json:"resources"`
	ContractRevision        string                                  `json:"contract_revision"`
	ImplementationRevisions map[string]string                       `json:"implementation_revision"`
}

type DeploymentAction struct {
	Kind        string         `json:"kind"`
	Address     string         `json:"address"`
	Before      map[string]any `json:"before,omitempty"`
	After       map[string]any `json:"after,omitempty"`
	Destructive bool           `json:"destructive,omitempty"`
}

type DeploymentProviderPlan struct {
	APIVersion      string             `json:"api_version"`
	ProviderAddress string             `json:"provider_address"`
	ProviderSource  string             `json:"provider_source"`
	ProviderABI     string             `json:"provider_abi"`
	Instances       []string           `json:"instances"`
	Actions         []DeploymentAction `json:"actions"`
	Opaque          json.RawMessage    `json:"opaque,omitempty"`
	RequiresApply   bool               `json:"requires_apply"`
	Digest          string             `json:"digest"`
}

type DeploymentProvider interface {
	Plan(context.Context, DeploymentProviderRequest) (DeploymentProviderPlan, error)
	Apply(context.Context, DeploymentProviderPlan) (func(context.Context) error, error)
}

type DeploymentProviderRecovery interface {
	Rollback(context.Context, DeploymentProviderPlan) error
}

type DeploymentProviderRegistry map[string]DeploymentProvider

type DeploymentPlan struct {
	APIVersion             string                   `json:"api_version"`
	PlanID                 string                   `json:"plan_id"`
	Application            string                   `json:"application"`
	Deployment             string                   `json:"deployment"`
	DeploymentName         string                   `json:"deployment_name"`
	Environment            string                   `json:"environment"`
	BaseWorkspaceRevision  string                   `json:"base_workspace_revision"`
	ContractRevision       string                   `json:"contract_revision"`
	ImplementationRevision map[string]string        `json:"implementation_revision"`
	DeploymentRevision     string                   `json:"deployment_revision"`
	Projection             DeploymentProjection     `json:"projection"`
	ProviderPlans          []DeploymentProviderPlan `json:"provider_plans"`
	Caller                 string                   `json:"caller"`
	Capabilities           []string                 `json:"capabilities"`
	RequiredApprovals      []string                 `json:"required_approvals"`
	RiskRecords            []map[string]any         `json:"risk_records"`
	ExpiresAt              time.Time                `json:"expires_at"`
}

type DeploymentApplyOptions struct {
	ExpectedWorkspaceRevision string
	ExpectedContractRevision  string
	ExpectedImplementation    map[string]string
	Caller                    string
	ApprovalTokens            []ApprovalToken
	VerifyApproval            ApprovalVerifier
}

type DeploymentReceipt struct {
	APIVersion             string            `json:"api_version"`
	PlanID                 string            `json:"plan_id"`
	Application            string            `json:"application"`
	Deployment             string            `json:"deployment"`
	WorkspaceRevision      string            `json:"workspace_revision"`
	ContractRevision       string            `json:"contract_revision"`
	ImplementationRevision map[string]string `json:"implementation_revision"`
	DeploymentRevision     string            `json:"deployment_revision"`
	ProviderPlanDigests    []string          `json:"provider_plan_digests"`
	AppliedAt              time.Time         `json:"applied_at"`
}

type deploymentApplyJournal struct {
	APIVersion          string         `json:"api_version"`
	Plan                DeploymentPlan `json:"plan"`
	Applied             []int          `json:"applied_provider_indexes"`
	RestoreState        bool           `json:"restore_state"`
	PreviousState       []byte         `json:"previous_state,omitempty"`
	PreviousStateExists bool           `json:"previous_state_exists"`
	Committed           bool           `json:"committed"`
}

type deploymentApplyLock struct {
	APIVersion string           `json:"api_version"`
	Owner      localagent.Owner `json:"owner"`
}

func PlanDeployment(ctx context.Context, root string, request DeploymentPlanRequest, providers DeploymentProviderRegistry) (DeploymentPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := compileContractGraph(root, false)
	if err != nil {
		return DeploymentPlan{}, err
	}
	if !result.Valid() {
		return DeploymentPlan{}, fmt.Errorf("failed_precondition: deployment planning requires a valid contract")
	}
	if request.BaseWorkspaceRevision != "" && request.BaseWorkspaceRevision != result.WorkspaceRevision || request.BaseContractRevision != "" && request.BaseContractRevision != result.Manifest.ContractRevision {
		return DeploymentPlan{}, fmt.Errorf("revision_conflict: deployment planning base revisions changed")
	}
	implementation := cloneStringMap(request.ImplementationRevisions)
	if len(implementation) == 0 || !canonicalRevisionMap(implementation) {
		return DeploymentPlan{}, fmt.Errorf("failed_precondition: deployment planning requires a build-supplied implementation_revision")
	}
	projection, diagnostics := ResolveDeployment(result.Manifest, request.Deployment)
	if hasErrors(diagnostics) {
		return DeploymentPlan{}, fmt.Errorf("failed_precondition: deployment resolution failed: %s", firstError(diagnostics))
	}
	byAddress := resourcesByAddress(result.Manifest)
	providerGroups, coreAddresses, err := deploymentProviderGroups(projection, byAddress)
	if err != nil {
		return DeploymentPlan{}, err
	}
	var plans []DeploymentProviderPlan
	if len(coreAddresses) > 0 {
		coreResources := selectDeploymentResources(projection.Resources, coreAddresses)
		plans = append(plans, normalizeDeploymentProviderPlan(DeploymentProviderPlan{
			ProviderAddress: "scenery/core", ProviderSource: "builtin:scenery/core", ProviderABI: deploymentProviderABI,
			Instances: coreAddresses, Actions: deploymentBindActions(coreResources), RequiresApply: false,
		}))
	}
	providerAddresses := make([]string, 0, len(providerGroups))
	for address := range providerGroups {
		providerAddresses = append(providerAddresses, address)
	}
	sort.Strings(providerAddresses)
	for _, providerAddress := range providerAddresses {
		group := providerGroups[providerAddress]
		providerResource := byAddress[providerAddress]
		providerSource := stringValue(providerResource.Spec["source"])
		adapter := lookupDeploymentProvider(providers, providerAddress, providerSource)
		managed := deploymentGroupRequiresProvider(group, byAddress)
		var providerPlan DeploymentProviderPlan
		if adapter == nil {
			if managed {
				return DeploymentPlan{}, fmt.Errorf("capability_unavailable: required provider %s has no %s adapter", providerAddress, deploymentProviderABI)
			}
			providerPlan = DeploymentProviderPlan{
				ProviderAddress: providerAddress, ProviderSource: providerSource, ProviderABI: deploymentProviderABI,
				Instances: group, Actions: deploymentBindActions(selectDeploymentResources(projection.Resources, group)), RequiresApply: false,
			}
		} else {
			providerPlan, err = adapter.Plan(ctx, DeploymentProviderRequest{
				Application: result.Manifest.Application.Name, Deployment: projection.Deployment, Environment: projection.Environment,
				ProviderAddress: providerAddress, ProviderSource: providerSource, Instances: append([]string(nil), group...),
				Resources: selectDeploymentResources(projection.Resources, group), ContractRevision: result.Manifest.ContractRevision,
				ImplementationRevisions: cloneStringMap(implementation),
			})
			if err != nil {
				return DeploymentPlan{}, fmt.Errorf("provider plan %s: %w", providerAddress, err)
			}
			providerPlan.ProviderAddress, providerPlan.ProviderSource = providerAddress, providerSource
			providerPlan.Instances, providerPlan.RequiresApply = append([]string(nil), group...), true
		}
		providerPlan = normalizeDeploymentProviderPlan(providerPlan)
		if providerPlan.ProviderABI != deploymentProviderABI {
			return DeploymentPlan{}, fmt.Errorf("capability_unavailable: provider %s requires unsupported deployment ABI %s", providerAddress, providerPlan.ProviderABI)
		}
		if err := validateDeploymentProviderPlan(providerPlan); err != nil {
			return DeploymentPlan{}, fmt.Errorf("provider plan %s: %w", providerAddress, err)
		}
		plans = append(plans, providerPlan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ProviderAddress < plans[j].ProviderAddress })
	digests := deploymentProviderPlanDigests(plans)
	revisions := computeDeploymentRevisions(result.Manifest, implementation, map[string][]string{projection.Deployment: digests})
	deploymentName := lastAddressSegment(projection.Deployment)
	deploymentRevision := revisions[deploymentName]
	if deploymentRevision == "" {
		return DeploymentPlan{}, fmt.Errorf("failed_precondition: deployment_revision is unavailable")
	}
	caller := strings.TrimSpace(request.Caller)
	if caller == "" {
		caller = "local"
	}
	plan := DeploymentPlan{
		APIVersion: "scenery.deployment-plan/v1", Application: result.Manifest.Application.Name,
		Deployment: projection.Deployment, DeploymentName: deploymentName, Environment: projection.Environment,
		BaseWorkspaceRevision: result.WorkspaceRevision, ContractRevision: result.Manifest.ContractRevision,
		ImplementationRevision: implementation, DeploymentRevision: deploymentRevision, Projection: projection,
		ProviderPlans: plans, Caller: caller, Capabilities: canonicalStrings(request.Capabilities), ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	}
	if len(plan.Capabilities) == 0 {
		plan.Capabilities = []string{"scenery.deployment/v1"}
	}
	for _, providerPlan := range plans {
		for _, action := range providerPlan.Actions {
			if action.Destructive {
				scope := "deployment.destructive:" + action.Address
				plan.RequiredApprovals = append(plan.RequiredApprovals, scope)
				plan.RiskRecords = append(plan.RiskRecords, map[string]any{"risk_id": scope, "kind": "destructive_deployment_action", "address": action.Address})
			}
		}
	}
	plan.RequiredApprovals = canonicalStrings(plan.RequiredApprovals)
	plan.PlanID = deploymentPlanID(plan)
	return plan, nil
}

func ApplyDeploymentPlan(ctx context.Context, root string, plan DeploymentPlan, options DeploymentApplyOptions, providers DeploymentProviderRegistry) (DeploymentReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := acquireDeploymentApplyLock(root)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	defer release()
	if err := recoverDeploymentJournals(ctx, root, providers); err != nil {
		return DeploymentReceipt{}, err
	}
	if time.Now().UTC().After(plan.ExpiresAt) {
		return DeploymentReceipt{}, fmt.Errorf("failed_precondition: deployment plan expired")
	}
	if plan.PlanID == "" || deploymentPlanID(plan) != plan.PlanID {
		return DeploymentReceipt{}, fmt.Errorf("failed_precondition: deployment plan identity mismatch")
	}
	if options.Caller != plan.Caller {
		return DeploymentReceipt{}, fmt.Errorf("permission_denied: deployment plan caller mismatch")
	}
	if err := validateDeploymentApprovals(plan, options); err != nil {
		return DeploymentReceipt{}, err
	}
	appliedPath := deploymentAppliedPlanPath(root, plan.PlanID)
	if pathExists(appliedPath) {
		return DeploymentReceipt{}, fmt.Errorf("failed_precondition: deployment plan was already applied")
	}
	current, err := compileContractGraph(root, false)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	if !current.Valid() || current.WorkspaceRevision != plan.BaseWorkspaceRevision || current.Manifest.ContractRevision != plan.ContractRevision || options.ExpectedWorkspaceRevision != plan.BaseWorkspaceRevision || options.ExpectedContractRevision != plan.ContractRevision {
		return DeploymentReceipt{}, fmt.Errorf("revision_conflict: deployment plan revisions changed")
	}
	expectedImplementation := options.ExpectedImplementation
	if len(expectedImplementation) == 0 {
		return DeploymentReceipt{}, fmt.Errorf("failed_precondition: deployment apply requires the selected runtime bundle implementation_revision")
	}
	if !reflect.DeepEqual(expectedImplementation, plan.ImplementationRevision) {
		return DeploymentReceipt{}, fmt.Errorf("revision_conflict: implementation_revision changed")
	}
	resolved, diagnostics := ResolveDeployment(current.Manifest, plan.Deployment)
	if hasErrors(diagnostics) || !reflect.DeepEqual(resolved, plan.Projection) {
		return DeploymentReceipt{}, fmt.Errorf("revision_conflict: resolved deployment changed")
	}
	digests := deploymentProviderPlanDigests(plan.ProviderPlans)
	revisions := computeDeploymentRevisions(current.Manifest, plan.ImplementationRevision, map[string][]string{plan.Deployment: digests})
	if revisions[plan.DeploymentName] != plan.DeploymentRevision {
		return DeploymentReceipt{}, fmt.Errorf("revision_conflict: deployment_revision changed")
	}
	for _, providerPlan := range plan.ProviderPlans {
		if !providerPlan.RequiresApply {
			continue
		}
		adapter := lookupDeploymentProvider(providers, providerPlan.ProviderAddress, providerPlan.ProviderSource)
		if adapter == nil {
			return DeploymentReceipt{}, fmt.Errorf("capability_unavailable: required provider %s is unavailable at apply", providerPlan.ProviderAddress)
		}
		if _, ok := adapter.(DeploymentProviderRecovery); !ok {
			return DeploymentReceipt{}, fmt.Errorf("capability_unavailable: provider %s does not implement crash-safe rollback", providerPlan.ProviderAddress)
		}
	}
	statePath := deploymentStatePath(root, plan.DeploymentName)
	previous, previousExists, err := readDeploymentFile(root, statePath)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	journal := deploymentApplyJournal{
		APIVersion:          "scenery.deployment-apply-journal/v1",
		Plan:                plan,
		Applied:             []int{},
		PreviousState:       previous,
		PreviousStateExists: previousExists,
	}
	journalPath := deploymentJournalPath(root, plan.PlanID)
	if err := writeDeploymentJournal(root, journalPath, journal); err != nil {
		return DeploymentReceipt{}, err
	}
	var rollbacks []func(context.Context) error
	rollbackProviders := func(cause error) error {
		var rollbackErrors []error
		if journal.RestoreState {
			if err := restoreDeploymentState(root, journal); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore deployment state: %w", err))
			}
		}
		for index := len(journal.Applied) - 1; index >= 0; index-- {
			planIndex := journal.Applied[index]
			providerPlan := plan.ProviderPlans[planIndex]
			if index < len(rollbacks) && rollbacks[index] != nil {
				if err := rollbacks[index](context.Background()); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("provider rollback %s: %w", providerPlan.ProviderAddress, err))
				}
				continue
			}
			adapter := lookupDeploymentProvider(providers, providerPlan.ProviderAddress, providerPlan.ProviderSource).(DeploymentProviderRecovery)
			if err := adapter.Rollback(context.Background(), providerPlan); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("provider rollback %s: %w", providerPlan.ProviderAddress, err))
			}
		}
		if len(rollbackErrors) == 0 {
			_ = os.Remove(journalPath)
		}
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}
	for planIndex, providerPlan := range plan.ProviderPlans {
		if !providerPlan.RequiresApply {
			continue
		}
		adapter := lookupDeploymentProvider(providers, providerPlan.ProviderAddress, providerPlan.ProviderSource)
		journal.Applied = append(journal.Applied, planIndex)
		if err := writeDeploymentJournal(root, journalPath, journal); err != nil {
			return DeploymentReceipt{}, rollbackProviders(err)
		}
		rollback, applyErr := adapter.Apply(ctx, providerPlan)
		if applyErr != nil {
			return DeploymentReceipt{}, rollbackProviders(fmt.Errorf("provider apply %s: %w", providerPlan.ProviderAddress, applyErr))
		}
		if rollback == nil {
			return DeploymentReceipt{}, rollbackProviders(fmt.Errorf("provider apply %s returned no compensator", providerPlan.ProviderAddress))
		}
		rollbacks = append(rollbacks, rollback)
	}
	receipt := DeploymentReceipt{
		APIVersion: "scenery.deployment-receipt/v1", PlanID: plan.PlanID, Application: plan.Application,
		Deployment: plan.Deployment, WorkspaceRevision: plan.BaseWorkspaceRevision, ContractRevision: plan.ContractRevision,
		ImplementationRevision: cloneStringMap(plan.ImplementationRevision), DeploymentRevision: plan.DeploymentRevision,
		ProviderPlanDigests: digests, AppliedAt: time.Now().UTC(),
	}
	stateBytes, err := json.MarshalIndent(struct {
		APIVersion string            `json:"api_version"`
		Plan       DeploymentPlan    `json:"plan"`
		Receipt    DeploymentReceipt `json:"receipt"`
	}{"scenery.deployment-state/v1", plan, receipt}, "", "  ")
	if err != nil {
		return DeploymentReceipt{}, rollbackProviders(err)
	}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return DeploymentReceipt{}, rollbackProviders(err)
	}
	// Make restoration durable before publishing state. Recovery may restore an
	// unchanged file after a crash between these two writes; that is harmless.
	journal.RestoreState = true
	if err := writeDeploymentJournal(root, journalPath, journal); err != nil {
		return DeploymentReceipt{}, rollbackProviders(err)
	}
	if err := writeDeploymentFile(root, statePath, append(stateBytes, '\n')); err != nil {
		return DeploymentReceipt{}, rollbackProviders(err)
	}
	if err := writeDeploymentFile(root, appliedPath, append(receiptBytes, '\n')); err != nil {
		return DeploymentReceipt{}, rollbackProviders(err)
	}
	journal.Committed = true
	_ = writeDeploymentJournal(root, journalPath, journal)
	_ = removeDeploymentFile(root, journalPath)
	return receipt, nil
}

func recoverDeploymentJournals(ctx context.Context, root string, providers DeploymentProviderRegistry) error {
	directory := filepath.Join(root, ".scenery", "deployments", "journal")
	if _, err := confinedDeploymentPath(root, directory, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("internal: read deployment recovery journal: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("internal: read deployment recovery journal: %w", err)
		}
		var journal deploymentApplyJournal
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&journal); err != nil || decoder.Decode(&struct{}{}) != io.EOF || journal.APIVersion != "scenery.deployment-apply-journal/v1" || journal.Plan.PlanID == "" || deploymentPlanID(journal.Plan) != journal.Plan.PlanID {
			return fmt.Errorf("internal: invalid deployment recovery journal %s", entry.Name())
		}
		if journal.Committed || pathExists(deploymentAppliedPlanPath(root, journal.Plan.PlanID)) {
			if err := removeDeploymentFile(root, path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("internal: remove committed deployment journal: %w", err)
			}
			continue
		}
		if journal.RestoreState {
			if err := restoreDeploymentState(root, journal); err != nil {
				return fmt.Errorf("internal: recover deployment state: %w", err)
			}
		}
		for index := len(journal.Applied) - 1; index >= 0; index-- {
			planIndex := journal.Applied[index]
			if planIndex < 0 || planIndex >= len(journal.Plan.ProviderPlans) {
				return fmt.Errorf("internal: deployment recovery journal has invalid provider index")
			}
			providerPlan := journal.Plan.ProviderPlans[planIndex]
			adapter := lookupDeploymentProvider(providers, providerPlan.ProviderAddress, providerPlan.ProviderSource)
			recovery, ok := adapter.(DeploymentProviderRecovery)
			if !ok {
				return fmt.Errorf("capability_unavailable: provider %s cannot recover interrupted deployment", providerPlan.ProviderAddress)
			}
			if err := recovery.Rollback(ctx, providerPlan); err != nil {
				return fmt.Errorf("internal: recover provider %s: %w", providerPlan.ProviderAddress, err)
			}
		}
		if err := removeDeploymentFile(root, path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("internal: remove recovered deployment journal: %w", err)
		}
	}
	return nil
}

func restoreDeploymentState(root string, journal deploymentApplyJournal) error {
	path := deploymentStatePath(root, journal.Plan.DeploymentName)
	if journal.PreviousStateExists {
		return writeDeploymentFile(root, path, journal.PreviousState)
	}
	err := removeDeploymentFile(root, path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func deploymentJournalPath(root, planID string) string {
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return filepath.Join(root, ".scenery", "deployments", "journal", name)
}

func writeDeploymentJournal(root, path string, journal deploymentApplyJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeDeploymentFile(root, path, append(data, '\n'))
}

func acquireDeploymentApplyLock(root string) (func(), error) {
	directory := filepath.Join(root, ".scenery", "deployments")
	if _, err := confinedDeploymentPath(root, directory, true); err != nil {
		return nil, fmt.Errorf("failed_precondition: deployment state directory is unsafe: %w", err)
	}
	path := filepath.Join(directory, "apply.lock")
	owner := localagent.CurrentOwner("vnext-deployment-apply")
	lock := deploymentApplyLock{APIVersion: "scenery.deployment-apply-lock/v1", Owner: owner}
	encoded, _ := json.Marshal(lock)
	for attempts := 0; attempts < 3; attempts++ {
		if _, err := confinedDeploymentPath(root, path, true); err != nil {
			return nil, fmt.Errorf("failed_precondition: deployment apply lock is unsafe: %w", err)
		}
		err := writeSyncedFile(path, append(encoded, '\n'), 0o600)
		if err == nil {
			_ = syncDirectory(directory)
			owned := true
			return func() {
				if owned {
					_ = removeDeploymentFile(root, path)
					owned = false
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("failed_precondition: deployment apply lock cannot be read: %w", readErr)
		}
		var existing deploymentApplyLock
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&existing); decodeErr != nil || decoder.Decode(&struct{}{}) != io.EOF || existing.APIVersion != "scenery.deployment-apply-lock/v1" || existing.Owner.PID <= 0 {
			return nil, fmt.Errorf("failed_precondition: deployment apply lock is invalid")
		}
		if localagent.VerifyOwner(existing.Owner) == nil {
			return nil, fmt.Errorf("failed_precondition: deployment apply is active in process %d", existing.Owner.PID)
		}
		if err := removeDeploymentFile(root, path); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("failed_precondition: deployment apply lock is contended")
}

func writeDeploymentFile(root, path string, data []byte) error {
	target, err := confinedDeploymentPath(root, path, true)
	if err != nil {
		return fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("failed_precondition: deployment target is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWriteSynced(target, data, 0o600)
}

func readDeploymentFile(root, path string) ([]byte, bool, error) {
	target, err := confinedDeploymentPath(root, path, false)
	if err != nil {
		return nil, false, fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("failed_precondition: deployment target is not a regular file")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func removeDeploymentFile(root, path string) error {
	target, err := confinedDeploymentPath(root, path, false)
	if err != nil {
		return fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return err
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("failed_precondition: deployment target is not a regular file")
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func confinedDeploymentPath(root, path string, createParent bool) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil {
		return "", err
	}
	target, err := confinedPath(absoluteRoot, filepath.ToSlash(relative))
	if err != nil {
		return "", err
	}
	if createParent {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		if err := rejectPathSymlinks(absoluteRoot, filepath.Dir(target)); err != nil {
			return "", err
		}
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("path contains symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return target, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func deploymentProviderGroups(projection DeploymentProjection, resources map[string]Resource) (map[string][]string, []string, error) {
	groups := map[string][]string{}
	var core []string
	for address, projected := range projection.Resources {
		resource := resources[address]
		if resource.Address == "" {
			return nil, nil, fmt.Errorf("failed_precondition: deployment resource %s is unavailable", address)
		}
		switch projected.Kind {
		case "scenery.data-source/v1", "scenery.execution-engine/v1", "scenery.event-bus/v1", "scenery.secret-store/v1":
			providerAddress := resolveResourceRef(resource, refString(resource.Spec["provider"]), "provider")
			if resources[providerAddress].Kind != "scenery.provider/v1" {
				return nil, nil, fmt.Errorf("failed_precondition: %s has no typed provider", address)
			}
			groups[providerAddress] = append(groups[providerAddress], address)
		case "scenery.fixture/v1":
			entity := resources[resolveResourceRef(resource, refString(resource.Spec["entity"]), "entity")]
			dataSource := resources[resolveResourceRef(entity, refString(entity.Spec["data_source"]), "data_source")]
			providerAddress := resolveResourceRef(dataSource, refString(dataSource.Spec["provider"]), "provider")
			if entity.Kind != "scenery.entity/v1" || dataSource.Kind != "scenery.data-source/v1" || resources[providerAddress].Kind != "scenery.provider/v1" {
				return nil, nil, fmt.Errorf("failed_precondition: fixture %s has no typed data provider", address)
			}
			groups[providerAddress] = append(groups[providerAddress], address)
		case "scenery.provider/v1":
			if len(projected.Provenance) > 0 {
				groups[address] = append(groups[address], address)
			}
		default:
			core = append(core, address)
		}
	}
	for address := range groups {
		groups[address] = canonicalStrings(groups[address])
	}
	return groups, canonicalStrings(core), nil
}

func deploymentGroupRequiresProvider(addresses []string, resources map[string]Resource) bool {
	for _, address := range addresses {
		switch stringValue(resources[address].Spec["lifecycle"]) {
		case "external", "attached":
		default:
			return true
		}
	}
	return false
}

func deploymentBindActions(resources map[string]DeploymentResourceProjection) []DeploymentAction {
	addresses := make([]string, 0, len(resources))
	for address := range resources {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	actions := make([]DeploymentAction, 0, len(addresses))
	for _, address := range addresses {
		actions = append(actions, DeploymentAction{Kind: "bind", Address: address, After: cloneMapValue(resources[address].Values)})
	}
	return actions
}

func selectDeploymentResources(resources map[string]DeploymentResourceProjection, addresses []string) map[string]DeploymentResourceProjection {
	selected := make(map[string]DeploymentResourceProjection, len(addresses))
	for _, address := range addresses {
		selected[address] = resources[address]
	}
	return selected
}

func normalizeDeploymentProviderPlan(plan DeploymentProviderPlan) DeploymentProviderPlan {
	if plan.APIVersion == "" {
		plan.APIVersion = "scenery.provider-deployment-plan/v1"
	}
	if plan.ProviderABI == "" {
		plan.ProviderABI = deploymentProviderABI
	}
	plan.Instances = canonicalStrings(plan.Instances)
	sort.Slice(plan.Actions, func(i, j int) bool {
		if plan.Actions[i].Address != plan.Actions[j].Address {
			return plan.Actions[i].Address < plan.Actions[j].Address
		}
		return plan.Actions[i].Kind < plan.Actions[j].Kind
	})
	plan.Digest = deploymentProviderPlanDigest(plan)
	return plan
}

func deploymentProviderPlanDigest(plan DeploymentProviderPlan) string {
	plan.Digest = ""
	return revisionHash("scenery.provider-plan.v1\x00", plan)
}

func deploymentProviderPlanDigests(plans []DeploymentProviderPlan) []string {
	digests := make([]string, 0, len(plans))
	for _, plan := range plans {
		digests = append(digests, plan.Digest)
	}
	sort.Strings(digests)
	return digests
}

func validateDeploymentProviderPlan(plan DeploymentProviderPlan) error {
	if plan.APIVersion != "scenery.provider-deployment-plan/v1" || plan.ProviderAddress == "" || plan.ProviderSource == "" || plan.ProviderABI == "" || len(plan.Instances) == 0 {
		return fmt.Errorf("invalid provider plan identity")
	}
	if plan.Digest != deploymentProviderPlanDigest(plan) || !isCanonicalSHA256Digest(plan.Digest) {
		return fmt.Errorf("invalid provider plan digest")
	}
	for _, action := range plan.Actions {
		if action.Kind == "" || action.Address == "" {
			return fmt.Errorf("provider action requires kind and address")
		}
		if deploymentValueContainsPlaintextSecret(action.Before) || deploymentValueContainsPlaintextSecret(action.After) {
			return fmt.Errorf("permission_denied: provider plan contains secret plaintext")
		}
	}
	return nil
}

func deploymentValueContainsPlaintextSecret(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for key, item := range object {
		lower := strings.ToLower(key)
		if (strings.Contains(lower, "plaintext") || strings.HasSuffix(lower, "_value")) && refString(item) == "" && item != nil {
			return true
		}
		if deploymentValueContainsPlaintextSecret(item) {
			return true
		}
	}
	return false
}

func canonicalRevisionMap(revisions map[string]string) bool {
	for name, revision := range revisions {
		if strings.TrimSpace(name) == "" || !isCanonicalSHA256Digest(revision) {
			return false
		}
	}
	return len(revisions) > 0
}

func lookupDeploymentProvider(registry DeploymentProviderRegistry, address, source string) DeploymentProvider {
	if registry == nil {
		return nil
	}
	if provider := registry[address]; provider != nil {
		return provider
	}
	return registry[source]
}

func deploymentPlanID(plan DeploymentPlan) string {
	plan.PlanID = ""
	return revisionHash("scenery.deployment-plan.v1\x00", plan)
}

func validateDeploymentApprovals(plan DeploymentPlan, options DeploymentApplyOptions) error {
	if len(plan.RequiredApprovals) == 0 {
		return nil
	}
	if options.VerifyApproval == nil {
		return fmt.Errorf("permission_denied: required approvals are unavailable")
	}
	required := map[string]bool{}
	for _, scope := range plan.RequiredApprovals {
		required[scope] = true
	}
	approved := map[string]bool{}
	for _, token := range options.ApprovalTokens {
		if err := scenery.ValidateApprovalToken(token); err != nil {
			return fmt.Errorf("permission_denied: invalid approval token: %w", err)
		}
		if token.PlanID != plan.PlanID || token.Caller != plan.Caller || token.Caller != options.Caller || time.Now().UTC().After(token.ExpiresAt) {
			return fmt.Errorf("permission_denied: approval token binding is invalid")
		}
		payload, err := ApprovalTokenPayload(token)
		if err != nil || options.VerifyApproval(token, payload) != nil {
			return fmt.Errorf("permission_denied: invalid approval token")
		}
		for _, scope := range canonicalStrings(token.RiskScopes) {
			if !required[scope] {
				return fmt.Errorf("permission_denied: approval token contains an unrequested risk scope")
			}
			approved[scope] = true
		}
	}
	for scope := range required {
		if !approved[scope] {
			return fmt.Errorf("permission_denied: missing approval for %s", scope)
		}
	}
	return nil
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func lastAddressSegment(address string) string {
	parts := strings.Split(strings.Trim(address, "/"), "/")
	return parts[len(parts)-1]
}

func deploymentStatePath(root, name string) string {
	return filepath.Join(root, ".scenery", "deployments", name+".json")
}

func deploymentAppliedPlanPath(root, planID string) string {
	return filepath.Join(root, ".scenery", "deployments", "applied", strings.TrimPrefix(planID, "sha256:")+".json")
}
