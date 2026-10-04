package devdash

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStorePruningAccountsExactEncodedBytesAndClearsDroppedReferences(t *testing.T) {
	state := &storeState{Version: 1}
	for i := range 8 {
		state.ProcessOutput = append(state.ProcessOutput, ProcessOutput{ID: int64(i), Output: []byte("binary<&")})
		state.DevEvents = append(state.DevEvents, storedDevEvent{DevEvent: DevEvent{ID: int64(i), Message: "escaped <&\"", Fields: json.RawMessage(`{"key":"<"}`)}})
		state.ProcessEvents = append(state.ProcessEvents, ProcessEvent{ID: int64(i), PayloadJSON: json.RawMessage(`{"key":"<&"}`)})
	}
	output, events, process := state.ProcessOutput, state.DevEvents, state.ProcessEvents
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	remainingBytes := len(data)
	for len(state.ProcessOutput)+len(state.DevEvents)+len(state.ProcessEvents) > 0 {
		var removed int
		switch {
		case len(state.ProcessOutput) > 0:
			state.ProcessOutput, removed = dropOldestBudgetChunk(state.ProcessOutput, "process_output")
		case len(state.DevEvents) > 0:
			state.DevEvents, removed = dropOldestBudgetChunk(state.DevEvents, "dev_events")
		default:
			state.ProcessEvents, removed = dropOldestBudgetChunk(state.ProcessEvents, "process_events")
		}
		remainingBytes -= removed
		data, err = json.Marshal(state)
		if err != nil || len(data) != remainingBytes {
			t.Fatalf("budget accounting: actual=%d expected=%d err=%v", len(data), remainingBytes, err)
		}
	}
	for _, item := range output {
		if item.Output != nil {
			t.Fatal("retained dropped output")
		}
	}
	for _, item := range events {
		if item.Message != "" || item.Fields != nil {
			t.Fatal("retained dropped event")
		}
	}
	for _, item := range process {
		if item.PayloadJSON != nil {
			t.Fatal("retained dropped process event")
		}
	}
}

func TestTailRetentionClearsDiscardedStorage(t *testing.T) {
	items := []string{strings.Repeat("old", 100), "new"}
	if got := tailSlice(items, 1); len(got) != 1 || got[0] != "new" || items[0] != "" {
		t.Fatal("tail retained discarded value")
	}
}
