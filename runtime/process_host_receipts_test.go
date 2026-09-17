package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"scenery.sh/internal/runtimeapi"
)

// A durable receipt accepted through one host incarnation stays authorized for
// its principal after a contract change replaces the host and keeps the process
// running the execution: status and cancellation reach that process, and no
// other principal gains access.
func TestDurableReceiptsStayAuthorizedAcrossHostReplacement(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useLinkedProcessIdentityForTest(t)
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: processLinkTarget{Network: "unix", Address: "/unused"}})
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "app/assistant/support#house/binding/process_scene_mcp", Name: "house__process_scene", AssistantAddress: "app/assistant/support",
		DecodeInput:  func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
		Durable:      true, DurableService: "house", DurableTask: "process_scene",
		Invoke: func(context.Context, MCPToolCallContext, any) (any, error) {
			return runtimeapi.ExecutionReceipt{DurableIdentity: "house/process_scene", ExecutionID: "execution-1", AcceptedRevision: processHostTestContract}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	var served atomic.Int32
	kept := serveProcessMCPOwnerForTest(t, &served)
	journal := filepath.Join(t.TempDir(), "durable-receipts.jsonl")
	startHost := func(number uint64) *processHost {
		t.Helper()
		host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "house_house", MCPTools: []ProcessHostMCPTool{
			{Process: "house_house", AssistantAddress: "app/assistant/support", Name: "house__process_scene"},
		}}, processLinkTestToken, processHostTestContract)
		if err != nil {
			t.Fatal(err)
		}
		if err := host.owners.open(journal); err != nil {
			t.Fatal(err)
		}
		if err := host.publish(processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Identity: processHostTestBuild(number), Processes: map[string]processGenerationInstance{"house_house": kept}}); err != nil {
			t.Fatal(err)
		}
		setActiveProcessHost(host)
		return host
	}
	t.Cleanup(func() { setActiveProcessHost(nil) })
	call := MCPToolCallContext{Principal: "principal-1", AssistantAddress: "app/assistant/support", RequestID: "request-1"}
	startHost(1)
	dispatch, _ := assistantMCPDispatchers()
	if outcome, err := dispatch.CallTool(context.Background(), call, "house__process_scene", json.RawMessage(`{}`)); err != nil || outcome.Receipt == nil {
		t.Fatalf("accepted durable call = %#v, %v", outcome, err)
	}
	mcpDurableOwners.Lock()
	mcpDurableOwners.values, mcpDurableOwners.order = map[string]mcpDurableOwner{}, nil
	mcpDurableOwners.Unlock()

	startHost(2)
	served.Store(0)
	_, durable := assistantMCPDispatchers()
	other := call
	other.Principal = "principal-2"
	if _, err := durable.Status(context.Background(), other, "execution-1"); err == nil || !strings.Contains(err.Error(), "not_found") || served.Load() != 0 {
		t.Fatalf("status for another principal after replacement = %v (served %d)", err, served.Load())
	}
	// Without a durable store the kept process reports its own store failure,
	// which proves the replacement host authorized the original receipt.
	for _, operation := range []func(context.Context, MCPToolCallContext, string) (json.RawMessage, error){durable.Status, durable.Cancel} {
		if _, err := operation(context.Background(), call, "execution-1"); err == nil || !strings.Contains(err.Error(), "durable execution store is unavailable") {
			t.Fatalf("durable operation after host replacement = %v", err)
		}
	}
	if served.Load() != 2 {
		t.Fatalf("kept process served %d durable requests", served.Load())
	}
}

// The journal replays ambiguity, ignores a torn record, and stays bounded.
func TestDurableReceiptJournalReplaysAmbiguityAndStaysBounded(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "durable-receipts.jsonl")
	owner := processHostDurableOwner{process: "house_house", service: "house", taskName: "process_scene"}
	var first processHostDurableOwners
	if err := first.open(journal); err != nil {
		t.Fatal(err)
	}
	first.store("principal-1", "kept", owner)
	first.store("principal-1", "conflict", owner)
	conflicting := owner
	conflicting.process = "maps_maps"
	first.store("principal-1", "conflict", conflicting)
	first.store("principal-1", "kept", owner)
	file, err := os.OpenFile(journal, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"principal":"principal-1","execution_id":"torn","proc`)
	_ = file.Close()
	var replayed processHostDurableOwners
	if err := replayed.open(journal); err != nil {
		t.Fatal(err)
	}
	if got, ok := replayed.load("principal-1", "kept"); !ok || got != owner {
		t.Fatalf("replayed receipt = %#v, %v", got, ok)
	}
	for _, id := range []string{"conflict", "torn"} {
		if _, ok := replayed.load("principal-1", id); ok {
			t.Fatalf("receipt %s is authorized after replay", id)
		}
	}
	if replayed.journal.lines != 4 {
		t.Fatalf("journal records = %d", replayed.journal.lines)
	}

	var oversized bytes.Buffer
	for index := range processHostDurableOwnerLimit + 1 {
		line, _ := json.Marshal(processHostDurableRecord{Principal: "principal-1", ExecutionID: strconv.Itoa(index), Process: owner.process, Service: owner.service, TaskName: owner.taskName})
		oversized.Write(append(line, '\n'))
	}
	if err := os.WriteFile(journal, oversized.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var bounded processHostDurableOwners
	if err := bounded.open(journal); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if lines := bytes.Count(data, []byte{'\n'}); lines != processHostDurableOwnerLimit || bounded.journal.lines != lines {
		t.Fatalf("compacted journal has %d records, host counts %d", lines, bounded.journal.lines)
	}
	if _, ok := bounded.load("principal-1", "0"); ok {
		t.Fatal("the oldest receipt beyond the limit stayed authorized")
	}
	bounded.journal.lines = 2*processHostDurableOwnerLimit - 1
	bounded.store("principal-1", "next", owner)
	if bounded.journal.lines != processHostDurableOwnerLimit {
		t.Fatalf("journal records after an append at the bound = %d", bounded.journal.lines)
	}
	var reopened processHostDurableOwners
	if err := reopened.open(journal); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.load("principal-1", "next"); !ok {
		t.Fatal("the receipt stored at the bound was not journaled")
	}
}
