package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scenery.sh/internal/assistantapi"
	"scenery.sh/internal/assistantruntime"
)

// assistantRunTestHelper is a fake helper whose turns can be held open and
// answered by the test, and whose client event streams run a hook before the
// helper answers. The host's own run observers pass through untouched.
type assistantRunTestHelper struct {
	*assistantruntime.FakeHelper
	mu      sync.Mutex
	gates   map[string]chan error
	entered chan string
	hook    func(assistantruntime.StreamRequest)
}

func (helper *assistantRunTestHelper) SendTurn(ctx context.Context, request assistantruntime.TurnRequest) (assistantruntime.TurnResult, error) {
	helper.mu.Lock()
	gate := helper.gates[request.Message]
	helper.mu.Unlock()
	if gate != nil {
		helper.entered <- request.RunID
		if err := <-gate; err != nil {
			return assistantruntime.TurnResult{}, err
		}
	}
	return helper.FakeHelper.SendTurn(ctx, request)
}

func (helper *assistantRunTestHelper) StreamEvents(ctx context.Context, request assistantruntime.StreamRequest) (io.ReadCloser, error) {
	if !strings.HasPrefix(request.RequestID, assistantRunObserverRequestPrefix+"_") {
		helper.mu.Lock()
		hook := helper.hook
		helper.hook = nil
		helper.mu.Unlock()
		if hook != nil {
			hook(request)
		}
	}
	return helper.FakeHelper.StreamEvents(ctx, request)
}

// assistantRunFixture serves one assistant conversation through process host
// incarnations that share a session's host state, with an MCP tool whose
// answer names the echo instance of the generation it executed.
type assistantRunFixture struct {
	t        *testing.T
	server   assistantTestServer
	helper   *assistantRunTestHelper
	echo     []*processHostTestBackend
	owner    processGenerationInstance
	state    string
	host     *processHost
	serving  atomic.Pointer[processHost]
	created  assistantapi.CreateConversationResponse
	cookie   *http.Cookie
	mu       sync.Mutex
	channels map[string]string
}

func newAssistantRunFixture(t *testing.T) *assistantRunFixture {
	t.Helper()
	fixture := &assistantRunFixture{t: t, helper: &assistantRunTestHelper{gates: map[string]chan error{}, entered: make(chan string, 4)}, state: t.TempDir(), channels: map[string]string{}}
	fixture.server = newAssistantTestServerWithRegistration(t, assistantruntime.FakeConfig{Text: "approval", CapabilityName: "delete", CapabilityInput: json.RawMessage(`{}`), RequireApproval: true}, func(registration *AssistantRegistration) {
		fixture.helper.FakeHelper = registration.Client.(*assistantruntime.FakeHelper)
		registration.Client = fixture.helper
	})
	useLinkedProcessIdentityForTest(t)
	fixture.echo = []*processHostTestBackend{startProcessHostTestBackend(t, "echo_echo", 601, "sha256:echo-1"), startProcessHostTestBackend(t, "echo_echo", 602, "sha256:echo-2")}
	control := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fixture.serving.Load().serveControl(w, req) }))
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: control})
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "support#house/binding/describe_mcp", Name: "house__describe", AssistantAddress: "support",
		DecodeInput: func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) {
			return []byte(`{"kind":"result","name":"ok","value":` + string(value.([]byte)) + `}`), nil
		},
		Invoke: func(ctx context.Context, call MCPToolCallContext, input any) (any, error) {
			return InvokeContractBindingJSON(ctx, "echo/binding/echo_internal", "house", []byte(`{}`))
		},
	}); err != nil {
		t.Fatal(err)
	}
	var served atomic.Int32
	fixture.owner = serveProcessMCPOwnerForTest(t, &served)
	t.Cleanup(func() { setActiveProcessHost(nil) })
	fixture.startHost()
	return fixture
}

