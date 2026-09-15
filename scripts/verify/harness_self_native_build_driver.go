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
	"scenery.sh/internal/nativebuilddriver"
)

const harnessNativeBuildDriverName = "full ONLV retained Go build driver experiment"
const harnessNativeBuildCompilerName = "full ONLV retained Go compiler experiment"

type nativeBuildExperimentSpec struct {
	benchmark, stepName, candidate, candidateMode, stage, orderSeed string
	control                                                         bool
}

var nativeBuildDriverSpec = nativeBuildExperimentSpec{
	benchmark: "native-build-driver", stepName: harnessNativeBuildDriverName, candidate: "driver", candidateMode: "driver",
	stage: "full_capture_stock_vs_full_capture_retained_driver", orderSeed: "0193-v1-balanced",
}

var nativeBuildCompilerSpec = nativeBuildExperimentSpec{
	benchmark: "native-build-compiler", stepName: harnessNativeBuildCompilerName, candidate: "compiler", candidateMode: "compiler",
	stage: "full_capture_stock_vs_retained_manifest_compiler", orderSeed: "0194-v1-balanced",
	control: true,
}

type nativeBuildDriverLane struct {
	name, backend, appRoot, stateRoot, socket, session, origin, token string
	scenery, source                                                   string
	env                                                               []string
	original, current                                                 []byte
	lastPID                                                           int
	logPath                                                           string
	initialIdentity, lastIdentity                                     nativeBuildServedIdentity
	bootstrap                                                         map[string]any
	buildArgv                                                         []string
	prepareMS                                                         float64
	productGeneration                                                 int
	productCandidate                                                  bool
	controlScenery                                                    string
	owner                                                             *exec.Cmd
	ownerLog                                                          *os.File
	closed                                                            bool
}

type nativeBuildPhaseWaterfall struct {
	Name            string  `json:"name"`
	StartedAt       string  `json:"started_at"`
	EndedAt         string  `json:"ended_at"`
	StartFromEditMS float64 `json:"start_from_edit_ms"`
	EndFromEditMS   float64 `json:"end_from_edit_ms"`
	DurationMS      float64 `json:"duration_ms"`
}

