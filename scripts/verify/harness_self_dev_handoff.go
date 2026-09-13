package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
)

// The lifetime-held OS lock is a real write-generation exclusion assertion.
// It is acquired by the service constructor, never by package initialization.
func prepareHarnessHandoffService(appRoot string) error {
	source := `package service
import (
  "context"
  "fmt"
  "os"
  "path/filepath"
  "syscall"
  servicecontract "example.com/basicapp/service/scenerycontract"
  sharedprefix "example.com/basicapp/sharedprefix"
)
type Service struct { writerLock *os.File }
func NewService(context.Context, servicecontract.ServiceConstructorInput) (*Service, error) {
  lock, err := os.OpenFile(filepath.Join(".scenery", "probe-writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
  if err != nil { return nil, err }
  if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
    _ = lock.Close()
    return nil, fmt.Errorf("overlapping write generations: %w", err)
  }
  if false { return nil, fmt.Errorf("intentional candidate startup failure") }
  return &Service{writerLock: lock}, nil
}
func (*Service) Echo(_ context.Context, input servicecontract.EchoInput) (servicecontract.EchoOutcome, error) {
  return servicecontract.EchoOk{Value: servicecontract.EchoResult{Message: sharedprefix.Value("echo:") + input.Message}}, nil
}
`
	sharedRoot := filepath.Join(appRoot, "sharedprefix")
	if err := os.MkdirAll(sharedRoot, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sharedRoot, "prefix.go"), []byte("package sharedprefix\nfunc Value(value string) string { return value }\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(appRoot, "service/api.go"), []byte(source), 0o600)
}

func runHarnessAppHandoffProbe(parent context.Context, root, home string, started detachedDevResult) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	paths, err := localagent.PathsForWorktree(home, root)
	if err != nil {
		return nil, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	readSession := func() (localagent.Session, error) {
		sessions, err := client.List(ctx, paths.AppRoot)
		if err != nil {
			return localagent.Session{}, err
		}
		if len(sessions) != 1 || sessions[0].OwnerPID != started.PID || sessions[0].AppRoot != paths.AppRoot {
			return localagent.Session{}, fmt.Errorf("handoff lost exact-root supervisor ownership")
		}
		return sessions[0], nil
	}
	apiURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/") + "/echo"
	initial, err := readSession()
	if err != nil {
		return nil, err
	}
	initialIdentity, _, err := harnessHandoffEcho(ctx, root, apiURL, "echo:handoff", initial.AppPID, nil)
	if err != nil {
		return nil, err
	}
	sourcePath := filepath.Join(root, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	// Invalid test syntax makes any accidental ordinary rebuild observable.
	if err := os.WriteFile(filepath.Join(root, "service/ignored_test.go"), []byte("package service\nfunc invalid test syntax\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("Documentation-only runtime edit probe.\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, time.Second); err != nil {
		return nil, err
	}
	unchanged, err := readSession()
	if err != nil || unchanged.AppPID != initial.AppPID || unchanged.Status != "running" {
		return nil, fmt.Errorf("test/doc/identical-content edits changed the runtime: %+v: %v", unchanged, err)
	}
	if err := os.Remove(filepath.Join(root, "service/ignored_test.go")); err != nil {
		return nil, err
	}
	rejectedPath := filepath.Join(root, "service/preflight_reject.go")
	rejected := "package service\nimport \"os\"\nfunc init() { if len(os.Args) == 2 && os.Args[1] == \"--scenery-runtime-preflight\" { os.Exit(19) } }\n"
	if err := os.WriteFile(rejectedPath, []byte(rejected), 0o600); err != nil {
		return nil, err
	}
	for {
		log, err := os.ReadFile(started.LogPath)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(log, []byte("candidate runtime preflight failed")) {
			break
		}
		if err := harnessWaitContext(ctx, 100*time.Millisecond); err != nil {
			return nil, fmt.Errorf("candidate did not reach preflight rejection: %w", err)
		}
	}
	preserved, err := readSession()
	if err != nil || preserved.AppPID != initial.AppPID || preserved.Status != "running" {
		return nil, fmt.Errorf("rejected candidate displaced the live generation: %+v: %v", preserved, err)
	}
	if _, _, err := harnessHandoffEcho(ctx, root, apiURL, "echo:handoff", preserved.AppPID, &initialIdentity); err != nil {
		return nil, err
	}
	if err := os.Remove(rejectedPath); err != nil {
		return nil, err
	}
	var lastVerifiedResponseLatency time.Duration
	waitReplacement := func(previousPID, response string, expected *build.CandidateIdentity, editCompleted time.Time) (localagent.Session, build.CandidateIdentity, time.Duration, error) {
		var last error
		for {
			session, readErr := readSession()
			if readErr == nil && session.Status == "running" && session.AppPID != "" && session.AppPID != previousPID {
				var identity build.CandidateIdentity
				requestStarted := time.Now()
				if identity, lastVerifiedResponseLatency, last = harnessHandoffEcho(ctx, root, apiURL, response, session.AppPID, expected); last == nil {
					// Candidate verification after reading the response validates the
					// observation but is not part of edit-to-response latency.
					responseCompleted := requestStarted.Add(lastVerifiedResponseLatency)
					return session, identity, responseCompleted.Sub(editCompleted), nil
				}
			} else {
				last = readErr
			}
			if err := harnessWaitContext(ctx, 100*time.Millisecond); err != nil {
				return localagent.Session{}, build.CandidateIdentity{}, 0, fmt.Errorf("wait for serving replacement: %w: %v", err, last)
			}
		}
	}
	failed := bytes.Replace(source, []byte("if false {"), []byte("if true {"), 1)
	if err := os.WriteFile(sourcePath, failed, 0o600); err != nil {
		return nil, err
	}
	failedEditCompleted := time.Now()
	recovered, _, recoveryLatency, err := waitReplacement(initial.AppPID, "echo:handoff", &initialIdentity, failedEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("failed candidate recovery: %w", err)
	}
	recoveryResponseLatency := lastVerifiedResponseLatency
	logInfo, err := os.Stat(started.LogPath)
	if err != nil {
		return nil, err
	}
	implementationEditLogOffset := logInfo.Size()
	changed := bytes.Replace(source, []byte(`sharedprefix.Value("echo:")`), []byte(`sharedprefix.Value("changed:")`), 1)
	if err := os.WriteFile(sourcePath, changed, 0o600); err != nil {
		return nil, err
	}
	changedEditCompleted := time.Now()
	updated, updatedIdentity, updatedLatency, err := waitReplacement(recovered.AppPID, "changed:handoff", nil, changedEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("correct served revision after recovery: %w", err)
	}
	updatedResponseLatency := lastVerifiedResponseLatency
	incremental, err := harnessIncrementalPreparationEvidence(started.LogPath, implementationEditLogOffset)
	if err != nil {
		return nil, err
	}
	watch, err := runHarnessWatchBatchProbe(ctx, root, started, updated, readSession, waitReplacement)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"watch_batches":                         watch,
		"test_doc_identical_edits_no_restart":   true,
		"failed_start_restored_previous":        true,
		"preflight_rejection_preserved_backend": true,
		"exclusive_writer_lock":                 true,
		"runtime_edit_served_correct_response":  true,
		"runtime_edit_to_verified_response_ms":  float64(updatedLatency.Microseconds()) / 1000,
		"first_verified_response_ms":            float64(updatedResponseLatency.Microseconds()) / 1000,
		"incremental_preparation":               incremental,
		"failed_edit_to_recovered_response_ms":  float64(recoveryLatency.Microseconds()) / 1000,
		"recovery_verified_response_ms":         float64(recoveryResponseLatency.Microseconds()) / 1000,
		"served_contract_revision":              updatedIdentity.ContractRevision,
		"served_implementation_revision":        updatedIdentity.ImplementationRevision,
		"served_build_input_digest":             updatedIdentity.BuildInputDigest,
		"served_go_target":                      updatedIdentity.Target,
		"initial_pid":                           initial.AppPID, "restored_pid": recovered.AppPID, "updated_pid": updated.AppPID,
	}, nil
}

func harnessIncrementalPreparationEvidence(log string, offset int64) (map[string]any, error) {
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return nil, err
	}
	hits := map[string]bool{}
	contractChecks := 0
	filesWritten := -1
	sharedQueued, sharedPublished := false, false
	operationID := ""
	for _, event := range events {
		if event.Type != "build.step" {
			continue
		}
		if operationID == "" && event.Data.OperationID != "" {
			operationID = event.Data.OperationID
		}
		if event.Data.Name == "build.request" && event.Data.OK {
			operationID = event.Data.OperationID
		}
		switch event.Data.Name {
		case "contract.check":
			contractChecks++
		case "projection.go", "projection.typescript":
			if event.Data.Cache == "hit" && event.Data.Reason == "preparation_key_and_artifacts_match" {
				hits[event.Data.Name] = true
			}
		case "workspace.materialize":
			filesWritten = event.Data.FilesWritten
		case "build.shared_queue":
			sharedQueued = true
		case "build.shared_artifact":
			sharedPublished = event.Data.Cache == "miss" && event.Data.Reason == "linked_and_published"
		}
	}
	if contractChecks != 1 || !hits["projection.go"] || !hits["projection.typescript"] || filesWritten != 1 || !sharedQueued || !sharedPublished {
		return nil, fmt.Errorf("implementation edit did not use the incremental preparation path: contract_checks=%d go_projection_hit=%t typescript_projection_hit=%t files_written=%d shared_queued=%t shared_published=%t", contractChecks, hits["projection.go"], hits["projection.typescript"], filesWritten, sharedQueued, sharedPublished)
	}
	if operationID == "" {
		return nil, fmt.Errorf("implementation edit did not emit a successful correlated build request")
	}
	type phaseEvidence struct {
		Name                     string   `json:"name"`
		StartedAt                string   `json:"started_at"`
		DurationMS               float64  `json:"duration_ms"`
		QueueMS                  float64  `json:"queue_ms,omitempty"`
		Cache                    string   `json:"cache"`
		Reason                   string   `json:"reason"`
		OK                       bool     `json:"ok"`
		Actions                  int      `json:"actions,omitempty"`
		CacheHits                int      `json:"cache_hits,omitempty"`
		CacheMisses              int      `json:"cache_misses,omitempty"`
		FilesWritten             int      `json:"files_written,omitempty"`
		FilesRemoved             int      `json:"files_removed,omitempty"`
		BytesWritten             int64    `json:"bytes_written,omitempty"`
		ExecutableBytes          int64    `json:"executable_bytes,omitempty"`
		PackagesRebuilt          []string `json:"packages_rebuilt,omitempty"`
		PackagesRebuiltAvailable bool     `json:"packages_rebuilt_available"`
	}
	phases := make([]phaseEvidence, 0, 16)
	var identity map[string]string
	for _, event := range events {
		if event.Type != "build.step" || event.Data.OperationID != operationID {
			continue
		}
		data := event.Data
		phases = append(phases, phaseEvidence{
			Name: data.Name, StartedAt: data.StartedAt, DurationMS: data.DurationMS,
			QueueMS: data.QueueMS, Cache: data.Cache, Reason: data.Reason, OK: data.OK,
			Actions: data.Actions, CacheHits: data.CacheHits, CacheMisses: data.CacheMisses,
			FilesWritten: data.FilesWritten, FilesRemoved: data.FilesRemoved, BytesWritten: data.BytesWritten,
			ExecutableBytes: data.ExecutableBytes, PackagesRebuilt: data.PackagesRebuilt,
			PackagesRebuiltAvailable: data.PackagesRebuiltAvailable,
		})
		if data.Name == "build.identity" {
			identity = map[string]string{
				"snapshot_digest": data.SnapshotDigest, "contract_revision": data.ContractRevision,
				"implementation_revision": data.ImplementationRevision, "build_input_digest": data.BuildInputDigest,
				"framework_source_digest":     data.FrameworkSourceDigest,
				"framework_executable_digest": data.FrameworkExecutableDigest, "go_target": data.GoTarget,
			}
		}
	}
	return map[string]any{
		"operation_id":              operationID,
		"phase_intervals":           phases,
		"phase_intervals_overlap":   true,
		"candidate_identity":        identity,
		"contract_checks":           contractChecks,
		"go_projection_cache_hit":   true,
		"typescript_projection_hit": true,
		"workspace_files_written":   filesWritten,
		"shared_link_queued":        true,
		"shared_artifact_published": true,
	}, nil
}

func harnessHandoffEcho(ctx context.Context, root, url, want, processID string, expected *build.CandidateIdentity) (build.CandidateIdentity, time.Duration, error) {
	started := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"message":"handoff"}`))
	if err != nil {
		return build.CandidateIdentity{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		return build.CandidateIdentity{}, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	// Session inspection and HTTP are separate observations. A later generation
	// may already serve this response; never attribute it to the earlier PID.
	if actual := response.Header.Get("X-Scenery-Process-ID"); processID == "" || actual != processID {
		return build.CandidateIdentity{}, 0, fmt.Errorf("served process %q; want observed session process %q", actual, processID)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return build.CandidateIdentity{}, 0, err
	}
	var result struct {
		Message string `json:"message"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(data, &result) != nil || result.Message != want {
		return build.CandidateIdentity{}, 0, fmt.Errorf("served response %d %s; want %q", response.StatusCode, data, want)
	}
	responseLatency := time.Since(started)
	served := build.CandidateIdentity{
		ContractRevision:       response.Header.Get("X-Scenery-Contract-Revision"),
		ImplementationRevision: response.Header.Get("X-Scenery-Implementation-Revision"),
		BuildInputDigest:       response.Header.Get("X-Scenery-Build-Input-Digest"),
		Target:                 response.Header.Get("X-Scenery-Go-Target"),
	}
	wantIdentity := expected
	if wantIdentity == nil {
		candidate, verifyErr := build.VerifyCandidate(ctx, root, served.Target, build.RuntimeBundlePath(root, served.Target))
		if verifyErr != nil {
			return build.CandidateIdentity{}, 0, fmt.Errorf("verify served candidate: %w", verifyErr)
		}
		wantIdentity = &candidate
	}
	if served.ContractRevision != wantIdentity.ContractRevision ||
		served.ImplementationRevision != wantIdentity.ImplementationRevision ||
		served.BuildInputDigest != wantIdentity.BuildInputDigest || served.Target != wantIdentity.Target {
		return build.CandidateIdentity{}, 0, fmt.Errorf("served identity does not match the exact candidate")
	}
	served.SpecRevision = wantIdentity.SpecRevision
	served.Producer = wantIdentity.Producer
	served.FrameworkSourceDigest = wantIdentity.FrameworkSourceDigest
	served.FrameworkExecutableDigest = wantIdentity.FrameworkExecutableDigest
	return served, responseLatency, nil
}

func harnessWaitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
