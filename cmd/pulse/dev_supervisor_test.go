package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"os"
	"testing"
	"time"

	"pulse.dev/internal/app"
	"pulse.dev/internal/devdash"
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

func TestIsExpectedOutputReadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "eof", err: io.EOF, want: true},
		{name: "os err closed", err: os.ErrClosed, want: true},
		{name: "net err closed", err: net.ErrClosed, want: true},
		{name: "wrapped path error", err: &fs.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}, want: true},
		{name: "other", err: io.ErrUnexpectedEOF, want: false},
	}
	for _, tt := range tests {
		if got := isExpectedOutputReadError(tt.err); got != tt.want {
			t.Fatalf("%s: isExpectedOutputReadError(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

func TestListAppsReturnsOnlyActiveSupervisorApp(t *testing.T) {
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	for _, rec := range []devdash.AppRecord{
		{ID: "basicapp", Name: "basicapp", Root: "/tmp/basicapp", UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "cronapp", Name: "cronapp", Root: "/tmp/cronapp", UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "onlvnext-o5o2", Name: "onlvnext-o5o2", Root: "/tmp/onlv", Running: true, UpdatedAt: now},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	s := &devSupervisor{
		cfg:   app.Config{Name: "onlvnext-o5o2"},
		store: store,
		status: devdash.AppRecord{
			ID:      "onlvnext-o5o2",
			Name:    "onlvnext-o5o2",
			Root:    "/tmp/onlv",
			Running: true,
		},
	}

	items, err := s.listApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("listApps() returned %d items, want 1: %#v", len(items), items)
	}
	if got := items[0]["id"]; got != "onlvnext-o5o2" {
		t.Fatalf("listApps()[0].id = %v, want onlvnext-o5o2", got)
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
