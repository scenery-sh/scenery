package contractagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

type AgentRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type AgentError struct {
	Code        int    `json:"code"`
	Kind        string `json:"kind"`
	Message     string `json:"message"`
	ReportToken string `json:"report_token,omitempty"`
}

type AgentResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *AgentError     `json:"error,omitempty"`
}

type AgentSession struct {
	mu        sync.Mutex
	snapshots map[string]*agentSnapshot
	order     []*agentSnapshot
	execution AgentExecutionContext
	mutation  agentMutationDependencies
}

type agentMutationDependencies struct {
	planChanges        func(string, evolution.ChangeRequest) (evolution.ChangePlan, error)
	applyIssuedPlan    func(string, string, evolution.ApplyOptions) (evolution.ChangeApplyResult, error)
	loadIssuedPlan     func(string, string) (evolution.ChangePlan, error)
	loadAppliedReceipt func(string, string) (evolution.ChangeReceipt, error)
}

type agentSnapshot struct {
	Manifest          *Manifest
	Views             map[string]*Manifest
	WorkspaceRevision string
	Diagnostics       []Diagnostic
	Aliases           []string
}

func NewAgentSession() *AgentSession {
	// The convenience session has no app-root authority and is therefore
	// read-only for mutation/recovery methods. Real mutation callers must use
	// NewAgentSessionWithContext with a launcher-bound absolute AppRoot.
	return NewAgentSessionWithContext(AgentExecutionContext{
		Principal:           "local",
		GrantedCapabilities: []string{"scenery.agent-mutation"},
	})
}

// NewAgentSessionWithContext creates a session whose identity and authority
// come from the authenticated caller. The context is copied and normalized so
// later model requests cannot mutate the server-owned slices or credentials.
func NewAgentSessionWithContext(context AgentExecutionContext) *AgentSession {
	return newAgentSessionWithDependencies(context, agentMutationDependencies{
		planChanges:        evolution.PlanChanges,
		applyIssuedPlan:    evolution.ApplyIssuedChangePlanWithOptions,
		loadIssuedPlan:     evolution.LoadIssuedChangePlan,
		loadAppliedReceipt: evolution.LoadAppliedChangeReceipt,
	})
}

func newAgentSessionWithDependencies(context AgentExecutionContext, mutation agentMutationDependencies) *AgentSession {
	context.Principal = strings.TrimSpace(context.Principal)
	context.AppRoot = strings.TrimSpace(context.AppRoot)
	context.ClientIdentity = strings.TrimSpace(context.ClientIdentity)
	context.ProtocolVersion = strings.TrimSpace(context.ProtocolVersion)
	context.SessionID = strings.TrimSpace(context.SessionID)
	context.AssistantAddress = strings.TrimSpace(context.AssistantAddress)
	context.ConversationDigest = strings.TrimSpace(context.ConversationDigest)
	context.CapabilityRevision = strings.TrimSpace(context.CapabilityRevision)
	context.RequestID = strings.TrimSpace(context.RequestID)
	context.CallID = strings.TrimSpace(context.CallID)
	context.TraceID = strings.TrimSpace(context.TraceID)
	context.IdempotencyKey = strings.TrimSpace(context.IdempotencyKey)
	context.AppRoot = canonicalExecutionRoot(context.AppRoot)
	context.GrantedCapabilities = canonicalStrings(context.GrantedCapabilities)
	context.ApprovalTokens = cloneApprovalTokens(context.ApprovalTokens)
	context.TraceContext = cloneTraceContext(context.TraceContext)
	return &AgentSession{snapshots: map[string]*agentSnapshot{}, execution: context, mutation: mutation}
}