// startHost starts a host incarnation of the session; the previous one stops.
func (fixture *assistantRunFixture) startHost() {
	t := fixture.t
	t.Helper()
	if fixture.host != nil {
		close(fixture.host.closing)
	}
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "echo_echo", MCPTools: []ProcessHostMCPTool{
		{Process: "house_house", AssistantAddress: "support", Name: "house__describe"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.openState(fixture.state); err != nil {
		t.Fatal(err)
	}
	host.local, host.localRoutes = fixture.server.server.Handler, newRouteTable()
	for _, endpoint := range listEndpoints() {
		registerEndpointRoute(host.localRoutes, endpoint, func(http.ResponseWriter, *http.Request, routeParams) {})
	}
	t.Cleanup(func() {
		select {
		case <-host.closing:
		default:
			close(host.closing)
		}
	})
	fixture.host = host
	fixture.serving.Store(host)
	setActiveProcessHost(host)
}

// publish publishes generation number, whose echo instance answers
// "echo-1" for odd and "echo-2" for even numbers.
func (fixture *assistantRunFixture) publish(number uint64) {
	fixture.t.Helper()
	if err := fixture.host.publish(processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Identity: processHostTestBuild(number), Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
		Processes: map[string]processGenerationInstance{"house_house": fixture.owner, "echo_echo": fixture.echo[(number+1)%2].instance}}); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *assistantRunFixture) ingress() *http.Server {
	return &http.Server{Handler: http.HandlerFunc(fixture.host.serveIngress)}
}

func (fixture *assistantRunFixture) base() string {
	return strings.TrimSuffix(fixture.created.EventsURL, "/events")
}

func (fixture *assistantRunFixture) create() string {
	fixture.t.Helper()
	recorder := assistantRequest(fixture.t, fixture.ingress(), http.MethodPost, fixture.server.basePath, map[string]any{"message": map[string]string{"role": "user", "content": "hello"}}, nil)
	fixture.created, fixture.cookie = decodeCreateResponse(fixture.t, recorder), cookieFrom(fixture.t, recorder)
	fixture.remember(fixture.created.RunID)
	return fixture.created.RunID
}

// turn sends a turn and returns its answer.
func (fixture *assistantRunFixture) turn(message string) *httptest.ResponseRecorder {
	fixture.t.Helper()
	return assistantRequest(fixture.t, fixture.ingress(), http.MethodPost, fixture.base()+"/turns", map[string]any{"message": map[string]string{"role": "user", "content": message}}, fixture.cookie)
}

// heldTurn sends a turn the helper holds open until the test answers it, and
// returns the turn's run, its answer gate and its eventual response.
func (fixture *assistantRunFixture) heldTurn(message string) (string, chan error, chan *httptest.ResponseRecorder) {
	fixture.t.Helper()
	gate := make(chan error, 1)
	fixture.helper.mu.Lock()
	fixture.helper.gates[message] = gate
	fixture.helper.mu.Unlock()
	answered := make(chan *httptest.ResponseRecorder, 1)
	server := fixture.ingress()
	go func() {
		answered <- assistantRequest(fixture.t, server, http.MethodPost, fixture.base()+"/turns", map[string]any{"message": map[string]string{"role": "user", "content": message}}, fixture.cookie)
	}()
	runID := <-fixture.helper.entered
	fixture.remember(runID)
	return runID, gate, answered
}

// remember records the conversation of a run the host has reserved.
func (fixture *assistantRunFixture) remember(runID string) {
	fixture.t.Helper()
	conversations := &fixture.host.conversations
	conversations.Lock()
	defer conversations.Unlock()
	for _, run := range conversations.runs {
		if run.runID == runID {
			fixture.mu.Lock()
			fixture.channels[runID] = run.conversation
			fixture.mu.Unlock()
			return
		}
	}
	fixture.t.Fatalf("the host reserved no run %s", runID)
}

// describe calls the tool as run runID and returns its answer or failure.
func (fixture *assistantRunFixture) describe(runID string) string {
	fixture.t.Helper()
	fixture.mu.Lock()
	conversation := fixture.channels[runID]
	fixture.mu.Unlock()
	if conversation == "" {
		conversation = fixture.channels[fixture.created.RunID]
	}
	parts := strings.SplitN(conversation, "\x00", 3)
	dispatch, _ := assistantMCPDispatchers()
	outcome, err := dispatch.CallTool(context.Background(), MCPToolCallContext{AssistantAddress: parts[0], Principal: parts[1], ConversationDigest: parts[2], RunID: runID}, "house__describe", json.RawMessage(`{}`))
	if err != nil {
		return err.Error()
	}
	return string(outcome.Value)
}

func (fixture *assistantRunFixture) events() *httptest.ResponseRecorder {
	fixture.t.Helper()
	return assistantEventsRequest(fixture.t, fixture.ingress(), fixture.created.EventsURL, fixture.cookie)
}

// waitEnded waits until the host no longer runs runID.
func (fixture *assistantRunFixture) waitEnded(runID string) {
	fixture.t.Helper()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(fixture.describe(runID), "is not running") {
		if time.Now().After(deadline) {
			fixture.t.Fatalf("run %s did not end: %s", runID, fixture.describe(runID))
		}
		time.Sleep(time.Millisecond)
	}
}

const (
	assistantRunEchoOne = `"echo_echo:sha256:echo-1"`
	assistantRunEchoTwo = `"echo_echo:sha256:echo-2"`
)

// A tool call executes the generation of the run that makes it. A turn whose
// start is still in progress, rejected, or overlapping another rejected turn
// never redirects another run's calls; a turn whose outcome is unknown keeps
// executing until it is observed or expires; a run ends when the host observes
// its terminal event although no client streams; and a replacement host
// neither revives an ended run nor lets a run it did not start execute.
func TestAssistantToolCallsExecuteTheGenerationOfTheirOwnRun(t *testing.T) {
	fixture := newAssistantRunFixture(t)
	fixture.publish(1)
	first := fixture.create()
	if got := fixture.describe(first); got != assistantRunEchoOne {
		t.Fatalf("tool call of the first run = %s", got)
	}
	fixture.publish(2)

	pending, pendingGate, pendingAnswer := fixture.heldTurn("second")
	if first, second := fixture.describe(first), fixture.describe(pending); first != assistantRunEchoOne || second != assistantRunEchoTwo {
		t.Fatalf("while a turn is pending the first run executes %s and the pending run %s", first, second)
	}
	overlapping, overlappingGate, overlappingAnswer := fixture.heldTurn("third")
	pendingGate <- &assistantruntime.ControlError{Code: "helper_request_failed"}
	overlappingGate <- &assistantruntime.ControlError{Code: "helper_request_failed"}
	if code := (<-pendingAnswer).Code; code == http.StatusOK {
		t.Fatalf("rejected turn answered %d", code)
	}
	<-overlappingAnswer
	for _, rejected := range []string{pending, overlapping} {
		if got := fixture.describe(rejected); !strings.Contains(got, "is not running") {
			t.Fatalf("tool call of a rejected run = %s", got)
		}
	}
	if got := fixture.describe(first); got != assistantRunEchoOne {
		t.Fatalf("after two rejected turns the first run executes %s", got)
	}

	unknown, unknownGate, unknownAnswer := fixture.heldTurn("fourth")
	unknownGate <- assistantruntime.ErrUnavailable
	<-unknownAnswer
	if got := fixture.describe(unknown); got != assistantRunEchoTwo {
		t.Fatalf("tool call of a run whose start outcome is unknown = %s", got)
	}
	if got := fixture.describe(""); !strings.Contains(got, "names no run") {
		t.Fatalf("tool call without a run = %s", got)
	}
	if got := fixture.describe("run_unknown"); !strings.Contains(got, "is not running") {
		t.Fatalf("tool call of a run the host never started = %s", got)
	}

	// No client stream is open: the host observes the cancelled run's end.
	if recorder := assistantRequest(t, fixture.ingress(), http.MethodPost, fixture.base()+"/runs/"+first+"/cancel", nil, fixture.cookie); recorder.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", recorder.Code, recorder.Body.String())
	}
	fixture.waitEnded(first)
	if status, _ := fixture.host.retire(1, false); status != http.StatusNoContent {
		t.Fatalf("retiring the generation of the ended run = %d", status)
	}

	fixture.startHost()
	fixture.publish(4)
	if got := fixture.describe(first); !strings.Contains(got, "is not running") {
		t.Fatalf("replacement host revived an ended run: %s", got)
	}
	if got := fixture.describe(unknown); !strings.Contains(got, "generation 2, which is no longer dispatchable") {
		t.Fatalf("replacement host let an earlier host's run execute: %s", got)
	}
	var run *processHostRun
	fixture.host.conversations.Lock()
	for _, candidate := range fixture.host.conversations.runs {
		if candidate.runID == unknown {
			run = candidate
		}
	}
	fixture.host.conversations.Unlock()
	next := fixture.turn("fifth")
	var accepted assistantapi.SendTurnResponse
	if next.Code != http.StatusOK || json.Unmarshal(next.Body.Bytes(), &accepted) != nil {
		t.Fatalf("turn on the replacement host = %d %s", next.Code, next.Body.String())
	}
	fixture.remember(accepted.RunID)
	if got := fixture.describe(accepted.RunID); got != assistantRunEchoTwo {
		t.Fatalf("tool call of a run started on the replacement host = %s", got)
	}
	// A run whose start outcome stayed unknown ends when its bound expires.
	fixture.host.conversations.Lock()
	run.state = assistantRunUnknown
	fixture.host.conversations.Unlock()
	(&assistantRunReservation{host: fixture.host, run: run}).expire()
	(&assistantRunReservation{host: fixture.host, run: run}).observed(true)
	if got := fixture.describe(unknown); !strings.Contains(got, "was revoked because its start outcome stayed unknown") {
		t.Fatalf("tool call of a revoked run = %s", got)
	}
	fixture.startHost()
	fixture.publish(6)
	if got := fixture.describe(unknown); !strings.Contains(got, "was revoked because its start outcome stayed unknown") {
		t.Fatalf("tool call of a revoked run on a replacement host = %s", got)
	}
}

