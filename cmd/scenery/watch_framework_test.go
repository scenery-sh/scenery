package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appcfg "scenery.sh/internal/app"
)

func TestFrameworkSourceWatcherSignalsOnLinkedEditAndLinkChange(t *testing.T) {
	origPoll, origDebounce := frameworkWatchPollInterval, frameworkWatchDebounce
	frameworkWatchPollInterval = 25 * time.Millisecond
	frameworkWatchDebounce = 15 * time.Millisecond
	t.Cleanup(func() {
		frameworkWatchPollInterval = origPoll
		frameworkWatchDebounce = origDebounce
	})

	appRoot := t.TempDir()
	origin := t.TempDir()
	if err := os.WriteFile(filepath.Join(origin, "go.mod"), []byte("module scenery.sh\n\ngo 1.27.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "version.go"), []byte("package scenery\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher := newFrameworkSourceWatcher(ctx, appRoot)
	defer watcher.Close()

	waitSignal := func(label string) {
		t.Helper()
		select {
		case <-watcher.Events():
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for framework watch signal (%s)", label)
		}
	}

	// Linking after startup must wake the watcher.
	if _, err := appcfg.WriteFrameworkLink(appRoot, origin); err != nil {
		t.Fatal(err)
	}
	waitSignal("link created")

	// Drain any coalesced follow-up event from directory registration.
	select {
	case <-watcher.Events():
	case <-time.After(50 * time.Millisecond):
	}

	// Editing a Go source file in the linked checkout must wake the watcher.
	if err := os.WriteFile(filepath.Join(origin, "version.go"), []byte("package scenery\n// edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitSignal("linked source edited")

	// Unlinking must also wake the watcher.
	if _, err := appcfg.RemoveFrameworkLink(appRoot); err != nil {
		t.Fatal(err)
	}
	waitSignal("link removed")
}