// ExecutionContext returns a defensive copy of the server-owned execution
// context for transport adapters and tests. Approval verifier functions are
// immutable function values and are copied as-is.
func (session *AgentSession) ExecutionContext() AgentExecutionContext {
	if session == nil {
		return AgentExecutionContext{}
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	context := session.execution
	context.GrantedCapabilities = append([]string(nil), context.GrantedCapabilities...)
	context.ApprovalTokens = cloneApprovalTokens(context.ApprovalTokens)
	context.TraceContext = cloneTraceContext(context.TraceContext)
	return context
}

func cloneTraceContext(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	maps.Copy(clone, values)
	return clone
}

func cloneApprovalTokens(values []ApprovalToken) []ApprovalToken {
	if len(values) == 0 {
		return nil
	}
	clone := make([]ApprovalToken, len(values))
	for index, value := range values {
		clone[index] = value
		clone[index].RiskScopes = append([]string(nil), value.RiskScopes...)
	}
	return clone
}

func HandleAgentRequest(result *Result, request AgentRequest) AgentResponse {
	return NewAgentSession().Handle(result, request)
}

func (session *AgentSession) Handle(result *Result, request AgentRequest) AgentResponse {
	response := AgentResponse{JSONRPC: "2.0", ID: request.ID}
	if result == nil {
		response.Error = agentError("failed_precondition", "no valid manifest is available")
		return response
	}
	execution := session.ExecutionContext()
	if execution.AppRoot != "" && execution.AppRoot != canonicalExecutionRoot(result.Root) {
		response.Error = agentError("permission_denied", "execution context app root mismatch")
		return response
	}
	if result.Manifest == nil && !agentMethodAllowsInvalidManifest(request.Method) {
		response.Error = agentError("failed_precondition", "no valid manifest is available")
		return response
	}
	if result.Manifest != nil {
		session.retain(result)
	}
	switch request.Method {
	case "capabilities":
		response.Result = agentCapabilities(result.Manifest, execution)
	case "schema.get":
		var params struct {
			Kind string `json:"kind"`
		}
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		schema, ok := AgentSchema(params.Kind)
		if !ok {
			response.Error = agentError("invalid_request", "schema not found")
			break
		}
		response.Result = schema
	case "resources.list":
		var params struct {
			Kind   string `json:"kind"`
			Module string `json:"module"`
			View   string `json:"view"`
		}
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		manifest, err := result.ManifestForView(defaultString(params.View, "effective"))
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		var resources []Resource
		for _, resource := range manifest.Resources {
			if (params.Kind == "" || agentKindMatches(resource, params.Kind)) && (params.Module == "" || resource.Module == params.Module) {
				resources = append(resources, resource)
			}
		}
		if len(resources) > agentMaxResources {
			response.Error = agentError("invalid_request", "resource result exceeds transport limit; add kind or module filters")
			break
		}
		response.Result = map[string]any{"view": defaultString(params.View, "effective"), "resources": resources, "contract_revision": manifest.ContractRevision}
	case "resources.get", "resources.explain":
		var params struct {
			Addresses []string `json:"addresses"`
			Address   string   `json:"address"`
			View      string   `json:"view"`
		}
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if params.Address != "" {
			params.Addresses = append(params.Addresses, params.Address)
		}
		if len(canonicalStrings(params.Addresses)) > agentMaxResources {
			response.Error = agentError("invalid_request", "addresses exceed transport limit")
			break
		}
		view := defaultString(params.View, "effective")
		manifest, err := result.ManifestForView(view)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		resources, err := selectedResources(manifest, params.Addresses)
		if err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		resultValue := map[string]any{"view": view, "resources": resources}
		if request.Method == "resources.explain" {
			provenance := map[string]Origin{}
			for _, resource := range resources {
				provenance[resource.Address] = resource.Origin
			}
			resultValue["provenance"] = provenance
			resultValue["source_map"] = manifest.SourceMap
		}
		response.Result = resultValue
	case "graph.get":
		var params struct {
			Address      string `json:"address"`
			Direction    string `json:"direction"`
			Depth        int    `json:"depth"`
			MaxResources int    `json:"max_resources"`
			View         string `json:"view"`
		}
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		manifest, err := result.ManifestForView(defaultString(params.View, "effective"))
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		resourceGraph, err := graph.Graph(manifest, params.Address, graph.GraphOptions{Direction: params.Direction, Depth: params.Depth, MaxResources: params.MaxResources})
		if err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		response.Result = resourceGraph
	case "revisions.diff":
		var params struct {
			Base           *Manifest                 `json:"base"`
			Target         *Manifest                 `json:"target"`
			BaseRevision   string                    `json:"base_revision"`
			TargetRevision string                    `json:"target_revision"`
			View           string                    `json:"view"`
			Dimensions     []string                  `json:"dimensions"`
			Renames        []evolution.RenameReceipt `json:"rename_receipts"`
		}
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		base, err := session.resolveSnapshot(params.Base, params.BaseRevision)
		if err != nil {
			response.Error = agentError("failed_precondition", err.Error())
			break
		}
		target, err := session.resolveSnapshot(params.Target, params.TargetRevision)
		if err != nil {
			response.Error = agentError("failed_precondition", err.Error())
			break
		}
		renames := append([]evolution.RenameReceipt(nil), params.Renames...)
		if result.Root != "" {
			persisted, loadErr := evolution.LoadAppliedRenameReceipts(result.Root, base, target)
			if loadErr != nil {
				response.Error = agentError("failed_precondition", loadErr.Error())
				break
			}
			renames = append(renames, persisted...)
		}
		response.Result = evolution.CompareManifests(base, target, evolution.CompareOptions{View: params.View, Dimensions: params.Dimensions, Renames: renames})
	case "diagnostics.get":
		var contractRevision any
		if result.Manifest != nil {
			contractRevision = result.Manifest.ContractRevision
		}
		response.Result = map[string]any{"diagnostics": result.Diagnostics, "workspace_revision": result.WorkspaceRevision, "contract_revision": contractRevision}
	case "context.get":
		var params ContextOptions
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		snapshot, snapshotErr := session.contextSnapshot(result, params.ContinuationToken)
		if snapshotErr != nil {
			response.Error = agentError("failed_precondition", snapshotErr.Error())
			break
		}
		var manifest *graph.Manifest
		view := defaultString(params.View, "effective")
		if params.ContinuationToken == "" {
			var viewErr error
			manifest, viewErr = result.ManifestForView(view)
			if viewErr != nil {
				response.Error = agentErrorFrom(viewErr)
				break
			}
		} else {
			manifest = snapshot.Views[view]
			if manifest == nil {
				response.Error = agentError("failed_precondition", "retained context snapshot is unavailable for requested view")
				break
			}
		}
		bundle, err := graph.ContextSnapshotWithDiagnostics(manifest, snapshot.WorkspaceRevision, snapshot.Diagnostics, params)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = bundle
	case "resource.create", "resource.delete", "resource.rename", "value.set", "value.unset", "module.configure":
		var operation SemanticOperation
		if err := decodeAgentParams(request.Params, &operation); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		operation.Op = request.Method
		operation, err := validateAgentSemanticOperation(result, operation)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = operation
	case "changes.plan":
		var params PlanRequest
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validateMutationExecutionContext(execution); err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		plan, err := session.mutation.planChanges(result.Root, evolution.ChangeRequest{
			BaseWorkspaceRevision:     params.BaseWorkspaceRevision,
			BaseContractRevision:      params.BaseContractRevision,
			Capabilities:              append([]string(nil), execution.GrantedCapabilities...),
			Caller:                    execution.Principal,
			Operations:                append([]SemanticOperation(nil), params.Operations...),
			CheckPredictedGoContracts: execution.CheckPredictedGoContracts,
			CheckPredictedTypeScript:  execution.CheckPredictedTypeScript,
		})
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = compactChangePlan(plan)
	case "changes.apply":
		var params ChangeApplyRequest
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validatePlanID(params.PlanID); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validateMutationExecutionContext(execution); err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		resultValue, err := session.mutation.applyIssuedPlan(result.Root, params.PlanID, ApplyOptions{
			Caller:              execution.Principal,
			GrantedCapabilities: append([]string(nil), execution.GrantedCapabilities...),
			ApprovalTokens:      cloneApprovalTokens(execution.ApprovalTokens),
			VerifyApproval:      execution.ApprovalVerifier,
			CheckGenerated:      execution.CheckGenerated,
		})
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = ChangeApplyResponse{Receipt: resultValue.Receipt, Replayed: resultValue.Replayed}
	case "plans.get":
		if !execution.AllowPlanInspection {
			response.Error = agentError("permission_denied", "full plan inspection is restricted to a trusted review channel")
			break
		}
		var params ChangeApplyRequest
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validatePlanID(params.PlanID); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validateMutationExecutionContext(execution); err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		plan, err := session.mutation.loadIssuedPlan(result.Root, params.PlanID)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		if plan.Caller != execution.Principal {
			response.Error = agentError("permission_denied", "plan caller mismatch")
			break
		}
		response.Result = plan
	case "changes.receipt.get":
		var params ChangeApplyRequest
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validatePlanID(params.PlanID); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if err := validateReceiptReadExecutionContext(execution); err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		plan, err := session.mutation.loadIssuedPlan(result.Root, params.PlanID)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		if plan.Caller != execution.Principal {
			response.Error = agentError("permission_denied", "plan caller mismatch")
			break
		}
		receipt, err := session.mutation.loadAppliedReceipt(result.Root, params.PlanID)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = ChangeReceiptResponse{PlanID: params.PlanID, Status: "applied", Receipt: receipt}
	default:
		response.Error = agentError("invalid_request", "unknown method "+request.Method)
	}
	if response.Error == nil && response.Result != nil {
		if encoded, err := json.Marshal(response.Result); err != nil {
			response.Result = nil
			response.Error = agentError("internal", "encode agent response: "+err.Error())
		} else if len(encoded) > agentMaxBytes {
			response.Result = nil
			response.Error = agentError("invalid_request", "response exceeds transport limit; narrow the request")
		}
	}
	return response
}

func agentMethodAllowsInvalidManifest(method string) bool {
	return method == "capabilities" || method == "schema.get" || method == "diagnostics.get" || method == "changes.plan" || method == "changes.apply" || method == "plans.get" || method == "changes.receipt.get"
}

func validateMutationExecutionContext(context AgentExecutionContext) error {
	if context.Principal == "" {
		return fmt.Errorf("permission_denied: execution context principal is unavailable")
	}
	if canonicalExecutionRoot(context.AppRoot) == "" {
		return fmt.Errorf("permission_denied: execution context app root is unavailable")
	}
	if !hasAgentCapability(context.GrantedCapabilities, "scenery.agent-mutation") {
		return fmt.Errorf("permission_denied: execution context lacks scenery.agent-mutation")
	}
	return nil
}

func validateReceiptReadExecutionContext(context AgentExecutionContext) error {
	if context.Principal == "" {
		return fmt.Errorf("permission_denied: execution context principal is unavailable")
	}
	if canonicalExecutionRoot(context.AppRoot) == "" {
		return fmt.Errorf("permission_denied: execution context app root is unavailable")
	}
	return nil
}

func hasAgentCapability(capabilities []string, want string) bool {
	return slices.Contains(capabilities, want)
}

func canonicalExecutionRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute)
}

