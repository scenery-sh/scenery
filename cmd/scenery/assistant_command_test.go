package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

func TestAssistantStatusSnapshotSchemaRevisionMatchesDescriptor(t *testing.T) {
	if got := string(spec.SchemaRevision(assistantStatusSnapshotSchemaDescriptor)); got != assistantStatusSnapshotSchemaRevision {
		t.Fatalf("status snapshot schema revision = %q, want %q", got, assistantStatusSnapshotSchemaRevision)
	}
}

func TestInspectAssistantsDefaultsToProviderNeutral(t *testing.T) {
	root := filepath.Join(repoRootForTest(t), "internal", "compiler", "testdata", "native")
	output := captureStdout(t, func() error {
		return runSceneryInspect([]string{"assistants", "--app-root", root, "-o", "json"}, os.Stdout)
	})
	var payload inspectAssistantsResponse
	if err := decodeCLIJSON([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != inspectAssistantsKind || payload.SchemaRevision != newCLIPayloadIdentity(inspectAssistantsKind).SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if len(payload.Assistants) != 1 || payload.Assistants[0].Name != "support" {
		t.Fatalf("assistants = %#v", payload.Assistants)
	}
	if payload.Assistants[0].Status != "not_started" || payload.Assistants[0].Ready {
		t.Fatalf("default status = %#v", payload.Assistants[0])
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"eve", "adapter", "node_version", "package_lock", "overlay_path", "private_control_addr", "private_mcp_addr", "pid"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("provider implementation detail %q escaped default inspection: %s", forbidden, encoded)
		}
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.inspect.assistants.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("schema diagnostics = %v", diagnostics)
	}
}

func TestInspectAssistantsImplementationIsExplicit(t *testing.T) {
	root := filepath.Join(repoRootForTest(t), "internal", "compiler", "testdata", "native")
	output := captureStdout(t, func() error {
		return runSceneryInspect([]string{"assistants", "--implementation", "--app-root", root, "-o", "json"}, os.Stdout)
	})
	var payload inspectAssistantsResponse
	if err := decodeCLIJSON([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Assistants) != 1 || payload.Assistants[0].Implementation == nil {
		t.Fatalf("implementation view = %#v", payload.Assistants)
	}
	if payload.Assistants[0].Implementation.Adapter != "eve" {
		t.Fatalf("adapter = %q", payload.Assistants[0].Implementation.Adapter)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.inspect.assistants.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("schema diagnostics = %v", diagnostics)
	}
}

func TestAssistantStatusReadsSnapshotAndRemainsProviderNeutral(t *testing.T) {
	root := filepath.Join(repoRootForTest(t), "internal", "compiler", "testdata", "native")
	statusPath, err := assistantStatusSnapshotPath(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(statusPath)
		_ = os.Remove(filepath.Dir(statusPath))
		_ = os.Remove(filepath.Dir(filepath.Dir(statusPath)))
	})
	output := captureStdout(t, func() error {
		return runAssistantStatus([]string{"support", "--app-root", root, "-o", "json"}, os.Stdout)
	})
	var payload assistantStatusResponse
	if err := decodeCLIJSON([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != assistantStatusKind || payload.Assistant.Name != "support" {
		t.Fatalf("status = %#v", payload)
	}
	if payload.Assistant.Ready || payload.Assistant.Status != "not_started" {
		t.Fatalf("default status = %#v", payload.Assistant)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.assistant.status.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("schema diagnostics = %v", diagnostics)
	}

	record := assistantStatusRecord{
		Address: "app/assistant/support", Name: "support", Path: "/assistants/support", Access: "public", SessionAccess: "initiator",
		Policy:                  assistantPolicyRecord{Authentication: "std.authentication.none", Authorization: "std.authorization.public", Pipeline: "std.pipeline.empty"},
		ExpectedRuntimeRevision: "runtime-expected", ExpectedCapabilityRevision: "capability-expected",
		ActualRuntimeRevision: "runtime-actual", ActualCapabilityRevision: "capability-actual",
		Ready: true, Status: "ready", RestartCount: 2, LastFailure: "previous_helper_exited", LogSource: "assistant:support",
		Implementation: &assistantImplementationRecord{NodeVersion: "v24.18.0", RuntimePackage: "eve@0.29.5", PackageLockDigest: "sha256:lock", OverlayPath: "/private/overlay", PrivateControlAddr: "127.0.0.1:1", PrivateMCPAddr: "127.0.0.1:2", PID: 42},
	}
	if err := writeAssistantStatusSnapshot(root, []assistantStatusRecord{record}); err != nil {
		t.Fatal(err)
	}
	output = captureStdout(t, func() error {
		return runAssistantStatus([]string{"support", "--app-root", root, "-o", "json"}, os.Stdout)
	})
	if err := decodeCLIJSON([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Assistant.Ready || payload.Assistant.Revisions.ActualRuntimeRevision != "runtime-actual" || payload.Assistant.RestartCount != 2 {
		t.Fatalf("persisted status = %#v", payload.Assistant)
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "private_") || strings.Contains(string(encoded), "pid") {
		t.Fatalf("private status escaped = %s", encoded)
	}
	output = captureStdout(t, func() error {
		return runSceneryInspect([]string{"assistants", "--implementation", "--app-root", root, "-o", "json"}, os.Stdout)
	})
	var inspectPayload inspectAssistantsResponse
	if err := decodeCLIJSON([]byte(output), &inspectPayload); err != nil {
		t.Fatal(err)
	}
	implementation := inspectPayload.Assistants[0].Implementation
	if implementation == nil || implementation.NodeVersion != "v24.18.0" || implementation.PID != 42 || implementation.PrivateControlAddr == "" {
		t.Fatalf("implementation status = %#v", implementation)
	}
}

func TestAssistantStatusSnapshotRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	path, err := assistantStatusSnapshotPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_revision":"`+assistantStatusSnapshotSchemaRevision+`","assistants":[],"extra":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAssistantStatusSnapshot(root); err == nil {
		t.Fatal("snapshot accepted unknown field")
	}
	if err := os.WriteFile(path, []byte(`{"schema_revision":"`+assistantStatusSnapshotSchemaRevision+`","schema_revision":"`+assistantStatusSnapshotSchemaRevision+`","assistants":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAssistantStatusSnapshot(root); err == nil {
		t.Fatal("snapshot accepted duplicate field")
	}
}

func TestAssistantLiveStatusEmptyFailureStaysEmpty(t *testing.T) {
	root := t.TempDir()
	result := &compiler.Result{
		ImplementationRevisions: map[string]string{"app/assistant/support": "runtime-1"},
		Manifest: &graph.Manifest{
			ContractRevision: "capability-1",
			Resources: []graph.Resource{{
				Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support",
				Spec: map[string]any{
					"surface":        map[string]any{"authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}},
					"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
				},
			}},
		},
	}
	if err := writeAssistantLiveStatusSnapshot(root, result, []AssistantStatusRecord{{
		Address: "app/assistant/support", State: "ready", Ready: true,
		ActualRuntimeRevision: "runtime-1", ActualCapabilityRevision: "capability-1", LogSource: "assistant:support",
	}}); err != nil {
		t.Fatal(err)
	}
	records, err := readAssistantStatusSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := records["app/assistant/support"].LastFailure; got != "" {
		t.Fatalf("empty live failure became %q", got)
	}
}

func TestAssistantStatusSnapshotRejectsUnsafeFile(t *testing.T) {
	root := t.TempDir()
	path, err := assistantStatusSnapshotPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema_revision":"` + assistantStatusSnapshotSchemaRevision + `","assistants":[]}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAssistantStatusSnapshot(root); err == nil {
		t.Fatal("snapshot accepted group/world-readable file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readAssistantStatusSnapshot(root); err == nil {
		t.Fatal("snapshot accepted symlink")
	}
}
