package runtimeapp

import (
	"context"
	"errors"
	"testing"
)

func TestAuthContextBeforeBindingPreservesNativeValueAndCancellation(t *testing.T) {
	type key struct{}
	data := &struct{ Name string }{Name: "actor"}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, data))
	defer cancel()
	ctx := WithAuthContext(parent, AuthInfo{UID: "user", Data: data})
	auth, started, ok := AuthContextValue(ctx)
	if !ok || auth.UID != "user" || auth.Data != data || ctx.Value(key{}) != data || started.IsZero() {
		t.Fatal("native auth context was not preserved")
	}
	if CurrentAuth() != nil {
		t.Fatal("context construction installed ambient authentication")
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("auth context lost parent cancellation")
	}
}