func validatePlanID(planID string) error {
	if !graph.IsCanonicalSHA256Digest(planID) {
		return fmt.Errorf("plan_id must be a canonical sha256 digest")
	}
	return nil
}

func compactChangePlan(plan evolution.ChangePlan) ChangePlan {
	affected := canonicalStrings(plan.AffectedResources)
	affectedCount := len(affected)
	affectedTruncated := affectedCount > 256
	if len(affected) > 256 {
		affected = affected[:256]
	}
	approvals := canonicalStrings(plan.RequiredApprovals)
	if len(approvals) > 256 {
		approvals = approvals[:256]
	}
	capabilities := canonicalStrings(plan.RequiredCapabilities)
	if len(capabilities) > 256 {
		capabilities = capabilities[:256]
	}
	for index := range affected {
		affected[index] = boundedAgentString(affected[index], 256)
	}
	for index := range approvals {
		approvals[index] = boundedAgentString(approvals[index], 256)
	}
	for index := range capabilities {
		capabilities[index] = boundedAgentString(capabilities[index], 256)
	}
	risks := compactRiskRecords(plan.RiskRecords)
	riskCount := len(plan.RiskRecords)
	riskTruncated := riskCount > 256
	if len(risks) > 256 {
		risks = risks[:256]
	}
	return ChangePlan{
		PlanID:                     plan.PlanID,
		Application:                boundedAgentString(plan.Application, 256),
		Summary:                    plan.SemanticDiff.Summary,
		BaseWorkspaceRevision:      plan.BaseWorkspaceRevision,
		BaseContractRevision:       cloneStringPointer(plan.BaseContractRevision),
		PredictedWorkspaceRevision: plan.PredictedWorkspaceRevision,
		PredictedContractRevision:  plan.PredictedContractRevision,
		ImplementationStatus:       plan.ImplementationStatus,
		DeploymentStatus:           plan.DeploymentStatus,
		AffectedResources:          affected,
		AffectedResourceCount:      affectedCount,
		AffectedResourcesTruncated: affectedTruncated,
		RequiredApprovals:          approvals,
		RequiredCapabilities:       capabilities,
		RiskRecords:                risks,
		RiskCount:                  riskCount,
		RiskRecordsTruncated:       riskTruncated,
		ExpiresAt:                  plan.ExpiresAt,
	}
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func compactRiskRecords(records []any) []ChangeRiskSummary {
	result := make([]ChangeRiskSummary, 0, len(records))
	for _, raw := range records {
		values, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		risk := ChangeRiskSummary{
			RiskID:             stringValue(values["risk_id"]),
			Kind:               stringValue(values["kind"]),
			Address:            stringValue(values["address"]),
			Path:               stringValue(values["path"]),
			ComparisonChangeID: stringValue(values["comparison_change_id"]),
		}
		if requires, ok := values["requires_approval"].(bool); ok {
			risk.RequiresApproval = requires
		}
		result = append(result, risk)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RiskID != result[j].RiskID {
			return result[i].RiskID < result[j].RiskID
		}
		if result[i].Address != result[j].Address {
			return result[i].Address < result[j].Address
		}
		return result[i].Path < result[j].Path
	})
	for index := range result {
		result[index].RiskID = boundedAgentString(result[index].RiskID, 256)
		result[index].Kind = boundedAgentString(result[index].Kind, 256)
		result[index].Address = boundedAgentString(result[index].Address, 256)
		result[index].Path = boundedAgentString(result[index].Path, 256)
		result[index].ComparisonChangeID = boundedAgentString(result[index].ComparisonChangeID, 256)
	}
	return result
}