type nativeBuildDriverSample struct {
	Cohort                       string                      `json:"cohort"`
	Lane                         string                      `json:"lane"`
	Backend                      string                      `json:"backend"`
	Marker                       string                      `json:"marker"`
	Pair                         int                         `json:"pair"`
	Order                        int                         `json:"order"`
	CaptureMS                    float64                     `json:"capture_ms"`
	PackageLoadingMS             float64                     `json:"package_loading_ms,omitempty"`
	DirectoryValidationMS        float64                     `json:"directory_validation_ms,omitempty"`
	InputHashMS                  float64                     `json:"input_hash_ms,omitempty"`
	SnapshotMS                   float64                     `json:"snapshot_ms,omitempty"`
	ArchiveValidationMS          float64                     `json:"archive_validation_ms,omitempty"`
	SupportValidationMS          float64                     `json:"support_validation_ms,omitempty"`
	ArtifactBuildMS              float64                     `json:"artifact_build_ms"`
	BackendFinalizationMS        float64                     `json:"backend_finalization_ms,omitempty"`
	StateCommitMS                float64                     `json:"state_commit_ms,omitempty"`
	StateCommitFilesHashed       int                         `json:"state_commit_files_hashed,omitempty"`
	StateCommitBytesHashed       int64                       `json:"state_commit_bytes_hashed,omitempty"`
	StateCommitFilesReused       int                         `json:"state_commit_files_reused,omitempty"`
	StateCommitBytesReused       int64                       `json:"state_commit_bytes_reused,omitempty"`
	AccountableBuildMS           float64                     `json:"accountable_build_ms"`
	FirstVerifiedResponseMS      float64                     `json:"first_verified_response_ms"`
	AcceptedEditMS               float64                     `json:"accepted_edit_ms"`
	CandidateVerificationMS      float64                     `json:"candidate_verification_ms"`
	ImplementationCheckMS        float64                     `json:"implementation_check_ms,omitempty"`
	ImplementationCheckMedianMS  float64                     `json:"implementation_check_lane_median_ms,omitempty"`
	ImplementationCheckOutlier   bool                        `json:"implementation_check_outlier,omitempty"`
	ImplementationCheckDeviation float64                     `json:"implementation_check_deviation,omitempty"`
	FirstLaunchMS                float64                     `json:"first_launch_attestation_ms,omitempty"`
	ActivationMS                 float64                     `json:"runtime_activation_ms,omitempty"`
	ArtifactHandlingMS           float64                     `json:"artifact_handling_ms,omitempty"`
	SchedulerDelayMS             float64                     `json:"scheduler_delay_ms,omitempty"`
	SchedulingPolicy             string                      `json:"scheduling_policy,omitempty"`
	OperationID                  string                      `json:"operation_id,omitempty"`
	Phases                       []harnessEditLatencyPhase   `json:"phases,omitempty"`
	Waterfall                    []nativeBuildPhaseWaterfall `json:"waterfall,omitempty"`
	CompileMS                    float64                     `json:"compile_ms,omitempty"`
	LinkMS                       float64                     `json:"link_ms,omitempty"`
	ToolInvocations              int                         `json:"tool_invocations"`
	RebuiltPackages              []string                    `json:"rebuilt_packages,omitempty"`
	ArtifactDigest               string                      `json:"artifact_digest"`
	ImplementationRevision       string                      `json:"implementation_revision"`
	BuildInputDigest             string                      `json:"build_input_digest"`
	ProcessID                    int                         `json:"process_id"`
	Generation                   int                         `json:"generation"`
	OK                           bool                        `json:"ok"`
	Error                        string                      `json:"error,omitempty"`
}

type nativeBuildDriverRun struct {
	ctx, cleanupCtx                 context.Context
	repoRoot, sourceRoot            string
	root, evidence, runID           string
	baseEnv                         []string
	commands                        []nativeReloadCommandRecord
	worktrees                       []string
	lanes                           []*nativeBuildDriverLane
	ownerDigest                     string
	summary                         map[string]any
	spec                            nativeBuildExperimentSpec
	cohorts, warmups, rounds, churn int
	short                           bool
}

func runHarnessNativeBuildDriverStep(ctx context.Context, repoRoot, workloadRoot string, write, short bool) harnessStep {
	return runHarnessNativeBuildExperimentStep(ctx, repoRoot, workloadRoot, write, short, nativeBuildDriverSpec)
}

func runHarnessNativeBuildCompilerStep(ctx context.Context, repoRoot, workloadRoot string, write, short bool) harnessStep {
	return runHarnessNativeBuildExperimentStep(ctx, repoRoot, workloadRoot, write, short, nativeBuildCompilerSpec)
}

