package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/doctor"
	"scenery.sh/internal/runtimeassets"
)

func TestDoctorAssistantSourceAndReservedChecksAreReadOnly(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "assistants", "support")
	if err := os.MkdirAll(filepath.Join(source, "agent", "channels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "agent", "channels", "scenery.ts"), []byte("authored"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, sourceCheck, ok := doctorAssistantSourceCheck(root, "support", "./assistants/support")
	if !ok || sourceCheck.Status != doctor.StatusOK || resolved != source {
		t.Fatalf("source check = %#v, path=%q, ok=%v", sourceCheck, resolved, ok)
	}
	reserved := doctorAssistantReservedCheck("support", resolved, ok)
	if reserved.Status != doctor.StatusError || !strings.Contains(reserved.Message, "reserved") {
		t.Fatalf("reserved check = %#v", reserved)
	}
	if _, err := os.Stat(filepath.Join(source, "agent", "channels", "scenery.ts")); err != nil {
		t.Fatalf("reserved authored file was changed or removed: %v", err)
	}

	_, escaped, escapedOK := doctorAssistantSourceCheck(root, "support", "../outside")
	if escapedOK || escaped.Status != doctor.StatusError || !strings.Contains(escaped.Message, "escapes") {
		t.Fatalf("escaped source check = %#v, ok=%v", escaped, escapedOK)
	}
}

func TestDoctorAssistantPackageLockDetectsMissingAndDrift(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "assistants", "support")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	packageJSON := `{"name":"example","version":"1.0.0","dependencies":{"eve":"0.29.5"}}`
	lockJSON := `{"name":"example","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"example","version":"1.0.0","dependencies":{"eve":"0.29.5"}}}}`
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package-lock.json"), []byte(lockJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	check := doctorAssistantPackageCheck(root, "support", source, true, "./assistants/support/package.json", "./assistants/support/package-lock.json", "")
	if check.Status != doctor.StatusOK {
		t.Fatalf("exact package lock check = %#v", check)
	}
	if err := os.WriteFile(filepath.Join(source, "package-lock.json"), []byte(strings.Replace(lockJSON, "1.0.0", "2.0.0", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	check = doctorAssistantPackageCheck(root, "support", source, true, "./assistants/support/package.json", "./assistants/support/package-lock.json", "")
	if check.Status != doctor.StatusError || !strings.Contains(check.Message, "drifted") {
		t.Fatalf("drift package lock check = %#v", check)
	}
	check = doctorAssistantPackageCheck(root, "support", source, true, "./assistants/support/package.json", "./assistants/support/missing-package-lock.json", "")
	if check.Status != doctor.StatusError {
		t.Fatalf("missing package lock check = %#v", check)
	}
}

func TestDoctorAssistantManagedNodeNeverDownloads(t *testing.T) {
	root := t.TempDir()
	check := doctorAssistantNodeCheck(context.Background(), root, "support", doctor.RuntimeInfo{GOOS: "darwin", GOARCH: "arm64"})
	if check.Status != doctor.StatusError || !strings.Contains(check.Message, "not installed") {
		t.Fatalf("missing managed Node check = %#v", check)
	}
	platform := doctorAssistantPlatformCheck("support", doctor.RuntimeInfo{GOOS: "plan9", GOARCH: "amd64"})
	if platform.Status != doctor.StatusError || !strings.Contains(platform.Message, "unsupported") {
		t.Fatalf("unsupported platform check = %#v", platform)
	}
}

func TestDoctorAssistantProductionTokenCheckIsExplicitAndRedacted(t *testing.T) {
	root := t.TempDir()
	check := doctorAssistantProductionTokenCheck(root, appcfg.Config{Envs: map[string]appcfg.EnvConfig{"local": {Default: true}}}, "support")
	if check.Status != doctor.StatusSkipped || !strings.Contains(check.Message, "not applicable") {
		t.Fatalf("local-only production check = %#v", check)
	}
	t.Setenv(assistantTokenKeyEnv, "")
	check = doctorAssistantProductionTokenCheck(root, appcfg.Config{Envs: map[string]appcfg.EnvConfig{"production": {}}}, "support")
	if check.Status != doctor.StatusError || !strings.Contains(check.Message, "missing") {
		t.Fatalf("missing production key check = %#v", check)
	}
	t.Setenv(assistantTokenKeyEnv, strings.Repeat("k", 32))
	check = doctorAssistantProductionTokenCheck(root, appcfg.Config{Envs: map[string]appcfg.EnvConfig{"production": {}}}, "support")
	if check.Status != doctor.StatusOK {
		t.Fatalf("available production key check = %#v", check)
	}
	encoded, _ := json.Marshal(check)
	if strings.Contains(string(encoded), strings.Repeat("k", 32)) || strings.Contains(string(encoded), `"eve"`) {
		t.Fatalf("provider or secret leaked in doctor check: %s", encoded)
	}
}

func TestDoctorAssistantRevisionCheckUsesProviderNeutralStatus(t *testing.T) {
	result := &compiler.Result{Manifest: &compiler.Manifest{ContractRevision: "sha256:" + strings.Repeat("a", 64)}}
	status := doctorAssistantStatus{Present: true, Ready: true, ExpectedRuntime: "runtime-1", ExpectedCapability: result.Manifest.ContractRevision, ActualRuntime: "runtime-stale", ActualCapability: result.Manifest.ContractRevision}
	check := doctorAssistantRevisionCheck("support", result, status, nil)
	if check.Status != doctor.StatusError || !strings.Contains(check.Message, "runtime revision") {
		t.Fatalf("stale runtime revision check = %#v", check)
	}
	status.ActualRuntime = "runtime-1"
	check = doctorAssistantRevisionCheck("support", result, status, nil)
	if check.Status != doctor.StatusOK {
		t.Fatalf("matching revisions check = %#v", check)
	}
	encoded, _ := json.Marshal(check)
	if strings.Contains(string(encoded), `"eve"`) {
		t.Fatalf("provider leaked in revision check: %s", encoded)
	}
}

func TestDoctorAssistantAssetDescriptorSelfDigest(t *testing.T) {
	root := t.TempDir()
	assetDir := filepath.Join(root, ".scenery", "build", "assets", "development")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := func(value string) string {
		sum := sha256.Sum256([]byte(value))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	value := map[string]any{
		"kind": runtimeassets.AssistantAssetKind, "schema_revision": runtimeassets.AssistantAssetSchemaRevision,
		"assistant_address": "app/assistant/support", "target": "development", "runtime_revision": "runtime-1", "capability_revision": "sha256:" + strings.Repeat("a", 64),
		"node_archive_digest": digest("node"), "node_tree_digest": digest("node-tree"), "capsule_archive_digest": digest("capsule"), "capsule_tree_digest": digest("capsule-tree"), "capsule_entry": ".scenery/bootstrap.mjs", "package_lock_digest": digest("package-lock"),
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	value["descriptor_digest"] = "sha256:" + hex.EncodeToString(sum[:])
	encoded, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "runtime-descriptor.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	result := &compiler.Result{Manifest: &compiler.Manifest{ContractRevision: value["capability_revision"].(string)}}
	assistant := compiler.Resource{Address: "app/assistant/support", Name: "support", Kind: "scenery.assistant"}
	check := doctorAssistantAssetCheck(root, assistant, result)
	if check.Status != doctor.StatusOK {
		t.Fatalf("valid asset descriptor check = %#v", check)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "runtime-descriptor.json"), []byte(strings.Replace(string(encoded), "descriptor_digest", "descriptor_digest_bad", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	check = doctorAssistantAssetCheck(root, assistant, result)
	if check.Status != doctor.StatusError {
		t.Fatalf("tampered asset descriptor check = %#v", check)
	}
}