func boundedAgentString(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	runes := []rune(value)
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

func (session *AgentSession) retain(result *Result) {
	if session == nil || result == nil || result.Manifest == nil {
		return
	}
	keys := []string{result.Manifest.ContractRevision, result.WorkspaceRevision}
	for _, revision := range result.DeploymentRevisions {
		keys = append(keys, revision)
	}
	views := map[string]*Manifest{"expanded": cloneAgentManifest(result.Manifest)}
	for view, manifest := range result.ViewManifests {
		views[view] = cloneAgentManifest(manifest)
	}
	for _, view := range []string{"source", "effective", "expanded"} {
		if views[view] == nil {
			views[view] = cloneAgentManifest(result.Manifest)
		}
	}
	snapshot := &agentSnapshot{Manifest: views["expanded"], Views: views, WorkspaceRevision: result.WorkspaceRevision, Diagnostics: append([]Diagnostic(nil), result.Diagnostics...)}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.snapshots == nil {
		session.snapshots = map[string]*agentSnapshot{}
	}
	snapshot.Aliases = canonicalStrings(keys)
	if existing := session.snapshots[result.WorkspaceRevision]; existing != nil {
		return
	}
	session.order = append(session.order, snapshot)
	for _, key := range snapshot.Aliases {
		if strings.TrimSpace(key) == "" {
			continue
		}
		session.snapshots[key] = snapshot
	}
	for len(session.order) > 32 {
		oldest := session.order[0]
		for _, alias := range oldest.Aliases {
			if session.snapshots[alias] == oldest {
				delete(session.snapshots, alias)
			}
		}
		session.order = session.order[1:]
	}
}

func (session *AgentSession) resolveSnapshot(supplied *Manifest, revision string) (*Manifest, error) {
	if supplied != nil {
		return supplied, nil
	}
	if strings.TrimSpace(revision) == "" {
		return nil, fmt.Errorf("base and target must identify a supplied or retained snapshot")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	snapshot := session.snapshots[revision]
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot %s is unavailable", revision)
	}
	return cloneAgentManifest(snapshot.Manifest), nil
}

func (session *AgentSession) contextSnapshot(result *Result, token string) (*agentSnapshot, error) {
	if token == "" {
		return &agentSnapshot{Manifest: result.Manifest, Views: result.ViewManifests, WorkspaceRevision: result.WorkspaceRevision, Diagnostics: append([]Diagnostic(nil), result.Diagnostics...)}, nil
	}
	payload, err := graph.ParseContextToken(token)
	if err != nil {
		return nil, fmt.Errorf("invalid continuation token")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	snapshot := session.snapshots[payload.WorkspaceRevision]
	if snapshot == nil || snapshot.Manifest == nil || snapshot.Manifest.ContractRevision != payload.ContractRevision {
		return nil, fmt.Errorf("continuation snapshot is unavailable")
	}
	views := map[string]*Manifest{}
	for view, manifest := range snapshot.Views {
		views[view] = cloneAgentManifest(manifest)
	}
	return &agentSnapshot{Manifest: cloneAgentManifest(snapshot.Manifest), Views: views, WorkspaceRevision: snapshot.WorkspaceRevision, Diagnostics: append([]Diagnostic(nil), snapshot.Diagnostics...)}, nil
}

func cloneAgentManifest(manifest *Manifest) *Manifest {
	if manifest == nil {
		return nil
	}
	data, _ := json.Marshal(manifest)
	var cloned Manifest
	_ = json.Unmarshal(data, &cloned)
	return &cloned
}

func agentCapabilities(manifest *Manifest, execution AgentExecutionContext) map[string]any {
	operations := []string{"capabilities", "schema.get", "resources.list", "resources.get", "resources.explain", "graph.get", "revisions.diff", "diagnostics.get", "context.get", "changes.plan", "changes.apply", "changes.receipt.get", "resource.create", "resource.delete", "resource.rename", "value.set", "value.unset", "module.configure"}
	if execution.AllowPlanInspection {
		// Full plan inspection is intentionally omitted from ordinary sessions;
		// trusted review adapters opt in through the execution context.
		operations = append(operations, "plans.get")
	}
	return map[string]any{
		"kind":                      "scenery.agent.capabilities",
		"schema_revision":           "sha256:c510c09edae970695642f4d6a805fcba8f6497c99c217486393968c41a1428dc",
		"spec_revision":             string(spec.CurrentRevision()),
		"producer":                  machine.RuntimeProducer(),
		"resource_schema_revisions": allResourceSchemaRevisions(),
		"resource_create_kinds":     spec.ResourceCreateSchemaRevisions(),
		"mutation_schema_revisions": allMutationSchemaRevisions(),
		"unsupported_draft_surfaces": []string{
			"compatibility_source_and_wire_classification",
			"declarative_extensions",
			"entity_evolution_migration",
			"native_toolchain_identity",
			"patch_authorization_and_review_policy",
			"platform_listener_and_certificate_schemas",
			"provider_capability_vocabulary",
			"provider_deployment_plan_and_target_vocabulary",
			"registry_trust_and_revocation",
			"standard_library_catalog",
			"streaming_and_websockets",
			"workflow_runtime",
		},
		"operations":       operations,
		"transport_limits": map[string]any{"max_resources": 1000, "max_bytes": 2_000_000, "max_depth": 16, "continuation_ttl_seconds": int(graph.ContextTokenTTL.Seconds()), "retained_snapshots": 32},
	}
}

func decodeAgentParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid params: trailing JSON value")
	}
	return nil
}

