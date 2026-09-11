package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"scenery.sh/internal/runtimeapi"
	runtime "scenery.sh/runtime"
	"scenery.sh/runtime/host"
)

func TestNativeFacadeUsesTheHostsRegisteredCallback(t *testing.T) {
	type key struct{}
	pointer := &struct{ Value int }{42}
	invocation := runtimeapi.NewInvocation("facade", "actor", "tenant", "trace", time.Time{})
	ctx := runtimeapi.WithInvocation(context.WithValue(context.Background(), key{}, pointer), invocation)
	failure := errors.New("native implementation failure")
	if err := host.RegisterContractInternalBinding("test/native-facade", func(got context.Context, token, input any) (any, error) {
		if got != ctx || got.Value(key{}) != pointer || token != invocation || input != pointer {
			t.Fatal("host binding changed native state")
		}
		return pointer, failure
	}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.InvokeContractBinding(ctx, "test/native-facade", invocation, pointer)
	if result != pointer || !errors.Is(err, failure) {
		t.Fatalf("native facade result=%v error=%v", result, err)
	}
	if _, err := runtime.InvokeContractBinding(context.Background(), "test/native-facade", invocation, pointer); err == nil {
		t.Fatal("native facade bypassed trusted invocation check")
	}
}
