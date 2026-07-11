package vnext

import (
	stdjson "encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	scenery "scenery.sh"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

type ChangePrecondition struct {
	Exists *bool `json:"exists,omitempty"`
	Absent bool  `json:"absent,omitempty"`
	Equals any   `json:"equals,omitempty"`
}
type SemanticOperation struct {
	Op           string              `json:"op"`
	Address      string              `json:"address"`
	View         string              `json:"view,omitempty"`
	Path         string              `json:"path,omitempty"`
	Value        any                 `json:"value,omitempty"`
	Precondition *ChangePrecondition `json:"precondition,omitempty"`
}
type ChangeRequest struct {
	BaseWorkspaceRevision string              `json:"base_workspace_revision"`
	BaseContractRevision  *string             `json:"base_contract_revision"`
	Caller                string              `json:"caller,omitempty"`
	Capabilities          []string            `json:"capabilities,omitempty"`
	Operations            []SemanticOperation `json:"operations"`
}
type SourceEdit struct {
	Path         string `json:"path"`
	BeforeDigest string `json:"before_digest"`
	After        []byte `json:"after"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	Mode         uint32 `json:"mode,omitempty"`
}
type ChangePlan struct {
	APIVersion                 string              `json:"api_version"`
	PlanID                     string              `json:"plan_id"`
	Application                string              `json:"application"`
	BaseWorkspaceRevision      string              `json:"base_workspace_revision"`
	BaseContractRevision       *string             `json:"base_contract_revision"`
	PredictedWorkspaceRevision string              `json:"predicted_workspace_revision"`
	PredictedContractRevision  string              `json:"predicted_contract_revision"`
	ImplementationStatus       string              `json:"implementation_revision_status"`
	DeploymentStatus           string              `json:"deployment_revision_status"`
	Caller                     string              `json:"caller"`
	Capabilities               []string            `json:"capabilities"`
	OperationsDigest           string              `json:"operations_digest"`
	Operations                 []SemanticOperation `json:"operations"`
	SemanticDiff               SemanticDiff        `json:"semantic_diff"`
	AffectedResources          []string            `json:"affected_resources"`
	Diagnostics                []Diagnostic        `json:"diagnostics"`
	Edits                      []SourceEdit        `json:"source_edits"`
	FormattingEffects          []string            `json:"formatting_effects"`
	RequiredApprovals          []string            `json:"required_approvals"`
	RequiredCapabilities       []string            `json:"required_capabilities"`
	RiskRecords                []any               `json:"risk_records"`
	ExpiresAt                  time.Time           `json:"expires_at"`
}
type ChangeReceipt struct {
	APIVersion           string   `json:"api_version"`
	PlanID               string   `json:"plan_id"`
	WorkspaceRevision    string   `json:"workspace_revision"`
	ContractRevision     string   `json:"contract_revision"`
	ImplementationStatus string   `json:"implementation_revision_status"`
	DeploymentStatus     string   `json:"deployment_revision_status"`
	Applied              []string `json:"applied"`
}

type ApprovalToken = scenery.ApprovalToken

type ApprovalVerifier func(token ApprovalToken, canonicalPayload []byte) error

type ApplyOptions struct {
	ExpectedWorkspaceRevision string
	ExpectedContractRevision  *string
	Caller                    string
	ApprovalTokens            []ApprovalToken
	VerifyApproval            ApprovalVerifier
}

type ChangePlanFailure struct {
	Message     string       `json:"message"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Edits       []SourceEdit `json:"predicted_source_edits"`
}

func (failure *ChangePlanFailure) Error() string {
	if failure == nil {
		return "failed_precondition: change plan failed"
	}
	return "failed_precondition: " + failure.Message
}

func PlanChanges(root string, request ChangeRequest) (ChangePlan, error) {
	base, err := Compile(root)
	if err != nil {
		return ChangePlan{}, err
	}
	baseContract := resultContractRevision(base)
	if base.WorkspaceRevision != request.BaseWorkspaceRevision || !sameOptionalString(baseContract, request.BaseContractRevision) {
		return ChangePlan{}, fmt.Errorf("revision_conflict: base revisions changed")
	}
	if base.Manifest == nil && request.BaseContractRevision != nil {
		return ChangePlan{}, fmt.Errorf("failed_precondition: repair planning requires a null base contract revision")
	}
	if base.Manifest != nil && request.BaseContractRevision == nil {
		return ChangePlan{}, fmt.Errorf("failed_precondition: normal planning requires the base contract revision")
	}
	planningBase, err := writablePlanningResult(base)
	if err != nil {
		return ChangePlan{}, err
	}
	temp, err := cloneWorkspace(root)
	if err != nil {
		return ChangePlan{}, err
	}
	defer os.RemoveAll(temp)
	for index, operation := range request.Operations {
		if err := rejectSecretMutation(operation); err != nil {
			return ChangePlan{}, err
		}
		if err := applySemanticOperation(temp, planningBase, operation); err != nil {
			return ChangePlan{}, err
		}
		if index == len(request.Operations)-1 {
			continue
		}
		if refreshed, compileErr := compileContractGraph(temp, false); compileErr == nil {
			if next, writableErr := writablePlanningResult(refreshed); writableErr == nil {
				planningBase = next
			}
		}
	}
	if _, err := Format(temp, false); err != nil {
		return ChangePlan{}, err
	}
	predicted, err := Compile(temp)
	if err != nil {
		return ChangePlan{}, err
	}
	if !predicted.Valid() {
		predictedEdits, _ := changedWorkspaceFiles(root, temp)
		return ChangePlan{}, &ChangePlanFailure{Diagnostics: append([]Diagnostic(nil), predicted.Diagnostics...), Edits: predictedEdits, Message: "planned source does not compile: " + firstError(predicted.Diagnostics)}
	}
	if _, err := generateGoContractsFromResult(predicted, false); err != nil {
		return ChangePlan{}, &ChangePlanFailure{Diagnostics: []Diagnostic{{Code: "SCN6205", Severity: "error", Message: err.Error()}}, Message: "planned generated Go artifacts are invalid: " + err.Error()}
	}
	if _, err := generateTypeScriptClientsFromResult(predicted, "", false); err != nil {
		return ChangePlan{}, &ChangePlanFailure{Diagnostics: []Diagnostic{{Code: "SCN6206", Severity: "error", Message: err.Error()}}, Message: "planned generated TypeScript artifacts are invalid: " + err.Error()}
	}
	if err := refreshWorkspaceRevision(predicted); err != nil {
		return ChangePlan{}, err
	}
	edits, err := changedWorkspaceFiles(root, temp)
	if err != nil {
		return ChangePlan{}, err
	}
	diff := CompareManifests(base.Manifest, predicted.Manifest, CompareOptions{View: "expanded"})
	requiredApprovals := make([]string, 0, len(diff.RiskRecords))
	for _, risk := range diff.RiskRecords {
		if values, ok := risk.(map[string]any); ok {
			if id, ok := values["risk_id"].(string); ok {
				requiredApprovals = append(requiredApprovals, id)
			}
		}
	}
	caller := strings.TrimSpace(request.Caller)
	if caller == "" {
		caller = "local"
	}
	capabilities := canonicalStrings(request.Capabilities)
	if len(capabilities) == 0 {
		capabilities = []string{"scenery.agent-mutation/v1"}
	}
	affected := make([]string, 0, len(diff.Changes))
	formatting := make([]string, 0, len(edits))
	for _, change := range diff.Changes {
		affected = append(affected, change.Address)
	}
	for _, edit := range edits {
		if strings.HasSuffix(edit.Path, ".scn") {
			formatting = append(formatting, edit.Path)
		}
	}
	affected = canonicalStrings(affected)
	formatting = canonicalStrings(formatting)
	implementationStatus, deploymentStatus := "unchanged", "unchanged"
	if !sameOptionalString(baseContract, resultContractRevision(predicted)) {
		implementationStatus, deploymentStatus = "invalidated", "invalidated"
	}
	plan := ChangePlan{
		APIVersion: "scenery.change-plan/v1", Application: predicted.Manifest.Application.Name,
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: cloneStringPointer(baseContract),
		PredictedWorkspaceRevision: predicted.WorkspaceRevision, PredictedContractRevision: predicted.Manifest.ContractRevision,
		ImplementationStatus: implementationStatus, DeploymentStatus: deploymentStatus,
		Caller: caller, Capabilities: capabilities, Operations: append([]SemanticOperation(nil), request.Operations...),
		SemanticDiff: diff, AffectedResources: affected, Diagnostics: append([]Diagnostic(nil), predicted.Diagnostics...), Edits: edits,
		FormattingEffects: formatting, RequiredApprovals: canonicalStrings(requiredApprovals), RequiredCapabilities: []string{},
		RiskRecords: diff.RiskRecords, ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	}
	plan.OperationsDigest = semanticOperationsDigest(plan.Operations)
	plan.PlanID = changePlanID(plan)
	return plan, nil
}

func ApplyChangePlan(root string, plan ChangePlan, expectedWorkspace, expectedContract string) (ChangeReceipt, error) {
	return ApplyChangePlanWithOptions(root, plan, ApplyOptions{ExpectedWorkspaceRevision: expectedWorkspace, ExpectedContractRevision: stringPointer(expectedContract), Caller: plan.Caller})
}

func ApplyChangePlanWithOptions(root string, plan ChangePlan, options ApplyOptions) (ChangeReceipt, error) {
	if time.Now().UTC().After(plan.ExpiresAt) {
		return ChangeReceipt{}, fmt.Errorf("failed_precondition: plan expired")
	}
	if pathExists(appliedPlanPath(root, plan.PlanID)) {
		return ChangeReceipt{}, fmt.Errorf("failed_precondition: plan was already applied")
	}
	if plan.Caller == "" || options.Caller != plan.Caller {
		return ChangeReceipt{}, fmt.Errorf("permission_denied: plan caller mismatch")
	}
	if err := validateApprovals(plan, options); err != nil {
		return ChangeReceipt{}, err
	}
	current, err := compileContractGraph(root, false)
	if err != nil {
		return ChangeReceipt{}, err
	}
	currentContract := resultContractRevision(current)
	if current.WorkspaceRevision != options.ExpectedWorkspaceRevision || !sameOptionalString(currentContract, options.ExpectedContractRevision) || options.ExpectedWorkspaceRevision != plan.BaseWorkspaceRevision || !sameOptionalString(options.ExpectedContractRevision, plan.BaseContractRevision) {
		return ChangeReceipt{}, fmt.Errorf("revision_conflict: expected revisions do not match")
	}
	if resultApplication(current) != plan.Application || changePlanID(plan) != plan.PlanID || semanticOperationsDigest(plan.Operations) != plan.OperationsDigest {
		return ChangeReceipt{}, fmt.Errorf("failed_precondition: plan identity mismatch")
	}
	if err := revalidateChangePreconditions(current, plan.Operations); err != nil {
		return ChangeReceipt{}, err
	}
	stagedRoot, err := cloneWorkspace(root)
	if err != nil {
		return ChangeReceipt{}, err
	}
	defer os.RemoveAll(stagedRoot)
	if err := applyPlannedEdits(stagedRoot, plan.Edits, true); err != nil {
		return ChangeReceipt{}, err
	}
	stagedResult, checkedFiles, err := validateStagedWorkspace(stagedRoot, true)
	if err != nil || !stagedResult.Valid() || stagedResult.WorkspaceRevision != plan.PredictedWorkspaceRevision || stagedResult.Manifest.ContractRevision != plan.PredictedContractRevision {
		if err != nil {
			return ChangeReceipt{}, err
		}
		return ChangeReceipt{}, fmt.Errorf("failed_precondition: staged plan no longer validates: %s", firstError(stagedResult.Diagnostics))
	}
	rollback, finalize, err := commitPlannedEdits(root, plan.Edits, appliedPlanPath(root, plan.PlanID))
	if err != nil {
		return ChangeReceipt{}, err
	}
	actual, err := revalidateCommittedResult(root, stagedResult, checkedFiles)
	if err != nil || !actual.Valid() || actual.WorkspaceRevision != plan.PredictedWorkspaceRevision || actual.Manifest.ContractRevision != plan.PredictedContractRevision {
		rollback()
		if err != nil {
			return ChangeReceipt{}, err
		}
		return ChangeReceipt{}, fmt.Errorf("internal: applied revisions differ from plan")
	}
	applied := make([]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		applied = append(applied, edit.Path)
	}
	sort.Strings(applied)
	receipt := ChangeReceipt{APIVersion: "scenery.change-receipt/v1", PlanID: plan.PlanID, WorkspaceRevision: actual.WorkspaceRevision, ContractRevision: actual.Manifest.ContractRevision, ImplementationStatus: plan.ImplementationStatus, DeploymentStatus: plan.DeploymentStatus, Applied: applied}
	receiptBytes, marshalErr := stdjson.MarshalIndent(receipt, "", "  ")
	if marshalErr != nil {
		rollback()
		return ChangeReceipt{}, marshalErr
	}
	if writeErr := atomicWriteSynced(appliedPlanPath(root, plan.PlanID), append(receiptBytes, '\n'), 0o644); writeErr != nil {
		rollback()
		return ChangeReceipt{}, writeErr
	}
	finalize()
	return receipt, nil
}

func applySemanticOperation(root string, base *Result, operation SemanticOperation) error {
	if operation.View != "" && operation.View != "source" {
		return fmt.Errorf("expanded resources are read-only")
	}
	if operation.Op == "resource.create" {
		return createResourceBlock(root, base, operation)
	}
	var resource *Resource
	for index := range base.Manifest.Resources {
		if base.Manifest.Resources[index].Address == operation.Address {
			resource = &base.Manifest.Resources[index]
			break
		}
	}
	if resource == nil || resource.Origin.Kind != "authored" {
		return fmt.Errorf("resource %q is not an authored writable resource", operation.Address)
	}
	if err := checkChangePrecondition(*resource, operation); err != nil {
		return err
	}
	switch operation.Op {
	case "resource.rename":
		name, ok := operation.Value.(string)
		if !ok || !validSemanticName(name) {
			return fmt.Errorf("rename requires a valid lower-snake name")
		}
		return renameResource(root, base, *resource, name)
	case "resource.delete":
		for _, edge := range resourceEdges(base.Manifest.Resources) {
			if edge.To == resource.Address {
				return fmt.Errorf("failed_precondition: %s depends on %s", edge.From, resource.Address)
			}
		}
		return removeResourceBlock(root, base, *resource)
	case "module.configure":
		operation.Op, operation.Path = "value.set", "/spec/inputs"
	case "module.upgrade":
		if err := validateLocalModuleUpgrade(*resource, operation.Value); err != nil {
			return err
		}
		operation.Op, operation.Path = "value.set", "/spec/version"
	case "value.set", "value.unset":
	default:
		return fmt.Errorf("unsupported semantic operation %q", operation.Op)
	}
	return mutateResourceValue(root, base, *resource, operation)
}

func validateLocalModuleUpgrade(resource Resource, value any) error {
	if resource.Kind != "scenery.module/v1" {
		return fmt.Errorf("invalid_request: module.upgrade requires a module resource")
	}
	constraint, ok := value.(string)
	constraint = strings.TrimSpace(constraint)
	if !ok || constraint == "" {
		return fmt.Errorf("invalid_request: module.upgrade value must be a non-empty version constraint")
	}
	source := strings.TrimSpace(stringValue(resource.Spec["source"]))
	workspaceRoot := strings.TrimSpace(stringValue(resource.Spec["workspace_package_root"]))
	if workspaceRoot == "" && !strings.HasPrefix(source, "./") && !strings.HasPrefix(source, "../") {
		return fmt.Errorf("capability_unavailable: registry module upgrades require the future registry profile; source and lockfile were not changed")
	}
	metadata, _ := resource.Spec["package"].(map[string]any)
	version := strings.TrimSpace(stringValue(metadata["version"]))
	if version == "" || !semanticVersionSatisfies(version, constraint) {
		return fmt.Errorf("failed_precondition: local package version %s does not satisfy upgrade constraint %s", version, constraint)
	}
	return nil
}

func renameResource(root string, base *Result, resource Resource, newName string) error {
	parts := strings.Split(resource.Address, "/")
	if len(parts) != 3 {
		return fmt.Errorf("invalid resource address")
	}
	newAddress := resourceAddress(resource.Module, strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery."), "-", "_"), newName)
	for _, existing := range base.Manifest.Resources {
		if existing.Address == newAddress {
			return fmt.Errorf("failed_precondition: resource %s already exists", newAddress)
		}
	}
	oldTraversal := parts[1] + "." + resource.Name
	newTraversal := parts[1] + "." + newName
	for _, source := range base.Sources {
		if source.ID == "" {
			continue
		}
		var replacements []traversalReplacement
		collectTraversalReplacements(source.Blocks, oldTraversal, newTraversal, &replacements)
		if len(replacements) == 0 {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(source.Relative))
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sort.Slice(replacements, func(i, j int) bool {
			return replacements[i].Start.ByteOffset > replacements[j].Start.ByteOffset
		})
		for _, replacement := range replacements {
			rng := replacement.Range
			if rng.Start.ByteOffset < 0 || rng.End.ByteOffset > len(b) {
				return fmt.Errorf("reference range outside source")
			}
			b = append(append(append([]byte(nil), b[:rng.Start.ByteOffset]...), []byte(replacement.Value)...), b[rng.End.ByteOffset:]...)
		}
		if err := atomicWrite(path, b); err != nil {
			return err
		}
	}
	return mutateResourceBlock(root, base, resource, func(body *hclwrite.Body, block *hclwrite.Block) { block.SetLabels([]string{newName}) })
}

func removeResourceBlock(root string, base *Result, resource Resource) error {
	return mutateResourceBlock(root, base, resource, func(body *hclwrite.Body, block *hclwrite.Block) { body.RemoveBlock(block) })
}

func mutateResourceBlock(root string, base *Result, resource Resource, mutation func(*hclwrite.Body, *hclwrite.Block)) error {
	source := sourceByID(base.Sources, resource.Origin.SourceID)
	if source == nil {
		return fmt.Errorf("source for %s not found", resource.Address)
	}
	path := filepath.Join(root, filepath.FromSlash(source.Relative))
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, diagnostics := hclwrite.ParseConfig(b, source.Relative, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return fmt.Errorf("parse writable source: %s", diagnostics.Error())
	}
	block := writableBlock(file.Body(), &resource)
	if block == nil {
		return fmt.Errorf("source block for %s not found", resource.Address)
	}
	mutation(file.Body(), block)
	return atomicWrite(path, hclwrite.Format(file.Bytes()))
}

type traversalReplacement struct {
	Range
	Value string
}

func collectTraversalReplacements(blocks []*Block, oldTraversal, newTraversal string, replacements *[]traversalReplacement) {
	for _, block := range blocks {
		for _, expression := range block.Attributes {
			if expression.Kind == "reference" && (expression.Traversal == oldTraversal || strings.HasPrefix(expression.Traversal, oldTraversal+".")) {
				*replacements = append(*replacements, traversalReplacement{
					Range: expression.Range,
					Value: newTraversal + strings.TrimPrefix(expression.Traversal, oldTraversal),
				})
			}
		}
		collectTraversalReplacements(block.Blocks, oldTraversal, newTraversal, replacements)
	}
}

func validSemanticName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func checkChangePrecondition(resource Resource, operation SemanticOperation) error {
	if operation.Precondition == nil {
		return nil
	}
	current, exists := resourcePointerValue(resource, operation.Path)
	pre := operation.Precondition
	if pre.Exists != nil && *pre.Exists != exists {
		return fmt.Errorf("failed_precondition: exists mismatch")
	}
	if pre.Absent && exists {
		return fmt.Errorf("failed_precondition: value exists")
	}
	if pre.Equals != nil && (!exists || !semanticEqual(current, pre.Equals)) {
		return fmt.Errorf("failed_precondition: value mismatch")
	}
	return nil
}

func changeValue(value any) (cty.Value, error) {
	b, err := stdjson.Marshal(value)
	if err != nil {
		return cty.NilVal, err
	}
	t, err := ctyjson.ImpliedType(b)
	if err != nil {
		return cty.NilVal, err
	}
	return ctyjson.Unmarshal(b, t)
}

func writableBlock(body *hclwrite.Body, resource *Resource) *hclwrite.Block {
	blockType := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery."), "-", "_")
	for _, block := range body.Blocks() {
		labels := block.Labels()
		if block.Type() == blockType && len(labels) == 1 && labels[0] == resource.Name {
			return block
		}
	}
	return nil
}
func sourceByID(sources []*Source, id string) *Source {
	for _, source := range sources {
		if source.ID == id {
			return source
		}
	}
	return nil
}

func resultContractRevision(result *Result) *string {
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" || result.Manifest.ContractRevision == "" {
		return nil
	}
	return stringPointer(result.Manifest.ContractRevision)
}

func resultApplication(result *Result) string {
	if result == nil {
		return ""
	}
	if result.Manifest != nil {
		return result.Manifest.Application.Name
	}
	if result.PartialGraph != nil {
		return result.PartialGraph.Application.Name
	}
	return ""
}

func writablePlanningResult(result *Result) (*Result, error) {
	if result == nil {
		return nil, fmt.Errorf("failed_precondition: source snapshot is unavailable")
	}
	if result.Manifest != nil {
		return result, nil
	}
	if result.PartialGraph == nil {
		return nil, fmt.Errorf("failed_precondition: invalid source has no recoverable graph")
	}
	copyResult := *result
	copyResult.Manifest = &Manifest{
		APIVersion: ManifestVersion, Edition: Edition, DiagnosticCatalog: DiagnosticCatalog,
		Application: result.PartialGraph.Application, Profiles: append([]string(nil), result.PartialGraph.Profiles...),
		Resources: append([]Resource(nil), result.PartialGraph.Resources...), SourceMap: result.PartialGraph.SourceMap,
	}
	return &copyResult, nil
}

func stringPointer(value string) *string {
	return &value
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func semanticOperationsDigest(operations []SemanticOperation) string {
	b, _ := MarshalCanonical(operations)
	return byteDigest(append([]byte("scenery.semantic-operations.v1\x00"), b...))
}

func rejectSecretMutation(operation SemanticOperation) error {
	path := strings.ToLower(operation.Path)
	if text, ok := operation.Value.(string); ok {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "<redacted>" || lower == "[redacted]" || strings.Contains(path, "password") || strings.Contains(path, "provider_token") || strings.HasSuffix(path, "/token") || strings.HasSuffix(path, "/secret_value") {
			return fmt.Errorf("permission_denied: semantic mutations do not accept secret plaintext or redaction markers")
		}
	}
	var inspect func(any) bool
	inspect = func(value any) bool {
		switch typed := value.(type) {
		case map[string]any:
			if _, ok := typed["$redacted"]; ok {
				return true
			}
			for key, item := range typed {
				lower := strings.ToLower(key)
				if oneOf(lower, "plaintext", "secret_value", "password", "token_value") {
					return true
				}
				if inspect(item) {
					return true
				}
			}
		case []any:
			for _, item := range typed {
				if inspect(item) {
					return true
				}
			}
		case string:
			lower := strings.ToLower(strings.TrimSpace(typed))
			return lower == "<redacted>" || lower == "[redacted]"
		}
		return false
	}
	if inspect(operation.Value) {
		return fmt.Errorf("permission_denied: semantic mutations do not accept secret plaintext or redaction markers")
	}
	return nil
}

func revalidateChangePreconditions(current *Result, operations []SemanticOperation) error {
	planning, err := writablePlanningResult(current)
	if err != nil {
		return err
	}
	byAddress := resourcesByAddress(planning.Manifest)
	for _, operation := range operations {
		if operation.Precondition == nil || operation.Op == "resource.create" {
			continue
		}
		resource, ok := byAddress[operation.Address]
		if !ok {
			return fmt.Errorf("revision_conflict: precondition resource %s is unavailable", operation.Address)
		}
		if err := checkChangePrecondition(resource, operation); err != nil {
			return err
		}
	}
	return nil
}

func validateApprovals(plan ChangePlan, options ApplyOptions) error {
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
	now := time.Now().UTC()
	for _, token := range options.ApprovalTokens {
		if err := scenery.ValidateApprovalToken(token); err != nil {
			return fmt.Errorf("permission_denied: invalid approval token: %w", err)
		}
		if token.PlanID != plan.PlanID || token.Caller != plan.Caller || token.Caller != options.Caller || now.After(token.ExpiresAt) {
			return fmt.Errorf("permission_denied: approval token binding is invalid")
		}
		payload, err := ApprovalTokenPayload(token)
		if err != nil {
			return fmt.Errorf("permission_denied: invalid approval token: %w", err)
		}
		if err := options.VerifyApproval(token, payload); err != nil {
			return fmt.Errorf("permission_denied: invalid approval signature: %w", err)
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

func ApprovalTokenPayload(token ApprovalToken) ([]byte, error) {
	return scenery.ApprovalTokenPayload(token)
}

func appliedPlanPath(root, planID string) string {
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return filepath.Join(root, ".scenery", "changes", "applied", name)
}

func changePlanID(plan ChangePlan) string {
	projection := struct {
		Application                                           string
		BaseWorkspace, PredictedWorkspace, PredictedContract  string
		BaseContract                                          *string
		Caller                                                string
		Capabilities, RequiredApprovals, RequiredCapabilities []string
		OperationsDigest, ComparisonDigest                    string
		Operations                                            []SemanticOperation
		Edits                                                 []SourceEdit
		ExpiresAt                                             time.Time
	}{
		plan.Application, plan.BaseWorkspaceRevision, plan.PredictedWorkspaceRevision, plan.PredictedContractRevision,
		plan.BaseContractRevision, plan.Caller, plan.Capabilities, plan.RequiredApprovals, plan.RequiredCapabilities,
		plan.OperationsDigest, plan.SemanticDiff.Digest, plan.Operations, plan.Edits, plan.ExpiresAt.UTC(),
	}
	b, _ := MarshalCanonical(projection)
	return byteDigest(append([]byte("scenery.change-plan.v1\x00"), b...))
}
func firstError(diagnostics []Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return diagnostic.Code + ": " + diagnostic.Message
		}
	}
	return "unknown error"
}
