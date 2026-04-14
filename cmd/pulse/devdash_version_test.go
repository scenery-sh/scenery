package main

import "testing"

func TestPulseDashboardCompatVersion(t *testing.T) {
	if pulseDashboardCompatVersion == "" {
		t.Fatal("pulse dashboard compat version must not be empty")
	}
	if pulseDashboardCompatChannel != "ga" {
		t.Fatalf("unexpected dashboard compat channel: %q", pulseDashboardCompatChannel)
	}
}
