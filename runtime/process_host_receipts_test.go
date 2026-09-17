package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// The journal replays ambiguity, discards only a torn final record, and stays
// bounded.
func TestDurableReceiptJournalReplaysAmbiguityAndStaysBounded(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "durable-receipts.jsonl")
	owner := processHostDurableOwner{process: "house_house", service: "house", taskName: "process_scene"}
	authorized := func(owners *processHostDurableOwners, id string) bool {
		t.Helper()
		got, ok, err := owners.load("principal-1", id)
		if err != nil {
			t.Fatal(err)
		}
		return ok && got == owner
	}
	var first processHostDurableOwners
	if err := first.open(journal); err != nil {
		t.Fatal(err)
	}
	conflicting := owner
	conflicting.process = "maps_maps"
	for _, store := range []struct {
		id    string
		owner processHostDurableOwner
	}{{"kept", owner}, {"conflict", owner}, {"conflict", conflicting}, {"kept", owner}} {
		if err := first.store("principal-1", store.id, store.owner); err != nil {
			t.Fatal(err)
		}
	}
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
	if !authorized(&replayed, "kept") || authorized(&replayed, "conflict") || authorized(&replayed, "torn") || replayed.journal.lines != 3 {
		t.Fatalf("replayed receipts: kept %v, conflict %v, torn %v, records %d", authorized(&replayed, "kept"), authorized(&replayed, "conflict"), authorized(&replayed, "torn"), replayed.journal.lines)
	}
	// The torn record is removed, so a later append does not extend it.
	if err := replayed.store("principal-1", "after-torn", owner); err != nil {
		t.Fatal(err)
	}
	var again processHostDurableOwners
	if err := again.open(journal); err != nil || again.journal.poisoned != nil || !authorized(&again, "after-torn") {
		t.Fatalf("receipt appended after a torn record: %v, poisoned %v", err, again.journal.poisoned)
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
	if lines := bytes.Count(data, []byte{'\n'}); lines != processHostDurableOwnerLimit || bounded.journal.lines != lines || authorized(&bounded, "0") {
		t.Fatalf("compacted journal has %d records, host counts %d", lines, bounded.journal.lines)
	}
	bounded.journal.lines = 2*processHostDurableOwnerLimit - 1
	if err := bounded.store("principal-1", "next", owner); err != nil || bounded.journal.lines != processHostDurableOwnerLimit {
		t.Fatalf("journal records after an append at the bound = %d, %v", bounded.journal.lines, err)
	}
	var reopened processHostDurableOwners
	if err := reopened.open(journal); err != nil || !authorized(&reopened, "next") {
		t.Fatalf("the receipt stored at the bound was not journaled: %v", err)
	}
}

// A receipt authorization that cannot be committed is never made, and no later
// host incarnation restores an authorization the failed commit revoked: a
// failed append or a corrupt record poisons the journal, which then authorizes
// nothing.
func TestDurableReceiptJournalFailureNeverRestoresAnAuthorization(t *testing.T) {
	owner := processHostDurableOwner{process: "house_house", service: "house", taskName: "process_scene"}
	conflicting := owner
	conflicting.process = "maps_maps"
	unavailable := func(owners *processHostDurableOwners, id string) bool {
		_, ok, err := owners.load("principal-1", id)
		return !ok && errors.Is(err, errProcessHostStateUnavailable)
	}

	// The ambiguity that revokes execution-1 cannot be committed.
	journal := filepath.Join(t.TempDir(), "durable-receipts.jsonl")
	var first processHostDurableOwners
	if err := first.open(journal); err != nil {
		t.Fatal(err)
	}
	if err := first.store("principal-1", "execution-1", owner); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(journal, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := first.store("principal-1", "execution-1", conflicting); !errors.Is(err, errProcessHostStateUnavailable) || !unavailable(&first, "execution-1") {
		t.Fatalf("uncommitted ambiguity = %v", err)
	}
	var replaced processHostDurableOwners
	if err := replaced.open(journal); err != nil || !unavailable(&replaced, "execution-1") {
		t.Fatalf("replacement host after an uncommitted ambiguity: %v", err)
	}
	if err := replaced.store("principal-1", "execution-2", owner); !errors.Is(err, errProcessHostStateUnavailable) {
		t.Fatalf("receipt stored by a host of a poisoned journal = %v", err)
	}

	// A record that does not decode before the last one poisons the journal.
	corrupt := filepath.Join(t.TempDir(), "durable-receipts.jsonl")
	line, _ := json.Marshal(processHostDurableRecord{Principal: "principal-1", ExecutionID: "execution-1", Process: owner.process, Service: owner.service, TaskName: owner.taskName})
	if err := os.WriteFile(corrupt, append(append(line, '\n'), []byte("{not json}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	var damaged processHostDurableOwners
	if err := damaged.open(corrupt); err != nil || !unavailable(&damaged, "execution-1") {
		t.Fatalf("host of a corrupt journal: %v", err)
	}
	if _, err := os.Stat(corrupt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a poisoned journal remains replayable: %v", err)
	}
}
