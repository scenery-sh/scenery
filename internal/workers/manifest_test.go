package workers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testRegistrationHashA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testRegistrationHashB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestValidateWorkerManifests(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "email.json", `{
  "schema_version": "onlava.worker.manifest.v1",
  "app": "orders",
  "language": "python",
  "build_id": "sha-123",
  "payload_codec": "onlava-json-v1",
  "temporal": {
    "namespace": "default",
    "task_queues": ["onlava.orders.activity.email.python"]
  },
  "activities": [
    {"name": "email.SendWelcome/v1", "input": "WelcomeEmail", "output": "Void"}
  ]
}`)

	result := Validate(root, "orders")
	if !result.Checked || !result.OK || result.Count != 1 {
		t.Fatalf("validation = %#v", result)
	}
	if len(result.Manifests) != 1 || result.Manifests[0].Activities[0] != "email.SendWelcome/v1" {
		t.Fatalf("summaries = %#v", result.Manifests)
	}
}

func TestValidateWorkerManifestRejectsIncompatibleQueueSharing(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "python.json", `{
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email"]},
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)
	writeManifest(t, root, "typescript.json", `{
  "app": "orders",
  "language": "typescript",
  "build_id": "sha-ts",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email"]},
  "activities": [{"name": "email.Render/v1", "input": "Input", "output": "Output"}]
}`)

	result := Validate(root, "orders")
	if result.OK {
		t.Fatalf("expected validation failure: %#v", result)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "shared by incompatible worker languages") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestValidateWorkerManifestV2AcceptsQueueRegistrations(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "email.json", `{
  "schema_version": "onlava.worker.manifest.v2",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default"},
  "task_queues": [
    {
      "name": "onlava.orders.activity.email",
      "activities": ["email.Send/v1"],
      "workflows": [],
      "registration_hash": "`+testRegistrationHashA+`"
    }
  ],
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)

	result := Validate(root, "orders")
	if !result.OK || len(result.Manifests) != 1 {
		t.Fatalf("validation = %#v", result)
	}
	manifest := result.Manifests[0]
	if manifest.SchemaVersion != ManifestSchemaVersionV2 || len(manifest.TaskQueueRegistrations) != 1 {
		t.Fatalf("manifest summary = %#v", manifest)
	}
	if len(manifest.TaskQueues) != 1 || manifest.TaskQueues[0] != "onlava.orders.activity.email" {
		t.Fatalf("task queues = %#v", manifest.TaskQueues)
	}
}

func TestValidateWorkerManifestV2RejectsRegistrationHashMismatch(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "python.json", `{
  "schema_version": "onlava.worker.manifest.v2",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default"},
  "task_queues": [{"name": "onlava.orders.activity.email", "activities": ["email.Send/v1"], "registration_hash": "`+testRegistrationHashA+`"}],
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)
	writeManifest(t, root, "typescript.json", `{
  "schema_version": "onlava.worker.manifest.v2",
  "app": "orders",
  "language": "typescript",
  "build_id": "sha-ts",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default"},
  "task_queues": [{"name": "onlava.orders.activity.email", "activities": ["email.Send/v1"], "registration_hash": "`+testRegistrationHashB+`"}],
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)

	result := Validate(root, "orders")
	if result.OK {
		t.Fatalf("expected validation failure: %#v", result)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "registration hash") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestValidateWorkerManifestV2RejectsMalformedRegistrationHash(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "python.json", `{
  "schema_version": "onlava.worker.manifest.v2",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default"},
  "task_queues": [{"name": "onlava.orders.activity.email", "activities": ["email.Send/v1"], "registration_hash": "sha256:ABC"}],
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)

	result := Validate(root, "orders")
	if result.OK {
		t.Fatalf("expected validation failure: %#v", result)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "64 lowercase hex") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestValidateWorkerManifestAllowsV1AndV2SharedQueueDuringMigration(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "python-v1.json", `{
  "schema_version": "onlava.worker.manifest.v1",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python-v1",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email"]},
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)
	writeManifest(t, root, "python-v2.json", `{
  "schema_version": "onlava.worker.manifest.v2",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python-v2",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default"},
  "task_queues": [{"name": "onlava.orders.activity.email", "activities": ["email.Render/v1"], "registration_hash": "`+testRegistrationHashA+`"}],
  "activities": [{"name": "email.Render/v1", "input": "Input", "output": "Output"}]
}`)

	result := Validate(root, "orders")
	if !result.OK {
		t.Fatalf("validation = %#v", result)
	}
}

