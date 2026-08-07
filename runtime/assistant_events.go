package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/assistantapi"
	"scenery.sh/internal/assistantcontrol"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/assistanttoken"
)

func (g *assistantGateway) handleEvents(w http.ResponseWriter, req *http.Request) {
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
	afterValue := strings.TrimSpace(req.URL.Query().Get("after"))
	if afterValue == "" {
		afterValue = "0"
	}
	after, err := assistantapi.ParseCursor(afterValue)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	streamRequest := assistantruntime.StreamRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		After:             0, // Recompute from the beginning for stable redaction/cache.
	}
	client := g.currentClient()
	value, err := g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return client.StreamEvents(ctx, streamRequest)
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	stream, ok := value.(io.ReadCloser)
	if !ok || stream == nil {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	if err := g.streamPrivateEvents(w, stream, conversationID, claims, after); err != nil {
		g.writeError(w, err)
	}
}

func (g *assistantGateway) writeNDJSON(w http.ResponseWriter, events []assistantapi.Event) {
	// The marker is consumed by runtime's outer gzip wrapper; unlike ordinary
	// JSON responses, each NDJSON line must reach the client immediately.
	beginNDJSON(w)
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		if err := writeNDJSONEvent(w, event); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func beginNDJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", assistantapi.NDJSONContentType+"; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Scenery-Contract-Compression", "identity")
	if flusher, ok := w.(interface{ WriteHeader(int) }); ok {
		flusher.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeNDJSONEvent(w http.ResponseWriter, event assistantapi.Event) error {
	encoded, err := assistantapi.MarshalEvent(event)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = w.Write(encoded)
	return err
}

type assistantEventStreamState struct {
	gateway      *assistantGateway
	w            http.ResponseWriter
	after        assistantapi.Cursor
	conversation string
	claims       assistanttoken.ConversationClaims
	public       []assistantapi.Event
	pendingText  []int
	redactor     *assistantapi.Redactor
	previous     uint64
	lastRun      string
	written      int
	started      bool
}

func (g *assistantGateway) streamPrivateEvents(w http.ResponseWriter, stream io.ReadCloser, conversationID string, claims assistanttoken.ConversationClaims, after assistantapi.Cursor) error {
	if stream == nil {
		return assistantruntime.ErrMalformedEvent
	}
	defer func() { _ = stream.Close() }()
	state := &assistantEventStreamState{gateway: g, w: w, after: after, conversation: conversationID, claims: claims, redactor: assistantapi.NewRedactor()}
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64<<10), 17<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			return state.fail(assistantruntime.ErrMalformedEvent)
		}
		private, err := assistantcontrol.ParseEvent(line)
		if err != nil {
			return state.fail(assistantruntime.ErrMalformedEvent)
		}
		if err := state.accept(private); err != nil {
			return state.fail(err)
		}
	}
	if err := scanner.Err(); err != nil {
		return state.fail(assistantruntime.ErrMalformedEvent)
	}
	state.flushText()
	if err := state.flushPublic(); err != nil {
		return err
	}
	state.commit()
	if !state.started {
		beginNDJSON(w)
	}
	return nil
}

func (state *assistantEventStreamState) accept(private assistantcontrol.Event) error {
	if state.gateway == nil || state.redactor == nil {
		return assistantruntime.ErrMalformedEvent
	}
	if err := private.Validate(); err != nil {
		return assistantruntime.ErrMalformedEvent
	}
	if private.Sequence <= state.previous {
		return assistantruntime.ErrMalformedEvent
	}
	state.previous = private.Sequence
	if private.AssistantAddress != state.gateway.registration.AssistantAddress || private.PrivateSessionID != state.claims.PrivateSessionID {
		return assistantruntime.ErrMalformedEvent
	}
	if private.ContinuationToken == "" || private.ContinuationToken != state.claims.ContinuationToken {
		return assistantruntime.ErrMalformedEvent
	}
	if err := private.ValidateRevisions(state.gateway.registration.RuntimeRevision, state.gateway.registration.CapabilityRevision); err != nil {
		return assistantruntime.ErrRevisionMismatch
	}
	if private.Type != assistantcontrol.EventTextDelta {
		state.flushText()
		state.redactor.Reset()
	}
	if private.Type == assistantcontrol.EventMessageCompleted {
		state.flushText()
		state.redactor.Reset()
	}
	event, text, err := state.gateway.normalizePrivateEvent(state.conversation, state.claims, private, state.redactor, state.lastRun)
	if err != nil {
		return err
	}
	state.public = append(state.public, event)
	if text {
		state.pendingText = append(state.pendingText, len(state.public)-1)
	} else {
		if err := state.flushPublic(); err != nil {
			return err
		}
	}
	state.lastRun = event.RunID
	return nil
}

