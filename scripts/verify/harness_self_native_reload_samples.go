package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/atomicfile"
)

type nativeReloadSample struct {
	Label               string                   `json:"label"`
	Behavior            string                   `json:"behavior"`
	Binary              string                   `json:"binary"`
	Identity            nativeReloadIdentity     `json:"identity"`
	ExecutableBytes     int64                    `json:"executable_bytes"`
	PackageCount        int                      `json:"package_count"`
	EditCompletedAt     string                   `json:"edit_completed_at"`
	ResponseCompletedAt string                   `json:"response_completed_at,omitempty"`
	InputCaptureMS      float64                  `json:"input_capture_ms"`
	BuildMS             float64                  `json:"build_ms"`
	ToolActions         []nativeReloadToolAction `json:"tool_actions"`
	ParentDigestMS      float64                  `json:"parent_digest_ms"`
	LaunchAttestMS      float64                  `json:"launch_attest_ms"`
	ActivationMS        float64                  `json:"activation_ms"`
	FirstInvocationMS   float64                  `json:"first_invocation_ms"`
	NativeReplacementMS float64                  `json:"native_replacement_ms"`
	EditToResponseMS    float64                  `json:"edit_to_response_ms"`
	Attestation         nativeReloadFrame        `json:"attestation"`
	Ready               nativeReloadFrame        `json:"ready"`
	Response            nativeReloadFrame        `json:"response"`
	SteadyCalls         map[string]any           `json:"steady_calls,omitempty"`
	Assertions          []string                 `json:"assertions,omitempty"`
	InputsUnchanged     bool                     `json:"inputs_unchanged"`
	Error               string                   `json:"error,omitempty"`
}

func (b *nativeReloadBenchmark) start(binary string, want nativeReloadIdentity) (*nativeReloadChild, nativeReloadFrame, error) {
	child, hello, err := startNativeReloadChild(b.ctx, b.appRoot, binary, b.env, want, nil)
	if child != nil {
		b.children = append(b.children, child)
	}
	return child, hello, err
}

func (b *nativeReloadBenchmark) measure() error {
	var warmups, samples []nativeReloadSample
	for i := 1; i <= 2; i++ {
		sample, err := b.sample(fmt.Sprintf("warmup-%02d", i), i == 1, false)
		warmups = append(warmups, sample)
		b.summary["warmups"] = warmups
		if err != nil {
			return err
		}
	}
	for i := 1; i <= 30; i++ {
		sample, err := b.sample(fmt.Sprintf("edit-%02d", i), false, false)
		samples = append(samples, sample)
		b.summary["samples"] = samples
		if err != nil {
			return err
		}
		if i == 5 && (sample.PackageCount > 310 || nativeReloadSampleStats(samples, func(s nativeReloadSample) float64 { return s.NativeReplacementMS })["p50_ms"].(float64) > 325) {
			b.summary["series_stopped_reason"] = "predeclared first-five closure/325ms continuation gate failed"
			break
		}
	}
	buildStats := nativeReloadSampleStats(samples, func(s nativeReloadSample) float64 { return s.BuildMS })
	launchStats := nativeReloadSampleStats(samples, func(s nativeReloadSample) float64 { return s.LaunchAttestMS + s.ActivationMS })
	nativeStats := nativeReloadSampleStats(samples, func(s nativeReloadSample) float64 { return s.NativeReplacementMS })
	b.summary["build"], b.summary["launch_attest_ready"], b.summary["native_replacement"] = buildStats, launchStats, nativeStats
	b.summary["edit_to_typed_response"] = nativeReloadSampleStats(samples, func(s nativeReloadSample) float64 { return s.EditToResponseMS })
	passed := len(samples) == 30 && buildStats["p50_ms"].(float64) <= 200 && launchStats["p50_ms"].(float64) <= 100 && nativeStats["p50_ms"].(float64) <= 250
	b.summary["feasibility_passed"] = passed
	if passed {
		b.summary["decision"] = "go_checkpoint_only"
	} else {
		b.summary["decision"] = "no_go"
	}
	last := samples[len(samples)-1]
	if err := b.negativeLifecycle(samples[0], last); err != nil {
		return err
	}
	diagnostic, err := b.sample("diagnostic-actions", false, true)
	b.summary["action_diagnostic"] = diagnostic
	if err != nil {
		return err
	}
	return b.initializationDiagnostic(last)
}

