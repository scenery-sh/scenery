package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devtools"
	"scenery.sh/internal/envpolicy"
)

const victoriaDefaultHost = "127.0.0.1"

const (
	victoriaHealthCheckInterval = time.Second
	victoriaRecoveryBackoffMax  = 30 * time.Second
)

type victoriaComponentSpec struct {
	Name               string
	DisplayName        string
	Repo               string
	ArchiveSlug        string
	BinaryName         string
	ExtraBinaries      []string
	Version            string
	DefaultPort        int
	EndpointPath       string
	URLPath            string
	StorageDir         string
	EnvPrefix          string
	OTELVar            string
	SceneryURLVar      string
	SceneryEndpointVar string
}

type victoriaComponent struct {
	spec        victoriaComponentSpec
	binaryPath  string
	baseURL     string
	endpointURL string
	storagePath string
	stdoutLog   string
	stderrLog   string
	cmd         *exec.Cmd
	done        chan error
	external    bool
	startedAt   time.Time
}

type victoriaComponentStartPlan struct {
	spec        victoriaComponentSpec
	binaryPath  string
	baseURL     string
	endpointURL string
	storagePath string
	external    bool
}

type victoriaComponentStartResult struct {
	index     int
	spec      victoriaComponentSpec
	component *victoriaComponent
	err       error
}

type victoriaStack struct {
	components []*victoriaComponent
	mu         sync.RWMutex
	clearSince map[string]time.Time
}

var victoriaToolchainSyncMu sync.Mutex
var victoriaSubstrateProcessLocks sync.Map

func startVictoriaStack(ctx context.Context, appRoot string, console *runConsole) *victoriaStack {
	return startVictoriaStackWithRoot(ctx, victoriaRootDir(appRoot), console)
}

func startVictoriaStackWithRoot(ctx context.Context, root string, console *runConsole) *victoriaStack {
	if !victoriaEnabled() {
		return nil
	}
	binDir := filepath.Join(root, "bin")
	download := victoriaDownloadEnabled()
	if err := ensureLocalStateDirIgnored(root); err != nil {
		warnVictoria(console, "Victoria local state unavailable: %v", err)
		return nil
	}

	results := startVictoriaComponents(ctx, root, binDir, victoriaComponentSpecs(), download, console)
	stack := &victoriaStack{components: make([]*victoriaComponent, 0, len(results))}
	for _, result := range results {
		if result.err != nil {
			warnVictoria(console, "%s unavailable: %v", result.spec.DisplayName, result.err)
			continue
		}
		stack.components = append(stack.components, result.component)
	}
	if len(stack.components) == 0 {
		return nil
	}
	return stack
}

func victoriaStackFromSubstrate(substrate localagent.Substrate) *victoriaStack {
	if substrate.Kind != localagent.SubstrateVictoria || len(substrate.Endpoints) == 0 {
		return nil
	}
	stack := &victoriaStack{}
	specs := victoriaComponentSpecs()
	for _, spec := range specs {
		endpoint := strings.TrimSpace(substrate.Endpoints[spec.Name])
		baseURL := strings.TrimSpace(substrate.URLs[spec.Name])
		if endpoint == "" && baseURL != "" {
			endpoint = strings.TrimRight(baseURL, "/") + spec.EndpointPath
		}
		if baseURL == "" && endpoint != "" {
			baseURL = strings.TrimSuffix(endpoint, spec.EndpointPath)
		}
		if baseURL == "" || endpoint == "" {
			continue
		}
		stack.components = append(stack.components, &victoriaComponent{
			spec:        spec,
			baseURL:     baseURL,
			endpointURL: endpoint,
			stdoutLog:   strings.TrimSpace(substrate.Endpoints[spec.Name+"_stdout_log"]),
			stderrLog:   strings.TrimSpace(substrate.Endpoints[spec.Name+"_stderr_log"]),
			external:    true,
		})
	}
	if len(stack.components) != len(specs) {
		return nil
	}
	return stack
}

