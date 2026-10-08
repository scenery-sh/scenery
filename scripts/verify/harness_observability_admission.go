package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/toolchain"
)

const harnessAdmissionCounter = `vt_rows_dropped_total{reason="too_many_fields"}`

type harnessAdmissionSnapshot struct {
	Owner          localagent.Owner `json:"owner"`
	ObservedBefore localagent.Owner `json:"observed_before"`
	ObservedAfter  localagent.Owner `json:"observed_after"`
	Counter        uint64           `json:"too_many_fields_rows"`
}

// The pinned backend exposes this exact series even at zero. Absence and
// malformed/duplicate values are missing evidence, never an implicit zero.
func parseHarnessAdmissionCounter(text string) (uint64, error) {
	var value uint64
	found := false
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != harnessAdmissionCounter {
			continue
		}
		if found || len(fields) != 2 {
			return 0, errors.New("backend admission counter is duplicated or malformed")
		}
		var err error
		value, err = strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("backend admission counter: %w", err)
		}
		found = true
	}
	if !found {
		return 0, errors.New("backend admission counter is absent")
	}
	return value, nil
}

func harnessAdmissionDelta(before, after harnessAdmissionSnapshot) (uint64, error) {
	a, b := before.Owner, after.Owner
	if a.PID <= 0 || a.StartedAt == "" || a.Exe == "" || a.CmdlineHash == "" ||
		a.PID != b.PID || a.StartedAt != b.StartedAt || a.Exe != b.Exe || a.CmdlineHash != b.CmdlineHash {
		return 0, errors.New("backend admission process identity is missing or changed")
	}
	if after.Counter < before.Counter {
		return 0, errors.New("backend admission counter reset")
	}
	return after.Counter - before.Counter, nil
}

// Verify public event detail, including identity and microsecond timestamps;
// matching counts alone would let a duplicate conceal a missing occurrence.
func verifyHarnessAdmissionEvents(events []devdash.TraceDetailEvent, count int) error {
	if len(events) != count {
		return fmt.Errorf("admission control has %d events, want %d", len(events), count)
	}
	seen := make(map[int]bool, count)
	for _, event := range events {
		data, ok := event.Data["admission"].(map[string]any)
		ordinal, numeric := data["ordinal"].(float64)
		index := int(ordinal)
		if !ok || !numeric || ordinal != float64(index) || index < 1 || index > count || seen[index] ||
			event.Name != "scenery.admission" || data["kind"] != "control" ||
			event.Time.IsZero() || data["time"] != event.Time.UTC().Format(time.RFC3339Nano) {
			return errors.New("admission control event identity/time/data is missing or duplicated")
		}
		seen[index] = true
	}
	return nil
}

