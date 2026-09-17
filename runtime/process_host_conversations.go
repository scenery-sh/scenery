package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
)

// An assistant run executes its tool calls in one application generation: the
// generation current when the host accepted the create or turn request that
// starts it. The host reserves the run in that generation before it contacts
// the helper, so a tool call the run makes while its start is in progress is
// scoped; the reservation affects no other run. A tool call names its run (the
// helper attributes it, see the Eve connection template) and executes only in
// that run's generation:
//
//   - pending: the start request is in progress; its tool calls execute.
//   - active: the helper accepted the run.
//   - unknown: the start request's outcome is unknown (the helper may have
//     started the run and lost its answer); its tool calls execute until the
//     host observes the run's events.
//   - revoked: an unknown run whose events the host did not observe within
//     assistantRunUnknownTimeout. Its tool calls are refused from then on, and
//     whether it started or had effects stays unknown; the host neither treats
//     it as rejected nor repeats its start. A revoked run is journaled.
//   - ended: the helper rejected the run, or the host observed its terminal
//     event. The host forgets an ended run.
//
// A run holds its generation until it ends. A run whose generation's
// retirement is forced, or that an earlier host incarnation started, is
// revoked: its tool calls fail as unavailable. A tool call that names no run,
// or a run the host does not know or has ended, fails the same way; it never
// executes the current generation instead. An approval continues its run and
// changes no scope.
//
// Run starts and ends are committed to the session's run journal (see
// process_host_journal.go) before the host acts on them, and a replacement host
// replays the runs that had not ended as revoked. The run limit bounds live
// runs: an ended or revoked run is forgotten to make room, and a start beyond
// the limit is refused, never admitted by forgetting a live run. Forgetting a
// run never makes a call of it acceptable again: a call of a run the host does
// not know is refused, and a run ID is never reserved twice.
//
// The host observes each run's end from the helper's private event stream (see
// observeAssistantRun), not from client streams. An event stream a client
// opens observes its conversation and never selects a generation: it attests
// the generation of the conversation's latest accepted run while that run can
// execute (its own ingress generation otherwise), so overlapping streams agree,
// and it ends without a failure event when a later run of the conversation is
// accepted in another generation or its attested generation's retirement is
// forced, so its client resumes from its cursor under the new attestation.

const (
	// processHostRunLimit bounds the runs a session retains.
	processHostRunLimit = 4096
)

// assistantRunUnknownTimeout bounds how long a run whose start outcome is
// unknown executes without the host observing any of its events. Tests replace
// it to observe the bound without waiting for it.
var assistantRunUnknownTimeout = 30 * time.Second

type assistantRunState int

const (
	assistantRunPending assistantRunState = iota
	assistantRunActive
	assistantRunUnknown
)

// assistantRunOutcome is the helper's answer to a request that starts a run.
type assistantRunOutcome int

const (
	assistantRunAccepted assistantRunOutcome = iota
	assistantRunRejected
	assistantRunOutcomeUnknown
)

// errAssistantStreamSuperseded ends an event stream whose attestation no longer
// describes its conversation.
var errAssistantStreamSuperseded = errors.New("assistant event stream attestation was superseded")

type processHostConversations struct {
	sync.Mutex
	runs     map[string]*processHostRun
	order    []string
	sequence uint64
	latest   map[string]*processHostRun
	streams  map[string]map[*processHostConversationStream]struct{}
	journal  processHostJournal
}

type processHostRun struct {
	conversation string
	runID        string
	number       uint64
	// generation is the held generation, or nil for a run an earlier host
	// incarnation started.
	generation *processHostGeneration
	state      assistantRunState
	sequence   uint64
	// revoked marks a run whose start outcome stayed unknown.
	revoked bool
	// ended is closed when the run stops holding its generation (it is
	// revoked or forgotten); wake asks its observer to read the run's events
	// now.
	ended  chan struct{}
	closed bool
	wake   chan struct{}
}

type processHostConversationStream struct {
	number uint64
	end    context.CancelCauseFunc
}

type processHostRunRecord struct {
	Conversation string `json:"conversation"`
	RunID        string `json:"run_id"`
	Generation   uint64 `json:"generation,omitempty"`
	Ended        bool   `json:"ended,omitempty"`
	Revoked      bool   `json:"revoked,omitempty"`
}

