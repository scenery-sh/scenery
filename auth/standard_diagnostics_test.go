package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStandardAuthInitializationIsIndependentOfFirstRequest(t *testing.T) {
	t.Parallel()

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()

	initCtx, cancelInit := standardAuthInitializationContext(requestCtx)
	defer cancelInit()
	if err := requestCtx.Err(); err != context.Canceled {
		t.Fatalf("request context error = %v, want context canceled", err)
	}
	if err := initCtx.Err(); err != nil {
		t.Fatalf("initialization context inherited request cancellation: %v", err)
	}
	deadline, ok := initCtx.Deadline()
	if !ok {
		t.Fatal("initialization context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > standardAuthInitializationTimeout {
		t.Fatalf("initialization deadline remaining = %v, want within (0, %v]", remaining, standardAuthInitializationTimeout)
	}
}

func TestClarifyStandardAuthTenantError(t *testing.T) {
	t.Parallel()

	original := errors.New(`no such table: scenery_auth_tenants`)

	err := clarifyStandardAuthTenantError(original)
	if !errors.Is(err, original) {
		t.Fatalf("wrapped error does not preserve original: %v", err)
	}
	got := err.Error()
	for _, want := range []string{"standard auth owns framework tenant state", "scenery_auth_tenants", "not an app-local tenants service"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q does not contain %q", got, want)
		}
	}
}

func TestClarifyStandardAuthTenantErrorIgnoresUnrelatedErrors(t *testing.T) {
	t.Parallel()

	original := errors.New("plain runtime error")
	if got := clarifyStandardAuthTenantError(original); got != original {
		t.Fatalf("error = %v, want original", got)
	}
}
