package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"scenery.sh/auth"
	"scenery.sh/internal/nativecall"
	"scenery.sh/internal/nativeprotocol"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/runtimeapp"
	"scenery.sh/internal/runtimescope"
	"scenery.sh/runtime/shared"
)

type RequestContext = nativeprotocol.RequestContext
type SpanRecord = nativeprotocol.SpanRecord

type nativeState struct {
	request shared.Request
	auth    runtimeapp.AuthInfo
	mu      sync.Mutex
	spans   []SpanRecord
}

type stateKey struct{}

var scope runtimescope.Scope[*nativeState]

func init() {
	nativecall.Bind(func() *nativecall.Registry { return application.bindings })
	runtimeapp.Bind(runtimeapp.Provider{
		Meta: application.metadataSnapshot,
		CurrentRequest: func() *shared.Request {
			if state, ok := scope.Current(); ok {
				request := state.request
				return &request
			}
			return &shared.Request{Type: shared.None}
		},
		CurrentAuth: func() *runtimeapp.AuthInfo {
			if state, ok := scope.Current(); ok {
				auth := state.auth
				return &auth
			}
			return nil
		},
		WithAuthContext: func(ctx context.Context, auth runtimeapp.AuthInfo) context.Context {
			if ctx == nil {
				ctx = context.Background()
			}
			state := &nativeState{auth: auth, request: shared.Request{Type: shared.InternalCall}}
			if parent, ok := ctx.Value(stateKey{}).(*nativeState); ok {
				state.request = parent.request
			}
			return context.WithValue(ctx, stateKey{}, state)
		},
		StartSpan: startSpan,
	})
}

func startSpan(ctx context.Context, name string) (context.Context, *runtimeapp.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	state, _ := ctx.Value(stateKey{}).(*nativeState)
	if state == nil {
		state, _ = scope.Current()
	}
	started := time.Now()
	return ctx, runtimeapp.NewSpan(func(err error) {
		if state == nil {
			return
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		state.spans = append(state.spans, SpanRecord{Name: name, Started: started, Ended: time.Now(), Failed: err != nil})
	})
}

func enterRequest(ctx context.Context, metadata RequestContext, input any) (context.Context, *nativeState, func(), error) {
	if metadata.InvocationID == "" || metadata.CallerBinding == "" {
		return nil, nil, nil, fmt.Errorf("native request requires invocation and binding identities")
	}
	var data *auth.AuthData
	tenant := ""
	if metadata.Auth != nil {
		value := metadata.Auth
		if value.UserID != metadata.Principal {
			return nil, nil, nil, fmt.Errorf("native standard auth principal mismatch")
		}
		data = &auth.AuthData{UserID: auth.AuthUserID(value.UserID), TenantID: auth.TenantID(value.TenantID), SessionID: value.SessionID, ActorUserID: auth.AuthUserID(value.ActorUserID), ImpersonationID: value.ImpersonationID}
		tenant = value.TenantID
	} else if metadata.Principal != "" {
		return nil, nil, nil, fmt.Errorf("native worker does not support this auth data")
	}
	state := &nativeState{
		auth: runtimeapp.AuthInfo{UID: metadata.Principal, Data: data},
		request: shared.Request{Type: shared.APICall, Started: metadata.Started, Headers: metadata.Headers.Clone(), PathParams: metadata.PathParams, ExecutionID: metadata.ExecutionID, Deployment: metadata.Deployment, Locale: metadata.Locale, API: &shared.APIDesc{Exposed: true, AuthRequired: metadata.AuthRequired}, InvocationID: metadata.InvocationID, TraceID: metadata.TraceID,
			CallerBinding: metadata.CallerBinding, Deadline: metadata.Deadline, Service: metadata.Service, Endpoint: metadata.Endpoint,
			Method: metadata.Method, Path: metadata.Path, Payload: input},
	}
	ctx = context.WithValue(ctx, stateKey{}, state)
	ctx = runtimeapi.WithInvocation(ctx, runtimeapi.NewInvocationWithMetadata(runtimeapi.InvocationMetadata{
		ID: metadata.InvocationID, Principal: metadata.Principal, TenantID: tenant, TraceID: metadata.TraceID, CallerBinding: metadata.CallerBinding, Deadline: metadata.Deadline, ExecutionID: metadata.ExecutionID, Deployment: metadata.Deployment, Locale: metadata.Locale,
	}))
	return ctx, state, scope.Enter(state), nil
}
