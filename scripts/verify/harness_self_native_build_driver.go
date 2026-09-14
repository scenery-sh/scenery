package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
)

const harnessNativeBuildDriverName = "full ONLV retained Go build driver experiment"

type nativeBuildDriverLane struct {
	name, backend, appRoot, stateRoot, socket, session, origin, token string
	scenery, source                                                   string
	env                                                               []string
	original                                                          []byte
	lastPID                                                           int
	owner                                                             *exec.Cmd
	ownerLog                                                          *os.File
}

type nativeBuildDriverSample struct {
	Cohort                  string   `json:"cohort"`
	Lane                    string   `json:"lane"`
	Backend                 string   `json:"backend"`
	Marker                  string   `json:"marker"`
	Pair                    int      `json:"pair"`
	Order                   int      `json:"order"`
	CaptureMS               float64  `json:"capture_ms"`
	ArtifactBuildMS         float64  `json:"artifact_build_ms"`
	AccountableBuildMS      float64  `json:"accountable_build_ms"`
	FirstVerifiedResponseMS float64  `json:"first_verified_response_ms"`
	AcceptedEditMS          float64  `json:"accepted_edit_ms"`
	CandidateVerificationMS float64  `json:"candidate_verification_ms"`
	CompileMS               float64  `json:"compile_ms,omitempty"`
	LinkMS                  float64  `json:"link_ms,omitempty"`
	ToolInvocations         int      `json:"tool_invocations"`
	RebuiltPackages         []string `json:"rebuilt_packages,omitempty"`
	ArtifactDigest          string   `json:"artifact_digest"`
	ImplementationRevision  string   `json:"implementation_revision"`
	BuildInputDigest        string   `json:"build_input_digest"`
	ProcessID               int      `json:"process_id"`
	Generation              int      `json:"generation"`
	OK                      bool     `json:"ok"`
	Error                   string   `json:"error,omitempty"`
}

type nativeBuildDriverRun struct {
	ctx, cleanupCtx       context.Context
	repoRoot, sourceRoot  string
	root, evidence, runID string
	baseEnv               []string
	commands              []nativeReloadCommandRecord
	worktrees             []string
	lanes                 []*nativeBuildDriverLane
	ownerDigest           string
	summary               map[string]any
}

