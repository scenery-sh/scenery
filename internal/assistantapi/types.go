// Package assistantapi owns Scenery's provider-neutral public assistant HTTP
// contracts. Provider adapters and private helper values must not enter these
// types.
package assistantapi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"scenery.sh/internal/contract"
)

const (
	NDJSONContentType    = "application/x-ndjson"
	MaxMessageBytes      = 64 << 10
	MaxErrorMessageBytes = 8 << 10
	MaxEventDataBytes    = 1 << 20
	// Opaque IDs are encrypted lowercase-hex handles. Keep them bounded while
	// leaving room for the current token envelope (which is typically <1 KiB).
	MaxIdentifierHexBytes = 8 << 10
)

const (
	EventRunStarted          = "assistant.run.started"
	EventMessageDelta        = "assistant.message.delta"
	EventMessageCompleted    = "assistant.message.completed"
	EventCapabilityProposed  = "assistant.capability.proposed"
	EventApprovalRequired    = "assistant.approval.required"
	EventCapabilityStarted   = "assistant.capability.started"
	EventCapabilityCompleted = "assistant.capability.completed"
	EventRunCompleted        = "assistant.run.completed"
	EventRunCancelled        = "assistant.run.cancelled"
	EventRunFailed           = "assistant.run.failed"
	EventRuntimeRestarting   = "assistant.runtime.restarting"
)

const (
	ApprovalApprove = "approve"
	ApprovalDeny    = "deny"

	ErrorInvalidRequest = "invalid_request"
	ErrorUnauthorized   = "unauthorized"
	ErrorForbidden      = "forbidden"
	ErrorNotFound       = "not_found"
	ErrorConflict       = "conflict"
	ErrorApproval       = "approval_required"
	ErrorCancelled      = "cancelled"
	ErrorUnavailable    = "unavailable"
	ErrorRateLimited    = "rate_limited"
	ErrorInternal       = "internal"
)

var (
	conversationIDPattern = regexp.MustCompile(`^conv1_[0-9a-f]+$`)
	runIDPattern          = regexp.MustCompile(`^run_[0-9a-f]+$`)
	approvalIDPattern     = regexp.MustCompile(`^appr1_[0-9a-f]+$`)
	assistantPattern      = regexp.MustCompile(`^[a-z][a-z0-9_./-]{0,127}$`)
)

// Cursor is the last event sequence observed by a client. Zero is the
// beginning of a stream; event sequences themselves begin at one.
type Cursor uint64

func (c Cursor) String() string { return strconv.FormatUint(uint64(c), 10) }

func ParseCursor(value string) (Cursor, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return 0, errors.New("event cursor must be a non-negative decimal integer")
	}
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errors.New("event cursor must be a non-negative decimal integer")
	}
	return Cursor(number), nil
}

func ValidateCursorAfter(previous Cursor, next Cursor) error {
	if next <= previous {
		return fmt.Errorf("event cursor is not monotonic: %d followed by %d", previous, next)
	}
	return nil
}

func ValidateEventSequences(events []Event) error {
	var previous Cursor
	var assistant string
	var conversationID string
	for index, event := range events {
		if err := event.Validate(); err != nil {
			return fmt.Errorf("event %d: %w", index, err)
		}
		if index == 0 {
			assistant = event.Assistant
			conversationID = event.ConversationID
		} else if event.Assistant != assistant || event.ConversationID != conversationID {
			return fmt.Errorf("event %d changes assistant or conversation identity", index)
		}
		if err := ValidateCursorAfter(previous, Cursor(event.Sequence)); err != nil {
			return err
		}
		previous = Cursor(event.Sequence)
	}
	return nil
}