func (s *victoriaStack) SubstrateRequest(ownerPID int) localagent.UpsertSubstrateRequest {
	if s == nil || len(s.components) == 0 {
		return localagent.UpsertSubstrateRequest{}
	}
	urls := make(map[string]string, len(s.components))
	endpoints := make(map[string]string, len(s.components))
	pids := make(map[string]int)
	for _, component := range s.components {
		if component == nil {
			continue
		}
		urls[component.spec.Name] = component.baseURL
		endpoints[component.spec.Name] = component.endpointURL
		if component.stdoutLog != "" {
			endpoints[component.spec.Name+"_stdout_log"] = component.stdoutLog
		}
		if component.stderrLog != "" {
			endpoints[component.spec.Name+"_stderr_log"] = component.stderrLog
		}
		if component.cmd != nil && component.cmd.Process != nil {
			pids[component.spec.Name] = component.cmd.Process.Pid
		}
	}
	if pid := firstPID(pids); pid > 0 {
		ownerPID = pid
	} else if ownerPID <= 0 {
		ownerPID = firstPID(pids)
	}
	return localagent.UpsertSubstrateRequest{
		Kind:      localagent.SubstrateVictoria,
		Status:    "ready",
		OwnerPID:  ownerPID,
		PIDs:      pids,
		URLs:      urls,
		Endpoints: endpoints,
	}
}

func firstPID(pids map[string]int) int {
	for _, pid := range pids {
		if pid > 0 {
			return pid
		}
	}
	return 0
}

func (s *victoriaStack) MarkExternal() {
	if s == nil {
		return
	}
	for _, component := range s.components {
		if component != nil {
			component.external = true
		}
	}
}

func (c *victoriaComponent) ExitRecord(err error) localagent.SubstrateExit {
	if c == nil {
		return localagent.SubstrateExit{}
	}
	pid := 0
	var state *os.ProcessState
	if c.cmd != nil {
		state = c.cmd.ProcessState
		if c.cmd.Process != nil {
			pid = c.cmd.Process.Pid
		}
	}
	return substrateExitRecord(c.spec.Name, pid, c.startedAt, c.stdoutLog, c.stderrLog, err, state)
}

func (s *victoriaStack) Reachable() bool {
	if s == nil || len(s.components) == 0 {
		return false
	}
	done := make(chan bool, len(s.components))
	for _, component := range s.components {
		component := component
		go func() {
			done <- component != nil && urlAcceptsTCP(component.baseURL)
		}()
	}
	for range s.components {
		if !<-done {
			return false
		}
	}
	return true
}

func victoriaEnabled() bool {
	value, ok := envpolicy.Lookup("SCENERY_DEV_VICTORIA")
	if !ok {
		return true
	}
	return !isFalseEnv(value)
}

func victoriaDownloadEnabled() bool {
	value, ok := envpolicy.Lookup("SCENERY_DEV_VICTORIA_DOWNLOAD")
	if !ok {
		return true
	}
	return !isFalseEnv(value)
}

func isFalseEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

func victoriaRootDir(appRoot string) string {
	if value := strings.TrimSpace(envpolicy.Get("SCENERY_DEV_VICTORIA_DIR")); value != "" {
		return value
	}
	return filepath.Join(appRoot, ".scenery", "victoria")
}