func runHarnessNativeBuildDriverStep(ctx context.Context, repoRoot, workloadRoot string, write bool) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessNativeBuildDriverName, Command: []string{"go", "run", "./scripts/verify", "--benchmark", "native-build-driver", "--workload-root", workloadRoot, "--summary", "--write"}}
	var err error
	step.Summary, err = runNativeBuildDriverBenchmark(ctx, repoRoot, workloadRoot, write)
	step.OK, step.DurationMS = err == nil, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func runNativeBuildDriverBenchmark(parent context.Context, repoRoot, sourceRoot string, write bool) (summary map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 35*time.Minute)
	defer cancel()
	run := &nativeBuildDriverRun{ctx: ctx, repoRoot: repoRoot, summary: map[string]any{
		"benchmark": "native-build-driver", "protocol": "scenery.native-build-driver", "protocol_version": 1, "decision": "incomplete",
		"workload_commit": nativeReloadONLVCommit, "stage_i": "full_capture_stock_vs_full_capture_retained_driver",
		"stage_ii": "conditional", "product_acceptance": "not_measured", "warmups_per_lane": 2, "pairs_per_cohort": 30,
		"cohort_count": 2, "churn_edits": 50, "order_seed": "0193-v1-balanced", "quantile_method": "nearest-rank",
		"scope":                "complete generated ONLV ./scenery_internal_main; identical logical AHJ handler bytes, production flags, generated contracts, target and runtime protocol",
		"cache_policy":         "lane-private GOCACHE; driver bootstrap uses a separate disposable cache; stock and driver never share changed archives",
		"payoff_gate":          map[string]any{"median_relative_reduction": 0.25, "median_absolute_reduction_ms": 100, "p95_max_regression": 0.05},
		"native_checkpoint_ms": map[string]any{"build_p50": 200, "launch_ready_p50": 100, "build_to_response_p50": 250},
		"hardware":             map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU()},
	}}
	summary = run.summary
	var err error
	run.sourceRoot, err = filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return summary, err
	}
	run.root, err = os.MkdirTemp("", "scn-native-build-driver-")
	if err != nil {
		return summary, err
	}
	run.root, err = filepath.EvalSymlinks(run.root)
	if err != nil {
		return summary, err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return summary, err
	}
	run.runID = time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(nonce[:])
	run.evidence = filepath.Join(run.root, "evidence")
	if write {
		run.evidence = filepath.Join(repoRoot, ".scenery", "harness", "native-build-driver", run.runID)
	}
	if err := os.MkdirAll(run.evidence, 0o700); err != nil {
		return summary, err
	}
	run.baseEnv = envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "DATABASE_URL", "SCENERY_DEV_CACHE_DIR"),
		"GOWORK=off", "SCENERY_DEV_CACHE_DIR="+filepath.Join(run.root, "cache"), "SCENERY_AGENT_HOME="+filepath.Join(run.root, "agent"))
	marker := filepath.Join(run.root, "owner.json")
	if err := nativeReloadWriteJSON(marker, map[string]any{"kind": "native-build-driver-experiment", "pid": os.Getpid(), "run_id": run.runID, "source": run.sourceRoot, "commit": nativeReloadONLVCommit, "created_at": time.Now().UTC()}); err != nil {
		return summary, err
	}
	run.ownerDigest, err = nativeReloadFileDigest(marker)
	if err != nil {
		return summary, err
	}
	summary["run_id"], summary["evidence_root"], summary["owned_root"] = run.runID, run.evidence, run.root
	summary["command"] = []string{"go", "run", "./scripts/verify", "--benchmark", "native-build-driver", "--workload-root", run.sourceRoot, "--summary", "--write"}
	summary["cwd"] = repoRoot

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		run.cleanupCtx = cleanupCtx
		resultErr = errors.Join(resultErr, run.cleanup())
		run.summary["commands"] = run.commands
		if resultErr != nil {
			run.summary["error"] = resultErr.Error()
			if run.summary["decision"] == "incomplete" {
				run.summary["decision"] = "invalid_evidence"
			}
		}
		resultErr = errors.Join(resultErr, nativeReloadWriteJSON(filepath.Join(run.evidence, "report.json"), run.summary))
	}()

	before, err := run.command(run.sourceRoot, "source-status-before", "git", "status", "--porcelain=v1")
	if err != nil {
		return summary, err
	}
	run.summary["source_status_before"] = string(before)
	if err := run.buildTools(); err != nil {
		return summary, err
	}
	var allSamples []nativeBuildDriverSample
	for cohort := 1; cohort <= 2; cohort++ {
		cohortSamples, lanes, err := run.runCohort(cohort)
		allSamples = append(allSamples, cohortSamples...)
		run.summary[fmt.Sprintf("cohort_%d", cohort)] = nativeBuildCohortSummary(cohortSamples)
		if err != nil {
			return summary, err
		}
		if cohort == 2 {
			for _, lane := range lanes {
				if lane.backend == "driver" {
					churn, churnErr := run.runChurn(lane, 50)
					run.summary["churn"] = churn
					if churnErr != nil {
						return summary, churnErr
					}
				}
			}
		}
		if err := run.closeLanes(lanes); err != nil {
			return summary, err
		}
	}
	run.summary["samples"] = allSamples
	decision := nativeBuildDriverDecision(run.summary)
	run.summary["decision"] = decision
	if decision == "no_go_current_candidate" {
		run.summary["stage_ii"] = "unperformed_stage_i_payoff_gate_failed"
	}
	after, err := run.command(run.sourceRoot, "source-status-after", "git", "status", "--porcelain=v1")
	if err != nil || string(after) != string(before) {
		return summary, fmt.Errorf("original ONLV checkout changed: %v", err)
	}
	run.summary["original_checkout_status_unchanged"] = true
	run.summary["product_acceptance"] = "not_measured"
	return summary, nil
}

