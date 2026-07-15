package symphony

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCodexAppServerClientCompletesAfterNotificationHandler(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	client := &CodexAppServerClient{
		done:         make(chan error, 1),
		turnDone:     make(chan struct{}),
		lastActivity: time.Now(),
		onNotification: func(method string, params json.RawMessage) {
			if method == "turn/completed" {
				close(handled)
			}
		},
	}
	go client.readLoop(strings.NewReader(`{"method":"turn/completed","params":{"turn":{"id":"turn-1"}}}` + "\n"))
	if err := client.WaitForTurnCompleted(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handled:
	default:
		t.Fatal("turn completed before notification handler ran")
	}
}

func TestCodexAppServerClientStallTimeout(t *testing.T) {
	t.Parallel()

	client := &CodexAppServerClient{
		done:         make(chan error, 1),
		turnDone:     make(chan struct{}),
		lastActivity: time.Now().Add(-time.Second),
	}
	err := client.WaitForTurnCompleted(context.Background(), 5*time.Millisecond)
	if !errors.Is(err, ErrRunStalled) {
		t.Fatalf("err = %v, want stalled", err)
	}
}
