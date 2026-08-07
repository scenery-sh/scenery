package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/assistantapi"
	"scenery.sh/internal/assistantcontrol"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/assistanttoken"
)

const assistantTestPath = "/assistants/support/v1/conversations"

type assistantTestClock struct {
	value time.Time
}

func (clock *assistantTestClock) Now() time.Time {
	return clock.value
}

// counterReader is deterministic, but unlike a repeated byte reader still
// gives each generated Scenery run a distinct ID.
type counterReader struct {
	next byte
}

func (reader *counterReader) Read(dst []byte) (int, error) {
	for index := range dst {
		dst[index] = reader.next
		reader.next++
	}
	return len(dst), nil
}

type assistantStreamingWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	status int
	wrote  chan struct{}
	once   sync.Once
}

func newAssistantStreamingWriter() *assistantStreamingWriter {
	return &assistantStreamingWriter{header: make(http.Header), wrote: make(chan struct{})}
}

func (writer *assistantStreamingWriter) Header() http.Header { return writer.header }

func (writer *assistantStreamingWriter) WriteHeader(status int) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *assistantStreamingWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	written, err := writer.body.Write(data)
	writer.mu.Unlock()
	writer.once.Do(func() { close(writer.wrote) })
	return written, err
}

func (*assistantStreamingWriter) Flush() {}

func (writer *assistantStreamingWriter) Bytes() []byte {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]byte(nil), writer.body.Bytes()...)
}

type assistantTestServer struct {
	server   *http.Server
	helper   *assistantruntime.FakeHelper
	clock    *assistantTestClock
	manager  assistanttoken.Manager
	signer   assistanttoken.InitiatorSigner
	basePath string
}

func newAssistantTestServer(t *testing.T, config assistantruntime.FakeConfig) assistantTestServer {
	return newAssistantTestServerWithRegistration(t, config, nil)
}

func newAssistantTestServerWithRegistration(t *testing.T, config assistantruntime.FakeConfig, configure func(*AssistantRegistration)) assistantTestServer {
	t.Helper()
	restore := replaceGlobalRegistryForTest()
	t.Cleanup(restore)

	if config.AssistantAddress == "" {
		config.AssistantAddress = "support"
	}
	clock := &assistantTestClock{value: time.Unix(1_754_280_000, 0).UTC()}
	signerClock := &assistantTestClock{value: clock.value}
	if config.Now == nil {
		config.Now = clock.Now
	}
	helper := assistantruntime.NewFakeHelper(config)
	if err := helper.Start(context.Background()); err != nil {
		t.Fatalf("start fake assistant helper: %v", err)
	}
	t.Cleanup(func() { _ = helper.Stop(context.Background()) })

	key := bytes.Repeat([]byte{0x41}, 32)
	keyring := assistanttoken.NewStaticKeyring("test", key, nil)
	manager := assistanttoken.Manager{Keys: keyring, Now: clock.Now, TTL: time.Hour}
	signer := assistanttoken.InitiatorSigner{Key: append([]byte(nil), key...), KeyID: "test", Now: signerClock.Now, TTL: time.Hour}
	registration := AssistantRegistration{
		Address:            "app/assistant/support",
		Name:               "support",
		Path:               "/assistants/support",
		Access:             Public,
		AssistantAddress:   "support",
		RuntimeRevision:    "runtime-1",
		CapabilityRevision: "capability-1",
		Client:             helper,
		TokenManager:       manager,
		InitiatorSigner:    signer,
		RunIDReader:        &counterReader{next: 1},
		Random:             &counterReader{next: 101},
	}
	if configure != nil {
		configure(&registration)
	}
	if err := RegisterAssistantChecked(registration); err != nil {
		t.Fatalf("register assistant: %v", err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	return assistantTestServer{server: server, helper: helper, clock: clock, manager: manager, signer: signer, basePath: assistantTestPath}
}

func assistantRequest(t *testing.T, server *http.Server, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	return assistantRequestWithEncoding(t, server, method, path, body, cookie, "")
}

func assistantRequestWithEncoding(t *testing.T, server *http.Server, method, path string, body any, cookie *http.Cookie, acceptEncoding string) *httptest.ResponseRecorder {
	return assistantRequestWithHeaders(t, server, method, path, body, cookie, acceptEncoding, nil)
}

func assistantRequestWithHeaders(t *testing.T, server *http.Server, method, path string, body any, cookie *http.Cookie, acceptEncoding string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, req)
	return recorder
}

func assistantEventsRequest(t *testing.T, server *http.Server, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return assistantRequestWithEncoding(t, server, http.MethodGet, path, nil, cookie, "gzip")
}

func decodeJSONMap(t *testing.T, recorder *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode JSON response %q: %v", recorder.Body.String(), err)
	}
	return value
}