func (run *nativeBuildDriverRun) command(cwd, name, program string, args ...string) ([]byte, error) {
	data, record, err := nativeReloadCommand(run.ctx, cwd, run.baseEnv, filepath.Join(run.evidence, name+".log"), program, args...)
	run.commands = append(run.commands, record)
	return data, err
}

func (run *nativeBuildDriverRun) commandEnv(env []string, cwd, name, program string, args ...string) ([]byte, error) {
	data, record, err := nativeReloadCommand(run.ctx, cwd, env, filepath.Join(run.evidence, name+".log"), program, args...)
	run.commands = append(run.commands, record)
	return data, err
}

func (run *nativeBuildDriverRun) buildTools() error {
	toolsRoot := filepath.Join(run.evidence, "tools")
	if err := os.MkdirAll(toolsRoot, 0o700); err != nil {
		return err
	}
	for name, pkg := range map[string]string{"gowrap": "./scripts/verify/internal/nativebuilddriver/cmd/gowrap", "toolexec": "./scripts/verify/internal/nativebuilddriver/cmd/toolexec", "driver": "./scripts/verify/internal/nativebuilddriver/cmd/driver"} {
		if _, err := run.command(run.repoRoot, "build-tool-"+name, "go", "build", "-o", filepath.Join(toolsRoot, name), pkg); err != nil {
			return err
		}
	}
	return nil
}

func (run *nativeBuildDriverRun) runCohort(cohort int) ([]nativeBuildDriverSample, []*nativeBuildDriverLane, error) {
	backends := []string{"stock", "driver"}
	if cohort == 2 {
		backends[0], backends[1] = backends[1], backends[0]
	}
	lanes := make([]*nativeBuildDriverLane, 0, 2)
	for slot, backend := range backends {
		lane, err := run.prepareLane(cohort, slot, backend)
		if err != nil {
			_ = run.closeLanes(lanes)
			return nil, lanes, err
		}
		lanes = append(lanes, lane)
	}
	var samples []nativeBuildDriverSample
	for warmup := 0; warmup < 2; warmup++ {
		for order, lane := range lanes {
			row, err := run.measureLane(lane, cohort, -(warmup + 1), order, fmt.Sprintf("c%d-warmup-%02d", cohort, warmup+1))
			if err != nil {
				return samples, lanes, err
			}
			_ = row
		}
	}
	for pair := 1; pair <= 30; pair++ {
		order := []*nativeBuildDriverLane{lanes[0], lanes[1]}
		if pair%2 == 0 {
			order[0], order[1] = order[1], order[0]
		}
		marker := fmt.Sprintf("c%d-pair-%02d", cohort, pair)
		for index, lane := range order {
			row, err := run.measureLane(lane, cohort, pair, index, marker)
			samples = append(samples, row)
			if err != nil {
				return samples, lanes, err
			}
		}
		run.summary[fmt.Sprintf("cohort_%d_progress", cohort)] = map[string]any{"completed_pairs": pair, "samples": len(samples)}
		if err := nativeReloadWriteJSON(filepath.Join(run.evidence, fmt.Sprintf("cohort-%d-progress.json", cohort)), samples); err != nil {
			return samples, lanes, err
		}
	}
	return samples, lanes, nil
}