func (state *assistantEventStreamState) flushText() {
	if len(state.pendingText) == 0 {
		return
	}
	last := state.pendingText[len(state.pendingText)-1]
	appendPublicText(&state.public[last], state.redactor.Flush())
	state.pendingText = nil
}

func (state *assistantEventStreamState) flushPublic() error {
	for state.written < len(state.public) {
		event := state.public[state.written]
		state.written++
		if assistantapi.Cursor(event.Sequence) <= state.after {
			continue
		}
		if !state.started {
			beginNDJSON(state.w)
			state.started = true
		}
		if err := writeNDJSONEvent(state.w, event); err != nil {
			return err
		}
		if flusher, ok := state.w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return nil
}

func (state *assistantEventStreamState) fail(err error) error {
	if err == nil {
		return nil
	}
	if len(state.public) == 0 || state.lastRun == "" {
		return err
	}
	state.flushText()
	sequence := uint64(1)
	if len(state.public) > 0 {
		sequence = state.public[len(state.public)-1].Sequence + 1
	}
	neutral, createErr := assistantapi.NewEvent(assistantapi.EventRunFailed, state.gateway.registration.Name, state.conversation, state.lastRun, sequence, time.Now().UTC(), json.RawMessage(`{"code":"assistant_failed","message":"assistant run failed"}`))
	if createErr == nil {
		state.public = append(state.public, neutral)
		_ = state.flushPublic()
		state.commit()
		return nil
	}
	return err
}

func (state *assistantEventStreamState) commit() {
	if state.gateway == nil {
		return
	}
	if err := assistantapi.ValidateEventSequences(state.public); err != nil {
		return
	}
	state.gateway.mu.Lock()
	if state.gateway.publicEvents == nil {
		state.gateway.publicEvents = make(map[string][]assistantapi.Event)
	}
	state.gateway.publicEvents[state.conversation] = append([]assistantapi.Event(nil), state.public...)
	state.gateway.trimConversationCachesLocked()
	state.gateway.mu.Unlock()
}

func appendPublicText(event *assistantapi.Event, suffix string) {
	if event == nil || suffix == "" {
		return
	}
	var data map[string]any
	if json.Unmarshal(event.Data, &data) != nil {
		return
	}
	text, _ := data["text"].(string)
	data["text"] = text + suffix
	encoded, _ := json.Marshal(data)
	event.Data = encoded
}

func (g *assistantGateway) normalizePrivateEvent(conversationID string, claims assistanttoken.ConversationClaims, private assistantcontrol.Event, redactor *assistantapi.Redactor, previousRun string) (assistantapi.Event, bool, error) {
	publicName := g.registration.Name
	runID, err := g.publicRunID(conversationID, private.RunID, private.Type, previousRun)
	if err != nil {
		return assistantapi.Event{}, false, err
	}
	eventType := ""
	var data any
	textEvent := false
	safeData, _ := decodeAssistantObject(private.Data)
	switch private.Type {
	case assistantcontrol.EventRunStarted:
		eventType, data = assistantapi.EventRunStarted, map[string]any{"state": "started"}
	case assistantcontrol.EventTextDelta:
		eventType, textEvent = assistantapi.EventMessageDelta, true
		text, ok := safeData["text"].(string)
		if !ok {
			return assistantapi.Event{}, false, assistantruntime.ErrMalformedEvent
		}
		data = map[string]any{"text": redactor.Write(text)}
	case assistantcontrol.EventMessageCompleted:
		eventType = assistantapi.EventMessageCompleted
		text, _ := safeData["text"].(string)
		data = map[string]any{"text": assistantapi.RedactString(text)}
	case assistantcontrol.EventCapabilityProposal:
		eventType = assistantapi.EventCapabilityProposed
		capability, input, approvalID := private.CapabilityName, private.Data, private.ApprovalID
		if private.Proposal != nil {
			capability, input, approvalID = private.Proposal.CapabilityName, private.Proposal.Input, private.Proposal.ApprovalID
		}
		if capability == "" || approvalID == "" {
			return assistantapi.Event{}, false, assistantruntime.ErrMalformedEvent
		}
		publicApproval, err := g.publicApproval(conversationID, claims, private.RunID, approvalID, capability)
		if err != nil {
			return assistantapi.Event{}, false, err
		}
		data = map[string]any{"capability": assistantapi.RedactString(capability), "approval_id": publicApproval, "input": redactJSON(input)}
	case assistantcontrol.EventApprovalWait:
		eventType = assistantapi.EventApprovalRequired
		approvalID := private.ApprovalID
		if private.ApprovalWait != nil {
			approvalID = private.ApprovalWait.ApprovalID
		}
		if approvalID == "" || private.CapabilityName == "" {
			return assistantapi.Event{}, false, assistantruntime.ErrMalformedEvent
		}
		publicApproval, err := g.publicApproval(conversationID, claims, private.RunID, approvalID, private.CapabilityName)
		if err != nil {
			return assistantapi.Event{}, false, err
		}
		data = map[string]any{"capability": assistantapi.RedactString(private.CapabilityName), "approval_id": publicApproval}
	case assistantcontrol.EventCapabilityStarted:
		eventType, data = assistantapi.EventCapabilityStarted, map[string]any{"capability": assistantapi.RedactString(private.CapabilityName)}
	case assistantcontrol.EventCapabilityCompleted:
		eventType, data = assistantapi.EventCapabilityCompleted, map[string]any{"capability": assistantapi.RedactString(private.CapabilityName), "state": "completed"}
	case assistantcontrol.EventRunCompleted:
		eventType, data = assistantapi.EventRunCompleted, map[string]any{"state": "completed"}
	case assistantcontrol.EventRunCancelled:
		eventType, data = assistantapi.EventRunCancelled, map[string]any{"state": "cancelled"}
	case assistantcontrol.EventRunFailed:
		eventType = assistantapi.EventRunFailed
		// Helper/provider failure details are private. Keep one stable public
		// shape so callers cannot infer provider names or implementation errors.
		data = map[string]any{"code": "assistant_failed", "message": "assistant run failed"}
	case assistantcontrol.EventRuntimeCrashed, assistantcontrol.EventRuntimeRestarting:
		eventType, data = assistantapi.EventRuntimeRestarting, map[string]any{"state": "restarting"}
	default:
		return assistantapi.Event{}, false, assistantruntime.ErrMalformedEvent
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return assistantapi.Event{}, false, assistantruntime.ErrMalformedEvent
	}
	event, err := assistantapi.NewEvent(eventType, publicName, conversationID, runID, private.Sequence, private.OccurredAt.UTC(), encoded)
	if err != nil {
		return assistantapi.Event{}, false, assistantruntime.ErrMalformedEvent
	}
	return event, textEvent, nil
}

func decodeAssistantObject(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("assistant event data must be an object")
	}
	return value, nil
}

