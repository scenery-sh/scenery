package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
	"scenery.sh/internal/envpolicy"
)

const harnessEdgeProcessProbeName = "edge process probes"

type harnessEdgeProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessEdgeProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessEdgeProcessProbeStepWithCheck(ctx, repoRoot, runHarnessEdgeProcessProbeCheck)
}

func runHarnessEdgeProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessEdgeProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessEdgeProcessProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the edge process boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessEdgeProcessProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{
			"proof":  "not_applicable_on_windows",
			"reason": "managed Caddy reload uses a Unix admin socket",
		}, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "scenery-edge-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)

	argsPath := filepath.Join(dir, "reload-args.txt")
	caddyPath := filepath.Join(dir, "caddy")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n"
	if err := os.WriteFile(caddyPath, []byte(script), 0o755); err != nil {
		return nil, nil, err
	}
	const configPath = "/tmp/Caddyfile.next"
	const adminSocket = "/tmp/caddy-admin.sock"
	if err := edgelifecycle.Reload(caddyPath, configPath, adminSocket); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		return nil, nil, err
	}
	wantArgs := []string{"reload", "--config", configPath, "--adapter", "caddyfile", "--address", "unix//" + adminSocket}
	want := strings.Join(wantArgs, "\n") + "\n"
	if string(data) != want {
		return nil, nil, fmt.Errorf("Caddy reload invocation = %q, want %q", string(data), want)
	}
	trust, err := runHarnessTrustLocalCAProbe(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	validation, err := runHarnessCaddyConfigValidation(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	startStop, err := runHarnessEdgeStartStopProbe(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"proof":             "managed_caddy_reload_trust_validation_start_stop_lock_and_fast_exit_verified",
		"args":              wantArgs,
		"local_ca_trust":    trust,
		"config_validation": validation,
		"start_stop":        startStop,
	}, nil, nil
}

func runHarnessEdgeStartStopProbe(ctx context.Context, root string) (map[string]any, error) {
	paths := localagent.PathsForHome(filepath.Join(root, "edge-home"))
	if err := localagent.EnsureDirs(paths); err != nil {
		return nil, err
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		return nil, err
	}
	caddyPath := filepath.Join(root, "edge-caddy")
	if err := os.WriteFile(caddyPath, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755); err != nil {
		return nil, err
	}
	adminSocket := filepath.Join(paths.RunDir, "caddy-admin.sock")
	config := edgelifecycle.StartConfig{
		Binary: caddyPath, Paths: paths, PublicAddr: "127.0.0.1:443",
		TargetAddr: "127.0.0.1:19443", HTTPTargetAddr: "127.0.0.1:19080",
		AdminSocket: adminSocket, UpstreamAddr: "127.0.0.1:9440", StartupSettle: 50 * time.Millisecond,
	}
	if err := edgelifecycle.Start(config); err != nil {
		return nil, err
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = edgelifecycle.Stop(paths, 2*time.Second)
		}
	}()
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return nil, err
	}
	target, err := localagent.LoadEdgeTargetState(paths.EdgeTargetPath)
	if err != nil {
		return nil, err
	}
	if state.Status != localagent.EdgeStatusRunning || state.PID <= 0 || target.PID != state.PID || target.OwnerUID != os.Getuid() {
		return nil, fmt.Errorf("running edge state/target = %+v / %+v", state, target)
	}
	if err := edgelifecycle.Start(config); !errors.Is(err, localagent.ErrProcessLocked) {
		return nil, fmt.Errorf("second edge start error = %v, want process lock", err)
	}
	if err := edgelifecycle.Stop(paths, 2*time.Second); err != nil {
		return nil, err
	}
	stopped = true
	if err := waitForHarnessCondition(ctx, func() bool { return !processAliveForEdge(state.PID) }); err != nil {
		return nil, fmt.Errorf("edge PID %d remained alive: %w", state.PID, err)
	}

	failingPaths := localagent.PathsForHome(filepath.Join(root, "failing-edge-home"))
	if err := localagent.EnsureDirs(failingPaths); err != nil {
		return nil, err
	}
	if err := os.WriteFile(failingPaths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(failingPaths.EdgeLogPath, []byte("old caddy log line\n"), 0o600); err != nil {
		return nil, err
	}
	failingCaddy := filepath.Join(root, "failing-edge-caddy")
	if err := os.WriteFile(failingCaddy, []byte("#!/bin/sh\necho 'listen tcp 127.0.0.1:443: bind: permission denied' >&2\nexit 1\n"), 0o755); err != nil {
		return nil, err
	}
	failure := edgelifecycle.Start(edgelifecycle.StartConfig{
		Binary: failingCaddy, Paths: failingPaths, PublicAddr: "127.0.0.1:443",
		TargetAddr: "127.0.0.1:19443", HTTPTargetAddr: "127.0.0.1:19080",
		AdminSocket: filepath.Join(failingPaths.RunDir, "caddy-admin.sock"), UpstreamAddr: "127.0.0.1:9440", StartupSettle: 15 * time.Second,
	})
	if failure == nil || !strings.Contains(failure.Error(), "Caddy edge exited during startup") || !strings.Contains(failure.Error(), "permission denied") || strings.Contains(failure.Error(), "old caddy log line") {
		return nil, fmt.Errorf("fast startup failure = %v", failure)
	}
	failedState, err := localagent.LoadEdgeState(failingPaths.EdgeStatePath)
	if err != nil {
		return nil, err
	}
	if localagent.EdgeStateRunning(failedState) {
		return nil, fmt.Errorf("failed edge state = %+v", failedState)
	}
	return map[string]any{
		"running_state_written": true,
		"target_state_written":  true,
		"lock_rejected":         true,
		"process_stopped":       true,
		"fast_exit_attributed":  true,
	}, nil
}

