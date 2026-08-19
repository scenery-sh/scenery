package assistantcontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestVariantsRoundTrip(t *testing.T) {
	tests := []Request{
		{Type: RequestCreateConversation, RequestID: "req-create", Principal: "principal-a", ConversationDigest: "sha256:conversation", Message: "hello"},
		{Type: RequestSendTurn, RequestID: "req-turn", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a", Message: "follow up"},
		{Type: RequestResumeEvents, RequestID: "req-resume", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", After: 4},
		{Type: RequestResolveApproval, RequestID: "req-approval", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a", ApprovalID: "approval-a", Decision: DecisionAllow},
		{Type: RequestCancelRun, RequestID: "req-cancel", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a"},
		{Type: RequestHealth, RequestID: "req-health"},
		{Type: RequestInfo, RequestID: "req-info"},
	}
	for _, original := range tests {
		request := withRequestIdentity(original)
		encoded, err := MarshalRequest(request)
		if err != nil {
			t.Fatalf("%s marshal: %v", request.Type, err)
		}
		parsed, err := ParseRequest(encoded)
		if err != nil {
			t.Fatalf("%s parse: %v\n%s", request.Type, err, encoded)
		}
		if !reflect.DeepEqual(request, parsed) {
			t.Fatalf("%s round trip changed request:\n%#v\n%#v", request.Type, request, parsed)
		}
	}
}

func TestResponseVariantsRoundTrip(t *testing.T) {
	descriptor := validDescriptor()
	health := &Health{Ready: true, RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a", Status: "ready"}
	tests := []Response{
		{Type: ResponseConversationCreated, PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a"},
		{Type: ResponseTurnAccepted, PrivateSessionID: "session-a", ContinuationToken: "continuation-b", RunID: "run-a"},
		{Type: ResponseEventsResumed, PrivateSessionID: "session-a", ContinuationToken: "continuation-b", NextSequence: 3, Events: []Event{validTextEvent(1), validTextEvent(2)}},
		{Type: ResponseApprovalResolved, PrivateSessionID: "session-a", ContinuationToken: "continuation-b", RunID: "run-a", ApprovalID: "approval-a", Decision: DecisionDeny},
		{Type: ResponseRunCancelled, PrivateSessionID: "session-a", ContinuationToken: "continuation-b", RunID: "run-a"},
		{Type: ResponseHealth, Health: health},
		{Type: ResponseInfo, Descriptor: &descriptor},
		{Type: ResponseError, Error: &Error{Code: "helper_unavailable", Message: "helper unavailable", Retryable: true}},
	}
	for _, original := range tests {
		response := withResponseIdentity(original)
		encoded, err := MarshalResponse(response)
		if err != nil {
			t.Fatalf("%s marshal: %v", response.Type, err)
		}
		parsed, err := ParseResponse(encoded)
		if err != nil {
			t.Fatalf("%s parse: %v\n%s", response.Type, err, encoded)
		}
		if !reflect.DeepEqual(response, parsed) {
			t.Fatalf("%s round trip changed response:\n%#v\n%#v", response.Type, response, parsed)
		}
	}
}

func TestEventVariantsRoundTripAndSignals(t *testing.T) {
	events := []Event{
		validTextEvent(1),
		func() Event {
			event := validTextEvent(2)
			event.Type = EventCapabilityProposal
			event.CapabilityName = "process_scene"
			event.ApprovalID = "approval-a"
			event.Proposal = &CapabilityProposal{ApprovalID: "approval-a", CapabilityName: "process_scene", Input: json.RawMessage(`{"scene_id":"one"}`), RequiresApproval: true}
			return event
		}(),
		func() Event {
			event := validTextEvent(3)
			event.Type = EventApprovalWait
			event.ApprovalID = "approval-a"
			event.ApprovalWait = &ApprovalWait{ApprovalID: "approval-a", ExpiresAt: "2030-01-01T00:00:00Z"}
			return event
		}(),
		func() Event {
			event := validTextEvent(4)
			event.Type = EventRunStarted
			return event
		}(),
		func() Event {
			event := validTextEvent(5)
			event.Type = EventMessageCompleted
			return event
		}(),
		func() Event {
			event := validTextEvent(6)
			event.Type = EventCapabilityStarted
			event.CapabilityName = "process_scene"
			return event
		}(),
		func() Event {
			event := validTextEvent(7)
			event.Type = EventCapabilityCompleted
			event.CapabilityName = "process_scene"
			return event
		}(),
		func() Event {
			event := validTextEvent(8)
			event.Type = EventRunCompleted
			return event
		}(),
		func() Event {
			event := validTextEvent(9)
			event.Type = EventRunFailed
			return event
		}(),
		func() Event {
			event := validTextEvent(10)
			event.Type = EventRunCancelled
			return event
		}(),
		func() Event {
			event := validTextEvent(11)
			event.Type = EventRuntimeRestarting
			event.RunID = "runtime"
			event.Crash = &CrashSignal{Code: "helper_restarted", Message: "helper restarted", Restartable: true}
			return event
		}(),
		func() Event {
			event := validTextEvent(12)
			event.Type = EventRuntimeCrashed
			event.RunID = "runtime"
			event.Crash = &CrashSignal{Code: "helper_crashed", Message: "helper exited", Restartable: true}
			return event
		}(),
		func() Event {
			event := validTextEvent(13)
			event.Type = EventProtocolMalformed
			event.RunID = "runtime"
			event.Malformed = &MalformedSignal{Code: "malformed_event", Message: "unknown event payload", ObservedType: "provider.event"}
			return event
		}(),
	}
	for _, original := range events {
		encoded, err := MarshalEvent(original)
		if err != nil {
			t.Fatalf("%s marshal: %v", original.Type, err)
		}
		parsed, err := ParseEvent(encoded)
		if err != nil {
			t.Fatalf("%s parse: %v\n%s", original.Type, err, encoded)
		}
		if !reflect.DeepEqual(original, parsed) {
			t.Fatalf("%s round trip changed event:\n%#v\n%#v", original.Type, original, parsed)
		}
	}
	if err := ValidateEventSequence(events[:10]); err != nil {
		t.Fatalf("valid event sequence rejected: %v", err)
	}
}

func TestRuntimeDescriptorRoundTripAndRevisions(t *testing.T) {
	descriptor := validDescriptor()
	encoded, err := MarshalRuntimeDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRuntimeDescriptor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(descriptor, parsed) {
		t.Fatalf("descriptor round trip changed value: %#v != %#v", descriptor, parsed)
	}
	if err := descriptor.ValidateRevisions("runtime-b", "capability-a"); err == nil {
		t.Fatal("expected runtime revision mismatch")
	} else {
		var mismatch RevisionMismatchError
		if !errors.As(err, &mismatch) || mismatch.Field != "runtime_revision" || mismatch.Expected != "runtime-b" || mismatch.Actual != "runtime-a" {
			t.Fatalf("revision error = %#v", err)
		}
	}
	if err := descriptor.ValidateRevisions("runtime-a", "capability-a"); err != nil {
		t.Fatal(err)
	}
}

func TestStrictParsingRejectsUnknownMalformedAndWrongIdentity(t *testing.T) {
	request := withRequestIdentity(Request{Type: RequestHealth, RequestID: "req-health"})
	encoded, err := MarshalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"unknown field":   append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"unknown":true}`)...),
		"trailing value":  append(append([]byte(nil), encoded...), []byte(` {}`)...),
		"duplicate field": []byte(`{"kind":"scenery.assistant.control.request","kind":"scenery.assistant.control.request"}`),
		"malformed JSON":  []byte(`{"kind":`),
		"wrong kind":      []byte(`{"kind":"wrong"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRequest(input); err == nil {
				t.Fatal("malformed request was accepted")
			}
		})
	}

	badData := request
	badData.Data = json.RawMessage(`{"unterminated":`)
	if _, err := MarshalRequest(badData); err == nil {
		t.Fatal("invalid request data was accepted")
	}
	badType := request
	badType.Type = "provider.native.event"
	if _, err := MarshalRequest(badType); err == nil {
		t.Fatal("unknown request type was accepted")
	}
}

func TestValidateEventSequenceRejectsDuplicatesAndOutOfOrder(t *testing.T) {
	for name, events := range map[string][]Event{
		"duplicate":    {validTextEvent(1), validTextEvent(1)},
		"out of order": {validTextEvent(2), validTextEvent(1)},
		"mixed session": func() []Event {
			second := validTextEvent(2)
			second.PrivateSessionID = "other-session"
			return []Event{validTextEvent(1), second}
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateEventSequence(events); err == nil {
				t.Fatal("invalid event sequence was accepted")
			}
		})
	}
}

func TestValidateEventSequenceAllowsMultipleRunsWithStableConversationIdentity(t *testing.T) {
	first := validTextEvent(1)
	second := validTextEvent(2)
	second.RunID = "run-b"
	if err := ValidateEventSequence([]Event{first, second}); err != nil {
		t.Fatalf("different runs in one conversation were rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Event){
		"assistant":           func(event *Event) { event.AssistantAddress = "app/assistant/other" },
		"runtime revision":    func(event *Event) { event.RuntimeRevision = "runtime-b" },
		"capability revision": func(event *Event) { event.CapabilityRevision = "capability-b" },
		"session":             func(event *Event) { event.PrivateSessionID = "session-b"; event.ContinuationToken = "continuation-b" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := second
			mutate(&bad)
			if err := ValidateEventSequence([]Event{first, bad}); err == nil {
				t.Fatal("identity drift was accepted")
			}
		})
	}
}

func TestRequestValidationRejectsConflictsAndBounds(t *testing.T) {
	base := withRequestIdentity(Request{Type: RequestCreateConversation, RequestID: "req-create", Principal: "principal-a", ConversationDigest: "sha256:conversation", Message: "hello"})
	tests := map[string]Request{
		"message and data": func() Request {
			request := base
			request.Data = json.RawMessage(`{"payload":true}`)
			return request
		}(),
		"session on create": func() Request {
			request := base
			request.PrivateSessionID = "session-a"
			return request
		}(),
		"missing principal": func() Request {
			request := base
			request.Principal = ""
			return request
		}(),
		"oversized request id": func() Request {
			request := base
			request.RequestID = strings.Repeat("x", maxControlIDBytes+1)
			return request
		}(),
		"whitespace request id": func() Request {
			request := base
			request.RequestID = "req create"
			return request
		}(),
		"oversized data": func() Request {
			request := base
			request.Message = ""
			request.Data = json.RawMessage(`"` + strings.Repeat("x", maxControlDataBytes) + `"`)
			return request
		}(),
	}
	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err == nil {
				t.Fatal("invalid request was accepted")
			}
		})
	}
}

