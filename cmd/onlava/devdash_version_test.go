package main

import "testing"

func TestOnlavaDashboardCompatVersion(t *testing.T) {
	if onlavaDashboardCompatVersion == "" {
		t.Fatal("onlava dashboard compat version must not be empty")
	}
	if onlavaDashboardCompatChannel != "ga" {
		t.Fatalf("unexpected dashboard compat channel: %q", onlavaDashboardCompatChannel)
	}
}
