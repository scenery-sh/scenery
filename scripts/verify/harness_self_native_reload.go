package main

import (
	"context"
	"crypto/rand"
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

const nativeReloadONLVCommit = "4f8126a3e3806b7100ab7efaca1b7dd06b894221"
const harnessNativeReloadName = "minimal native reload artifact experiment"

type nativeReloadBenchmark struct {
	ctx                                           context.Context
	repoRoot, sourceRoot, root, appRoot, evidence string
	env                                           []string
	selection                                     build.FrameworkSelection
	goEnvironment                                 json.RawMessage
	base                                          nativeReloadIdentity
	original                                      []byte
	commands                                      []nativeReloadCommandRecord
	children                                      []*nativeReloadChild
	ownerDigest                                   string
	summary                                       map[string]any
}

func runHarnessNativeReloadStep(ctx context.Context, repoRoot, workloadRoot string, write bool) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessNativeReloadName,
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--benchmark", "native-reload", "--workload-root", workloadRoot, "--summary", "--write"}}
	var err error
	step.Summary, err = runNativeReloadBenchmark(ctx, repoRoot, workloadRoot, write)
	step.OK, step.DurationMS = err == nil, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func runNativeReloadBenchmark(parent context.Context, repoRoot, sourceRoot string, write bool) (summary map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	bench := &nativeReloadBenchmark{ctx: ctx, repoRoot: repoRoot, summary: map[string]any{
		"benchmark": "native-reload", "decision": "incomplete", "feasibility_passed": false,
		"workload_commit": nativeReloadONLVCommit, "warmup_count": 2, "early_sample_count": 5,
		"gate":                map[string]any{"build_p50_ms": 200, "launch_attest_ready_p50_ms": 100, "native_replacement_p50_ms": 250, "continue_to_30_max_p50_ms": 325},
		"scope":               "real ONLV AHJ typed validation path; experimental pipes, no public endpoint or production-equivalence claim",
		"cache_state":         "existing stock Go package/module caches retained; no cache clearing; run-unique behavior markers",
		"native_inputs_limit": "go-list-reported Go/cgo/native/embed files and module inputs recorded; external system headers/libraries not independently snapshotted; no production cache reuse authorized",
		"quantile_method":     "nearest-rank; five samples are early feasibility evidence",
		"hardware":            map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU()},
	}}
	summary = bench.summary
	if runtime.GOOS == "windows" {
		return summary, fmt.Errorf("inherited extra descriptors require Unix; this environment is unperformed")
	}
	var err error
	bench.sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		return summary, err
	}
	bench.sourceRoot, err = filepath.EvalSymlinks(bench.sourceRoot)
	if err != nil {
		return summary, err
	}
	bench.root, err = os.MkdirTemp("", "scn-native-reload-")
	if err != nil {
		return summary, err
	}
	bench.root, err = filepath.EvalSymlinks(bench.root)
	if err != nil {
		return summary, err
	}
	bench.appRoot = filepath.Join(bench.root, "onlv")
	bench.evidence = filepath.Join(bench.root, "evidence")
	if write {
		parent := filepath.Join(repoRoot, ".scenery/harness/minimal-native-reload")
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return summary, err
		}
		bench.evidence, err = os.MkdirTemp(parent, "attested-")
		if err != nil {
			return summary, err
		}
	} else if err := os.MkdirAll(bench.evidence, 0o700); err != nil {
		return summary, err
	}
	summary["evidence_root"], summary["owned_root"], summary["workload_root"] = bench.evidence, bench.root, bench.sourceRoot
	bench.env = envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "DATABASE_URL", "SCENERY_DEV_CACHE_DIR"),
		"GOWORK=off", "SCENERY_DEV_CACHE_DIR="+filepath.Join(bench.root, "cache"), "SCENERY_AGENT_HOME="+filepath.Join(bench.root, "agent"))
	addedWorktree := false
	defer func() {
		stopped := true
		for _, child := range bench.children {
			if err := child.close(); err != nil {
				resultErr = errors.Join(resultErr, err)
				stopped = false
			}
		}
		summary["all_children_stopped"] = stopped
		if addedWorktree {
			digest, err := nativeReloadFileDigest(filepath.Join(bench.root, "owner.json"))
			if err != nil || bench.ownerDigest == "" || digest != bench.ownerDigest {
				resultErr = errors.Join(resultErr, fmt.Errorf("owned root marker changed; cleanup refused: %v", err))
				stopped = false
			}
		}
		if stopped && addedWorktree {
			cleanup, cancelCleanup := context.WithTimeout(context.Background(), 20*time.Second)
			_, record, err := nativeReloadCommand(cleanup, bench.sourceRoot, bench.env, "", "git", "worktree", "remove", "--force", bench.appRoot)
			cancelCleanup()
			bench.commands = append(bench.commands, record)
			resultErr = errors.Join(resultErr, err)
			summary["owned_worktree_removed"] = err == nil
		}
		summary["commands"] = bench.commands
		if resultErr != nil {
			summary["error"] = resultErr.Error()
			summary["decision"] = "invalid_evidence"
		}
		if write {
			resultErr = errors.Join(resultErr, nativeReloadWriteJSON(filepath.Join(bench.evidence, "report.json"), summary))
		}
		if resultErr == nil && stopped {
			resultErr = os.RemoveAll(bench.root)
		}
	}()
	if _, err := bench.command(bench.sourceRoot, "source-status-before", "git", "status", "--porcelain=v1"); err != nil {
		return summary, err
	}
	if _, err := bench.command(bench.sourceRoot, "create-worktree", "git", "worktree", "add", "--quiet", "--detach", bench.appRoot, nativeReloadONLVCommit); err != nil {
		return summary, err
	}
	addedWorktree = true
	var session [16]byte
	if _, err := rand.Read(session[:]); err != nil {
		return summary, err
	}
	bench.base.Session = hex.EncodeToString(session[:])
	if err := nativeReloadWriteJSON(filepath.Join(bench.root, "owner.json"), map[string]any{"kind": "native-reload-experiment", "pid": os.Getpid(), "session": bench.base.Session, "source": bench.sourceRoot, "commit": nativeReloadONLVCommit, "created": time.Now().UTC()}); err != nil {
		return summary, err
	}
	bench.ownerDigest, err = nativeReloadFileDigest(filepath.Join(bench.root, "owner.json"))
	if err != nil {
		return summary, err
	}
	summary["owner_marker_digest"] = bench.ownerDigest
	if err := bench.prepare(); err != nil {
		return summary, err
	}
	if err := bench.measure(); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(bench.appRoot, "solar/ahjs/service.go"), bench.original, 0o600); err != nil {
		return summary, err
	}
	for _, check := range []struct {
		name, program string
		args          []string
	}{
		{"onlv-check", bench.selection.Executable, []string{"check", "-o", "json"}},
		{"onlv-ahj-tests", "go", []string{"test", "./solar/ahjs/..."}},
		{"onlv-harness", bench.selection.Executable, []string{"harness", "-o", "json", "--write"}},
	} {
		if _, err := bench.command(bench.appRoot, check.name, check.program, check.args...); err != nil {
			return summary, err
		}
	}
	if err := build.VerifyFrameworkSelection(ctx, bench.selection); err != nil {
		return summary, err
	}
	current, err := build.FrameworkSourceManifest(repoRoot)
	if err != nil || current.Digest != bench.selection.Source.Digest {
		return summary, fmt.Errorf("framework source changed during experiment: %v", err)
	}
	after, err := bench.command(bench.sourceRoot, "source-status-after", "git", "status", "--porcelain=v1")
	if err != nil {
		return summary, err
	}
	before, err := os.ReadFile(filepath.Join(bench.evidence, "source-status-before.log"))
	if err != nil || string(after) != string(before) {
		return summary, fmt.Errorf("original ONLV checkout status changed during experiment: %v", err)
	}
	summary["original_checkout_status_unchanged"] = true
	return summary, nil
}

