package main

import (
	"strings"
	"testing"
)

// A word close to a deploy command is a typo, not an SSH target, unless the
// app lists it as one.
func TestDeployRefusesMisspelledCommandsAsTargets(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"statsu", "stauts", "setpu", "tardown"} {
		err := deployTargetTypoError(target, []string{"prod"})
		if err == nil || cliExitCode(err) != 2 || !strings.HasPrefix(err.Error(), `unknown deploy command "`+target+`"; did you mean "`) {
			t.Errorf("deploy %s: %v", target, err)
		}
	}
	for _, target := range []string{"prod", "deploy@prod.example.com", "10.0.0.5", "stage"} {
		if err := deployTargetTypoError(target, nil); err != nil {
			t.Errorf("deploy %s refused: %v", target, err)
		}
	}
	if err := deployTargetTypoError("statsu", []string{"statsu"}); err != nil {
		t.Fatalf("a configured SSH target was refused: %v", err)
	}
	if targets := configuredDeployTargets([]string{"--app-root", t.TempDir()}); len(targets) != 0 {
		t.Fatalf("targets without an app = %q", targets)
	}
}