func (b *nativeReloadBenchmark) initializationDiagnostic(sample nativeReloadSample) (resultErr error) {
	output := &nativeReloadOutput{limit: 64 << 10}
	child, hello, err := startNativeReloadChild(b.ctx, b.appRoot, sample.Binary, envWithOverrides(b.env, "GODEBUG=inittrace=1"), sample.Identity, output)
	if child != nil {
		b.children = append(b.children, child)
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, child.close()) }()
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()
	ready, err := child.call(ctx, nativeReloadFrame{Kind: "activate", Identity: sample.Identity})
	if err != nil || ready.Kind != "ready" {
		return fmt.Errorf("init diagnostic activation failed: %v", err)
	}
	rss, err := b.command(b.appRoot, "initialized-rss", "ps", "-o", "rss=", "-p", strconv.Itoa(child.process.PID))
	if err != nil {
		return err
	}
	if err := child.close(); err != nil {
		return err
	}
	if output.truncated {
		return fmt.Errorf("initialization trace exceeded 64 KiB")
	}
	trace := filepath.Join(b.evidence, "inittrace.log")
	if err := atomicfile.Write(trace, output.Bytes(), 0o600, atomicfile.Options{}); err != nil {
		return err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(rss)), 10, 64)
	if err != nil {
		return err
	}
	b.summary["initialization_diagnostic"] = map[string]any{"trace": trace, "rss_bytes": value * 1024, "constructor_ms": ready.ConstructorMS, "self_hash_ms": hello.SelfHashMS, "timing_sample": false}
	return nil
}

func nativeReloadSampleStats(samples []nativeReloadSample, field func(nativeReloadSample) float64) map[string]any {
	values := make([]float64, len(samples))
	for i, sample := range samples {
		values[i] = field(sample)
	}
	return nativeReloadStats(values)
}

func (b *nativeReloadBenchmark) sample(label string, negative, traceActions bool) (sample nativeReloadSample, resultErr error) {
	sample.Label, sample.Behavior = label, b.base.Session+"-"+label
	defer func() {
		if resultErr != nil {
			sample.Error = resultErr.Error()
		}
		resultErr = errors.Join(resultErr, nativeReloadWriteJSON(filepath.Join(b.evidence, label+".json"), sample))
	}()
	changed, err := nativeReloadEditedSource(b.original, sample.Behavior)
	if err != nil {
		return sample, err
	}
	if err := os.WriteFile(filepath.Join(b.appRoot, "solar/ahjs/service.go"), changed, 0o600); err != nil {
		return sample, err
	}
	editCompleted := time.Now()
	sample.EditCompletedAt = editCompleted.UTC().Format(time.RFC3339Nano)
	data, err := b.command(b.appRoot, label+"-deps", "go", "list", "-mod=readonly", "-deps", "-json", "./scenery_implementation_island")
	if err != nil {
		return sample, err
	}
	inputs, err := nativeReloadCapture(data, b.goEnvironment, b.appRoot)
	if err != nil {
		return sample, err
	}
	if err := nativeReloadWriteJSON(filepath.Join(b.evidence, label+"-inputs.json"), inputs); err != nil {
		return sample, err
	}
	sample.InputCaptureMS, sample.PackageCount = nativeReloadMS(time.Since(editCompleted)), len(inputs.Packages)
	sample.Identity = b.base
	sample.Identity.BuildInputs, sample.Identity.Implementation = inputs.Digest, nativeReloadDigest(changed)
	record, err := json.Marshal(sample.Identity)
	if err != nil {
		return sample, err
	}
	sample.Identity.ExecutionGeneration = nativeReloadDigest(record)
	record, err = json.Marshal(sample.Identity)
	if err != nil {
		return sample, err
	}
	sample.Binary = filepath.Join(b.root, label)
	buildStarted := time.Now()
	actionGraph := filepath.Join(b.evidence, label+"-actions.json")
	args := []string{"build", "-mod=readonly", "-ldflags", "-X=main.nativeReloadBuildRecord=" + base64.RawStdEncoding.EncodeToString(record), "-o", sample.Binary}
	if traceActions {
		args = append(args, "-debug-actiongraph="+actionGraph)
	}
	args = append(args, "./scenery_implementation_island")
	_, command, err := nativeReloadCommand(b.ctx, b.appRoot, b.env, filepath.Join(b.evidence, label+"-build.log"), "go", args...)
	b.commands = append(b.commands, command)
	sample.BuildMS = command.DurationMS
	if err != nil {
		return sample, err
	}
	hashStarted := time.Now()
	sample.Identity.ExecutableDigest, err = nativeReloadFileDigest(sample.Binary)
	if err != nil {
		return sample, err
	}
	info, err := os.Stat(sample.Binary)
	if err != nil {
		return sample, err
	}
	sample.ExecutableBytes = info.Size()
	if err := os.Chmod(sample.Binary, 0o500); err != nil {
		return sample, err
	}
	sample.ParentDigestMS = nativeReloadMS(time.Since(hashStarted))
	launchStarted := time.Now()
	child, hello, err := b.start(sample.Binary, sample.Identity)
	sample.Attestation, sample.LaunchAttestMS = hello, nativeReloadMS(time.Since(launchStarted))
	if err != nil {
		return sample, err
	}
	defer func() { resultErr = errors.Join(resultErr, child.close()) }()
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()
	if negative {
		sample.Assertions, err = nativeReloadProveRejections(ctx, child)
		if err != nil {
			return sample, err
		}
	}
	activationStarted := time.Now()
	sample.Ready, err = child.call(ctx, nativeReloadFrame{Kind: "activate", Identity: sample.Identity})
	sample.ActivationMS = nativeReloadMS(time.Since(activationStarted))
	if err != nil || sample.Ready.Kind != "ready" || !sample.Ready.Constructed || sample.Ready.Failure != "" {
		return sample, fmt.Errorf("activation did not construct a ready service: %v", err)
	}
	input, err := json.Marshal(map[string]string{"query": strings.Repeat("q", 512)})
	if err != nil {
		return sample, err
	}
	request := nativeReloadFrame{Kind: "invoke", Identity: sample.Identity, Operation: nativeReloadOperation, Binding: nativeReloadBinding, Input: input}
	invokeStarted := time.Now()
	sample.Response, err = child.call(ctx, request)
	responseCompleted := time.Now()
	sample.FirstInvocationMS = nativeReloadMS(responseCompleted.Sub(invokeStarted))
	sample.NativeReplacementMS, sample.EditToResponseMS = nativeReloadMS(responseCompleted.Sub(buildStarted)), nativeReloadMS(responseCompleted.Sub(editCompleted))
	sample.ResponseCompletedAt = responseCompleted.UTC().Format(time.RFC3339Nano)
	if err != nil {
		return sample, err
	}
	expected := "query must be at most 200 characters [" + sample.Behavior + "]"
	if err := nativeReloadCheckBehavior(sample.Response, expected); err != nil {
		return sample, err
	}
	if traceActions {
		// This separate unique-edit diagnostic is not in the decision series.
		// Action intervals may overlap; never add them as sequential latency.
		actions, err := os.ReadFile(actionGraph)
		if err != nil {
			return sample, err
		}
		sample.ToolActions, err = nativeReloadToolActions(actions)
		if err != nil {
			return sample, err
		}
	}
	sample.Assertions = append(sample.Assertions, "self_digest_and_linked_generation", "exact_supervised_pid", "constructor_before_ready", "unique_compiled_behavior")
	var pings, invocations []float64
	for i := 0; i < 20; i++ {
		started := time.Now()
		pong, err := child.call(ctx, nativeReloadFrame{Kind: "ping", Identity: sample.Identity})
		pings = append(pings, nativeReloadMS(time.Since(started)))
		if err != nil || pong.Kind != "pong" {
			return sample, fmt.Errorf("transport ping failed: %v", err)
		}
		started = time.Now()
		response, err := child.call(ctx, request)
		invocations = append(invocations, nativeReloadMS(time.Since(started)))
		if err != nil {
			return sample, err
		}
		if err := nativeReloadCheckBehavior(response, expected); err != nil {
			return sample, err
		}
	}
	sample.SteadyCalls = map[string]any{"transport_ping": nativeReloadStats(pings), "typed_invocation": nativeReloadStats(invocations), "ping_samples_ms": pings, "typed_samples_ms": invocations}
	if err := child.close(); err != nil {
		return sample, err
	}
	postData, err := b.command(b.appRoot, label+"-deps-after", "go", "list", "-mod=readonly", "-deps", "-json", "./scenery_implementation_island")
	if err != nil {
		return sample, err
	}
	postInputs, err := nativeReloadCapture(postData, b.goEnvironment, b.appRoot)
	if err != nil {
		return sample, err
	}
	if postInputs.Digest != inputs.Digest {
		return sample, fmt.Errorf("source membership or bytes changed during build/execution")
	}
	sample.InputsUnchanged = true
	return sample, nil
}

