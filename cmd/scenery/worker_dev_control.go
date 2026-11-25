package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devreport"
)

type workerDevEndpoint struct {
	Root      string `json:"root"`
	Socket    string `json:"socket"`
	Control   string `json:"control"`
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
}

func workerDevEndpointPath(root string) string {
	return filepath.Join(root, ".scenery", "worker-dev.json")
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

func readWorkerSourceStatus(ctx context.Context, endpoint workerDevEndpoint, timeout time.Duration, result *runtimeSourceWait) error {
	client := workerControlClient(endpoint.Control)
	defer client.CloseIdleConnections()
	path := fmt.Sprintf("/__scenery/process/v1/source?timeout_ms=%d", timeout.Milliseconds())
	return workerReadJSON(ctx, client, endpoint.Token, path, result)
}

func workerReadJSON(ctx context.Context, client *http.Client, token, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://host"+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

type workerLogEntry struct {
	devreport.LogEvent
	EventID int64 `json:"event_id"`
}

type workerTelemetrySnapshot struct {
	Logs  []workerLogEntry          `json:"logs"`
	Spans []devdash.TraceSpanDetail `json:"spans"`
}

func readWorkerTelemetry(ctx context.Context, client *http.Client, endpoint workerDevEndpoint, traceID string) (workerTelemetrySnapshot, error) {
	var snapshot workerTelemetrySnapshot
	path := "/__scenery/process/v1/telemetry"
	if traceID != "" {
		path += "?id=" + url.QueryEscape(traceID)
	}
	err := workerReadJSON(ctx, client, endpoint.Token, path, &snapshot)
	return snapshot, err
}
