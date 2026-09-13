package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
)

const (
	harnessEditLatencySamples = 30
	harnessEditLatencyWarmups = 2
)

type harnessEditLatencyLane struct {
	label       string
	appRoot     string
	sessionRoot string
	home        string
	binary      string
	env         []string
	started     detachedDevResult
	framework   map[string]string
	client      *localagent.Client
	apiURL      string
	sourcePath  string
	source      []byte
	seen        map[string]struct{}
}

type harnessEditLatencySample struct {
	Lane                string                    `json:"lane"`
	Ordinal             int                       `json:"ordinal"`
	Behavior            string                    `json:"behavior"`
	EditCompletedAt     string                    `json:"edit_completed_at"`
	ResponseCompletedAt string                    `json:"response_completed_at,omitempty"`
	EditToResponseMS    float64                   `json:"edit_to_response_ms,omitempty"`
	MatchingRequestMS   float64                   `json:"matching_request_ms,omitempty"`
	OwnershipObservedMS float64                   `json:"ownership_observed_ms,omitempty"`
	Polls               int                       `json:"polls,omitempty"`
	TransientFailures   int                       `json:"transient_failures,omitempty"`
	ProcessID           string                    `json:"process_id,omitempty"`
	Identity            build.CandidateIdentity   `json:"identity,omitempty"`
	OperationID         string                    `json:"operation_id,omitempty"`
	Phases              []harnessEditLatencyPhase `json:"phases,omitempty"`
	Error               string                    `json:"error,omitempty"`
}

type harnessEditLatencyPhase struct {
	Name            string  `json:"name"`
	DurationMS      float64 `json:"duration_ms"`
	QueueMS         float64 `json:"queue_ms,omitempty"`
	Cache           string  `json:"cache"`
	Reason          string  `json:"reason"`
	OK              bool    `json:"ok"`
	Actions         int     `json:"actions,omitempty"`
	CacheHits       int     `json:"cache_hits,omitempty"`
	CacheMisses     int     `json:"cache_misses,omitempty"`
	FilesWritten    int     `json:"files_written,omitempty"`
	FilesRemoved    int     `json:"files_removed,omitempty"`
	BytesWritten    int64   `json:"bytes_written,omitempty"`
	ExecutableBytes int64   `json:"executable_bytes,omitempty"`
}

func runHarnessEditLatencyStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "warm implementation edit latency benchmark",
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--benchmark", "edit-latency", "--summary", "--write"},
	}
	step.Summary, step.Error = runHarnessEditLatencyBenchmark(ctx, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), step.Error == ""
	return step
}

