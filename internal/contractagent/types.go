package contractagent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
)

var reportTokenCounter atomic.Uint64

func newReportToken() string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		fallback := strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.FormatUint(reportTokenCounter.Add(1), 10)
		sum := sha256.Sum256([]byte(fallback))
		copy(entropy[:], sum[:])
	}
	return "rpt_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:]))
}

type Result = compiler.Result
type Manifest = graph.Manifest
type Resource = graph.Resource
type Diagnostic = graph.Diagnostic
type Origin = graph.Origin
type ContextOptions = graph.ContextOptions
type SemanticOperation = evolution.SemanticOperation

// ChangeRequest remains an internal/CLI convenience alias. The agent JSON
// surface decodes PlanRequest instead so caller and capability fields cannot
// be supplied by model input.
type ChangeRequest = evolution.ChangeRequest

// FullChangePlan is the retained server-side artifact type. It is not used
// for model-visible changes.plan responses; plans.get is the explicit trusted
// review path for this shape.
type FullChangePlan = evolution.ChangePlan
type ApprovalToken = evolution.ApprovalToken
type ApprovalVerifier = evolution.ApprovalVerifier
type ApplyOptions = evolution.ApplyOptions

// AgentExecutionContext is established by the authenticated transport or
// launcher. It is deliberately not decoded from AgentRequest.Params: model
// input must not be able to select a principal, capabilities, or approval
// credentials.
type AgentExecutionContext struct {
	Principal           string
	GrantedCapabilities []string
	ApprovalTokens      []ApprovalToken
	ApprovalVerifier    ApprovalVerifier
	AppRoot             string
	ClientIdentity      string
	ProtocolVersion     string
	SessionID           string
	AssistantAddress    string
	ConversationDigest  string
	CapabilityRevision  string
	RequestID           string
	CallID              string
	TraceID             string
	TraceContext        map[string]string
	IdempotencyKey      string
	// AllowPlanInspection is reserved for a trusted review/approval channel.
	// Ordinary model sessions leave it false so plans.get cannot expose source
	// bytes or full semantic evidence into model history.
	AllowPlanInspection bool
}

// PlanRequest is the model-visible input to changes.plan. The canonical
// caller, granted capabilities, and any non-semantic source edits are bound
// by the execution context or retained server-side; unknown fields are
// rejected by the JSON-RPC decoder.
type PlanRequest struct {
	BaseWorkspaceRevision string              `json:"base_workspace_revision"`
	BaseContractRevision  *string             `json:"base_contract_revision"`
	Operations            []SemanticOperation `json:"operations"`
}

// ChangeRiskSummary contains only the stable, review-relevant fields of a
// semantic risk record. Full comparison evidence remains in the retained
// server-side plan.
type ChangeRiskSummary struct {
	RiskID             string `json:"risk_id"`
	Kind               string `json:"kind"`
	Address            string `json:"address"`
	Path               string `json:"path,omitempty"`
	RequiresApproval   bool   `json:"requires_approval"`
	ComparisonChangeID string `json:"comparison_change_id,omitempty"`
}

// ChangePlan is the compact model-visible handle returned by changes.plan.
// The full evolution.ChangePlan (including source bytes and semantic diff)
// remains retained by Scenery and can only be fetched explicitly with
// plans.get.
type ChangePlan struct {
	PlanID                     string                `json:"plan_id"`
	Application                string                `json:"application,omitempty"`
	Summary                    evolution.DiffSummary `json:"summary"`
	BaseWorkspaceRevision      string                `json:"base_workspace_revision"`
	BaseContractRevision       *string               `json:"base_contract_revision"`
	PredictedWorkspaceRevision string                `json:"predicted_workspace_revision"`
	PredictedContractRevision  string                `json:"predicted_contract_revision"`
	ImplementationStatus       string                `json:"implementation_revision_status"`
	DeploymentStatus           string                `json:"deployment_revision_status"`
	AffectedResources          []string              `json:"affected_resources"`
	AffectedResourceCount      int                   `json:"affected_resource_count"`
	AffectedResourcesTruncated bool                  `json:"affected_resources_truncated,omitempty"`
	RequiredApprovals          []string              `json:"required_approvals"`
	RequiredCapabilities       []string              `json:"required_capabilities,omitempty"`
	RiskRecords                []ChangeRiskSummary   `json:"risk_records"`
	RiskCount                  int                   `json:"risk_count"`
	RiskRecordsTruncated       bool                  `json:"risk_records_truncated,omitempty"`
	ExpiresAt                  time.Time             `json:"expires_at"`
}

// ChangeApplyRequest is the complete model-visible input to changes.apply.
// A plan ID is an opaque handle; Scenery loads and verifies the retained
// canonical plan and binds identity from AgentExecutionContext.
type ChangeApplyRequest struct {
	PlanID string `json:"plan_id"`
}

// ChangeApplyResponse keeps replay metadata out of the durable receipt. A
// replay returns the exact persisted receipt with Replayed=true.
type ChangeApplyResponse struct {
	Receipt  evolution.ChangeReceipt `json:"receipt"`
	Replayed bool                    `json:"replayed"`
}

// ChangeReceiptResponse is returned by changes.receipt.get. The explicit
// status envelope lets clients recover after an uncertain apply outcome while
// preserving the canonical receipt as the nested value.
type ChangeReceiptResponse struct {
	PlanID  string                  `json:"plan_id"`
	Status  string                  `json:"status"`
	Receipt evolution.ChangeReceipt `json:"receipt"`
}

const (
	agentMaxResources = graph.AgentMaxResources
	agentMaxBytes     = graph.AgentMaxBytes
)

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	result := map[string]Resource{}
	if manifest != nil {
		for _, resource := range manifest.Resources {
			result[resource.Address] = resource
		}
	}
	return result
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
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