func victoriaComponentSpecs() []victoriaComponentSpec {
	versions := devtools.PinnedVersions()
	return []victoriaComponentSpec{
		{
			Name:               "metrics",
			DisplayName:        "VictoriaMetrics",
			Repo:               "VictoriaMetrics",
			ArchiveSlug:        "victoria-metrics",
			BinaryName:         "victoria-metrics-prod",
			ExtraBinaries:      []string{"victoria-metrics"},
			Version:            envOrDefault("SCENERY_VICTORIA_METRICS_VERSION", versions.Victoria.Metrics.Version),
			DefaultPort:        intEnvOrDefault("SCENERY_VICTORIA_METRICS_PORT", 8428),
			EndpointPath:       "/opentelemetry/v1/metrics",
			URLPath:            "/vmui",
			StorageDir:         "metrics-data",
			EnvPrefix:          "SCENERY_VICTORIA_METRICS",
			OTELVar:            "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
			SceneryURLVar:      "SCENERY_VICTORIA_METRICS_URL",
			SceneryEndpointVar: "SCENERY_VICTORIA_METRICS_ENDPOINT",
		},
		{
			Name:               "logs",
			DisplayName:        "VictoriaLogs",
			Repo:               "VictoriaLogs",
			ArchiveSlug:        "victoria-logs",
			BinaryName:         "victoria-logs-prod",
			ExtraBinaries:      []string{"victoria-logs"},
			Version:            envOrDefault("SCENERY_VICTORIA_LOGS_VERSION", versions.Victoria.Logs.Version),
			DefaultPort:        intEnvOrDefault("SCENERY_VICTORIA_LOGS_PORT", 9428),
			EndpointPath:       "/insert/opentelemetry/v1/logs",
			URLPath:            "/select/vmui",
			StorageDir:         "logs-data",
			EnvPrefix:          "SCENERY_VICTORIA_LOGS",
			OTELVar:            "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
			SceneryURLVar:      "SCENERY_VICTORIA_LOGS_URL",
			SceneryEndpointVar: "SCENERY_VICTORIA_LOGS_ENDPOINT",
		},
		{
			Name:               "traces",
			DisplayName:        "VictoriaTraces",
			Repo:               "VictoriaTraces",
			ArchiveSlug:        "victoria-traces",
			BinaryName:         "victoria-traces-prod",
			ExtraBinaries:      []string{"victoria-traces"},
			Version:            envOrDefault("SCENERY_VICTORIA_TRACES_VERSION", versions.Victoria.Traces.Version),
			DefaultPort:        intEnvOrDefault("SCENERY_VICTORIA_TRACES_PORT", 10428),
			EndpointPath:       "/insert/opentelemetry/v1/traces",
			URLPath:            "/select/vmui",
			StorageDir:         "traces-data",
			EnvPrefix:          "SCENERY_VICTORIA_TRACES",
			OTELVar:            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
			SceneryURLVar:      "SCENERY_VICTORIA_TRACES_URL",
			SceneryEndpointVar: "SCENERY_VICTORIA_TRACES_ENDPOINT",
		},
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(envpolicy.Get(key)); value != "" {
		return value
	}
	return fallback
}

func intEnvOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(envpolicy.Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func startVictoriaComponent(ctx context.Context, root, binDir string, spec victoriaComponentSpec, download bool, console *runConsole) (*victoriaComponent, error) {
	plan, err := prepareVictoriaComponentStart(ctx, root, binDir, spec, download, console)
	if err != nil {
		return nil, err
	}
	return startVictoriaComponentFromPlan(ctx, root, plan, console)
}

func startVictoriaComponents(ctx context.Context, root, binDir string, specs []victoriaComponentSpec, download bool, console *runConsole) []victoriaComponentStartResult {
	if len(specs) == 0 {
		return nil
	}
	plans := make([]victoriaComponentStartPlan, len(specs))
	results := make([]victoriaComponentStartResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		results[i] = victoriaComponentStartResult{index: i, spec: spec}
		wg.Add(1)
		go func(index int, spec victoriaComponentSpec) {
			defer wg.Done()
			plan, err := prepareVictoriaComponentStart(ctx, root, binDir, spec, download, console)
			if err != nil {
				results[index].err = err
				return
			}
			plans[index] = plan
		}(i, spec)
	}
	wg.Wait()
	wg = sync.WaitGroup{}
	for i := range specs {
		if results[i].err != nil {
			continue
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			component, err := startVictoriaComponentFromPlan(ctx, root, plans[index], console)
			results[index].component = component
			results[index].err = err
		}(i)
	}
	wg.Wait()
	return results
}

func prepareVictoriaComponentStart(ctx context.Context, root, binDir string, spec victoriaComponentSpec, download bool, console *runConsole) (victoriaComponentStartPlan, error) {
	baseURL := fmt.Sprintf("http://%s:%d", victoriaDefaultHost, spec.DefaultPort)
	plan := victoriaComponentStartPlan{
		spec:        spec,
		baseURL:     baseURL,
		endpointURL: baseURL + spec.EndpointPath,
		storagePath: filepath.Join(root, spec.StorageDir),
	}
	if !tcpAddrAvailable(victoriaDefaultHost, spec.DefaultPort) {
		plan.external = true
		warnVictoria(console, "%s appears to be already running at %s; reusing it", spec.DisplayName, baseURL)
		return plan, nil
	}

	binaryPath, err := resolveVictoriaBinary(ctx, spec, binDir, download)
	if err != nil {
		return victoriaComponentStartPlan{}, err
	}
	plan.binaryPath = binaryPath
	return plan, nil
}

func startVictoriaComponentFromPlan(ctx context.Context, root string, plan victoriaComponentStartPlan, console *runConsole) (*victoriaComponent, error) {
	component := &victoriaComponent{
		spec:        plan.spec,
		binaryPath:  plan.binaryPath,
		baseURL:     plan.baseURL,
		endpointURL: plan.endpointURL,
		storagePath: plan.storagePath,
		external:    plan.external,
	}
	if component.external {
		return component, nil
	}

	if err := os.MkdirAll(component.storagePath, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, component.binaryPath,
		"-httpListenAddr="+net.JoinHostPort(victoriaDefaultHost, strconv.Itoa(component.spec.DefaultPort)),
		"-storageDataPath="+component.storagePath,
	)
	configureChildProcess(cmd)
	configureCommandCancellation(cmd, 5*time.Second)
	cmd.Dir = root
	logs, err := openSubstrateLogWriters(root, localagent.SubstrateVictoria, component.spec.Name, console)
	if err != nil {
		return nil, fmt.Errorf("open substrate logs: %w", err)
	}
	cmd.Stdout = logs.stdout
	cmd.Stderr = logs.stderr
	if err := cmd.Start(); err != nil {
		_ = logs.close()
		return nil, err
	}
	component.cmd = cmd
	component.done = make(chan error, 1)
	component.stdoutLog = logs.stdoutPath
	component.stderrLog = logs.stderrPath
	component.startedAt = time.Now().UTC()
	go func() {
		component.done <- cmd.Wait()
		_ = logs.close()
		close(component.done)
	}()
	if err := waitForVictoriaComponentReady(ctx, component, 5*time.Second); err != nil {
		_ = interruptProcessTree(cmd)
		_ = waitOrKillVictoriaComponent(component, time.Second)
		return nil, err
	}
	if console != nil && console.verbose {
		console.Event("victoria.start", map[string]any{
			"component":    component.spec.Name,
			"url":          component.baseURL,
			"endpoint_url": component.endpointURL,
			"storage_path": component.storagePath,
			"pid":          cmd.Process.Pid,
		})
	}
	return component, nil
}

func waitForVictoriaComponentReady(ctx context.Context, component *victoriaComponent, timeout time.Duration) error {
	if component == nil {
		return fmt.Errorf("Victoria component is nil")
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if urlAcceptsTCP(component.baseURL) {
			return nil
		}
		select {
		case err, ok := <-component.done:
			if !ok {
				return fmt.Errorf("%s exited before accepting TCP connections", component.spec.DisplayName)
			}
			if err != nil {
				return fmt.Errorf("%s exited before accepting TCP connections: %w", component.spec.DisplayName, err)
			}
			return fmt.Errorf("%s exited before accepting TCP connections", component.spec.DisplayName)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s did not accept TCP connections at %s within %s", component.spec.DisplayName, component.baseURL, timeout)
		case <-ticker.C:
		}
	}
}

func resolveVictoriaBinary(ctx context.Context, spec victoriaComponentSpec, binDir string, download bool) (string, error) {
	if path := strings.TrimSpace(envpolicy.Get(spec.EnvPrefix + "_BIN")); path != "" {
		if isExecutableFile(path) {
			return path, nil
		}
		return "", fmt.Errorf("%s_BIN points to a non-executable file: %s", spec.EnvPrefix, path)
	}
	artifactName := spec.ArchiveSlug
	if status, err := managedToolchainArtifactStatus(filepath.Dir(binDir), artifactName); err == nil && status.ManagedPath != "" && isExecutableFile(status.ManagedPath) && status.Version == spec.Version {
		return status.ManagedPath, nil
	}
	if !download {
		return "", fmt.Errorf("managed %s is not installed; system PATH binaries are not used for managed toolchain artifacts; run `scenery system toolchain sync --tool %s` or set %s_BIN explicitly", spec.DisplayName, artifactName, spec.EnvPrefix)
	}
	victoriaToolchainSyncMu.Lock()
	defer victoriaToolchainSyncMu.Unlock()
	status, err := syncManagedToolchainArtifact(ctx, filepath.Dir(binDir), artifactName)
	if err != nil {
		return "", fmt.Errorf("managed %s is not installed and could not be synced: %w", spec.DisplayName, err)
	}
	if status.Version != spec.Version {
		return "", fmt.Errorf("managed %s version is %s, expected %s from %s_VERSION", spec.DisplayName, status.Version, spec.Version, spec.EnvPrefix)
	}
	if status.ManagedPath == "" || !isExecutableFile(status.ManagedPath) {
		return "", fmt.Errorf("managed %s is not installed; run `scenery system toolchain sync --tool %s` or set %s_BIN explicitly", spec.DisplayName, artifactName, spec.EnvPrefix)
	}
	return status.ManagedPath, nil
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func checksumForArchive(body, archiveName string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, archiveName) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && len(fields[0]) == 64 && isLowerHex(strings.ToLower(fields[0])) {
			return fields[0]
		}
		for _, field := range fields {
			field = strings.TrimPrefix(field, "sha256:")
			if len(field) == 64 && isLowerHex(strings.ToLower(field)) {
				return field
			}
		}
	}
	return ""
}

func victoriaArchiveName(spec victoriaComponentSpec) (string, error) {
	goos := goruntime.GOOS
	goarch := goruntime.GOARCH
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf("automatic Victoria binary download is unsupported on %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("automatic Victoria binary download is unsupported on %s/%s", goos, goarch)
	}
	return fmt.Sprintf("%s-%s-%s-%s.tar.gz", spec.ArchiveSlug, goos, goarch, spec.Version), nil
}

func tcpAddrAvailable(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func (s *victoriaStack) Env() []string {
	if s == nil || len(s.components) == 0 {
		return nil
	}
	env := []string{"SCENERY_DEV_OBSERVABILITY_BACKEND=victoria"}
	for _, component := range s.components {
		env = append(env,
			component.spec.OTELVar+"="+component.endpointURL,
			component.spec.SceneryURLVar+"="+component.baseURL,
			component.spec.SceneryEndpointVar+"="+component.endpointURL,
		)
	}
	return env
}

func (s *victoriaStack) URLs() map[string]string {
	if s == nil || len(s.components) == 0 {
		return nil
	}
	urls := make(map[string]string, len(s.components))
	for _, component := range s.components {
		url := component.baseURL
		if component.spec.URLPath != "" {
			url += component.spec.URLPath
		}
		urls[component.spec.Name] = url
	}
	return urls
}

func (s *victoriaStack) Endpoint(name string) string {
	if s == nil {
		return ""
	}
	for _, component := range s.components {
		if component.spec.Name == name {
			return component.endpointURL
		}
	}
	return ""
}

func (s *victoriaStack) MarkCleared(appID string, at time.Time) {
	if s == nil || appID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clearSince == nil {
		s.clearSince = make(map[string]time.Time)
	}
	s.clearSince[appID] = at.UTC()
}

func (s *victoriaStack) ClearedAt(appID string) time.Time {
	if s == nil || appID == "" {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clearSince[appID]
}

func (s *victoriaStack) Interrupt() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, component := range s.components {
		if component.external || component.cmd == nil {
			continue
		}
		if err := interruptProcessTree(component.cmd); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *victoriaStack) WaitOrKill(grace time.Duration) error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, component := range s.components {
		if component.external || component.cmd == nil {
			continue
		}
		if err := waitOrKillVictoriaComponent(component, grace); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", component.spec.DisplayName, err))
		}
	}
	return errors.Join(errs...)
}

func waitOrKillVictoriaComponent(component *victoriaComponent, grace time.Duration) error {
	select {
	case err := <-component.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		_ = killProcessTree(component.cmd)
		select {
		case err := <-component.done:
			if err == nil || isExpectedExit(err) {
				return nil
			}
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("process did not exit after SIGKILL")
		}
	}
}

func warnVictoria(console *runConsole, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if console != nil && console.verbose {
		console.Event("victoria.warn", map[string]any{"message": msg})
		if !console.json {
			fmt.Fprintf(os.Stderr, "scenery: %s\n", msg)
		}
	}
}

func (s *devSupervisor) ensureSharedVictoriaStack(ctx context.Context, root string) (*victoriaStack, bool, error) {
	console := (*runConsole)(nil)
	if s != nil {
		console = s.console
	}
	if s == nil || s.agent == nil {
		return startVictoriaStackWithRoot(ctx, root, console), false, nil
	}
	processUnlock := lockVictoriaSubstrateProcess(root)
	defer processUnlock()
	unlock, err := lockManagedSubstrateRoot(root, localagent.SubstrateVictoria)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	var existing *localagent.Substrate
	if substrate, err := s.agent.GetSubstrate(ctx, localagent.SubstrateVictoria); err == nil {
		stack, reusable := reusableVictoriaStack(substrate)
		if reusable {
			emitVictoriaSubstrateEvent(s.eventSink(), ctx, "running", "shared Victoria stack reused", map[string]any{
				"owner":     "agent",
				"endpoints": substrate.Endpoints,
			})
			return stack, true, nil
		}
		existing = &substrate
	} else if !localagent.IsNotFound(err) {
		return nil, false, err
	}
	if existing != nil {
		if err := stopVerifiedVictoriaStack(ctx, *existing); err != nil {
			return nil, false, err
		}
		if _, err := s.agent.DeleteSubstrate(ctx, localagent.SubstrateVictoria); err != nil {
			return nil, false, err
		}
	}
	stack := startVictoriaStackWithRoot(ctx, root, console)
	if stack == nil {
		return nil, false, nil
	}
	if !fullyManagedVictoriaStack(stack) {
		discardVictoriaStack(stack)
		return nil, false, fmt.Errorf("shared Victoria stack did not start all components")
	}
	req := stack.SubstrateRequest(os.Getpid())
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = localagent.SubstrateVictoria
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = "ready"
	}
	if _, err := s.agent.UpsertSubstrate(ctx, req); err != nil {
		discardVictoriaStack(stack)
		return nil, false, err
	}
	stack.MarkExternal()
	emitVictoriaSubstrateEvent(s.eventSink(), ctx, "running", "shared Victoria stack ready", map[string]any{
		"owner":     "agent",
		"endpoints": req.Endpoints,
	})
	return stack, false, nil
}

