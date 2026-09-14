package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/atomicfile"
)

const harnessNativeAttributionName = "native build and first execution attribution"

type nativeAttributionExecution struct {
	StartCallMS        float64           `json:"start_call_ms"`
	LaunchAttestMS     float64           `json:"launch_attest_ms"`
	ActivationMS       float64           `json:"activation_ms"`
	FirstInvocationMS  float64           `json:"first_invocation_ms"`
	LaunchToResponseMS float64           `json:"launch_to_response_ms"`
	Attestation        nativeReloadFrame `json:"attestation"`
	Ready              nativeReloadFrame `json:"ready"`
	Response           nativeReloadFrame `json:"response"`
	InitTrace          string            `json:"init_trace,omitempty"`
	SameArtifact       bool              `json:"same_artifact"`
	Error              string            `json:"error,omitempty"`
}

type nativeAttributionPair struct {
	First                   nativeReloadSample         `json:"first"`
	Repeated                nativeAttributionExecution `json:"repeated"`
	Diagnostic              bool                       `json:"diagnostic"`
	FirstMinusRepeatReadyMS float64                    `json:"first_minus_repeat_ready_ms"`
}

func runHarnessNativeAttributionStep(ctx context.Context, repoRoot, workloadRoot string, write bool) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessNativeAttributionName,
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--benchmark", "native-reload-attribution", "--workload-root", workloadRoot, "--summary", "--write"}}
	var err error
	step.Summary, err = runNativeReloadExperiment(ctx, repoRoot, workloadRoot, write, nativeReloadExperiment{
		id: "native-reload-attribution", evidenceDirectory: "native-reload-attribution", timeout: 20 * time.Minute,
		measure: (*nativeReloadBenchmark).measureAttribution,
	})
	step.OK, step.DurationMS = err == nil, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func (b *nativeReloadBenchmark) measureAttribution() error {
	b.summary["early_sample_count"] = 0
	b.summary["measurement_count"] = 30
	b.summary["diagnostic_count"] = 5
	b.summary["quantile_method"] = "nearest-rank; primary first executions and repeated executions are separate distributions"
	b.summary["linux_cohort"] = "deferred by human; unmeasured"
	b.summary["decision"] = "insufficient_attribution"
	b.summary["attribution_scope"] = "parent monotonic boundaries plus trace-local Go spans; loader, OS scheduling and detailed cache internals remain unknown unless separately evidenced"
	b.summary["launcher_policy"] = map[string]any{"developer_tools": "unknown", "reason": "per-application policy requires read-only observation of the actual responsible launcher; not inferred from the shell"}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("current attribution execution is authorized for native macOS only")
	}
	if err := b.attributionHost(); err != nil {
		return err
	}
	var first, latest nativeReloadSample
	for _, series := range []struct {
		name       string
		count      int
		diagnostic bool
	}{{"warmups", 2, false}, {"samples", 30, false}, {"diagnostics", 5, true}} {
		pairs := make([]nativeAttributionPair, 0, series.count)
		for i := 1; i <= series.count; i++ {
			if err := b.ctx.Err(); err != nil {
				return err
			}
			label := fmt.Sprintf("%s-%02d", series.name, i)
			pair := nativeAttributionPair{Diagnostic: series.diagnostic}
			var err error
			pair.First, err = b.sampleWithOptions(label, series.name == "warmups" && i == 1,
				nativeReloadSampleOptions{actions: series.diagnostic, initialization: series.diagnostic, driverTrace: series.diagnostic, attribution: true})
			if err == nil {
				pair.Repeated, err = b.repeatAttribution(pair.First, series.diagnostic)
			}
			pair.FirstMinusRepeatReadyMS = pair.First.LaunchAttestMS + pair.First.ActivationMS - pair.Repeated.LaunchAttestMS - pair.Repeated.ActivationMS
			pairs = append(pairs, pair)
			b.summary[series.name] = pairs
			if writeErr := nativeReloadWriteJSON(filepath.Join(b.evidence, label+"-pair.json"), pair); writeErr != nil {
				err = errors.Join(err, writeErr)
			}
			if err != nil {
				return err
			}
			bytes, err := nativeAttributionEvidenceSize(b.evidence)
			if err != nil {
				return err
			}
			b.summary["evidence_bytes"] = bytes
			if series.name == "samples" {
				if i == 1 {
					first = pair.First
				}
				latest = pair.First
			}
		}
		b.summary[series.name+"_statistics"] = nativeAttributionPairStats(pairs)
	}
	if err := b.negativeLifecycle(first, latest); err != nil {
		return err
	}
	b.summary["macos_pair_series_complete"] = true
	return nil
}

func nativeAttributionPairStats(pairs []nativeAttributionPair) map[string]any {
	var build, firstReady, repeatReady, native, edit, difference []float64
	for _, pair := range pairs {
		if pair.First.Error != "" || pair.Repeated.Error != "" || !pair.Repeated.SameArtifact || !pair.First.InputsUnchanged {
			return map[string]any{"valid": false, "reason": "incomplete or invalid pair retained"}
		}
		build = append(build, pair.First.BuildMS)
		firstReady = append(firstReady, pair.First.LaunchAttestMS+pair.First.ActivationMS)
		repeatReady = append(repeatReady, pair.Repeated.LaunchAttestMS+pair.Repeated.ActivationMS)
		native = append(native, pair.First.NativeReplacementMS)
		edit = append(edit, pair.First.EditToResponseMS)
		difference = append(difference, pair.FirstMinusRepeatReadyMS)
	}
	return map[string]any{"valid": true, "build": nativeReloadStats(build), "first_launch_ready": nativeReloadStats(firstReady),
		"repeated_launch_ready": nativeReloadStats(repeatReady), "build_to_first_response": nativeReloadStats(native),
		"edit_to_first_response": nativeReloadStats(edit), "paired_first_minus_repeated_ready": nativeReloadStats(difference)}
}