func (run *nativeBuildDriverRun) prepareLane(cohort, slot int, backend string) (*nativeBuildDriverLane, error) {
	name := fmt.Sprintf("cohort-%d-root-%c-%s", cohort, 'a'+rune(slot), backend)
	appRoot := filepath.Join(run.root, name)
	if _, err := run.command(run.sourceRoot, name+"-worktree", "git", "worktree", "add", "--quiet", "--detach", appRoot, nativeReloadONLVCommit); err != nil {
		return nil, err
	}
	run.worktrees = append(run.worktrees, appRoot)
	stateRoot := filepath.Join(run.evidence, name)
	binRoot := filepath.Join(stateRoot, "bin")
	if err := os.MkdirAll(binRoot, 0o700); err != nil {
		return nil, err
	}
	for src, dst := range map[string]string{"gowrap": "go", "toolexec": "toolexec"} {
		data, err := os.ReadFile(filepath.Join(run.evidence, "tools", src))
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(binRoot, dst), data, 0o700); err != nil {
			return nil, err
		}
	}
	if _, err := run.command(appRoot, name+"-framework-use", harnessLocalSceneryBinaryPath(run.repoRoot), "framework", "use", "--source", run.repoRoot, "-o", "json"); err != nil {
		return nil, err
	}
	if _, err := build.ReadFrameworkSelection(appRoot); err != nil {
		return nil, err
	}
	lane := &nativeBuildDriverLane{name: name, backend: backend, appRoot: appRoot, stateRoot: stateRoot, socket: filepath.Join(run.root, fmt.Sprintf("c%d%c.sock", cohort, 'a'+rune(slot))), session: run.runID + "-" + name, scenery: filepath.Join(appRoot, "scripts", "scenery"), source: filepath.Join(appRoot, "solar", "ahjs", "service.go")}
	run.lanes = append(run.lanes, lane)
	if err := run.writeLaneConfig(lane, map[bool]string{true: "bootstrap", false: "stock"}[backend == "driver"]); err != nil {
		return nil, err
	}
	lane.env = run.laneEnvironment(lane)
	if _, err := run.commandEnv(lane.env, appRoot, name+"-prepare", "bun", "development/prepare.ts"); err != nil {
		return nil, err
	}
	if backend == "driver" {
		if _, err := run.commandEnv(lane.env, appRoot, name+"-down-bootstrap", lane.scenery, "down", "-o", "json"); err != nil {
			return nil, err
		}
		if err := run.startOwner(lane); err != nil {
			return nil, err
		}
		if err := run.writeLaneConfig(lane, "driver"); err != nil {
			return nil, err
		}
		lane.env = run.laneEnvironment(lane)
		if _, err := run.commandEnv(lane.env, appRoot, name+"-up-driver", lane.scenery, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
			return nil, err
		}
	}
	marker, err := os.ReadFile(filepath.Join(appRoot, ".scenery", "task-environment.json"))
	if err != nil {
		return nil, err
	}
	var task struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(marker, &task); err != nil || task.Origin == "" {
		return nil, fmt.Errorf("%s fixture origin unavailable: %v", name, err)
	}
	lane.origin = task.Origin
	lane.token, err = nativeBuildDevToken(run.ctx, lane.origin)
	if err != nil {
		return nil, err
	}
	lane.original, err = os.ReadFile(lane.source)
	if err != nil {
		return nil, err
	}
	identity, _, err := nativeBuildAwaitResponse(run.ctx, lane, "query must be at most 200 characters", 0)
	if err != nil {
		return nil, err
	}
	lane.lastPID = identity.ProcessID
	return lane, nil
}

func (run *nativeBuildDriverRun) laneEnvironment(lane *nativeBuildDriverLane) []string {
	values := map[string]string{
		"PATH":    filepath.Join(lane.stateRoot, "bin") + string(os.PathListSeparator) + envpolicy.Get("PATH"),
		"GOCACHE": filepath.Join(lane.stateRoot, "go-cache"),
	}
	args := make([]string, 0, len(values))
	for key, value := range values {
		args = append(args, key+"="+value)
	}
	sort.Strings(args)
	return envWithOverrides(run.baseEnv, args...)
}

func (run *nativeBuildDriverRun) writeLaneConfig(lane *nativeBuildDriverLane, mode string) error {
	return nativeReloadWriteJSON(filepath.Join(lane.stateRoot, "config.json"), map[string]any{
		"protocol": "scenery.native-build-driver", "version": 1,
		"real_go": "/usr/local/go/bin/go", "mode": mode, "root": lane.stateRoot,
		"socket": lane.socket, "session": lane.session,
	})
}