func (b *nativeReloadBenchmark) command(cwd, name, program string, args ...string) ([]byte, error) {
	data, record, err := nativeReloadCommand(b.ctx, cwd, b.env, filepath.Join(b.evidence, name+".log"), program, args...)
	b.commands = append(b.commands, record)
	return data, err
}

func (b *nativeReloadBenchmark) prepare() error {
	if _, err := b.command(b.appRoot, "framework-use", harnessLocalSceneryBinaryPath(b.repoRoot), "framework", "use", "--source", b.repoRoot, "-o", "json"); err != nil {
		return err
	}
	var err error
	b.selection, err = build.ReadFrameworkSelection(b.appRoot)
	if err != nil {
		return err
	}
	if err := build.VerifyFrameworkSelection(b.ctx, b.selection); err != nil {
		return err
	}
	b.summary["framework_selection"] = map[string]string{"source_root": b.selection.Source.Root, "source_digest": b.selection.Source.Digest, "executable": b.selection.Executable, "executable_digest": b.selection.ExecutableDigest}
	if err := nativeReloadWriteJSON(filepath.Join(b.evidence, "framework-selection.json"), b.selection); err != nil {
		return err
	}
	if _, err := b.command(b.appRoot, "generate-contracts", b.selection.Executable, "generate", "--target", "contracts", "-o", "json"); err != nil {
		return err
	}
	if _, err := b.command(b.appRoot, "initial-check", b.selection.Executable, "check", "-o", "json"); err != nil {
		return err
	}
	b.goEnvironment, err = b.command(b.appRoot, "go-environment", "go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOROOT", "GOFLAGS", "GOTOOLCHAIN", "GOWORK", "GOCACHE", "CGO_ENABLED", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS")
	if err != nil {
		return err
	}
	var goEnv map[string]string
	if err := json.Unmarshal(b.goEnvironment, &goEnv); err != nil {
		return err
	}
	b.summary["go_environment"] = goEnv
	for _, command := range [][]string{{"uptime"}, {"uname", "-a"}} {
		if data, err := b.command(b.appRoot, command[0], command[0], command[1:]...); err == nil {
			b.summary[command[0]] = strings.TrimSpace(string(data))
		}
	}
	if runtime.GOOS == "darwin" {
		if data, err := b.command(b.appRoot, "hardware", "sysctl", "-n", "machdep.cpu.brand_string", "hw.model", "hw.memsize"); err == nil {
			b.summary["hardware_detail"] = strings.TrimSpace(string(data))
		}
	}
	for source, target := range map[string]string{"harness_native_reload_protocol.go": "protocol.go", "testdata/native-reload/main.go.txt": "main.go"} {
		data, err := os.ReadFile(filepath.Join(b.repoRoot, "scripts/verify", source))
		if err != nil {
			return err
		}
		if target == "protocol.go" {
			b.base.ProtocolRevision = nativeReloadDigest(data)
		}
		path := filepath.Join(b.appRoot, "scenery_implementation_island", target)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(b.evidence, target), data, 0o600); err != nil {
			return err
		}
	}
	abi, err := nativeReloadContractABI(b.appRoot)
	if err != nil {
		return err
	}
	b.base = nativeReloadIdentity{ABI: nativeReloadABI, ProtocolRevision: b.base.ProtocolRevision, FrameworkSource: b.selection.Source.Digest, FrameworkExecutable: b.selection.ExecutableDigest,
		ContractABI: abi, Worktree: b.appRoot, Session: b.base.Session, Toolchain: goEnv["GOVERSION"], Target: goEnv["GOOS"] + "/" + goEnv["GOARCH"]}
	b.original, err = os.ReadFile(filepath.Join(b.appRoot, "solar/ahjs/service.go"))
	return err
}
