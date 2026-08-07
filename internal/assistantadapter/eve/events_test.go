package eve

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

func TestNormalizeProviderEventsUsesPrivateCursorAndStrictNeutralEvents(t *testing.T) {
	lines := []byte(stringsJoin(
		`{"type":"session.started","data":{},"meta":{"at":"2026-01-01T00:00:00Z"}}`,
		`{"type":"turn.started","data":{"sequence":0,"turnId":"turn"},"meta":{"at":"2026-01-01T00:00:01Z"}}`,
		`{"type":"message.appended","data":{"messageDelta":"hello"},"meta":{"at":"2026-01-01T00:00:02Z"}}`,
		`{"type":"actions.requested","data":{"actions":[{"kind":"tool-call","callId":"call-1","toolName":"local","input":{"value":"x"}}]},"meta":{"at":"2026-01-01T00:00:03Z"}}`,
		`{"type":"input.requested","data":{"requests":[{"kind":"tool-approval","requestId":"approval-1","prompt":"Approve","action":{"kind":"tool-call","callId":"call-1","toolName":"local","input":{"value":"x"}}}]},"meta":{"at":"2026-01-01T00:00:04Z"}}`,
		`{"type":"turn.completed","data":{},"meta":{"at":"2026-01-01T00:00:05Z"}}`,
	))
	ctx := EventContext{AssistantAddress: "assistant/support", RuntimeRevision: "runtime", CapabilityRevision: "capability", PrivateSessionID: "session", ContinuationToken: "token", RunID: "run", OccurredAt: time.Unix(1, 0).UTC()}
	events, err := NormalizeProviderEvents(bytes.NewReader(lines), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("events=%d want=5", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Errorf("event[%d] sequence=%d", index, event.Sequence)
		}
		encoded, err := assistantcontrol.MarshalEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := assistantcontrol.ParseEvent(encoded); err != nil {
			t.Fatalf("strict parse event[%d]: %v (%s)", index, err, encoded)
		}
	}
	if events[2].Proposal == nil || events[2].Proposal.CapabilityName != "local" || !events[2].Proposal.RequiresApproval {
		t.Fatalf("proposal=%+v", events[2].Proposal)
	}
	if events[3].ApprovalWait == nil || events[3].ApprovalWait.ApprovalID != "approval-1" {
		t.Fatalf("approval=%+v", events[3].ApprovalWait)
	}
	if events[4].Type != assistantcontrol.EventRunCompleted {
		t.Fatalf("terminal event=%q", events[4].Type)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(mustMarshal(t, events[2]), &roundTrip); err != nil {
		t.Fatal(err)
	}
	if _, ok := roundTrip["capability_proposal"]; !ok {
		t.Fatalf("proposal field missing: %v", roundTrip)
	}
}

func TestNormalizeProviderEventRejectsUnknownProviderType(t *testing.T) {
	ctx := EventContext{AssistantAddress: "assistant/support", RuntimeRevision: "runtime", CapabilityRevision: "capability", PrivateSessionID: "session", ContinuationToken: "token", RunID: "run"}
	if _, _, err := NormalizeProviderEvent([]byte(`{"type":"provider.secret_event","data":{}}`), ctx, 1); err == nil {
		t.Fatal("unknown provider event accepted")
	}
}

func TestNormalizeProviderEventMarksApprovalFreeCapability(t *testing.T) {
	ctx := EventContext{
		AssistantAddress: "assistant/support", RuntimeRevision: "runtime", CapabilityRevision: "capability",
		ApprovalNeverTools: []string{"scenery__safe"}, PrivateSessionID: "session", ContinuationToken: "token", RunID: "run",
	}
	event, ok, err := NormalizeProviderEvent([]byte(`{"type":"actions.requested","data":{"actions":[{"kind":"tool-call","callId":"call-1","toolName":"scenery__safe","input":{}}]}}`), ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || event.Proposal == nil || event.Proposal.RequiresApproval {
		t.Fatalf("approval-free proposal = %+v", event.Proposal)
	}
}

func TestNormalizeProviderEventsEmitsEveryParallelActionAndApproval(t *testing.T) {
	lines := []byte(stringsJoin(
		`{"type":"actions.requested","data":{"actions":[{"kind":"tool-call","callId":"call-1","toolName":"local","input":{}},{"kind":"tool-call","callId":"call-2","toolName":"other","input":{}}]}}`,
		`{"type":"input.requested","data":{"requests":[{"kind":"tool-approval","requestId":"approval-1","action":{"toolName":"local"}},{"kind":"tool-approval","requestId":"approval-2","action":{"toolName":"other"}}]}}`,
	))
	context := EventContext{AssistantAddress: "assistant/support", RuntimeRevision: "runtime", CapabilityRevision: "capability", PrivateSessionID: "session", ContinuationToken: "token", RunID: "run"}
	events, err := NormalizeProviderEvents(bytes.NewReader(lines), context)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events=%d want=4", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event[%d] sequence=%d", index, event.Sequence)
		}
	}
	if events[0].ApprovalID != "call-1" || events[1].ApprovalID != "call-2" || events[2].ApprovalID != "approval-1" || events[3].ApprovalID != "approval-2" {
		t.Fatalf("parallel IDs=%q,%q,%q,%q", events[0].ApprovalID, events[1].ApprovalID, events[2].ApprovalID, events[3].ApprovalID)
	}
}

func stringsJoin(lines ...string) string {
	result := ""
	for index, line := range lines {
		if index != 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