func runHarnessEditLatencyBenchmark(parent context.Context, repoRoot string) (summary map[string]any, failure string) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Minute)
	defer cancel()
	summary = map[string]any{
		"benchmark":          "edit-latency",
		"samples_per_lane":   harnessEditLatencySamples,
		"warmups_per_lane":   harnessEditLatencyWarmups,
		"metric":             "source edit completion through complete normal-endpoint response; exact identity is validated post-response without extending the latency interval",
		"quantile_method":    "nearest-rank",
		"comparison":         "interleaved baseline and candidate lanes",
		"cache_state":        "lane-private GOCACHE and Scenery cache, separately warmed; module download and host filesystem caches retained",
		"background_load":    "developer workloads were not stopped",
		"hardware":           map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "logical_cpus": runtime.NumCPU()},
		"acceptance_targets": map[string]any{"p50_ms": 300, "p95_ms": 500},
	}
	root, err := os.MkdirTemp("/tmp", "scn-edit-latency-")
	if err != nil {
		return summary, err.Error()
	}
	defer func() { _ = os.RemoveAll(root) }()
	if output, runErr := harnessEditLatencyCommand(ctx, repoRoot, nil, "uptime"); runErr == nil {
		summary["load_before"] = strings.TrimSpace(string(output))
	}
	if output, runErr := harnessEditLatencyCommand(ctx, repoRoot, nil, "go", "version"); runErr == nil {
		summary["go_version"] = strings.TrimSpace(string(output))
	}
	if runtime.GOOS == "darwin" {
		if output, runErr := harnessEditLatencyCommand(ctx, repoRoot, nil, "sysctl", "-n", "hw.model", "hw.memsize", "hw.ncpu"); runErr == nil {
			summary["hardware"].(map[string]any)["sysctl_model_memory_cpu"] = strings.Fields(string(output))
		}
		if output, runErr := harnessEditLatencyCommand(ctx, repoRoot, nil, "sw_vers", "-productVersion"); runErr == nil {
			summary["os_version"] = strings.TrimSpace(string(output))
		}
	}

	baselineSource := filepath.Join(root, "baseline-source")
	if output, runErr := harnessEditLatencyCommand(ctx, repoRoot, nil, "git", "worktree", "add", "--quiet", "--detach", baselineSource, "HEAD"); runErr != nil {
		return summary, fmt.Sprintf("prepare baseline source: %v: %s", runErr, output)
	}
	defer func() {
		_, _ = harnessEditLatencyCommand(context.Background(), repoRoot, nil, "git", "worktree", "remove", "--force", baselineSource)
	}()
	baselineCommitOutput, err := harnessEditLatencyCommand(ctx, baselineSource, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return summary, err.Error()
	}
	baselineBootstrap := filepath.Join(root, "baseline-scenery")
	baselineBuildEnv := envWithOverrides(envpolicy.Environ(), "GOWORK=off", "GOCACHE="+filepath.Join(root, "baseline-bootstrap-gocache"))
	if output, runErr := harnessEditLatencyCommand(ctx, baselineSource, baselineBuildEnv, "go", "build", "-o", baselineBootstrap, "./cmd/scenery"); runErr != nil {
		return summary, fmt.Sprintf("build baseline Scenery: %v: %s", runErr, output)
	}
	summary["baseline_commit"] = strings.TrimSpace(string(baselineCommitOutput))

	candidate, err := prepareHarnessEditLatencyLane(ctx, root, "candidate", repoRoot, repoRoot, harnessLocalSceneryBinaryPath(repoRoot))
	if err != nil {
		return summary, "prepare candidate lane: " + err.Error()
	}
	defer candidate.close()
	baseline, err := prepareHarnessEditLatencyLane(ctx, root, "baseline", repoRoot, baselineSource, baselineBootstrap)
	if err != nil {
		return summary, "prepare baseline lane: " + err.Error()
	}
	defer baseline.close()
	summary["candidate_framework"] = candidate.framework
	summary["baseline_framework"] = baseline.framework

	for warmup := 1; warmup <= harnessEditLatencyWarmups; warmup++ {
		for _, lane := range []*harnessEditLatencyLane{baseline, candidate} {
			if sample := lane.measure(ctx, -warmup); sample.Error != "" {
				summary["warmup_failure"] = sample
				return summary, sample.Error
			}
		}
	}

	all := map[string][]harnessEditLatencySample{"baseline": {}, "candidate": {}}
	for ordinal := 1; ordinal <= harnessEditLatencySamples; ordinal++ {
		lanes := []*harnessEditLatencyLane{baseline, candidate}
		if ordinal%2 == 0 {
			slices.Reverse(lanes)
		}
		for _, lane := range lanes {
			sample := lane.measure(ctx, ordinal)
			all[lane.label] = append(all[lane.label], sample)
			summary["samples"] = all
			if sample.Error != "" {
				return summary, sample.Error
			}
		}
	}
	summary["baseline"] = harnessEditLatencyStats(all["baseline"])
	summary["candidate"] = harnessEditLatencyStats(all["candidate"])
	candidateStats := summary["candidate"].(map[string]any)
	summary["targets_met"] = candidateStats["p50_ms"].(float64) <= 300 && candidateStats["p95_ms"].(float64) <= 500
	if output, runErr := harnessEditLatencyCommand(ctx, repoRoot, nil, "uptime"); runErr == nil {
		summary["load_after"] = strings.TrimSpace(string(output))
	}
	return summary, ""
}

