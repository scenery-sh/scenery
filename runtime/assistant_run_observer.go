package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"scenery.sh/internal/assistantcontrol"
	"scenery.sh/internal/assistantruntime"
)

// assistantRunObservePoll is the delay between two reads of a run's private
// events while it has not ended.
const assistantRunObservePoll = 250 * time.Millisecond

// assistantRunObserverRequestPrefix names the private event reads of the host's
// own run observer.
const assistantRunObserverRequestPrefix = "observe"

// assistantRunStartOutcome classifies the failure of a request that starts a
// run. A request the helper never received, or that the helper answered with a
// refusal, rejected the run; a request whose answer was lost or unreadable may
// have started it.
func assistantRunStartOutcome(sent bool, err error) assistantRunOutcome {
	if !sent {
		return assistantRunRejected
	}
	var refused *assistantruntime.ControlError
	switch {
	case errors.As(err, &refused),
		errors.Is(err, assistantruntime.ErrHelperRequest),
		errors.Is(err, assistantruntime.ErrInvalidRequest),
		errors.Is(err, assistantruntime.ErrRequestTooLarge),
		errors.Is(err, assistantruntime.ErrInvalidControlAddress),
		errors.Is(err, assistantruntime.ErrRedirectRejected),
		errors.Is(err, assistantruntime.ErrRevisionMismatch),
		errors.Is(err, assistantruntime.ErrStopped),
		errors.Is(err, assistantruntime.ErrNotStarted),
		errors.Is(err, assistantruntime.ErrConversation):
		return assistantRunRejected
	}
	return assistantRunOutcomeUnknown
}

// reserveRun reserves a run for a start request; a run the host cannot commit
// is refused as unavailable before the helper sees the request.
func (g *assistantGateway) reserveRun(req *http.Request, principal, conversationDigest, runID string) (*assistantRunReservation, error) {
	reservation, err := reserveAssistantRun(req, g.registration.AssistantAddress, principal, conversationDigest, runID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", assistantruntime.ErrUnavailable, err)
	}
	return reservation, nil
}

// observeAssistantRun follows a reserved run's private events until the host
// observes its terminal event, the run ends otherwise, or the host stops. A run
// whose start outcome is unknown and whose events the host never observes is
// revoked after assistantRunUnknownTimeout, which also ends the read in
// progress. request names the run's private session; without one only that
// bound applies.
func (g *assistantGateway) observeAssistantRun(reservation *assistantRunReservation, client assistantruntime.Client, request assistantruntime.StreamRequest) {
	if reservation == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	var seen atomic.Bool
	// The bound governs the read in progress: an open stream that never sends
	// an event must not outlast it.
	unknown := time.AfterFunc(assistantRunUnknownTimeout, func() {
		if !seen.Load() {
			reservation.expire()
			cancel()
		}
	})
	go func() {
		select {
		case <-reservation.run.ended:
		case <-reservation.host.closing:
		}
		cancel()
	}()
	go func() {
		defer cancel()
		defer unknown.Stop()
		poll := time.NewTicker(assistantRunObservePoll)
		defer poll.Stop()
		// observe records the run's events as they arrive, not when the read
		// ends, so an unknown start becomes known as soon as it is observed.
		observe := func(terminal bool) {
			if terminal || seen.CompareAndSwap(false, true) {
				reservation.observed(terminal)
			}
		}
		for {
			if client != nil && request.PrivateSessionID != "" {
				request.RequestID = assistantRunObserverRequestID()
				terminal, after := readAssistantRunEvents(ctx, client, request, reservation.run.runID, observe)
				request.After = after
				if terminal {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
			case <-reservation.run.wake:
			}
		}
	}()
}

// assistantRunObserverRequestID is a fresh request ID of the run observer. It
// draws its own randomness rather than the gateway's, which serves requests.
func assistantRunObserverRequestID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return assistantRunObserverRequestPrefix + "_" + hex.EncodeToString(value)
}

// readAssistantRunEvents reads the private events after request.After, calls
// observe for every event of runID, and returns whether the run's terminal
// event arrived and the cursor to resume after.
func readAssistantRunEvents(ctx context.Context, client assistantruntime.Client, request assistantruntime.StreamRequest, runID string, observe func(terminal bool)) (terminal bool, after uint64) {
	after = request.After
	stream, err := client.StreamEvents(ctx, request)
	if err != nil {
		return false, after
	}
	defer func() { _ = stream.Close() }()
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64<<10), 17<<20)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		event, err := assistantcontrol.ParseEvent(scanner.Bytes())
		if err != nil || event.Sequence <= after {
			return false, after
		}
		after = event.Sequence
		if event.RunID != runID {
			continue
		}
		switch event.Type {
		case assistantcontrol.EventRunCompleted, assistantcontrol.EventRunFailed, assistantcontrol.EventRunCancelled:
			observe(true)
			return true, after
		}
		observe(false)
	}
	return false, after
}
