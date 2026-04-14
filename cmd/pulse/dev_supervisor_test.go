package main

import (
	"bytes"
	"testing"
)

func TestAppChildEnvForcesColorWhenRequested(t *testing.T) {
	env := appChildEnv([]string{"A=1"}, true, "B=2")
	if !containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) missing CLICOLOR_FORCE=1", env)
	}
}

func TestAppChildEnvLeavesColorUnsetWhenDisabled(t *testing.T) {
	env := appChildEnv([]string{"A=1"}, false, "B=2")
	if containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) unexpectedly added CLICOLOR_FORCE=1", env)
	}
}

func TestStripANSI(t *testing.T) {
	input := []byte("\x1b[34mTRC\x1b[0m request completed code=ok\n")
	got := stripANSI(input)
	want := []byte("TRC request completed code=ok\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("stripANSI(%q) = %q, want %q", input, got, want)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
