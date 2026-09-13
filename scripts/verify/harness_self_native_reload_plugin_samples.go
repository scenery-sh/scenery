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
)

type nativeReloadPluginSample struct {
	Label               string               `json:"label"`
	Behavior            string               `json:"behavior"`
	Artifact            string               `json:"artifact"`
	Identity            pluginReloadIdentity `json:"identity"`
	ArtifactBytes       int64                `json:"artifact_bytes"`
	PackageCount        int                  `json:"package_count"`
	EditCompletedAt     string               `json:"edit_completed_at"`
	ResponseCompletedAt string               `json:"response_completed_at,omitempty"`
	InputCaptureMS      float64              `json:"input_capture_ms"`
	BuildMS             float64              `json:"build_ms"`
	ParentDigestMS      float64              `json:"parent_digest_ms"`
	LoadCallMS          float64              `json:"load_call_ms"`
	PluginOpenMS        float64              `json:"plugin_open_ms"`
	ActivationMS        float64              `json:"activation_ms"`
	FirstInvocationMS   float64              `json:"first_invocation_ms"`
	NativeReplacementMS float64              `json:"native_replacement_ms"`
	EditToResponseMS    float64              `json:"edit_to_response_ms"`
	HostRSSBytes        int64                `json:"host_rss_bytes"`
	RetainedPlugins     int                  `json:"retained_plugins"`
	Response            pluginReloadFrame    `json:"response"`
	SteadyCalls         map[string]any       `json:"steady_calls,omitempty"`
	Assertions          []string             `json:"assertions,omitempty"`
	InputsUnchanged     bool                 `json:"inputs_unchanged"`
	Error               string               `json:"error,omitempty"`
}

func (b *nativeReloadPluginBenchmark) measure() error {
	var warmups, samples []nativeReloadPluginSample
	for i := 1; i <= 2; i++ {
		sample, err := b.sample(fmt.Sprintf("warmup-%02d", i), i == 1)
		warmups = append(warmups, sample)
		b.summary["warmups"] = warmups
		if err != nil {
			return err
		}
	}
	for i := 1; i <= 30; i++ {
		sample, err := b.sample(fmt.Sprintf("edit-%02d", i), false)
		samples = append(samples, sample)
		b.summary["samples"] = samples
		if err != nil {
			return err
		}
		if i == 5 && (sample.PackageCount > 310 || nativeReloadPluginSampleStats(samples, func(s nativeReloadPluginSample) float64 { return s.NativeReplacementMS })["p50_ms"].(float64) > 325) {
			b.summary["series_stopped_reason"] = "predeclared first-five closure/325ms continuation gate failed"
			break
		}
	}
	buildStats := nativeReloadPluginSampleStats(samples, func(s nativeReloadPluginSample) float64 { return s.BuildMS })
	openStats := nativeReloadPluginSampleStats(samples, func(s nativeReloadPluginSample) float64 { return s.PluginOpenMS + s.ActivationMS })
	replacementStats := nativeReloadPluginSampleStats(samples, func(s nativeReloadPluginSample) float64 { return s.NativeReplacementMS })
	b.summary["build"], b.summary["plugin_open_activate"], b.summary["native_replacement"] = buildStats, openStats, replacementStats
	b.summary["edit_to_typed_response"] = nativeReloadPluginSampleStats(samples, func(s nativeReloadPluginSample) float64 { return s.EditToResponseMS })
	b.summary["plan_0181_process_comparison"] = map[string]any{
		"process_package_count": 238, "plugin_package_count": samples[0].PackageCount,
		"process_artifact_bytes": 8166370, "plugin_artifact_bytes": samples[0].ArtifactBytes,
		"process_build_p50_ms": 518.559, "plugin_build_p50_ms": buildStats["p50_ms"],
		"process_launch_attest_ready_p50_ms": 401.598, "plugin_open_activate_p50_ms": openStats["p50_ms"],
		"process_native_replacement_p50_ms": 934.457, "plugin_native_replacement_p50_ms": replacementStats["p50_ms"],
	}
	passed := len(samples) == 30 && buildStats["p50_ms"].(float64) <= 200 && openStats["p50_ms"].(float64) <= 50 && replacementStats["p50_ms"].(float64) <= 250
	b.summary["feasibility_passed"] = passed
	if passed {
		b.summary["decision"] = "plugin_checkpoint_only"
	} else {
		b.summary["decision"] = "no_go"
	}
	if len(samples) == 0 {
		return fmt.Errorf("plugin experiment produced no samples")
	}
	last := samples[len(samples)-1]
	if err := b.proveIncompatibleCommonPackage(last); err != nil {
		return err
	}
	finalRSS, err := b.hostRSS()
	if err != nil {
		return err
	}
	b.summary["retention"] = map[string]any{
		"plugins_loaded": b.loaded, "plugins_unloadable": false,
		"initial_host_rss_bytes": b.initialHostRSS, "final_host_rss_bytes": finalRSS,
		"rss_growth_bytes": finalRSS - b.initialHostRSS,
	}
	return nil
}

