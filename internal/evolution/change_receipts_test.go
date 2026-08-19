package evolution

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/spec"
)

func TestIssuedChangePlanLoadingAndReplayAfterExpiryAndWorkspaceDrift(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	base, err := compiler.Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  new(base.Manifest.ContractRevision),
		Caller:                "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIssuedChangePlan(root, plan.PlanID)
	if err != nil || !semanticEqual(loaded, plan) {
		t.Fatalf("loaded plan = %#v err=%v", loaded, err)
	}

	// Leave enough time for the initial commit, then prove replay does not
	// consult expiry or the current workspace revision.
	plan.ExpiresAt = time.Now().UTC().Add(time.Second)
	plan.PlanID = changePlanID(plan)
	if err := RetainIssuedPlan(root, IssuedChangePlan, plan.PlanID, plan); err != nil {
		t.Fatal(err)
	}
	first, err := ApplyIssuedChangePlanWithOptions(root, plan.PlanID, ApplyOptions{Caller: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatalf("first apply was marked replayed: %#v", first)
	}
	time.Sleep(1100 * time.Millisecond)
	packagePath := filepath.Join(root, "house", testPackageFilename)
	packageBytes, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packagePath, append(packageBytes, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ApplyIssuedChangePlanWithOptions(root, plan.PlanID, ApplyOptions{Caller: "local"})
	if err != nil {
		t.Fatalf("replay after expiry and drift: %v", err)
	}
	if !second.Replayed || !semanticEqual(second.Receipt, first.Receipt) {
		t.Fatalf("replay = %#v first = %#v", second, first)
	}
	if _, err := ApplyIssuedChangePlanWithOptions(root, plan.PlanID, ApplyOptions{Caller: "other"}); err == nil || !strings.Contains(err.Error(), "caller mismatch") {
		t.Fatalf("caller mismatch error = %v", err)
	}
}

func TestAppliedChangeReceiptLoaderRejectsCorruptAndMismatchedRecords(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	base, err := compiler.Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: new(base.Manifest.ContractRevision), Caller: "local"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ApplyIssuedChangePlanWithOptions(root, plan.PlanID, ApplyOptions{Caller: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChangeReceiptOnce(root, plan.PlanID, []byte("forged\n")); !errors.Is(err, errChangeReceiptExists) {
		t.Fatalf("receipt overwrite error = %v", err)
	}
	receiptPath := appliedPlanPath(root, plan.PlanID)
	original, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAppliedChangeReceipt(root, plan.PlanID); err == nil || !strings.Contains(err.Error(), "applied change receipt") {
		t.Fatalf("corrupt receipt error = %v", err)
	}
	if err := os.WriteFile(receiptPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	duplicate := append([]byte(`{"plan_id":"`+plan.PlanID+`","plan_id":"`+plan.PlanID+`",`), original[1:]...)
	if err := os.WriteFile(receiptPath, duplicate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAppliedChangeReceipt(root, plan.PlanID); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("duplicate-key receipt error = %v", err)
	}
	if err := os.WriteFile(receiptPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	var receipt ChangeReceipt
	if err := json.Unmarshal(original, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.WorkspaceRevision = "sha256:" + strings.Repeat("0", 64)
	mismatched, err := spec.MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, append(mismatched, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyIssuedChangePlanWithOptions(root, plan.PlanID, ApplyOptions{Caller: "local"}); err == nil || !strings.Contains(err.Error(), "receipt revisions") {
		t.Fatalf("mismatched receipt error = %v", err)
	}
	_ = first
}

func TestLoadIssuedChangePlanRejectsContentTampering(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	base, err := compiler.Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: new(base.Manifest.ContractRevision), Caller: "local"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := issuedPlanPath(root, IssuedChangePlan, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tampered ChangePlan
	if err := json.Unmarshal(original, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.ImplementationStatus = "forged"
	tampered.RiskRecords = append(tampered.RiskRecords, map[string]any{"risk_id": "forged"})
	encoded, err := json.MarshalIndent(tampered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIssuedChangePlan(root, plan.PlanID); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("tampered plan error = %v", err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIssuedChangePlan(root, plan.PlanID); err != nil {
		t.Fatalf("restored plan load: %v", err)
	}
	duplicate := append([]byte(`{"plan_id":"`+plan.PlanID+`","plan_id":"`+plan.PlanID+`",`), original[1:]...)
	if err := os.WriteFile(path, duplicate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIssuedChangePlan(root, plan.PlanID); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("duplicate-key plan error = %v", err)
	}
}

func TestConcurrentIssuedChangePlanApplyReturnsOneReplay(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	base, err := compiler.Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: new(base.Manifest.ContractRevision), Caller: "local"})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		value ChangeApplyResult
		err   error
	}
	results := make([]result, 2)
	var wait sync.WaitGroup
	wait.Add(len(results))
	for index := range results {
		go func(index int) {
			defer wait.Done()
			results[index].value, results[index].err = ApplyIssuedChangePlanWithOptions(root, plan.PlanID, ApplyOptions{Caller: "local"})
		}(index)
	}
	wait.Wait()
	var first, replay int
	for index, result := range results {
		if result.err != nil {
			t.Fatalf("apply %d error = %v", index, result.err)
		}
		if result.value.Replayed {
			replay++
		} else {
			first++
		}
	}
	if first != 1 || replay != 1 {
		t.Fatalf("concurrent results = %#v", results)
	}
}

func TestLoadAppliedChangeReceiptRequiresIssuedPlan(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadAppliedChangeReceipt(root, "sha256:"+strings.Repeat("a", 64)); err == nil {
		t.Fatalf("missing issued plan error = %v", err)
	}
}