// EventsAfter returns a stable suffix strictly after after. The input must be
// ordered and gap-free only in the monotonic sense; sequence gaps are valid.
func EventsAfter(events []Event, after Cursor) ([]Event, error) {
	if err := ValidateEventSequences(events); err != nil {
		return nil, err
	}
	result := make([]Event, 0, len(events))
	for _, event := range events {
		if Cursor(event.Sequence) > after {
			result = append(result, event)
		}
	}
	return result, nil
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (m Message) ValidateUser() error {
	if m.Role != "user" {
		return fmt.Errorf("message role must be user")
	}
	if strings.TrimSpace(m.Content) == "" {
		return errors.New("message content is required")
	}
	if !utf8Valid(m.Content) {
		return errors.New("message content must be valid UTF-8")
	}
	if len(m.Content) > MaxMessageBytes {
		return fmt.Errorf("message content exceeds %d bytes", MaxMessageBytes)
	}
	return nil
}

type CreateConversationRequest struct {
	Message Message `json:"message"`
}

type CreateConversationResponse struct {
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	EventsURL      string `json:"events_url"`
}

func NewCreateConversationResponse(sealedHandle []byte, eventsURL string, random io.Reader) (CreateConversationResponse, error) {
	conversationID, err := NewConversationID(sealedHandle)
	if err != nil {
		return CreateConversationResponse{}, err
	}
	runID, err := NewRunID(random)
	if err != nil {
		return CreateConversationResponse{}, err
	}
	response := CreateConversationResponse{ConversationID: conversationID, RunID: runID, EventsURL: eventsURL}
	if err := response.Validate(); err != nil {
		return CreateConversationResponse{}, err
	}
	return response, nil
}

func ConversationEventsURL(assistant, conversationID string) (string, error) {
	if !assistantPattern.MatchString(assistant) {
		return "", errors.New("assistant name is not a canonical address")
	}
	if err := ValidateConversationID(conversationID); err != nil {
		return "", err
	}
	value := "/assistants/" + assistant + "/v1/conversations/" + conversationID + "/events"
	if err := ValidateEventsURL(value); err != nil {
		return "", err
	}
	return value, nil
}

// ValidateEventsURL accepts only the canonical absolute event-stream path.
// Query parameters belong to the request's after cursor, not to the URL
// carried in a create response.
func ValidateEventsURL(value string) error {
	if value == "" {
		return errors.New("events_url is required")
	}
	if !utf8Valid(value) {
		return errors.New("events_url must be valid UTF-8")
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return errors.New("events_url must be an absolute path")
	}
	if strings.Contains(value, "//") {
		return errors.New("events_url must not contain duplicate slashes")
	}
	if strings.ContainsAny(value, "?#\\%") {
		return errors.New("events_url must not contain query, fragment, backslash, or escapes")
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("events_url contains a control character")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.Path != value || parsed.RawPath != "" {
		return errors.New("events_url must be a canonical absolute path")
	}
	if path.Clean(value) != value {
		return errors.New("events_url must not contain dot segments")
	}
	marker := "/v1/conversations/"
	markerIndex := strings.LastIndex(value, marker)
	if markerIndex <= 1 {
		return errors.New("events_url must use a canonical assistant events route")
	}
	if err := validateSurfacePath(value[:markerIndex]); err != nil {
		return fmt.Errorf("events_url surface: %w", err)
	}
	rest := value[markerIndex+len(marker):]
	if !strings.HasSuffix(rest, "/events") {
		return errors.New("events_url must end in /events")
	}
	conversationID := strings.TrimSuffix(rest, "/events")
	if strings.Contains(conversationID, "/") {
		return errors.New("events_url conversation ID must be one path segment")
	}
	return ValidateConversationID(conversationID)
}

// ValidateEventsURLForSurface validates an event URL against a configured
// assistant surface path such as /assistants/support. The optional
// conversation ID additionally binds the URL to one exact conversation.
func ValidateEventsURLForSurface(eventsURL, surfacePath string, conversationID ...string) error {
	if err := ValidateEventsURL(eventsURL); err != nil {
		return err
	}
	if err := validateSurfacePath(surfacePath); err != nil {
		return err
	}
	prefix := strings.TrimSuffix(surfacePath, "/") + "/v1/conversations/"
	if !strings.HasPrefix(eventsURL, prefix) {
		return errors.New("events_url does not belong to the configured assistant surface")
	}
	if len(conversationID) > 1 {
		return errors.New("at most one conversation ID may be supplied")
	}
	if len(conversationID) == 1 {
		if err := ValidateConversationID(conversationID[0]); err != nil {
			return err
		}
		expected := prefix + conversationID[0] + "/events"
		if eventsURL != expected {
			return errors.New("events_url does not match the conversation ID")
		}
	}
	return nil
}

func validateSurfacePath(value string) error {
	if value == "" || value == "/" || strings.HasSuffix(value, "/") {
		return errors.New("assistant surface path must be a non-root path without a trailing slash")
	}
	if strings.Contains(value, "/v1/conversations/") {
		return errors.New("assistant surface path must not contain the events route")
	}
	return validateAbsolutePath(value)
}

func validateAbsolutePath(value string) error {
	if !utf8Valid(value) || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "//") || strings.ContainsAny(value, "?#\\%") {
		return errors.New("path must be a canonical absolute path")
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || path.Clean(value) != value {
		return errors.New("path contains forbidden characters or segments")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.Path != value || parsed.RawPath != "" {
		return errors.New("path must be a canonical absolute path")
	}
	return nil
}

type SendTurnRequest struct {
	Message Message `json:"message"`
}

type SendTurnResponse struct {
	RunID string `json:"run_id"`
}

type EventsRequest struct {
	After Cursor
}

func ParseEventsRequest(after string) (EventsRequest, error) {
	cursor, err := ParseCursor(after)
	if err != nil {
		return EventsRequest{}, err
	}
	return EventsRequest{After: cursor}, nil
}

type ResolveApprovalRequest struct {
	Decision string `json:"decision"`
}

type ResolveApprovalResponse struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
}

type CancelRunResponse struct {
	RunID string `json:"run_id"`
	State string `json:"state"`
}

type EventsResponse struct {
	Events []Event `json:"events"`
}

type Event struct {
	Type           string          `json:"type"`
	Assistant      string          `json:"assistant"`
	ConversationID string          `json:"conversation_id"`
	RunID          string          `json:"run_id"`
	Sequence       uint64          `json:"sequence"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Data           json.RawMessage `json:"data"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewEvent(eventType, assistant, conversationID, runID string, sequence uint64, occurredAt time.Time, data json.RawMessage) (Event, error) {
	event := Event{Type: eventType, Assistant: assistant, ConversationID: conversationID, RunID: runID, Sequence: sequence, OccurredAt: occurredAt, Data: append(json.RawMessage(nil), data...)}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func NewConversationID(sealedHandle []byte) (string, error) {
	if len(sealedHandle) == 0 {
		return "", errors.New("sealed conversation handle is required")
	}
	if len(sealedHandle)*2 > MaxIdentifierHexBytes {
		return "", fmt.Errorf("sealed conversation handle exceeds %d hex bytes", MaxIdentifierHexBytes)
	}
	return "conv1_" + hexEncode(sealedHandle), nil
}

func NewRunID(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	bytesValue := make([]byte, 16)
	if _, err := io.ReadFull(random, bytesValue); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run_" + hexEncode(bytesValue), nil
}

func (request CreateConversationRequest) Validate() error {
	return request.Message.ValidateUser()
}

func (request SendTurnRequest) Validate() error {
	return request.Message.ValidateUser()
}

func (request ResolveApprovalRequest) Validate() error {
	if request.Decision != ApprovalApprove && request.Decision != ApprovalDeny {
		return fmt.Errorf("approval decision must be %q or %q", ApprovalApprove, ApprovalDeny)
	}
	return nil
}

func (response CreateConversationResponse) Validate() error {
	if err := ValidateConversationID(response.ConversationID); err != nil {
		return err
	}
	if err := ValidateRunID(response.RunID); err != nil {
		return err
	}
	if err := ValidateEventsURL(response.EventsURL); err != nil {
		return err
	}
	return nil
}

func (response SendTurnResponse) Validate() error {
	return ValidateRunID(response.RunID)
}

func (response ResolveApprovalResponse) Validate() error {
	if err := ValidateApprovalID(response.ApprovalID); err != nil {
		return err
	}
	return (ResolveApprovalRequest{Decision: response.Decision}).Validate()
}

func (response CancelRunResponse) Validate() error {
	if err := ValidateRunID(response.RunID); err != nil {
		return err
	}
	if response.State != "cancelled" {
		return errors.New("cancel state must be cancelled")
	}
	return nil
}

func (response EventsResponse) Validate() error {
	return ValidateEventSequences(response.Events)
}

func DecodeCreateConversationRequest(encoded []byte) (CreateConversationRequest, error) {
	var request CreateConversationRequest
	if err := decodeExact(encoded, &request); err != nil {
		return CreateConversationRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return CreateConversationRequest{}, err
	}
	return request, nil
}

func DecodeCreateConversationResponse(encoded []byte) (CreateConversationResponse, error) {
	var response CreateConversationResponse
	if err := decodeExact(encoded, &response); err != nil {
		return CreateConversationResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return CreateConversationResponse{}, err
	}
	return response, nil
}

func DecodeSendTurnRequest(encoded []byte) (SendTurnRequest, error) {
	var request SendTurnRequest
	if err := decodeExact(encoded, &request); err != nil {
		return SendTurnRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return SendTurnRequest{}, err
	}
	return request, nil
}

func DecodeSendTurnResponse(encoded []byte) (SendTurnResponse, error) {
	var response SendTurnResponse
	if err := decodeExact(encoded, &response); err != nil {
		return SendTurnResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return SendTurnResponse{}, err
	}
	return response, nil
}

func DecodeResolveApprovalRequest(encoded []byte) (ResolveApprovalRequest, error) {
	var request ResolveApprovalRequest
	if err := decodeExact(encoded, &request); err != nil {
		return ResolveApprovalRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return ResolveApprovalRequest{}, err
	}
	return request, nil
}

func DecodeResolveApprovalResponse(encoded []byte) (ResolveApprovalResponse, error) {
	var response ResolveApprovalResponse
	if err := decodeExact(encoded, &response); err != nil {
		return ResolveApprovalResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return ResolveApprovalResponse{}, err
	}
	return response, nil
}

func DecodeCancelRunResponse(encoded []byte) (CancelRunResponse, error) {
	var response CancelRunResponse
	if err := decodeExact(encoded, &response); err != nil {
		return CancelRunResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return CancelRunResponse{}, err
	}
	return response, nil
}

func DecodeEventsResponse(encoded []byte) (EventsResponse, error) {
	var response EventsResponse
	if err := decodeExact(encoded, &response); err != nil {
		return EventsResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return EventsResponse{}, err
	}
	return response, nil
}

func DecodeEvent(encoded []byte) (Event, error) {
	var event Event
	if err := decodeExact(encoded, &event); err != nil {
		return Event{}, err
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func DecodeError(encoded []byte) (Error, error) {
	var value Error
	if err := decodeExact(encoded, &value); err != nil {
		return Error{}, err
	}
	if err := value.Validate(); err != nil {
		return Error{}, err
	}
	return value, nil
}

func MarshalCreateConversationRequest(request CreateConversationRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

func MarshalCreateConversationResponse(response CreateConversationResponse) ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func MarshalSendTurnRequest(request SendTurnRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

func MarshalSendTurnResponse(response SendTurnResponse) ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func MarshalResolveApprovalRequest(request ResolveApprovalRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

func MarshalResolveApprovalResponse(response ResolveApprovalResponse) ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func MarshalCancelRunResponse(response CancelRunResponse) ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func MarshalEventsResponse(response EventsResponse) ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func MarshalEvent(event Event) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(event)
}

func MarshalError(value Error) ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func EncodeNDJSON(events []Event) ([]byte, error) {
	if err := ValidateEventSequences(events); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	for _, event := range events {
		encoded, err := MarshalEvent(event)
		if err != nil {
			return nil, err
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return output.Bytes(), nil
}

func DecodeNDJSON(encoded []byte) ([]Event, error) {
	scanner := bufio.NewScanner(bytes.NewReader(encoded))
	scanner.Buffer(make([]byte, 4096), 16<<20)
	events := make([]Event, 0)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, errors.New("NDJSON contains an empty line")
		}
		event, err := DecodeEvent(line)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := ValidateEventSequences(events); err != nil {
		return nil, err
	}
	return events, nil
}

func (event Event) Validate() error {
	if !validEventType(event.Type) {
		return fmt.Errorf("event type %q is unsupported", event.Type)
	}
	if !assistantPattern.MatchString(event.Assistant) {
		return errors.New("event assistant is not a canonical address")
	}
	if err := ValidateConversationID(event.ConversationID); err != nil {
		return err
	}
	if err := ValidateRunID(event.RunID); err != nil {
		return err
	}
	if event.Sequence == 0 {
		return errors.New("event sequence must be positive")
	}
	if event.OccurredAt.IsZero() || event.OccurredAt.Location() == nil {
		return errors.New("event occurred_at is required")
	}
	if _, offset := event.OccurredAt.Zone(); offset != 0 {
		return errors.New("event occurred_at must use UTC")
	}
	if len(event.Data) == 0 || len(event.Data) > MaxEventDataBytes || !json.Valid(event.Data) || !isJSONObject(event.Data) {
		return errors.New("event data must be a JSON object")
	}
	return validateEventData(event.Type, event.Data)
}

func validateEventData(eventType string, encoded []byte) error {
	fields, err := contract.DecodeJSONObject(encoded)
	if err != nil {
		return errors.New("event data must be a JSON object")
	}
	var expected map[string]bool
	switch eventType {
	case EventRunStarted, EventRunCompleted, EventRunCancelled, EventRuntimeRestarting:
		expected = map[string]bool{"state": true}
	case EventMessageDelta, EventMessageCompleted:
		expected = map[string]bool{"text": true}
	case EventCapabilityProposed:
		expected = map[string]bool{"capability": true, "approval_id": true, "input": true}
	case EventApprovalRequired:
		expected = map[string]bool{"capability": true, "approval_id": true}
	case EventCapabilityStarted:
		expected = map[string]bool{"capability": true}
	case EventCapabilityCompleted:
		expected = map[string]bool{"capability": true, "state": true}
	case EventRunFailed:
		expected = map[string]bool{"code": true, "message": true}
	default:
		return fmt.Errorf("event type %q is unsupported", eventType)
	}
	if len(fields) != len(expected) {
		return errors.New("event data has missing or unknown fields")
	}
	for key := range fields {
		if !expected[key] {
			return fmt.Errorf("event data field %q is not public", key)
		}
	}
	for key := range expected {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("event data field %q is required", key)
		}
	}

	switch eventType {
	case EventRunStarted:
		return requireLiteralString(fields["state"], "state", "started")
	case EventRunCompleted:
		return requireLiteralString(fields["state"], "state", "completed")
	case EventRunCancelled:
		return requireLiteralString(fields["state"], "state", "cancelled")
	case EventRuntimeRestarting:
		return requireLiteralString(fields["state"], "state", "restarting")
	case EventMessageDelta, EventMessageCompleted:
		_, err := boundedEventString(fields["text"], "text", MaxMessageBytes, false)
		return err
	case EventCapabilityProposed:
		if _, err := boundedEventString(fields["capability"], "capability", MaxMessageBytes, true); err != nil {
			return err
		}
		var approvalID string
		if approvalID, err = boundedEventString(fields["approval_id"], "approval_id", MaxIdentifierHexBytes+len("appr1_"), true); err != nil {
			return err
		}
		if err := ValidateApprovalID(approvalID); err != nil {
			return err
		}
		return validateNestedJSON(fields["input"])
	case EventApprovalRequired:
		if _, err := boundedEventString(fields["capability"], "capability", MaxMessageBytes, true); err != nil {
			return err
		}
		approvalID, err := boundedEventString(fields["approval_id"], "approval_id", MaxIdentifierHexBytes+len("appr1_"), true)
		if err != nil {
			return err
		}
		return ValidateApprovalID(approvalID)
	case EventCapabilityStarted:
		_, err := boundedEventString(fields["capability"], "capability", MaxMessageBytes, true)
		return err
	case EventCapabilityCompleted:
		if _, err := boundedEventString(fields["capability"], "capability", MaxMessageBytes, true); err != nil {
			return err
		}
		return requireLiteralString(fields["state"], "state", "completed")
	case EventRunFailed:
		if _, err := boundedEventString(fields["code"], "code", 256, true); err != nil {
			return err
		}
		_, err := boundedEventString(fields["message"], "message", MaxErrorMessageBytes, true)
		return err
	}
	return nil
}

func requireLiteralString(raw json.RawMessage, field, expected string) error {
	value, err := boundedEventString(raw, field, len(expected), true)
	if err != nil {
		return err
	}
	if value != expected {
		return fmt.Errorf("event data %s must be %q", field, expected)
	}
	return nil
}

func boundedEventString(raw json.RawMessage, field string, maxBytes int, nonEmpty bool) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	var value string
	if len(trimmed) == 0 || trimmed[0] != '"' || json.Unmarshal(trimmed, &value) != nil || !utf8Valid(value) || len(value) > maxBytes || (nonEmpty && value == "") {
		return "", fmt.Errorf("event data %s must be a bounded string", field)
	}
	return value, nil
}

func validateNestedJSON(raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return errors.New("event data input must be valid JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("event data input must contain one JSON value")
	}
	return validateNestedJSONValue(value, 0)
}

func validateNestedJSONValue(value any, depth int) error {
	if depth > 64 {
		return errors.New("event data input is too deeply nested")
	}
	switch typed := value.(type) {
	case nil, bool, json.Number:
		return nil
	case string:
		if !utf8Valid(typed) || len(typed) > MaxMessageBytes {
			return errors.New("event data input string exceeds the size limit")
		}
	case []any:
		if len(typed) > 4096 {
			return errors.New("event data input array exceeds the size limit")
		}
		for _, item := range typed {
			if err := validateNestedJSONValue(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if len(typed) > 4096 {
			return errors.New("event data input object exceeds the size limit")
		}
		for key, item := range typed {
			if !utf8Valid(key) || len(key) > MaxMessageBytes {
				return errors.New("event data input key exceeds the size limit")
			}
			if err := validateNestedJSONValue(item, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("event data input contains an unsupported JSON value")
	}
	return nil
}

func (event Event) MarshalJSON() ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	type plain Event
	value := plain(event)
	value.OccurredAt = event.OccurredAt.UTC()
	return json.Marshal(value)
}

func (event *Event) UnmarshalJSON(encoded []byte) error {
	var value struct {
		Type           string          `json:"type"`
		Assistant      string          `json:"assistant"`
		ConversationID string          `json:"conversation_id"`
		RunID          string          `json:"run_id"`
		Sequence       uint64          `json:"sequence"`
		OccurredAt     time.Time       `json:"occurred_at"`
		Data           json.RawMessage `json:"data"`
	}
	if err := decodeExact(encoded, &value); err != nil {
		return err
	}
	*event = Event(value)
	return event.Validate()
}

func ValidateConversationID(value string) error {
	const prefix = "conv1_"
	if len(value) > len(prefix)+MaxIdentifierHexBytes || !conversationIDPattern.MatchString(value) || len(value) <= len(prefix) || (len(value)-len(prefix))%2 != 0 {
		return errors.New("conversation_id must use conv1_ followed by lowercase hex")
	}
	return nil
}

func ValidateRunID(value string) error {
	const prefix = "run_"
	if len(value) > len(prefix)+MaxIdentifierHexBytes || !runIDPattern.MatchString(value) || len(value) <= len(prefix) || (len(value)-len(prefix))%2 != 0 {
		return errors.New("run_id must use run_ followed by lowercase hex")
	}
	return nil
}

func ValidateApprovalID(value string) error {
	const prefix = "appr1_"
	if len(value) > len(prefix)+MaxIdentifierHexBytes || !approvalIDPattern.MatchString(value) || len(value) <= len(prefix) || (len(value)-len(prefix))%2 != 0 {
		return errors.New("approval_id must use appr1_ followed by lowercase hex")
	}
	return nil
}

func (err Error) Validate() error {
	if !validErrorCode(err.Code) {
		return fmt.Errorf("error code %q is unsupported", err.Code)
	}
	if strings.TrimSpace(err.Message) == "" || !utf8Valid(err.Message) || len(err.Message) > MaxErrorMessageBytes {
		return errors.New("error message is required and must be valid UTF-8")
	}
	return nil
}

func NewError(code, message string) Error {
	message = RedactString(message)
	if strings.TrimSpace(message) == "" {
		message = "assistant request failed"
	}
	return Error{Code: code, Message: truncateUTF8(message, MaxErrorMessageBytes)}
}

func NormalizeError(code string, cause error) Error {
	message := "assistant request failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = cause.Error()
	}
	return NewError(code, message)
}

func decodeExact(encoded []byte, target any) error {
	if _, err := contract.DecodeJSONObject(encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return errors.New("JSON must contain exactly one value")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func isJSONObject(raw []byte) bool {
	_, err := contract.DecodeJSONObject(raw)
	return err == nil
}

func validEventType(value string) bool {
	switch value {
	case EventRunStarted, EventMessageDelta, EventMessageCompleted, EventCapabilityProposed, EventApprovalRequired, EventCapabilityStarted, EventCapabilityCompleted, EventRunCompleted, EventRunCancelled, EventRunFailed, EventRuntimeRestarting:
		return true
	default:
		return false
	}
}

func validErrorCode(value string) bool {
	switch value {
	case ErrorInvalidRequest, ErrorUnauthorized, ErrorForbidden, ErrorNotFound, ErrorConflict, ErrorApproval, ErrorCancelled, ErrorUnavailable, ErrorRateLimited, ErrorInternal:
		return true
	default:
		return false
	}
}

func utf8Valid(value string) bool {
	return utf8.ValidString(value)
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func hexEncode(value []byte) string {
	const alphabet = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, byteValue := range value {
		encoded[index*2] = alphabet[byteValue>>4]
		encoded[index*2+1] = alphabet[byteValue&15]
	}
	return string(encoded)
}
