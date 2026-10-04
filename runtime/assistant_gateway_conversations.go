package runtime

import (
	"context"
	"net/http"
	"strings"

	"scenery.sh/internal/assistantapi"
	"scenery.sh/internal/assistantcontrol"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/assistanttoken"
)

func (g *assistantGateway) handleCreate(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, true)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	body, err := readAssistantBody(req)
	if err != nil {
		g.writeError(w, err)
		return
	}
	request, err := assistantapi.DecodeCreateConversationRequest(body)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	runID, err := g.runID()
	if err != nil {
		g.writeError(w, err)
		return
	}
	conversationDigest := assistanttoken.ConversationDigest(runID)
	reservation, err := g.reserveRun(req, identity.Principal, conversationDigest, runID)
	if err != nil {
		g.writeError(w, err)
		return
	}
	startRequest := assistantruntime.StartRequest{
		RequestMetadata: g.requestMetadata(req, identity, conversationDigest),
		RunID:           runID,
		Message:         request.Message.Content,
	}
	client := g.currentClient()
	sent := false
	value, err := g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		sent = true
		return client.StartConversation(ctx, startRequest)
	})
	g.recordConversationRequest(err)
	if err != nil {
		reservation.finish(assistantRunStartOutcome(sent, err))
		g.observeAssistantRun(reservation, nil, assistantruntime.StreamRequest{})
		g.writeError(w, err)
		return
	}
	result, ok := value.(assistantruntime.StartResult)
	if !ok || result.RunID != runID || result.PrivateSessionID == "" || result.ContinuationToken == "" {
		reservation.finish(assistantRunOutcomeUnknown)
		g.observeAssistantRun(reservation, nil, assistantruntime.StreamRequest{})
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	reservation.finish(assistantRunAccepted)
	g.observeAssistantRun(reservation, client, assistantruntime.StreamRequest{RequestMetadata: startRequest.RequestMetadata, PrivateSessionID: result.PrivateSessionID, ContinuationToken: result.ContinuationToken})
	// The helper's private session and continuation are sealed directly into
	// the public conv1 handle. The token itself is already a canonical conv1_
	// value; re-encoding it would create a second, incompatible envelope.
	sealed, err := g.registration.TokenManager.SealConversation(assistanttoken.ConversationClaims{
		AssistantAddress: g.registration.AssistantAddress, OwnerDigest: identity.Owner,
		ConversationDigest: conversationDigest,
		PrivateSessionID:   result.PrivateSessionID, ContinuationToken: result.ContinuationToken,
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	g.rememberContinuation(sealed, result.ContinuationToken)
	g.rememberRun(sealed, runID)
	eventsURL := strings.TrimSuffix(g.registration.Path, "/") + "/v1/conversations/" + sealed + "/events"
	if err := assistantapi.ValidateEventsURLForSurface(eventsURL, g.registration.Path, sealed); err != nil {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	public := assistantapi.CreateConversationResponse{ConversationID: sealed, RunID: runID, EventsURL: eventsURL}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}

func (g *assistantGateway) handleTurn(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, false)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	conversationID := CurrentRequest().PathParams.Get("conversation_id")
	claims, err := g.conversationClaims(conversationID, identity)
	if err != nil {
		g.writeError(w, err)
		return
	}
	body, err := readAssistantBody(req)
	if err != nil {
		g.writeError(w, err)
		return
	}
	request, err := assistantapi.DecodeSendTurnRequest(body)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	runID, err := g.runID()
	if err != nil {
		g.writeError(w, err)
		return
	}
	reservation, err := g.reserveRun(req, identity.Principal, claims.ConversationDigest, runID)
	if err != nil {
		g.writeError(w, err)
		return
	}
	turnRequest := assistantruntime.TurnRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		RunID:             runID,
		Message:           request.Message.Content,
	}
	client := g.currentClient()
	sent := false
	value, err := g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		sent = true
		return client.SendTurn(ctx, turnRequest)
	})
	observe := assistantruntime.StreamRequest{RequestMetadata: turnRequest.RequestMetadata, PrivateSessionID: claims.PrivateSessionID, ContinuationToken: claims.ContinuationToken}
	if err != nil {
		reservation.finish(assistantRunStartOutcome(sent, err))
		g.observeAssistantRun(reservation, client, observe)
		g.writeError(w, err)
		return
	}
	result, ok := value.(assistantruntime.TurnResult)
	if !ok || result.RunID != runID || (result.PrivateSessionID != "" && result.PrivateSessionID != claims.PrivateSessionID) || result.ContinuationToken == "" {
		reservation.finish(assistantRunOutcomeUnknown)
		g.observeAssistantRun(reservation, client, observe)
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	reservation.finish(assistantRunAccepted)
	observe.ContinuationToken = result.ContinuationToken
	g.observeAssistantRun(reservation, client, observe)
	g.rememberContinuation(conversationID, result.ContinuationToken)
	g.rememberRun(conversationID, runID)
	public := assistantapi.SendTurnResponse{RunID: runID}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}

func (g *assistantGateway) handleApproval(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, false)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	conversationID := CurrentRequest().PathParams.Get("conversation_id")
	approvalToken := CurrentRequest().PathParams.Get("approval_id")
	claims, err := g.conversationClaims(conversationID, identity)
	if err != nil {
		g.writeError(w, err)
		return
	}
	approvalClaims, err := g.registration.TokenManager.UnsealApproval(approvalToken, assistanttoken.ApprovalExpectation{
		AssistantAddress: g.registration.AssistantAddress,
		OwnerDigest:      identity.Owner,
		ConversationID:   conversationID,
	})
	if err != nil {
		g.writeError(w, assistanttoken.ErrNotFound)
		return
	}
	body, err := readAssistantBody(req)
	if err != nil {
		g.writeError(w, err)
		return
	}
	request, err := assistantapi.DecodeResolveApprovalRequest(body)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	decision := assistantcontrol.DecisionDeny
	if request.Decision == "approve" {
		decision = assistantcontrol.DecisionAllow
	}
	approvalRequest := assistantruntime.ApprovalRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		RunID:             approvalClaims.RunID,
		ApprovalID:        approvalClaims.ApprovalID,
		Decision:          decision,
	}
	client := g.currentClient()
	_, err = g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return nil, client.ResolveApproval(ctx, approvalRequest)
	})
	wakeAssistantRun(g.registration.AssistantAddress, identity.Principal, claims.ConversationDigest, approvalClaims.RunID)
	if err != nil {
		g.writeError(w, err)
		return
	}
	public := assistantapi.ResolveApprovalResponse{ApprovalID: approvalToken, Decision: request.Decision}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}

func (g *assistantGateway) handleCancel(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, false)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	conversationID := CurrentRequest().PathParams.Get("conversation_id")
	runID := CurrentRequest().PathParams.Get("run_id")
	if err := assistantapi.ValidateRunID(runID); err != nil {
		g.writeError(w, err)
		return
	}
	claims, err := g.conversationClaims(conversationID, identity)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if g.wasCancelled(conversationID, runID) {
		writeAssistantJSON(w, assistantapi.CancelRunResponse{RunID: runID, State: "cancelled"})
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	cancelRequest := assistantruntime.CancelRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		RunID:             runID,
	}
	client := g.currentClient()
	_, err = g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return nil, client.CancelRun(ctx, cancelRequest)
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	g.rememberCancelled(conversationID, runID)
	wakeAssistantRun(g.registration.AssistantAddress, identity.Principal, claims.ConversationDigest, runID)
	public := assistantapi.CancelRunResponse{RunID: runID, State: "cancelled"}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}
