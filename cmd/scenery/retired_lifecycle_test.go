package main

import (
	"testing"
)

func TestRetiredLifecycleSyntaxIsRejectedBeforeWork(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"gen", "client"}, {"generate", "client"}, {"db", "sync"}, {"inspect", "endpoints", "--json"}, {"generate", "--json"}} {
		if err := runWithCLITelemetry(args, nil); err == nil || cliExitCode(err) != 2 {
			t.Fatalf("retired syntax %v was admitted: %v", args, err)
		}
	}
}
