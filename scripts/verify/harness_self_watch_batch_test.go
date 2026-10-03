package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessWaitBuildRequestBindsCompletionToNewOperation(t *testing.T) {
	event := func(operation, name string, ok bool) string {
		data, err := json.Marshal(map[string]any{"data": map[string]any{
			"type": "build.step", "data": map[string]any{"operation_id": operation, "name": name, "ok": ok},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return string(data) + "\n"
	}
	prefix := event("previous", "build.queue", true)
	for _, test := range []struct {
		name    string
		suffix  string
		ok      bool
		pending bool
		failure string
	}{
		{name: "late previous success", suffix: event("previous", "build.request", true), ok: true, pending: true},
		{name: "late previous failure", suffix: event("previous", "build.request", false), ok: true, pending: true},
		{name: "new failure after previous success", suffix: event("next", "build.queue", true) + event("previous", "build.request", true) + event("next", "build.request", false)},
		{name: "new success after previous failure", suffix: event("next", "build.queue", true) + event("previous", "build.request", false) + event("next", "build.request", true), ok: true},
		{name: "new wrong outcome", suffix: event("next", "build.queue", true) + event("next", "build.request", false), ok: true, failure: "build request ok=false, want true"},
		{name: "missing identity", suffix: event("", "build.queue", true) + event("", "build.request", true), ok: true, pending: true},
		{name: "different operation", suffix: event("next", "build.queue", true) + event("unrelated", "build.request", true), ok: true, pending: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "supervisor.jsonl")
			if err := os.WriteFile(path, []byte(prefix+test.suffix), 0o600); err != nil {
				t.Fatal(err)
			}
			// Completed evidence can be read immediately. Missing evidence must
			// observe cancellation instead of accepting an unrelated completion.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := harnessWaitBuildRequest(ctx, path, int64(len(prefix)), test.ok)
			switch {
			case test.pending:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("wait = %v, want pending evidence and cancellation", err)
				}
			case test.failure != "":
				if err == nil || err.Error() != test.failure {
					t.Fatalf("wait = %v, want %s", err, test.failure)
				}
			case err != nil:
				t.Fatal(err)
			}
		})
	}
}
