package deployplan

import (
	"context"
	"strings"
	"testing"
)

func TestPendingDeploymentPlanReportsRevisionSchemeChanged(t *testing.T) {
	plan := DeploymentPlan{SpecRevision: "sha256:old"}
	_, err := ApplyDeploymentPlan(context.Background(), t.TempDir(), plan, DeploymentApplyOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "revision_scheme_changed") {
		t.Fatalf("error = %v", err)
	}
}
