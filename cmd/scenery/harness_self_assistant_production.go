package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/runtimeassets"
	sceneryruntime "scenery.sh/runtime"
)

const harnessAssistantProductionProbeName = "assistant production process probe"

type harnessAssistantProductionCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessAssistantProductionProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessAssistantProductionProbeStepWithCheck(ctx, repoRoot, runHarnessAssistantProductionProbeCheck)
}

func runHarnessAssistantProductionProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessAssistantProductionCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessAssistantProductionProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the production assistant asset/process lifecycle, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessAssistantProductionProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-assistant-production-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	stateRoot := filepath.Join(root, "state")
	asset, nodeArchive, err := buildHarnessAssistantProductionAsset(root)
	if err != nil {
		return nil, nil, err
	}

	restoreEnv := patchEnv(map[string]*string{
		sceneryruntime.AssistantTokenKeyEnv:      stringPtr(strings.Repeat("k", 32)),
		sceneryruntime.AssistantTokenKeyFileEnv:  nil,
		sceneryruntime.AssistantRuntimeConfigEnv: nil,
	})
	defer restoreEnv()
	const assistantAddress = "app/assistant/release_probe"
	const serverAddress = "app/mcp_server/release_probe"
	if err := sceneryruntime.RegisterAssistantChecked(sceneryruntime.AssistantRegistration{
		Address: assistantAddress, Name: "release_probe", Path: "/assistants/release-probe", Access: sceneryruntime.Public,
		AssistantAddress: assistantAddress, RuntimeRevision: "runtime-test", CapabilityRevision: "capability-test", Required: true,
	}); err != nil {
		return nil, nil, err
	}
	if err := sceneryruntime.RegisterNativeService(sceneryruntime.NativeServiceRegistration{
		Address: serverAddress, Initialize: func(context.Context) error { return nil },
	}); err != nil {
		return nil, nil, err
	}
	manifestJSON, err := mcpcontract.MarshalCanonical(mcpcontract.Manifest{
		Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision, ProtocolVersion: mcpcontract.ProtocolVersion,
		ContractRevision: "capability-test", Capabilities: []mcpcontract.Capability{}, Connections: []mcpcontract.Connection{},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := sceneryruntime.RegisterAssistantMCPManifestChecked(assistantAddress, serverAddress, manifestJSON); err != nil {
		return nil, nil, err
	}
	if err := sceneryruntime.RegisterEmbeddedAssistantAssets(sceneryruntime.AssistantProductionOptions{
		StateRoot: stateRoot, ApplicationID: "runtime-production-release-probe",
	}, []sceneryruntime.AssistantEmbeddedAsset{asset}); err != nil {
		return nil, nil, err
	}
	servicesStarted := false
	defer func() {
		if servicesStarted {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 7*time.Second)
			defer shutdownCancel()
			_ = sceneryruntime.ShutdownServices(shutdownCtx)
		}
	}()
	if err := sceneryruntime.InitializeServices(); err != nil {
		return nil, nil, err
	}
	servicesStarted = true

	configPath := filepath.Join(stateRoot, "runtime.json")
	config, err := sceneryruntime.LoadAssistantRuntimeConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	if len(config.Assistants) != 1 || config.Assistants[0].AssistantAddress != assistantAddress {
		return nil, nil, fmt.Errorf("production assistant config = %+v", config.Assistants)
	}
	markers, err := filepath.Glob(filepath.Join(stateRoot, "assistant-homes", "*", "production-probe"))
	if err != nil {
		return nil, nil, err
	}
	var marker []byte
	if err := waitForHarnessCondition(ctx, func() bool {
		markers, err = filepath.Glob(filepath.Join(stateRoot, "assistant-homes", "*", "production-probe"))
		if err != nil || len(markers) != 1 {
			return false
		}
		marker, err = os.ReadFile(markers[0])
		return err == nil && len(marker) > 0
	}); err != nil {
		return nil, nil, fmt.Errorf("wait for production assistant marker: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(marker)), "\n")
	if len(lines) != 3 {
		return nil, nil, fmt.Errorf("production assistant marker = %q", marker)
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil || pid <= 0 || !processAliveForEdge(pid) {
		return nil, nil, fmt.Errorf("production assistant PID = %q, err=%v", lines[0], err)
	}
	if lines[2] == "" {
		return nil, nil, fmt.Errorf("production assistant cwd/entry = %q/%q", lines[1], lines[2])
	}
	cwdInfo, err := os.Stat(lines[1])
	if err != nil {
		return nil, nil, err
	}
	homeInfo, err := os.Stat(filepath.Dir(markers[0]))
	if err != nil {
		return nil, nil, err
	}
	entryDirInfo, err := os.Stat(filepath.Dir(lines[2]))
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(cwdInfo, homeInfo) || os.SameFile(cwdInfo, entryDirInfo) {
		return nil, nil, fmt.Errorf("production assistant cwd/entry = %q/%q", lines[1], lines[2])
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 7*time.Second)
	err = sceneryruntime.ShutdownServices(shutdownCtx)
	shutdownCancel()
	servicesStarted = false
	if err != nil {
		return nil, nil, err
	}
	if err := waitForHarnessCondition(ctx, func() bool { return !processAliveForEdge(pid) }); err != nil {
		return nil, nil, fmt.Errorf("production assistant process remained alive: %w", err)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("production assistant config after shutdown: %v", err)
	}

	reused, err := runtimeassets.InstallContext(ctx, stateRoot, nodeArchive)
	if err != nil || !reused.Reused {
		return nil, nil, fmt.Errorf("verified node reuse = %+v, err=%v", reused, err)
	}
	nodePath := filepath.Join(reused.Path, "bin", "node")
	if err := os.WriteFile(nodePath, []byte("tampered"), 0o755); err != nil {
		return nil, nil, err
	}
	if _, err := runtimeassets.InstallContext(ctx, stateRoot, nodeArchive); !errors.Is(err, runtimeassets.ErrExistingInstallTampered) {
		return nil, nil, fmt.Errorf("tampered node install error = %v", err)
	}
	if err := os.RemoveAll(reused.Path); err != nil {
		return nil, nil, err
	}
	recovered, err := runtimeassets.InstallContext(ctx, stateRoot, nodeArchive)
	if err != nil || recovered.Reused {
		return nil, nil, fmt.Errorf("recovered node install = %+v, err=%v", recovered, err)
	}

	return map[string]any{
		"proof":              "production_assistant_real_process_ports_assets_reuse_tamper_and_recovery_verified",
		"pid":                pid,
		"config_descriptors": len(config.Assistants),
		"verified_reuse":     true,
		"tamper_rejected":    true,
		"recovered":          true,
	}, nil, nil
}

func buildHarnessAssistantProductionAsset(root string) (sceneryruntime.AssistantEmbeddedAsset, runtimeassets.Archive, error) {
	const assistantAddress = "app/assistant/release_probe"
	nodeRoot := filepath.Join(root, "node")
	if err := os.MkdirAll(filepath.Join(nodeRoot, "bin"), 0o755); err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	const nodeScript = `#!/bin/sh
set -eu
printf '%s\n%s\n%s\n' "$$" "$PWD" "$1" > "$HOME/production-probe"
trap 'exit 0' TERM INT
while :; do /bin/sleep 1; done
`
	if err := os.WriteFile(filepath.Join(nodeRoot, "bin", "node"), []byte(nodeScript), 0o755); err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	capsuleRoot := filepath.Join(root, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleRoot, ".scenery"), 0o755); err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	if err := os.WriteFile(filepath.Join(capsuleRoot, ".scenery", "bootstrap.mjs"), []byte("// release process probe\n"), 0o644); err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	nodeArchive, err := runtimeassets.BuildArchive(nodeRoot)
	if err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	capsuleArchive, err := runtimeassets.BuildArchive(capsuleRoot)
	if err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	descriptor := sceneryruntime.AssistantAssetDescriptor{
		Kind: runtimeassets.AssistantAssetKind, SchemaRevision: runtimeassets.AssistantAssetSchemaRevision,
		AssistantAddress: assistantAddress, Target: goruntime.GOOS + "/" + goruntime.GOARCH,
		RuntimeRevision: "runtime-test", CapabilityRevision: "capability-test",
		NodeArchiveDigest: nodeArchive.ArchiveDigest, NodeTreeDigest: nodeArchive.Descriptor.Digest,
		CapsuleArchiveDigest: capsuleArchive.ArchiveDigest, CapsuleTreeDigest: capsuleArchive.Descriptor.Digest,
		CapsuleEntry: ".scenery/bootstrap.mjs", PackageLockDigest: "sha256:" + strings.Repeat("a", 64),
	}
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	nodeDescriptorJSON, err := json.Marshal(nodeArchive.Descriptor)
	if err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	capsuleDescriptorJSON, err := json.Marshal(capsuleArchive.Descriptor)
	if err != nil {
		return sceneryruntime.AssistantEmbeddedAsset{}, runtimeassets.Archive{}, err
	}
	return sceneryruntime.AssistantEmbeddedAsset{
		Descriptor: descriptor, DescriptorJSON: descriptorJSON,
		NodeArchive: nodeArchive.Data, NodeDescriptorJSON: nodeDescriptorJSON,
		CapsuleArchive: capsuleArchive.Data, CapsuleDescriptorJSON: capsuleDescriptorJSON,
	}, nodeArchive, nil
}