func (b *nativeReloadBenchmark) repeatAttribution(sample nativeReloadSample, diagnostic bool) (result nativeAttributionExecution, resultErr error) {
	defer func() {
		if resultErr != nil {
			result.Error = resultErr.Error()
		}
	}()
	before, err := os.Stat(sample.Binary)
	if err != nil {
		return result, err
	}
	if sample.artifactInfo == nil || !os.SameFile(sample.artifactInfo, before) {
		return result, fmt.Errorf("repeat artifact differs from the first-execution inode")
	}
	digest, err := nativeReloadFileDigest(sample.Binary)
	if err != nil || digest != sample.Identity.ExecutableDigest {
		return result, fmt.Errorf("repeat artifact changed before launch: %v", err)
	}
	var output *nativeReloadOutput
	if diagnostic {
		output = &nativeReloadOutput{limit: 64 << 10}
	}
	started := time.Now()
	child, hello, err := b.startObserved(sample.Binary, sample.Identity, output)
	result.LaunchAttestMS, result.Attestation = nativeReloadMS(time.Since(started)), hello
	if child != nil {
		result.StartCallMS = child.startCallMS
		defer func() { resultErr = errors.Join(resultErr, child.close()) }()
	}
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()
	activated := time.Now()
	result.Ready, err = child.call(ctx, nativeReloadFrame{Kind: "activate", Identity: sample.Identity})
	result.ActivationMS = nativeReloadMS(time.Since(activated))
	if err != nil || result.Ready.Kind != "ready" || !result.Ready.Constructed || result.Ready.Failure != "" {
		return result, fmt.Errorf("repeat activation failed: %v", err)
	}
	input, err := json.Marshal(map[string]string{"query": strings.Repeat("q", 512)})
	if err != nil {
		return result, err
	}
	invoked := time.Now()
	result.Response, err = child.call(ctx, nativeReloadFrame{Kind: "invoke", Identity: sample.Identity, Operation: nativeReloadOperation, Binding: nativeReloadBinding, Input: input})
	if err != nil {
		return result, err
	}
	if err := nativeReloadCheckBehavior(result.Response, "query must be at most 200 characters ["+sample.Behavior+"]"); err != nil {
		return result, err
	}
	verified := time.Now()
	result.FirstInvocationMS, result.LaunchToResponseMS = nativeReloadMS(verified.Sub(invoked)), nativeReloadMS(verified.Sub(started))
	if err := child.close(); err != nil {
		return result, err
	}
	after, err := os.Stat(sample.Binary)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() {
		return result, fmt.Errorf("repeat artifact inode or metadata changed: %v", err)
	}
	digest, err = nativeReloadFileDigest(sample.Binary)
	if err != nil || digest != sample.Identity.ExecutableDigest {
		return result, fmt.Errorf("repeat artifact bytes changed: %v", err)
	}
	result.SameArtifact = true
	if output != nil {
		if output.truncated {
			return result, fmt.Errorf("repeat init trace exceeded 64 KiB")
		}
		result.InitTrace = filepath.Join(b.evidence, sample.Label+"-repeated-inittrace.log")
		if err := atomicfile.Write(result.InitTrace, output.Bytes(), 0o600, atomicfile.Options{}); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (b *nativeReloadBenchmark) attributionHost() error {
	for _, command := range []struct {
		name string
		args []string
	}{
		{"os-build", []string{"sw_vers"}},
		{"power-state", []string{"pmset", "-g", "batt"}},
		{"translation", []string{"sysctl", "-n", "sysctl.proc_translated"}},
		{"filesystem", []string{"df", "-h", b.root}},
	} {
		data, err := b.command(b.appRoot, command.name, command.args[0], command.args[1:]...)
		observation := map[string]any{"output": strings.TrimSpace(string(data)), "observed": err == nil}
		if err != nil {
			observation["error"] = err.Error()
		}
		b.summary[command.name] = observation
	}
	pid := os.Getpid()
	var ancestry []map[string]any
	seen := map[int]bool{}
	for pid > 0 && len(ancestry) < 32 && !seen[pid] {
		seen[pid] = true
		data, err := b.command(b.appRoot, fmt.Sprintf("launcher-%d", pid), "ps", "-p", fmt.Sprint(pid), "-o", "ppid=", "-o", "comm=")
		if err != nil {
			return err
		}
		var parent int
		if _, err := fmt.Sscan(string(data), &parent); err != nil {
			return err
		}
		ancestry = append(ancestry, map[string]any{"pid": pid, "observation": strings.TrimSpace(string(data))})
		pid = parent
	}
	b.summary["launcher_ancestry"] = ancestry
	b.attributionLauncherPolicy(ancestry)
	return nil
}