// Isolate the real backend row boundary from buffer eviction. This deliberately
// demonstrates loss evidence; production serialization and delivery stay intact.
func proveHarnessBackendAdmission(ctx context.Context, p *worktreeRuntimeProbe, paths localagent.WorktreePaths, agent *localagent.Client, substrate localagent.Substrate, rpc *runtimeBoundsConnection, appID, api string, selectEvents *atomic.Int64, hostPID, buildDigest string, artifacts harnessArtifactContext) (map[string]any, error) {
	owner := substrate.Owners["traces"]
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return nil, err
	}
	store, err := toolchain.NewStore(filepath.Join(p.repo, ".scenery/harness/worktree-runtime/victoria-toolchain"), manifest)
	if err != nil {
		return nil, err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	installed, err := store.Path(ctx, "victoria-traces", toolchain.CurrentPlatform())
	if err != nil || installed.Status != "installed" || installed.Version != "v0.9.2" || installed.ManagedPath == "" {
		return nil, fmt.Errorf("admission proof requires pinned VictoriaTraces v0.9.2: %v", err)
	}
	managed, err := filepath.EvalSymlinks(installed.ManagedPath)
	if err != nil {
		return nil, err
	}
	executable, err := filepath.EvalSymlinks(owner.Exe)
	if err != nil || managed != executable {
		return nil, errors.New("admission backend is not the pinned managed binary")
	}
	digest, err := worktreeProbeFileSHA(managed)
	if err != nil {
		return nil, err
	}
	// Admission evidence requires a complete observed fingerprint and exact
	// executable path, beyond the general owner's tolerant verification policy.
	observe := func(candidate localagent.Owner) (localagent.Owner, error) {
		live := localagent.CaptureOwner(candidate.PID, "")
		_, err := harnessAdmissionDelta(harnessAdmissionSnapshot{Owner: candidate}, harnessAdmissionSnapshot{Owner: live})
		return live, err
	}
	read := func() (harnessAdmissionSnapshot, error) {
		current, err := agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
		if err != nil || current.Status != "ready" || current.PIDs["traces"] != owner.PID || current.URLs["traces"] != substrate.URLs["traces"] {
			return harnessAdmissionSnapshot{}, fmt.Errorf("backend admission substrate identity changed: %v", err)
		}
		candidate := current.Owners["traces"]
		if _, err := harnessAdmissionDelta(harnessAdmissionSnapshot{Owner: owner}, harnessAdmissionSnapshot{Owner: candidate}); err != nil {
			return harnessAdmissionSnapshot{}, err
		}
		observedBefore, err := observe(candidate)
		if err != nil {
			return harnessAdmissionSnapshot{}, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(current.URLs["traces"], "/")+"/metrics", nil)
		if err != nil {
			return harnessAdmissionSnapshot{}, err
		}
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			return harnessAdmissionSnapshot{}, err
		}
		defer func() { _ = response.Body.Close() }()
		const maxBytes = 1 << 20
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
		if err != nil || response.StatusCode != http.StatusOK || len(body) > maxBytes {
			return harnessAdmissionSnapshot{}, fmt.Errorf("backend admission metrics HTTP %d/read budget: %v", response.StatusCode, err)
		}
		counter, err := parseHarnessAdmissionCounter(string(body))
		if err != nil {
			return harnessAdmissionSnapshot{}, err
		}
		observedAfter, err := observe(candidate)
		if err != nil {
			return harnessAdmissionSnapshot{}, err
		}
		return harnessAdmissionSnapshot{Owner: candidate, ObservedBefore: observedBefore, ObservedAfter: observedAfter, Counter: counter}, nil
	}
	status := func() (map[string]uint64, error) {
		response, err := rpc.call(20, "status", map[string]any{}, 5*time.Second)
		if err != nil || response.Error != nil {
			return nil, fmt.Errorf("admission status: %v %+v", err, response.Error)
		}
		var value struct {
			Observability struct {
				Export map[string]uint64 `json:"export"`
			} `json:"observability"`
		}
		if err := json.Unmarshal(response.Result, &value); err != nil {
			return nil, err
		}
		counters := map[string]uint64{}
		for _, key := range []string{"dropped", "failed", "trace_buffer_dropped_events"} {
			v, present := value.Observability.Export[key]
			if !present {
				return nil, fmt.Errorf("admission status lacks %s", key)
			}
			counters[key] = v
		}
		return counters, nil
	}
	before, err := read()
	if err != nil {
		return nil, err
	}
	beforeLoss, err := status()
	if err != nil {
		return nil, err
	}
	selectEvents.Store(-1)
	defer selectEvents.Store(0)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/books", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	selectEvents.Store(0)
	if readErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("X-Scenery-Process-ID") != hostPID || response.Header.Get("X-Scenery-Build-Input-Digest") != buildDigest {
		return nil, fmt.Errorf("admission fixture response/identity HTTP %d: %v", response.StatusCode, readErr)
	}
	readback, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	var after harnessAdmissionSnapshot
	var lastErr error
	if err := waitForHarnessCondition(readback, func() bool {
		after, lastErr = read()
		if lastErr != nil {
			return true
		}
		delta, err := harnessAdmissionDelta(before, after)
		lastErr = err
		return err != nil || delta >= 1
	}); err != nil || lastErr != nil {
		return nil, fmt.Errorf("backend admission rejection evidence: %w", errors.Join(err, lastErr))
	}
	delta, err := harnessAdmissionDelta(before, after)
	if err != nil || delta != 1 {
		return nil, fmt.Errorf("backend admission rejection delta=%d, want1: %v", delta, err)
	}
	query := func(traceID string) (devdash.TraceDetail, error) {
		response, err := rpc.call(21, "traces/get", map[string]any{"app_id": appID, "trace_id": traceID}, 5*time.Second)
		if err != nil || response.Error != nil {
			return devdash.TraceDetail{}, fmt.Errorf("admission scoped trace query: %v %+v", err, response.Error)
		}
		var detail devdash.TraceDetail
		err = json.Unmarshal(response.Result, &detail)
		return detail, err
	}
	var control devdash.TraceDetail
	if err := waitForHarnessCondition(readback, func() bool {
		control, lastErr = query(strings.Repeat("c", 32))
		if lastErr == nil && len(control.Spans) == 1 {
			lastErr = verifyHarnessAdmissionEvents(control.Spans[0].Events, 196)
			return lastErr == nil && control.Spans[0].SpanID == "0000000000000001" && !control.Spans[0].IsError
		}
		return false
	}); err != nil {
		return nil, fmt.Errorf("backend admission healthy control: %w: %v", err, lastErr)
	}
	rejected, err := query(strings.Repeat("d", 32))
	if err != nil || len(rejected.Spans) != 0 {
		return nil, fmt.Errorf("backend rejected span readback has%d spans: %v", len(rejected.Spans), err)
	}
	after, err = read()
	if err != nil {
		return nil, err
	}
	delta, err = harnessAdmissionDelta(before, after)
	if err != nil || delta != 1 {
		return nil, fmt.Errorf("final backend rejection delta=%d, want1: %v", delta, err)
	}
	finalDigest, err := worktreeProbeFileSHA(managed)
	if err != nil || finalDigest != digest {
		return nil, fmt.Errorf("backend admission binary changed during proof: %v", err)
	}
	// The owned backend diagnostic confirms the derived field shape too; a
	// changed encoding must not silently retain a stale boundary claim.
	backendLog, err := os.ReadFile(filepath.Join(paths.ControlPaths().AgentDir, "victoria", "logs", "victoria.traces.stderr.log"))
	if err != nil {
		return nil, fmt.Errorf("backend admission diagnostic: %w", err)
	}
	if !strings.Contains(string(backendLog), "dropping log line with 1005 fields; it exceeds -insert.maxFieldsPerLine=1000;") {
		return nil, errors.New("backend admission diagnostic does not confirm the derived field boundary")
	}
	afterLoss, err := status()
	if err != nil {
		return nil, err
	}
	for key, value := range beforeLoss {
		if afterLoss[key] != value {
			return nil, fmt.Errorf("backend rejection incorrectly charged %s: before%d after%d", key, value, afterLoss[key])
		}
	}
	data, err := json.Marshal(control)
	if err != nil {
		return nil, err
	}
	controlArtifact, err := artifacts.Write("backend admission control", "observability-admission-control.json", "", data)
	if err != nil {
		return nil, err
	}
	return map[string]any{"backend_version": installed.Version, "backend_binary_sha256": "sha256:" + digest, "toolchain_manifest_sha256": toolchain.BundledManifestSHA256(), "before": before, "after": after, "backend_rejected_rows": delta, "field_limit": 1000, "control_fields": 1000, "rejected_fields": 1005, "control_events": 196, "rejected_events": 197, "control": controlArtifact, "rejected_trace_id": rejected.TraceID, "rejected_span_absent": true, "scenery_loss_counters_unchanged": true, "intake_status": 204, "export_transport": "source-supported success inference; exporter HTTP response not captured", "host_pid": hostPID, "build_input_digest": buildDigest}, nil
}
