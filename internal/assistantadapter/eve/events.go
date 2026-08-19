package eve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

// EventContext carries the negotiated private identity around one Eve event.
type EventContext struct {
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string
	ApprovalNeverTools []string
	PrivateSessionID   string
	ContinuationToken  string
	RunID              string
	After              uint64
	OccurredAt         time.Time
}

type providerEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	Meta struct {
		At string `json:"at"`
	} `json:"meta"`
}

// NormalizeProviderEvent converts one Eve stream event into the private
// Scenery control catalog. Events with no provider-neutral equivalent return
// (zero, false, nil) and should be skipped by the stream adapter.
func NormalizeProviderEvent(raw []byte, context EventContext, sequence uint64) (assistantcontrol.Event, bool, error) {
	var event providerEvent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&event); err != nil {
		return assistantcontrol.Event{}, false, fmt.Errorf("decode Eve event: %w", err)
	}
	if strings.TrimSpace(event.Type) == "" {
		return assistantcontrol.Event{}, false, errors.New("Eve event type is required")
	}
	if sequence == 0 {
		sequence = context.After + 1
	}
	if sequence == 0 {
		sequence = 1
	}
	occurred := context.OccurredAt
	if event.Meta.At != "" {
		parsed, err := time.Parse(time.RFC3339Nano, event.Meta.At)
		if err == nil {
			occurred = parsed.UTC()
		}
	}
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	} else {
		occurred = occurred.UTC()
	}
	var data map[string]any
	if len(event.Data) != 0 {
		_ = json.Unmarshal(event.Data, &data)
	}
	base := func(kind string, payload any) (assistantcontrol.Event, bool, error) {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return assistantcontrol.Event{}, false, err
		}
		result := assistantcontrol.Event{
			Kind:               assistantcontrol.EventKind,
			SchemaRevision:     assistantcontrol.EventSchemaRevision,
			Type:               kind,
			AssistantAddress:   context.AssistantAddress,
			RuntimeRevision:    context.RuntimeRevision,
			CapabilityRevision: context.CapabilityRevision,
			PrivateSessionID:   context.PrivateSessionID,
			ContinuationToken:  context.ContinuationToken,
			RunID:              context.RunID,
			Sequence:           sequence,
			OccurredAt:         occurred,
			Data:               encoded,
		}
		// Variant-specific envelope fields (capability name, proposal, and
		// approval payloads) are filled by the caller immediately below.
		if kind != assistantcontrol.EventCapabilityProposal && kind != assistantcontrol.EventApprovalWait && kind != assistantcontrol.EventCapabilityStarted && kind != assistantcontrol.EventCapabilityCompleted {
			if err := result.Validate(); err != nil {
				return assistantcontrol.Event{}, false, err
			}
		}
		return result, true, nil
	}
	stringValue := func(key string) string {
		value, _ := data[key].(string)
		return value
	}
	switch event.Type {
	case "turn.started":
		return base(assistantcontrol.EventRunStarted, map[string]string{"state": "started"})
	case "message.appended":
		return base(assistantcontrol.EventTextDelta, map[string]string{"text": stringValue("messageDelta")})
	case "message.completed":
		return base(assistantcontrol.EventMessageCompleted, map[string]string{"text": stringValue("message")})
	case "actions.requested":
		var payload struct {
			Actions []struct {
				CallID   string          `json:"callId"`
				Kind     string          `json:"kind"`
				Input    json.RawMessage `json:"input"`
				ToolName string          `json:"toolName"`
			} `json:"actions"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return assistantcontrol.Event{}, false, fmt.Errorf("decode Eve actions: %w", err)
		}
		for _, action := range payload.Actions {
			if action.Kind != "tool-call" || action.CallID == "" || action.ToolName == "" {
				continue
			}
			input := action.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			proposal := assistantcontrol.CapabilityProposal{ApprovalID: action.CallID, CapabilityName: action.ToolName, Input: input, RequiresApproval: requiresToolApproval(action.ToolName, context.ApprovalNeverTools)}
			encoded, err := json.Marshal(map[string]any{"input": json.RawMessage(input)})
			if err != nil {
				return assistantcontrol.Event{}, false, err
			}
			result, ok, err := base(assistantcontrol.EventCapabilityProposal, json.RawMessage(encoded))
			if err != nil || !ok {
				return assistantcontrol.Event{}, false, err
			}
			result.CapabilityName = action.ToolName
			result.ApprovalID = action.CallID
			result.Proposal = &proposal
			if err := result.Validate(); err != nil {
				return assistantcontrol.Event{}, false, err
			}
			return result, true, nil
		}
		return assistantcontrol.Event{}, false, nil
	case "input.requested":
		var payload struct {
			Requests []struct {
				Kind      string `json:"kind"`
				Prompt    string `json:"prompt"`
				RequestID string `json:"requestId"`
				Action    struct {
					ToolName string `json:"toolName"`
				} `json:"action"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return assistantcontrol.Event{}, false, fmt.Errorf("decode Eve input request: %w", err)
		}
		for _, request := range payload.Requests {
			if request.Kind != "tool-approval" || request.RequestID == "" {
				continue
			}
			wait := assistantcontrol.ApprovalWait{ApprovalID: request.RequestID}
			result, ok, err := base(assistantcontrol.EventApprovalWait, map[string]string{"prompt": request.Prompt})
			if err != nil || !ok {
				return assistantcontrol.Event{}, false, err
			}
			result.ApprovalID = request.RequestID
			result.CapabilityName = request.Action.ToolName
			result.ApprovalWait = &wait
			if err := result.Validate(); err != nil {
				return assistantcontrol.Event{}, false, err
			}
			return result, true, nil
		}
		return assistantcontrol.Event{}, false, nil
	case "action.result":
		var payload struct {
			Result struct {
				ToolName string `json:"toolName"`
				Name     string `json:"name"`
			} `json:"result"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return assistantcontrol.Event{}, false, fmt.Errorf("decode Eve action result: %w", err)
		}
		name := payload.Result.ToolName
		if name == "" {
			name = payload.Result.Name
		}
		if name == "" {
			name = "capability"
		}
		return withCapability(base, assistantcontrol.EventCapabilityCompleted, name, map[string]bool{"ok": payload.Status == "completed"})
	case "turn.completed":
		return base(assistantcontrol.EventRunCompleted, map[string]string{"state": "completed"})
	case "turn.cancelled":
		return base(assistantcontrol.EventRunCancelled, map[string]string{"state": "cancelled"})
	case "turn.failed", "step.failed", "session.failed":
		return base(assistantcontrol.EventRunFailed, map[string]string{"code": "assistant_failed", "message": "assistant run failed"})
	case "session.started", "session.waiting", "session.completed", "message.received", "reasoning.appended", "reasoning.completed", "step.started", "step.completed", "context.cleared", "compaction.requested", "compaction.completed", "authorization.required", "authorization.completed", "subagent.called", "subagent.started", "subagent.event", "subagent.completed", "action.partial", "input.resolved":
		return assistantcontrol.Event{}, false, nil
	default:
		return assistantcontrol.Event{}, false, fmt.Errorf("unsupported Eve event type %q", event.Type)
	}
}

func requiresToolApproval(toolName string, approvalNeverTools []string) bool {
	for _, allowed := range approvalNeverTools {
		if toolName == allowed {
			return false
		}
	}
	return true
}

func withCapability(base func(string, any) (assistantcontrol.Event, bool, error), typ, name string, payload any) (assistantcontrol.Event, bool, error) {
	result, ok, err := base(typ, payload)
	if err != nil || !ok {
		return assistantcontrol.Event{}, false, err
	}
	result.CapabilityName = name
	if err := result.Validate(); err != nil {
		return assistantcontrol.Event{}, false, err
	}
	return result, true, nil
}

// NormalizeProviderEvents reads newline-delimited provider events and assigns
// deterministic positive private sequence numbers beginning after context.After.
func NormalizeProviderEvents(reader io.Reader, context EventContext) ([]assistantcontrol.Event, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4<<10), 16<<20)
	result := make([]assistantcontrol.Event, 0)
	sequence := context.After
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		lines, err := splitProviderEvent(line)
		if err != nil {
			return nil, err
		}
		for _, split := range lines {
			event, ok, err := NormalizeProviderEvent(split, context, sequence+1)
			if err != nil {
				return nil, err
			}
			if ok {
				sequence++
				result = append(result, event)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) > 1 {
		if err := assistantcontrol.ValidateEventSequence(result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// splitProviderEvent turns a parallel Eve action/request event into one
// provider event per item so the private Scenery cursor can assign a
// consecutive sequence to every proposal or approval.
func splitProviderEvent(raw []byte) ([][]byte, error) {
	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
		Meta json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode Eve event: %w", err)
	}
	if envelope.Type != "actions.requested" && envelope.Type != "input.requested" {
		return [][]byte{raw}, nil
	}
	if len(envelope.Data) == 0 {
		return [][]byte{raw}, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("decode Eve event data: %w", err)
	}
	key := "actions"
	if envelope.Type == "input.requested" {
		key = "requests"
	}
	var items []json.RawMessage
	if encoded := data[key]; len(encoded) != 0 {
		if err := json.Unmarshal(encoded, &items); err != nil {
			return nil, fmt.Errorf("decode Eve %s: %w", key, err)
		}
	}
	if len(items) == 0 {
		return [][]byte{raw}, nil
	}
	result := make([][]byte, 0, len(items))
	for _, item := range items {
		one := make(map[string]json.RawMessage, len(data)+1)
		for name, value := range data {
			one[name] = value
		}
		encoded, err := json.Marshal([]json.RawMessage{item})
		if err != nil {
			return nil, err
		}
		one[key] = encoded
		oneData, err := json.Marshal(one)
		if err != nil {
			return nil, err
		}
		split, err := json.Marshal(struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
			Meta json.RawMessage `json:"meta,omitempty"`
		}{Type: envelope.Type, Data: oneData, Meta: envelope.Meta})
		if err != nil {
			return nil, err
		}
		result = append(result, split)
	}
	return result, nil
}