func nativeReloadPluginSampleStats(samples []nativeReloadPluginSample, field func(nativeReloadPluginSample) float64) map[string]any {
	values := make([]float64, len(samples))
	for i, sample := range samples {
		values[i] = field(sample)
	}
	return nativeReloadStats(values)
}

func (b *nativeReloadPluginBenchmark) preparePluginPackage(label string) (string, error) {
	if len(b.pluginTemplate) == 0 || strings.ContainsAny(label, "/\\.") {
		return "", fmt.Errorf("invalid plugin template or generation label")
	}
	name := "scenery_implementation_plugin_" + strings.ReplaceAll(label, "-", "_")
	path := filepath.Join(b.common.appRoot, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(path, "main.go"), b.pluginTemplate, 0o600); err != nil {
		return "", err
	}
	return "./" + name, nil
}

func (b *nativeReloadPluginBenchmark) sample(label string, negative bool) (sample nativeReloadPluginSample, resultErr error) {
	sample.Label, sample.Behavior = label, b.common.base.Session+"-plugin-"+label
	defer func() {
		if resultErr != nil {
			sample.Error = resultErr.Error()
		}
		resultErr = errors.Join(resultErr, nativeReloadWriteJSON(filepath.Join(b.common.evidence, label+".json"), sample))
	}()
	changed, err := nativeReloadEditedSource(b.common.original, sample.Behavior)
	if err != nil {
		return sample, err
	}
	if err := os.WriteFile(filepath.Join(b.common.appRoot, "solar/ahjs/service.go"), changed, 0o600); err != nil {
		return sample, err
	}
	editCompleted := time.Now()
	sample.EditCompletedAt = editCompleted.UTC().Format(time.RFC3339Nano)
	packagePath, err := b.preparePluginPackage(label)
	if err != nil {
		return sample, err
	}
	data, err := b.common.command(b.common.appRoot, label+"-deps", "go", "list", "-mod=readonly", "-deps", "-json", packagePath)
	if err != nil {
		return sample, err
	}
	inputs, err := nativeReloadCapture(data, b.common.goEnvironment, b.common.appRoot)
	if err != nil {
		return sample, err
	}
	if err := nativeReloadWriteJSON(filepath.Join(b.common.evidence, label+"-inputs.json"), inputs); err != nil {
		return sample, err
	}
	sample.InputCaptureMS, sample.PackageCount = nativeReloadMS(time.Since(editCompleted)), len(inputs.Packages)
	linked := pluginReloadLinkedIdentity{
		Host: b.hostIdentity, BuildInputs: inputs.Digest,
		Implementation: nativeReloadDigest(changed),
	}
	record, err := json.Marshal(linked)
	if err != nil {
		return sample, err
	}
	linked.ExecutionGeneration = nativeReloadDigest(record)
	record, err = json.Marshal(linked)
	if err != nil {
		return sample, err
	}
	sample.Artifact = filepath.Join(b.pluginRoot, label+".so")
	if b.paths[sample.Artifact] {
		return sample, fmt.Errorf("plugin artifact path reused: %s", sample.Artifact)
	}
	b.paths[sample.Artifact] = true
	buildStarted := time.Now()
	linkerFlags := "-X=main.SceneryNativeReloadIdentity=" + base64.RawStdEncoding.EncodeToString(record)
	args := []string{"build", "-buildmode=plugin", "-mod=readonly", "-ldflags", linkerFlags, "-o", sample.Artifact, packagePath}
	_, command, err := nativeReloadCommand(b.common.ctx, b.common.appRoot, b.common.env, filepath.Join(b.common.evidence, label+"-build.log"), "go", args...)
	b.common.commands = append(b.common.commands, command)
	sample.BuildMS = command.DurationMS
	if err != nil {
		return sample, err
	}
	digestStarted := time.Now()
	sample.Identity = pluginReloadIdentity{Linked: linked}
	sample.Identity.ArtifactDigest, err = pluginReloadFileDigest(sample.Artifact)
	if err != nil {
		return sample, err
	}
	info, err := os.Stat(sample.Artifact)
	if err != nil {
		return sample, err
	}
	sample.ArtifactBytes = info.Size()
	if err := os.Chmod(sample.Artifact, 0o400); err != nil {
		return sample, err
	}
	sample.ParentDigestMS = nativeReloadMS(time.Since(digestStarted))
	input, err := json.Marshal(map[string]string{"query": strings.Repeat("q", 512)})
	if err != nil {
		return sample, err
	}
	request := pluginReloadFrame{Kind: "load", Host: b.hostIdentity, Identity: sample.Identity, PluginPath: sample.Artifact,
		Operation: pluginReloadOperation, Binding: pluginReloadBinding, Input: input}
	ctx, cancel := context.WithTimeout(b.common.ctx, 10*time.Second)
	defer cancel()
	if negative {
		wrong := request
		wrong.Identity.Linked.Host.Base.Session += "-foreign"
		response, callErr := b.host.call(ctx, wrong)
		if callErr != nil || response.Failure != "identity_mismatch" || response.Constructed {
			return sample, fmt.Errorf("cross-session plugin was accepted: response=%+v err=%v", response, callErr)
		}
		wrong = request
		wrong.Identity.ArtifactDigest = nativeReloadDigest([]byte("wrong artifact"))
		response, callErr = b.host.call(ctx, wrong)
		if callErr != nil || response.Failure != "artifact_mismatch" || response.Constructed {
			return sample, fmt.Errorf("wrong plugin digest was accepted: response=%+v err=%v", response, callErr)
		}
		sample.Assertions = append(sample.Assertions, "cross_session_rejected_before_open", "artifact_digest_rejected_before_open")
	}
	loadStarted := time.Now()
	sample.Response, err = b.host.call(ctx, request)
	responseCompleted := time.Now()
	sample.LoadCallMS = nativeReloadMS(responseCompleted.Sub(loadStarted))
	sample.PluginOpenMS, sample.ActivationMS, sample.FirstInvocationMS = sample.Response.OpenMS, sample.Response.ActivationMS, sample.Response.InvocationMS
	sample.NativeReplacementMS, sample.EditToResponseMS = nativeReloadMS(responseCompleted.Sub(buildStarted)), nativeReloadMS(responseCompleted.Sub(editCompleted))
	sample.ResponseCompletedAt = responseCompleted.UTC().Format(time.RFC3339Nano)
	if err != nil {
		return sample, err
	}
	sample.HostRSSBytes, err = b.hostRSS()
	if err != nil {
		return sample, err
	}
	postData, err := b.common.command(b.common.appRoot, label+"-deps-after", "go", "list", "-mod=readonly", "-deps", "-json", packagePath)
	if err != nil {
		return sample, err
	}
	postInputs, err := nativeReloadCapture(postData, b.common.goEnvironment, b.common.appRoot)
	if err != nil {
		return sample, err
	}
	if postInputs.Digest != inputs.Digest {
		return sample, fmt.Errorf("plugin source membership or bytes changed during build/load")
	}
	sample.InputsUnchanged = true
	if sample.Response.Kind != "result" || sample.Response.Failure != "" || !sample.Response.Constructed {
		return sample, fmt.Errorf("plugin generation did not become active: %s: %s", sample.Response.Failure, sample.Response.Detail)
	}
	if err := pluginReloadCheckIdentity(b.hostIdentity, sample.Identity, sample.Response.Identity); err != nil {
		return sample, err
	}
	expected := "query must be at most 200 characters [" + sample.Behavior + "]"
	if err := nativeReloadCheckBehavior(nativeReloadFrame{Kind: "result", Constructed: true, Operation: pluginReloadOperation, Binding: pluginReloadBinding, Outcome: sample.Response.Outcome}, expected); err != nil {
		return sample, err
	}
	b.loaded++
	sample.RetainedPlugins = b.loaded
	var calls []float64
	for i := 0; i < 20; i++ {
		started := time.Now()
		response, callErr := b.host.call(ctx, pluginReloadFrame{Kind: "invoke", Host: b.hostIdentity, Identity: sample.Identity,
			Operation: pluginReloadOperation, Binding: pluginReloadBinding, Input: input})
		calls = append(calls, nativeReloadMS(time.Since(started)))
		if callErr != nil {
			return sample, callErr
		}
		if err := nativeReloadCheckBehavior(nativeReloadFrame{Kind: "result", Constructed: true, Operation: pluginReloadOperation, Binding: pluginReloadBinding, Outcome: response.Outcome}, expected); err != nil {
			return sample, err
		}
	}
	sample.SteadyCalls = map[string]any{"typed_invocation": nativeReloadStats(calls), "samples_ms": calls}
	sample.Assertions = append(sample.Assertions, "stable_host_pid", "linked_identity", "unique_plugin_path", "typed_new_behavior", "source_inputs_unchanged")
	return sample, nil
}

