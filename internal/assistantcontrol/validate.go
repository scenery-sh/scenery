package assistantcontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/spec"
)

const (
	DecisionAllow       = "allow"
	DecisionDeny        = "deny"
	maxControlDataBytes = 16 << 20
	maxControlTextBytes = 64 << 10
	maxControlIDBytes   = 256
)

// ParseRequest strictly decodes one current control request. Unknown fields,
// duplicate members, trailing JSON, and invalid operation payloads fail closed.
func ParseRequest(data []byte) (Request, error) {
	var request Request
	if err := decodeStrict(data, &request); err != nil {
		return request, fmt.Errorf("parse assistant control request: %w", err)
	}
	if err := request.Validate(); err != nil {
		return request, err
	}
	return request, nil
}

// ParseResponse strictly decodes one current control response.
func ParseResponse(data []byte) (Response, error) {
	var response Response
	if err := decodeStrict(data, &response); err != nil {
		return response, fmt.Errorf("parse assistant control response: %w", err)
	}
	if err := response.Validate(); err != nil {
		return response, err
	}
	return response, nil
}

// ParseEvent strictly decodes one current private helper event.
func ParseEvent(data []byte) (Event, error) {
	var event Event
	if err := decodeStrict(data, &event); err != nil {
		return event, fmt.Errorf("parse assistant control event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return event, err
	}
	return event, nil
}

// ParseRuntimeDescriptor strictly decodes the helper's provider-neutral
// startup descriptor.
func ParseRuntimeDescriptor(data []byte) (RuntimeDescriptor, error) {
	var descriptor RuntimeDescriptor
	if err := decodeStrict(data, &descriptor); err != nil {
		return descriptor, fmt.Errorf("parse assistant runtime descriptor: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return descriptor, err
	}
	return descriptor, nil
}

// MarshalCanonical validates a recognized protocol value and emits its stable
// canonical JSON representation. Other JSON-compatible values are accepted for
// callers serializing typed private payloads.
func MarshalCanonical(value any) ([]byte, error) {
	if err := validateValue(value); err != nil {
		return nil, err
	}
	return spec.MarshalCanonical(value)
}

func MarshalRequest(request Request) ([]byte, error) {
	return MarshalCanonical(request)
}

func MarshalResponse(response Response) ([]byte, error) {
	return MarshalCanonical(response)
}

func MarshalEvent(event Event) ([]byte, error) {
	return MarshalCanonical(event)
}

func MarshalRuntimeDescriptor(descriptor RuntimeDescriptor) ([]byte, error) {
	return MarshalCanonical(descriptor)
}

func decodeStrict(data []byte, target any) error {
	if _, err := contract.DecodeJSONObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateValue(value any) error {
	switch typed := value.(type) {
	case Request:
		return typed.Validate()
	case *Request:
		if typed == nil {
			return errors.New("assistant control request is nil")
		}
		return typed.Validate()
	case Response:
		return typed.Validate()
	case *Response:
		if typed == nil {
			return errors.New("assistant control response is nil")
		}
		return typed.Validate()
	case Event:
		return typed.Validate()
	case *Event:
		if typed == nil {
			return errors.New("assistant control event is nil")
		}
		return typed.Validate()
	case RuntimeDescriptor:
		return typed.Validate()
	case *RuntimeDescriptor:
		if typed == nil {
			return errors.New("assistant runtime descriptor is nil")
		}
		return typed.Validate()
	default:
		return nil
	}
}

func (request Request) Validate() error {
	if err := validateIdentity(RequestKind, request.Kind, request.SchemaRevision, RequestSchemaRevision); err != nil {
		return err
	}
	if err := validateIdentifier("request_id", request.RequestID); err != nil {
		return err
	}
	if err := validateIdentifier("assistant_address", request.AssistantAddress); err != nil {
		return err
	}
	if err := validateRevision("runtime_revision", request.RuntimeRevision); err != nil {
		return err
	}
	if err := validateRevision("capability_revision", request.CapabilityRevision); err != nil {
		return err
	}
	if err := validateJSON(request.Data, false); err != nil {
		return fmt.Errorf("request data: %w", err)
	}
	switch request.Type {
	case RequestCreateConversation:
		if err := validateIdentifier("principal", request.Principal); err != nil {
			return err
		}
		if err := validateIdentifier("conversation_digest", request.ConversationDigest); err != nil {
			return err
		}
		if err := validateOptionalIdentifier("run_id", request.RunID); err != nil {
			return err
		}
		if err := validateMessageOrData(request.Message, request.Data); err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		if request.PrivateSessionID != "" || request.ContinuationToken != "" || request.ApprovalID != "" || request.Decision != "" || request.After != 0 {
			return errors.New("create request contains fields for another operation")
		}
	case RequestSendTurn:
		if err := validateIdentifier("principal", request.Principal); err != nil {
			return err
		}
		if err := validateIdentifier("conversation_digest", request.ConversationDigest); err != nil {
			return err
		}
		if err := validateSession(request.PrivateSessionID, request.ContinuationToken); err != nil {
			return err
		}
		if err := validateOptionalIdentifier("run_id", request.RunID); err != nil {
			return err
		}
		if err := validateMessageOrData(request.Message, request.Data); err != nil {
			return fmt.Errorf("turn request: %w", err)
		}
		if request.ApprovalID != "" || request.Decision != "" || request.After != 0 {
			return errors.New("turn request contains fields for another operation")
		}
	case RequestResumeEvents:
		if err := validateIdentifier("principal", request.Principal); err != nil {
			return err
		}
		if err := validateIdentifier("conversation_digest", request.ConversationDigest); err != nil {
			return err
		}
		if err := validateSession(request.PrivateSessionID, request.ContinuationToken); err != nil {
			return err
		}
		if request.RunID != "" || request.ApprovalID != "" || request.Decision != "" || request.Message != "" || len(request.Data) != 0 {
			return errors.New("resume request contains fields for another operation")
		}
	case RequestResolveApproval:
		if err := validateIdentifier("principal", request.Principal); err != nil {
			return err
		}
		if err := validateIdentifier("conversation_digest", request.ConversationDigest); err != nil {
			return err
		}
		if err := validateSession(request.PrivateSessionID, request.ContinuationToken); err != nil {
			return err
		}
		if err := validateIdentifier("run_id", request.RunID); err != nil {
			return err
		}
		if err := validateIdentifier("approval_id", request.ApprovalID); err != nil {
			return err
		}
		if request.Decision != DecisionAllow && request.Decision != DecisionDeny {
			return fmt.Errorf("approval decision %q is unsupported", request.Decision)
		}
		if request.After != 0 || request.Message != "" || len(request.Data) != 0 {
			return errors.New("approval request contains fields for another operation")
		}
	case RequestCancelRun:
		if err := validateIdentifier("principal", request.Principal); err != nil {
			return err
		}
		if err := validateIdentifier("conversation_digest", request.ConversationDigest); err != nil {
			return err
		}
		if err := validateSession(request.PrivateSessionID, request.ContinuationToken); err != nil {
			return err
		}
		if err := validateIdentifier("run_id", request.RunID); err != nil {
			return err
		}
		if request.ApprovalID != "" || request.Decision != "" || request.After != 0 || request.Message != "" || len(request.Data) != 0 {
			return errors.New("cancel request contains fields for another operation")
		}
	case RequestHealth, RequestInfo:
		// Health and info are runtime-scoped probes and intentionally carry no
		// session or user principal.
		if request.Principal != "" || request.ConversationDigest != "" || request.PrivateSessionID != "" || request.ContinuationToken != "" || request.RunID != "" || request.ApprovalID != "" || request.Decision != "" || request.After != 0 || request.Message != "" || len(request.Data) != 0 {
			return errors.New("health/info request contains fields for another operation")
		}
	default:
		return fmt.Errorf("assistant control request type %q is unsupported", request.Type)
	}
	return nil
}

func (response Response) Validate() error {
	if err := validateIdentity(ResponseKind, response.Kind, response.SchemaRevision, ResponseSchemaRevision); err != nil {
		return err
	}
	if err := validateIdentifier("request_id", response.RequestID); err != nil {
		return err
	}
	if err := validateIdentifier("assistant_address", response.AssistantAddress); err != nil {
		return err
	}
	if err := validateRevision("runtime_revision", response.RuntimeRevision); err != nil {
		return err
	}
	if err := validateRevision("capability_revision", response.CapabilityRevision); err != nil {
		return err
	}
	if err := validateJSON(response.Data, false); err != nil {
		return fmt.Errorf("response data: %w", err)
	}
	if len(response.Events) > 0 {
		if err := ValidateEventSequence(response.Events); err != nil {
			return err
		}
		for index, event := range response.Events {
			if event.AssistantAddress != response.AssistantAddress {
				return fmt.Errorf("response event %d has a different assistant_address", index)
			}
			if event.RuntimeRevision != response.RuntimeRevision {
				return fmt.Errorf("response event %d has a different runtime_revision", index)
			}
			if event.CapabilityRevision != response.CapabilityRevision {
				return fmt.Errorf("response event %d has a different capability_revision", index)
			}
			if response.PrivateSessionID != "" && event.PrivateSessionID != response.PrivateSessionID {
				return fmt.Errorf("response event %d does not belong to private_session_id", index)
			}
		}
		if response.NextSequence != 0 && response.NextSequence <= response.Events[len(response.Events)-1].Sequence {
			return errors.New("response next_sequence must be greater than the last event sequence")
		}
	}
	if response.Error != nil {
		if err := response.Error.Validate(); err != nil {
			return err
		}
	}
	if response.Type != ResponseError && response.Error != nil {
		return errors.New("response error payload is only valid for error responses")
	}
	switch response.Type {
	case ResponseConversationCreated:
		if err := validateSession(response.PrivateSessionID, response.ContinuationToken); err != nil {
			return err
		}
		if err := validateIdentifier("run_id", response.RunID); err != nil {
			return err
		}
		if response.ApprovalID != "" || response.Decision != "" || response.NextSequence != 0 || response.Health != nil || response.Descriptor != nil || len(response.Events) != 0 {
			return errors.New("created response contains fields for another response variant")
		}
	case ResponseTurnAccepted:
		if err := validateSession(response.PrivateSessionID, response.ContinuationToken); err != nil {
			return err
		}
		if err := validateIdentifier("run_id", response.RunID); err != nil {
			return err
		}
		if response.ApprovalID != "" || response.Decision != "" || response.NextSequence != 0 || response.Health != nil || response.Descriptor != nil || len(response.Events) != 0 {
			return errors.New("turn response contains fields for another response variant")
		}
	case ResponseEventsResumed:
		if err := validateSession(response.PrivateSessionID, response.ContinuationToken); err != nil {
			return err
		}
		if response.RunID != "" || response.ApprovalID != "" || response.Decision != "" || response.Health != nil || response.Descriptor != nil || len(response.Data) != 0 {
			return errors.New("resumed response contains fields for another response variant")
		}
	case ResponseApprovalResolved:
		if err := validateSession(response.PrivateSessionID, response.ContinuationToken); err != nil {
			return err
		}
		if err := validateIdentifier("run_id", response.RunID); err != nil {
			return err
		}
		if err := validateIdentifier("approval_id", response.ApprovalID); err != nil {
			return err
		}
		if response.Decision != DecisionAllow && response.Decision != DecisionDeny {
			return fmt.Errorf("approval decision %q is unsupported", response.Decision)
		}
		if response.NextSequence != 0 || response.Health != nil || response.Descriptor != nil || len(response.Events) != 0 {
			return errors.New("approval response contains fields for another response variant")
		}
	case ResponseRunCancelled:
		if err := validateSession(response.PrivateSessionID, response.ContinuationToken); err != nil {
			return err
		}
		if err := validateIdentifier("run_id", response.RunID); err != nil {
			return err
		}
		if response.ApprovalID != "" || response.Decision != "" || response.NextSequence != 0 || response.Health != nil || response.Descriptor != nil || len(response.Events) != 0 {
			return errors.New("cancel response contains fields for another response variant")
		}
	case ResponseHealth:
		if response.Health == nil {
			return errors.New("health response is missing health payload")
		}
		if err := response.Health.Validate(); err != nil {
			return err
		}
		if err := ValidateRevisions(response.Health.RuntimeRevision, response.Health.CapabilityRevision, response.RuntimeRevision, response.CapabilityRevision); err != nil {
			return err
		}
		if response.PrivateSessionID != "" || response.ContinuationToken != "" || response.RunID != "" || response.ApprovalID != "" || response.Decision != "" || response.NextSequence != 0 || response.Descriptor != nil || len(response.Events) != 0 || len(response.Data) != 0 {
			return errors.New("health response contains fields for another response variant")
		}
	case ResponseInfo:
		if response.Descriptor == nil {
			return errors.New("info response is missing runtime descriptor")
		}
		if err := response.Descriptor.Validate(); err != nil {
			return err
		}
		if err := ValidateRevisions(response.Descriptor.RuntimeRevision, response.Descriptor.CapabilityRevision, response.RuntimeRevision, response.CapabilityRevision); err != nil {
			return err
		}
		if response.PrivateSessionID != "" || response.ContinuationToken != "" || response.RunID != "" || response.ApprovalID != "" || response.Decision != "" || response.NextSequence != 0 || response.Health != nil || len(response.Events) != 0 || len(response.Data) != 0 {
			return errors.New("info response contains fields for another response variant")
		}
	case ResponseError:
		if response.Error == nil {
			return errors.New("error response is missing error payload")
		}
		if response.PrivateSessionID != "" || response.ContinuationToken != "" || response.RunID != "" || response.ApprovalID != "" || response.Decision != "" || response.NextSequence != 0 || response.Health != nil || response.Descriptor != nil || len(response.Events) != 0 || len(response.Data) != 0 {
			return errors.New("error response contains success payload fields")
		}
	default:
		return fmt.Errorf("assistant control response type %q is unsupported", response.Type)
	}
	return nil
}

func (event Event) Validate() error {
	if err := validateIdentity(EventKind, event.Kind, event.SchemaRevision, EventSchemaRevision); err != nil {
		return err
	}
	if err := validateIdentifier("assistant_address", event.AssistantAddress); err != nil {
		return err
	}
	if err := validateRevision("runtime_revision", event.RuntimeRevision); err != nil {
		return err
	}
	if err := validateRevision("capability_revision", event.CapabilityRevision); err != nil {
		return err
	}
	if event.Sequence == 0 {
		return errors.New("assistant event sequence must be positive")
	}
	if event.OccurredAt.IsZero() {
		return errors.New("assistant event occurred_at is required")
	}
	if event.OccurredAt.Location() != time.UTC {
		return errors.New("assistant event occurred_at must be UTC")
	}
	if err := validateSession(event.PrivateSessionID, event.ContinuationToken); err != nil {
		return fmt.Errorf("event session: %w", err)
	}
	if err := validateIdentifier("run_id", event.RunID); err != nil {
		return err
	}
	if err := validateOptionalIdentifier("capability_name", event.CapabilityName); err != nil {
		return err
	}
	if err := validateOptionalIdentifier("approval_id", event.ApprovalID); err != nil {
		return err
	}
	if err := validateJSON(event.Data, true); err != nil {
		return fmt.Errorf("event data: %w", err)
	}
	switch event.Type {
	case EventRunStarted:
		if event.CapabilityName != "" || event.ApprovalID != "" || event.Proposal != nil || event.ApprovalWait != nil || event.Crash != nil || event.Malformed != nil {
			return errors.New("run started event contains fields for another event variant")
		}
	case EventTextDelta, EventMessageCompleted:
		if event.CapabilityName != "" || event.ApprovalID != "" || event.Proposal != nil || event.ApprovalWait != nil || event.Crash != nil || event.Malformed != nil {
			return errors.New("message event contains fields for another event variant")
		}
	case EventCapabilityStarted, EventCapabilityCompleted:
		if event.CapabilityName == "" {
			return errors.New("capability event requires capability_name")
		}
		if event.ApprovalID != "" || event.Proposal != nil || event.ApprovalWait != nil || event.Crash != nil || event.Malformed != nil {
			return errors.New("capability event contains fields for another event variant")
		}
	case EventRunCompleted, EventRunFailed, EventRunCancelled:
		if event.CapabilityName != "" || event.ApprovalID != "" || event.Proposal != nil || event.ApprovalWait != nil || event.Crash != nil || event.Malformed != nil {
			return errors.New("run terminal event contains fields for another event variant")
		}
	case EventCapabilityProposal:
		if event.ApprovalID == "" || event.CapabilityName == "" || event.Proposal == nil {
			return errors.New("capability proposal requires approval_id, capability_name, and proposal payload")
		}
		if err := event.Proposal.Validate(); err != nil {
			return err
		}
		if event.ApprovalID != event.Proposal.ApprovalID {
			return errors.New("capability proposal approval_id does not match envelope")
		}
		if event.CapabilityName != event.Proposal.CapabilityName {
			return errors.New("capability proposal capability_name does not match envelope")
		}
		if event.ApprovalWait != nil || event.Crash != nil || event.Malformed != nil {
			return errors.New("capability proposal contains fields for another event variant")
		}
	case EventApprovalWait:
		if event.ApprovalID == "" || event.ApprovalWait == nil {
			return errors.New("approval wait requires approval_id and approval wait payload")
		}
		if err := event.ApprovalWait.Validate(); err != nil {
			return err
		}
		if event.ApprovalID != event.ApprovalWait.ApprovalID {
			return errors.New("approval wait approval_id does not match envelope")
		}
		if event.Proposal != nil || event.Crash != nil || event.Malformed != nil {
			return errors.New("approval wait contains fields for another event variant")
		}
	case EventRuntimeCrashed, EventRuntimeRestarting:
		if event.Crash == nil {
			return errors.New("runtime crash event is missing crash payload")
		}
		if err := event.Crash.Validate(); err != nil {
			return err
		}
		if event.CapabilityName != "" || event.ApprovalID != "" || event.Proposal != nil || event.ApprovalWait != nil || event.Malformed != nil {
			return errors.New("runtime crash event contains fields for another event variant")
		}
	case EventProtocolMalformed:
		if event.Malformed == nil {
			return errors.New("malformed event is missing malformed payload")
		}
		if err := event.Malformed.Validate(); err != nil {
			return err
		}
		if event.CapabilityName != "" || event.ApprovalID != "" || event.Proposal != nil || event.ApprovalWait != nil || event.Crash != nil {
			return errors.New("malformed event contains fields for another event variant")
		}
	default:
		return fmt.Errorf("assistant control event type %q is unsupported", event.Type)
	}
	return nil
}

// ValidateEventSequence validates strict monotonic sequence numbers in a
// resumed event batch. An empty batch is valid.
func ValidateEventSequence(events []Event) error {
	var previous uint64
	var assistant, runtimeRevision, capabilityRevision, session string
	for index, event := range events {
		if err := event.Validate(); err != nil {
			return fmt.Errorf("event %d: %w", index, err)
		}
		if index > 0 && event.Sequence <= previous {
			return fmt.Errorf("event sequence %d is not greater than %d", event.Sequence, previous)
		}
		if index == 0 {
			assistant = event.AssistantAddress
			runtimeRevision = event.RuntimeRevision
			capabilityRevision = event.CapabilityRevision
			session = event.PrivateSessionID
		} else {
			if event.AssistantAddress != assistant {
				return errors.New("event sequence batch mixes assistant addresses")
			}
			if event.RuntimeRevision != runtimeRevision {
				return errors.New("event sequence batch mixes runtime revisions")
			}
			if event.CapabilityRevision != capabilityRevision {
				return errors.New("event sequence batch mixes capability revisions")
			}
			if event.PrivateSessionID != session {
				return errors.New("event sequence batch mixes sessions")
			}
		}
		previous = event.Sequence
	}
	return nil
}

func (descriptor RuntimeDescriptor) Validate() error {
	if err := validateIdentity(RuntimeDescriptorKind, descriptor.Kind, descriptor.SchemaRevision, DescriptorSchemaRevision); err != nil {
		return err
	}
	if err := validateIdentifier("assistant_address", descriptor.AssistantAddress); err != nil {
		return err
	}
	if err := validateRevision("runtime_revision", descriptor.RuntimeRevision); err != nil {
		return err
	}
	if err := validateRevision("capability_revision", descriptor.CapabilityRevision); err != nil {
		return err
	}
	if descriptor.ControlProtocol != ControlProtocol {
		return fmt.Errorf("runtime descriptor control_protocol %q is unsupported", descriptor.ControlProtocol)
	}
	if descriptor.MCPProtocol != MCPProtocolVersion {
		return fmt.Errorf("runtime descriptor mcp_protocol %q is unsupported", descriptor.MCPProtocol)
	}
	return nil
}

func (health Health) Validate() error {
	if err := validateRevision("health.runtime_revision", health.RuntimeRevision); err != nil {
		return err
	}
	if err := validateRevision("health.capability_revision", health.CapabilityRevision); err != nil {
		return err
	}
	return nil
}

func (proposal CapabilityProposal) Validate() error {
	if err := validateIdentifier("approval_id", proposal.ApprovalID); err != nil {
		return err
	}
	if err := validateIdentifier("capability_name", proposal.CapabilityName); err != nil {
		return err
	}
	if err := validateJSON(proposal.Input, true); err != nil {
		return fmt.Errorf("capability proposal input: %w", err)
	}
	return nil
}

func (wait ApprovalWait) Validate() error {
	return validateIdentifier("approval_id", wait.ApprovalID)
}

func (signal CrashSignal) Validate() error {
	if err := validateIdentifier("crash.code", signal.Code); err != nil {
		return err
	}
	return validateText("crash.message", signal.Message)
}

func (signal MalformedSignal) Validate() error {
	if err := validateIdentifier("malformed.code", signal.Code); err != nil {
		return err
	}
	return validateText("malformed.message", signal.Message)
}

func (problem Error) Validate() error {
	if err := validateIdentifier("error.code", problem.Code); err != nil {
		return err
	}
	return validateText("error.message", problem.Message)
}

// ValidateRevisions compares a message's revisions with the revisions the Go
// side negotiated during helper startup. Empty expected values skip that
// comparison, which is useful for tests and descriptor discovery.
func ValidateRevisions(actualRuntime, actualCapability, expectedRuntime, expectedCapability string) error {
	if expectedRuntime != "" && actualRuntime != expectedRuntime {
		return RevisionMismatchError{Field: "runtime_revision", Expected: expectedRuntime, Actual: actualRuntime}
	}
	if expectedCapability != "" && actualCapability != expectedCapability {
		return RevisionMismatchError{Field: "capability_revision", Expected: expectedCapability, Actual: actualCapability}
	}
	return nil
}

func (request Request) ValidateRevisions(expectedRuntime, expectedCapability string) error {
	return ValidateRevisions(request.RuntimeRevision, request.CapabilityRevision, expectedRuntime, expectedCapability)
}

func (response Response) ValidateRevisions(expectedRuntime, expectedCapability string) error {
	return ValidateRevisions(response.RuntimeRevision, response.CapabilityRevision, expectedRuntime, expectedCapability)
}

func (event Event) ValidateRevisions(expectedRuntime, expectedCapability string) error {
	return ValidateRevisions(event.RuntimeRevision, event.CapabilityRevision, expectedRuntime, expectedCapability)
}

func (descriptor RuntimeDescriptor) ValidateRevisions(expectedRuntime, expectedCapability string) error {
	return ValidateRevisions(descriptor.RuntimeRevision, descriptor.CapabilityRevision, expectedRuntime, expectedCapability)
}

type RevisionMismatchError struct {
	Field    string
	Expected string
	Actual   string
}

func (err RevisionMismatchError) Error() string {
	return fmt.Sprintf("assistant control %s mismatch: got %q, want %q", err.Field, err.Actual, err.Expected)
}

func validateIdentity(expectedKind, kind, schema, expectedSchema string) error {
	if kind != expectedKind {
		return fmt.Errorf("assistant control kind %q is unsupported", kind)
	}
	if schema != expectedSchema {
		return fmt.Errorf("assistant control schema revision %q is unsupported", schema)
	}
	return nil
}

func validateSession(session, continuation string) error {
	if err := validateIdentifier("private_session_id", session); err != nil {
		return err
	}
	return validateIdentifier("continuation_token", continuation)
}

func validateMessageOrData(message string, data json.RawMessage) error {
	hasMessage := message != ""
	hasData := len(data) != 0
	if hasMessage == hasData {
		return errors.New("exactly one of message or data is required")
	}
	if hasMessage {
		return validateText("message", message)
	}
	return validateJSON(data, true)
}

func validateOptionalIdentifier(name, value string) error {
	if value == "" {
		return nil
	}
	return validateIdentifier(name, value)
}

func validateRevision(name, value string) error {
	return validateIdentifier(name, value)
}

func validateIdentifier(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maxControlIDBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%s is invalid", name)
	}
	for _, runeValue := range value {
		if unicode.IsSpace(runeValue) || runeValue < 0x20 || runeValue == 0x7f || runeValue == '\u2028' || runeValue == '\u2029' {
			return fmt.Errorf("%s contains forbidden control characters", name)
		}
	}
	return nil
}

func validateText(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maxControlTextBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func validateJSON(raw json.RawMessage, required bool) error {
	if len(raw) == 0 {
		if required {
			return errors.New("JSON value is required")
		}
		return nil
	}
	if len(raw) > maxControlDataBytes {
		return errors.New("JSON value is too large")
	}
	if !json.Valid(raw) {
		return errors.New("invalid JSON value")
	}
	return nil
}
