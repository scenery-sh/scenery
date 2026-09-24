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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/envpolicy"
)

// The configuration probes prove environment configuration against real
// `scenery up` runtimes of a disposable copy of testdata/apps/multiservice
// whose echo service consumes configurable inputs. Each owns its temporary
// roots and agent homes and removes them, and any secret items it created.

type configurationProbe struct {
	ctx      context.Context
	repo     string
	binary   string
	base     string
	home     string
	appID    string
	extraEnv []string
}

func newConfigurationProbe(ctx context.Context, repo, prefix string) (*configurationProbe, error) {
	base, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, err
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	return &configurationProbe{ctx: ctx, repo: repo, binary: harnessLocalSceneryBinaryPath(repo), base: base, home: filepath.Join(base, "agent-home"), appID: "cfgprobe-" + hex.EncodeToString(suffix)}, nil
}

func (p *configurationProbe) env() []string {
	return envWithOverrides(envpolicy.Environ(), append([]string{"SCENERY_AGENT_HOME=" + p.home, "SCENERY_AGENT_ROUTER_ADDR=127.0.0.1:0"}, p.extraEnv...)...)
}

// run executes the Scenery CLI; stdin carries secret bytes, never argv.
func (p *configurationProbe) run(root string, stdin []byte, args ...string) ([]byte, error) {
	command := commandTreeContext(p.ctx, p.binary, append(args, "--app-root", root)...)
	command.Dir, command.Env = root, p.env()
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	if err := command.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("scenery %s: %w: %s", strings.Join(args, " "), err, tailString(firstNonEmpty(stderr.String(), stdout.String()), 4096))
	}
	return stdout.Bytes(), nil
}

// prepare writes the fixture into rootA, commits it and adds rootB as a
// linked worktree when set. withSecret adds a required environment secret.
func (p *configurationProbe) prepare(rootA, rootB string, environments string, withSecret bool) error {
	source := filepath.Join(p.repo, "testdata", "apps", "multiservice")
	for _, name := range []string{".gitignore", "app.scn", "go.mod", "go.sum", "echo/api.go", "echo/package.scn", "greeter/api.go", "greeter/package.scn", "internal/text/text.go"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return err
		}
		switch name {
		case "go.mod":
			data = bytes.ReplaceAll(data, []byte("=> ../../.."), []byte("=> "+filepath.ToSlash(p.repo)))
		case "echo/package.scn":
			data = configurationProbeEchoPackage(data, withSecret)
		case "echo/api.go":
			data = []byte(configurationProbeEchoSource(withSecret))
		}
		path := filepath.Join(rootA, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
	}
	config := fmt.Sprintf(`{"name":%q,"id":%q,"envs":{%s}}`, p.appID, p.appID, environments)
	if err := os.WriteFile(filepath.Join(rootA, ".scenery.json"), []byte(config), 0o600); err != nil {
		return err
	}
	steps := [][]string{{"init", "--quiet"}, {"add", "."}, {"commit", "--quiet", "-m", "Configuration probe fixture"}}
	if rootB != "" {
		steps = append(steps, []string{"worktree", "add", "--quiet", "-b", "probe-b", rootB})
	}
	for _, args := range steps {
		command := commandTreeContext(p.ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Scenery configuration probe", "-c", "user.email=configuration-probe@example.invalid"}, args...)...)
		command.Dir = rootA
		if out, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("prepare configuration fixture: %w: %s", err, out)
		}
	}
	return nil
}

func configurationProbeEchoPackage(data []byte, withSecret bool) []byte {
	inputs := `input "gateway" {
  type = resource_ref("http_gateway")
}

input "prefix" {
  type    = string
  phase   = "deployment"
  default = "echo"
}

input "repeat" {
  type    = uint32
  phase   = "deployment"
  default = 1
  minimum = 1
}`
	config := "\n  config {\n    prefix = var.prefix\n    repeat = var.repeat\n"
	if withSecret {
		inputs += `

input "token" {
  type      = resource_ref("secret")
  phase     = "deployment"
  sensitive = true
}`
		config += "    token  = var.token\n"
	}
	config += "  }"
	text := strings.Replace(string(data), "input \"gateway\" {\n  type = resource_ref(\"http_gateway\")\n}", inputs, 1)
	text = strings.Replace(text, "  implementation {\n    constructor = \"NewService\"\n  }", "  implementation {\n    constructor = \"NewService\"\n  }\n"+config, 1)
	return []byte(text)
}

