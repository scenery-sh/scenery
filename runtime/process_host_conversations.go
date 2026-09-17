package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"scenery.sh/errs"
)

// An assistant run executes its tool calls in one application generation, the
// generation current when the host accepted the request that started the run
// (its create or turn request). The run scope holds that generation until an
// event stream of the conversation observes the run's terminal event, the next
// run of the conversation replaces it, or the supervisor forces the
// generation's retirement. An approval continues the scope of its run. A tool
// call of the conversation executes in the scope's generation; a tool call of a
// scope whose generation is gone fails as unavailable instead of executing a
// newer implementation set, and a conversation without a scope executes the
// current generation, which no answer attests.
//
// An event stream observes a conversation; it never selects a generation. It
// attests the generation its conversation's tool calls execute in when it
// opens, so overlapping streams attest the same generation. A stream whose
// attestation no longer describes its conversation ends: when a run of the
// conversation starts in another generation, or when the attested generation's
// retirement is forced. Its client resumes from its cursor under a new
// attestation.
//
// Scopes are journaled in the session's host state (see
// process_host_journal.go). A run keeps executing in its helper when a
// contract change replaces the host, so a replacement incarnation replays the
// scopes it did not create; their generations belong to an earlier
// incarnation and are never dispatchable again, so their tool calls fail until
// the conversation's next run.

// processHostConversationLimit bounds the run scopes a session retains; the
// oldest scope is released and forgotten first.
const processHostConversationLimit = 4096

// errAssistantStreamSuperseded ends an event stream whose attestation no longer
// describes its conversation.
var errAssistantStreamSuperseded = errors.New("assistant event stream attestation was superseded")

type processHostConversations struct {
	sync.Mutex
	scopes  map[string]*processHostRunScope
	order   []string
	streams map[string]map[*processHostConversationStream]struct{}
	journal processHostJournal
}

type processHostRunScope struct {
	runID  string
	number uint64
	// generation is the held generation, or nil for a scope an earlier host
	// incarnation created.
	generation *processHostGeneration
}

type processHostConversationStream struct {
	number uint64
	end    context.CancelCauseFunc
}

type processHostRunRecord struct {
	Key        string `json:"conversation"`
	RunID      string `json:"run_id"`
	Generation uint64 `json:"generation"`
}

func processHostConversationKey(assistantAddress, principal, conversationDigest string) string {
	return strings.TrimSpace(assistantAddress) + "\x00" + strings.TrimSpace(principal) + "\x00" + strings.TrimSpace(conversationDigest)
}

func activeAssistantProcessHost() *processHost {
	activeProcessHost.RLock()
	defer activeProcessHost.RUnlock()
	return activeProcessHost.host
}

// open replays the run scopes of earlier host incarnations and journals later
// runs.
func (conversations *processHostConversations) open(path string) error {
	conversations.Lock()
	defer conversations.Unlock()
	err := conversations.journal.replay(path, func(line []byte) {
		var record processHostRunRecord
		if json.Unmarshal(line, &record) == nil && record.Key != "" && record.RunID != "" && record.Generation != 0 {
			conversations.install(record.Key, &processHostRunScope{runID: record.RunID, number: record.Generation})
		}
	})
	if err == nil && conversations.journal.lines > processHostConversationLimit {
		conversations.compact()
	}
	return err
}

// install replaces the scope of key and returns the replaced one; the caller
// holds the lock and releases what the replaced scope holds.
func (conversations *processHostConversations) install(key string, scope *processHostRunScope) *processHostRunScope {
	if conversations.scopes == nil {
		conversations.scopes = map[string]*processHostRunScope{}
	}
	previous, exists := conversations.scopes[key]
	if !exists {
		if len(conversations.order) >= processHostConversationLimit {
			oldest := conversations.order[0]
			conversations.order = conversations.order[1:]
			conversations.scopes[oldest].release()
			delete(conversations.scopes, oldest)
		}
		conversations.order = append(conversations.order, key)
	}
	conversations.scopes[key] = scope
	return previous
}

// remove forgets the scope of key when it is still scope; the caller holds the
// lock.
func (conversations *processHostConversations) remove(key string, scope *processHostRunScope) {
	if conversations.scopes[key] != scope {
		return
	}
	delete(conversations.scopes, key)
	for index, candidate := range conversations.order {
		if candidate == key {
			conversations.order = append(conversations.order[:index], conversations.order[index+1:]...)
			break
		}
	}
	scope.release()
}

func (scope *processHostRunScope) release() {
	if scope != nil && scope.generation != nil {
		scope.generation.inFlight.Add(-1)
		scope.generation = nil
	}
}

// compact replaces the journal with the retained scopes; the caller holds the
// lock.
func (conversations *processHostConversations) compact() {
	records := make([]any, 0, len(conversations.order))
	for _, key := range conversations.order {
		scope := conversations.scopes[key]
		records = append(records, processHostRunRecord{Key: key, RunID: scope.runID, Generation: scope.number})
	}
	conversations.journal.rewrite(records)
}

