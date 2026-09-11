package nativecall

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/runtimeapi"
)

func TestNestedInvocationRetainsNativeState(t *testing.T) {
	var registry Registry
	type contextKey struct{}
	pointer := &struct{ Value int }{42}
	invocation := runtimeapi.NewInvocation("native", "actor", "tenant", "trace", time.Time{})
	ctx, cancel := context.WithCancel(runtimeapi.WithInvocation(context.WithValue(context.Background(), contextKey{}, pointer), invocation))
	defer cancel()
	failure := errors.New("native failure")
	if err := registry.Register(Registration{Address: "inner", Visibility: "package", Package: "app", Invoke: func(got context.Context, token, input any) (any, error) {
		if got != ctx || got.Value(contextKey{}) != pointer || input != pointer || token != invocation {
			t.Fatal("native state changed")
		}
		cancel()
		return pointer, failure
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Registration{Address: "outer", Invoke: func(got context.Context, token, input any) (any, error) {
		return registry.InvokeFrom(got, "inner", "app", token, input)
	}}); err != nil {
		t.Fatal(err)
	}
	value, err := registry.InvokeFrom(ctx, "outer", "app", invocation, pointer)
	if value != pointer || !errors.Is(err, failure) || ctx.Err() != context.Canceled {
		t.Fatalf("nested invocation = %v, %v", value, err)
	}
	if _, err := registry.InvokeFrom(ctx, "inner", "outsider", invocation, pointer); err == nil || !strings.Contains(err.Error(), "visible only") {
		t.Fatalf("visibility = %v", err)
	}
	forged := runtimeapi.NewInvocation("native", "actor", "tenant", "trace", time.Time{})
	if _, err := registry.InvokeFrom(ctx, "inner", "app", forged, pointer); err == nil {
		t.Fatal("independently minted invocation accepted")
	}
	snapshot := registry.Clone()
	if err := registry.Register(Registration{Address: "later", Invoke: func(context.Context, any, any) (any, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	if snapshot.Has("later") || !snapshot.Has("inner") {
		t.Fatal("snapshot shares mutable registrations")
	}
}

func TestJSONInvocationUsesRegisteredNativeCodecAndErrorOwner(t *testing.T) {
	var registry Registry
	pointer := &struct{ Value int }{7}
	invocation := runtimeapi.NewInvocation("json", "actor", "", "", time.Time{})
	ctx := runtimeapi.WithInvocation(context.Background(), invocation)
	encodeFailure := errors.New("encode failed")
	publicFailure := errors.New("sanitized")
	called := false
	err := registry.Register(Registration{Address: "json",
		DecodeInput: func(data []byte) (any, error) {
			if string(data) != "input" {
				t.Fatal("input bytes changed")
			}
			return pointer, nil
		},
		Invoke: func(got context.Context, token, input any) (any, error) {
			if got != ctx || token != invocation || input != pointer {
				t.Fatal("decoded native state changed")
			}
			called = true
			return pointer, nil
		},
		EncodeOutput: func(value any) ([]byte, error) {
			if value != pointer {
				t.Fatal("output pointer changed")
			}
			return nil, encodeFailure
		},
		SystemError: func(err error) error {
			if !errors.Is(err, encodeFailure) {
				t.Fatal("concrete encoding error lost")
			}
			return publicFailure
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.InvokeJSON(context.Background(), "json", "app", []byte("input")); err == nil || called {
		t.Fatal("anonymous JSON invocation reached native handler")
	}
	if _, err := registry.InvokeJSON(ctx, "json", "app", []byte("input")); !errors.Is(err, publicFailure) || !called {
		t.Fatalf("codec outcome = %v", err)
	}
}