func (b *nativeReloadPluginBenchmark) hostRSS() (int64, error) {
	data, err := b.common.command(b.common.appRoot, fmt.Sprintf("host-rss-%d", len(b.paths)), "ps", "-o", "rss=", "-p", strconv.Itoa(b.host.process.PID))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return value * 1024, err
}

func (b *nativeReloadPluginBenchmark) proveIncompatibleCommonPackage(current nativeReloadPluginSample) error {
	path := filepath.Join(b.common.appRoot, "solar/ahjs/scenerycontract/contract.gen.go")
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	changed := strings.Replace(string(original), "const PackageIdentity = \"ahjs\"", "const PackageIdentity = \"ahjs-incompatible\"", 1)
	if changed == string(original) {
		return fmt.Errorf("contract mismatch fixture anchor is absent")
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.WriteFile(path, original, 0o600) }()
	packagePath, err := b.preparePluginPackage("incompatible_common")
	if err != nil {
		return err
	}
	artifact := filepath.Join(b.pluginRoot, "incompatible-common.so")
	record, err := json.Marshal(current.Identity.Linked)
	if err != nil {
		return err
	}
	linkerFlags := "-X=main.SceneryNativeReloadIdentity=" + base64.RawStdEncoding.EncodeToString(record)
	args := []string{"build", "-buildmode=plugin", "-mod=readonly", "-ldflags", linkerFlags, "-o", artifact, packagePath}
	_, command, err := nativeReloadCommand(b.common.ctx, b.common.appRoot, b.common.env, filepath.Join(b.common.evidence, "incompatible-build.log"), "go", args...)
	b.common.commands = append(b.common.commands, command)
	if err != nil {
		return err
	}
	digest, err := pluginReloadFileDigest(artifact)
	if err != nil {
		return err
	}
	identity := current.Identity
	identity.ArtifactDigest = digest
	ctx, cancel := context.WithTimeout(b.common.ctx, 10*time.Second)
	defer cancel()
	response, err := b.host.call(ctx, pluginReloadFrame{Kind: "load", Host: b.hostIdentity, Identity: identity, PluginPath: artifact,
		Operation: pluginReloadOperation, Binding: pluginReloadBinding, Input: json.RawMessage(`{"query":"` + strings.Repeat("q", 512) + `"}`)})
	if err != nil || response.Failure != "open_failed" {
		return fmt.Errorf("incompatible common package did not fail closed: response=%+v err=%v", response, err)
	}
	b.summary["incompatible_common_package"] = map[string]any{"rejected": true, "failure": response.Failure, "detail": response.Detail,
		"plugin_open_ms": response.OpenMS, "host_pid": b.host.process.PID, "active_generation_preserved": response.Constructed}
	return nil
}
