package assistantapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicIdentityAndCreateResponse(t *testing.T) {
	response, err := NewCreateConversationResponse([]byte{0xab, 0xcd}, "/assistants/support/v1/conversations/conv1_abcd/events", bytes.NewReader(bytes.Repeat([]byte{0x11}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if response.ConversationID != "conv1_abcd" || response.RunID != "run_"+strings.Repeat("11", 16) {
		t.Fatalf("response ids = %#v", response)
	}
	encoded, err := MarshalCreateConversationResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "kind") || strings.Contains(string(encoded), "schema_revision") {
		t.Fatalf("public response leaked machine envelope fields: %s", encoded)
	}
	decoded, err := DecodeCreateConversationResponse(encoded)
	if err != nil || decoded != response {
		t.Fatalf("response round trip = %#v, %v", decoded, err)
	}
	if _, err := ConversationEventsURL("support", response.ConversationID); err != nil {
		t.Fatal(err)
	}
}

func TestStrictPublicRequestsAndErrors(t *testing.T) {
	request, err := DecodeCreateConversationRequest([]byte(`{"message":{"role":"user","content":"hello"}}`))
	if err != nil || request.Message.Content != "hello" {
		t.Fatalf("request = %#v, %v", request, err)
	}
	for _, encoded := range [][]byte{
		[]byte(`{"message":{"role":"user","content":"hello"},"extra":true}`),
		[]byte(`{"message":{"role":"user","content":"hello"}} {}`),
		[]byte(`{"message":{"role":"assistant","content":"hello"}}`),
	} {
		if _, err := DecodeCreateConversationRequest(encoded); err == nil {
			t.Fatalf("accepted invalid request %s", encoded)
		}
	}
	approval, err := DecodeResolveApprovalRequest([]byte(`{"decision":"approve"}`))
	if err != nil || approval.Decision != ApprovalApprove {
		t.Fatalf("approval = %#v, %v", approval, err)
	}
	if _, err := DecodeResolveApprovalRequest([]byte(`{"decision":"maybe"}`)); err == nil {
		t.Fatal("accepted unsupported approval decision")
	}
	if err := (ResolveApprovalResponse{ApprovalID: "appr1_abcd", Decision: ApprovalApprove}).Validate(); err != nil {
		t.Fatalf("valid approval response rejected: %v", err)
	}
	for _, approvalID := range []string{"", "approval_abcd", "appr1_abc", "appr1_ABCD"} {
		if err := (ResolveApprovalResponse{ApprovalID: approvalID, Decision: ApprovalApprove}).Validate(); err == nil {
			t.Fatalf("accepted invalid approval id %q", approvalID)
		}
	}
	if err := (CancelRunResponse{RunID: "run_" + strings.Repeat("11", 16), State: "cancelled"}).Validate(); err != nil {
		t.Fatalf("valid cancel response rejected: %v", err)
	}
	if err := (CancelRunResponse{RunID: "run_" + strings.Repeat("11", 16), State: "done"}).Validate(); err == nil {
		t.Fatal("accepted non-cancelled state")
	}
	publicError := NewError(ErrorUnavailable, "helper unavailable")
	if err := publicError.Validate(); err != nil {
		t.Fatal(err)
	}
	oversized := NewError(ErrorInternal, strings.Repeat("eve", MaxErrorMessageBytes))
	if len(oversized.Message) > MaxErrorMessageBytes {
		t.Fatalf("redacted public error exceeded limit: %d", len(oversized.Message))
	}
	if err := oversized.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeError([]byte(`{"code":"unavailable","message":"down","extra":true}`)); err == nil {
		t.Fatal("accepted unknown public error field")
	}
}

func TestStrictDecodersRejectDuplicateMembersRecursively(t *testing.T) {
	requestCases := [][]byte{
		[]byte(`{"message":{"role":"user","content":"hello"},"message":{"role":"user","content":"again"}}`),
		[]byte(`{"message":{"role":"user","role":"user","content":"hello"}}`),
	}
	for _, encoded := range requestCases {
		if _, err := DecodeCreateConversationRequest(encoded); err == nil {
			t.Fatalf("accepted duplicate request member: %s", encoded)
		}
	}

	encoded := []byte(`{"type":"assistant.run.started","assistant":"support","conversation_id":"conv1_abcd","run_id":"run_11111111111111111111111111111111","sequence":1,"occurred_at":"1970-01-01T00:00:00Z","data":{"state":"started","state":"again"}}`)
	if _, err := DecodeEvent(encoded); err == nil {
		t.Fatal("accepted duplicate nested event data member")
	}
	encoded = []byte(`{"type":"assistant.message.delta","assistant":"support","conversation_id":"conv1_abcd","run_id":"run_11111111111111111111111111111111","sequence":1,"occurred_at":"1970-01-01T00:00:00Z","data":{"text":null}}`)
	if _, err := DecodeEvent(encoded); err == nil {
		t.Fatal("accepted null event text")
	}
}

func TestPublicLimitsAndEventIdentity(t *testing.T) {
	if err := (Message{Role: "user", Content: strings.Repeat("x", MaxMessageBytes+1)}).ValidateUser(); err == nil {
		t.Fatal("accepted oversized message")
	}
	if err := (Error{Code: ErrorInternal, Message: strings.Repeat("x", MaxErrorMessageBytes+1)}).Validate(); err == nil {
		t.Fatal("accepted oversized error")
	}
	if err := (Message{Role: "user", Content: "ok"}).ValidateUser(); err != nil {
		t.Fatal(err)
	}
	data := json.RawMessage(`{"blob":"` + strings.Repeat("x", MaxEventDataBytes) + `"}`)
	if _, err := NewEvent(EventRunStarted, "support", "conv1_abcd", "run_11111111111111111111111111111111", 1, time.Unix(0, 0).UTC(), data); err == nil {
		t.Fatal("accepted oversized event data")
	}

	tooLongHex := strings.Repeat("ab", MaxIdentifierHexBytes/2+1)
	for _, value := range []string{"conv1_" + tooLongHex, "run_" + tooLongHex, "appr1_" + tooLongHex} {
		if err := map[string]func(string) error{
			"conv1_": ValidateConversationID,
			"run_":   ValidateRunID,
			"appr1_": ValidateApprovalID,
		}[value[:strings.IndexByte(value, '_')+1]](value); err == nil {
			t.Fatalf("accepted oversized identifier %q", value[:strings.IndexByte(value, '_')+1])
		}
	}
	if _, err := NewConversationID(make([]byte, MaxIdentifierHexBytes/2+1)); err == nil {
		t.Fatal("accepted oversized sealed handle")
	}

	first := mustTestEvent(t, EventRunStarted, "support", "conv1_abcd", "run_11111111111111111111111111111111", 1)
	second := mustTestEvent(t, EventRunCompleted, "other", first.ConversationID, first.RunID, 2)
	if err := ValidateEventSequences([]Event{first, second}); err == nil {
		t.Fatal("accepted mixed assistant identities")
	}
	second = mustTestEvent(t, EventRunCompleted, first.Assistant, "conv1_cdef", first.RunID, 2)
	if err := ValidateEventSequences([]Event{first, second}); err == nil {
		t.Fatal("accepted mixed conversation identities")
	}
}

func TestEventsURLCanonicality(t *testing.T) {
	valid := "/assistants/support/v1/conversations/conv1_abcd/events"
	if err := ValidateEventsURL(valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEventsURLForSurface(valid, "/assistants/support", "conv1_abcd"); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{
		"//assistants/support/v1/conversations/conv1_abcd/events",
		"/assistants//support/v1/conversations/conv1_abcd/events",
		"/assistants/support/v1/conversations/conv1_abcd/events?after=1",
		"/assistants/support/v1/conversations/conv1_abcd/events#tail",
		"/assistants/support/v1/conversations/conv1_abcd\\events",
		"https://example.test/assistants/support/v1/conversations/conv1_abcd/events",
		"/assistants/support/./v1/conversations/conv1_abcd/events",
		"/assistants/support/v1/conversations/conv1_abcd/events%2Ftail",
		"/assistants/support/v1/conversations/conv1_abcd/events/v1/conversations/conv1_cdef/events",
	} {
		if err := ValidateEventsURL(candidate); err == nil {
			t.Fatalf("accepted URL attack %q", candidate)
		}
	}
	if err := ValidateEventsURLForSurface(valid, "/assistants/other", "conv1_abcd"); err == nil {
		t.Fatal("accepted URL for wrong assistant surface")
	}
	if err := ValidateEventsURLForSurface(valid, "/assistants/support", "conv1_cdef"); err == nil {
		t.Fatal("accepted URL for wrong conversation")
	}
}

func TestEventTimestampIsUTCAndMarshalsCanonicalZ(t *testing.T) {
	event := mustTestEvent(t, EventRunStarted, "support", "conv1_abcd", "run_11111111111111111111111111111111", 1)
	event.OccurredAt = time.Date(2026, 8, 4, 2, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	if err := event.Validate(); err == nil {
		t.Fatal("accepted non-UTC event timestamp")
	}
	event.OccurredAt = time.Date(2026, 8, 4, 0, 0, 0, 0, time.FixedZone("UTC+0", 0))
	encoded, err := MarshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"occurred_at":"2026-08-04T00:00:00Z"`) {
		t.Fatalf("timestamp was not canonicalized to Z: %s", encoded)
	}
}

func mustTestEvent(t *testing.T, eventType, assistant, conversationID, runID string, sequence uint64) Event {
	t.Helper()
	data := json.RawMessage(`{"state":"started"}`)
	switch eventType {
	case EventRunCompleted:
		data = json.RawMessage(`{"state":"completed"}`)
	case EventRunCancelled:
		data = json.RawMessage(`{"state":"cancelled"}`)
	case EventRuntimeRestarting:
		data = json.RawMessage(`{"state":"restarting"}`)
	case EventMessageDelta, EventMessageCompleted:
		data = json.RawMessage(`{"text":"ok"}`)
	case EventCapabilityProposed:
		data = json.RawMessage(`{"capability":"test","approval_id":"appr1_abcd","input":{}}`)
	case EventApprovalRequired:
		data = json.RawMessage(`{"capability":"test","approval_id":"appr1_abcd"}`)
	case EventCapabilityStarted:
		data = json.RawMessage(`{"capability":"test"}`)
	case EventCapabilityCompleted:
		data = json.RawMessage(`{"capability":"test","state":"completed"}`)
	case EventRunFailed:
		data = json.RawMessage(`{"code":"failed","message":"failed"}`)
	}
	event, err := NewEvent(eventType, assistant, conversationID, runID, sequence, time.Unix(int64(sequence), 0).UTC(), data)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestStrictEventEnvelopeAndCursorResume(t *testing.T) {
	first, err := NewEvent(EventRunStarted, "support", "conv1_abcd", "run_"+strings.Repeat("11", 16), 1, time.Unix(0, 0).UTC(), json.RawMessage(`{"state":"started"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEvent(EventMessageDelta, "support", "conv1_abcd", first.RunID, 2, time.Unix(1, 0).UTC(), json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeNDJSON([]Event{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "kind") || strings.Contains(string(encoded), "schema_revision") {
		t.Fatalf("event envelope has unexpected machine fields: %s", encoded)
	}
	events, err := DecodeNDJSON(encoded)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	resumed, err := EventsAfter(events, Cursor(1))
	if err != nil || len(resumed) != 1 || resumed[0].Sequence != 2 {
		t.Fatalf("resumed = %#v, %v", resumed, err)
	}
	if _, err := EventsAfter([]Event{second, first}, 0); err == nil {
		t.Fatal("accepted non-monotonic event stream")
	}
	if _, err := DecodeEvent([]byte(`{"type":"assistant.unknown","assistant":"support","conversation_id":"conv1_abcd","run_id":"run_11111111111111111111111111111111","sequence":1,"occurred_at":"1970-01-01T00:00:00Z","data":{}}`)); err == nil {
		t.Fatal("accepted unknown event type")
	}
}
