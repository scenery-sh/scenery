package vnext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
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
}

type agentSnapshot struct {
	Manifest          *Manifest
	Views             map[string]*Manifest
	WorkspaceRevision string
	Diagnostics       []Diagnostic
	Aliases           []string
}

func NewAgentSession() *AgentSession {
	return &AgentSession{snapshots: map[string]*agentSnapshot{}}
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
	if result.Manifest == nil && !agentMethodAllowsInvalidManifest(request.Method) {
		response.Error = agentError("failed_precondition", "no valid manifest is available")
		return response
	}
	if result.Manifest != nil {
		session.retain(result)
	}
	switch request.Method {
	case "capabilities":
		response.Result = agentCapabilities(result.Manifest)
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
		graph, err := Graph(manifest, params.Address, GraphOptions{Direction: params.Direction, Depth: params.Depth, MaxResources: params.MaxResources})
		if err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		response.Result = graph
	case "revisions.diff":
		var params struct {
			Base           *Manifest       `json:"base"`
			Target         *Manifest       `json:"target"`
			BaseRevision   string          `json:"base_revision"`
			TargetRevision string          `json:"target_revision"`
			View           string          `json:"view"`
			Dimensions     []string        `json:"dimensions"`
			Renames        []RenameReceipt `json:"rename_receipts"`
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
		renames := append([]RenameReceipt(nil), params.Renames...)
		if result.Root != "" {
			persisted, loadErr := LoadAppliedRenameReceipts(result.Root, base, target)
			if loadErr != nil {
				response.Error = agentError("failed_precondition", loadErr.Error())
				break
			}
			renames = append(renames, persisted...)
		}
		response.Result = CompareManifests(base, target, CompareOptions{View: params.View, Dimensions: params.Dimensions, Renames: renames})
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
		manifest := snapshot.Manifest
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
		bundle, err := ContextSnapshotWithDiagnostics(manifest, snapshot.WorkspaceRevision, snapshot.Diagnostics, params)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = bundle
	case "resource.create", "resource.delete", "resource.rename", "value.set", "value.unset", "module.configure", "module.upgrade":
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
		var params ChangeRequest
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		plan, err := PlanChanges(result.Root, params)
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = plan
	case "changes.apply":
		var params struct {
			Plan                    ChangePlan      `json:"plan"`
			ExpectWorkspaceRevision string          `json:"expect_workspace_revision"`
			ExpectContractRevision  *string         `json:"expect_contract_revision"`
			Caller                  string          `json:"caller"`
			ApprovalTokens          []ApprovalToken `json:"approval_tokens"`
		}
		if err := decodeAgentParams(request.Params, &params); err != nil {
			response.Error = agentError("invalid_request", err.Error())
			break
		}
		if params.Caller == "" {
			params.Caller = params.Plan.Caller
		}
		var verifier ApprovalVerifier
		if len(params.ApprovalTokens) > 0 {
			loaded, loadErr := LoadApprovalVerifier(result.Root)
			if loadErr != nil {
				response.Error = agentError("permission_denied", loadErr.Error())
				break
			}
			verifier = loaded
		}
		receipt, err := ApplyChangePlanWithOptions(result.Root, params.Plan, ApplyOptions{ExpectedWorkspaceRevision: params.ExpectWorkspaceRevision, ExpectedContractRevision: params.ExpectContractRevision, Caller: params.Caller, ApprovalTokens: params.ApprovalTokens, VerifyApproval: verifier})
		if err != nil {
			response.Error = agentErrorFrom(err)
			break
		}
		response.Result = receipt
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
	return method == "capabilities" || method == "schema.get" || method == "diagnostics.get" || method == "changes.plan" || method == "changes.apply"
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
	payload, err := parseContextToken(token)
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

func agentCapabilities(manifest *Manifest) map[string]any {
	profiles := make([]string, 0, len(SupportedProfiles))
	for profile := range SupportedProfiles {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	return map[string]any{
		"api_version": "scenery.agent.v1", "editions": []string{Edition}, "profiles": profiles,
		"resource_schema_revisions": allResourceSchemaRevisions(),
		"resource_create_kinds":     resourceCreateSchemaRevisions(),
		"mutation_schema_revisions": allMutationSchemaRevisions(),
		"codec_profiles":            []string{"scenery.http-codec/v1"},
		"unsupported_draft_surfaces": []string{
			"compatibility_source_and_wire_classification",
			"declarative_extensions",
			"entity_evolution_migration",
			"legacy_v0_fixture_catalog_and_bridge_removal",
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
		"operations":       []string{"capabilities", "schema.get", "resources.list", "resources.get", "resources.explain", "graph.get", "revisions.diff", "diagnostics.get", "context.get", "changes.plan", "changes.apply", "resource.create", "resource.delete", "resource.rename", "value.set", "value.unset", "module.configure", "module.upgrade"},
		"transport_limits": map[string]any{"max_resources": 1000, "max_bytes": 2_000_000, "max_depth": 16, "continuation_ttl_seconds": int(contextTokenTTL.Seconds()), "retained_snapshots": 32},
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
	if result == nil || result.Manifest == nil || result.Root == "" {
		return SemanticOperation{}, fmt.Errorf("failed_precondition: no writable graph is available")
	}
	var err error
	operation, err = normalizeSemanticOperationShape(operation)
	if err != nil {
		return SemanticOperation{}, err
	}
	if err := rejectSecretMutation(operation); err != nil {
		return SemanticOperation{}, fmt.Errorf("invalid_request: %w", err)
	}
	planningBase, err := writablePlanningResult(result)
	if err != nil {
		return SemanticOperation{}, fmt.Errorf("failed_precondition: %w", err)
	}
	if err := validateSemanticOperationExpectation(operation, planningBase.Manifest); err != nil {
		return SemanticOperation{}, err
	}
	temp, err := cloneWorkspace(result.Root)
	if err != nil {
		return SemanticOperation{}, fmt.Errorf("internal: %w", err)
	}
	defer os.RemoveAll(temp)
	if err := applySemanticOperation(temp, planningBase, operation); err != nil {
		if strings.HasPrefix(err.Error(), "capability_unavailable:") {
			return SemanticOperation{}, err
		}
		return SemanticOperation{}, fmt.Errorf("failed_precondition: %w", err)
	}
	if _, err := Format(temp, false); err != nil {
		return SemanticOperation{}, fmt.Errorf("failed_precondition: %w", err)
	}
	predicted, err := Compile(temp)
	if err != nil {
		return SemanticOperation{}, fmt.Errorf("internal: %w", err)
	}
	if !predicted.Valid() {
		return SemanticOperation{}, fmt.Errorf("failed_precondition: semantic operation does not produce a valid graph: %s", firstError(predicted.Diagnostics))
	}
	return normalizeSemanticOperationValues(operation, result.Manifest, predicted.Manifest), nil
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
	return resource.Kind == value || strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery.") == strings.ReplaceAll(value, "_", "-")
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
