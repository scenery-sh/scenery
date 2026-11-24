package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devreport"
	"scenery.sh/internal/envpolicy"
)

type workerDevEndpoint struct {
	Root      string `json:"root"`
	Socket    string `json:"socket"`
	Control   string `json:"control"`
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
}

type workerDevControl struct {
	mu        sync.RWMutex
	watch     workerInputWatch
	serving   *runtimeServingIdentity
	attempt   *devBuildAttempt
	timing    *workerRebuildTiming
	reports   []devreport.ReportEnvelope
	next      int
	sessionID string
	appID     string
}

func workerDevEndpointPath(root string) string {
	return filepath.Join(root, ".scenery", "worker-dev.json")
}

func startWorkerDevControl(dev *workerDevelopment, directory, hostControl, token string) (*workerDevControl, func(), error) {
	socket := filepath.Join(directory, "dev.sock")
	local, err := net.Listen("unix", socket)
	if err != nil {
		return nil, nil, err
	}
	reports, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = local.Close()
		return nil, nil, err
	}
	control := &workerDevControl{
		watch:     workerInputWatch{root: dev.watch.root, generated: slices.Clone(dev.watch.generated)},
		sessionID: workerRandomToken(), appID: dev.compiled.Manifest.Application.Name,
	}
	privateMux := http.NewServeMux()
	privateMux.HandleFunc("GET /status", control.status)
	privateMux.HandleFunc("GET /trace", control.trace)
	rpc := &http.Server{Handler: privateMux, ReadHeaderTimeout: 2 * time.Second}
	receiver := &http.Server{ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/report" ||
			subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		control.report(w, req)
	})}
	closeServers := func() {
		_ = rpc.Close()
		_ = receiver.Close()
		_ = local.Close()
		_ = reports.Close()
	}
	for key, value := range map[string]string{
		"SCENERY_DEV_REPORT_URL":   "http://" + reports.Addr().String() + "/report",
		"SCENERY_DEV_REPORT_TOKEN": token, "SCENERY_APP_ID": control.appID, "SCENERY_SESSION_ID": control.sessionID,
	} {
		if err := envpolicy.Set(key, value); err != nil {
			closeServers()
			return nil, nil, err
		}
	}
	path := workerDevEndpointPath(dev.watch.root)
	parent := filepath.Dir(path)
	if canonical, err := filepath.EvalSymlinks(parent); err != nil || canonical != parent {
		closeServers()
		return nil, nil, fmt.Errorf("worker development state directory must remain in the app root")
	}
	file, err := os.CreateTemp(parent, ".worker-dev-*")
	if err != nil {
		closeServers()
		return nil, nil, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	err = json.NewEncoder(file).Encode(workerDevEndpoint{dev.watch.root, socket, hostControl, token, control.sessionID})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		closeServers()
		return nil, nil, err
	}
	go func() { _ = rpc.Serve(local) }()
	go func() { _ = receiver.Serve(reports) }()
	return control, func() {
		_ = os.Remove(path)
		closeServers()
	}, nil
}

func readWorkerDevEndpoint(root string) (workerDevEndpoint, bool, error) {
	var endpoint workerDevEndpoint
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return endpoint, false, err
	}
	path := workerDevEndpointPath(root)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return endpoint, false, nil
	}
	if err != nil {
		return endpoint, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return endpoint, false, fmt.Errorf("failed_precondition: unsafe worker development descriptor for %s", root)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return endpoint, false, err
	}
	held, err := paths.ProbeLiveLock()
	if err != nil || !held {
		return endpoint, false, err
	}
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return endpoint, false, err
	}
	if endpoint.Root != root || endpoint.Socket == "" || endpoint.Control == "" || endpoint.Token == "" || endpoint.SessionID == "" {
		return endpoint, false, fmt.Errorf("failed_precondition: invalid worker development owner for %s", root)
	}
	// A supervisor killed without cleanup can leave a descriptor. A stale
	// socket must not shadow another owner of this worktree.
	conn, err := net.DialTimeout("unix", endpoint.Socket, 200*time.Millisecond)
	if err != nil {
		return endpoint, false, nil
	}
	_ = conn.Close()
	return endpoint, true, nil
}