func assertExactFields(t *testing.T, value map[string]json.RawMessage, fields ...string) {
	t.Helper()
	want := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		want[field] = struct{}{}
	}
	got := make([]string, 0, len(value))
	for field := range value {
		got = append(got, field)
		if _, ok := want[field]; !ok {
			t.Errorf("public field %q is not in the contract", field)
		}
	}
	sort.Strings(got)
	sort.Strings(fields)
	if !reflect.DeepEqual(got, fields) {
		t.Errorf("public fields = %v, want exactly %v", got, fields)
	}
}

func decodeCreateResponse(t *testing.T, recorder *httptest.ResponseRecorder) assistantapi.CreateConversationResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	value := decodeJSONMap(t, recorder)
	assertExactFields(t, value, "conversation_id", "events_url", "run_id")
	var response assistantapi.CreateConversationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("create response validation: %v", err)
	}
	if !strings.HasPrefix(response.RunID, "run_") || strings.HasPrefix(response.RunID, "run-") {
		t.Fatalf("create response did not use a Scenery run ID: %q", response.RunID)
	}
	return response
}

func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) assistantapi.Error {
	t.Helper()
	value := decodeJSONMap(t, recorder)
	assertExactFields(t, value, "code", "message")
	var response assistantapi.Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("error response validation: %v", err)
	}
	return response
}

func decodePublicEvents(t *testing.T, recorder *httptest.ResponseRecorder) []assistantapi.Event {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("events response was compressed: Content-Encoding=%q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != assistantapi.NDJSONContentType+"; charset=utf-8" && got != assistantapi.NDJSONContentType {
		t.Fatalf("events content type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("events cache-control = %q", got)
	}
	if !recorder.Flushed {
		t.Fatal("events response was buffered instead of flushed")
	}
	lines := bytes.Split(bytes.TrimSpace(recorder.Body.Bytes()), []byte{'\n'})
	if len(lines) == 1 && len(lines[0]) == 0 {
		return nil
	}
	for _, line := range lines {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode event line %q: %v", line, err)
		}
		assertExactFields(t, fields, "assistant", "conversation_id", "data", "occurred_at", "run_id", "sequence", "type")
		for key, raw := range fields {
			if strings.Contains(strings.ToLower(key), "provider") || containsProviderToken(string(raw)) {
				t.Errorf("provider/private value escaped public event field %q: %s", key, raw)
			}
		}
	}
	events, err := assistantapi.DecodeNDJSON(recorder.Body.Bytes())
	if err != nil {
		t.Fatalf("decode public NDJSON: %v; body=%s", err, recorder.Body.String())
	}
	for _, event := range events {
		if event.Assistant != "support" {
			t.Errorf("event assistant = %q, want support", event.Assistant)
		}
		if strings.HasPrefix(event.RunID, "run-") {
			t.Errorf("private helper run ID escaped public event: %q", event.RunID)
		}
	}
	return events
}