// Every event stream of a conversation attests the generation its latest
// accepted run executes: overlapping streams agree, a stream a later run in
// another generation supersedes ends so its client resumes, and a forced
// retirement ends the streams attesting that generation and fails its run's
// tool calls instead of executing a newer generation.
func TestAssistantEventStreamsAttestTheirConversationsRun(t *testing.T) {
	fixture := newAssistantRunFixture(t)
	generation := func(recorder *httptest.ResponseRecorder) string {
		return recorder.Header().Get(processGenerationHeader)
	}
	fixture.publish(1)
	first := fixture.create()

	var inner *httptest.ResponseRecorder
	var during string
	fixture.helper.hook = func(assistantruntime.StreamRequest) {
		fixture.publish(2)
		inner = fixture.events()
		during = fixture.describe(first)
	}
	outer := fixture.events()
	if generation(outer) != "1" || generation(inner) != "1" || during != assistantRunEchoOne {
		t.Fatalf("overlapping streams attest %q and %q while the run executes %s", generation(outer), generation(inner), during)
	}
	assertProcessHostAttestation(t, inner.Header(), processHostTestBuild(1))

	var second string
	fixture.helper.hook = func(assistantruntime.StreamRequest) {
		fixture.publish(3)
		var accepted assistantapi.SendTurnResponse
		_ = json.Unmarshal(fixture.turn("second").Body.Bytes(), &accepted)
		second = accepted.RunID
	}
	superseded := fixture.events()
	if superseded.Code != http.StatusOK || generation(superseded) != "1" || strings.TrimSpace(superseded.Body.String()) != "" {
		t.Fatalf("superseded stream = %d in %q: %s", superseded.Code, generation(superseded), superseded.Body.String())
	}
	fixture.remember(second)
	if resumed := fixture.events(); generation(resumed) != "3" || fixture.describe(second) != assistantRunEchoOne || fixture.describe(first) != assistantRunEchoOne {
		t.Fatalf("resumed stream attests %q while the runs execute %s and %s", generation(resumed), fixture.describe(second), fixture.describe(first))
	}

	fixture.helper.hook = func(assistantruntime.StreamRequest) {
		fixture.publish(4)
		if status, _ := fixture.host.retire(3, true); status != http.StatusNoContent {
			t.Errorf("forced retirement = %d", status)
		}
	}
	if retired := fixture.events(); generation(retired) != "3" || strings.TrimSpace(retired.Body.String()) != "" {
		t.Fatalf("stream of a retired generation = %q: %s", generation(retired), retired.Body.String())
	}
	if got := fixture.describe(second); !strings.Contains(got, "generation 3, which is no longer dispatchable") {
		t.Fatalf("tool call of a run whose generation was retired = %s", got)
	}
	if current := fixture.events(); generation(current) != "4" {
		t.Fatalf("stream of a conversation whose latest run cannot execute attests %q", generation(current))
	}
}

