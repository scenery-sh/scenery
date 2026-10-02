package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/envpolicy"
)

const (
	envDurableEndpoint = "SCENERY_DURABLE_ENDPOINT"
	envDurableToken    = "SCENERY_DURABLE_TOKEN"
	envDurableServices = "SCENERY_DURABLE_SERVICES"
	envDurableWorkerID = "SCENERY_DURABLE_WORKER_ID"
)

type durableRemoteWorkerConfig struct {
	Endpoint string
	Token    string
	Services []string
	WorkerID string
}

func durableRemoteWorkerConfigFromEnv() durableRemoteWorkerConfig {
	cfg := durableRemoteWorkerConfig{
		Endpoint: strings.TrimRight(strings.TrimSpace(envpolicy.Get(envDurableEndpoint)), "/"),
		Token:    strings.TrimSpace(envpolicy.Get(envDurableToken)),
		WorkerID: strings.TrimSpace(envpolicy.Get(envDurableWorkerID)),
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = fmt.Sprintf("remote-%d", os.Getpid())
	}
	for item := range strings.SplitSeq(envpolicy.Get(envDurableServices), ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			cfg.Services = append(cfg.Services, item)
		}
	}
	return cfg
}

// startDurableRemoteWorkers runs one acquisition loop per durable task of every
// selected service against the endpoint's lease protocol.
func startDurableRemoteWorkers(parent context.Context, handlers map[string]map[string]durableRegisteredHandler, cfg durableRemoteWorkerConfig) func(context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	client := &http.Client{Timeout: 65 * time.Second}
	services := cfg.Services
	if len(services) == 0 {
		for service := range handlers {
			services = append(services, service)
		}
	}
	var wg sync.WaitGroup
	for _, service := range services {
		service = strings.TrimSpace(service)
		if service == "" {
			continue
		}
		for taskName, handler := range handlers[service] {
			if handler.handler == nil {
				continue
			}
			wg.Add(1)
			go func(service, taskName string, handler durableRegisteredHandler) {
				defer wg.Done()
				runDurableRemoteWorker(ctx, client, cfg, service, taskName, handler)
			}(service, taskName, handler)
		}
	}
	return func(stopCtx context.Context) error {
		cancel()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
	}
}

// runDurableRemoteWorker leases and runs one task's jobs over HTTP, up to
// handler.slots attempts at once.
func runDurableRemoteWorker(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, service, taskName string, handler durableRegisteredHandler) {
	slots := make(chan struct{}, durableTaskSlots(handler.slots))
	var attempts sync.WaitGroup
	defer attempts.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case slots <- struct{}{}:
		}
		lease, err := durableRemoteLease(ctx, client, cfg, service, taskName)
		if err != nil || !lease.Leased || lease.Job == nil {
			<-slots
			sleepDurableWorker(ctx)
			continue
		}
		attempts.Add(1)
		go func() {
			defer attempts.Done()
			defer func() { <-slots }()
			runDurableRemoteAttempt(ctx, client, cfg, service, lease, handler)
		}()
	}
}

// runDurableRemoteAttempt runs one leased attempt and reports its outcome.
func runDurableRemoteAttempt(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, service string, lease durableLeaseResponse, handler durableRegisteredHandler) {
	stopHeartbeat := startDurableHeartbeat(ctx, time.Duration(lease.Job.LeaseMS)*time.Millisecond, func(heartbeatCtx context.Context) error {
		return durableRemoteHeartbeat(heartbeatCtx, client, cfg, service, lease.Job.ID, lease.LeaseID)
	})
	timeout := handler.timeout
	if lease.Job.TimeoutMS > 0 {
		timeout = time.Duration(lease.Job.TimeoutMS) * time.Millisecond
	}
	// A remote worker is never process-linked, so its attempts run unpinned.
	jobCtx, restore := enterDurableInvocation(ctx, service, lease.Job.TaskName, lease.Job.ID, timeout, lease.Job.Invocation, 0)
	result, err := runDurableTaskHandler(jobCtx, timeout, handler.handler, []byte(lease.Job.Input))
	stopHeartbeat()
	restore(err)
	if err != nil {
		_ = durableRemoteFail(ctx, client, cfg, service, lease.Job.ID, lease.LeaseID, string(durableFailureMessage(err)))
		return
	}
	_ = durableRemoteComplete(ctx, client, cfg, service, lease.Job.ID, lease.LeaseID, result)
}

func durableRemoteLease(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, service, taskName string) (durableLeaseResponse, error) {
	var resp durableLeaseResponse
	err := durableRemotePost(ctx, client, cfg, durableRemotePath(service, "lease"), durableLeaseRequest{WorkerID: cfg.WorkerID, TaskName: taskName}, &resp)
	return resp, err
}

func durableRemoteHeartbeat(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, service, jobID, leaseID string) error {
	return durableRemotePost(ctx, client, cfg, durableRemotePath(service, "jobs", jobID, "heartbeat"), durableLeaseActionRequest{WorkerID: cfg.WorkerID, LeaseID: leaseID}, nil)
}

func durableRemoteComplete(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, service, jobID, leaseID string, result []byte) error {
	return durableRemotePost(ctx, client, cfg, durableRemotePath(service, "jobs", jobID, "complete"), durableLeaseActionRequest{WorkerID: cfg.WorkerID, LeaseID: leaseID, Result: json.RawMessage(result)}, nil)
}

func durableRemoteFail(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, service, jobID, leaseID, message string) error {
	return durableRemotePost(ctx, client, cfg, durableRemotePath(service, "jobs", jobID, "fail"), durableLeaseActionRequest{WorkerID: cfg.WorkerID, LeaseID: leaseID, Error: message}, nil)
}

func durableRemotePost(ctx context.Context, client *http.Client, cfg durableRemoteWorkerConfig, path string, body any, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("durable remote worker: %s returned %s", path, resp.Status)
	}
	if dst == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func durableRemotePath(parts ...string) string {
	escaped := make([]string, 0, len(parts)+3)
	escaped = append(escaped, "__scenery", "durable", "v1")
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return "/" + strings.Join(escaped, "/")
}