func redactJSON(value json.RawMessage) any {
	if len(value) == 0 || !json.Valid(value) {
		return map[string]any{}
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return map[string]any{}
	}
	return redactJSONValue(decoded)
}

func redactJSONValue(value any) any {
	switch typed := value.(type) {
	case string:
		return assistantapi.RedactString(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactJSONValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			lowerKey := strings.ToLower(key)
			if strings.Contains(lowerKey, "provider") || strings.Contains(lowerKey, "private") || strings.Contains(lowerKey, "continuation") || strings.Contains(lowerKey, "session") {
				continue
			}
			result[assistantapi.RedactString(key)] = redactJSONValue(item)
		}
		return result
	default:
		return value
	}
}

func (g *assistantGateway) publicApproval(conversationID string, claims assistanttoken.ConversationClaims, privateRunID, privateID, capability string) (string, error) {
	g.mu.Lock()
	for token, state := range g.approvals {
		if state.PrivateID == privateID && state.ConversationID == conversationID && state.RunID == privateRunID && state.OwnerDigest == claims.OwnerDigest {
			g.mu.Unlock()
			return token, nil
		}
	}
	g.mu.Unlock()
	decisionContext := "capability:" + assistantapi.RedactString(capability)
	token, err := g.registration.TokenManager.SealApproval(assistanttoken.ApprovalClaims{
		AssistantAddress: g.registration.AssistantAddress, ConversationDigest: assistanttoken.ConversationDigest(conversationID), RunID: privateRunID,
		ApprovalID: privateID, OwnerDigest: claims.OwnerDigest, DecisionContext: decisionContext,
	})
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	if g.approvals == nil {
		g.approvals = make(map[string]assistantApprovalState)
	}
	if existing := g.approvals[token]; existing.PrivateID != "" {
		g.mu.Unlock()
		return token, nil
	}
	g.approvals[token] = assistantApprovalState{PrivateID: privateID, ConversationID: conversationID, RunID: privateRunID, OwnerDigest: claims.OwnerDigest, DecisionContext: decisionContext}
	if len(g.approvals) > 4096 {
		keys := make([]string, 0, len(g.approvals))
		for value := range g.approvals {
			keys = append(keys, value)
		}
		sort.Strings(keys)
		for _, value := range keys[:len(keys)-4096] {
			delete(g.approvals, value)
		}
	}
	g.mu.Unlock()
	return token, nil
}