func processHostConversationKey(assistantAddress, principal, conversationDigest string) string {
	return strings.TrimSpace(assistantAddress) + "\x00" + strings.TrimSpace(principal) + "\x00" + strings.TrimSpace(conversationDigest)
}

func processHostRunKey(conversation, runID string) string {
	return conversation + "\x00" + runID
}

func activeAssistantProcessHost() *processHost {
	activeProcessHost.RLock()
	defer activeProcessHost.RUnlock()
	return activeProcessHost.host
}

// open replays the runs of earlier host incarnations, revoked, and journals
// later runs.
func (conversations *processHostConversations) open(path string) error {
	conversations.Lock()
	defer conversations.Unlock()
	conversations.runs, conversations.order = map[string]*processHostRun{}, nil
	err := conversations.journal.replay(path, func(line []byte) error {
		var record processHostRunRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if record.Conversation == "" || record.RunID == "" || !record.Ended && !record.Revoked && record.Generation == 0 {
			return errors.New("run record is incomplete")
		}
		key := processHostRunKey(record.Conversation, record.RunID)
		if record.Ended {
			conversations.forget(key)
			return nil
		}
		if record.Revoked {
			if run := conversations.runs[key]; run != nil {
				run.revoked = true
			}
			return nil
		}
		if conversations.runs[key] == nil {
			conversations.install(key, &processHostRun{conversation: record.Conversation, runID: record.RunID, number: record.Generation, ended: make(chan struct{})})
		}
		return nil
	})
	if err == nil && conversations.journal.lines > processHostRunLimit {
		conversations.compact()
	}
	return err
}

// install adds a run; the caller holds the lock and has made room.
func (conversations *processHostConversations) install(key string, run *processHostRun) {
	if conversations.runs == nil {
		conversations.runs = map[string]*processHostRun{}
	}
	conversations.sequence++
	run.sequence = conversations.sequence
	run.wake = make(chan struct{}, 1)
	conversations.runs[key] = run
	conversations.order = append(conversations.order, key)
}

// forget removes a run, releases its generation and ends its observers; the
// caller holds the lock and has committed the end.
func (conversations *processHostConversations) forget(key string) {
	run := conversations.runs[key]
	if run == nil {
		return
	}
	delete(conversations.runs, key)
	for index, candidate := range conversations.order {
		if candidate == key {
			conversations.order = append(conversations.order[:index], conversations.order[index+1:]...)
			break
		}
	}
	if conversations.latest[run.conversation] == run {
		delete(conversations.latest, run.conversation)
	}
	run.release()
}

// release stops the run holding its generation and ends its observer; the
// caller holds the lock.
func (run *processHostRun) release() {
	if run.generation != nil {
		run.generation.inFlight.Add(-1)
		run.generation = nil
	}
	if !run.closed {
		run.closed = true
		close(run.ended)
	}
}

// end commits and forgets a run. A run whose end cannot be committed is still
// forgotten: the poisoned journal then authorizes no run.
func (conversations *processHostConversations) end(run *processHostRun) {
	key := processHostRunKey(run.conversation, run.runID)
	if conversations.runs[key] != run {
		return
	}
	_ = conversations.journal.append(processHostRunRecord{Conversation: run.conversation, RunID: run.runID, Ended: true})
	conversations.forget(key)
	if conversations.journal.lines >= 2*processHostRunLimit {
		conversations.compact()
	}
}

// room makes room for one run by ending the oldest run that can no longer
// execute; the caller holds the lock.
func (conversations *processHostConversations) room() bool {
	if len(conversations.runs) < processHostRunLimit {
		return true
	}
	for _, key := range conversations.order {
		if run := conversations.runs[key]; !run.dispatchable() {
			conversations.end(run)
			return true
		}
	}
	return false
}

// compact replaces the journal with the retained runs; the caller holds the
// lock.
func (conversations *processHostConversations) compact() {
	records := make([]any, 0, len(conversations.order))
	for _, key := range conversations.order {
		run := conversations.runs[key]
		records = append(records, processHostRunRecord{Conversation: run.conversation, RunID: run.runID, Generation: run.number})
		if run.revoked {
			records = append(records, processHostRunRecord{Conversation: run.conversation, RunID: run.runID, Revoked: true})
		}
	}
	_ = conversations.journal.rewrite(records)
}

func (run *processHostRun) dispatchable() bool {
	if run == nil || run.generation == nil {
		return false
	}
	select {
	case <-run.generation.retired:
		return false
	default:
		return true
	}
}