func workerSourceDigest(inputs workerInputs) string {
	hash := sha256.New()
	for _, path := range slices.Sorted(maps.Keys(inputs)) {
		input := inputs[path]
		_, _ = fmt.Fprintf(hash, "%s\x00%x:%o\n", path, input.Digest, input.Mode)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (control *workerDevControl) publish(inputs workerInputs, bundle workerBundle, generation uint64, timing *workerRebuildTiming) {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.serving = &runtimeServingIdentity{
		Generation: generation, PID: strconv.Itoa(os.Getpid()), ContractRevision: bundle.ContractRevision,
		ImplementationRevision: workerBundleDigest(bundle), BuildInputDigest: workerBundleDigest(bundle),
		SourceSnapshotDigest: workerSourceDigest(inputs), PublishedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if timing != nil {
		control.attempt = nil
		control.timing = timing
	}
}

func (control *workerDevControl) failure(inputs workerInputs, failure *devBuildFailure, timing *workerRebuildTiming) {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.attempt = &devBuildAttempt{SourceSnapshotDigest: workerSourceDigest(inputs), Error: failure.Stderr, Failure: failure}
	control.timing = timing
}

func (control *workerDevControl) status(w http.ResponseWriter, req *http.Request) {
	timeout, _ := strconv.ParseInt(req.URL.Query().Get("timeout_ms"), 10, 64)
	ctx, cancel := context.WithTimeout(req.Context(), time.Duration(min(max(0, timeout), maxSourceWaitTimeout.Milliseconds()))*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := runtimeSourceWait{cliPayloadIdentity: newCLIPayloadIdentity(devRuntimeWaitKind), SourceFreshness: "unknown"}
	for {
		_, source := worktreeSourceStatus(control.watch.root)
		if source == "missing" {
			result.Outcome, result.SourceFreshness = "source_missing", "source_missing"
			break
		}
		current, err := control.watch.capture(nil, nil, true, false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		digest := workerSourceDigest(current)
		control.mu.RLock()
		result.Serving, result.RebuildTiming = control.serving, control.timing
		attempt := control.attempt
		control.mu.RUnlock()
		if result.TargetSourceSnapshotDigest == "" {
			result.TargetSourceSnapshotDigest = digest
		}
		result.SourceFreshness = "stale"
		switch {
		case digest != result.TargetSourceSnapshotDigest:
			result.Outcome, result.CurrentSourceSnapshotDigest = "superseded", digest
		case result.Serving != nil && result.Serving.SourceSnapshotDigest == digest:
			result.Outcome, result.SourceFreshness, result.OK = "served", "current", true
		case attempt != nil && attempt.SourceSnapshotDigest == digest:
			result.Outcome, result.CompileError, result.BuildFailure = "failed", attempt.Error, attempt.Failure
		case timeout <= 0:
			result.Outcome = "building"
		}
		if result.Outcome != "" {
			break
		}
		select {
		case <-ctx.Done():
			result.Outcome = "timeout"
		case <-time.After(sourceWaitPollInterval):
			continue
		}
		break
	}
	result.WaitedMS = time.Since(started).Milliseconds()
	writeWorkerDevJSON(w, result)
}

func writeWorkerDevJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func workerDevGet(ctx context.Context, socket, path string, result any) error {
	client := localCallClient("unix", socket)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://host"+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("worker development %s: %s", response.Status, message)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(result)
}

type callTelemetry struct {
	Logs  []devreport.LogEvent      `json:"logs"`
	Spans []devdash.TraceSpanDetail `json:"spans"`
}

func emptyCallTelemetry() callTelemetry {
	return callTelemetry{Logs: []devreport.LogEvent{}, Spans: []devdash.TraceSpanDetail{}}
}

// Keep recent trace-scoped development records even when the optional
// Victoria substrate has not been started for this foreground runtime.
func (control *workerDevControl) report(w http.ResponseWriter, req *http.Request) {
	var batch devreport.ReportBatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, devreport.MaxBatchBytes))
	if err := decoder.Decode(&batch); err != nil || len(batch.Reports) > devreport.MaxBatchReports {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	for _, report := range batch.Reports {
		if report.AppID != control.appID || report.SessionID != control.sessionID {
			continue
		}
		if report.InternalFailure != nil {
			recordRuntimeFailureReport(report)
			continue
		}
		if len(control.reports) < 512 {
			control.reports = append(control.reports, report)
		} else {
			control.reports[control.next] = report
			control.next = (control.next + 1) % len(control.reports)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (control *workerDevControl) trace(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("id")
	result := emptyCallTelemetry()
	events := map[string][]devdash.TraceDetailEvent{}
	control.mu.RLock()
	for index := range control.reports {
		report := control.reports[(control.next+index)%len(control.reports)]
		switch {
		case report.TraceSummary != nil && report.TraceSummary.TraceID == id:
			result.Spans = append(result.Spans, devdash.TraceSpanDetail{TraceSummary: *report.TraceSummary})
		case report.TraceEvent != nil && report.TraceEvent.TraceID == id:
			event := report.TraceEvent
			events[event.SpanID] = append(events[event.SpanID], devdash.TraceDetailEvent{Time: event.EventTime, Data: event.Event})
		case report.LogEvent != nil && report.LogEvent.TraceID == id:
			result.Logs = append(result.Logs, *report.LogEvent)
		}
	}
	control.mu.RUnlock()
	for i := range result.Spans {
		span := &result.Spans[i]
		span.Events = append([]devdash.TraceDetailEvent{}, events[span.SpanID]...)
	}
	writeWorkerDevJSON(w, result)
}
