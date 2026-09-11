package host

import (
	"context"
	"testing"

	"scenery.sh/auth"
	"scenery.sh/runtime/shared"
)

func TestApplicationAuthContextSharesRequestAndNativeIdentity(t *testing.T) {
	type key struct{}
	actor := &auth.AuthData{}
	parentAuth := &auth.AuthData{}
	payload := &struct{ ID int }{ID: 42}
	parent := &requestState{request: shared.Request{Payload: payload}, auth: AuthInfo{UID: "parent", Data: parentAuth}, trace: &traceSpan{traceID: "same-trace"}}
	ctx := auth.WithContext(context.WithValue(withState(context.Background(), parent), key{}, payload), "actor", actor)
	current := stateFromContext(ctx)
	if current == parent || current.request.Payload != payload || current.trace != parent.trace || ctx.Value(key{}) != payload {
		t.Fatal("auth context did not clone the request while preserving native state")
	}
	if parent.auth.Data != parentAuth || parent.auth.UID != "parent" {
		t.Fatal("auth override mutated its parent")
	}
	leave := enterState(current)
	defer leave()
	if uid, ok := auth.UserID(); !ok || uid != "actor" || auth.Data() != actor {
		t.Fatal("public auth helpers do not see the native request owner")
	}
}