func TestConversationRequestsRequireOwnershipMetadata(t *testing.T) {
	requests := []Request{
		{Type: RequestSendTurn, RequestID: "req-turn", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", Message: "hello"},
		{Type: RequestResumeEvents, RequestID: "req-resume", PrivateSessionID: "session-a", ContinuationToken: "continuation-a"},
		{Type: RequestResolveApproval, RequestID: "req-approval", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a", ApprovalID: "approval-a", Decision: DecisionAllow},
		{Type: RequestCancelRun, RequestID: "req-cancel", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a"},
	}
	for _, original := range requests {
		request := withRequestIdentity(original)
		for field := range map[string]struct{}{"principal": {}, "conversation digest": {}} {
			request := request
			if field == "principal" {
				request.Principal = ""
			} else {
				request.ConversationDigest = ""
			}
			if err := request.Validate(); err == nil {
				t.Fatalf("%s accepted missing %s", request.Type, field)
			}
		}
	}
}

func TestResponseValidationRejectsConflictsAndMismatchedEvents(t *testing.T) {
	base := withResponseIdentity(Response{
		Type:              ResponseEventsResumed,
		PrivateSessionID:  "session-a",
		ContinuationToken: "continuation-a",
		NextSequence:      3,
		Events:            []Event{validTextEvent(1), validTextEvent(2)},
	})
	tests := map[string]Response{
		"error with success": func() Response {
			response := base
			response.Error = &Error{Code: "bad", Message: "bad"}
			return response
		}(),
		"event assistant": func() Response {
			response := base
			response.Events = append([]Event(nil), base.Events...)
			response.Events[1].AssistantAddress = "app/assistant/other"
			return response
		}(),
		"event runtime revision": func() Response {
			response := base
			response.Events = append([]Event(nil), base.Events...)
			response.Events[1].RuntimeRevision = "runtime-b"
			return response
		}(),
		"event session": func() Response {
			response := base
			response.Events = append([]Event(nil), base.Events...)
			response.Events[1].PrivateSessionID = "session-b"
			response.Events[1].ContinuationToken = "continuation-b"
			return response
		}(),
		"next sequence before last": func() Response {
			response := base
			response.NextSequence = 2
			return response
		}(),
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if err := response.Validate(); err == nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
}

func TestEventValidationRejectsMetadataBoundsAndVariantConflicts(t *testing.T) {
	base := validTextEvent(1)
	tests := map[string]Event{
		"zero occurred_at": func() Event {
			event := base
			event.OccurredAt = time.Time{}
			return event
		}(),
		"non-UTC occurred_at": func() Event {
			event := base
			event.OccurredAt = time.Unix(1_700_000_000, 0).In(time.FixedZone("offset", 3600))
			return event
		}(),
		"oversized run id": func() Event {
			event := base
			event.RunID = strings.Repeat("r", maxControlIDBytes+1)
			return event
		}(),
		"oversized data": func() Event {
			event := base
			event.Data = json.RawMessage(`"` + strings.Repeat("x", maxControlDataBytes) + `"`)
			return event
		}(),
		"text with capability fields": func() Event {
			event := base
			event.CapabilityName = "process_scene"
			return event
		}(),
		"proposal mismatch": func() Event {
			event := base
			event.Type = EventCapabilityProposal
			event.CapabilityName = "process_scene"
			event.ApprovalID = "approval-a"
			event.Proposal = &CapabilityProposal{ApprovalID: "approval-b", CapabilityName: "process_scene", Input: json.RawMessage(`{}`)}
			return event
		}(),
		"crash with proposal": func() Event {
			event := base
			event.Type = EventRuntimeCrashed
			event.Crash = &CrashSignal{Code: "helper_crashed", Message: "helper exited"}
			event.Proposal = &CapabilityProposal{ApprovalID: "approval-a", CapabilityName: "process_scene", Input: json.RawMessage(`{}`)}
			return event
		}(),
	}
	for name, event := range tests {
		t.Run(name, func(t *testing.T) {
			if err := event.Validate(); err == nil {
				t.Fatal("invalid event was accepted")
			}
		})
	}
}

func TestRuntimeDescriptorRequiresCurrentProtocols(t *testing.T) {
	for name, mutate := range map[string]func(*RuntimeDescriptor){
		"control protocol": func(descriptor *RuntimeDescriptor) { descriptor.ControlProtocol = "scenery.assistant.control.stale" },
		"MCP protocol":     func(descriptor *RuntimeDescriptor) { descriptor.MCPProtocol = "2024-11-05" },
	} {
		t.Run(name, func(t *testing.T) {
			descriptor := validDescriptor()
			mutate(&descriptor)
			if err := descriptor.Validate(); err == nil {
				t.Fatal("unsupported protocol was accepted")
			}
		})
	}
}

func TestValidationIsConcurrencySafe(t *testing.T) {
	request := withRequestIdentity(Request{Type: RequestSendTurn, RequestID: "req-race", PrivateSessionID: "session-a", ContinuationToken: "continuation-a", Message: "hello"})
	event := validTextEvent(1)
	encodedRequest, err := MarshalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	encodedEvent, err := MarshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 100 {
				if _, err := ParseRequest(encodedRequest); err != nil {
					t.Errorf("parse request: %v", err)
					return
				}
				if _, err := ParseEvent(encodedEvent); err != nil {
					t.Errorf("parse event: %v", err)
					return
				}
			}
		})
	}
	group.Wait()
}

func TestCanonicalEncodingIsDeterministic(t *testing.T) {
	request := withRequestIdentity(Request{Type: RequestCreateConversation, RequestID: "req-canonical", Principal: "principal-a", ConversationDigest: "sha256:conversation", Data: json.RawMessage(`{"z":1,"a":2}`)})
	first, err := MarshalCanonical(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCanonical(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical encoding changed: %s != %s", first, second)
	}
	if !strings.Contains(string(first), `"data":{"a":2,"z":1}`) {
		t.Fatalf("nested data was not canonicalized: %s", first)
	}
}

func withRequestIdentity(request Request) Request {
	request.Kind = RequestKind
	request.SchemaRevision = RequestSchemaRevision
	request.AssistantAddress = "app/assistant/support"
	request.RuntimeRevision = "runtime-a"
	request.CapabilityRevision = "capability-a"
	if request.Type != RequestHealth && request.Type != RequestInfo {
		if request.Principal == "" {
			request.Principal = "principal-a"
		}
		if request.ConversationDigest == "" {
			request.ConversationDigest = "sha256:conversation"
		}
	}
	return request
}

func withResponseIdentity(response Response) Response {
	response.Kind = ResponseKind
	response.SchemaRevision = ResponseSchemaRevision
	response.RequestID = "req-response"
	response.AssistantAddress = "app/assistant/support"
	response.RuntimeRevision = "runtime-a"
	response.CapabilityRevision = "capability-a"
	return response
}

func validTextEvent(sequence uint64) Event {
	return Event{
		Kind: EventKind, SchemaRevision: EventSchemaRevision, Type: EventTextDelta,
		AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a",
		PrivateSessionID: "session-a", ContinuationToken: "continuation-a", RunID: "run-a", Sequence: sequence,
		OccurredAt: time.Unix(1_700_000_000+int64(sequence), 0).UTC(), Data: json.RawMessage(`{"text":"hello"}`),
	}
}

func validDescriptor() RuntimeDescriptor {
	return RuntimeDescriptor{
		Kind: RuntimeDescriptorKind, SchemaRevision: DescriptorSchemaRevision,
		AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a",
		ControlProtocol: ControlProtocol, MCPProtocol: "2025-11-25",
	}
}