func configurationProbeEchoSource(withSecret bool) string {
	token := ""
	if withSecret {
		token = `
	secret, err := input.Config.Token.Reveal()
	if err != nil {
		return nil, err
	}
	prefix += fmt.Sprintf("[%d]", len(secret))`
	}
	return `package echo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	echocontract "example.com/multiservice/echo/scenerycontract"
	"example.com/multiservice/internal/text"
)

type Service struct {
	prefix string
}

func NewService(_ context.Context, input echocontract.EchoConstructorInput) (*Service, error) {
	if input.Config.Prefix == "boom" {
		return nil, errors.New("echo refuses prefix boom")
	}
	prefix := strings.Repeat(input.Config.Prefix, int(input.Config.Repeat))` + token + `
	_ = fmt.Sprint()
	return &Service{prefix: prefix}, nil
}

func (s *Service) Echo(_ context.Context, input echocontract.EchoInput) (echocontract.EchoOutcome, error) {
	return echocontract.EchoOk{Value: echocontract.EchoResult{Message: text.Label(s.prefix, input.Message)}}, nil
}
`
}

type configurationRuntime struct {
	socket    string
	processes map[string]configurationProcess
}

type configurationProcess struct {
	PID        int
	Executable string
}

func (p *configurationProbe) up(root string) (configurationRuntime, error) {
	output, err := p.run(root, nil, "up", "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return configurationRuntime{}, err
	}
	return decodeConfigurationRuntime(output)
}

