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

type victoriaStack struct {
	components []*victoriaComponent
	mu         sync.RWMutex
	clearSince map[string]time.Time
}

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

	stack := &victoriaStack{}
	for _, spec := range victoriaComponentSpecs() {
		component, err := startVictoriaComponent(ctx, root, binDir, spec, download, console)
		if err != nil {
			warnVictoria(console, "%s unavailable: %v", spec.DisplayName, err)
			continue
		}
		stack.components = append(stack.components, component)
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

func (s *victoriaStack) Components() []managedSubstrateComponent {
	if s == nil {
		return nil
	}
	var components []managedSubstrateComponent
	for _, component := range s.components {
		if component == nil || component.done == nil {
			continue
		}
		component := component
		components = append(components, managedSubstrateComponent{
			Name:        component.spec.Name,
			DisplayName: component.spec.DisplayName,
			Role:        "observability",
			URL:         component.baseURL,
			Done:        component.done,
			ExitRecord:  component.ExitRecord,
		})
	}
	return components
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
	for _, component := range s.components {
		if component == nil || !urlAcceptsTCP(component.baseURL) {
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
	baseURL := fmt.Sprintf("http://%s:%d", victoriaDefaultHost, spec.DefaultPort)
	component := &victoriaComponent{
		spec:        spec,
		baseURL:     baseURL,
		endpointURL: baseURL + spec.EndpointPath,
		storagePath: filepath.Join(root, spec.StorageDir),
	}
	if !tcpAddrAvailable(victoriaDefaultHost, spec.DefaultPort) {
		component.external = true
		warnVictoria(console, "%s appears to be already running at %s; reusing it", spec.DisplayName, baseURL)
		return component, nil
	}

	binaryPath, err := resolveVictoriaBinary(ctx, spec, binDir, download)
	if err != nil {
		return nil, err
	}
	component.binaryPath = binaryPath

	if err := os.MkdirAll(component.storagePath, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binaryPath,
		"-httpListenAddr="+net.JoinHostPort(victoriaDefaultHost, strconv.Itoa(spec.DefaultPort)),
		"-storageDataPath="+component.storagePath,
	)
	configureChildProcess(cmd)
	configureCommandCancellation(cmd, 5*time.Second)
	cmd.Dir = root
	logs, err := openSubstrateLogWriters(root, localagent.SubstrateVictoria, spec.Name, console)
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
			"component":    spec.Name,
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

type victoriaSubstrateAdapter struct {
	console *runConsole
}

func (a victoriaSubstrateAdapter) Kind() string       { return localagent.SubstrateVictoria }
func (a victoriaSubstrateAdapter) SourceID() string   { return "victoria" }
func (a victoriaSubstrateAdapter) SourceName() string { return "Victoria stack" }
func (a victoriaSubstrateAdapter) Role() string       { return "observability" }

func (a victoriaSubstrateAdapter) Start(_ context.Context, root string) (managedSubstrateHandle, error) {
	return startVictoriaStackWithRoot(context.Background(), root, a.console), nil
}

func (a victoriaSubstrateAdapter) FromSubstrate(_ context.Context, substrate localagent.Substrate) (managedSubstrateHandle, bool) {
	stack := victoriaStackFromSubstrate(substrate)
	if stack == nil || !stack.Reachable() {
		return nil, false
	}
	return stack, true
}

func (a victoriaSubstrateAdapter) ReadyFields(handle managedSubstrateHandle) map[string]any {
	stack, _ := handle.(*victoriaStack)
	if stack == nil {
		return nil
	}
	return map[string]any{
		"owner":     "agent",
		"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
	}
}

func (a victoriaSubstrateAdapter) ReuseFields(handle managedSubstrateHandle, substrate localagent.Substrate) map[string]any {
	fields := a.ReadyFields(handle)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["endpoints"] = substrate.Endpoints
	return fields
}

func (a victoriaSubstrateAdapter) ExitStatus(managedSubstrateComponent) string {
	return "degraded"
}

func (a victoriaSubstrateAdapter) ExitMessage(component managedSubstrateComponent) string {
	return component.DisplayName + " exited"
}

func (a victoriaSubstrateAdapter) EventSource(_ managedSubstrateHandle, component managedSubstrateComponent, status string) devdash.DevSource {
	return devdash.DevSource{
		ID:     "victoria." + component.Name,
		Kind:   "substrate",
		Name:   component.DisplayName,
		Role:   "observability",
		Status: status,
		URL:    component.URL,
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