func prepareHarnessEditLatencyLane(ctx context.Context, root, label, fixtureRoot, sourceRoot, bootstrap string) (*harnessEditLatencyLane, error) {
	laneRoot := filepath.Join(root, label)
	appRoot := filepath.Join(laneRoot, "app")
	for _, name := range []string{".scenery.json", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn"} {
		content, err := os.ReadFile(filepath.Join(fixtureRoot, "testdata/apps/basic", name))
		if err != nil {
			return nil, err
		}
		if name == "go.mod" {
			content = bytes.ReplaceAll(content, []byte("=> ../../.."), []byte("=> "+sourceRoot))
		}
		path := filepath.Join(appRoot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return nil, err
		}
	}
	if err := prepareHarnessHandoffService(appRoot); err != nil {
		return nil, err
	}
	env := envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "SCENERY_DEV_DASHBOARD_ADDR", "SCENERY_DEV_CACHE_DIR", "DATABASE_URL", "GOCACHE", detachedDevChildEnv),
		"SCENERY_AGENT_HOME="+filepath.Join(laneRoot, "agent"),
		"SCENERY_DEV_CACHE_DIR="+filepath.Join(laneRoot, "cache"),
		"GOCACHE="+filepath.Join(laneRoot, "gocache"),
		"SCENERY_DEV_VICTORIA=0", "SCENERY_DEV_VICTORIA_DOWNLOAD=0")
	selection, err := prepareHarnessSelectedFramework(ctx, sourceRoot, laneRoot, appRoot, bootstrap, env)
	if err != nil {
		return nil, err
	}
	output, err := harnessEditLatencyCommand(ctx, appRoot, env, selection.Executable, "up", "--detach", "--wait", "ready", "--app-root", appRoot, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("start lane: %w: %s", err, output)
	}
	var started detachedDevResult
	if err := decodeCLIJSON(output, &started); err != nil {
		return nil, err
	}
	if started.Session.Status != "running" || started.Session.AppPID == "" || started.LogPath == "" {
		return nil, fmt.Errorf("lane did not start a ready application: %s", output)
	}
	paths, err := localagent.PathsForWorktree(filepath.Join(laneRoot, "agent"), appRoot)
	if err != nil {
		return nil, err
	}
	sourcePath := filepath.Join(appRoot, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	lane := &harnessEditLatencyLane{
		label: label, appRoot: appRoot, sessionRoot: paths.AppRoot, home: filepath.Join(laneRoot, "agent"), binary: selection.Executable,
		env: env, started: started, client: localagent.NewClient(paths.Socket),
		framework:  map[string]string{"source_digest": selection.Source.Digest, "executable_digest": selection.ExecutableDigest},
		apiURL:     strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/") + "/echo",
		sourcePath: sourcePath, source: source, seen: map[string]struct{}{},
	}
	if _, _, err := harnessHandoffEcho(ctx, appRoot, lane.apiURL, "echo:handoff", started.Session.AppPID, nil); err != nil {
		lane.close()
		return nil, err
	}
	return lane, nil
}

