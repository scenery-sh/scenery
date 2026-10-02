package runtime

import (
	"errors"
	"net/http"
	"time"

	"scenery.sh/internal/appsdk"
)

// RoundTrip preserves the caller's request and reports headers separately from
// streamed body completion. A transport created by the app can opt into this
// same wrapper through scenery.TraceHTTPTransport.
func (t *tracedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	state := stateFromContext(req.Context())
	if state == nil {
		state = currentState()
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if state == nil || state.trace == nil || !state.traceEnabled {
		return base.RoundTrip(req)
	}
	ctx, end := beginOperationTrace(req.Context(), "HTTP", state.request.Service, req.Method, map[string]any{"method": req.Method, "url": redactURL(req.URL)})
	request := req.Clone(ctx)
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("traceparent", traceParentForContext(ctx))
	// Do not forward vendor state belonging to a different caller-supplied trace.
	request.Header.Del("tracestate")
	started := time.Now()
	response, err := base.RoundTrip(request)
	if err != nil {
		end(err)
		return response, err
	}
	if response == nil {
		end(errors.New("HTTP transport returned no response"))
		return nil, nil
	}
	var statusErr error
	if response.StatusCode >= 400 {
		statusErr = errors.New(http.StatusText(response.StatusCode))
	}
	child := stateFromContext(ctx).trace
	reporter := activeReporter()
	if reporter != nil {
		emitOperationEvent(reporter, child, time.Now().UTC(), map[string]any{"http_headers": map[string]any{"status_code": response.StatusCode, "duration_nanos": uint64(time.Since(started))}})
	}
	finish := func(size int64, readErr error) {
		if reporter != nil {
			emitOperationEvent(reporter, child, time.Now().UTC(), map[string]any{"http_body": map[string]any{"bytes": size}})
		}
		end(errors.Join(statusErr, readErr))
	}
	if response.Body == nil || response.Body == http.NoBody {
		finish(0, nil)
	} else {
		response.Body = appsdk.ObserveReadCloser(ctx, response.Body, finish)
	}
	return response, nil
}

// traceHTTPTransport instruments a custom application transport. The default
// transport is instrumented automatically during development reporting.
func traceHTTPTransport(base http.RoundTripper) http.RoundTripper {
	if _, ok := base.(*tracedRoundTripper); ok {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
		if _, ok := base.(*tracedRoundTripper); ok {
			return base
		}
	}
	return &tracedRoundTripper{base: base, reporter: activeReporter()}
}
