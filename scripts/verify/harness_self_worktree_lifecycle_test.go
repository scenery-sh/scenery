package main

import (
	"slices"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestWorktreeServingReplacementsNameEitherModelsProcess(t *testing.T) {
	before := localagent.Session{AppPID: "100", Processes: map[string]localagent.Process{"service-library-library": {PID: 200}, "frontend-web": {PID: 300}}}
	if replaced := worktreeServingReplacements(before, before); len(replaced) != 0 {
		t.Fatalf("an unchanged session reported replacements %v", replaced)
	}
	application := before
	application.AppPID = "101"
	if replaced := worktreeServingReplacements(before, application); !slices.Equal(replaced, []string{"app"}) {
		t.Fatalf("a replaced application process = %v", replaced)
	}
	service := before
	service.Processes = map[string]localagent.Process{"service-library-library": {PID: 201}, "frontend-web": {PID: 301}}
	if replaced := worktreeServingReplacements(before, service); !slices.Equal(replaced, []string{"service-library-library"}) {
		t.Fatalf("a replaced service process = %v", replaced)
	}
}
