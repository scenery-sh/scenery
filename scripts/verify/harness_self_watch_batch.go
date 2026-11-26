package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Exercise production watch timing, actual atomic saves and edits after Go
// compilation has begun. Unit tests retain fake-clock settling coverage.
func runHarnessWatchBatchProbe(ctx context.Context, root string, started detachedDevResult, current harnessServedIdentity,
	served func(string, *harnessServedIdentity) (harnessServedIdentity, string, time.Duration, error),
	waitReplacement func(harnessServedIdentity, string, *harnessServedIdentity, time.Time) (harnessServedIdentity, string, time.Duration, error),
	sourceServed func() (harnessServedIdentity, error),
) (map[string]any, harnessServedIdentity, error) {
	// waitBuilt waits for the successful build of an edit that need not change
	// the served build, then for a verified answer.
	waitBuilt := func(offset int64, response string, editCompleted time.Time) (harnessServedIdentity, time.Duration, error) {
		if err := harnessWaitBuildRequest(ctx, started.LogPath, offset, true); err != nil {
			return harnessServedIdentity{}, 0, err
		}
		for {
			requestStarted := time.Now()
			identity, _, latency, err := served(response, nil)
			if err == nil {
				return identity, requestStarted.Add(latency).Sub(editCompleted), nil
			}
			if waitErr := harnessWaitContext(ctx, 100*time.Millisecond); waitErr != nil {
				return harnessServedIdentity{}, 0, fmt.Errorf("%w: %v", waitErr, err)
			}
		}
	}
	sourcePath := filepath.Join(root, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	anchor := []byte(`sharedprefix.Value("changed:")`)
	if bytes.Count(source, anchor) != 1 {
		return nil, harnessServedIdentity{}, fmt.Errorf("watch batch source anchor is missing")
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
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(sourcePath, bytes.Replace(source, anchor, []byte(`sharedprefix.Value(watchPrefix())`), 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	prefixPath := filepath.Join(root, "service/watch_batch.go")
	prefix := func(value string) []byte {
		return []byte(fmt.Sprintf("package service\nfunc watchPrefix() string { return %q }\n", value))
	}
	if err := os.WriteFile(prefixPath, prefix("batch:"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	batchEditCompleted := time.Now()
	batchedIdentity, _, batchLatency, err := waitReplacement(current, "batch:handoff", nil, batchEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("atomic multi-file save did not serve the completed batch: %w", err)
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, batchStart, 1); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	rapidStart, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := os.WriteFile(prefixPath, prefix("rapid-one:"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	// The host detects an edit within its scan interval and then compiles for
	// several hundred milliseconds; the next edit lands during that build.
	if err := harnessWaitContext(ctx, 150*time.Millisecond); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := os.WriteFile(prefixPath, prefix("rapid-two:"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(prefixPath, prefix("rapid-final:")); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	rapidEditCompleted := time.Now()
	finalIdentity, _, rapidLatency, err := waitReplacement(batchedIdentity, "rapid-final:handoff", nil, rapidEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("edit during compilation was dropped: %w", err)
	}
	generated := filepath.Join(root, "service/scenerycontract/types.gen.go")
	data, err := os.ReadFile(generated)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(generated, data); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	// Edits during a build coalesce: at most the build in progress and one
	// more, never a feedback loop or a dropped final edit.
	if err := harnessAssertWatchBuildCountWithin(ctx, started.LogPath, rapidStart, 1, 3); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	roundtripStart, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(prefixPath, prefix("batch:")); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	roundtripEditCompleted := time.Now()
	roundtripIdentity, _, roundtripLatency, err := waitReplacement(finalIdentity, "batch:handoff", nil, roundtripEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("return to a previously compiled behavior did not activate the exact generation: %w", err)
	}
	if !harnessSameBuild(roundtripIdentity, batchedIdentity) {
		return nil, harnessServedIdentity{}, fmt.Errorf("return to a previously compiled behavior changed its exact build identity")
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, roundtripStart, 1); err != nil {
		return nil, harnessServedIdentity{}, err
	}

	matrixStart, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	dependencyPath := filepath.Join(root, "sharedprefix/prefix.go")
	if err := os.WriteFile(dependencyPath, []byte("package sharedprefix\nfunc Value(value string) string { return \"dependency:\" + value }\n"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	dependencyEditCompleted := time.Now()
	dependencyIdentity, _, dependencyLatency, err := waitReplacement(roundtripIdentity, "dependency:batch:handoff", nil, dependencyEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("shared Go dependency edit did not serve new behavior: %w", err)
	}
	if dependencyIdentity.ImplementationRevision == finalIdentity.ImplementationRevision || dependencyIdentity.BuildInputDigest == finalIdentity.BuildInputDigest {
		return nil, harnessServedIdentity{}, fmt.Errorf("shared Go dependency edit retained stale implementation identity")
	}

	revisionInput := filepath.Join(root, "probe.input")
	if err := os.WriteFile(revisionInput, []byte("captured-input\n"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	appSourcePath := filepath.Join(root, "app.scn")
	appSource, err := os.ReadFile(appSourcePath)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	declarationAnchor := []byte("  managed_generated_roots = [")
	declarationReplacement := []byte("  revision_input \"probe\" { paths = [\"probe.input\"] }\n\n  managed_generated_roots = [")
	if bytes.Count(appSource, declarationAnchor) != 1 {
		return nil, harnessServedIdentity{}, fmt.Errorf("declaration input anchor is missing")
	}
	// A declared revision input no Go target consumes changes no served build
	// identity; its capture is proven by a successful build of the edit.
	declarationOffset, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(appSourcePath, bytes.Replace(appSource, declarationAnchor, declarationReplacement, 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	declarationEditCompleted := time.Now()
	declarationIdentity, declarationLatency, err := waitBuilt(declarationOffset, "dependency:batch:handoff", declarationEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("new declaration input did not rebuild from captured bytes: %w", err)
	}
	packagePath := filepath.Join(root, "service/package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if bytes.Count(packageSource, []byte(`timeout   = "30s"`)) != 1 {
		return nil, harnessServedIdentity{}, fmt.Errorf("contract change anchor is missing")
	}
	if err := harnessAtomicWatchSave(packagePath, bytes.Replace(packageSource, []byte(`timeout   = "30s"`), []byte(`timeout   = "29s"`), 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	contractEditCompleted := time.Now()
	contractIdentity, _, contractLatency, err := waitReplacement(declarationIdentity, "dependency:batch:handoff", nil, contractEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("semantic contract edit did not preserve the endpoint: %w", err)
	}
	if contractIdentity.ContractRevision == declarationIdentity.ContractRevision {
		return nil, harnessServedIdentity{}, fmt.Errorf("semantic declaration edit retained stale contract identity")
	}

	configPath := filepath.Join(root, ".scenery.json")
	configSource, err := os.ReadFile(configPath)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	configAnchor := []byte(`{"name":"basicapp",`)
	if bytes.Count(configSource, configAnchor) != 1 {
		return nil, harnessServedIdentity{}, fmt.Errorf("config change anchor is missing")
	}
	configOffset, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(configPath, bytes.Replace(configSource, configAnchor, []byte(`{"name":"basicapp","watch":{"ignore":["probe-ignore/"]},`), 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	configEditCompleted := time.Now()
	configuredIdentity, configLatency, err := waitBuilt(configOffset, "dependency:batch:handoff", configEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("configuration edit did not preserve the endpoint: %w", err)
	}

	bannerPath := filepath.Join(root, "service/banner.txt")
	embedPath := filepath.Join(root, "service/embed.go")
	if err := os.WriteFile(bannerPath, []byte("embed:"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := os.WriteFile(embedPath, []byte("package service\n\nimport _ \"embed\"\n\n//go:embed banner.txt\nvar banner string\n"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	currentSource, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	embedAnchor := []byte(`Message: sharedprefix.Value(watchPrefix())`)
	if bytes.Count(currentSource, embedAnchor) != 1 {
		return nil, harnessServedIdentity{}, fmt.Errorf("embedded input anchor is missing")
	}
	if err := harnessAtomicWatchSave(sourcePath, bytes.Replace(currentSource, embedAnchor, []byte(`Message: banner + sharedprefix.Value(watchPrefix())`), 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	embedSetupCompleted := time.Now()
	embeddedSetupIdentity, _, embedSetupLatency, err := waitReplacement(configuredIdentity, "embed:dependency:batch:handoff", nil, embedSetupCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("new embedded input did not serve captured behavior: %w", err)
	}
	if err := harnessAtomicWatchSave(bannerPath, []byte("asset:")); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	embedEditCompleted := time.Now()
	embeddedIdentity, embeddedService, embedEditLatency, err := waitReplacement(embeddedSetupIdentity, "asset:dependency:batch:handoff", nil, embedEditCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("embedded asset edit did not serve new bytes: %w", err)
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, matrixStart, 6); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if _, stableService, _, err := served("asset:dependency:batch:handoff", &embeddedIdentity); err != nil || stableService != embeddedService {
		return nil, harnessServedIdentity{}, fmt.Errorf("generated publication restarted the final generation: service %s, want %s: %v", stableService, embeddedService, err)
	}
	// A contract edit whose builds fail must not outlive its own revert: the
	// provisional graph a failed build discovered is membership scope, never an
	// accepted graph the next build may reuse for returning declarations.
	brokenPath := filepath.Join(root, "service/watch_broken.go")
	failingStart, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := os.WriteFile(brokenPath, []byte("package service\n\nfunc watchBroken() { undefinedWatchSymbol() }\n"), 0o600); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, failingStart, false); err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("broken implementation did not fail its build: %w", err)
	}
	failingStart, err = logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(packagePath, bytes.Replace(packageSource, []byte(`timeout   = "30s"`), []byte(`timeout   = "28s"`), 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, failingStart, false); err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("contract edit during a failing build did not fail: %w", err)
	}
	failingStart, err = logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessAtomicWatchSave(packagePath, bytes.Replace(packageSource, []byte(`timeout   = "30s"`), []byte(`timeout   = "29s"`), 1)); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, failingStart, false); err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("returning the contract during a failing build did not fail: %w", err)
	}
	if err := os.Remove(brokenPath); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	// The repaired source equals the inputs of the generation that kept
	// serving, so the host serves it without another build.
	returnedIdentity, err := sourceServed()
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("repaired implementation did not serve the returned contract: %w", err)
	}
	if _, _, _, err := served("asset:dependency:batch:handoff", &returnedIdentity); err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("repaired implementation did not serve the returned contract: %w", err)
	}
	if returnedIdentity.ContractRevision != embeddedIdentity.ContractRevision {
		return nil, harnessServedIdentity{}, fmt.Errorf("returning a contract edit served %s, want the previously compiled %s", returnedIdentity.ContractRevision, embeddedIdentity.ContractRevision)
	}

	churnOffset, err := logOffset()
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	churnCurrent, churnService := returnedIdentity, embeddedService
	churnIdentities := map[string]struct{}{}
	churnLatencies := make([]float64, 0, 20)
	for ordinal := 1; ordinal <= 20; ordinal++ {
		value := fmt.Sprintf("churn-%03d:", ordinal)
		if err := harnessAtomicWatchSave(prefixPath, prefix(value)); err != nil {
			return nil, harnessServedIdentity{}, err
		}
		completed := time.Now()
		identity, service, latency, err := waitReplacement(churnCurrent, "asset:dependency:"+value+"handoff", nil, completed)
		if err != nil {
			return nil, harnessServedIdentity{}, fmt.Errorf("unique churn edit %d did not serve its generation: %w", ordinal, err)
		}
		if _, duplicate := churnIdentities[identity.ImplementationRevision]; duplicate {
			return nil, harnessServedIdentity{}, fmt.Errorf("unique churn edit %d reused an implementation revision", ordinal)
		}
		churnIdentities[identity.ImplementationRevision] = struct{}{}
		churnLatencies = append(churnLatencies, float64(latency.Microseconds())/1000)
		churnCurrent, churnService = identity, service
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, churnOffset, 20); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	nativePath := filepath.Join(root, "nativevalue/native.c")
	nativeSource, err := os.ReadFile(nativePath)
	if err != nil {
		return nil, harnessServedIdentity{}, err
	}
	nativeChanged := bytes.Replace(nativeSource, []byte("return 7;"), []byte("return 3 + 4;"), 1)
	if bytes.Equal(nativeSource, nativeChanged) {
		return nil, harnessServedIdentity{}, fmt.Errorf("native input edit anchor is missing")
	}
	if err := harnessAtomicWatchSave(nativePath, nativeChanged); err != nil {
		return nil, harnessServedIdentity{}, err
	}
	nativeCompleted := time.Now()
	nativeIdentity, nativeService, nativeLatency, err := waitReplacement(churnCurrent, "asset:dependency:churn-020:handoff", nil, nativeCompleted)
	if err != nil {
		return nil, harnessServedIdentity{}, fmt.Errorf("behavior-preserving native input edit did not activate: %w", err)
	}
	if nativeService == churnService || nativeIdentity.ImplementationRevision == "" {
		return nil, harnessServedIdentity{}, fmt.Errorf("native input edit retained the previous execution generation")
	}
	slices.Sort(churnLatencies)
	return map[string]any{
		"production_settle_ms": 100, "atomic_multifile_builds": 1,
		"inflight_batch_builds": 2, "rapid_final_response": true,
		"previous_behavior_roundtrip_verified":  true,
		"generated_publication_no_extra_build":  true,
		"shared_dependency_behavior_verified":   true,
		"new_revision_input_captured":           true,
		"semantic_contract_revision_changed":    true,
		"configuration_edit_verified":           true,
		"embedded_asset_behavior_verified":      true,
		"unique_churn_edits_verified":           len(churnIdentities),
		"unique_churn_latency":                  nativeReloadStats(churnLatencies),
		"native_input_behavior_preserved":       true,
		"native_input_new_generation":           true,
		"native_input_to_response_ms":           float64(nativeLatency.Microseconds()) / 1000,
		"native_served_implementation_revision": nativeIdentity.ImplementationRevision,
		"native_served_build_input_digest":      nativeIdentity.BuildInputDigest,
		"batch_edit_to_verified_response_ms":    float64(batchLatency.Microseconds()) / 1000,
		"rapid_edit_to_verified_response_ms":    float64(rapidLatency.Microseconds()) / 1000,
		"roundtrip_to_verified_response_ms":     float64(roundtripLatency.Microseconds()) / 1000,
		"shared_dependency_to_response_ms":      float64(dependencyLatency.Microseconds()) / 1000,
		"declaration_input_to_response_ms":      float64(declarationLatency.Microseconds()) / 1000,
		"contract_change_to_response_ms":        float64(contractLatency.Microseconds()) / 1000,
		"configuration_change_to_response_ms":   float64(configLatency.Microseconds()) / 1000,
		"embed_setup_to_response_ms":            float64(embedSetupLatency.Microseconds()) / 1000,
		"embedded_asset_to_response_ms":         float64(embedEditLatency.Microseconds()) / 1000,
	}, nativeIdentity, nil
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
		Output                    string   `json:"output"`
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
		WrittenPaths              []string `json:"written_paths"`
		RemovedPaths              []string `json:"removed_paths"`
		ExecutableBytes           int64    `json:"executable_bytes"`
		FilesHashed               int      `json:"files_hashed"`
		BytesHashed               int64    `json:"bytes_hashed"`
		FilesReused               int      `json:"files_reused"`
		BytesReused               int64    `json:"bytes_reused"`
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

// harnessWaitBuildRequest waits for a build after offset and requires its
// outcome: a supervisor start of the host or a rebuild the running host
// reported. A previously serving build can finish logging after the edit.
func harnessWaitBuildRequest(ctx context.Context, log string, offset int64, ok bool) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		events, err := harnessWatchEvents(log, offset)
		if err != nil {
			return err
		}
		if rebuilds := harnessHostRebuilds(events); len(rebuilds) > 0 {
			if rebuilds[0].Failed == ok {
				return fmt.Errorf("host rebuild ok=%t, want %t", !rebuilds[0].Failed, ok)
			}
			return nil
		}
		queued := map[string]bool{}
		for _, event := range events {
			if event.Type != "build.step" || event.Data.OperationID == "" {
				continue
			}
			if event.Data.Name == "build.queue" {
				queued[event.Data.OperationID] = true
			}
			if event.Data.Name == "build.request" && queued[event.Data.OperationID] {
				if event.Data.OK != ok {
					return fmt.Errorf("build request ok=%t, want %t", event.Data.OK, ok)
				}
				return nil
			}
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}
	events, _ := harnessWatchEvents(log, offset)
	var seen []string
	for _, event := range events {
		switch {
		case event.Type == "process.output":
			seen = append(seen, strings.TrimSpace(event.Data.Output))
		case event.Type == "build.step" && (event.Data.Name == "build.queue" || event.Data.Name == "build.request"):
			seen = append(seen, fmt.Sprintf("%s %s ok=%t", event.Data.Name, event.Data.OperationID, event.Data.OK))
		case event.Type != "build.step" && event.Type != "":
			seen = append(seen, event.Type)
		}
	}
	data, _ := os.ReadFile(log)
	return fmt.Errorf("no build request completed after the edit (log %d bytes, offset %d); observed: %s; log tail: %s", len(data), offset, tailString(strings.Join(seen, "\n"), 3000), tailString(string(data), 3000))
}

// harnessAssertWatchBuildCount requires the host to have rebuilt exactly want
// times after offset.
func harnessAssertWatchBuildCount(ctx context.Context, log string, offset int64, want int) error {
	return harnessAssertWatchBuildCountWithin(ctx, log, offset, want, want)
}

// harnessAssertHostPublications requires the host to have published exactly
// want rebuilt generations after offset.
func harnessAssertHostPublications(ctx context.Context, log string, offset int64, want int) error {
	if err := harnessWaitContext(ctx, 2500*time.Millisecond); err != nil {
		return err
	}
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return err
	}
	published := 0
	for _, rebuild := range harnessHostRebuilds(events) {
		if !rebuild.Failed && len(rebuild.Rebuilt) > 0 {
			published++
		}
	}
	if published != want {
		return fmt.Errorf("host published %d rebuilt generations, want %d", published, want)
	}
	return nil
}

// harnessAssertWatchBuildCountWithin requires the host to have rebuilt
// between least and most times after offset.
func harnessAssertWatchBuildCountWithin(ctx context.Context, log string, offset int64, least, most int) error {
	// Include a complete scan interval to catch feedback.
	if err := harnessWaitContext(ctx, 2500*time.Millisecond); err != nil {
		return err
	}
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return err
	}
	if count := len(harnessHostRebuilds(events)); count < least || count > most {
		return fmt.Errorf("completed watch batch built %d times, want %d to %d", count, least, most)
	}
	return nil
}

// Nearest-rank quantiles match the existing edit benchmark. Small series are
// labelled as early evidence, never as a 30-edit acceptance distribution.
func nativeReloadStats(values []float64) map[string]any {
	if len(values) == 0 {
		return map[string]any{"count": 0}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	var sum float64
	for _, value := range ordered {
		sum += value
	}
	return map[string]any{"count": len(ordered), "p50_ms": ordered[(len(ordered)-1)/2],
		"p95_ms": ordered[(95*len(ordered)+99)/100-1], "worst_ms": ordered[len(ordered)-1], "mean_ms": sum / float64(len(ordered))}
}