func (run *nativeBuildDriverRun) startOwner(lane *nativeBuildDriverLane) error {
	var recipe struct {
		Workspace string `json:"workspace"`
	}
	data, err := os.ReadFile(filepath.Join(lane.stateRoot, "recipe.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &recipe); err != nil || recipe.Workspace == "" {
		return fmt.Errorf("driver recipe workspace unavailable: %v", err)
	}
	log, err := os.OpenFile(filepath.Join(lane.stateRoot, "owner.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	lane.ownerLog = log
	ready := filepath.Join(lane.stateRoot, "owner-ready.json")
	cmd := commandTreeContext(run.ctx, filepath.Join(run.evidence, "tools", "driver"), "serve", "--recipe", filepath.Join(lane.stateRoot, "recipe.json"), "--socket", lane.socket, "--session", lane.session, "--workspace", recipe.Workspace, "--ready", ready)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = run.repoRoot, run.baseEnv, log, log
	if err := cmd.Start(); err != nil {
		return err
	}
	lane.owner = cmd
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			return nil
		}
		if cmd.ProcessState != nil {
			return fmt.Errorf("driver owner exited before ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("driver owner readiness timeout")
}

func (run *nativeBuildDriverRun) measureLane(lane *nativeBuildDriverLane, cohort, pair, order int, marker string) (row nativeBuildDriverSample, resultErr error) {
	row = nativeBuildDriverSample{Cohort: fmt.Sprintf("cohort-%d", cohort), Lane: lane.name, Backend: lane.backend, Marker: marker, Pair: pair, Order: order}
	changed, err := nativeReloadEditedSource(lane.original, marker)
	if err != nil {
		return row, err
	}
	if current, err := os.ReadFile(lane.source); err != nil || (!bytes.Equal(current, lane.original) && !strings.Contains(string(current), "[c")) {
		return row, fmt.Errorf("lane source ownership conflict: %v", err)
	}
	beforeGeneration := readNativeBuildCounter(lane.stateRoot)
	started := time.Now()
	if err := os.WriteFile(lane.source, changed, 0o600); err != nil {
		return row, err
	}
	identity, responseAt, err := nativeBuildAwaitResponse(run.ctx, lane, "query must be at most 200 characters ["+marker+"]", lane.lastPID)
	row.FirstVerifiedResponseMS = nativeReloadMS(responseAt.Sub(started))
	if err != nil {
		row.Error = err.Error()
		return row, err
	}
	verificationStarted := time.Now()
	candidate, err := run.inspectCandidate(lane)
	verifiedAt := time.Now()
	row.CandidateVerificationMS = nativeReloadMS(verifiedAt.Sub(verificationStarted))
	row.AcceptedEditMS = nativeReloadMS(verifiedAt.Sub(started))
	if err != nil {
		return row, err
	}
	if candidate.ImplementationRevision != identity.ImplementationRevision || candidate.BuildInputDigest != identity.BuildInputDigest {
		return row, fmt.Errorf("candidate and served response identity differ")
	}
	result, generation, err := nativeBuildReadResult(lane.stateRoot, beforeGeneration)
	if err != nil {
		return row, err
	}
	row.CaptureMS, row.ArtifactBuildMS = result.CaptureMS, result.ArtifactBuildMS
	row.AccountableBuildMS = result.CaptureMS + result.ArtifactBuildMS
	row.CompileMS, row.LinkMS, row.ToolInvocations, row.RebuiltPackages = result.CompileMS, result.LinkMS, result.ToolInvocations, result.RebuiltPackages
	row.ArtifactDigest, row.Generation = result.ArtifactDigest, generation
	row.ImplementationRevision, row.BuildInputDigest, row.ProcessID = identity.ImplementationRevision, identity.BuildInputDigest, identity.ProcessID
	if result.Status != map[bool]string{true: "supported_and_rebuilt", false: "stock_go_build"}[lane.backend == "driver"] || row.ArtifactDigest == "" || identity.ProcessID == lane.lastPID {
		return row, fmt.Errorf("invalid backend result status=%s digest=%q pid=%d", result.Status, row.ArtifactDigest, identity.ProcessID)
	}
	lane.lastPID = identity.ProcessID
	row.OK = true
	return row, nil
}

type nativeBuildServedIdentity struct {
	ImplementationRevision string
	BuildInputDigest       string
	ProcessID              int
}

func nativeBuildAwaitResponse(ctx context.Context, lane *nativeBuildDriverLane, expected string, previousPID int) (nativeBuildServedIdentity, time.Time, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, lane.origin+"/api/solar/ahjs?query="+strings.Repeat("q", 512), nil)
		request.Header.Set("Authorization", "Bearer "+lane.token)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			pid, _ := strconv.Atoi(response.Header.Get("X-Scenery-Process-ID"))
			if readErr == nil && response.StatusCode == http.StatusBadRequest && bytes.Contains(data, []byte(strconv.Quote(expected))) && pid > 0 && (previousPID == 0 || pid != previousPID) {
				implementation, input := response.Header.Get("X-Scenery-Implementation-Revision"), response.Header.Get("X-Scenery-Build-Input-Digest")
				if strings.HasPrefix(implementation, "sha256:") && strings.HasPrefix(input, "sha256:") && response.Header.Get("X-Scenery-Go-Target") == "development" {
					return nativeBuildServedIdentity{implementation, input, pid}, time.Now(), nil
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nativeBuildServedIdentity{}, time.Now(), fmt.Errorf("timed out waiting for %s", expected)
}

func nativeBuildDevToken(ctx context.Context, origin string) (string, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/users/dev-bootstrap", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var result struct {
		Token string `json:"token"`
	}
	if response.StatusCode >= 300 {
		return "", fmt.Errorf("dev bootstrap returned %s", response.Status)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil || result.Token == "" {
		return "", fmt.Errorf("decode dev bootstrap: %v", err)
	}
	return result.Token, nil
}

type nativeBuildCandidate struct {
	ImplementationRevision string `json:"implementationRevision"`
	BuildInputDigest       string `json:"buildInputDigest"`
}

func (run *nativeBuildDriverRun) inspectCandidate(lane *nativeBuildDriverLane) (nativeBuildCandidate, error) {
	data, err := run.commandEnv(lane.env, lane.appRoot, lane.name+"-inspect-"+strconv.Itoa(readNativeBuildCounter(lane.stateRoot)), lane.scenery, "inspect", "build", "--verify-generation", "-o", "json")
	if err != nil {
		return nativeBuildCandidate{}, err
	}
	var envelope struct {
		Data struct {
			Candidate nativeBuildCandidate `json:"candidate_identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nativeBuildCandidate{}, err
	}
	if envelope.Data.Candidate.ImplementationRevision == "" {
		return nativeBuildCandidate{}, fmt.Errorf("candidate identity absent")
	}
	return envelope.Data.Candidate, nil
}

type nativeBuildBackendResult struct {
	Status, ArtifactDigest                        string
	CaptureMS, ArtifactBuildMS, CompileMS, LinkMS float64
	ToolInvocations                               int
	RebuiltPackages                               []string
}

func nativeBuildReadResult(root string, previous int) (nativeBuildBackendResult, int, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		generation := readNativeBuildCounter(root)
		if generation > previous {
			path := filepath.Join(root, "generations", fmt.Sprintf("generation-%04d", generation), "result.json")
			data, err := os.ReadFile(path)
			if err == nil {
				var raw struct {
					Status          string   `json:"status"`
					ArtifactDigest  string   `json:"artifact_digest"`
					CaptureMS       float64  `json:"capture_ms"`
					ArtifactBuildMS float64  `json:"artifact_build_ms"`
					CompileMS       float64  `json:"compile_ms"`
					LinkMS          float64  `json:"link_ms"`
					ToolInvocations int      `json:"tool_invocations"`
					RebuiltPackages []string `json:"rebuilt_packages"`
					Capture         struct {
						DurationMS float64 `json:"duration_ms"`
					} `json:"capture"`
				}
				if err := json.Unmarshal(data, &raw); err != nil {
					return nativeBuildBackendResult{}, generation, err
				}
				if raw.CaptureMS == 0 {
					raw.CaptureMS = raw.Capture.DurationMS
				}
				return nativeBuildBackendResult{raw.Status, raw.ArtifactDigest, raw.CaptureMS, raw.ArtifactBuildMS, raw.CompileMS, raw.LinkMS, raw.ToolInvocations, raw.RebuiltPackages}, generation, nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nativeBuildBackendResult{}, previous, fmt.Errorf("backend result did not appear after generation %d", previous)
}

func readNativeBuildCounter(root string) int {
	data, _ := os.ReadFile(filepath.Join(root, "counter"))
	value, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return value
}

func nativeBuildCohortSummary(samples []nativeBuildDriverSample) map[string]any {
	result := map[string]any{"sample_count": len(samples), "complete_pairs": len(samples) / 2}
	for _, backend := range []string{"stock", "driver"} {
		var capture, build, response, accepted []float64
		for _, sample := range samples {
			if sample.Backend == backend && sample.OK {
				capture = append(capture, sample.CaptureMS)
				build = append(build, sample.AccountableBuildMS)
				response = append(response, sample.FirstVerifiedResponseMS)
				accepted = append(accepted, sample.AcceptedEditMS)
			}
		}
		result[backend] = map[string]any{"count": len(build), "capture": nativeReloadStats(capture), "accountable_build": nativeReloadStats(build), "first_verified_response": nativeReloadStats(response), "accepted_edit": nativeReloadStats(accepted)}
	}
	return result
}

func nativeBuildDriverDecision(summary map[string]any) string {
	for cohort := 1; cohort <= 2; cohort++ {
		value, ok := summary[fmt.Sprintf("cohort_%d", cohort)].(map[string]any)
		if !ok || value["complete_pairs"] != 30 {
			return "incomplete"
		}
		stock := value["stock"].(map[string]any)
		driver := value["driver"].(map[string]any)
		s50 := stock["accountable_build"].(map[string]any)["p50_ms"].(float64)
		d50 := driver["accountable_build"].(map[string]any)["p50_ms"].(float64)
		s95 := stock["accountable_build"].(map[string]any)["p95_ms"].(float64)
		d95 := driver["accountable_build"].(map[string]any)["p95_ms"].(float64)
		sa95 := stock["accepted_edit"].(map[string]any)["p95_ms"].(float64)
		da95 := driver["accepted_edit"].(map[string]any)["p95_ms"].(float64)
		sa50 := stock["accepted_edit"].(map[string]any)["p50_ms"].(float64)
		da50 := driver["accepted_edit"].(map[string]any)["p50_ms"].(float64)
		if s50-d50 < 100 || (s50-d50)/s50 < .25 || d95 > s95*1.05 || da50 >= sa50 || da95 > sa95*1.05 {
			return "no_go_current_candidate"
		}
	}
	return "go_for_next_experiment"
}

func (run *nativeBuildDriverRun) runChurn(lane *nativeBuildDriverLane, count int) (map[string]any, error) {
	startPID := lane.lastPID
	for i := 1; i <= count; i++ {
		row, err := run.measureLane(lane, 3, i, 0, fmt.Sprintf("churn-%02d", i))
		if err != nil {
			return map[string]any{"completed": i - 1, "error": err.Error()}, err
		}
		if !row.OK {
			return map[string]any{"completed": i - 1}, fmt.Errorf("churn row %d failed", i)
		}
	}
	entries, _ := filepath.Glob(filepath.Join(lane.stateRoot, "generations", "generation-*"))
	var stat syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &stat)
	return map[string]any{"completed": count, "start_pid": startPID, "end_pid": lane.lastPID, "retained_generation_count": len(entries), "generation_limit": 4, "child_max_rss_raw": stat.Maxrss}, nil
}

func (run *nativeBuildDriverRun) closeLanes(lanes []*nativeBuildDriverLane) error {
	var result error
	for _, lane := range lanes {
		if lane == nil {
			continue
		}
		if len(lane.original) != 0 {
			current, readErr := os.ReadFile(lane.source)
			result = errors.Join(result, readErr)
			if readErr == nil && !bytes.Equal(current, lane.original) {
				result = errors.Join(result, os.WriteFile(lane.source, lane.original, 0o600))
				if run.ctx.Err() == nil && lane.token != "" && lane.lastPID != 0 {
					identity, _, restoreErr := nativeBuildAwaitResponse(run.ctx, lane, "query must be at most 200 characters", lane.lastPID)
					result = errors.Join(result, restoreErr)
					if restoreErr == nil {
						lane.lastPID = identity.ProcessID
					}
				}
			}
		}
		ctx := run.cleanupCtx
		if ctx == nil {
			ctx = run.ctx
		}
		ownerPID := nativeBuildSupervisorPID(lane.appRoot)
		_, record, downErr := nativeReloadCommand(ctx, lane.appRoot, lane.env, "", lane.scenery, "down", "-o", "json")
		run.commands = append(run.commands, record)
		if ownerPID > 0 && !nativeBuildWaitProcessExit(ownerPID, 5*time.Second) {
			command, _ := exec.Command("ps", "-p", strconv.Itoa(ownerPID), "-o", "command=").Output()
			if !strings.Contains(string(command), lane.appRoot) {
				result = errors.Join(result, fmt.Errorf("refusing to signal unverified supervisor pid %d after down error %v", ownerPID, downErr))
			} else if err := syscall.Kill(ownerPID, syscall.SIGTERM); err != nil || !nativeBuildWaitProcessExit(ownerPID, 10*time.Second) {
				result = errors.Join(result, fmt.Errorf("owned supervisor %d did not stop: down=%v signal=%v", ownerPID, downErr, err))
			}
		} else if downErr != nil {
			result = errors.Join(result, downErr)
		}
		if lane.owner != nil && lane.owner.Process != nil {
			_ = lane.owner.Process.Signal(syscall.SIGTERM)
			result = errors.Join(result, lane.owner.Wait())
			lane.owner = nil
		}
		if lane.ownerLog != nil {
			result = errors.Join(result, lane.ownerLog.Close())
			lane.ownerLog = nil
		}
	}
	return result
}

func nativeBuildSupervisorPID(appRoot string) int {
	paths, _ := filepath.Glob(filepath.Join(appRoot, ".scenery", "sessions", "*", "manifest.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record struct {
			AppRoot  string `json:"app_root"`
			OwnerPID int    `json:"owner_pid"`
			Status   string `json:"status"`
		}
		if json.Unmarshal(data, &record) == nil && filepath.Clean(record.AppRoot) == filepath.Clean(appRoot) && record.OwnerPID > 0 && record.Status == "running" {
			return record.OwnerPID
		}
	}
	return 0
}

func nativeBuildWaitProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}

func (run *nativeBuildDriverRun) cleanup() error {
	var result error
	result = errors.Join(result, run.closeLanes(run.lanes))
	digest, err := nativeReloadFileDigest(filepath.Join(run.root, "owner.json"))
	if err != nil || digest != run.ownerDigest {
		return errors.Join(result, fmt.Errorf("owned root marker mismatch: %v", err))
	}
	for i := len(run.worktrees) - 1; i >= 0; i-- {
		_, _, err := nativeReloadCommand(run.cleanupCtx, run.sourceRoot, run.baseEnv, "", "git", "worktree", "remove", "--force", run.worktrees[i])
		result = errors.Join(result, err)
	}
	run.summary["owned_worktrees_removed"] = result == nil
	if result == nil {
		result = os.RemoveAll(run.root)
	}
	return result
}
