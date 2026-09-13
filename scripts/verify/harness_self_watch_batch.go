package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
)

// Exercise production watch timing, actual atomic saves and edits after Go
// compilation has begun. Unit tests retain fake-clock settling coverage.
func runHarnessWatchBatchProbe(ctx context.Context, root string, started detachedDevResult, current localagent.Session,
	readSession func() (localagent.Session, error),
	waitReplacement func(string, string, *build.CandidateIdentity, time.Time) (localagent.Session, build.CandidateIdentity, time.Duration, error),
) (map[string]any, error) {
	sourcePath := filepath.Join(root, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	anchor := []byte(`sharedprefix.Value("changed:")`)
	if bytes.Count(source, anchor) != 1 {
		return nil, fmt.Errorf("watch batch source anchor is missing")
	}
	logOffset := func() (int64, error) {
		info, err := os.Stat(started.LogPath)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	batchStart, err := logOffset()
	if err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(sourcePath, bytes.Replace(source, anchor, []byte(`sharedprefix.Value(watchPrefix())`), 1)); err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
		return nil, err
	}
	prefixPath := filepath.Join(root, "service/watch_batch.go")
	prefix := func(value string) []byte {
		return []byte(fmt.Sprintf("package service\nfunc watchPrefix() string { return %q }\n", value))
	}
	if err := os.WriteFile(prefixPath, prefix("batch:"), 0o600); err != nil {
		return nil, err
	}
	batchEditCompleted := time.Now()
	batched, batchedIdentity, batchLatency, err := waitReplacement(current.AppPID, "batch:handoff", nil, batchEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("atomic multi-file save did not serve the completed batch: %w", err)
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, batchStart, 1); err != nil {
		return nil, err
	}
	rapidStart, err := logOffset()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(prefixPath, prefix("rapid-one:"), 0o600); err != nil {
		return nil, err
	}
	for {
		events, err := harnessWatchEvents(started.LogPath, rapidStart)
		if err != nil {
			return nil, err
		}
		compiling := false
		for _, event := range events {
			if event.Type == "phase.start" && event.Data.Title == "Compiling application source code" {
				compiling = true
			}
		}
		if compiling {
			break
		}
		if err := harnessWaitContext(ctx, 10*time.Millisecond); err != nil {
			return nil, fmt.Errorf("watch candidate never reached Go compilation: %w", err)
		}
	}
	if err := os.WriteFile(prefixPath, prefix("rapid-two:"), 0o600); err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(prefixPath, prefix("rapid-final:")); err != nil {
		return nil, err
	}
	rapidEditCompleted := time.Now()
	final, finalIdentity, rapidLatency, err := waitReplacement(batched.AppPID, "rapid-final:handoff", nil, rapidEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("edit during compilation was dropped: %w", err)
	}
	generated := filepath.Join(root, "service/scenerycontract/types.gen.go")
	data, err := os.ReadFile(generated)
	if err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(generated, data); err != nil {
		return nil, err
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, rapidStart, 2); err != nil {
		return nil, err
	}
	roundtripStart, err := logOffset()
	if err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(prefixPath, prefix("batch:")); err != nil {
		return nil, err
	}
	roundtripEditCompleted := time.Now()
	roundtripped, roundtripIdentity, roundtripLatency, err := waitReplacement(final.AppPID, "batch:handoff", nil, roundtripEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("return to a previously compiled behavior did not activate the exact generation: %w", err)
	}
	if roundtripIdentity.ContractRevision != batchedIdentity.ContractRevision ||
		roundtripIdentity.ImplementationRevision != batchedIdentity.ImplementationRevision ||
		roundtripIdentity.BuildInputDigest != batchedIdentity.BuildInputDigest ||
		roundtripIdentity.Target != batchedIdentity.Target {
		return nil, fmt.Errorf("return to a previously compiled behavior changed its exact build identity")
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, roundtripStart, 1); err != nil {
		return nil, err
	}

	matrixStart, err := logOffset()
	if err != nil {
		return nil, err
	}
	dependencyPath := filepath.Join(root, "sharedprefix/prefix.go")
	if err := os.WriteFile(dependencyPath, []byte("package sharedprefix\nfunc Value(value string) string { return \"dependency:\" + value }\n"), 0o600); err != nil {
		return nil, err
	}
	dependencyEditCompleted := time.Now()
	dependency, dependencyIdentity, dependencyLatency, err := waitReplacement(roundtripped.AppPID, "dependency:batch:handoff", nil, dependencyEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("shared Go dependency edit did not serve new behavior: %w", err)
	}
	if dependencyIdentity.ImplementationRevision == finalIdentity.ImplementationRevision || dependencyIdentity.BuildInputDigest == finalIdentity.BuildInputDigest {
		return nil, fmt.Errorf("shared Go dependency edit retained stale implementation identity")
	}

	revisionInput := filepath.Join(root, "probe.input")
	if err := os.WriteFile(revisionInput, []byte("captured-input\n"), 0o600); err != nil {
		return nil, err
	}
	appSourcePath := filepath.Join(root, "app.scn")
	appSource, err := os.ReadFile(appSourcePath)
	if err != nil {
		return nil, err
	}
	declarationAnchor := []byte("  managed_generated_roots = [")
	declarationReplacement := []byte("  revision_input \"probe\" { paths = [\"probe.input\"] }\n\n  managed_generated_roots = [")
	if bytes.Count(appSource, declarationAnchor) != 1 {
		return nil, fmt.Errorf("declaration input anchor is missing")
	}
	if err := harnessAtomicWatchSave(appSourcePath, bytes.Replace(appSource, declarationAnchor, declarationReplacement, 1)); err != nil {
		return nil, err
	}
	declarationEditCompleted := time.Now()
	declaration, declarationIdentity, declarationLatency, err := waitReplacement(dependency.AppPID, "dependency:batch:handoff", nil, declarationEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("new declaration input did not rebuild from captured bytes: %w", err)
	}
	packagePath := filepath.Join(root, "service/package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		return nil, err
	}
	if bytes.Count(packageSource, []byte(`timeout   = "30s"`)) != 1 {
		return nil, fmt.Errorf("contract change anchor is missing")
	}
	if err := harnessAtomicWatchSave(packagePath, bytes.Replace(packageSource, []byte(`timeout   = "30s"`), []byte(`timeout   = "29s"`), 1)); err != nil {
		return nil, err
	}
	contractEditCompleted := time.Now()
	contract, contractIdentity, contractLatency, err := waitReplacement(declaration.AppPID, "dependency:batch:handoff", nil, contractEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("semantic contract edit did not preserve the endpoint: %w", err)
	}
	if contractIdentity.ContractRevision == declarationIdentity.ContractRevision {
		return nil, fmt.Errorf("semantic declaration edit retained stale contract identity")
	}

	configPath := filepath.Join(root, ".scenery.json")
	configSource, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	configAnchor := []byte(`{"name":"basicapp",`)
	if bytes.Count(configSource, configAnchor) != 1 {
		return nil, fmt.Errorf("config change anchor is missing")
	}
	if err := harnessAtomicWatchSave(configPath, bytes.Replace(configSource, configAnchor, []byte(`{"name":"basicapp","watch":{"ignore":["probe-ignore/"]},`), 1)); err != nil {
		return nil, err
	}
	configEditCompleted := time.Now()
	configured, _, configLatency, err := waitReplacement(contract.AppPID, "dependency:batch:handoff", nil, configEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("configuration edit did not preserve the endpoint: %w", err)
	}

	bannerPath := filepath.Join(root, "service/banner.txt")
	embedPath := filepath.Join(root, "service/embed.go")
	if err := os.WriteFile(bannerPath, []byte("embed:"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(embedPath, []byte("package service\n\nimport _ \"embed\"\n\n//go:embed banner.txt\nvar banner string\n"), 0o600); err != nil {
		return nil, err
	}
	currentSource, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	embedAnchor := []byte(`Message: sharedprefix.Value(watchPrefix())`)
	if bytes.Count(currentSource, embedAnchor) != 1 {
		return nil, fmt.Errorf("embedded input anchor is missing")
	}
	if err := harnessAtomicWatchSave(sourcePath, bytes.Replace(currentSource, embedAnchor, []byte(`Message: banner + sharedprefix.Value(watchPrefix())`), 1)); err != nil {
		return nil, err
	}
	embedSetupCompleted := time.Now()
	embedded, _, embedSetupLatency, err := waitReplacement(configured.AppPID, "embed:dependency:batch:handoff", nil, embedSetupCompleted)
	if err != nil {
		return nil, fmt.Errorf("new embedded input did not serve captured behavior: %w", err)
	}
	if err := harnessAtomicWatchSave(bannerPath, []byte("asset:")); err != nil {
		return nil, err
	}
	embedEditCompleted := time.Now()
	embeddedEdit, _, embedEditLatency, err := waitReplacement(embedded.AppPID, "asset:dependency:batch:handoff", nil, embedEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("embedded asset edit did not serve new bytes: %w", err)
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, matrixStart, 6); err != nil {
		return nil, err
	}
	stable, err := readSession()
	if err != nil || stable.AppPID != embeddedEdit.AppPID {
		return nil, fmt.Errorf("generated publication restarted the final generation: %v", err)
	}
	return map[string]any{
		"production_settle_ms": 100, "atomic_multifile_builds": 1,
		"inflight_batch_builds": 2, "rapid_final_response": true,
		"previous_behavior_roundtrip_verified": true,
		"generated_publication_no_extra_build": true,
		"shared_dependency_behavior_verified":  true,
		"new_revision_input_captured":          true,
		"semantic_contract_revision_changed":   true,
		"configuration_edit_verified":          true,
		"embedded_asset_behavior_verified":     true,
		"batch_edit_to_verified_response_ms":   float64(batchLatency.Microseconds()) / 1000,
		"rapid_edit_to_verified_response_ms":   float64(rapidLatency.Microseconds()) / 1000,
		"roundtrip_to_verified_response_ms":    float64(roundtripLatency.Microseconds()) / 1000,
		"shared_dependency_to_response_ms":     float64(dependencyLatency.Microseconds()) / 1000,
		"declaration_input_to_response_ms":     float64(declarationLatency.Microseconds()) / 1000,
		"contract_change_to_response_ms":       float64(contractLatency.Microseconds()) / 1000,
		"configuration_change_to_response_ms":  float64(configLatency.Microseconds()) / 1000,
		"embed_setup_to_response_ms":           float64(embedSetupLatency.Microseconds()) / 1000,
		"embedded_asset_to_response_ms":        float64(embedEditLatency.Microseconds()) / 1000,
	}, nil
}

func harnessAtomicWatchSave(path string, data []byte) error {
	temporary := path + ".save-tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	return os.Rename(temporary, path)
}

type harnessWatchEvent struct {
	Type string `json:"type"`
	Data struct {
		Title                     string   `json:"title"`
		OperationID               string   `json:"operation_id"`
		Name                      string   `json:"name"`
		StartedAt                 string   `json:"started_at"`
		DurationMS                float64  `json:"duration_ms"`
		QueueMS                   float64  `json:"queue_ms"`
		Cache                     string   `json:"cache"`
		Reason                    string   `json:"reason"`
		OK                        bool     `json:"ok"`
		Actions                   int      `json:"actions"`
		CacheHits                 int      `json:"cache_hits"`
		CacheMisses               int      `json:"cache_misses"`
		FilesWritten              int      `json:"files_written"`
		FilesRemoved              int      `json:"files_removed"`
		BytesWritten              int64    `json:"bytes_written"`
		ExecutableBytes           int64    `json:"executable_bytes"`
		PackagesRebuilt           []string `json:"packages_rebuilt"`
		PackagesRebuiltAvailable  bool     `json:"packages_rebuilt_available"`
		SnapshotDigest            string   `json:"snapshot_digest"`
		ContractRevision          string   `json:"contract_revision"`
		ImplementationRevision    string   `json:"implementation_revision"`
		BuildInputDigest          string   `json:"build_input_digest"`
		FrameworkSourceDigest     string   `json:"framework_source_digest"`
		FrameworkExecutableDigest string   `json:"framework_executable_digest"`
		GoTarget                  string   `json:"go_target"`
	} `json:"data"`
}

func harnessWatchEvents(path string, offset int64) ([]harnessWatchEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if offset > int64(len(data)) {
		return nil, fmt.Errorf("owned supervisor log was truncated")
	}
	var events []harnessWatchEvent
	for _, line := range bytes.Split(data[offset:], []byte("\n")) {
		var envelope struct {
			Data harnessWatchEvent `json:"data"`
		}
		if json.Unmarshal(line, &envelope) == nil {
			events = append(events, envelope.Data)
		}
	}
	return events, nil
}

func harnessAssertWatchBuildCount(ctx context.Context, log string, offset int64, want int) error {
	// Include a complete production backup-poll interval to catch feedback.
	if err := harnessWaitContext(ctx, 2500*time.Millisecond); err != nil {
		return err
	}
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return err
	}
	count := 0
	for _, event := range events {
		if event.Type == "process.compile-start" {
			count++
		}
	}
	if count != want {
		return fmt.Errorf("completed watch batch built %d times, want %d", count, want)
	}
	return nil
}