func stopVerifiedVictoriaStack(ctx context.Context, substrate localagent.Substrate) error {
	live := make([]int, 0, len(substrate.PIDs))
	seen := map[int]bool{}
	for name, pid := range substrate.PIDs {
		if pid <= 0 || !processAliveForEdge(pid) {
			continue
		}
		owner := substrate.Owners[name]
		if owner.PID != pid {
			return fmt.Errorf("Victoria component %s process %d has no matching owner", name, pid)
		}
		if err := localagent.VerifyOwner(owner); err != nil {
			return fmt.Errorf("Victoria component %s process %d owner cannot be verified: %w", name, pid, err)
		}
		if !seen[pid] {
			live = append(live, pid)
		}
		seen[pid] = true
	}
	ownerPID := firstPositiveInt(substrate.Owner.PID, substrate.OwnerPID)
	if ownerPID > 0 && !seen[ownerPID] && processAliveForEdge(ownerPID) {
		return fmt.Errorf("Victoria stack owner process %d is not a registered component", ownerPID)
	}
	for _, pid := range live {
		if err := signalPID(pid, os.Interrupt); err != nil {
			return err
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		stopped := true
		for _, pid := range live {
			stopped = stopped && !processAliveForEdge(pid)
		}
		for _, spec := range victoriaComponentSpecs() {
			stopped = stopped && tcpAddrAvailable(victoriaDefaultHost, spec.DefaultPort)
		}
		if stopped {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for the previous Victoria stack to stop")
		case <-ticker.C:
		}
	}
}

func fullyManagedVictoriaStack(stack *victoriaStack) bool {
	if stack == nil || len(stack.components) != len(victoriaComponentSpecs()) || !stack.Reachable() {
		return false
	}
	for _, component := range stack.components {
		if component == nil || component.external || component.cmd == nil || component.cmd.Process == nil {
			return false
		}
	}
	return true
}

func discardVictoriaStack(stack *victoriaStack) {
	_ = stack.Interrupt()
	_ = stack.WaitOrKill(time.Second)
}

func lockVictoriaSubstrateProcess(root string) func() {
	keyRoot := strings.TrimSpace(root)
	if keyRoot == "" {
		keyRoot = os.TempDir()
	}
	if abs, err := filepath.Abs(keyRoot); err == nil {
		keyRoot = abs
	}
	key := filepath.Clean(keyRoot) + "\x00" + localagent.SubstrateVictoria
	value, _ := victoriaSubstrateProcessLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func reusableVictoriaStack(substrate localagent.Substrate) (*victoriaStack, bool) {
	if strings.TrimSpace(substrate.Status) != "" && strings.TrimSpace(substrate.Status) != "ready" {
		return nil, false
	}
	if err := verifySubstrateOwner(substrate); err != nil {
		return nil, false
	}
	stack := victoriaStackFromSubstrate(substrate)
	if stack == nil || !stack.Reachable() {
		return nil, false
	}
	stack.MarkExternal()
	return stack, true
}

func (s *devSupervisor) startVictoriaRecoveryMonitor() {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		s.reportVictoriaRecoveryFailure("", fmt.Errorf("recovery monitor unavailable: %w", err), 0)
		return
	}
	s.monitorVictoriaRecovery(filepath.Join(paths.AgentDir, "victoria"), victoriaHealthCheckInterval, victoriaRecoveryBackoffMax)
}

func (s *devSupervisor) monitorVictoriaRecovery(root string, interval, maxBackoff time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if s == nil || s.agent == nil || s.ctx == nil {
		close(done)
		return done
	}
	if interval <= 0 {
		interval = victoriaHealthCheckInterval
	}
	if maxBackoff < interval {
		maxBackoff = interval
	}
	go func() {
		defer close(done)
		delay := interval
		for {
			timer := time.NewTimer(delay)
			select {
			case <-s.ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}

			s.mu.RLock()
			current := s.victoria
			s.mu.RUnlock()
			if current != nil && current.Reachable() {
				delay = interval
				continue
			}

			stack, reused, err := s.ensureSharedVictoriaStack(s.ctx, root)
			if err != nil || stack == nil || !stack.Reachable() {
				if err == nil {
					err = errors.New("shared Victoria stack remains unavailable")
				}
				delay *= 2
				if delay > maxBackoff {
					delay = maxBackoff
				}
				s.reportVictoriaRecoveryFailure(root, err, delay)
				continue
			}

			s.mu.Lock()
			if s.ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			s.victoria = stack
			s.mu.Unlock()
			monitorVictoriaSubstrate(root, s.agent, s.eventSink(), stack)
			emitVictoriaSubstrateEvent(s.eventSink(), s.ctx, "running", "shared Victoria stack recovered", map[string]any{
				"reused":    reused,
				"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
			})
			delay = interval
		}
	}()
	return done
}

func (s *devSupervisor) reportVictoriaRecoveryFailure(root string, recoveryErr error, retryAfter time.Duration) {
	if s == nil || recoveryErr == nil {
		return
	}
	message := fmt.Sprintf("Victoria observability recovery failed: %v", recoveryErr)
	if retryAfter > 0 {
		message = fmt.Sprintf("Victoria observability recovery failed; retrying in %s: %v", formatDuration(retryAfter), recoveryErr)
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.eventSink().Output(ctx, devdash.DevSource{
		ID:     "victoria",
		Kind:   "substrate",
		Name:   "Victoria stack",
		Role:   "observability",
		Stream: "stderr",
		Status: "degraded",
		Reason: recoveryErr.Error(),
	}, []byte("ERR "+message+"\n"))
	if s.console != nil && !s.console.json {
		s.console.printf(s.console.err, "%s\n", s.console.palette.Bold(s.console.palette.Red("ERR "+message)))
	}
	if strings.TrimSpace(root) != "" {
		s.markVictoriaSubstrateDegraded(ctx, root)
	}
}

func (s *devSupervisor) markVictoriaSubstrateDegraded(ctx context.Context, root string) {
	if s == nil || s.agent == nil {
		return
	}
	processUnlock := lockVictoriaSubstrateProcess(root)
	defer processUnlock()
	unlock, err := lockManagedSubstrateRoot(root, localagent.SubstrateVictoria)
	if err != nil {
		return
	}
	defer unlock()
	current, err := s.agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return
	}
	if _, reusable := reusableVictoriaStack(current); reusable {
		return
	}
	_, _ = s.agent.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:           current.Kind,
		Status:         "degraded",
		OwnerPID:       current.OwnerPID,
		Owner:          current.Owner,
		PIDs:           current.PIDs,
		Owners:         current.Owners,
		URLs:           current.URLs,
		Endpoints:      current.Endpoints,
		Leases:         current.Leases,
		LastExit:       current.LastExit,
		ComponentExits: current.ComponentExits,
	})
}