func validateAgentSemanticOperation(result *Result, operation SemanticOperation) (SemanticOperation, error) {
	return evolution.ValidateSemanticOperation(result, operation)
}

func agentError(kind, message string) *AgentError {
	codes := map[string]int{"invalid_request": -32602, "revision_conflict": -32003, "failed_precondition": -32004, "capability_unavailable": -32005, "permission_denied": -32006, "internal": -32603}
	result := &AgentError{Code: codes[kind], Kind: kind, Message: message}
	if kind == "internal" {
		result.Message = "internal tooling failure"
		result.ReportToken = newReportToken()
	}
	return result
}

func agentErrorFrom(err error) *AgentError {
	if err == nil {
		return nil
	}
	message := err.Error()
	kind := "failed_precondition"
	for _, candidate := range []string{"invalid_request", "revision_conflict", "failed_precondition", "capability_unavailable", "permission_denied", "internal"} {
		if strings.HasPrefix(message, candidate+":") {
			kind = candidate
			break
		}
	}
	return agentError(kind, message)
}

func agentKindMatches(resource Resource, value string) bool {
	return resource.Kind == value || strings.TrimPrefix(resource.Kind, "scenery.") == strings.ReplaceAll(value, "_", "-")
}

func selectedResources(manifest *Manifest, addresses []string) ([]Resource, error) {
	byAddress := resourcesByAddress(manifest)
	addresses = append([]string(nil), addresses...)
	sort.Strings(addresses)
	resources := make([]Resource, 0, len(addresses))
	for _, address := range addresses {
		resource, ok := byAddress[address]
		if !ok {
			return nil, fmt.Errorf("resource %q not found", address)
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
