package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
)

const harnessNativeReloadPluginName = "Go plugin native reload feasibility experiment"

type nativeReloadPluginBenchmark struct {
	common         *nativeReloadBenchmark
	host           *pluginReloadHostChild
	hostIdentity   pluginReloadHostIdentity
	pluginRoot     string
	initialHostRSS int64
	loaded         int
	paths          map[string]bool
	pluginTemplate []byte
	summary        map[string]any
}

func runHarnessNativeReloadPluginStep(ctx context.Context, repoRoot, workloadRoot string, write bool) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessNativeReloadPluginName,
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--benchmark", "native-reload-plugin", "--workload-root", workloadRoot, "--summary", "--write"}}
	var err error
	step.Summary, err = runNativeReloadPluginBenchmark(ctx, repoRoot, workloadRoot, write)
	step.OK, step.DurationMS = err == nil, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func runNativeReloadPluginBenchmark(parent context.Context, repoRoot, sourceRoot string, write bool) (summary map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	summary = map[string]any{
		"benchmark": "native-reload-plugin", "decision": "incomplete", "feasibility_passed": false,
		"workload_commit": nativeReloadONLVCommit, "warmup_count": 2, "early_sample_count": 5,
		"gate":            map[string]any{"build_p50_ms": 200, "plugin_open_activate_p50_ms": 50, "native_replacement_p50_ms": 250, "continue_to_30_max_p50_ms": 325},
		"scope":           "real ONLV AHJ typed validation path; stable experimental host and unique Go plugins; no public endpoint or production-equivalence claim",
		"cache_state":     "existing stock Go package/module caches retained; no cache clearing; run-unique behavior markers and plugin paths",
		"quantile_method": "nearest-rank; five samples are early feasibility evidence",
		"hardware":        map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU()},
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		return summary, fmt.Errorf("go plugin experiment is unsupported on %s", runtime.GOOS)
	}
	common := &nativeReloadBenchmark{ctx: ctx, repoRoot: repoRoot, summary: summary}
	bench := &nativeReloadPluginBenchmark{common: common, paths: map[string]bool{}, summary: summary}
	var err error
	common.sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		return summary, err
	}
	common.sourceRoot, err = filepath.EvalSymlinks(common.sourceRoot)
	if err != nil {
		return summary, err
	}
	common.root, err = os.MkdirTemp("", "scn-native-reload-plugin-")
	if err != nil {
		return summary, err
	}
	common.root, err = filepath.EvalSymlinks(common.root)
	if err != nil {
		return summary, err
	}
	common.appRoot = filepath.Join(common.root, "onlv")
	common.evidence = filepath.Join(common.root, "evidence")
	if write {
		parent := filepath.Join(repoRoot, ".scenery/harness/minimal-native-reload-plugin")
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return summary, err
		}
		common.evidence, err = os.MkdirTemp(parent, "attested-")
		if err != nil {
			return summary, err
		}
	} else if err := os.MkdirAll(common.evidence, 0o700); err != nil {
		return summary, err
	}
	summary["evidence_root"], summary["owned_root"], summary["workload_root"] = common.evidence, common.root, common.sourceRoot
	common.env = envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "DATABASE_URL", "SCENERY_DEV_CACHE_DIR"),
		"GOWORK=off", "SCENERY_DEV_CACHE_DIR="+filepath.Join(common.root, "cache"), "SCENERY_AGENT_HOME="+filepath.Join(common.root, "agent"))
	addedWorktree := false
	defer func() {
		stopped := true
		if bench.host != nil {
			if err := bench.host.close(); err != nil {
				resultErr = errors.Join(resultErr, err)
				stopped = false
			}
		}
		summary["all_children_stopped"] = stopped
		if addedWorktree {
			digest, err := nativeReloadFileDigest(filepath.Join(common.root, "owner.json"))
			if err != nil || common.ownerDigest == "" || digest != common.ownerDigest {
				resultErr = errors.Join(resultErr, fmt.Errorf("owned root marker changed; cleanup refused: %v", err))
				stopped = false
			}
		}
		if stopped && addedWorktree {
			cleanup, cancelCleanup := context.WithTimeout(context.Background(), 20*time.Second)
			_, record, err := nativeReloadCommand(cleanup, common.sourceRoot, common.env, "", "git", "worktree", "remove", "--force", common.appRoot)
			cancelCleanup()
			common.commands = append(common.commands, record)
			resultErr = errors.Join(resultErr, err)
			summary["owned_worktree_removed"] = err == nil
		}
		summary["commands"] = common.commands
		if resultErr != nil {
			summary["error"] = resultErr.Error()
			summary["decision"] = "invalid_evidence"
		}
		if write {
			resultErr = errors.Join(resultErr, nativeReloadWriteJSON(filepath.Join(common.evidence, "report.json"), summary))
		}
		if resultErr == nil && stopped {
			resultErr = os.RemoveAll(common.root)
		}
	}()
	if _, err := common.command(common.sourceRoot, "source-status-before", "git", "status", "--porcelain=v1"); err != nil {
		return summary, err
	}
	if _, err := common.command(common.sourceRoot, "create-worktree", "git", "worktree", "add", "--quiet", "--detach", common.appRoot, nativeReloadONLVCommit); err != nil {
		return summary, err
	}
	addedWorktree = true
	var session [16]byte
	if _, err := rand.Read(session[:]); err != nil {
		return summary, err
	}
	common.base.Session = hex.EncodeToString(session[:])
	if err := nativeReloadWriteJSON(filepath.Join(common.root, "owner.json"), map[string]any{"kind": "native-reload-plugin-experiment", "pid": os.Getpid(), "session": common.base.Session, "source": common.sourceRoot, "commit": nativeReloadONLVCommit, "created": time.Now().UTC()}); err != nil {
		return summary, err
	}
	common.ownerDigest, err = nativeReloadFileDigest(filepath.Join(common.root, "owner.json"))
	if err != nil {
		return summary, err
	}
	summary["owner_marker_digest"] = common.ownerDigest
	if err := common.prepare(); err != nil {
		return summary, err
	}
	if err := bench.prepare(); err != nil {
		return summary, err
	}
	if err := bench.measure(); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(common.appRoot, "solar/ahjs/service.go"), common.original, 0o600); err != nil {
		return summary, err
	}
	for _, check := range []struct {
		name, program string
		args          []string
	}{
		{"onlv-check", common.selection.Executable, []string{"check", "-o", "json"}},
		{"onlv-ahj-tests", "go", []string{"test", "./solar/ahjs/..."}},
		{"onlv-harness", common.selection.Executable, []string{"harness", "-o", "json", "--write"}},
	} {
		if _, err := common.command(common.appRoot, check.name, check.program, check.args...); err != nil {
			return summary, err
		}
	}
	if err := build.VerifyFrameworkSelection(ctx, common.selection); err != nil {
		return summary, err
	}
	current, err := build.FrameworkSourceManifest(repoRoot)
	if err != nil || current.Digest != common.selection.Source.Digest {
		return summary, fmt.Errorf("framework source changed during experiment: %v", err)
	}
	after, err := common.command(common.sourceRoot, "source-status-after", "git", "status", "--porcelain=v1")
	if err != nil {
		return summary, err
	}
	before, err := os.ReadFile(filepath.Join(common.evidence, "source-status-before.log"))
	if err != nil || string(after) != string(before) {
		return summary, fmt.Errorf("original ONLV checkout status changed during experiment: %v", err)
	}
	summary["original_checkout_status_unchanged"] = true
	return summary, nil
}

