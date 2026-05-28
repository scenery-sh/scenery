package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/devtools"
)

const victoriaDefaultHost = "127.0.0.1"

type victoriaComponentSpec struct {
	Name              string
	DisplayName       string
	Repo              string
	ArchiveSlug       string
	BinaryName        string
	ExtraBinaries     []string
	Version           string
	DefaultPort       int
	EndpointPath      string
	URLPath           string
	StorageDir        string
	EnvPrefix         string
	OTELVar           string
	OnlavaURLVar      string
	OnlavaEndpointVar string
}

type victoriaComponent struct {
	spec        victoriaComponentSpec
	binaryPath  string
	baseURL     string
	endpointURL string
	storagePath string
	cmd         *exec.Cmd
	done        chan error
	external    bool
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
	value, ok := os.LookupEnv("ONLAVA_DEV_VICTORIA")
	if !ok {
		return true
	}
	return !isFalseEnv(value)
}

func victoriaDownloadEnabled() bool {
	value, ok := os.LookupEnv("ONLAVA_DEV_VICTORIA_DOWNLOAD")
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
	if value := strings.TrimSpace(os.Getenv("ONLAVA_DEV_VICTORIA_DIR")); value != "" {
		return value
	}
	return filepath.Join(appRoot, ".onlava", "victoria")
}

func victoriaComponentSpecs() []victoriaComponentSpec {
	versions := devtools.PinnedVersions()
	return []victoriaComponentSpec{
		{
			Name:              "metrics",
			DisplayName:       "VictoriaMetrics",
			Repo:              "VictoriaMetrics",
			ArchiveSlug:       "victoria-metrics",
			BinaryName:        "victoria-metrics-prod",
			ExtraBinaries:     []string{"victoria-metrics"},
			Version:           envOrDefault("ONLAVA_VICTORIA_METRICS_VERSION", versions.Victoria.Metrics.Version),
			DefaultPort:       intEnvOrDefault("ONLAVA_VICTORIA_METRICS_PORT", 8428),
			EndpointPath:      "/opentelemetry/v1/metrics",
			URLPath:           "/vmui",
			StorageDir:        "metrics-data",
			EnvPrefix:         "ONLAVA_VICTORIA_METRICS",
			OTELVar:           "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
			OnlavaURLVar:      "ONLAVA_VICTORIA_METRICS_URL",
			OnlavaEndpointVar: "ONLAVA_VICTORIA_METRICS_ENDPOINT",
		},
		{
			Name:              "logs",
			DisplayName:       "VictoriaLogs",
			Repo:              "VictoriaLogs",
			ArchiveSlug:       "victoria-logs",
			BinaryName:        "victoria-logs-prod",
			ExtraBinaries:     []string{"victoria-logs"},
			Version:           envOrDefault("ONLAVA_VICTORIA_LOGS_VERSION", versions.Victoria.Logs.Version),
			DefaultPort:       intEnvOrDefault("ONLAVA_VICTORIA_LOGS_PORT", 9428),
			EndpointPath:      "/insert/opentelemetry/v1/logs",
			URLPath:           "/select/vmui",
			StorageDir:        "logs-data",
			EnvPrefix:         "ONLAVA_VICTORIA_LOGS",
			OTELVar:           "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
			OnlavaURLVar:      "ONLAVA_VICTORIA_LOGS_URL",
			OnlavaEndpointVar: "ONLAVA_VICTORIA_LOGS_ENDPOINT",
		},
		{
			Name:              "traces",
			DisplayName:       "VictoriaTraces",
			Repo:              "VictoriaTraces",
			ArchiveSlug:       "victoria-traces",
			BinaryName:        "victoria-traces-prod",
			ExtraBinaries:     []string{"victoria-traces"},
			Version:           envOrDefault("ONLAVA_VICTORIA_TRACES_VERSION", versions.Victoria.Traces.Version),
			DefaultPort:       intEnvOrDefault("ONLAVA_VICTORIA_TRACES_PORT", 10428),
			EndpointPath:      "/insert/opentelemetry/v1/traces",
			URLPath:           "/select/vmui",
			StorageDir:        "traces-data",
			EnvPrefix:         "ONLAVA_VICTORIA_TRACES",
			OTELVar:           "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
			OnlavaURLVar:      "ONLAVA_VICTORIA_TRACES_URL",
			OnlavaEndpointVar: "ONLAVA_VICTORIA_TRACES_ENDPOINT",
		},
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intEnvOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
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
	output := io.Writer(io.Discard)
	if console != nil && console.verbose {
		output = os.Stderr
	}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	component.cmd = cmd
	component.done = make(chan error, 1)
	go func() {
		component.done <- cmd.Wait()
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
	if path := strings.TrimSpace(os.Getenv(spec.EnvPrefix + "_BIN")); path != "" {
		if isExecutableFile(path) {
			return path, nil
		}
		return "", fmt.Errorf("%s_BIN points to a non-executable file: %s", spec.EnvPrefix, path)
	}
	for _, name := range append([]string{spec.BinaryName}, spec.ExtraBinaries...) {
		if path := filepath.Join(binDir, name); isExecutableFile(path) {
			return path, nil
		}
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if !download {
		return "", fmt.Errorf("binary not found in PATH or %s; set %s_BIN or enable download", binDir, spec.EnvPrefix)
	}
	return downloadVictoriaBinary(ctx, spec, binDir)
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func downloadVictoriaBinary(ctx context.Context, spec victoriaComponentSpec, binDir string) (string, error) {
	archiveName, err := victoriaArchiveName(spec)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://github.com/VictoriaMetrics/%s/releases/download/%s/%s", spec.Repo, spec.Version, archiveName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return "", err
	}
	if err := verifyVictoriaArchiveChecksum(ctx, spec, archiveName, data); err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	target := filepath.Join(binDir, spec.BinaryName)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if header.FileInfo().IsDir() || filepath.Base(header.Name) != spec.BinaryName {
			continue
		}
		tmp := target + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(tmp)
			return "", copyErr
		}
		if closeErr != nil {
			_ = os.Remove(tmp)
			return "", closeErr
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("archive %s did not contain %s", archiveName, spec.BinaryName)
}

func verifyVictoriaArchiveChecksum(ctx context.Context, spec victoriaComponentSpec, archiveName string, data []byte) error {
	checksumName := strings.TrimSuffix(archiveName, ".tar.gz") + "_checksums.txt"
	checksumURL := fmt.Sprintf("https://github.com/VictoriaMetrics/%s/releases/download/%s/%s", spec.Repo, spec.Version, checksumName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", checksumURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	want := checksumForArchive(string(body), archiveName)
	if want == "" {
		return fmt.Errorf("%s does not contain checksum for %s", checksumName, archiveName)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s", archiveName)
	}
	return nil
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
	env := []string{"ONLAVA_DEV_OBSERVABILITY_BACKEND=victoria"}
	for _, component := range s.components {
		env = append(env,
			component.spec.OTELVar+"="+component.endpointURL,
			component.spec.OnlavaURLVar+"="+component.baseURL,
			component.spec.OnlavaEndpointVar+"="+component.endpointURL,
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
			fmt.Fprintf(os.Stderr, "onlava: %s\n", msg)
		}
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
