package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (run *nativeBuildDriverRun) nativeBuildEnvironmentSnapshot(sourceStatus []byte) map[string]any {
	result := map[string]any{
		"benchmark_pid": os.Getpid(), "launcher_ancestry": nativeBuildLauncherAncestry(os.Getpid()),
		"source_status": string(sourceStatus), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU(),
	}
	if data, err := run.command(run.repoRoot, "environment-scenery-commit", "git", "rev-parse", "HEAD"); err == nil {
		result["scenery_commit"] = strings.TrimSpace(string(data))
	}
	if data, err := run.command(run.repoRoot, "environment-scenery-diff", "git", "diff", "HEAD", "--binary", "--"); err == nil {
		hash := sha256.New()
		_, _ = hash.Write(data)
		result["scenery_worktree_diff_bytes"] = len(data)
		if untracked, listErr := run.command(run.repoRoot, "environment-scenery-untracked", "git", "ls-files", "--others", "--exclude-standard"); listErr == nil {
			paths := strings.Fields(string(untracked))
			sort.Strings(paths)
			result["scenery_untracked_files"] = paths
			for _, path := range paths {
				content, readErr := os.ReadFile(filepath.Join(run.repoRoot, path))
				if readErr != nil {
					continue
				}
				_, _ = hash.Write([]byte("\x00" + path + "\x00"))
				_, _ = hash.Write(content)
			}
		}
		result["scenery_dirty_source_digest"] = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	}
	if data, err := run.command(run.repoRoot, "environment-go", "/usr/local/go/bin/go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "GOROOT", "GOENV", "GOFLAGS", "GOWORK", "GOTOOLCHAIN", "CGO_ENABLED", "CC", "CXX"); err == nil {
		var value map[string]string
		if json.Unmarshal(data, &value) == nil {
			result["go"] = value
		}
	}
	if data, err := run.command(run.repoRoot, "environment-macos", "sw_vers"); err == nil {
		result["macos"] = strings.TrimSpace(string(data))
	}
	if data, err := run.command(run.repoRoot, "environment-hardware", "sysctl", "-n", "hw.model", "hw.memsize", "machdep.cpu.brand_string"); err == nil {
		result["hardware"] = strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	result["load_average"] = run.nativeBuildLoadAverage("environment-load-average")
	policy := map[string]any{"per_application_authorization": "not exposed by a stable command-line read API; launcher ancestry is recorded"}
	if data, err := run.command(run.repoRoot, "environment-developer-tools", "/usr/sbin/DevToolsSecurity", "-status"); err == nil {
		policy["developer_tools_security"] = strings.TrimSpace(string(data))
	}
	if data, err := run.command(run.repoRoot, "environment-gatekeeper", "/usr/sbin/spctl", "--status"); err == nil {
		policy["gatekeeper"] = strings.TrimSpace(string(data))
	}
	result["macos_launcher_policy"] = policy
	return result
}

func nativeBuildLauncherAncestry(pid int) []string {
	var result []string
	for depth := 0; pid > 1 && depth < 12; depth++ {
		data, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=", "-o", "ppid=", "-o", "comm=", "-o", "args=").Output()
		if err != nil || len(bytes.TrimSpace(data)) == 0 {
			break
		}
		result = append(result, strings.TrimSpace(string(data)))
		parent, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
		if err != nil {
			break
		}
		next, err := strconv.Atoi(strings.TrimSpace(string(parent)))
		if err != nil || next == pid {
			break
		}
		pid = next
	}
	return result
}

func nativeBuildPreparedExecutable(phases []harnessEditLatencyPhase) (string, error) {
	for _, phase := range phases {
		if phase.Name == "candidate.prepare" && phase.OK && len(phase.WrittenPaths) == 1 {
			path, err := filepath.EvalSymlinks(phase.WrittenPaths[0])
			if err != nil {
				return "", err
			}
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("product candidate retained executable path is absent")
}

func nativeBuildProductResult(phases []harnessEditLatencyPhase, owner string, generation int) (nativeBuildBackendResult, error) {
	result := nativeBuildBackendResult{Status: "supported_and_rebuilt", Backend: "retained_compiler", Owner: owner, RequestSequence: uint64(generation)}
	productBackend, retainedCommand := false, false
	for _, phase := range phases {
		switch phase.Name {
		case "build.backend":
			productBackend = phase.OK && phase.Reason == "retained_compiler"
		case "go.input_discovery":
			if phase.Reason == "complete_retained_domain" {
				result.CaptureMS = phase.DurationMS
			}
		case "go.package_loading":
			result.PackageLoadingMS = phase.DurationMS
		case "go.directory_validation":
			result.DirectoryValidationMS = phase.DurationMS
		case "go.input_hash":
			result.InputHashMS = phase.DurationMS
		case "go.snapshot":
			result.SnapshotMS = phase.DurationMS
		case "go.retained_archive":
			result.ArchiveValidationMS = phase.DurationMS
		case "go.retained_support":
			result.SupportValidationMS = phase.DurationMS
		case "go.compile":
			result.CompileMS = phase.DurationMS
		case "go.link":
			result.LinkMS = phase.DurationMS
		case "go.finalization":
			result.FinalizationMS = phase.DurationMS
		case "go.state_commit":
			result.StateCommit = nativeBuildBackendPhase{DurationMS: phase.DurationMS, FilesHashed: phase.FilesHashed, BytesHashed: phase.BytesHashed, FilesReused: phase.FilesReused, BytesReused: phase.BytesReused}
		case "go.command":
			if phase.Cache == "retained_recipe" && phase.Reason == "build" {
				retainedCommand = phase.OK
				result.TransactionMS, result.ToolInvocations = phase.DurationMS, phase.Actions
				result.RebuiltPackages = append([]string(nil), phase.PackagesRebuilt...)
			}
		}
	}
	if !productBackend || !retainedCommand || result.TransactionMS <= 0 || result.ToolInvocations <= 0 {
		return nativeBuildBackendResult{}, fmt.Errorf("product candidate did not expose a successful retained compiler transaction")
	}
	result.ArtifactBuildMS = result.CompileMS + result.LinkMS + result.FinalizationMS
	return result, nil
}

func (run *nativeBuildDriverRun) preserveNativeBuildProductEvidence(lane *nativeBuildDriverLane, result nativeBuildBackendResult, artifactPath string) error {
	destination := filepath.Join(run.evidence, "lanes", lane.name)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	if err := nativeReloadWriteJSON(filepath.Join(destination, "result.json"), result); err != nil {
		return err
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "application"), artifact, 0o700)
}

func (run *nativeBuildDriverRun) preserveNativeBuildLaneEvidence(lane *nativeBuildDriverLane, generation int) error {
	destination := filepath.Join(run.evidence, "lanes", lane.name)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	resultPath := filepath.Join(lane.stateRoot, "generations", fmt.Sprintf("generation-%04d", generation), "result.json")
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destination, "result.json"), resultData, 0o600); err != nil {
		return err
	}
	output := filepath.Join(lane.stateRoot, "generations", fmt.Sprintf("generation-%04d", generation), "application")
	artifact, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "application"), artifact, 0o700)
}

