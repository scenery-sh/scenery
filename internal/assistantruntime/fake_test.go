package assistantruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

func testFake(t *testing.T, config FakeConfig) *FakeHelper {
	t.Helper()
	if config.Now == nil {
		config.Now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	}
	fake := NewFakeHelper(config)
	if err := fake.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return fake
}

func testMetadata() RequestMetadata {
	return RequestMetadata{
		RequestID: "request-1", AssistantAddress: "support",
		RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1",
		Principal: "principal-1", ConversationDigest: "digest-1",
	}
}

func streamEvents(t *testing.T, fake *FakeHelper, request StreamRequest) []assistantcontrol.Event {
	t.Helper()
	stream, err := fake.StreamEvents(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	encoded, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	var events []assistantcontrol.Event
	for _, line := range strings.Split(strings.TrimSpace(string(encoded)), "\n") {
		if line == "" {
			continue
		}
		event, err := assistantcontrol.ParseEvent([]byte(line))
		if err != nil {
			t.Fatalf("parse streamed event: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func streamRequest(session, continuation string, after uint64) StreamRequest {
	return StreamRequest{RequestMetadata: testMetadata(), PrivateSessionID: session, ContinuationToken: continuation, After: after}
}

func TestFakeHelperCompleteFlowAndArbitraryChunks(t *testing.T) {
	fake := testFake(t, FakeConfig{TextChunks: []string{"hel", "lo", "!"}, CapabilityName: "write", CapabilityInput: json.RawMessage(`{"x":1}`), RequireApproval: true})
	info, err := fake.Info(context.Background())
	if err != nil || info.AssistantAddress != "support" || info.ControlProtocol != assistantcontrol.ControlProtocol {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	health, err := fake.Health(context.Background())
	if err != nil || !health.Ready {
		t.Fatalf("health=%+v err=%v", health, err)
	}

	created, err := fake.StartConversation(context.Background(), StartRequest{RequestMetadata: testMetadata(), RunID: "run_deadbeef", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if created.RunID != "run_deadbeef" || created.PrivateSessionID == "" || created.ContinuationToken == "" || created.ConversationID == "" {
		t.Fatalf("created=%+v", created)
	}

	first := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	if len(first) != 7 {
		t.Fatalf("events=%d, want run/text/proposal/wait", len(first))
	}
	for index, event := range first {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence=%d", index, event.Sequence)
		}
		if event.PrivateSessionID != created.PrivateSessionID || event.RunID != "run_deadbeef" {
			t.Fatalf("event %d identity=%+v", index, event)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("event %d failed control validation: %v", index, err)
		}
	}
	var deltas []string
	var approvalID string
	for _, event := range first {
		switch event.Type {
		case EventMessageDelta:
			var data struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(event.Data, &data)
			deltas = append(deltas, data.Text)
		case EventApprovalRequired:
			approvalID = event.ApprovalID
			if event.ApprovalWait == nil || event.ApprovalWait.ApprovalID != approvalID {
				t.Fatalf("approval wait=%+v", event)
			}
		}
	}
	if got := deltas; !reflect.DeepEqual(got, []string{"hel", "lo", "!"}) {
		t.Fatalf("deltas=%v", got)
	}
	if approvalID == "" {
		t.Fatal("approval event missing id")
	}

	missingDecision := ApprovalRequest{RequestMetadata: testMetadata(), PrivateSessionID: created.PrivateSessionID, ContinuationToken: created.ContinuationToken, RunID: created.RunID, ApprovalID: approvalID}
	if err := fake.ResolveApproval(context.Background(), missingDecision); err == nil {
		t.Fatal("missing approval decision unexpectedly succeeded")
	}
	allow := missingDecision
	allow.Decision = ApprovalAllow
	if err := fake.ResolveApproval(context.Background(), allow); err != nil {
		t.Fatal(err)
	}
	resumed := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, first[len(first)-1].Sequence))
	if len(resumed) != 3 || resumed[0].Type != EventCapabilityStarted || resumed[1].Type != EventCapabilityComplete || resumed[2].Type != EventRunCompleted {
		t.Fatalf("post-approval events=%+v", resumed)
	}
}

func TestFakeHelperResumeIsStrictlyAfterAndIdempotent(t *testing.T) {
	fake := testFake(t, FakeConfig{TextChunks: []string{"a", "b"}})
	created, err := fake.StartConversation(context.Background(), StartRequest{RequestMetadata: testMetadata(), RunID: "run_one", Message: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	request := streamRequest(created.PrivateSessionID, created.ContinuationToken, 0)
	all := streamEvents(t, fake, request)
	request.After = 2
	tail := streamEvents(t, fake, request)
	for _, event := range tail {
		if event.Sequence <= request.After {
			t.Fatalf("event sequence=%d not strictly after %d", event.Sequence, request.After)
		}
	}
	repeat := streamEvents(t, fake, request)
	if !reflect.DeepEqual(tail, repeat) {
		t.Fatalf("repeat=%+v tail=%+v", repeat, tail)
	}
	if len(all) != len(tail)+2 {
		t.Fatalf("all=%d tail=%d", len(all), len(tail))
	}
}

func TestFakeHelperApprovalDenyAndCancellation(t *testing.T) {
	fake := testFake(t, FakeConfig{CapabilityName: "delete", RequireApproval: true})
	created, err := fake.StartConversation(context.Background(), StartRequest{RequestMetadata: testMetadata(), RunID: "run_deny", Message: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	approval := ""
	events := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	for _, event := range events {
		if event.Type == EventApprovalRequired {
			approval = event.ApprovalID
		}
	}
	deny := ApprovalRequest{RequestMetadata: testMetadata(), PrivateSessionID: created.PrivateSessionID, ContinuationToken: created.ContinuationToken, RunID: created.RunID, ApprovalID: approval, Decision: ApprovalDeny}
	if err := fake.ResolveApproval(context.Background(), deny); err != nil {
		t.Fatal(err)
	}
	denyEvents := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	if denyEvents[len(denyEvents)-1].Type != EventRunFailed {
		t.Fatalf("deny events=%+v", denyEvents)
	}

	turn, err := fake.SendTurn(context.Background(), TurnRequest{RequestMetadata: testMetadata(), PrivateSessionID: created.PrivateSessionID, ContinuationToken: created.ContinuationToken, RunID: "run_cancel", Message: "cancel me"})
	if err != nil || turn.RunID != "run_cancel" {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	cancel := CancelRequest{RequestMetadata: testMetadata(), PrivateSessionID: created.PrivateSessionID, ContinuationToken: created.ContinuationToken, RunID: turn.RunID}
	if err := fake.CancelRun(context.Background(), cancel); err != nil {
		t.Fatal(err)
	}
	beforeRetry := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	if err := fake.CancelRun(context.Background(), cancel); !errors.Is(err, ErrTerminalRun) {
		t.Fatalf("second cancellation error=%v", err)
	}
	afterRetry := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	if len(afterRetry) != len(beforeRetry) {
		t.Fatalf("terminal cancellation appended event: before=%d after=%d", len(beforeRetry), len(afterRetry))
	}
	cancelled := 0
	for _, event := range afterRetry {
		if event.Type == EventRunCancelled && event.RunID == turn.RunID {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Fatalf("cancelled events=%d", cancelled)
	}
}

func TestFakeHelperRejectsWrongContinuationAndApprovalRun(t *testing.T) {
	fake := testFake(t, FakeConfig{CapabilityName: "write", RequireApproval: true})
	created, err := fake.StartConversation(context.Background(), StartRequest{RequestMetadata: testMetadata(), RunID: "run-one", Message: "write"})
	if err != nil {
		t.Fatal(err)
	}
	wrongStream := streamRequest(created.PrivateSessionID, "wrong-token", 0)
	if _, err := fake.StreamEvents(context.Background(), wrongStream); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong continuation error=%v", err)
	}
	events := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	var approval string
	for _, event := range events {
		if event.Type == EventApprovalRequired {
			approval = event.ApprovalID
		}
	}
	wrongRun := ApprovalRequest{RequestMetadata: testMetadata(), PrivateSessionID: created.PrivateSessionID, ContinuationToken: created.ContinuationToken, RunID: "other-run", ApprovalID: approval, Decision: ApprovalAllow}
	if err := fake.ResolveApproval(context.Background(), wrongRun); !errors.Is(err, ErrApproval) {
		t.Fatalf("wrong approval run error=%v", err)
	}
}

func TestFakeHelperCrashRestartUnavailableAndMalformed(t *testing.T) {
	fake := testFake(t, FakeConfig{Text: "hello"})
	created, err := fake.StartConversation(context.Background(), StartRequest{RequestMetadata: testMetadata(), RunID: "run_restart", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	before := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	fake.Crash()
	health, _ := fake.Health(context.Background())
	if health.Ready {
		t.Fatal("crashed helper reports ready")
	}
	if _, err := fake.SendTurn(context.Background(), TurnRequest{RequestMetadata: testMetadata(), PrivateSessionID: created.PrivateSessionID, ContinuationToken: created.ContinuationToken, RunID: "run_unavailable", Message: "nope"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("crash send error=%v", err)
	}
	if err := fake.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, before[len(before)-1].Sequence))
	if len(after) != 1 || after[0].Type != EventRuntimeRestarting {
		t.Fatalf("restart events=%+v", after)
	}
	if err := after[0].Validate(); err != nil {
		t.Fatalf("restart event validation: %v", err)
	}
	fake.InjectMalformedEvent(assistantcontrol.Event{})
	if _, err := fake.StreamEvents(context.Background(), streamRequest(created.PrivateSessionID, created.ContinuationToken, 0)); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("malformed stream error=%v", err)
	}
	fake.ClearMalformedEvents()
	fake.SetUnavailable()
	if _, err := fake.StreamEvents(context.Background(), streamRequest(created.PrivateSessionID, created.ContinuationToken, 0)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable stream error=%v", err)
	}
	fake.SetAvailable(true)
	_ = streamEvents(t, fake, streamRequest(created.PrivateSessionID, created.ContinuationToken, 0))
	stale := streamRequest(created.PrivateSessionID, created.ContinuationToken, 0)
	stale.CapabilityRevision = "old"
	if _, err := fake.StreamEvents(context.Background(), stale); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale revision error=%v", err)
	} else {
		var mismatch assistantcontrol.RevisionMismatchError
		if !errors.As(err, &mismatch) || mismatch.Field != "capability_revision" {
			t.Fatalf("stale revision detail=%v", err)
		}
	}
}

func TestFakeHelperConcurrentStreamsAndLauncher(t *testing.T) {
	fake := testFake(t, FakeConfig{Text: "parallel"})
	created, err := fake.StartConversation(context.Background(), StartRequest{RequestMetadata: testMetadata(), RunID: "run_parallel", Message: "parallel"})
	if err != nil {
		t.Fatal(err)
	}
	request := streamRequest(created.PrivateSessionID, created.ContinuationToken, 0)
	want := streamEvents(t, fake, request)
	const workers = 16
	results := make(chan []assistantcontrol.Event, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			stream, err := fake.StreamEvents(context.Background(), request)
			if err != nil {
				results <- nil
				return
			}
			defer stream.Close()
			encoded, err := io.ReadAll(stream)
			if err != nil {
				results <- nil
				return
			}
			var got []assistantcontrol.Event
			for _, line := range strings.Split(strings.TrimSpace(string(encoded)), "\n") {
				if line == "" {
					continue
				}
				event, err := assistantcontrol.ParseEvent([]byte(line))
				if err != nil {
					results <- nil
					return
				}
				got = append(got, event)
			}
			results <- got
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("concurrent stream differs: got=%+v want=%+v", result, want)
		}
	}

	launcher := NewFakeLauncher(FakeConfig{Available: true})
	client, process, err := launcher.Start(context.Background(), LaunchSpec{AssistantAddress: "support", RuntimeRevision: "runtime-launch", CapabilityRevision: "cap-launch"})
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() == 0 {
		t.Fatal("fake process has no pid")
	}
	health, err := client.Health(context.Background())
	if err != nil || health.RuntimeRevision != "runtime-launch" || health.CapabilityRevision != "cap-launch" {
		t.Fatalf("launched health=%+v err=%v", health, err)
	}
	fakeClient, ok := client.(*FakeHelper)
	if !ok {
		t.Fatalf("launched client type=%T", client)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- process.Wait() }()
	fakeClient.Crash()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake process did not observe crash")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFakeHelperZeroValueAndUnavailableConstructor(t *testing.T) {
	var zero FakeHelper
	if err := zero.Start(context.Background()); err != nil {
		t.Fatalf("zero-value start: %v", err)
	}
	if health, _ := zero.Health(context.Background()); !health.Ready {
		t.Fatal("zero-value helper is not ready")
	}
	unavailable := NewUnavailableFakeHelper()
	if err := unavailable.Start(context.Background()); err != nil {
		t.Fatalf("unavailable start: %v", err)
	}
	if health, _ := unavailable.Health(context.Background()); health.Ready {
		t.Fatal("unavailable helper reports ready")
	}
}
