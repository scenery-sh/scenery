package runtime

import (
	"context"
	"net/http"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"pulse.dev/runtime/shared"
)

type requestStateKey struct{}

type requestState struct {
	started time.Time
	request shared.Request
	auth    AuthInfo
}

var stateStore sync.Map

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
		auth: auth,
	})
}

func stateFromContext(ctx context.Context) *requestState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(requestStateKey{}).(*requestState)
	return state
}

func withState(ctx context.Context, state *requestState) context.Context {
	return context.WithValue(ctx, requestStateKey{}, state)
}

func currentState() *requestState {
	id := goroutineID()
	state, ok := stateStore.Load(id)
	if !ok {
		return nil
	}
	return state.(*requestState)
}

func enterState(state *requestState) func() {
	id := goroutineID()
	prev, hadPrev := stateStore.Load(id)
	stateStore.Store(id, state)
	return func() {
		if hadPrev {
			stateStore.Store(id, prev)
			return
		}
		stateStore.Delete(id)
	}
}

func goroutineID() uint64 {
	var buf [64]byte
	n := goruntime.Stack(buf[:], false)
	line := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	idField := line[:strings.IndexByte(line, ' ')]
	id, _ := strconv.ParseUint(idField, 10, 64)
	return id
}

func newExternalState(ep *Endpoint, req *http.Request, path shared.PathParams, payload any, auth AuthInfo) *requestState {
	requestType := shared.APICall
	if ep.Raw {
		requestType = shared.RawAPICall
	}
	return &requestState{
		started: time.Now(),
		request: shared.Request{
			Type:       requestType,
			Service:    ep.Service,
			Endpoint:   ep.Name,
			Method:     req.Method,
			Path:       req.URL.Path,
			PathParams: path,
			Headers:    req.Header.Clone(),
			Payload:    payload,
			API: &shared.APIDesc{
				RequestType:  ep.PayloadType,
				ResponseType: ep.ResponseType,
				Raw:          ep.Raw,
				Exposed:      ep.Access != Private,
				AuthRequired: ep.Access == Auth,
			},
		},
		auth: auth,
	}
}
