package main

import (
	"context"
	"errors"
	"testing"
)

func TestHarnessCoreSeparationReportsAssertions(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{nil, errors.New("missing proof")} {
		step := runHarnessCoreSeparationStepWithCheck(context.Background(), "/repo", func(_ context.Context, root string) (map[string]any, error) {
			if root != "/repo" {
				t.Fatalf("wrong repository %q", root)
			}
			return map[string]any{"assertions": map[string]any{"A3": true}}, failure
		})
		if step.OK != (failure == nil) || hasErrorDiagnostics(step.Diagnostics) != (failure != nil) || step.Name != harnessCoreSeparationName || step.Summary["assertions"] == nil {
			t.Fatalf("lost assertion/failure evidence: %+v", step)
		}
	}
}
