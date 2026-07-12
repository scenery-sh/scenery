package main

import "testing"

func TestParseLogsArgsRejectsRemovedBackendFlag(t *testing.T) {
	t.Parallel()

	if _, err := parseLogsArgs([]string{"--backend", "victoria"}); err == nil {
		t.Fatal("parseLogsArgs accepted removed --backend flag")
	}
}

func TestParseLogsArgsRejectsRemovedShortFlags(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-n", "-f"} {
		if _, err := parseLogsArgs([]string{flag}); err == nil {
			t.Fatalf("parseLogsArgs accepted removed %s flag", flag)
		}
	}
}