func runHarnessNativeBuildExperimentStep(ctx context.Context, repoRoot, workloadRoot string, write, short bool, spec nativeBuildExperimentSpec) harnessStep {
	started := time.Now()
	step := harnessStep{Name: spec.stepName, Command: []string{"go", "run", "./scripts/verify", "--benchmark", spec.benchmark, "--workload-root", workloadRoot, "--summary", "--write"}}
	if short {
		step.Command = append(step.Command, "--benchmark-short")
	}
	var err error
	step.Summary, err = runNativeBuildExperimentBenchmark(ctx, repoRoot, workloadRoot, write, short, spec)
	step.OK, step.DurationMS = err == nil, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func runNativeBuildExperimentBenchmark(parent context.Context, repoRoot, sourceRoot string, write, short bool, spec nativeBuildExperimentSpec) (summary map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Minute)
	defer cancel()
	cohorts, warmups, rounds, churn := 2, 2, 30, 50
	if short {
		cohorts, warmups, rounds, churn = 1, 1, 3, 0
	}
	run := &nativeBuildDriverRun{ctx: ctx, repoRoot: repoRoot, spec: spec, short: short, cohorts: cohorts, warmups: warmups, rounds: rounds, churn: churn, summary: map[string]any{
		"benchmark": spec.benchmark, "protocol": "scenery.native-build-driver", "protocol_version": 1, "decision": "incomplete",
		"workload_commit": nativeReloadONLVCommit, "comparison": spec.stage, "stage_i": spec.stage, "stage_ii": "conditional",
		"product_acceptance": "not_measured", "warmups_per_lane": warmups, "rounds_per_cohort": rounds,
		"cohort_count": cohorts, "churn_edits": churn, "order_seed": spec.orderSeed, "quantile_method": "nearest-rank", "short_observation": short,
		"scope":                "complete generated ONLV ./scenery_internal_main; identical logical AHJ handler bytes, production flags, generated contracts, target and runtime protocol",
		"cache_policy":         "lane-private GOCACHE; candidate bootstrap uses a separate disposable cache; benchmark lanes never share changed archives",
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
	run.root, err = os.MkdirTemp("", "scn-"+spec.benchmark+"-")
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
		run.evidence = filepath.Join(repoRoot, ".scenery", "harness", spec.benchmark, run.runID)
	}
	if err := os.MkdirAll(run.evidence, 0o700); err != nil {
		return summary, err
	}
	run.baseEnv = envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "DATABASE_URL", "SCENERY_DEV_CACHE_DIR"),
		"GOWORK=off", "SCENERY_DEV_CACHE_DIR="+filepath.Join(run.root, "cache"), "SCENERY_AGENT_HOME="+filepath.Join(run.root, "agent"))
	marker := filepath.Join(run.root, "owner.json")
	if err := nativeReloadWriteJSON(marker, map[string]any{"kind": spec.benchmark + "-experiment", "pid": os.Getpid(), "run_id": run.runID, "source": run.sourceRoot, "commit": nativeReloadONLVCommit, "created_at": time.Now().UTC()}); err != nil {
		return summary, err
	}
	run.ownerDigest, err = nativeReloadFileDigest(marker)
	if err != nil {
		return summary, err
	}
	summary["run_id"], summary["evidence_root"], summary["owned_root"] = run.runID, run.evidence, run.root
	summary["command"] = []string{"go", "run", "./scripts/verify", "--benchmark", spec.benchmark, "--workload-root", run.sourceRoot, "--summary", "--write"}
	if short {
		summary["command"] = append(summary["command"].([]string), "--benchmark-short")
	}
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
	run.summary["environment"] = run.nativeBuildEnvironmentSnapshot(before)
	originalTarget := filepath.Join(run.sourceRoot, "solar", "ahjs", "service.go")
	targetBefore, err := nativeReloadFileDigest(originalTarget)
	if err != nil {
		return summary, err
	}
	run.summary["original_checkout_target_digest_before"] = targetBefore
	if err := run.buildTools(); err != nil {
		return summary, err
	}
	correctness, correctnessErr := run.runNativeBuildDriverCorrectness()
	run.summary["correctness_matrix"] = correctness
	if correctnessErr != nil {
		return summary, correctnessErr
	}
	var allSamples []nativeBuildDriverSample
	for cohort := 1; cohort <= run.cohorts; cohort++ {
		cohortSamples, lanes, err := run.runCohort(cohort)
		nativeBuildMarkImplementationOutliers(cohortSamples)
		allSamples = append(allSamples, cohortSamples...)
		run.summary[fmt.Sprintf("cohort_%d", cohort)] = nativeBuildCohortSummary(cohortSamples)
		if err != nil {
			return summary, err
		}
		if cohort == run.cohorts && run.churn > 0 {
			for _, lane := range lanes {
				if lane.backend == spec.candidate {
					churn, churnErr := run.runChurn(lane, run.churn)
					run.summary["churn"] = churn
					if churnErr != nil {
						return summary, churnErr
					}
					resources := run.nativeBuildResourceSnapshot("after-churn", lane)
					resources["state_footprint"] = nativeBuildStateFootprint(lane.stateRoot)
					resources["generation_footprint"] = nativeBuildStateFootprint(filepath.Join(lane.stateRoot, "generations"))
					run.summary["resources_after_churn"] = resources
					run.summary["evidence_footprint_before_final_report"] = nativeBuildStateFootprint(run.evidence)
				}
			}
		}
		if err := run.closeLanes(lanes); err != nil {
			return summary, err
		}
	}
	run.summary["samples"] = allSamples
	run.summary["scheduling_policies"] = nativeBuildSchedulingPolicies(allSamples)
	aggregate := nativeBuildCohortSummary(allSamples)
	run.summary["aggregate"] = aggregate
	run.summary["execution_matrix"] = nativeBuildExecutionMatrix(aggregate, spec)
	deltas := map[string]any{"aggregate": nativeBuildComparisonDeltas(aggregate, "retained_stock", spec.candidate)}
	prepDeltas := map[string]any{"aggregate": nativeBuildComparisonDeltas(aggregate, "stock", "bare_stock")}
	for cohort := 1; cohort <= run.cohorts; cohort++ {
		key := fmt.Sprintf("cohort_%d", cohort)
		deltas[key] = nativeBuildComparisonDeltas(run.summary[key].(map[string]any), "retained_stock", spec.candidate)
		prepDeltas[key] = nativeBuildComparisonDeltas(run.summary[key].(map[string]any), "stock", "bare_stock")
	}
	run.summary["executor_comparison_deltas"] = deltas
	run.summary["preparation_comparison_deltas"] = prepDeltas
	run.summary["native_checkpoint"] = nativeBuildCheckpoint(aggregate, spec.candidate)
	run.summary["economics"] = nativeBuildEconomics(run.summary, aggregate, spec.candidate)
	decision := nativeBuildCandidateDecision(run.summary, spec.candidate)
	if run.short {
		decision = "short_observation_only"
	}
	run.summary["decision"] = decision
	if decision == "no_go_current_candidate" {
		run.summary["stage_ii"] = "unperformed_stage_i_payoff_gate_failed"
	}
	after, err := run.command(run.sourceRoot, "source-status-after", "git", "status", "--porcelain=v1")
	if err != nil {
		return summary, err
	}
	run.summary["source_status_after"] = string(after)
	run.summary["load_average_after"] = run.nativeBuildLoadAverage("environment-load-average-after")
	run.summary["source_status_unchanged"] = string(after) == string(before)
	targetAfter, err := nativeReloadFileDigest(originalTarget)
	if err != nil || targetAfter != targetBefore {
		return summary, fmt.Errorf("original ONLV benchmark target changed: %v", err)
	}
	run.summary["original_checkout_target_unchanged"] = true
	run.summary["product_acceptance"] = "not_measured"
	return summary, nil
}

