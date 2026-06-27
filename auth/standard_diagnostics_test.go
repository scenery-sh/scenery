package auth

import (
	"errors"
	"strings"
	"testing"
)

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
