package host

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/runtimeapp"
	"scenery.sh/internal/runtimescope"
	"scenery.sh/runtime/shared"
)

type requestStateKey struct{}

type requestState struct {
	started      time.Time
	request      shared.Request
	auth         AuthInfo
	trace        *traceSpan
	startLogged  bool
	logsEnabled  bool
	traceEnabled bool
}

var stateStore runtimescope.Scope[*requestState]

func CurrentRequest() *shared.Request {
	state := currentState()
	if state == nil {
		return &shared.Request{Type: shared.None}
	}
	req := state.request
	return &req
}

func CurrentAuth() *AuthInfo {
	state := currentState()
	if state == nil {
		return nil
	}
	auth := state.auth
	return &auth
}

func WithAuthContext(ctx context.Context, auth AuthInfo) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if state := stateFromContext(ctx); state != nil {
		clone := *state
		clone.auth = auth
		return withState(ctx, &clone)
	}
	return withState(ctx, &requestState{
		started: time.Now(),
		request: shared.Request{
			Type:    shared.InternalCall,
			Headers: make(http.Header),
		},
		auth:         auth,
		logsEnabled:  true,
		traceEnabled: true,
	})
}

func stateFromContext(ctx context.Context) *requestState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(requestStateKey{}).(*requestState)
	if state == nil {
		if auth, started, ok := runtimeapp.AuthContextValue(ctx); ok {
			return &requestState{started: started, auth: auth,
				request:     shared.Request{Type: shared.InternalCall, Headers: make(http.Header)},
				logsEnabled: true, traceEnabled: true}
		}
	}
	return state
}

func withState(ctx context.Context, state *requestState) context.Context {
	return context.WithValue(ctx, requestStateKey{}, state)
}

func withRuntimeInvocation(ctx context.Context, state *requestState) context.Context {
	if current, ok := runtimeapi.InvocationFromContext(ctx); ok && current.Valid() {
		return ctx
	}
	if state == nil {
		return ctx
	}
	id := state.request.InvocationID
	if id == "" {
		id = uuid.NewString()
		state.request.InvocationID = id
	}
	tenantID := ""
	if claims, ok := state.auth.Data.(map[string]any); ok {
		tenantID, _ = claims["tenant_id"].(string)
	}
	deadline := state.request.Deadline
	if deadline.IsZero() {
		deadline, _ = ctx.Deadline()
	}
	return runtimeapi.WithInvocation(ctx, runtimeapi.NewInvocationWithMetadata(runtimeapi.InvocationMetadata{
		ID: id, Principal: state.auth.UID, TenantID: tenantID, TraceID: state.request.TraceID,
		Deadline: deadline, CallerBinding: state.request.CallerBinding,
		ExecutionID: state.request.ExecutionID, Deployment: state.request.Deployment,
		Locale: state.request.Locale,
	}))
}

func currentState() *requestState {
	state, _ := stateStore.Current()
	return state
}

func enterState(state *requestState) func() { return stateStore.Enter(state) }

func newExternalState(ep *Endpoint, req *http.Request, path shared.PathParams, payload any, auth AuthInfo) *requestState {
	requestType := shared.APICall
	if ep.Raw {
		requestType = shared.RawAPICall
	}
	started := time.Now()
	request := shared.Request{
		Type:         requestType,
		Started:      started,
		InvocationID: uuid.NewString(),
		Service:      ep.Service,
		Endpoint:     ep.Name,
		Method:       req.Method,
		Path:         req.URL.Path,
		PathParams:   path,
		Headers:      req.Header.Clone(),
		Payload:      payload,
		API: &shared.APIDesc{
			Raw:          ep.Raw,
			Exposed:      ep.Access != Private,
			AuthRequired: ep.Access == Auth,
		},
	}
	if ep.ContractPolicy != nil {
		request.CallerBinding = ep.ContractPolicy.BindingAddress
	}
	if deadline, ok := req.Context().Deadline(); ok {
		request.Deadline = deadline.UTC()
	}
	return &requestState{
		started:      started,
		request:      request,
		auth:         auth,
		logsEnabled:  logsEnabledForRequest(request),
		traceEnabled: traceEnabledForRequest(request),
	}
}