func nativeBuildMarkImplementationOutliers(samples []nativeBuildDriverSample) {
	byLane := map[string][]float64{}
	for _, sample := range samples {
		if sample.OK && sample.ImplementationCheckMS > 0 {
			byLane[sample.Lane] = append(byLane[sample.Lane], sample.ImplementationCheckMS)
		}
	}
	medians := map[string]float64{}
	for lane, values := range byLane {
		sort.Float64s(values)
		medians[lane] = values[(len(values)-1)/2]
	}
	for index := range samples {
		median := medians[samples[index].Lane]
		if median <= 0 || samples[index].ImplementationCheckMS <= 0 {
			continue
		}
		deviation := (samples[index].ImplementationCheckMS - median) / median
		if deviation < 0 {
			deviation = -deviation
		}
		samples[index].ImplementationCheckMedianMS = median
		samples[index].ImplementationCheckDeviation = deviation
		samples[index].ImplementationCheckOutlier = deviation > 0.20
	}
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
	source, err := build.FrameworkSourceManifest(run.repoRoot)
	if err != nil {
		return err
	}
	linkerFlags, err := build.FrameworkProducerLinkerFlags(source.Digest)
	if err != nil {
		return err
	}
	if _, err := run.command(run.repoRoot, "build-tool-scenery-stock-control", "go", "build", "-tags=scenery_benchmark_stock", "-ldflags="+linkerFlags, "-o", filepath.Join(toolsRoot, "scenery-stock-control"), "./cmd/scenery"); err != nil {
		return err
	}
	return nil
}