// beginAssistantRun starts the scope of a run in the generation the host-local
// request req holds, or continues the run's scope when it already has one. The
// returned function reports whether the helper accepted the request: a refused
// run restores the conversation's previous scope. Outside a process host, or
// before a generation is published, it does nothing.
func beginAssistantRun(req *http.Request, assistantAddress, principal, conversationDigest, runID string) func(accepted bool) {
	host := activeAssistantProcessHost()
	if host == nil || req == nil {
		return func(bool) {}
	}
	generation, _ := req.Context().Value(processHostGenerationKey{}).(*processHostGeneration)
	if generation == nil {
		return func(bool) {}
	}
	key := processHostConversationKey(assistantAddress, principal, conversationDigest)
	conversations := &host.conversations
	conversations.Lock()
	if current := conversations.scopes[key]; current != nil && current.runID == runID {
		conversations.Unlock()
		return func(bool) {}
	}
	generation.inFlight.Add(1)
	scope := &processHostRunScope{runID: runID, number: generation.number, generation: generation}
	previous := conversations.install(key, scope)
	conversations.Unlock()
	var once sync.Once
	return func(accepted bool) {
		once.Do(func() {
			conversations.Lock()
			defer conversations.Unlock()
			if conversations.scopes[key] != scope {
				// A later run already replaced this one.
				if !accepted {
					scope.release()
				}
				previous.release()
				return
			}
			if !accepted {
				scope.release()
				if previous != nil {
					conversations.scopes[key] = previous
				} else {
					conversations.remove(key, scope)
				}
				return
			}
			previous.release()
			for stream := range conversations.streams[key] {
				if stream.number != scope.number {
					stream.end(errAssistantStreamSuperseded)
				}
			}
			conversations.journal.append(processHostRunRecord{Key: key, RunID: runID, Generation: scope.number})
			if conversations.journal.lines >= 2*processHostConversationLimit {
				conversations.compact()
			}
		})
	}
}

// endAssistantRun releases the scope of a run whose terminal event a stream of
// its conversation observed.
func endAssistantRun(assistantAddress, principal, conversationDigest, runID string) {
	host := activeAssistantProcessHost()
	if host == nil || runID == "" {
		return
	}
	key := processHostConversationKey(assistantAddress, principal, conversationDigest)
	host.conversations.Lock()
	defer host.conversations.Unlock()
	if scope := host.conversations.scopes[key]; scope != nil && scope.runID == runID {
		host.conversations.remove(key, scope)
	}
}

// attachAssistantStream attests, on the host-local event stream req, the
// generation its conversation's tool calls execute in, and returns the context
// the stream runs in, which ends with errAssistantStreamSuperseded when that
// attestation no longer holds. Outside a process host it returns req's context.
func attachAssistantStream(req *http.Request, assistantAddress, principal, conversationDigest string) (context.Context, func()) {
	host := activeAssistantProcessHost()
	if host == nil || req == nil {
		return req.Context(), func() {}
	}
	attested, _ := req.Context().Value(processHostGenerationKey{}).(*processHostGeneration)
	if attested == nil {
		return req.Context(), func() {}
	}
	key := processHostConversationKey(assistantAddress, principal, conversationDigest)
	ctx, end := context.WithCancelCause(req.Context())
	conversations := &host.conversations
	conversations.Lock()
	if scope := conversations.scopes[key]; scope != nil && scope.dispatchable() {
		attested = scope.generation
	}
	if writer, ok := req.Context().Value(processHostAttestationKey{}).(*processHostAttestingWriter); ok && !writer.attested {
		writer.generation = attested
	}
	stream := &processHostConversationStream{number: attested.number, end: end}
	if conversations.streams == nil {
		conversations.streams = map[string]map[*processHostConversationStream]struct{}{}
	}
	if conversations.streams[key] == nil {
		conversations.streams[key] = map[*processHostConversationStream]struct{}{}
	}
	conversations.streams[key][stream] = struct{}{}
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
		delete(conversations.streams[key], stream)
		if len(conversations.streams[key]) == 0 {
			delete(conversations.streams, key)
		}
	}
}

func (scope *processHostRunScope) dispatchable() bool {
	if scope.generation == nil {
		return false
	}
	select {
	case <-scope.generation.retired:
		return false
	default:
		return true
	}
}

// conversationGeneration returns the generation a tool call of the
// conversation executes in: its run scope's, 0 for the current one when it has
// no scope, or an unavailable failure when the scope's generation is gone.
func (h *processHost) conversationGeneration(key string) (uint64, error) {
	h.conversations.Lock()
	defer h.conversations.Unlock()
	scope := h.conversations.scopes[key]
	switch {
	case scope == nil:
		return 0, nil
	case scope.dispatchable():
		return scope.number, nil
	}
	return 0, &errs.Error{Code: errs.Unavailable, Message: fmt.Sprintf("the assistant run executes in application generation %d, which is no longer dispatchable", scope.number), Meta: errs.Metadata{"delivery": "not_sent"}}
}