func decodeConfigurationRuntime(output []byte) (configurationRuntime, error) {
	var envelope struct {
		Data struct {
			Session struct {
				Backends map[string]struct {
					Addr string `json:"addr"`
				} `json:"backends"`
				Processes map[string]struct {
					PID   int `json:"pid"`
					Owner struct {
						Exe string `json:"exe"`
					} `json:"owner"`
				} `json:"processes"`
			} `json:"session"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return configurationRuntime{}, err
	}
	result := configurationRuntime{socket: envelope.Data.Session.Backends["api"].Addr, processes: map[string]configurationProcess{}}
	for name, process := range envelope.Data.Session.Processes {
		result.processes[name] = configurationProcess{PID: process.PID, Executable: filepath.Base(process.Owner.Exe)}
	}
	if result.socket == "" || len(result.processes) == 0 {
		return result, fmt.Errorf("runtime reported no API socket or processes")
	}
	return result, nil
}

// processes lists the live application processes of root by executable.
func (p *configurationProbe) processes(root string) (map[string]configurationProcess, error) {
	output, err := exec.CommandContext(p.ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	sessions := filepath.Join(root, ".scenery", "sessions")
	canonical, _ := filepath.EvalSymlinks(root)
	result := map[string]configurationProcess{}
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (!strings.HasPrefix(fields[1], sessions) && (canonical == "" || !strings.HasPrefix(fields[1], filepath.Join(canonical, ".scenery", "sessions")))) {
			continue
		}
		var pid int
		_, _ = fmt.Sscan(fields[0], &pid)
		result[filepath.Base(fields[1])+fmt.Sprint("#", pid)] = configurationProcess{PID: pid, Executable: filepath.Base(fields[1])}
	}
	return result, nil
}

func echoMessage(ctx context.Context, socket string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://runtime/echo", strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		return "", err
	}
	request.Header.Set("content-type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var decoded struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("echo response %q", body)
	}
	return decoded.Message, nil
}

// waitForEcho polls until echo answers want.
func waitForEcho(ctx context.Context, socket, want string) error {
	deadline := time.Now().Add(30 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		message, err := echoMessage(ctx, socket)
		if err == nil && message == want {
			return nil
		}
		last = message
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("echo answered %q, want %q", last, want)
}

func (p *configurationProbe) show(root, environment string) (map[string]any, error) {
	output, err := p.run(root, nil, "config", "show", "--env", environment, "-o", "json")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, err
	}
	return envelope.Data, nil
}

// cleanup stops every runtime of roots and removes the probe directory.
func (p *configurationProbe) cleanup(summary map[string]any, roots ...string) {
	for _, root := range roots {
		_, _ = p.run(root, nil, "down", "-o", "json")
	}
	leftover := 0
	for _, root := range roots {
		if processes, err := p.processes(root); err == nil {
			leftover += len(processes)
		}
	}
	if leftover > 0 {
		summary["cleanup"], summary["retained_probe_root"] = "failed", p.base
		return
	}
	if err := os.RemoveAll(p.base); err != nil {
		summary["cleanup"] = "failed"
		return
	}
	summary["cleanup"] = "passed"
}

func configurationProbeStep(name string, run func(context.Context, string) (map[string]any, []checkDiagnostic, error)) func(context.Context, string) harnessStep {
	return func(ctx context.Context, repoRoot string) harnessStep {
		started := time.Now()
		step := harnessStep{Name: name}
		var err error
		step.Summary, step.Diagnostics, err = run(ctx, repoRoot)
		step.DurationMS = time.Since(started).Milliseconds()
		if err != nil {
			step.Error = strings.TrimSpace(err.Error())
			step.Diagnostics = append(step.Diagnostics, checkDiagnostic{Stage: name, Severity: "error", Message: step.Error, SuggestedAction: "Fix environment configuration delivery, then rerun `go run ./scripts/verify --summary --write`."})
		}
		step.OK = err == nil && !hasErrorDiagnostics(step.Diagnostics)
		return step
	}
}

// runHarnessConfigurationProbe proves two-worktree sharing, consumer-only
// restarts without builds, rejected candidates, unused keys, poisoned dotenv
// files and ambient variables, and default restoration.
func runHarnessConfigurationProbe(ctx context.Context, repoRoot string) (summary map[string]any, _ []checkDiagnostic, err error) {
	summary = map[string]any{"fixture": "testdata/apps/multiservice", "cleanup": "pending"}
	p, err := newConfigurationProbe(ctx, repoRoot, "scenery-configuration-probe-")
	if err != nil {
		return summary, nil, err
	}
	rootA, rootB := filepath.Join(p.base, "a"), filepath.Join(p.base, "b")
	defer p.cleanup(summary, rootA, rootB)
	// Ambient variables named like the inputs change nothing.
	p.extraEnv = []string{"ECHO_PREFIX=ambient", "PREFIX=ambient", "JWT_SECRET=ambient"}
	if err := p.prepare(rootA, rootB, `"local":{"default":true}`, false); err != nil {
		return summary, nil, err
	}
	for _, root := range []string{rootA, rootB} {
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte("MALFORMED\nECHO_PREFIX=poison\nPREFIX=poison\n"), 0o600); err != nil {
			return summary, nil, err
		}
	}
	if _, err := p.run(rootA, nil, "config", "set", "echo.prefix", "alpha", "--env", "local"); err != nil {
		return summary, nil, err
	}
	a, err := p.up(rootA)
	if err != nil {
		return summary, nil, err
	}
	b, err := p.up(rootB)
	if err != nil {
		return summary, nil, err
	}
	for _, runtime := range []configurationRuntime{a, b} {
		if err := waitForEcho(ctx, runtime.socket, "alpha:hi"); err != nil {
			return summary, nil, fmt.Errorf("shared value with poisoned dotenv and ambient variables: %w", err)
		}
	}
	if a.socket == b.socket {
		return summary, nil, errors.New("worktrees share one API socket")
	}
	summary["shared_value_distinct_runtimes"] = "passed"
	before, err := p.processes(rootA)
	if err != nil {
		return summary, nil, err
	}
	logBefore, err := configurationBuildArtifacts(p.home)
	if err != nil {
		return summary, nil, err
	}
	if _, err := p.run(rootB, nil, "config", "set", "echo.prefix", "beta", "--env", "local"); err != nil {
		return summary, nil, err
	}
	for _, runtime := range []configurationRuntime{a, b} {
		if err := waitForEcho(ctx, runtime.socket, "beta:hi"); err != nil {
			return summary, nil, fmt.Errorf("configuration change: %w", err)
		}
	}
	after, err := p.processes(rootA)
	if err != nil {
		return summary, nil, err
	}
	restarted, kept, err := compareConfigurationProcesses(before, after)
	if err != nil {
		return summary, nil, err
	}
	if restarted != 1 || kept != len(before)-1 {
		return summary, nil, fmt.Errorf("configuration change restarted %d and kept %d of %d processes; want exactly the consumer restarted", restarted, kept, len(before))
	}
	logAfter, err := configurationBuildArtifacts(p.home)
	if err != nil {
		return summary, nil, err
	}
	if logAfter != logBefore {
		return summary, nil, fmt.Errorf("a configuration-only change linked %d executables", logAfter-logBefore)
	}
	summary["consumer_only_restart_same_executables_no_link"] = "passed"
	// A newer branch's differently typed value and unknown key.
	store, err := appconfig.OpenStore(p.home, p.appID)
	if err != nil {
		return summary, nil, err
	}
	for _, mutation := range []appconfig.Mutation{{Key: "echo.repeat", Value: json.RawMessage(`"many"`)}, {Key: "echo.future_flag", Value: json.RawMessage(`true`)}} {
		if _, err := store.Mutate(ctx, "local", mutation); err != nil {
			return summary, nil, err
		}
	}
	if err := configurationWait(func() (bool, error) {
		shown, err := p.show(rootA, "local")
		if err != nil {
			return false, err
		}
		applied, _ := shown["applied"].(map[string]any)
		return applied["state"] == "rejected" && strings.Contains(fmt.Sprint(shown["unused"]), "echo.future_flag"), nil
	}); err != nil {
		return summary, nil, fmt.Errorf("rejected candidate was not reported: %w", err)
	}
	if message, err := echoMessage(ctx, a.socket); err != nil || message != "beta:hi" {
		return summary, nil, fmt.Errorf("healthy generation did not keep serving: %q %v", message, err)
	}
	summary["incompatible_candidate_rejected_unused_reported"] = "passed"
	for _, key := range []string{"echo.repeat", "echo.prefix"} {
		if _, err := p.run(rootA, nil, "config", "unset", key, "--env", "local"); err != nil {
			return summary, nil, err
		}
	}
	for _, runtime := range []configurationRuntime{a, b} {
		if err := waitForEcho(ctx, runtime.socket, "echo:hi"); err != nil {
			return summary, nil, fmt.Errorf("unset did not restore defaults: %w", err)
		}
	}
	summary["unset_restores_defaults"] = "passed"
	return summary, nil, nil
}

// configurationBuildArtifacts counts link steps recorded by every runtime log
// of an agent home.
func configurationBuildArtifacts(home string) (int, error) {
	logs, err := filepath.Glob(filepath.Join(home, "worktrees", "*", "control", "dev", "*.log"))
	if err != nil {
		return 0, err
	}
	count := 0
	for _, path := range logs {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		count += bytes.Count(data, []byte(`"name":"build.artifact"`))
	}
	return count, nil
}

func compareConfigurationProcesses(before, after map[string]configurationProcess) (restarted, kept int, err error) {
	pids := map[int]string{}
	for _, process := range after {
		pids[process.PID] = process.Executable
	}
	executables := map[string]bool{}
	for _, process := range after {
		executables[process.Executable] = true
	}
	for _, process := range before {
		if !executables[process.Executable] {
			return 0, 0, fmt.Errorf("executable %s changed", process.Executable)
		}
		if pids[process.PID] == process.Executable {
			kept++
		} else {
			restarted++
		}
	}
	return restarted, kept, nil
}

func configurationWait(check func() (bool, error)) error {
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		ok, err := check()
		if ok {
			return nil
		}
		last = err
		time.Sleep(200 * time.Millisecond)
	}
	if last != nil {
		return last
	}
	return errors.New("timed out")
}

// runHarnessConfigurationSecretsProbe proves the platform secret backend:
// readiness failure, hidden storage, exact delivery, no leakage, rotation of
// only the consumer, and cleanup of every item it created.
func runHarnessConfigurationSecretsProbe(ctx context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	summary = map[string]any{"fixture": "testdata/apps/multiservice", "cleanup": "pending", "platform": runtime.GOOS}
	p, err := newConfigurationProbe(ctx, repoRoot, "scenery-configuration-secrets-probe-")
	if err != nil {
		return summary, nil, err
	}
	root := filepath.Join(p.base, "app")
	store, err := appconfig.OpenStore(p.home, p.appID)
	if err != nil {
		return summary, nil, err
	}
	backend, backendErr := appconfig.DefaultSecretBackend(store)
	if backendErr == nil {
		backendErr = backend.Ready(ctx)
	}
	other := map[string]string{"darwin": "linux systemd-creds", "linux": "macOS Keychain"}[runtime.GOOS]
	diagnostics = append(diagnostics, checkDiagnostic{Stage: "configuration secrets probe", Severity: "warning", Message: "the " + other + " backend segment is unexecuted on " + runtime.GOOS + " and remains unverified until its platform job passes"})
	if backendErr != nil {
		summary["status"] = "unverified"
		diagnostics = append(diagnostics, checkDiagnostic{Stage: "configuration secrets probe", Severity: "warning", Message: "secret backend unavailable on this host: " + backendErr.Error()})
		_ = os.RemoveAll(p.base)
		summary["cleanup"] = "passed"
		return summary, diagnostics, nil
	}
	summary["backend"] = backend.Name()
	defer func() {
		// Secret items first: their version index lives in the agent home
		// that the directory cleanup removes.
		_, _ = p.run(root, nil, "down", "-o", "json")
		versions, _ := backend.Versions(context.Background(), "local")
		removed := 0
		for key, list := range versions {
			for _, version := range list {
				if backend.Remove(context.Background(), "local", key, version) == nil {
					removed++
				}
			}
		}
		remaining, err := backend.Versions(context.Background(), "local")
		summary["secret_items_removed"] = removed
		if err != nil || len(remaining) != 0 || removed == 0 {
			summary["secret_cleanup"] = "failed"
		} else {
			summary["secret_cleanup"] = "passed"
		}
		p.cleanup(summary, root)
	}()
	if err := p.prepare(root, "", `"local":{"default":true}`, true); err != nil {
		return summary, diagnostics, err
	}
	if _, err := p.up(root); err == nil || !strings.Contains(err.Error(), "echo.token") {
		return summary, diagnostics, fmt.Errorf("startup without the required secret = %v", err)
	}
	summary["missing_secret_named"] = "passed"
	secret := "probe-" + hex.EncodeToString([]byte(p.appID))
	output, err := p.run(root, []byte(secret), "config", "set", "echo.token", "--env", "local", "--stdin", "-o", "json")
	if err != nil {
		return summary, diagnostics, err
	}
	runtimeState, err := p.up(root)
	if err != nil {
		return summary, diagnostics, err
	}
	if err := waitForEcho(ctx, runtimeState.socket, fmt.Sprintf("echo[%d]:hi", len(secret))); err != nil {
		return summary, diagnostics, fmt.Errorf("secret delivery: %w", err)
	}
	shown, err := p.run(root, nil, "config", "show", "--env", "local", "-o", "json")
	if err != nil {
		return summary, diagnostics, err
	}
	if leak := configurationSecretLeak(secret, [][]byte{output, shown}, p.home, root); leak != "" {
		return summary, diagnostics, fmt.Errorf("secret plaintext leaked into %s", leak)
	}
	if processes, err := p.processes(root); err == nil {
		for _, process := range processes {
			environment, _ := exec.CommandContext(ctx, "ps", "eww", "-p", fmt.Sprint(process.PID)).Output()
			if bytes.Contains(environment, []byte(secret)) {
				return summary, diagnostics, errors.New("secret plaintext is in a process environment")
			}
		}
	}
	summary["secret_delivered_without_leak"] = "passed"
	rotated := secret + "-rotated"
	if _, err := p.run(root, []byte(rotated), "config", "set", "echo.token", "--env", "local", "--stdin"); err != nil {
		return summary, diagnostics, err
	}
	if err := waitForEcho(ctx, runtimeState.socket, fmt.Sprintf("echo[%d]:hi", len(rotated))); err != nil {
		return summary, diagnostics, fmt.Errorf("secret rotation: %w", err)
	}
	summary["rotation_applied"] = "passed"
	return summary, diagnostics, nil
}

// configurationSecretLeak reports where plaintext appears among outputs and
// the probe's state trees.
func configurationSecretLeak(secret string, outputs [][]byte, roots ...string) string {
	for _, output := range outputs {
		if bytes.Contains(output, []byte(secret)) {
			return "CLI output"
		}
	}
	for _, root := range roots {
		found := ""
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || found != "" {
				return nil
			}
			if info, err := entry.Info(); err != nil || info.Size() > 64<<20 {
				return nil
			}
			if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte(secret)) {
				found = path
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// runHarnessConfigurationDeployProbe deploys through a test-double ssh that
// runs every remote command on this host under a separate target home, so
// rsync, both receivers and the target's scenery up run for real. The
// systemd service owner and a real reboot are outside this probe.
func runHarnessConfigurationDeployProbe(ctx context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	summary = map[string]any{"fixture": "testdata/apps/multiservice", "cleanup": "pending"}
	p, err := newConfigurationProbe(ctx, repoRoot, "scenery-configuration-deploy-probe-")
	if err != nil {
		return summary, nil, err
	}
	root := filepath.Join(p.base, "app")
	targetHome := filepath.Join(p.base, "target-home")
	fakeSSH := filepath.Join(p.base, "bin")
	sourceRoot := filepath.Join(targetHome, ".scenery", "deployments", p.appID, "production", "source")
	// onTarget runs a command exactly as the deployment does: through ssh.
	onTarget := func(command string) ([]byte, error) {
		cmd := commandTreeContext(ctx, filepath.Join(fakeSSH, "ssh"), "-o", "BatchMode=yes", "--", "probe-target", command)
		cmd.Env = p.env()
		output, err := cmd.Output()
		if err != nil {
			return output, fmt.Errorf("target %q: %w", command, err)
		}
		return output, nil
	}
	defer func() {
		_, _ = onTarget("scenery down --app-root " + shellQuote(sourceRoot) + " -o json")
		p.cleanup(summary, root, sourceRoot)
	}()
	diagnostics = append(diagnostics, checkDiagnostic{Stage: "configuration deploy probe", Severity: "warning", Message: "the Linux systemd service owner, systemd-creds and a real reboot are unexecuted by this local target and remain unverified until a disposable systemd target runs them"})
	if err := os.MkdirAll(targetHome, 0o700); err != nil {
		return summary, diagnostics, err
	}
	if err := os.MkdirAll(fakeSSH, 0o700); err != nil {
		return summary, diagnostics, err
	}
	// The target shares this host's Go caches: its own HOME would hide the
	// module cache from an offline verifier.
	goEnv, err := exec.CommandContext(ctx, "go", "env", "GOMODCACHE", "GOCACHE", "GOPATH").Output()
	if err != nil {
		return summary, diagnostics, err
	}
	goValues := strings.Split(strings.TrimSpace(string(goEnv)), "\n")
	if len(goValues) != 3 {
		return summary, diagnostics, fmt.Errorf("go env reported %d values", len(goValues))
	}
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) shift 2 ;; --) shift; break ;; -*) shift ;; *) break ;; esac; done\nshift\ncd " + shellQuote(targetHome) + "\nexec env HOME=" + shellQuote(targetHome) + " SCENERY_AGENT_HOME=" + shellQuote(filepath.Join(targetHome, ".scenery")) + " SCENERY_AGENT_ROUTER_ADDR=127.0.0.1:0 GOMODCACHE=" + shellQuote(goValues[0]) + " GOCACHE=" + shellQuote(goValues[1]) + " GOPATH=" + shellQuote(goValues[2]) + " PATH=" + shellQuote(filepath.Dir(p.binary)+":"+envpolicy.Get("PATH")) + " /bin/sh -c \"$*\"\n"
	if err := os.WriteFile(filepath.Join(fakeSSH, "ssh"), []byte(script), 0o700); err != nil {
		return summary, diagnostics, err
	}
	p.extraEnv = []string{"PATH=" + fakeSSH + string(os.PathListSeparator) + envpolicy.Get("PATH")}
	if err := p.prepare(root, "", `"local":{"default":true},"production":{"deploy":{"ssh":["probe-target"]}}`, false); err != nil {
		return summary, diagnostics, err
	}
	// The workstation check needs generated contracts; they are ignored and
	// never deployed, so the target generates its own.
	if _, err := p.run(root, nil, "generate", "-o", "json"); err != nil {
		return summary, diagnostics, err
	}
	if _, err := p.run(root, nil, "config", "set", "echo.prefix", "prod", "--env", "production"); err != nil {
		return summary, diagnostics, err
	}
	if _, err := os.Stat(filepath.Join(p.home, "apps", p.appID, "environments", "production.json")); !errors.Is(err, os.ErrNotExist) {
		return summary, diagnostics, errors.New("a production value was stored on the workstation")
	}
	deploy := func() error {
		_, err := p.run(root, nil, "deploy", "--env", "production")
		return err
	}
	targetEcho := func(want string) error {
		output, err := onTarget("scenery ps -o json --app-root " + shellQuote(sourceRoot))
		if err != nil {
			return err
		}
		socket := configurationFindSocket(output)
		if socket == "" {
			return fmt.Errorf("target runtime has no API socket")
		}
		return waitForEcho(ctx, socket, want)
	}
	if err := deploy(); err != nil {
		return summary, diagnostics, err
	}
	if err := targetEcho("prod:hi"); err != nil {
		return summary, diagnostics, err
	}
	summary["first_release_active"] = "passed"
	if _, err := p.run(root, nil, "config", "set", "echo.prefix", "prod2", "--env", "production"); err != nil {
		return summary, diagnostics, err
	}
	if _, err := onTarget("scenery down --app-root " + shellQuote(sourceRoot) + " -o json && scenery up --detach --wait ready --env production --app-root " + shellQuote(sourceRoot) + " -o json"); err != nil {
		return summary, diagnostics, err
	}
	if err := targetEcho("prod:hi"); err != nil {
		return summary, diagnostics, fmt.Errorf("restart adopted desired configuration: %w", err)
	}
	summary["restart_uses_active_revision"] = "passed"
	before, err := p.processes(sourceRoot)
	if err != nil {
		return summary, diagnostics, err
	}
	if err := deploy(); err != nil {
		return summary, diagnostics, err
	}
	if err := targetEcho("prod2:hi"); err != nil {
		return summary, diagnostics, err
	}
	after, err := p.processes(sourceRoot)
	if err != nil {
		return summary, diagnostics, err
	}
	if _, _, err := compareConfigurationProcesses(before, after); err != nil {
		return summary, diagnostics, fmt.Errorf("configuration-only deploy relinked: %w", err)
	}
	summary["config_only_deploy_same_executables"] = "passed"
	if _, err := p.run(root, nil, "config", "set", "echo.prefix", "boom", "--env", "production"); err != nil {
		return summary, diagnostics, err
	}
	if err := deploy(); err == nil || !strings.Contains(err.Error(), "restored previous release") {
		return summary, diagnostics, fmt.Errorf("failed activation = %v", err)
	}
	if err := targetEcho("prod2:hi"); err != nil {
		return summary, diagnostics, fmt.Errorf("rollback did not restore the previous release: %w", err)
	}
	summary["failed_activation_restores_previous"] = "passed"
	legacy := filepath.Join(targetHome, ".scenery", "apps", p.appID, ".scenery.json")
	if err := os.WriteFile(legacy, []byte(`{"name":"legacy"}`), 0o600); err != nil {
		return summary, diagnostics, err
	}
	if _, err := p.run(root, nil, "config", "set", "echo.prefix", "late", "--env", "production"); err == nil || !strings.Contains(err.Error(), "legacy deployment checkout") {
		return summary, diagnostics, fmt.Errorf("configuration over a legacy checkout = %v", err)
	}
	if err := os.Remove(legacy); err != nil {
		return summary, diagnostics, err
	}
	summary["legacy_checkout_refused"] = "passed"
	return summary, diagnostics, nil
}

func configurationFindSocket(output []byte) string {
	var decoded any
	if json.Unmarshal(output, &decoded) != nil {
		return ""
	}
	var find func(any) string
	find = func(value any) string {
		switch typed := value.(type) {
		case map[string]any:
			if backends, ok := typed["backends"].(map[string]any); ok {
				if api, ok := backends["api"].(map[string]any); ok && api["network"] == "unix" {
					if addr, _ := api["addr"].(string); addr != "" {
						return addr
					}
				}
			}
			for _, child := range typed {
				if found := find(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := find(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return find(decoded)
}