func (b *nativeReloadBenchmark) negativeLifecycle(old, latest nativeReloadSample) error {
	proof := map[string]any{}
	b.summary["negative_lifecycle"] = proof
	child, observed, err := b.start(old.Binary, latest.Identity)
	if err == nil {
		return fmt.Errorf("predecessor was accepted as the latest artifact")
	}
	if child == nil {
		return fmt.Errorf("predecessor rejection did not reach the actual child: %w", err)
	}
	if observed.Identity.ExecutionGeneration != old.Identity.ExecutionGeneration || observed.Identity.ExecutableDigest != old.Identity.ExecutableDigest {
		return fmt.Errorf("predecessor did not report its own linked generation and digest")
	}
	if err := child.close(); err != nil {
		return err
	}
	proof["predecessor_cannot_attest_as_candidate"] = observed
	child, _, err = b.start(latest.Binary, latest.Identity)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()
	response, err := child.call(ctx, nativeReloadFrame{Kind: "activate", Identity: latest.Identity, FailConstructor: true})
	if err != nil || response.Failure != "constructor_failed" || response.Constructed {
		return fmt.Errorf("failed constructor was accepted: %v", err)
	}
	select {
	case <-child.process.Done:
		if child.process.WaitError() == nil {
			return fmt.Errorf("failed constructor exited successfully")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	proof["constructor_failure_exits_without_ready"] = response
	child, hello, err := b.start(latest.Binary, latest.Identity)
	if err != nil {
		return err
	}
	child.cancel()
	select {
	case <-child.process.Done:
	case <-ctx.Done():
		return ctx.Err()
	}
	proof["canceled_before_activation_stopped"] = !hello.Constructed
	return nil
}