func monitorVictoriaSubstrate(root string, agent *localagent.Client, events *devEventSink, stack *victoriaStack) <-chan struct{} {
	done := make(chan struct{})
	if agent == nil || stack == nil {
		close(done)
		return done
	}
	if len(stack.components) == 0 {
		close(done)
		return done
	}
	var wg sync.WaitGroup
	for _, component := range stack.components {
		if component == nil || component.done == nil {
			continue
		}
		wg.Add(1)
		go func(component *victoriaComponent) {
			defer wg.Done()
			err, ok := <-component.done
			if !ok {
				return
			}
			exit := component.ExitRecord(err)
			processUnlock := lockVictoriaSubstrateProcess(root)
			if unlock, lockErr := lockManagedSubstrateRoot(root, localagent.SubstrateVictoria); lockErr == nil {
				if current, getErr := agent.GetSubstrate(context.Background(), localagent.SubstrateVictoria); getErr == nil && current.PIDs[component.spec.Name] == exit.PID {
					req := localagent.UpsertSubstrateRequest{
						Kind:      current.Kind,
						Status:    "degraded",
						OwnerPID:  current.OwnerPID,
						Owner:     current.Owner,
						PIDs:      current.PIDs,
						Owners:    current.Owners,
						URLs:      current.URLs,
						Endpoints: current.Endpoints,
						Leases:    current.Leases,
						LastExit:  &exit,
						ComponentExits: map[string]localagent.SubstrateExit{
							component.spec.Name: exit,
						},
					}
					_, _ = agent.UpsertSubstrate(context.Background(), req)
				}
				unlock()
			}
			processUnlock()
			emitVictoriaSubstrateEvent(events, context.Background(), "degraded", component.spec.DisplayName+" exited", substrateExitEventFields(exit), devdash.DevSource{
				ID:     "victoria." + component.spec.Name,
				Kind:   "substrate",
				Name:   component.spec.DisplayName,
				Role:   "observability",
				Status: "degraded",
				URL:    component.baseURL,
			})
		}(component)
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func emitVictoriaSubstrateEvent(events *devEventSink, ctx context.Context, status, message string, fields map[string]any, sourceOverride ...devdash.DevSource) {
	if events == nil {
		return
	}
	source := devdash.DevSource{
		ID:     "victoria",
		Kind:   "substrate",
		Name:   "Victoria stack",
		Role:   "observability",
		Status: status,
	}
	if len(sourceOverride) > 0 {
		source = sourceOverride[0]
	}
	events.Emit(ctx, source, levelForSubstrateStatus(status), message, fields)
}

func levelForSubstrateStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "degraded", "exited", "unavailable":
		return "error"
	default:
		return "info"
	}
}

func urlAcceptsTCP(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