func (b *nativeReloadPluginBenchmark) prepare() error {
	b.pluginRoot = filepath.Join(b.common.root, "plugins")
	if err := os.MkdirAll(b.pluginRoot, 0o700); err != nil {
		return err
	}
	var protocolRevision string
	for _, item := range []struct{ source, target string }{
		{"harness_native_reload_plugin_protocol.go", "scenery_plugin_host/protocol.go"},
		{"testdata/native-reload-plugin/host.go.txt", "scenery_plugin_host/main.go"},
		{"testdata/native-reload-plugin/plugin.go.txt", "scenery_implementation_plugin/main.go"},
	} {
		data, err := os.ReadFile(filepath.Join(b.common.repoRoot, "scripts/verify", item.source))
		if err != nil {
			return err
		}
		if strings.HasSuffix(item.target, "protocol.go") {
			protocolRevision = pluginReloadDigest(data)
		}
		if strings.HasSuffix(item.source, "plugin.go.txt") {
			b.pluginTemplate = data
		}
		path := filepath.Join(b.common.appRoot, item.target)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(b.common.evidence, strings.ReplaceAll(item.target, "/", "-")), data, 0o600); err != nil {
			return err
		}
	}
	data, err := b.common.command(b.common.appRoot, "plugin-host-deps", "go", "list", "-mod=readonly", "-deps", "-json", "./scenery_plugin_host")
	if err != nil {
		return err
	}
	inputs, err := nativeReloadCapture(data, b.common.goEnvironment, b.common.appRoot)
	if err != nil {
		return err
	}
	if err := nativeReloadWriteJSON(filepath.Join(b.common.evidence, "plugin-host-inputs.json"), inputs); err != nil {
		return err
	}
	base := pluginReloadHostBase{ABI: pluginReloadABI, ProtocolRevision: protocolRevision,
		FrameworkSource: b.common.selection.Source.Digest, FrameworkExecutable: b.common.selection.ExecutableDigest,
		ContractABI: b.common.base.ContractABI, HostBuildInputs: inputs.Digest, ArtifactRoot: b.pluginRoot,
		Worktree: b.common.appRoot, Session: b.common.base.Session, Toolchain: b.common.base.Toolchain, Target: b.common.base.Target}
	record, err := json.Marshal(base)
	if err != nil {
		return err
	}
	hostPath := filepath.Join(b.common.root, "plugin-host")
	started := time.Now()
	args := []string{"build", "-mod=readonly", "-ldflags", "-X=main.pluginReloadHostRecord=" + base64.RawStdEncoding.EncodeToString(record), "-o", hostPath, "./scenery_plugin_host"}
	_, command, err := nativeReloadCommand(b.common.ctx, b.common.appRoot, b.common.env, filepath.Join(b.common.evidence, "plugin-host-build.log"), "go", args...)
	b.common.commands = append(b.common.commands, command)
	if err != nil {
		return err
	}
	digest, err := pluginReloadFileDigest(hostPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(hostPath, 0o500); err != nil {
		return err
	}
	b.hostIdentity = pluginReloadHostIdentity{Base: base, ExecutableDigest: digest}
	start := time.Now()
	output := &nativeReloadOutput{limit: 64 << 10}
	var ready pluginReloadFrame
	b.host, ready, err = startPluginReloadHost(b.common.ctx, b.common.appRoot, hostPath, b.common.env, b.hostIdentity, output)
	if err != nil {
		return errors.Join(err, nativeReloadWriteJSON(filepath.Join(b.common.evidence, "plugin-host-start-error.json"), map[string]any{"ready": ready, "stderr": output.String()}))
	}
	b.initialHostRSS, err = b.hostRSS()
	if err != nil {
		return err
	}
	b.summary["stable_host"] = map[string]any{"identity": b.hostIdentity, "path": hostPath, "package_count": len(inputs.Packages), "artifact_bytes": info.Size(),
		"build_ms": command.DurationMS, "prepare_and_build_ms": nativeReloadMS(time.Since(started)), "launch_attest_ms": nativeReloadMS(time.Since(start)), "pid": b.host.process.PID,
		"initial_rss_bytes": b.initialHostRSS, "ready": ready}
	return nil
}
