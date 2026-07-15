// Package victoria manages the local VictoriaMetrics/VictoriaLogs/VictoriaTraces
// observability substrate: pinned component specs, managed binary resolution,
// component process lifecycle, readiness probing, and agent substrate payloads.
//
// The package owns Victoria as substrate detail. Supervisor coordination —
// shared substrate locking, recovery monitoring, and dev event emission —
// stays with the caller in cmd/scenery.
package victoria

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
	"scenery.sh/internal/devtools"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/netprobe"
)

const defaultHost = "127.0.0.1"

// Console receives verbose diagnostics from Victoria lifecycle operations.
// A nil Console silences all output.
type Console interface {
	Verbose() bool
	JSON() bool
	Event(event string, fields map[string]any)
}

// ComponentSpec pins one Victoria component: identity, managed toolchain
// artifact, default port, endpoint paths, and environment variable names.
type ComponentSpec struct {
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

// Component is one running (or externally reused) Victoria process.
type Component struct {
	spec        ComponentSpec
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

// Name returns the component name ("metrics", "logs", "traces").
func (c *Component) Name() string { return c.spec.Name }

// DisplayName returns the human-readable component name.
func (c *Component) DisplayName() string { return c.spec.DisplayName }

// BaseURL returns the component base URL.
func (c *Component) BaseURL() string { return c.baseURL }

// External reports whether the component process is managed outside this stack.
func (c *Component) External() bool { return c.external }

// Done returns the channel that receives the managed process exit result.
// It is nil for external components.
func (c *Component) Done() <-chan error {
	if c == nil {
		return nil
	}
	return c.done
}

type componentStartPlan struct {
	spec        ComponentSpec
	binaryPath  string
	baseURL     string
	endpointURL string
	storagePath string
	external    bool
}

type componentStartResult struct {
	index     int
	spec      ComponentSpec
	component *Component
	err       error
}

// Stack is the set of Victoria components serving one shared substrate.
type Stack struct {
	components []*Component
	mu         sync.RWMutex
	clearSince map[string]time.Time
}

// Components returns the stack's components.
func (s *Stack) Components() []*Component {
	if s == nil {
		return nil
	}
	return s.components
}

// ExternalComponent describes an externally supplied Victoria component
// endpoint used to assemble a Stack without starting processes.
type ExternalComponent struct {
	Name        string
	DisplayName string
	BaseURL     string
	EndpointURL string
	StdoutLog   string
	StderrLog   string
	StartedAt   time.Time
	Done        chan error
}

// NewStack assembles a stack from externally supplied component endpoints.
func NewStack(components ...ExternalComponent) *Stack {
	stack := &Stack{}
	for _, component := range components {
		stack.components = append(stack.components, &Component{
			spec:        ComponentSpec{Name: component.Name, DisplayName: component.DisplayName},
			baseURL:     component.BaseURL,
			endpointURL: component.EndpointURL,
			stdoutLog:   component.StdoutLog,
			stderrLog:   component.StderrLog,
			startedAt:   component.StartedAt,
			done:        component.Done,
			external:    true,
		})
	}
	return stack
}

var toolchainSyncMu sync.Mutex

// Start starts the Victoria stack for an app root using the default
// app-local state directory. It returns nil when Victoria is disabled or no
// component became available.
func Start(ctx context.Context, appRoot string, console Console) *Stack {
	return StartAtRoot(ctx, rootDir(appRoot), console)
}

// StartAtRoot starts the Victoria stack with binaries and storage under root.
func StartAtRoot(ctx context.Context, root string, console Console) *Stack {
	if !Enabled() {
		return nil
	}
	binDir := filepath.Join(root, "bin")
	download := downloadEnabled()
	if err := ensureLocalStateDirIgnored(root); err != nil {
		Warn(console, "Victoria local state unavailable: %v", err)
		return nil
	}

	results := startComponents(ctx, root, binDir, ComponentSpecs(), download, console)
	stack := &Stack{components: make([]*Component, 0, len(results))}
	for _, result := range results {
		if result.err != nil {
			Warn(console, "%s unavailable: %v", result.spec.DisplayName, result.err)
			continue
		}
		stack.components = append(stack.components, result.component)
	}
	if len(stack.components) == 0 {
		return nil
	}
	return stack
}

// FromSubstrate rebuilds an external stack from a registered agent substrate.
// It returns nil unless every component has both a base URL and an endpoint.
func FromSubstrate(substrate localagent.Substrate) *Stack {
	if substrate.Kind != localagent.SubstrateVictoria || len(substrate.Endpoints) == 0 {
		return nil
	}
	stack := &Stack{}
	specs := ComponentSpecs()
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
		stack.components = append(stack.components, &Component{
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

// SubstrateRequest builds the agent upsert payload describing this stack.
func (s *Stack) SubstrateRequest(ownerPID int) localagent.UpsertSubstrateRequest {
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

// MarkExternal flags every component as externally managed so Interrupt and
// WaitOrKill leave the processes alone.
func (s *Stack) MarkExternal() {
	if s == nil {
		return
	}
	for _, component := range s.components {
		if component != nil {
			component.external = true
		}
	}
}

// ExitRecord builds the substrate exit record for a finished component process.
func (c *Component) ExitRecord(err error) localagent.SubstrateExit {
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

// Reachable reports whether every component accepts TCP connections.
func (s *Stack) Reachable() bool {
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

// FullyManaged reports whether this stack runs every declared component as a
// reachable process owned by this stack.
func (s *Stack) FullyManaged() bool {
	if s == nil || len(s.components) != len(ComponentSpecs()) || !s.Reachable() {
		return false
	}
	for _, component := range s.components {
		if component == nil || component.external || component.cmd == nil || component.cmd.Process == nil {
			return false
		}
	}
	return true
}

// Enabled reports whether the Victoria substrate is enabled
// (SCENERY_DEV_VICTORIA, default on).
func Enabled() bool {
	value, ok := envpolicy.Lookup("SCENERY_DEV_VICTORIA")
	if !ok {
		return true
	}
	return !isFalseEnv(value)
}

func downloadEnabled() bool {
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

func rootDir(appRoot string) string {
	if value := strings.TrimSpace(envpolicy.Get("SCENERY_DEV_VICTORIA_DIR")); value != "" {
		return value
	}
	return filepath.Join(appRoot, ".scenery", "victoria")
}

// ComponentSpecs returns the pinned Victoria component set with environment
// overrides applied.
func ComponentSpecs() []ComponentSpec {
	versions := devtools.PinnedVersions()
	return []ComponentSpec{
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

// ComponentPortsAvailable reports whether every default component port is
// free, meaning no Victoria component is still listening.
func ComponentPortsAvailable() bool {
	for _, spec := range ComponentSpecs() {
		if !tcpAddrAvailable(defaultHost, spec.DefaultPort) {
			return false
		}
	}
	return true
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

func startComponent(ctx context.Context, root, binDir string, spec ComponentSpec, download bool, console Console) (*Component, error) {
	plan, err := prepareComponentStart(ctx, root, binDir, spec, download, console)
	if err != nil {
		return nil, err
	}
	return startComponentFromPlan(ctx, root, plan, console)
}

func startComponents(ctx context.Context, root, binDir string, specs []ComponentSpec, download bool, console Console) []componentStartResult {
	if len(specs) == 0 {
		return nil
	}
	plans := make([]componentStartPlan, len(specs))
	results := make([]componentStartResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		results[i] = componentStartResult{index: i, spec: spec}
		wg.Add(1)
		go func(index int, spec ComponentSpec) {
			defer wg.Done()
			plan, err := prepareComponentStart(ctx, root, binDir, spec, download, console)
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
			component, err := startComponentFromPlan(ctx, root, plans[index], console)
			results[index].component = component
			results[index].err = err
		}(i)
	}
	wg.Wait()
	return results
}

func prepareComponentStart(ctx context.Context, root, binDir string, spec ComponentSpec, download bool, console Console) (componentStartPlan, error) {
	baseURL := fmt.Sprintf("http://%s:%d", defaultHost, spec.DefaultPort)
	plan := componentStartPlan{
		spec:        spec,
		baseURL:     baseURL,
		endpointURL: baseURL + spec.EndpointPath,
		storagePath: filepath.Join(root, spec.StorageDir),
	}
	if !tcpAddrAvailable(defaultHost, spec.DefaultPort) {
		plan.external = true
		Warn(console, "%s appears to be already running at %s; reusing it", spec.DisplayName, baseURL)
		return plan, nil
	}

	binaryPath, err := resolveBinary(ctx, spec, binDir, download)
	if err != nil {
		return componentStartPlan{}, err
	}
	plan.binaryPath = binaryPath
	return plan, nil
}

func startComponentFromPlan(ctx context.Context, root string, plan componentStartPlan, console Console) (*Component, error) {
	component := &Component{
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
		"-httpListenAddr="+net.JoinHostPort(defaultHost, strconv.Itoa(component.spec.DefaultPort)),
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
	if err := waitForComponentReady(ctx, component, 5*time.Second); err != nil {
		_ = interruptProcessTree(cmd)
		_ = waitOrKillComponent(component, time.Second)
		return nil, err
	}
	if console != nil && console.Verbose() {
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

func waitForComponentReady(ctx context.Context, component *Component, timeout time.Duration) error {
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

func resolveBinary(ctx context.Context, spec ComponentSpec, binDir string, download bool) (string, error) {
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
	toolchainSyncMu.Lock()
	defer toolchainSyncMu.Unlock()
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

func isLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func archiveName(spec ComponentSpec) (string, error) {
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
	return netprobe.BindFree(net.JoinHostPort(host, strconv.Itoa(port))) == nil
}

// Env returns the child process environment advertising this stack's OTLP
// endpoints and Scenery observability URLs.
func (s *Stack) Env() []string {
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

// URLs returns per-component browser URLs.
func (s *Stack) URLs() map[string]string {
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

// Endpoint returns the ingest endpoint URL for the named component.
func (s *Stack) Endpoint(name string) string {
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

// MarkCleared records the moment an app's observability data was cleared so
// queries can filter out older rows.
func (s *Stack) MarkCleared(appID string, at time.Time) {
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

// ClearedAt returns the last recorded clear time for an app.
func (s *Stack) ClearedAt(appID string) time.Time {
	if s == nil || appID == "" {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clearSince[appID]
}

// Interrupt signals every stack-managed component process tree.
func (s *Stack) Interrupt() error {
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

// WaitOrKill waits for every stack-managed component to exit within the grace
// period, escalating to SIGKILL.
func (s *Stack) WaitOrKill(grace time.Duration) error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, component := range s.components {
		if component.external || component.cmd == nil {
			continue
		}
		if err := waitOrKillComponent(component, grace); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", component.spec.DisplayName, err))
		}
	}
	return errors.Join(errs...)
}

func waitOrKillComponent(component *Component, grace time.Duration) error {
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

func isExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	_, ok := errors.AsType[*exec.ExitError](err)
	return ok
}

// Warn reports a Victoria warning through the console when verbose output is
// enabled, mirroring it to stderr for human sessions.
func Warn(console Console, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if console != nil && console.Verbose() {
		console.Event("victoria.warn", map[string]any{"message": msg})
		if !console.JSON() {
			fmt.Fprintf(os.Stderr, "scenery: %s\n", msg)
		}
	}
}

func urlAcceptsTCP(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return false
	}
	return netprobe.DialReachable(parsed.Host, 200*time.Millisecond)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