func (l *harnessEditLatencyLane) close() {
	if l.client != nil {
		l.client.CloseIdleConnections()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = harnessEditLatencyCommand(ctx, l.appRoot, l.env, l.binary, "down", "--app-root", l.appRoot, "-o", "json")
}

func (l *harnessEditLatencyLane) measure(ctx context.Context, ordinal int) harnessEditLatencySample {
	behavior := fmt.Sprintf("%s-%03d:", l.label, ordinal)
	if ordinal < 0 {
		behavior = fmt.Sprintf("warmup-%s-%03d:", l.label, -ordinal)
	}
	result := harnessEditLatencySample{Lane: l.label, Ordinal: ordinal, Behavior: behavior}
	anchor := []byte(`sharedprefix.Value("echo:")`)
	changed := bytes.Replace(l.source, anchor, []byte(`sharedprefix.Value("`+behavior+`")`), 1)
	if bytes.Equal(changed, l.source) {
		result.Error = l.label + " source anchor is missing"
		return result
	}
	logInfo, err := os.Stat(l.started.LogPath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := os.WriteFile(l.sourcePath, changed, 0o600); err != nil {
		result.Error = err.Error()
		return result
	}
	editCompleted := time.Now()
	result.EditCompletedAt = editCompleted.UTC().Format(time.RFC3339Nano)
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		result.Polls++
		observation, observeErr := l.observe(ctx, behavior+"handoff")
		if observeErr != nil {
			result.TransientFailures++
		} else if observation.matched {
			result.ResponseCompletedAt = observation.completed.UTC().Format(time.RFC3339Nano)
			result.EditToResponseMS = float64(observation.completed.Sub(editCompleted).Microseconds()) / 1000
			result.MatchingRequestMS = float64(observation.requestDuration.Microseconds()) / 1000
			result.ProcessID = observation.processID
			result.Identity = observation.identity
			ownershipStarted := time.Now()
			if ownershipErr := l.waitForOwnership(ctx, observation.processID); ownershipErr != nil {
				result.Error = ownershipErr.Error()
				return result
			}
			result.OwnershipObservedMS = float64(time.Since(ownershipStarted).Microseconds()) / 1000
			candidate, verifyErr := build.VerifyCandidate(ctx, l.appRoot, observation.identity.Target, build.RuntimeBundlePath(l.appRoot, observation.identity.Target))
			if verifyErr != nil {
				result.Error = "verify matched candidate: " + verifyErr.Error()
				return result
			}
			if observation.identity.ContractRevision != candidate.ContractRevision ||
				observation.identity.ImplementationRevision != candidate.ImplementationRevision ||
				observation.identity.BuildInputDigest != candidate.BuildInputDigest || observation.identity.Target != candidate.Target {
				result.Error = "matched response identity does not equal the exact current candidate"
				return result
			}
			if _, exists := l.seen[candidate.ImplementationRevision]; exists {
				result.Error = "benchmark reused an implementation revision"
				return result
			}
			l.seen[candidate.ImplementationRevision] = struct{}{}
			result.Identity = candidate
			result.OperationID, result.Phases = harnessEditLatencyPhases(l.started.LogPath, logInfo.Size())
			return result
		}
		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			return result
		case <-deadline.C:
			result.Error = fmt.Sprintf("%s edit %d did not reach the normal endpoint", l.label, ordinal)
			return result
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (l *harnessEditLatencyLane) waitForOwnership(ctx context.Context, processID string) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var last []localagent.Session
	var lastErr error
	for {
		last, lastErr = l.client.List(ctx, l.sessionRoot)
		if lastErr == nil && len(last) == 1 && last[0].Status == "running" && last[0].AppPID == processID {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("matched response is not owned by the current exact session: process=%s sessions=%+v error=%v", processID, last, lastErr)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

type harnessEditLatencyObservation struct {
	matched         bool
	completed       time.Time
	requestDuration time.Duration
	processID       string
	identity        build.CandidateIdentity
}

func (l *harnessEditLatencyLane) observe(ctx context.Context, want string) (harnessEditLatencyObservation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, l.apiURL, strings.NewReader(`{"message":"handoff"}`))
	if err != nil {
		return harnessEditLatencyObservation{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		return harnessEditLatencyObservation{}, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return harnessEditLatencyObservation{}, err
	}
	completed := time.Now()
	var body struct {
		Message string `json:"message"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(data, &body) != nil || body.Message != want {
		return harnessEditLatencyObservation{}, nil
	}
	identity := build.CandidateIdentity{
		ContractRevision:       response.Header.Get("X-Scenery-Contract-Revision"),
		ImplementationRevision: response.Header.Get("X-Scenery-Implementation-Revision"),
		BuildInputDigest:       response.Header.Get("X-Scenery-Build-Input-Digest"),
		Target:                 response.Header.Get("X-Scenery-Go-Target"),
	}
	processID := response.Header.Get("X-Scenery-Process-ID")
	if processID == "" || identity.ContractRevision == "" || identity.ImplementationRevision == "" || identity.BuildInputDigest == "" || identity.Target == "" {
		return harnessEditLatencyObservation{}, fmt.Errorf("matching response omitted exact execution identity")
	}
	return harnessEditLatencyObservation{matched: true, completed: completed, requestDuration: completed.Sub(started), processID: processID, identity: identity}, nil
}

func harnessEditLatencyPhases(logPath string, offset int64) (string, []harnessEditLatencyPhase) {
	events, err := harnessWatchEvents(logPath, offset)
	if err != nil {
		return "", nil
	}
	operationID := ""
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "build.request" && event.Data.OK {
			operationID = event.Data.OperationID
		}
	}
	phases := make([]harnessEditLatencyPhase, 0, 20)
	for _, event := range events {
		if event.Type != "build.step" || (operationID != "" && event.Data.OperationID != operationID) {
			continue
		}
		phases = append(phases, harnessEditLatencyPhase{
			Name: event.Data.Name, DurationMS: event.Data.DurationMS, QueueMS: event.Data.QueueMS,
			Cache: event.Data.Cache, Reason: event.Data.Reason, OK: event.Data.OK,
			Actions: event.Data.Actions, CacheHits: event.Data.CacheHits, CacheMisses: event.Data.CacheMisses,
			FilesWritten: event.Data.FilesWritten, FilesRemoved: event.Data.FilesRemoved, BytesWritten: event.Data.BytesWritten,
			ExecutableBytes: event.Data.ExecutableBytes,
		})
	}
	return operationID, phases
}

func harnessEditLatencyStats(samples []harnessEditLatencySample) map[string]any {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, sample.EditToResponseMS)
	}
	slices.Sort(values)
	nearestRank := func(numerator int) float64 {
		index := (len(values)*numerator + 99) / 100
		if index < 1 {
			index = 1
		}
		return values[index-1]
	}
	return map[string]any{
		"count": len(values), "p50_ms": nearestRank(50), "p95_ms": nearestRank(95),
		"worst_ms": values[len(values)-1], "best_ms": values[0],
	}
}

func harnessEditLatencyCommand(ctx context.Context, cwd string, env []string, program string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = cwd
	if env != nil {
		command.Env = env
	}
	return command.CombinedOutput()
}
