package main

import (
	"context"
	"errors"
	"testing"
)

func TestHarnessCapabilityAuthorityRetainsFailureEvidence(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{nil, errors.New("required SQL proof unavailable")} {
		step := runHarnessCapabilityAuthorityStepWithCheck(t.Context(), "/repo", func(_ context.Context, root string) (map[string]any, error) {
			if root != "/repo" {
				t.Fatalf("wrong target repository %s", root)
			}
			return map[string]any{"assertions": []map[string]any{{"name": "no-SQL non-mutation", "ok": true}}}, failure
		})
		if step.OK != (failure == nil) || hasErrorDiagnostics(step.Diagnostics) != (failure != nil) || step.Summary["assertions"] == nil || step.Name != harnessCapabilityAuthorityName {
			t.Fatalf("capability evidence changed: %+v", step)
		}
	}
}
