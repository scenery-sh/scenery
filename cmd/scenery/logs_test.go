package main

import "testing"

func TestParseLogsArgsRejectsRemovedBackendFlag(t *testing.T) {
	t.Parallel()

	if _, err := parseLogsArgs([]string{"--backend", "victoria"}); err == nil {
		t.Fatal("parseLogsArgs accepted removed --backend flag")
	}
}