// A run that cannot be committed is refused before the helper sees it, and the
// poisoned run journal lets no run execute, in this host or a later one.
func TestAssistantRunJournalFailureRefusesRuns(t *testing.T) {
	fixture := newAssistantRunFixture(t)
	fixture.publish(1)
	first := fixture.create()
	if err := os.Chmod(filepath.Join(fixture.state, processHostRunsJournal), 0o400); err != nil {
		t.Fatal(err)
	}
	if refused := fixture.turn("second"); refused.Code != http.StatusServiceUnavailable {
		t.Fatalf("turn whose run cannot be committed = %d %s", refused.Code, refused.Body.String())
	}
	if got := fixture.describe(first); !strings.Contains(got, errProcessHostStateUnavailable.Error()) {
		t.Fatalf("tool call with a poisoned run journal = %s", got)
	}
	fixture.startHost()
	fixture.publish(2)
	if got := fixture.describe(first); !strings.Contains(got, errProcessHostStateUnavailable.Error()) {
		t.Fatalf("tool call on a replacement host of a poisoned run journal = %s", got)
	}
}

// The run limit never forgets a run that can still execute: a start beyond it
// is refused, and a revoked run makes room.
func TestAssistantRunLimitNeverForgetsARunThatCanExecute(t *testing.T) {
	generation := &processHostGeneration{number: 1, retired: make(chan struct{})}
	var conversations processHostConversations
	for index := range processHostRunLimit {
		runID := "run-" + time.Duration(index).String()
		conversations.install(processHostRunKey("conversation", runID), &processHostRun{conversation: "conversation", runID: runID, number: 1, generation: generation, ended: make(chan struct{})})
	}
	if conversations.room() {
		t.Fatal("the limit made room by forgetting a run that can execute")
	}
	revoked := conversations.runs[conversations.order[len(conversations.order)/2]]
	revoked.generation = nil
	if !conversations.room() || len(conversations.runs) != processHostRunLimit-1 {
		t.Fatalf("a revoked run did not make room: %d runs", len(conversations.runs))
	}
	select {
	case <-revoked.ended:
	default:
		t.Fatal("the forgotten run is not ended")
	}
}

