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
	"slices"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
)

// The handoff service fails its constructor on demand, so a build whose
// replacement process cannot start is observable. Serving instances of the
// process model overlap while pinned requests finish; the exclusivity of
// background work is proven by the process-model probe.
func prepareHarnessHandoffService(appRoot string) error {
	source := `package service
import (
  "context"
  "fmt"
  servicecontract "example.com/basicapp/service/scenerycontract"
  sharedprefix "example.com/basicapp/sharedprefix"
)
type Service struct{}
func NewService(context.Context, servicecontract.ServiceConstructorInput) (*Service, error) {
  if false { return nil, fmt.Errorf("intentional candidate startup failure") }
  return &Service{}, nil
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

// prepareHarnessNativeHandoffService adds one real cgo dependency without
// changing the public handler shape. The probe later changes only the C
// expression while preserving its returned value.
func prepareHarnessNativeHandoffService(appRoot string) error {
	appPath := filepath.Join(appRoot, "app.scn")
	declaration, err := os.ReadFile(appPath)
	if err != nil {
		return err
	}
	declaration = bytes.Replace(declaration,
		[]byte(`revision_include = ["**/*.go", "go.mod"]`),
		[]byte(`revision_include = ["**/*.go", "**/*.c", "**/*.h", "go.mod"]`), 1)
	declaration = bytes.Replace(declaration, []byte(`cgo       = "disabled"`), []byte(`cgo       = "host"`), 1)
	if !bytes.Contains(declaration, []byte(`cgo       = "host"`)) || !bytes.Contains(declaration, []byte(`"**/*.c"`)) {
		return fmt.Errorf("native handoff declaration anchors are missing")
	}
	if err := os.WriteFile(appPath, declaration, 0o600); err != nil {
		return err
	}
	servicePath := filepath.Join(appRoot, "service/api.go")
	service, err := os.ReadFile(servicePath)
	if err != nil {
		return err
	}
	service = bytes.Replace(service,
		[]byte(`  sharedprefix "example.com/basicapp/sharedprefix"`),
		[]byte("  nativevalue \"example.com/basicapp/nativevalue\"\n  sharedprefix \"example.com/basicapp/sharedprefix\""), 1)
	service = bytes.Replace(service,
		[]byte("func (*Service) Echo(_ context.Context, input servicecontract.EchoInput) (servicecontract.EchoOutcome, error) {\n"),
		[]byte("func (*Service) Echo(_ context.Context, input servicecontract.EchoInput) (servicecontract.EchoOutcome, error) {\n  _ = nativevalue.Value()\n"), 1)
	if !bytes.Contains(service, []byte(`nativevalue.Value()`)) {
		return fmt.Errorf("native handoff service anchor is missing")
	}
	if err := os.WriteFile(servicePath, service, 0o600); err != nil {
		return err
	}
	nativeRoot := filepath.Join(appRoot, "nativevalue")
	if err := os.MkdirAll(nativeRoot, 0o755); err != nil {
		return err
	}
	goSource := "package nativevalue\n\n/*\nint scenery_probe_value(void);\n*/\nimport \"C\"\n\nfunc Value() int { return int(C.scenery_probe_value()) }\n"
	if err := os.WriteFile(filepath.Join(nativeRoot, "native.go"), []byte(goSource), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(nativeRoot, "native.c"), []byte("int scenery_probe_value(void) { return 7; }\n"), 0o600)
}

func runHarnessAppHandoffProbe(parent context.Context, root, home, sceneryCache, goCache, binary string, env []string, started detachedDevResult) (map[string]any, error) {
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
	logOffset := func() (int64, error) {
		info, err := os.Stat(started.LogPath)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	apiURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/") + "/echo"
	initial, err := readSession()
	if err != nil {
		return nil, err
	}
	// Every answer is attested by the session's current host process with the
	// build of the generation that served it; the answering service process is
	// named beside it and changes only when a build replaces that service. A
	// contract change replaces the host as well.
	served := func(response string, expected *build.CandidateIdentity) (build.CandidateIdentity, string, time.Duration, error) {
		session, err := readSession()
		if err != nil {
			return build.CandidateIdentity{}, "", 0, err
		}
		if session.Status != "running" {
			return build.CandidateIdentity{}, "", 0, fmt.Errorf("session is %s", session.Status)
		}
		return harnessHandoffEcho(ctx, root, apiURL, response, session.AppPID, expected)
	}
	// sameHost requires that no generation replaced the host.
	sameHost := func() error {
		session, err := readSession()
		if err != nil {
			return err
		}
		if session.AppPID != initial.AppPID {
			return fmt.Errorf("host %s replaced by %s", initial.AppPID, session.AppPID)
		}
		return nil
	}
	initialIdentity, initialService, _, err := served("echo:handoff", nil)
	if err != nil {
		return nil, err
	}
	resourcesBefore, err := captureHarnessDevResources(ctx, started.PID, sceneryCache, goCache)
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
	if _, service, _, err := served("echo:handoff", &initialIdentity); err != nil || service != initialService || sameHost() != nil {
		return nil, fmt.Errorf("test/doc/identical-content edits changed the runtime: service %s, want %s: %v, %v", service, initialService, err, sameHost())
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
	if _, service, _, err := served("echo:handoff", &initialIdentity); err != nil || service != initialService || sameHost() != nil {
		return nil, fmt.Errorf("rejected candidate displaced the live generation: service %s, want %s: %v, %v", service, initialService, err, sameHost())
	}
	removedOffset, err := logOffset()
	if err != nil {
		return nil, err
	}
	if err := os.Remove(rejectedPath); err != nil {
		return nil, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, removedOffset, true); err != nil {
		return nil, fmt.Errorf("removing the rejected preflight did not build again: %w", err)
	}
	var lastVerifiedResponseLatency time.Duration
	// waitReplacement waits until the session serves response from a build
	// other than previous, verified against the current runtime bundle unless
	// expected names it.
	waitReplacement := func(previous build.CandidateIdentity, response string, expected *build.CandidateIdentity, editCompleted time.Time) (build.CandidateIdentity, string, time.Duration, error) {
		var last error
		for {
			requestStarted := time.Now()
			identity, service, latency, err := served(response, expected)
			if err == nil && !harnessSameBuild(identity, previous) {
				lastVerifiedResponseLatency = latency
				// Candidate verification after reading the response validates the
				// observation but is not part of edit-to-response latency.
				return identity, service, requestStarted.Add(latency).Sub(editCompleted), nil
			}
			if err == nil {
				err = fmt.Errorf("the session still serves the previous build")
			}
			last = err
			if err := harnessWaitContext(ctx, 100*time.Millisecond); err != nil {
				return build.CandidateIdentity{}, "", 0, fmt.Errorf("wait for serving replacement: %w: %v", err, last)
			}
		}
	}
	failedOffset, err := logOffset()
	if err != nil {
		return nil, err
	}
	failed := bytes.Replace(source, []byte("if false {"), []byte("if true {"), 1)
	if err := os.WriteFile(sourcePath, failed, 0o600); err != nil {
		return nil, err
	}
	failedEditCompleted := time.Now()
	if err := harnessWaitBuildRequest(ctx, started.LogPath, failedOffset, false); err != nil {
		return nil, fmt.Errorf("a replacement service whose constructor fails did not fail its activation: %w", err)
	}
	recoveredIdentity, recoveredService, recoveryResponseLatency, err := served("echo:handoff", &initialIdentity)
	if err != nil || recoveredService != initialService || sameHost() != nil {
		return nil, fmt.Errorf("failed candidate did not keep the previous generation serving: service %s, want %s: %v, %v", recoveredService, initialService, err, sameHost())
	}
	recoveryLatency := time.Since(failedEditCompleted)
	implementationEditLogOffset, err := logOffset()
	if err != nil {
		return nil, err
	}
	changed := bytes.Replace(source, []byte(`sharedprefix.Value("echo:")`), []byte(`sharedprefix.Value("changed:")`), 1)
	if err := os.WriteFile(sourcePath, changed, 0o600); err != nil {
		return nil, err
	}
	changedEditCompleted := time.Now()
	updatedIdentity, updatedService, updatedLatency, err := waitReplacement(recoveredIdentity, "changed:handoff", nil, changedEditCompleted)
	if err != nil {
		return nil, fmt.Errorf("correct served revision after recovery: %w", err)
	}
	if updatedService == initialService {
		return nil, fmt.Errorf("an implementation edit kept service process %s", initialService)
	}
	updatedResponseLatency := lastVerifiedResponseLatency
	incremental, err := harnessIncrementalPreparationEvidence(started.LogPath, implementationEditLogOffset)
	if err != nil {
		return nil, err
	}
	watch, finalIdentity, err := runHarnessWatchBatchProbe(ctx, root, started, updatedIdentity, served, waitReplacement)
	if err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, 500*time.Millisecond); err != nil {
		return nil, err
	}
	resourcesAfter, err := captureHarnessDevResources(ctx, started.PID, sceneryCache, goCache)
	if err != nil {
		return nil, err
	}
	resourceSettling, err := harnessDevResourceSettling(resourcesBefore, resourcesAfter)
	if err != nil {
		return nil, err
	}
	servedBundle, err := build.ReadRuntimeBundle(root, "development")
	if err != nil {
		return nil, err
	}
	candidate, err := harnessDevelopmentCandidate(ctx, root, binary, env)
	if err != nil {
		return nil, err
	}
	if !harnessSameBuild(candidate, finalIdentity) {
		candidateBundle, readErr := build.ReadRuntimeBundleFile(filepath.Join(root, ".scenery", "probe-candidate.scenery.runtime-bundle.json"), "development")
		return nil, fmt.Errorf("the served build %s/%s differs from the verified development candidate %s/%s in build inputs %v (%v)", finalIdentity.ImplementationRevision, finalIdentity.BuildInputDigest,
			candidate.ImplementationRevision, candidate.BuildInputDigest, harnessBuildInputDifferences(servedBundle, candidateBundle), readErr)
	}
	return map[string]any{
		"watch_batches":                              watch,
		"test_doc_identical_edits_no_restart":        true,
		"failed_start_kept_previous_generation":      true,
		"preflight_rejection_preserved_backend":      true,
		"served_build_equals_verified_candidate":     true,
		"runtime_edit_served_correct_response":       true,
		"runtime_edit_to_verified_response_ms":       float64(updatedLatency.Microseconds()) / 1000,
		"first_verified_response_ms":                 float64(updatedResponseLatency.Microseconds()) / 1000,
		"incremental_preparation":                    incremental,
		"failed_edit_to_kept_generation_response_ms": float64(recoveryLatency.Microseconds()) / 1000,
		"kept_generation_verified_response_ms":       float64(recoveryResponseLatency.Microseconds()) / 1000,
		"served_contract_revision":                   updatedIdentity.ContractRevision,
		"served_implementation_revision":             updatedIdentity.ImplementationRevision,
		"served_build_input_digest":                  updatedIdentity.BuildInputDigest,
		"served_go_target":                           updatedIdentity.Target,
		"resource_settling":                          resourceSettling,
		"host_pid":                                   initial.AppPID, "initial_service_pid": initialService, "updated_service_pid": updatedService,
	}, nil
}

// harnessBuildInputDifferences names the build inputs two runtime bundles
// record differently.
func harnessBuildInputDifferences(left, right build.RuntimeBundleDescriptor) []string {
	digests := func(bundle build.RuntimeBundleDescriptor) map[string]string {
		values := map[string]string{}
		if bundle.BuildInput != nil {
			for _, entry := range bundle.BuildInput.Entries {
				values[entry.Identity] = entry.Digest
			}
		}
		return values
	}
	a, b := digests(left), digests(right)
	var differences []string
	for identity, digest := range a {
		if b[identity] != digest {
			differences = append(differences, identity)
		}
	}
	for identity := range b {
		if _, ok := a[identity]; !ok {
			differences = append(differences, identity)
		}
	}
	slices.Sort(differences)
	return differences
}

// harnessSameBuild reports whether two identities name the same build.
func harnessSameBuild(left, right build.CandidateIdentity) bool {
	return left.ContractRevision == right.ContractRevision && left.ImplementationRevision == right.ImplementationRevision &&
		left.BuildInputDigest == right.BuildInputDigest && left.Target == right.Target
}

// harnessDevelopmentCandidate builds the verified development candidate of the
// current source, as an application that binds its checks to served builds
// does, without starting or replacing the runtime. The probe root lies under a
// symbolic link; the build names it as given and must still reach the served
// build identity.
func harnessDevelopmentCandidate(ctx context.Context, root, binary string, env []string) (build.CandidateIdentity, error) {
	command := commandTreeContext(ctx, binary, "build", "--development", "--verify-generation", "--target", "development", "--output", filepath.Join(root, ".scenery", "probe-candidate"), "--app-root", root, "-o", "json")
	command.Dir, command.Env = root, env
	output, err := command.Output()
	if err != nil {
		return build.CandidateIdentity{}, fmt.Errorf("build the verified development candidate: %w: %s", err, output)
	}
	var result struct {
		CandidateIdentity *build.CandidateIdentity `json:"candidate_identity"`
	}
	if err := decodeCLIJSON(output, &result); err != nil {
		return build.CandidateIdentity{}, err
	}
	if result.CandidateIdentity == nil {
		return build.CandidateIdentity{}, fmt.Errorf("the development build returned no verified candidate: %s", output)
	}
	return *result.CandidateIdentity, nil
}

func harnessIncrementalPreparationEvidence(log string, offset int64) (map[string]any, error) {
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return nil, err
	}
	privateBuild, err := harnessPrivateExternalBuildEvidence(events)
	if err != nil {
		return nil, err
	}
	hits := map[string]bool{}
	contractChecks := 0
	filesWritten := -1
	var writtenPaths []string
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
			writtenPaths = append([]string(nil), event.Data.WrittenPaths...)
		}
	}
	if contractChecks != 1 || !hits["projection.go"] || !hits["projection.typescript"] || filesWritten != 1 {
		return nil, fmt.Errorf("implementation edit did not use the incremental preparation path: contract_checks=%d go_projection_hit=%t typescript_projection_hit=%t files_written=%d written_paths=%v", contractChecks, hits["projection.go"], hits["projection.typescript"], filesWritten, writtenPaths)
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
		"external_source_build":     privateBuild,
	}, nil
}

func harnessHandoffEcho(ctx context.Context, root, url, want, processID string, expected *build.CandidateIdentity) (build.CandidateIdentity, string, time.Duration, error) {
	started := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"message":"handoff"}`))
	if err != nil {
		return build.CandidateIdentity{}, "", 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		return build.CandidateIdentity{}, "", 0, err
	}
	defer func() { _ = response.Body.Close() }()
	// Session inspection and HTTP are separate observations: the answer must
	// be attested by the observed host process.
	if actual := response.Header.Get("X-Scenery-Process-ID"); processID == "" || actual != processID {
		return build.CandidateIdentity{}, "", 0, fmt.Errorf("served process %q; want observed session process %q", actual, processID)
	}
	service := response.Header.Get("X-Scenery-Service-Process-ID")
	if service == "" || service == processID {
		return build.CandidateIdentity{}, "", 0, fmt.Errorf("answer named service process %q beside host %q", service, processID)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return build.CandidateIdentity{}, "", 0, err
	}
	var result struct {
		Message string `json:"message"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(data, &result) != nil || result.Message != want {
		return build.CandidateIdentity{}, "", 0, fmt.Errorf("served response %d %s; want %q", response.StatusCode, data, want)
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
			return build.CandidateIdentity{}, "", 0, fmt.Errorf("verify served candidate: %w", verifyErr)
		}
		wantIdentity = &candidate
	}
	if !harnessSameBuild(served, *wantIdentity) {
		return build.CandidateIdentity{}, "", 0, fmt.Errorf("served identity does not match the exact candidate")
	}
	served.SpecRevision = wantIdentity.SpecRevision
	served.Producer = wantIdentity.Producer
	served.FrameworkSourceDigest = wantIdentity.FrameworkSourceDigest
	served.FrameworkExecutableDigest = wantIdentity.FrameworkExecutableDigest
	return served, service, responseLatency, nil
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