func TestValidateWorkerManifestRejectsUnknownActivitiesWhenKnownSetProvided(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "python.json", `{
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email"]},
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)

	result := ValidateWithKnownActivities(root, "orders", []string{"email.Other/v1"})
	if result.OK {
		t.Fatalf("expected validation failure: %#v", result)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "not declared") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestValidateWorkerManifestRejectsBadShape(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "bad.json", `{
  "schema_version": "wrong",
  "app": "other",
  "language": "",
  "build_id": "",
  "payload_codec": "bad",
  "temporal": {"namespace": "", "task_queues": [""]},
  "activities": [{"name": "email.Send/v1", "input": "", "output": ""}]
}`)

	result := Validate(root, "orders")
	if result.OK {
		t.Fatalf("expected validation failure: %#v", result)
	}
	if len(result.Diagnostics) < 5 {
		t.Fatalf("expected multiple diagnostics, got %#v", result.Diagnostics)
	}
}

func TestGenerateBindingsWritesPythonAndTypeScriptStarters(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "email-python.json", `{
  "schema_version": "onlava.worker.manifest.v1",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email.python"]},
  "activities": [{"name": "email.SendWelcome/v1", "input": "WelcomeEmail", "output": "Void"}]
}`)
	writeManifest(t, root, "email-ts.json", `{
  "schema_version": "onlava.worker.manifest.v1",
  "app": "orders",
  "language": "typescript",
  "build_id": "sha-ts",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email.typescript"]},
  "activities": [{"name": "email.Render/v1", "input": "EmailInput", "output": "EmailOutput"}]
}`)
	outDir := filepath.Join(root, "bindings")

	result, err := GenerateBindings(root, "orders", outDir)
	if err != nil {
		t.Fatalf("GenerateBindings returned error: %v", err)
	}
	if !result.OK || len(result.Files) != 2 {
		t.Fatalf("binding result = %#v", result)
	}
	python, err := os.ReadFile(filepath.Join(outDir, "email_python", "onlava_worker.py"))
	if err != nil {
		t.Fatalf("read python binding: %v", err)
	}
	if !strings.Contains(string(python), "async def email_sendwelcome_v1") || !strings.Contains(string(python), "PAYLOAD_CODEC = \"onlava-json-v1\"") {
		t.Fatalf("python binding content:\n%s", python)
	}
	ts, err := os.ReadFile(filepath.Join(outDir, "email_ts", "onlava_worker.ts"))
	if err != nil {
		t.Fatalf("read typescript binding: %v", err)
	}
	if !strings.Contains(string(ts), "export async function email_render_v1") || !strings.Contains(string(ts), "payloadCodec = \"onlava-json-v1\"") {
		t.Fatalf("typescript binding content:\n%s", ts)
	}
}

func TestGenerateBindingsReturnsValidationDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "bad.json", `{
  "app": "orders",
  "language": "python",
  "build_id": "",
  "payload_codec": "bad",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email.python"]},
  "activities": [{"name": "email.Send/v1", "input": "Input", "output": "Output"}]
}`)

	result, err := GenerateBindings(root, "orders", filepath.Join(root, "bindings"))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if result.OK || len(result.Diagnostics) == 0 {
		t.Fatalf("binding result = %#v", result)
	}
}

func writeManifest(t *testing.T, root, name, data string) {
	t.Helper()
	dir := filepath.Join(root, ".onlava", "workers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