var publicProviderToken = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])eve([^A-Za-z0-9_]|$)`)

func containsProviderToken(value string) bool {
	return publicProviderToken.MatchString(value)
}

func eventDataMap(t *testing.T, event assistantapi.Event) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &value); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	return value
}

func findEvent(events []assistantapi.Event, typ string) (assistantapi.Event, bool) {
	for _, event := range events {
		if event.Type == typ {
			return event, true
		}
	}
	return assistantapi.Event{}, false
}

func cookieFrom(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := recorder.Result().Cookies()
	for _, cookie := range cookies {
		if cookie.Name == assistanttoken.CookieName {
			return cookie
		}
	}
	t.Fatalf("response did not issue %q cookie; headers=%v", assistanttoken.CookieName, recorder.Header())
	return nil
}

func assertNotFoundResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantBody string) {
	t.Helper()
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != wantBody {
		t.Fatalf("not-found body = %q, want %q", got, wantBody)
	}
	value := decodeError(t, recorder)
	if value.Code != assistantapi.ErrorNotFound {
		t.Fatalf("not-found code = %q", value.Code)
	}
}

func createConversation(t *testing.T, testServer assistantTestServer, cookie *http.Cookie) (assistantapi.CreateConversationResponse, *http.Cookie) {
	t.Helper()
	recorder := assistantRequest(t, testServer.server, http.MethodPost, testServer.basePath, map[string]any{
		"message": map[string]string{"role": "user", "content": "hello"},
	}, cookie)
	response := decodeCreateResponse(t, recorder)
	if cookie == nil {
		cookie = cookieFrom(t, recorder)
	}
	return response, cookie
}

func TestAssistantGatewayBindsEachConversationToDistinctPrivateSession(t *testing.T) {
	testServer := newAssistantTestServer(t, assistantruntime.FakeConfig{})
	first, cookie := createConversation(t, testServer, nil)
	second, _ := createConversation(t, testServer, cookie)

	// Resolve the owner through the same signed cookie path instead of relying
	// on the cookie payload format.
	identity, err := testServer.signer.Verify(cookie)
	if err != nil {
		t.Fatalf("verify initiator cookie: %v", err)
	}
	owner := assistanttoken.OwnerDigest(identity.ID)
	expectation := assistanttoken.ConversationExpectation{AssistantAddress: "support", OwnerDigest: owner}
	firstClaims, err := testServer.manager.UnsealConversation(first.ConversationID, expectation)
	if err != nil {
		t.Fatalf("unseal first conversation: %v", err)
	}
	secondClaims, err := testServer.manager.UnsealConversation(second.ConversationID, expectation)
	if err != nil {
		t.Fatalf("unseal second conversation: %v", err)
	}
	if firstClaims.ConversationDigest == secondClaims.ConversationDigest {
		t.Fatalf("conversation digests were reused: %q", firstClaims.ConversationDigest)
	}
	if firstClaims.PrivateSessionID == secondClaims.PrivateSessionID {
		t.Fatalf("private helper session was reused: %q", firstClaims.PrivateSessionID)
	}
}

func TestAssistantGatewayPublicProtocolAndReconnect(t *testing.T) {
	testServer := newAssistantTestServer(t, assistantruntime.FakeConfig{
		TextChunks: []string{"hello ", "E", "ve", ".", " event"},
	})

	created, cookie := createConversation(t, testServer, nil)
	if cookie.Value == "" || !strings.HasPrefix(cookie.Value, "anon1_") {
		t.Fatalf("initiator cookie = %q", cookie.Value)
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("initiator cookie flags = HttpOnly:%v SameSite:%v", cookie.HttpOnly, cookie.SameSite)
	}
	identity, err := testServer.signer.Verify(cookie)
	if err != nil || identity.ID == "" {
		t.Fatalf("initiator cookie did not verify: identity=%+v err=%v", identity, err)
	}
	tamperedCookie := *cookie
	tamperedCookie.Value += "0"
	if _, err := testServer.signer.Verify(&tamperedCookie); err == nil {
		t.Fatal("tampered initiator cookie was accepted")
	}

	eventsRecorder := assistantEventsRequest(t, testServer.server, created.EventsURL, cookie)
	events := decodePublicEvents(t, eventsRecorder)
	if len(events) == 0 {
		t.Fatal("initial events stream was empty")
	}
	if _, ok := findEvent(events, assistantapi.EventRunStarted); !ok {
		t.Fatal("initial stream did not expose assistant.run.started")
	}
	if _, ok := findEvent(events, assistantapi.EventMessageDelta); !ok {
		t.Fatal("initial stream did not expose assistant.message.delta")
	}
	if _, ok := findEvent(events, assistantapi.EventRunCompleted); !ok {
		t.Fatal("initial stream did not expose assistant.run.completed")
	}
	for _, event := range events {
		if event.ConversationID != created.ConversationID || event.RunID != created.RunID {
			t.Errorf("event identity = conversation %q run %q, want %q/%q", event.ConversationID, event.RunID, created.ConversationID, created.RunID)
		}
	}
	if containsProviderToken(eventsRecorder.Body.String()) {
		t.Fatal("public event stream leaked the provider token")
	}
	joined := ""
	var deltaCount int
	for _, event := range events {
		if event.Type != assistantapi.EventMessageDelta {
			continue
		}
		deltaCount++
		data := eventDataMap(t, event)
		var text string
		if err := json.Unmarshal(data["text"], &text); err != nil {
			t.Fatalf("message delta text: %v", err)
		}
		joined += text
	}
	if deltaCount == 0 {
		t.Fatal("initial stream did not expose any message deltas")
	}
	wantText := assistantapi.RedactChunks([]string{"hello ", "E", "ve", ".", " event"})
	if joined != wantText {
		t.Fatalf("redacted message deltas = %q, want exact %q", joined, wantText)
	}
	completion, ok := findEvent(events, assistantapi.EventMessageCompleted)
	if !ok {
		t.Fatal("initial stream did not expose assistant.message.completed")
	}
	completionData := eventDataMap(t, completion)
	var completionText string
	if err := json.Unmarshal(completionData["text"], &completionText); err != nil {
		t.Fatalf("message completion text: %v", err)
	}
	wantCompletion := assistantapi.RedactString(strings.Join([]string{"hello ", "E", "ve", ".", " event"}, ""))
	if completionText != wantCompletion {
		t.Fatalf("redacted message completion = %q, want exact %q", completionText, wantCompletion)
	}

	last := events[len(events)-1].Sequence
	resumePath := created.EventsURL + "?after=" + strconv.FormatUint(last-1, 10)
	firstTail := assistantEventsRequest(t, testServer.server, resumePath, cookie)
	secondTail := assistantEventsRequest(t, testServer.server, resumePath, cookie)
	tail := decodePublicEvents(t, firstTail)
	repeat := decodePublicEvents(t, secondTail)
	if !reflect.DeepEqual(tail, repeat) {
		t.Fatalf("repeated after cursor changed stream: first=%+v repeat=%+v", tail, repeat)
	}
	for _, event := range tail {
		if event.Sequence <= last-1 {
			t.Fatalf("after cursor returned sequence %d", event.Sequence)
		}
	}

	turnRecorder := assistantRequest(t, testServer.server, http.MethodPost, created.EventsURL[:strings.Index(created.EventsURL, "/events")]+"/turns", map[string]any{
		"message": map[string]string{"role": "user", "content": "follow up"},
	}, cookie)
	if turnRecorder.Code != http.StatusOK {
		t.Fatalf("turn status = %d, body=%s", turnRecorder.Code, turnRecorder.Body.String())
	}
	turnFields := decodeJSONMap(t, turnRecorder)
	assertExactFields(t, turnFields, "run_id")
	var turn assistantapi.SendTurnResponse
	if err := json.Unmarshal(turnRecorder.Body.Bytes(), &turn); err != nil {
		t.Fatalf("decode turn response: %v", err)
	}
	if err := turn.Validate(); err != nil {
		t.Fatalf("turn response validation: %v", err)
	}
	if turn.RunID == created.RunID || strings.HasPrefix(turn.RunID, "run-") {
		t.Fatalf("turn did not receive a new Scenery run ID: %q", turn.RunID)
	}

	// The route table is exercised through the real newServer handler. The
	// stream assertions above also prove that the raw gateway response flushes
	// and bypasses the ordinary gzip middleware.
}

func TestAssistantGatewayOwnershipAndHandleFailures(t *testing.T) {
	testServer := newAssistantTestServer(t, assistantruntime.FakeConfig{Text: "private"})
	created, ownerCookie := createConversation(t, testServer, nil)
	_, otherCookie := createConversation(t, testServer, nil)

	unknown := assistantRequest(t, testServer.server, http.MethodGet, testServer.basePath+"/conv1_deadbeef/events", nil, ownerCookie)
	assertNotFoundResponse(t, unknown, `{"code":"not_found","message":"assistant resource not found"}`)
	wantNotFound := unknown.Body.String()

	crossOwner := assistantRequest(t, testServer.server, http.MethodGet, created.EventsURL, nil, otherCookie)
	assertNotFoundResponse(t, crossOwner, wantNotFound)

	malformed := assistantRequest(t, testServer.server, http.MethodGet, testServer.basePath+"/not-a-handle/events", nil, ownerCookie)
	assertNotFoundResponse(t, malformed, wantNotFound)

	// Advance only the conversation token clock. The signed initiator cookie
	// remains valid, so this specifically proves expired handles normalize to
	// the same not_found response as an unknown or cross-principal handle.
	testServer.clock.value = testServer.clock.value.Add(2 * time.Hour)
	expired := assistantRequest(t, testServer.server, http.MethodGet, created.EventsURL, nil, ownerCookie)
	assertNotFoundResponse(t, expired, wantNotFound)

	// Every route must enforce ownership, not only event reads.
	turn := assistantRequest(t, testServer.server, http.MethodPost, strings.TrimSuffix(created.EventsURL, "/events")+"/turns", map[string]any{
		"message": map[string]string{"role": "user", "content": "no"},
	}, otherCookie)
	assertNotFoundResponse(t, turn, wantNotFound)
	cancel := assistantRequest(t, testServer.server, http.MethodPost, strings.TrimSuffix(created.EventsURL, "/events")+"/runs/"+created.RunID+"/cancel", nil, otherCookie)
	assertNotFoundResponse(t, cancel, wantNotFound)
}

func TestAssistantGatewayApprovalSealsIDsAndCancellation(t *testing.T) {
	testServer := newAssistantTestServer(t, assistantruntime.FakeConfig{
		Text:            "approval",
		CapabilityName:  "delete",
		CapabilityInput: json.RawMessage(`{"resource":"private"}`),
		RequireApproval: true,
	})
	created, cookie := createConversation(t, testServer, nil)
	initialRecorder := assistantEventsRequest(t, testServer.server, created.EventsURL, cookie)
	initial := decodePublicEvents(t, initialRecorder)
	approvalEvent, ok := findEvent(initial, assistantapi.EventApprovalRequired)
	if !ok {
		t.Fatal("approval-required event was not exposed")
	}
	approvalData := eventDataMap(t, approvalEvent)
	var approvalID string
	if err := json.Unmarshal(approvalData["approval_id"], &approvalID); err != nil {
		t.Fatalf("decode sealed approval ID: %v; data=%s", err, approvalEvent.Data)
	}
	if err := assistantapi.ValidateApprovalID(approvalID); err != nil {
		t.Fatalf("approval ID is not a sealed appr1 token: %q: %v", approvalID, err)
	}
	if strings.Contains(approvalID, "approval-") {
		t.Fatalf("private helper approval ID escaped: %q", approvalID)
	}

	allowPath := strings.TrimSuffix(created.EventsURL, "/events") + "/approvals/" + approvalID
	allowRecorder := assistantRequest(t, testServer.server, http.MethodPost, allowPath, map[string]string{"decision": assistantapi.ApprovalApprove}, cookie)
	if allowRecorder.Code != http.StatusOK {
		t.Fatalf("approval allow status = %d, body=%s", allowRecorder.Code, allowRecorder.Body.String())
	}
	allowFields := decodeJSONMap(t, allowRecorder)
	assertExactFields(t, allowFields, "approval_id", "decision")
	var allow assistantapi.ResolveApprovalResponse
	if err := json.Unmarshal(allowRecorder.Body.Bytes(), &allow); err != nil {
		t.Fatalf("decode allow response: %v", err)
	}
	if allow.ApprovalID != approvalID || allow.Decision != assistantapi.ApprovalApprove {
		t.Fatalf("allow response = %+v", allow)
	}

	allowEventsRecorder := assistantEventsRequest(t, testServer.server, created.EventsURL+"?after="+strconv.FormatUint(approvalEvent.Sequence, 10), cookie)
	allowEvents := decodePublicEvents(t, allowEventsRecorder)
	if _, ok := findEvent(allowEvents, assistantapi.EventCapabilityStarted); !ok {
		t.Fatal("allow did not expose assistant.capability.started")
	}
	if _, ok := findEvent(allowEvents, assistantapi.EventCapabilityCompleted); !ok {
		t.Fatal("allow did not expose assistant.capability.completed")
	}
	if _, ok := findEvent(allowEvents, assistantapi.EventRunCompleted); !ok {
		t.Fatal("allow did not expose assistant.run.completed")
	}

	// A second conversation exercises the deny branch and proves that the
	// sealed approval ID cannot be substituted across conversations.
	createdDeny, cookieDeny := createConversation(t, testServer, nil)
	denyInitialRecorder := assistantEventsRequest(t, testServer.server, createdDeny.EventsURL, cookieDeny)
	denyInitial := decodePublicEvents(t, denyInitialRecorder)
	denyEvent, ok := findEvent(denyInitial, assistantapi.EventApprovalRequired)
	if !ok {
		t.Fatal("deny approval-required event was not exposed")
	}
	denyData := eventDataMap(t, denyEvent)
	var denyID string
	if err := json.Unmarshal(denyData["approval_id"], &denyID); err != nil {
		t.Fatalf("decode deny approval ID: %v", err)
	}
	if err := assistantapi.ValidateApprovalID(denyID); err != nil {
		t.Fatalf("deny approval ID is invalid: %v", err)
	}
	wrongConversation := assistantRequest(t, testServer.server, http.MethodPost, strings.TrimSuffix(createdDeny.EventsURL, "/events")+"/approvals/"+approvalID, map[string]string{"decision": assistantapi.ApprovalApprove}, cookieDeny)
	if wrongConversation.Code != http.StatusNotFound {
		t.Fatalf("cross-conversation approval status = %d, body=%s", wrongConversation.Code, wrongConversation.Body.String())
	}
	denyRecorder := assistantRequest(t, testServer.server, http.MethodPost, strings.TrimSuffix(createdDeny.EventsURL, "/events")+"/approvals/"+denyID, map[string]string{"decision": assistantapi.ApprovalDeny}, cookieDeny)
	if denyRecorder.Code != http.StatusOK {
		t.Fatalf("approval deny status = %d, body=%s", denyRecorder.Code, denyRecorder.Body.String())
	}
	denyFields := decodeJSONMap(t, denyRecorder)
	assertExactFields(t, denyFields, "approval_id", "decision")
	var deny assistantapi.ResolveApprovalResponse
	if err := json.Unmarshal(denyRecorder.Body.Bytes(), &deny); err != nil {
		t.Fatalf("decode deny response: %v", err)
	}
	if deny.ApprovalID != denyID || deny.Decision != assistantapi.ApprovalDeny {
		t.Fatalf("deny response = %+v", deny)
	}
	denyEventsRecorder := assistantEventsRequest(t, testServer.server, createdDeny.EventsURL+"?after="+strconv.FormatUint(denyEvent.Sequence, 10), cookieDeny)
	denyEvents := decodePublicEvents(t, denyEventsRecorder)
	if _, ok := findEvent(denyEvents, assistantapi.EventRunFailed); !ok {
		t.Fatal("deny did not expose assistant.run.failed")
	}
	if _, ok := findEvent(denyEvents, assistantapi.EventCapabilityStarted); ok {
		t.Fatal("deny exposed capability.started")
	}

	turnRecorder := assistantRequest(t, testServer.server, http.MethodPost, strings.TrimSuffix(createdDeny.EventsURL, "/events")+"/turns", map[string]any{
		"message": map[string]string{"role": "user", "content": "cancel me"},
	}, cookieDeny)
	if turnRecorder.Code != http.StatusOK {
		t.Fatalf("turn before cancel status = %d, body=%s", turnRecorder.Code, turnRecorder.Body.String())
	}
	var turn assistantapi.SendTurnResponse
	if err := json.Unmarshal(turnRecorder.Body.Bytes(), &turn); err != nil {
		t.Fatalf("decode cancel turn: %v", err)
	}
	cancelPath := strings.TrimSuffix(createdDeny.EventsURL, "/events") + "/runs/" + turn.RunID + "/cancel"
	cancelRecorder := assistantRequest(t, testServer.server, http.MethodPost, cancelPath, nil, cookieDeny)
	if cancelRecorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body=%s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
	cancelFields := decodeJSONMap(t, cancelRecorder)
	assertExactFields(t, cancelFields, "run_id", "state")
	var cancelled assistantapi.CancelRunResponse
	if err := json.Unmarshal(cancelRecorder.Body.Bytes(), &cancelled); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelled.RunID != turn.RunID || cancelled.State != "cancelled" {
		t.Fatalf("cancel response = %+v", cancelled)
	}
	cancelEventsRecorder := assistantEventsRequest(t, testServer.server, createdDeny.EventsURL+"?after="+strconv.FormatUint(denyEvent.Sequence, 10), cookieDeny)
	cancelEvents := decodePublicEvents(t, cancelEventsRecorder)
	if _, ok := findEvent(cancelEvents, assistantapi.EventRunCancelled); !ok {
		t.Fatal("cancel did not expose assistant.run.cancelled")
	}
}

func TestAssistantGatewayStreamsBeforeHelperEOFAndNeutralizesLateMalformedEvent(t *testing.T) {
	gateway := &assistantGateway{
		registration: AssistantRegistration{
			Address:            "app/assistant/support",
			Name:               "support",
			AssistantAddress:   "support",
			RuntimeRevision:    "runtime-1",
			CapabilityRevision: "capability-1",
		},
		approvals:    map[string]assistantApprovalState{},
		continuation: map[string]string{},
		publicEvents: map[string][]assistantapi.Event{},
		publicRuns:   map[string][]string{},
		privateRuns:  map[string]map[string]string{},
		cancelled:    map[string]map[string]bool{},
	}
	claims := assistanttoken.ConversationClaims{
		AssistantAddress:   "support",
		ConversationDigest: assistanttoken.ConversationDigest("conversation"),
		PrivateSessionID:   "session-1",
		ContinuationToken:  "continuation-1",
	}
	private := assistantcontrol.Event{
		Kind:               assistantcontrol.EventKind,
		SchemaRevision:     assistantcontrol.EventSchemaRevision,
		Type:               assistantcontrol.EventRunStarted,
		AssistantAddress:   "support",
		RuntimeRevision:    "runtime-1",
		CapabilityRevision: "capability-1",
		PrivateSessionID:   claims.PrivateSessionID,
		ContinuationToken:  claims.ContinuationToken,
		RunID:              "run_11111111111111111111111111111111",
		Sequence:           1,
		OccurredAt:         time.Unix(1_754_280_001, 0).UTC(),
		Data:               json.RawMessage(`{}`),
	}
	if err := private.Validate(); err != nil {
		t.Fatalf("validate private event fixture: %v", err)
	}
	encoded, err := assistantcontrol.MarshalEvent(private)
	if err != nil {
		t.Fatalf("marshal private event: %v", err)
	}

	reader, helper := io.Pipe()
	writer := newAssistantStreamingWriter()
	done := make(chan error, 1)
	go func() {
		done <- gateway.streamPrivateEvents(writer, reader, "conv1_01", claims, 0)
	}()
	if _, err := helper.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("write first private event: %v", err)
	}
	select {
	case <-writer.wrote:
		// The helper stream remains open, so this proves public bytes were not
		// buffered until EOF.
	case err := <-done:
		t.Fatalf("stream rejected first private event: %v", err)
	case <-time.After(time.Second):
		t.Fatal("first public assistant event was not flushed before helper EOF")
	}
	select {
	case err := <-done:
		t.Fatalf("stream returned before helper EOF: %v", err)
	default:
	}
	if _, err := helper.Write([]byte("not-json\n")); err != nil {
		t.Fatalf("write late malformed private event: %v", err)
	}
	if err := helper.Close(); err != nil {
		t.Fatalf("close helper stream: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("late malformed private event escaped after streaming began: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("assistant event stream did not terminate")
	}

	public, err := assistantapi.DecodeNDJSON(writer.Bytes())
	if err != nil {
		t.Fatalf("decode public event stream: %v", err)
	}
	if len(public) != 2 || public[0].Type != assistantapi.EventRunStarted || public[1].Type != assistantapi.EventRunFailed {
		t.Fatalf("public events = %#v, want run.started then neutral run.failed", public)
	}
	if strings.Contains(strings.ToLower(string(public[1].Data)), "private") || strings.Contains(string(public[1].Data), "not-json") {
		t.Fatalf("late private failure escaped public stream: %s", public[1].Data)
	}
}

func TestAssistantGatewayHelperFailuresAndMalformedPrivateEvents(t *testing.T) {
	testServer := newAssistantTestServer(t, assistantruntime.FakeConfig{Text: "hello"})
	created, cookie := createConversation(t, testServer, nil)

	testServer.helper.SetUnavailable()
	unavailable := assistantRequest(t, testServer.server, http.MethodGet, created.EventsURL, nil, cookie)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, body=%s", unavailable.Code, unavailable.Body.String())
	}
	unavailableError := decodeError(t, unavailable)
	if unavailableError.Code != assistantapi.ErrorUnavailable || containsProviderToken(unavailableError.Message) {
		t.Fatalf("unavailable error = %+v", unavailableError)
	}

	if err := testServer.helper.Restart(context.Background()); err != nil {
		t.Fatalf("restart fake helper: %v", err)
	}
	testServer.helper.InjectMalformedEvent(assistantcontrol.Event{})
	malformed := assistantRequest(t, testServer.server, http.MethodGet, created.EventsURL, nil, cookie)
	if malformed.Code != http.StatusInternalServerError {
		t.Fatalf("malformed private event status = %d, body=%s", malformed.Code, malformed.Body.String())
	}
	malformedError := decodeError(t, malformed)
	if malformedError.Code != assistantapi.ErrorInternal || containsProviderToken(malformedError.Message) || strings.Contains(strings.ToLower(malformedError.Message), "private") {
		t.Fatalf("malformed event error = %+v", malformedError)
	}

	// A crashed helper fails closed while unavailable, then emits only the
	// provider-neutral restart event after a successful restart.
	testServer.helper.ClearMalformedEvents()
	testServer.helper.Crash()
	crashed := assistantRequest(t, testServer.server, http.MethodGet, created.EventsURL, nil, cookie)
	if crashed.Code != http.StatusServiceUnavailable {
		t.Fatalf("crashed helper status = %d, body=%s", crashed.Code, crashed.Body.String())
	}
	if err := testServer.helper.Restart(context.Background()); err != nil {
		t.Fatalf("restart crashed fake helper: %v", err)
	}
	restarting := assistantEventsRequest(t, testServer.server, created.EventsURL, cookie)
	if restarting.Code != http.StatusOK {
		t.Fatalf("post-crash event status = %d, body=%s", restarting.Code, restarting.Body.String())
	}
	for _, event := range decodePublicEvents(t, restarting) {
		if event.Type == assistantapi.EventRuntimeRestarting {
			return
		}
	}
	t.Fatal("post-crash stream did not expose assistant.runtime.restarting")
}

func TestAssistantGatewayAuthenticatedOwnershipAndAuthData(t *testing.T) {
	testServer := newAssistantTestServerWithRegistration(t, assistantruntime.FakeConfig{Text: "auth"}, func(registration *AssistantRegistration) {
		registration.Access = Auth
		registration.Policy = &ContractHTTPPolicy{
			AuthorizationStrategy: "any_allow", AuthorizationRuleCount: 2,
			AuthorizationRules: []ContractAuthorizationRule{
				{Name: "support-tenant", Effect: "allow", Expression: `context.tenant_id == "tenant-a" && contains(principal.roles, "support")`},
				{Name: "viewer-tenant", Effect: "allow", Expression: `context.tenant_id == "tenant-b" && contains(principal.roles, "viewer")`},
			},
			PipelineSteps: []string{"std.middleware.trace"},
		}
		RegisterAuthHandler(&AuthHandler{
			Name: "test-auth",
			Authenticate: func(_ context.Context, token string) (AuthInfo, error) {
				switch token {
				case "user-a":
					return AuthInfo{UID: "user-a", Data: map[string]any{"tenant_id": "tenant-a", "roles": []string{"support"}}}, nil
				case "user-b":
					return AuthInfo{UID: "user-b", Data: map[string]any{"tenant_id": "tenant-b", "roles": []string{"viewer"}}}, nil
				default:
					return AuthInfo{}, fmt.Errorf("invalid auth token")
				}
			},
		})
	})

	authHeader := func(principal string) http.Header {
		return http.Header{"Authorization": []string{"Bearer " + principal}}
	}
	aCreateRecorder := assistantRequestWithHeaders(t, testServer.server, http.MethodPost, testServer.basePath, map[string]any{
		"message": map[string]string{"role": "user", "content": "hello from A"},
	}, nil, "", authHeader("user-a"))
	aCreated := decodeCreateResponse(t, aCreateRecorder)
	bCreateRecorder := assistantRequestWithHeaders(t, testServer.server, http.MethodPost, testServer.basePath, map[string]any{
		"message": map[string]string{"role": "user", "content": "hello from B"},
	}, nil, "", authHeader("user-b"))
	_ = decodeCreateResponse(t, bCreateRecorder)

	aEvents := assistantRequestWithHeaders(t, testServer.server, http.MethodGet, aCreated.EventsURL, nil, nil, "gzip", authHeader("user-a"))
	if events := decodePublicEvents(t, aEvents); len(events) == 0 {
		t.Fatal("authenticated owner could not read its conversation")
	}
	unknown := assistantRequestWithHeaders(t, testServer.server, http.MethodGet, testServer.basePath+"/conv1_deadbeef/events", nil, nil, "", authHeader("user-b"))
	assertNotFoundResponse(t, unknown, `{"code":"not_found","message":"assistant resource not found"}`)
	crossOwner := assistantRequestWithHeaders(t, testServer.server, http.MethodGet, aCreated.EventsURL, nil, nil, "", authHeader("user-b"))
	assertNotFoundResponse(t, crossOwner, unknown.Body.String())

}