func runHarnessCaddyConfigValidation(ctx context.Context, root string) (map[string]any, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	caddyBinary, err := resolveCaddyBinary(ctx, paths, false)
	if err != nil {
		return map[string]any{"available": false, "reason": err.Error()}, nil
	}
	frontendRoot := filepath.Join(root, "published", "current")
	if err := os.MkdirAll(frontendRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(frontendRoot, "index.html"), []byte("<html>probe</html>\n"), 0o644); err != nil {
		return nil, err
	}
	modes := make([]string, 0, 2)
	for _, direct := range []bool{false, true} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		config := edgelifecycle.CaddyConfig(edgelifecycle.CaddyConfigOptions{
			ListenAddr:  "127.0.0.1:19443",
			Upstream:    "127.0.0.1:9440",
			AdminSocket: filepath.Join(root, fmt.Sprintf("validate-%t.sock", direct)),
			Token:       "probe-token",
			PublicDomains: []edgelifecycle.PublicDomainSite{{
				Domain: "probe.example.invalid",
				Frontends: []edgelifecycle.StaticFrontendRoute{{
					Name: "web", Root: frontendRoot, BasePath: "/", OwnsRoot: true,
				}},
			}},
			StorageDir:     filepath.Join(root, "storage"),
			HTTPListenPort: "19080",
			PublicDirect:   direct,
		})
		configPath := filepath.Join(root, fmt.Sprintf("Caddyfile.validate-%t", direct))
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			return nil, err
		}
		if err := edgelifecycle.ValidateCaddyConfig(caddyBinary, configPath); err != nil {
			return nil, fmt.Errorf("validate generated static Caddy config (direct=%v): %w", direct, err)
		}
		modes = append(modes, fmt.Sprintf("direct=%v", direct))
	}
	return map[string]any{"available": true, "modes": modes}, nil
}

func runHarnessTrustLocalCAProbe(ctx context.Context, root string) (map[string]any, error) {
	marker := filepath.Join(root, "trust-marker.txt")
	sourcePath := filepath.Join(root, "fake-caddy.go")
	binaryPath := filepath.Join(root, "fake-caddy")
	source := fmt.Sprintf(`package main

import (
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const marker = %q

func appendMarker(line string) {
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(1)
	}
	_, _ = file.WriteString(line + "\n")
	_ = file.Close()
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	config := ""
	for i := 2; i+1 < len(os.Args); i++ {
		if os.Args[i] == "--config" {
			config = os.Args[i+1]
		}
	}
	switch os.Args[1] {
	case "run":
		data, err := os.ReadFile(config)
		if err != nil {
			os.Exit(1)
		}
		socket := ""
		for line := range strings.Lines(string(data)) {
			if _, rest, ok := strings.Cut(line, "admin unix//"); ok {
				socket = strings.TrimSpace(rest)
				break
			}
		}
		if socket == "" {
			os.Exit(1)
		}
		_ = os.Remove(socket)
		listener, err := net.Listen("unix", socket)
		if err != nil {
			os.Exit(1)
		}
		appendMarker("run")
		appendMarker("config " + config)
		appendMarker("socket " + socket)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			for {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				_ = connection.Close()
			}
		}()
		<-signals
		_ = listener.Close()
	case "trust":
		appendMarker("trust")
	default:
		os.Exit(2)
	}
}
`, marker)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		return nil, err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, sourcePath)
	build.Env = append(envpolicy.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build fake Caddy trust binary: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := edgelifecycle.TrustLocalCA(binaryPath, io.Discard, io.Discard); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	configPath := ""
	socketPath := ""
	seenRun, seenTrust := false, false
	for _, line := range lines {
		switch {
		case line == "run":
			seenRun = true
		case line == "trust":
			seenTrust = true
		case strings.HasPrefix(line, "config "):
			configPath = strings.TrimPrefix(line, "config ")
		case strings.HasPrefix(line, "socket "):
			socketPath = strings.TrimPrefix(line, "socket ")
		}
	}
	if !seenRun || !seenTrust || configPath == "" || socketPath == "" {
		return nil, fmt.Errorf("fake Caddy trust marker = %q", data)
	}
	for label, path := range map[string]string{"temporary config": configPath, "temporary admin socket": socketPath, "temporary directory": filepath.Dir(configPath)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return nil, fmt.Errorf("%s remains at %s: %v", label, path, err)
		}
	}
	return map[string]any{
		"run_invoked":                 true,
		"trust_invoked":               true,
		"temporary_directory_removed": true,
	}, nil
}