// assistantRunReservation is a run the host reserved for a start request.
type assistantRunReservation struct {
	host *processHost
	run  *processHostRun
}

// reserveAssistantRun commits a run in the generation the host-local request
// req holds before the start request reaches the helper. It returns nil outside
// a process host or before a generation is published, and an unavailable
// failure when the run cannot be committed or the session holds its run limit.
func reserveAssistantRun(req *http.Request, assistantAddress, principal, conversationDigest, runID string) (*assistantRunReservation, error) {
	host := activeAssistantProcessHost()
	if host == nil || req == nil {
		return nil, nil
	}
	generation, _ := req.Context().Value(processHostGenerationKey{}).(*processHostGeneration)
	if generation == nil {
		return nil, nil
	}
	conversation := processHostConversationKey(assistantAddress, principal, conversationDigest)
	key := processHostRunKey(conversation, runID)
	if host.quiescing.Load() {
		return nil, errProcessHostQuiescing
	}
	conversations := &host.conversations
	conversations.Lock()
	defer conversations.Unlock()
	if conversations.journal.poisoned != nil {
		return nil, conversations.journal.unavailable()
	}
	if conversations.runs[key] != nil {
		return nil, &errs.Error{Code: errs.Unavailable, Message: "assistant run is already started", Meta: errs.Metadata{"delivery": "not_sent"}}
	}
	if !conversations.room() {
		return nil, &errs.Error{Code: errs.Unavailable, Message: "the session holds its limit of running assistant runs", Meta: errs.Metadata{"delivery": "not_sent"}}
	}
	if err := conversations.journal.append(processHostRunRecord{Conversation: conversation, RunID: runID, Generation: generation.number}); err != nil {
		return nil, err
	}
	generation.inFlight.Add(1)
	run := &processHostRun{conversation: conversation, runID: runID, number: generation.number, generation: generation, state: assistantRunPending, ended: make(chan struct{})}
	conversations.install(key, run)
	return &assistantRunReservation{host: host, run: run}, nil
}

// finish records the helper's answer to the start request.
func (reservation *assistantRunReservation) finish(outcome assistantRunOutcome) {
	if reservation == nil {
		return
	}
	conversations := &reservation.host.conversations
	conversations.Lock()
	defer conversations.Unlock()
	run := reservation.run
	if conversations.runs[processHostRunKey(run.conversation, run.runID)] != run {
		return
	}
	switch outcome {
	case assistantRunRejected:
		conversations.end(run)
	case assistantRunOutcomeUnknown:
		run.state = assistantRunUnknown
	case assistantRunAccepted:
		run.state = assistantRunActive
		conversations.accept(run)
	}
}

// accept makes run the conversation's latest accepted run and ends the streams
// whose attestation it supersedes; the caller holds the lock.
func (conversations *processHostConversations) accept(run *processHostRun) {
	if latest := conversations.latest[run.conversation]; latest != nil && latest.sequence > run.sequence {
		return
	}
	if conversations.latest == nil {
		conversations.latest = map[string]*processHostRun{}
	}
	conversations.latest[run.conversation] = run
	for stream := range conversations.streams[run.conversation] {
		if stream.number != run.number {
			stream.end(errAssistantStreamSuperseded)
		}
	}
}

// observed records that the host observed an event of the run: an unknown
// start is then known to have started, and a terminal event ends the run.
func (reservation *assistantRunReservation) observed(terminal bool) {
	conversations := &reservation.host.conversations
	conversations.Lock()
	defer conversations.Unlock()
	run := reservation.run
	if conversations.runs[processHostRunKey(run.conversation, run.runID)] != run {
		return
	}
	if run.revoked {
		// A revoked run stays revoked: its outcome was not known in time.
		return
	}
	if terminal {
		conversations.end(run)
		return
	}
	if run.state == assistantRunUnknown {
		run.state = assistantRunActive
		conversations.accept(run)
	}
}

// expire ends a run whose start outcome is still unknown.
func (reservation *assistantRunReservation) expire() {
	conversations := &reservation.host.conversations
	conversations.Lock()
	defer conversations.Unlock()
	run := reservation.run
	if run.state != assistantRunUnknown || run.revoked || conversations.runs[processHostRunKey(run.conversation, run.runID)] != run {
		return
	}
	// A revocation that cannot be committed still revokes the run: the
	// poisoned journal then authorizes no run.
	_ = conversations.journal.append(processHostRunRecord{Conversation: run.conversation, RunID: run.runID, Revoked: true})
	run.revoked = true
	if conversations.latest[run.conversation] == run {
		delete(conversations.latest, run.conversation)
	}
	run.release()
}