func (run *nativeBuildDriverRun) runCohort(cohort int) ([]nativeBuildDriverSample, []*nativeBuildDriverLane, error) {
	backends := []string{"stock", "bare_stock", run.spec.candidate}
	if run.spec.control {
		backends = []string{"stock", "bare_stock", "retained_stock", run.spec.candidate}
	}
	if cohort == 2 {
		backends = append(backends[1:], backends[0])
	}
	lanes := make([]*nativeBuildDriverLane, 0, len(backends))
	for slot, backend := range backends {
		lane, err := run.prepareLane(cohort, slot, backend)
		if err != nil {
			_ = run.closeLanes(lanes)
			return nil, lanes, err
		}
		lanes = append(lanes, lane)
	}
	var samples []nativeBuildDriverSample
	for warmup := 0; warmup < run.warmups; warmup++ {
		for order, lane := range lanes {
			row, err := run.measureLane(lane, cohort, -(warmup + 1), order, fmt.Sprintf("c%d-warmup-%02d", cohort, warmup+1))
			if err != nil {
				return samples, lanes, err
			}
			_ = row
		}
	}
	for pair := 1; pair <= run.rounds; pair++ {
		order := append([]*nativeBuildDriverLane(nil), lanes...)
		rotation := pair % len(order)
		order = append(order[rotation:], order[:rotation]...)
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
	prepareStarted := time.Now()
	// Cohort zero is the low-level fault/cancellation matrix and intentionally
	// drives the retained package directly. Measured candidate cohorts exercise
	// the ordinary product build policy and supervisor lifecycle.
	productCandidate := cohort > 0 && backend == run.spec.candidate && run.spec.candidateMode == "compiler"
	name := fmt.Sprintf("cohort-%d-root-%c-%s", cohort, 'a'+rune(slot), backend)
	appRoot := filepath.Join(run.root, name)
	if _, err := run.command(run.sourceRoot, name+"-worktree", "git", "worktree", "add", "--quiet", "--detach", appRoot, nativeReloadONLVCommit); err != nil {
		return nil, err
	}
	run.worktrees = append(run.worktrees, appRoot)
	// Lane caches contain gigabytes of rebuildable archives. Keep them beneath
	// the run-owned temporary root; the durable report already contains raw
	// samples, bootstrap metadata, correctness evidence, and command logs.
	stateRoot := filepath.Join(run.root, "lane-state", name)
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
	frameworkCLI := harnessLocalSceneryBinaryPath(run.repoRoot)
	if !productCandidate {
		frameworkCLI = filepath.Join(run.evidence, "tools", "scenery-stock-control")
	}
	if _, err := run.commandEnv(run.baseEnv, appRoot, name+"-framework-use", frameworkCLI, "framework", "use", "--source", run.repoRoot, "-o", "json"); err != nil {
		return nil, err
	}
	selection, err := build.ReadFrameworkSelection(appRoot)
	if err != nil {
		return nil, err
	}
	lane := &nativeBuildDriverLane{name: name, backend: backend, appRoot: appRoot, stateRoot: stateRoot, socket: filepath.Join(run.root, fmt.Sprintf("c%d%c.sock", cohort, 'a'+rune(slot))), session: run.runID + "-" + name, scenery: filepath.Join(appRoot, "scripts", "scenery"), source: filepath.Join(appRoot, "solar", "ahjs", "service.go")}
	if !productCandidate {
		lane.controlScenery = selection.Executable
	}
	run.lanes = append(run.lanes, lane)
	initialMode := "stock"
	lane.productCandidate = productCandidate
	requiresBootstrap := backend == "retained_stock" || (backend == run.spec.candidate && !productCandidate)
	if backend == "bare_stock" {
		initialMode = "bare-stock"
	} else if requiresBootstrap {
		initialMode = "bootstrap"
	} else if backend == run.spec.candidate {
		initialMode = run.spec.candidateMode
	}
	if err := run.writeLaneConfig(lane, initialMode); err != nil {
		return nil, err
	}
	lane.env = run.laneEnvironment(lane)
	if _, err := run.commandEnv(lane.env, appRoot, name+"-prepare", "bun", "development/prepare.ts"); err != nil {
		return nil, err
	}
	bootstrap := map[string]any{"execution": "product_build_policy"}
	var buildArgv []string
	if !productCandidate {
		bootstrap, buildArgv, err = nativeBuildBootstrapResult(stateRoot)
		if err != nil {
			return nil, err
		}
	}
	lane.bootstrap, lane.buildArgv = bootstrap, buildArgv
	if requiresBootstrap {
		if _, err := run.commandEnv(lane.env, appRoot, name+"-down-bootstrap", lane.scenery, "down", "-o", "json"); err != nil {
			return nil, err
		}
		mode := "retained-stock"
		if backend == run.spec.candidate {
			if err := run.startOwner(lane); err != nil {
				return nil, err
			}
			mode = run.spec.candidateMode
		}
		if err := run.writeLaneConfig(lane, mode); err != nil {
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
	lane.logPath, err = nativeBuildLaneLogPath(lane.appRoot, lane.name)
	if err != nil {
		return nil, err
	}
	lane.token, err = nativeBuildDevToken(run.ctx, lane.origin)
	if err != nil {
		return nil, err
	}
	lane.original, err = os.ReadFile(lane.source)
	if err != nil {
		return nil, err
	}
	lane.current = append([]byte(nil), lane.original...)
	identity, _, err := nativeBuildAwaitResponse(run.ctx, lane, "query must be at most 200 characters", 0)
	if err != nil {
		return nil, err
	}
	lane.lastPID = identity.ProcessID
	lane.initialIdentity, lane.lastIdentity = identity, identity
	lane.prepareMS = nativeReloadMS(time.Since(prepareStarted))
	bootstrap["lane_prepare_ms"] = lane.prepareMS
	bootstrap["framework_source_digest"] = selection.Source.Digest
	bootstrap["framework_executable_digest"] = selection.ExecutableDigest
	bootstraps, _ := run.summary["bootstraps"].(map[string]any)
	if bootstraps == nil {
		bootstraps = map[string]any{}
		run.summary["bootstraps"] = bootstraps
	}
	bootstraps[lane.name] = bootstrap
	return lane, nil
}

func (run *nativeBuildDriverRun) laneEnvironment(lane *nativeBuildDriverLane) []string {
	values := map[string]string{
		"PATH":    filepath.Join(lane.stateRoot, "bin") + string(os.PathListSeparator) + envpolicy.Get("PATH"),
		"GOCACHE": filepath.Join(lane.stateRoot, "go-cache"),
	}
	if !lane.productCandidate {
		values["SCENERY_BIN"] = lane.controlScenery
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
		if data, err := os.ReadFile(ready); err == nil {
			var ownerReady map[string]any
			if json.Unmarshal(data, &ownerReady) != nil {
				return fmt.Errorf("driver owner ready record is invalid")
			}
			lane.bootstrap["owner_ready"] = ownerReady
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
	changed, err := nativeReloadEditedSource(lane.original, marker)
	if err != nil {
		return nativeBuildDriverSample{}, err
	}
	return run.measureLaneSource(lane, cohort, pair, order, marker, changed, "query must be at most 200 characters ["+marker+"]", nil)
}

func (run *nativeBuildDriverRun) measureLaneSource(lane *nativeBuildDriverLane, cohort, pair, order int, marker string, source []byte, expected string, restoreMTime *time.Time) (row nativeBuildDriverSample, resultErr error) {
	row = nativeBuildDriverSample{Cohort: fmt.Sprintf("cohort-%d", cohort), Lane: lane.name, Backend: lane.backend, Marker: marker, Pair: pair, Order: order}
	if current, err := os.ReadFile(lane.source); err != nil || !bytes.Equal(current, lane.current) {
		return row, fmt.Errorf("lane source ownership conflict: %v", err)
	}
	beforeGeneration := readNativeBuildCounter(lane.stateRoot)
	logOffset := int64(0)
	if info, err := os.Stat(lane.logPath); err == nil {
		logOffset = info.Size()
	}
	started := time.Now()
	if err := os.WriteFile(lane.source, source, 0o600); err != nil {
		return row, err
	}
	if restoreMTime != nil {
		if err := os.Chtimes(lane.source, *restoreMTime, *restoreMTime); err != nil {
			return row, err
		}
	}
	lane.current = append(lane.current[:0], source...)
	identity, responseAt, err := nativeBuildAwaitResponse(run.ctx, lane, expected, lane.lastPID)
	row.FirstVerifiedResponseMS = nativeReloadMS(responseAt.Sub(started))
	if err != nil {
		row.Error = err.Error()
		return row, err
	}
	row.OperationID, row.Phases = harnessEditLatencyPhases(lane.logPath, logOffset)
	row.Waterfall = nativeBuildWaterfall(row.Phases, started)
	for _, phase := range row.Phases {
		switch phase.Name {
		case "candidate.preflight":
			row.FirstLaunchMS = phase.DurationMS
		case "candidate.prepare":
			row.ArtifactHandlingMS = phase.DurationMS
		case "build.queue":
			row.SchedulerDelayMS = phase.DurationMS + phase.QueueMS
		case "process.scheduling":
			row.SchedulingPolicy = phase.Reason
		case "runtime.activation":
			row.ActivationMS = phase.DurationMS
		case "implementation.check":
			row.ImplementationCheckMS = phase.DurationMS
		}
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
	productCandidate := lane.productCandidate
	var result nativeBuildBackendResult
	var generation int
	artifactPath := ""
	if productCandidate {
		lane.productGeneration++
		generation = lane.productGeneration
		result, err = nativeBuildProductResult(row.Phases, lane.session, generation)
		if err != nil {
			return row, err
		}
		artifactPath, err = nativeBuildPreparedExecutable(row.Phases)
		if err == nil {
			result.ArtifactDigest, _, err = nativebuilddriver.FileDigest(artifactPath)
		}
	} else {
		result, generation, err = nativeBuildReadResult(lane.stateRoot, beforeGeneration)
	}
	if err != nil {
		return row, err
	}
	row.CaptureMS, row.ArtifactBuildMS = result.CaptureMS, result.ArtifactBuildMS
	row.PackageLoadingMS, row.DirectoryValidationMS, row.InputHashMS, row.SnapshotMS = result.PackageLoadingMS, result.DirectoryValidationMS, result.InputHashMS, result.SnapshotMS
	row.AccountableBuildMS = result.TransactionMS
	row.ArchiveValidationMS = result.ArchiveValidationMS
	row.SupportValidationMS = result.SupportValidationMS
	row.BackendFinalizationMS = result.FinalizationMS
	row.StateCommitMS = result.StateCommit.DurationMS
	row.StateCommitFilesHashed = result.StateCommit.FilesHashed
	row.StateCommitBytesHashed = result.StateCommit.BytesHashed
	row.StateCommitFilesReused = result.StateCommit.FilesReused
	row.StateCommitBytesReused = result.StateCommit.BytesReused
	row.CompileMS, row.LinkMS, row.ToolInvocations, row.RebuiltPackages = result.CompileMS, result.LinkMS, result.ToolInvocations, result.RebuiltPackages
	row.ArtifactDigest, row.Generation = result.ArtifactDigest, generation
	row.ImplementationRevision, row.BuildInputDigest, row.ProcessID = identity.ImplementationRevision, identity.BuildInputDigest, identity.ProcessID
	expectedStatus, expectedBackend := "stock_go_build", "stock"
	switch lane.backend {
	case "bare_stock":
		expectedStatus, expectedBackend = "bare_stock_go_build", "bare_stock"
	case "retained_stock":
		expectedStatus, expectedBackend = "retained_capture_stock_build", "retained_stock"
	case run.spec.candidate:
		expectedStatus, expectedBackend = "supported_and_rebuilt", "retained_compiler"
	}
	if result.Status != expectedStatus || result.Backend != expectedBackend || result.Owner != lane.session || result.RequestSequence != uint64(generation) || row.ArtifactDigest == "" || identity.ProcessID == lane.lastPID {
		return row, fmt.Errorf("invalid backend result status=%s digest=%q pid=%d", result.Status, row.ArtifactDigest, identity.ProcessID)
	}
	if productCandidate {
		err = run.preserveNativeBuildProductEvidence(lane, result, artifactPath)
	} else {
		err = run.preserveNativeBuildLaneEvidence(lane, generation)
	}
	if err != nil {
		return row, err
	}
	lane.lastPID = identity.ProcessID
	lane.lastIdentity = identity
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
			_ = response.Body.Close()
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
	defer func() { _ = response.Body.Close() }()
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
	Status                string                  `json:"status"`
	Backend               string                  `json:"backend"`
	Owner                 string                  `json:"owner"`
	ArtifactDigest        string                  `json:"artifact_digest"`
	RequestSequence       uint64                  `json:"request_sequence"`
	CaptureMS             float64                 `json:"capture_ms"`
	PackageLoadingMS      float64                 `json:"package_loading_ms,omitempty"`
	DirectoryValidationMS float64                 `json:"directory_validation_ms,omitempty"`
	InputHashMS           float64                 `json:"input_hash_ms,omitempty"`
	SnapshotMS            float64                 `json:"snapshot_ms,omitempty"`
	ArchiveValidationMS   float64                 `json:"archive_validation_ms,omitempty"`
	SupportValidationMS   float64                 `json:"support_validation_ms,omitempty"`
	ArtifactBuildMS       float64                 `json:"artifact_build_ms"`
	CompileMS             float64                 `json:"compile_ms,omitempty"`
	LinkMS                float64                 `json:"link_ms,omitempty"`
	FinalizationMS        float64                 `json:"finalization_ms,omitempty"`
	TransactionMS         float64                 `json:"transaction_ms"`
	ToolInvocations       int                     `json:"tool_invocations"`
	RebuiltPackages       []string                `json:"rebuilt_packages,omitempty"`
	StateCommit           nativeBuildBackendPhase `json:"state_commit,omitempty"`
}

type nativeBuildBackendPhase struct {
	DurationMS  float64 `json:"duration_ms"`
	FilesHashed int     `json:"files_hashed"`
	BytesHashed int64   `json:"bytes_hashed"`
	FilesReused int     `json:"files_reused"`
	BytesReused int64   `json:"bytes_reused"`
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

func nativeBuildStateFootprint(root string) map[string]any {
	var bytes, files int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
		return nil
	})
	result := map[string]any{"bytes": bytes, "files": files}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func (run *nativeBuildDriverRun) closeLanes(lanes []*nativeBuildDriverLane) error {
	var result error
	for _, lane := range lanes {
		if lane == nil || lane.closed {
			continue
		}
		lane.closed = true
		if len(lane.original) != 0 {
			current, readErr := os.ReadFile(lane.source)
			result = errors.Join(result, readErr)
			if readErr == nil && !bytes.Equal(current, lane.original) {
				result = errors.Join(result, os.WriteFile(lane.source, lane.original, 0o600))
				lane.current = append(lane.current[:0], lane.original...)
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
		result = errors.Join(result, os.RemoveAll(lane.stateRoot))
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