func nativeBuildWaterfall(phases []harnessEditLatencyPhase, editStarted time.Time) []nativeBuildPhaseWaterfall {
	result := make([]nativeBuildPhaseWaterfall, 0, len(phases))
	for _, phase := range phases {
		started, err := time.Parse(time.RFC3339Nano, phase.StartedAt)
		if err != nil {
			continue
		}
		ended := started.Add(time.Duration(phase.DurationMS * float64(time.Millisecond)))
		result = append(result, nativeBuildPhaseWaterfall{
			Name: phase.Name, StartedAt: started.UTC().Format(time.RFC3339Nano), EndedAt: ended.UTC().Format(time.RFC3339Nano),
			StartFromEditMS: nativeReloadMS(started.Sub(editStarted)), EndFromEditMS: nativeReloadMS(ended.Sub(editStarted)), DurationMS: phase.DurationMS,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt < result[j].StartedAt })
	return result
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
					Status           string                             `json:"status"`
					Backend          string                             `json:"backend"`
					Owner            string                             `json:"owner"`
					RequestSequence  uint64                             `json:"request_sequence"`
					ArtifactDigest   string                             `json:"artifact_digest"`
					CaptureMS        float64                            `json:"capture_ms"`
					PackageLoadingMS float64                            `json:"package_loading_ms"`
					DirectoryMS      float64                            `json:"directory_validation_ms"`
					InputHashMS      float64                            `json:"input_hash_ms"`
					SnapshotMS       float64                            `json:"snapshot_ms"`
					ArtifactBuildMS  float64                            `json:"artifact_build_ms"`
					ArchiveMS        float64                            `json:"archive_validation_ms"`
					SupportMS        float64                            `json:"support_validation_ms"`
					CompileMS        float64                            `json:"compile_ms"`
					LinkMS           float64                            `json:"link_ms"`
					FinalizationMS   float64                            `json:"finalization_ms"`
					TransactionMS    float64                            `json:"transaction_ms"`
					ToolInvocations  int                                `json:"tool_invocations"`
					RebuiltPackages  []string                           `json:"rebuilt_packages"`
					Phases           map[string]nativeBuildBackendPhase `json:"phases"`
					Capture          struct {
						DurationMS            float64 `json:"duration_ms"`
						PackageLoadingMS      float64 `json:"package_loading_ms"`
						DirectoryValidationMS float64 `json:"directory_validation_ms"`
						InputHashMS           float64 `json:"input_hash_ms"`
						SnapshotMS            float64 `json:"snapshot_ms"`
					} `json:"capture"`
				}
				if err := json.Unmarshal(data, &raw); err != nil {
					return nativeBuildBackendResult{}, generation, err
				}
				if raw.CaptureMS == 0 {
					raw.CaptureMS = raw.Capture.DurationMS
				}
				if raw.DirectoryMS == 0 {
					raw.DirectoryMS = raw.Capture.DirectoryValidationMS
				}
				if raw.InputHashMS == 0 {
					raw.InputHashMS = raw.Capture.InputHashMS
				}
				if raw.SnapshotMS == 0 {
					raw.SnapshotMS = raw.Capture.SnapshotMS
				}
				if raw.PackageLoadingMS == 0 {
					raw.PackageLoadingMS = raw.Capture.PackageLoadingMS
				}
				return nativeBuildBackendResult{Status: raw.Status, Backend: raw.Backend, Owner: raw.Owner, ArtifactDigest: raw.ArtifactDigest, RequestSequence: raw.RequestSequence, CaptureMS: raw.CaptureMS, PackageLoadingMS: raw.PackageLoadingMS, DirectoryValidationMS: raw.DirectoryMS, InputHashMS: raw.InputHashMS, SnapshotMS: raw.SnapshotMS, ArchiveValidationMS: raw.ArchiveMS, SupportValidationMS: raw.SupportMS, ArtifactBuildMS: raw.ArtifactBuildMS, CompileMS: raw.CompileMS, LinkMS: raw.LinkMS, FinalizationMS: raw.FinalizationMS, TransactionMS: raw.TransactionMS, ToolInvocations: raw.ToolInvocations, RebuiltPackages: raw.RebuiltPackages, StateCommit: raw.Phases["state_commit"]}, generation, nil
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

// nativeBuildLoadAverage records host load around the run. Link and compile
// phases slow down under contention, so comparisons need this context.
func (run *nativeBuildDriverRun) nativeBuildLoadAverage(name string) string {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	data, err := run.command(run.repoRoot, name, "sysctl", "-n", "vm.loadavg")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// nativeBuildSchedulingPolicies counts the Darwin scheduling state each
// measured request reported. A background policy moves compiler and linker
// work to throttled scheduling, which invalidates executor comparisons.
func nativeBuildSchedulingPolicies(samples []nativeBuildDriverSample) map[string]int {
	counts := map[string]int{}
	for _, sample := range samples {
		policy := sample.SchedulingPolicy
		if policy == "" {
			policy = "not_reported"
		}
		counts[policy]++
	}
	return counts
}