// wakeAssistantRun asks the observer of a run to read its events now, after a
// request that can end the run, such as an approval or a cancellation.
func wakeAssistantRun(assistantAddress, principal, conversationDigest, runID string) {
	host := activeAssistantProcessHost()
	if host == nil {
		return
	}
	host.conversations.Lock()
	defer host.conversations.Unlock()
	if run := host.conversations.runs[processHostRunKey(processHostConversationKey(assistantAddress, principal, conversationDigest), runID)]; run != nil {
		select {
		case run.wake <- struct{}{}:
		default:
		}
	}
}

// runGeneration returns the generation a tool call executes in: its run's. A
// call outside any conversation, which no assistant gateway makes, executes
// the current generation.
func (h *processHost) runGeneration(call MCPToolCallContext) (uint64, error) {
	if strings.TrimSpace(call.ConversationDigest) == "" && strings.TrimSpace(call.RunID) == "" {
		return 0, nil
	}
	h.conversations.Lock()
	defer h.conversations.Unlock()
	if h.conversations.journal.poisoned != nil {
		return 0, h.conversations.journal.unavailable()
	}
	conversation := processHostConversationKey(call.AssistantAddress, call.Principal, call.ConversationDigest)
	run := h.conversations.runs[processHostRunKey(conversation, strings.TrimSpace(call.RunID))]
	switch {
	case strings.TrimSpace(call.RunID) == "":
		return 0, &errs.Error{Code: errs.InvalidArgument, Message: "the assistant tool call names no run", Meta: errs.Metadata{"delivery": "not_sent"}}
	case run == nil:
		return 0, &errs.Error{Code: errs.Unavailable, Message: "the assistant run is not running", Meta: errs.Metadata{"delivery": "not_sent"}}
	case run.revoked:
		return 0, &errs.Error{Code: errs.Unavailable, Message: "the assistant run was revoked because its start outcome stayed unknown; whether it had effects is unknown", Meta: errs.Metadata{"delivery": "not_sent"}}
	case !run.dispatchable():
		return 0, &errs.Error{Code: errs.Unavailable, Message: fmt.Sprintf("the assistant run executes in application generation %d, which is no longer dispatchable", run.number), Meta: errs.Metadata{"delivery": "not_sent"}}
	}
	return run.number, nil
}

// attachAssistantStream attests, on the host-local event stream req, the
// generation of its conversation's latest accepted run, and returns the
// context the stream runs in, which ends with errAssistantStreamSuperseded when
// that attestation no longer holds. Outside a process host it returns req's
// context.
func attachAssistantStream(req *http.Request, assistantAddress, principal, conversationDigest string) (context.Context, func()) {
	host := activeAssistantProcessHost()
	if host == nil || req == nil {
		return req.Context(), func() {}
	}
	attested, _ := req.Context().Value(processHostGenerationKey{}).(*processHostGeneration)
	if attested == nil {
		return req.Context(), func() {}
	}
	conversation := processHostConversationKey(assistantAddress, principal, conversationDigest)
	ctx, end := context.WithCancelCause(req.Context())
	conversations := &host.conversations
	conversations.Lock()
	if latest := conversations.latest[conversation]; latest.dispatchable() {
		attested = latest.generation
	}
	if writer, ok := req.Context().Value(processHostAttestationKey{}).(*processHostAttestingWriter); ok && !writer.attested {
		writer.generation = attested
	}
	stream := &processHostConversationStream{number: attested.number, end: end}
	if conversations.streams == nil {
		conversations.streams = map[string]map[*processHostConversationStream]struct{}{}
	}
	if conversations.streams[conversation] == nil {
		conversations.streams[conversation] = map[*processHostConversationStream]struct{}{}
	}
	conversations.streams[conversation][stream] = struct{}{}
	conversations.Unlock()
	go func() {
		select {
		case <-attested.retired:
			end(errAssistantStreamSuperseded)
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		end(context.Canceled)
		conversations.Lock()
		defer conversations.Unlock()
		delete(conversations.streams[conversation], stream)
		if len(conversations.streams[conversation]) == 0 {
			delete(conversations.streams, conversation)
		}
	}
}