// assistantSilentStreamHelper answers every event read with a stream that sends
// nothing until its request ends, as a helper does while a run is silent.
type assistantSilentStreamHelper struct {
	*assistantruntime.FakeHelper
	reads chan struct{}
}

func (helper assistantSilentStreamHelper) StreamEvents(ctx context.Context, _ assistantruntime.StreamRequest) (io.ReadCloser, error) {
	select {
	case helper.reads <- struct{}{}:
	default:
	}
	return assistantSilentStream{ctx: ctx}, nil
}

type assistantSilentStream struct{ ctx context.Context }

func (stream assistantSilentStream) Read([]byte) (int, error) {
	<-stream.ctx.Done()
	return 0, stream.ctx.Err()
}

func (stream assistantSilentStream) Close() error { return nil }

// A run whose start outcome stays unknown is revoked at its bound although the
// event stream the host is reading never sends anything, and the read ends.
func TestUnknownAssistantRunIsRevokedWhileItsEventStreamIsSilent(t *testing.T) {
	previous := assistantRunUnknownTimeout
	assistantRunUnknownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { assistantRunUnknownTimeout = previous })
	generation := &processHostGeneration{number: 1, retired: make(chan struct{})}
	host := &processHost{closing: make(chan struct{})}
	t.Cleanup(func() { close(host.closing) })
	run := &processHostRun{conversation: "conversation", runID: "run_1", number: 1, generation: generation, state: assistantRunUnknown, ended: make(chan struct{})}
	host.conversations.install(processHostRunKey("conversation", "run_1"), run)
	reservation := &assistantRunReservation{host: host, run: run}
	helper := assistantSilentStreamHelper{FakeHelper: assistantruntime.NewFakeHelper(), reads: make(chan struct{}, 1)}
	(&assistantGateway{}).observeAssistantRun(reservation, helper, assistantruntime.StreamRequest{PrivateSessionID: "session-1"})
	select {
	case <-helper.reads:
	case <-time.After(time.Second):
		t.Fatal("the observer never read the run's events")
	}
	select {
	case <-run.ended:
	case <-time.After(time.Second):
		t.Fatal("the unknown run was not revoked while its stream stayed silent")
	}
	host.conversations.Lock()
	defer host.conversations.Unlock()
	if !run.revoked || run.generation != nil {
		t.Fatalf("revoked = %v, generation held = %v", run.revoked, run.generation != nil)
	}
}
