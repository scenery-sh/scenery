package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/authbridge"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

// An in-process internal call runs on the caller's goroutine, so the callee
// observes the caller's authentication, request metadata, SDK request and trace
// parent. A process-linked call carries exactly that state and the callee
// re-enters it for the duration of the dispatch. The request payload and Go
// types of unregistered authentication data do not cross the boundary.

type processLinkCallState struct {
	Generation   uint64                  `json:"generation,omitempty"`
	Auth         processLinkAuth         `json:"auth"`
	Request      processLinkRequestState `json:"request"`
	TraceID      string                  `json:"trace_id,omitempty"`
	SpanID       string                  `json:"span_id,omitempty"`
	LogsEnabled  bool                    `json:"logs_enabled"`
	TraceEnabled bool                    `json:"trace_enabled"`
}

type processLinkAuth struct {
	UID      string          `json:"uid,omitempty"`
	DataKind string          `json:"data_kind,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type processLinkRequestState struct {
	Type               shared.RequestType `json:"type"`
	Started            time.Time          `json:"started"`
	InvocationID       string             `json:"invocation_id,omitempty"`
	TraceID            string             `json:"trace_id,omitempty"`
	CallerBinding      string             `json:"caller_binding,omitempty"`
	ExecutionID        string             `json:"execution_id,omitempty"`
	Deployment         string             `json:"deployment,omitempty"`
	Locale             string             `json:"locale,omitempty"`
	Deadline           time.Time          `json:"deadline,omitzero"`
	API                *processLinkAPI    `json:"api,omitempty"`
	Service            string             `json:"service,omitempty"`
	Endpoint           string             `json:"endpoint,omitempty"`
	Path               string             `json:"path,omitempty"`
	PathParams         shared.PathParams  `json:"path_params,omitempty"`
	Method             string             `json:"method,omitempty"`
	Headers            http.Header        `json:"headers,omitempty"`
	CronIdempotencyKey string             `json:"cron_idempotency_key,omitempty"`
}

type processLinkAPI struct {
	Raw          bool `json:"raw"`
	Exposed      bool `json:"exposed"`
	AuthRequired bool `json:"auth_required"`
}

const processLinkJSONAuthData = "json"

// captureProcessLinkedCall reads the state an in-process callee would observe:
// authentication and request from the dispatching goroutine, and the trace
// parent from the call context, which carries an application child span.
func captureProcessLinkedCall(ctx context.Context) (*processLinkCallState, error) {
	requestSource := currentState()
	traceSource := stateFromContext(ctx)
	if requestSource == nil {
		requestSource = traceSource
	}
	if traceSource == nil {
		traceSource = requestSource
	}
	if requestSource == nil {
		return nil, nil
	}
	auth, err := encodeProcessLinkAuth(requestSource.auth)
	if err != nil {
		return nil, err
	}
	request := requestSource.request
	call := &processLinkCallState{Generation: requestSource.processGeneration, Auth: auth, LogsEnabled: requestSource.logsEnabled, TraceEnabled: requestSource.traceEnabled, Request: processLinkRequestState{
		Type: request.Type, Started: request.Started, InvocationID: request.InvocationID, TraceID: request.TraceID,
		CallerBinding: request.CallerBinding, ExecutionID: request.ExecutionID, Deployment: request.Deployment, Locale: request.Locale,
		Deadline: request.Deadline, Service: request.Service, Endpoint: request.Endpoint, Path: request.Path,
		PathParams: request.PathParams, Method: request.Method, Headers: request.Headers, CronIdempotencyKey: request.CronIdempotencyKey,
	}}
	if request.API != nil {
		call.Request.API = &processLinkAPI{Raw: request.API.Raw, Exposed: request.API.Exposed, AuthRequired: request.API.AuthRequired}
	}
	if traceSource.trace != nil {
		call.TraceID, call.SpanID = traceSource.trace.traceID, traceSource.trace.spanID
	}
	return call, nil
}

// enterProcessLinkedCall installs a forwarded call's invocation token and
// request state for the dispatching goroutine. The returned function restores
// the previous state.
func enterProcessLinkedCall(ctx context.Context, invocation runtimeapi.InvocationMetadata, call *processLinkCallState) (context.Context, func(), error) {
	ctx = runtimeapi.WithInvocation(ctx, runtimeapi.NewInvocationWithMetadata(invocation))
	if call == nil {
		return ctx, func() {}, nil
	}
	auth, err := decodeProcessLinkAuth(call.Auth)
	if err != nil {
		return ctx, func() {}, err
	}
	source := call.Request
	request := shared.Request{
		Type: source.Type, Started: source.Started, InvocationID: source.InvocationID, TraceID: source.TraceID,
		CallerBinding: source.CallerBinding, ExecutionID: source.ExecutionID, Deployment: source.Deployment, Locale: source.Locale,
		Deadline: source.Deadline, Service: source.Service, Endpoint: source.Endpoint, Path: source.Path,
		PathParams: source.PathParams, Method: source.Method, Headers: source.Headers, CronIdempotencyKey: source.CronIdempotencyKey,
	}
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}
	if source.API != nil {
		request.API = &shared.APIDesc{Raw: source.API.Raw, Exposed: source.API.Exposed, AuthRequired: source.API.AuthRequired}
	}
	state := &requestState{started: source.Started, request: request, auth: auth, logsEnabled: call.LogsEnabled, traceEnabled: call.TraceEnabled, processGeneration: call.Generation}
	if call.TraceID != "" && call.SpanID != "" {
		state.trace = &traceSpan{traceID: call.TraceID, spanID: call.SpanID, service: request.Service, endpoint: request.Endpoint, started: source.Started, requestType: request.Type}
	}
	return withState(ctx, state), enterState(state), nil
}

func encodeProcessLinkAuth(auth AuthInfo) (processLinkAuth, error) {
	encoded := processLinkAuth{UID: auth.UID}
	switch data := auth.Data.(type) {
	case nil:
		return encoded, nil
	case map[string]any:
		raw, err := json.Marshal(data)
		if err != nil {
			return encoded, fmt.Errorf("encode authentication data: %w", err)
		}
		encoded.DataKind, encoded.Data = processLinkJSONAuthData, raw
		return encoded, nil
	}
	kind, raw, ok, err := authbridge.EncodeData(auth.Data)
	if err != nil {
		return encoded, fmt.Errorf("encode authentication data: %w", err)
	}
	if !ok {
		return encoded, fmt.Errorf("authentication data %T cannot cross a process boundary", auth.Data)
	}
	encoded.DataKind, encoded.Data = kind, raw
	return encoded, nil
}

func decodeProcessLinkAuth(encoded processLinkAuth) (AuthInfo, error) {
	auth := AuthInfo{UID: encoded.UID}
	switch encoded.DataKind {
	case "":
		if len(encoded.Data) != 0 {
			return auth, fmt.Errorf("authentication data has no kind")
		}
		return auth, nil
	case processLinkJSONAuthData:
		var data map[string]any
		if err := json.Unmarshal(encoded.Data, &data); err != nil || data == nil {
			return auth, fmt.Errorf("decode authentication data: %v", err)
		}
		auth.Data = data
		return auth, nil
	}
	data, err := authbridge.DecodeData(encoded.DataKind, encoded.Data)
	if err != nil {
		return auth, fmt.Errorf("decode authentication data %s: %w", encoded.DataKind, err)
	}
	auth.Data = data
	return auth, nil
}

// processLinkError is the supported failure identity of a forwarded call:
// transport outcomes, typed errs failures, cancellation and deadline expiry.
// Other Go errors keep only their message.
type processLinkError struct {
	Kind    string            `json:"kind"`
	Outcome string            `json:"outcome,omitempty"`
	Status  int               `json:"status,omitempty"`
	Code    errs.ErrCode      `json:"code,omitempty"`
	Message string            `json:"message"`
	Meta    errs.Metadata     `json:"meta,omitempty"`
	Cause   *processLinkError `json:"cause,omitempty"`
}

func newProcessLinkError(err error) *processLinkError {
	if err == nil {
		return nil
	}
	transport, isTransport := errors.AsType[*ContractTransportError](err)
	typed, isErrs := errs.As(err)
	if isTransport && isErrs {
		// Encode the outer typed failure; the inner one is part of its cause chain.
		isErrs = !errors.Is(transport, typed)
		isTransport = !isErrs
	}
	switch {
	case isTransport:
		return &processLinkError{Kind: "transport", Outcome: transport.Outcome, Status: transport.Status, Message: transport.Message, Cause: newProcessLinkError(transport.Cause)}
	case isErrs:
		return &processLinkError{Kind: "errs", Code: typed.Code, Message: typed.Message, Meta: typed.Meta, Cause: newProcessLinkError(typed.Cause)}
	case errors.Is(err, context.DeadlineExceeded):
		return &processLinkError{Kind: "deadline_exceeded", Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &processLinkError{Kind: "canceled", Message: err.Error()}
	default:
		return &processLinkError{Kind: "error", Message: err.Error()}
	}
}

func (encoded *processLinkError) err() error {
	if encoded == nil {
		return nil
	}
	switch encoded.Kind {
	case "transport":
		return &ContractTransportError{Outcome: encoded.Outcome, Status: encoded.Status, Message: encoded.Message, Cause: encoded.Cause.err()}
	case "errs":
		return &errs.Error{Code: encoded.Code, Message: encoded.Message, Meta: encoded.Meta, Cause: encoded.Cause.err()}
	case "deadline_exceeded":
		return &processLinkSentinelError{message: encoded.Message, sentinel: context.DeadlineExceeded}
	case "canceled":
		return &processLinkSentinelError{message: encoded.Message, sentinel: context.Canceled}
	default:
		return errors.New(encoded.Message)
	}
}

// processLinkSentinelError keeps a remote cancellation or deadline failure
// matchable with errors.Is while preserving the callee's message.
type processLinkSentinelError struct {
	message  string
	sentinel error
}

func (e *processLinkSentinelError) Error() string { return e.message }

func (e *processLinkSentinelError) Unwrap() error { return e.sentinel }

type processGenerationKey struct{}

// withProcessGeneration moves the generation a process host assigned to a
// forwarded request out of the application-visible headers and into the
// request context. Only linked service processes accept it.
func withProcessGeneration(next http.Handler) http.Handler {
	if !processLinkConfigured() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		value := req.Header.Get(processGenerationHeader)
		if value == "" {
			next.ServeHTTP(w, req)
			return
		}
		req.Header.Del(processGenerationHeader)
		generation, err := strconv.ParseUint(value, 10, 64)
		if err != nil || generation == 0 {
			http.Error(w, "invalid process generation", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), processGenerationKey{}, generation)))
	})
}
