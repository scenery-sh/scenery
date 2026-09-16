package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"scenery.sh/errs"
)

// Background work of a service process (an event delivery attempt, a scheduled
// run, a durable task attempt) enters no request the host pinned. Each attempt
// is admitted to the newest published generation that includes the process
// instance running it, when the attempt starts rather than when its work was
// created, and its internal calls and their descendants are pinned to that
// generation as a forwarded request's are. The admission request stays open
// for the attempt's lifetime and holds the generation as in-flight work, so the
// generation stays dispatchable until the attempt ends, the process exits, or
// the supervisor forces the generation's retirement.
const (
	processAdmissionsPath   = "/__scenery/process/v1/admissions"
	processAdmissionTimeout = 5 * time.Second
)

// admit holds the newest generation that includes the instance named by the
// identity headers, or returns nil when no published generation includes it.
func (h *processHost) admit(identity http.Header) *processHostGeneration {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var admitted *processHostGeneration
	for number, generation := range h.generations {
		if admitted != nil && number < admitted.number {
			continue
		}
		for _, instance := range generation.instances {
			if processIdentityMatches(identity, instance.spec) {
				admitted = generation
				break
			}
		}
	}
	if admitted != nil {
		admitted.inFlight.Add(1)
	}
	return admitted
}

// serveAdmission answers with the admitted generation and holds it until the
// attempt closes the request or the host stops.
func (h *processHost) serveAdmission(w http.ResponseWriter, req *http.Request) {
	generation := h.admit(req.Header)
	if generation == nil {
		writeProcessLinkResponse(w, http.StatusServiceUnavailable, processLinkResponse{Error: &processLinkError{
			Kind: "errs", Code: errs.Unavailable, Message: "no published application generation includes this service process instance", Meta: errs.Metadata{"delivery": "not_sent"},
		}})
		return
	}
	defer generation.inFlight.Add(-1)
	w.Header().Set(processGenerationHeader, strconv.FormatUint(generation.number, 10))
	w.WriteHeader(http.StatusOK)
	if err := http.NewResponseController(w).Flush(); err != nil {
		return
	}
	select {
	case <-req.Context().Done():
	case <-h.closing:
	}
}

// admitProcessGeneration admits one background attempt of this process. It
// returns the generation the attempt's internal calls are pinned to and the
// release that ends the admission; a runtime without a process link runs
// unpinned. An attempt that is not admitted must not run: its failure is
// unavailable and names whether the host refused it.
func admitProcessGeneration(ctx context.Context) (uint64, func(), error) {
	config, err := currentProcessLink()
	if err != nil {
		return 0, nil, err
	}
	if config == nil {
		return 0, func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The admission outlives the attempt's own cancellation: work that ignores
	// cancellation keeps its generation until the attempt returns.
	scope, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopFollowing := context.AfterFunc(ctx, cancel)
	timeout := time.AfterFunc(processAdmissionTimeout, cancel)
	request, err := http.NewRequestWithContext(scope, http.MethodPost, "http://scenery-process"+processAdmissionsPath, nil)
	if err != nil {
		cancel()
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)
	setProcessIdentityHeaders(request.Header)
	response, err := processLinkClient(config.Dispatch).Do(request)
	following, timely := stopFollowing(), timeout.Stop()
	fail := func(cause error) (uint64, func(), error) {
		cancel()
		if response != nil {
			_ = response.Body.Close()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		if typed, ok := errs.As(cause); ok {
			return 0, nil, typed
		}
		return 0, nil, &errs.Error{Code: errs.Unavailable, Message: "background work was not admitted to an application generation", Meta: errs.Metadata{"delivery": "not_sent"}, Cause: cause}
	}
	switch {
	case err != nil:
		return fail(err)
	case !following || !timely:
		return fail(fmt.Errorf("generation admission was not answered in time"))
	case response.StatusCode != http.StatusOK:
		var decoded processLinkResponse
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&decoded) == nil && decoded.Error != nil {
			return fail(decoded.Error.err())
		}
		return fail(fmt.Errorf("generation admission answered HTTP %d", response.StatusCode))
	}
	generation, err := strconv.ParseUint(response.Header.Get(processGenerationHeader), 10, 64)
	if err != nil || generation == 0 {
		return fail(fmt.Errorf("generation admission named no generation"))
	}
	var once sync.Once
	return generation, func() {
		once.Do(func() {
			cancel()
			_ = response.Body.Close()
		})
	}, nil
}
