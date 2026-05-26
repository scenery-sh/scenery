package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	"time"

	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/devtools"
)

const (
	grafanaDefaultHost      = "127.0.0.1"
	grafanaDefaultPort      = 10429
	grafanaHealthPath       = "/api/health"
	grafanaReadyTimeout     = 3 * time.Minute
	grafanaMetricsUID       = "onlava-victoriametrics"
	grafanaLogsUID          = "onlava-victorialogs"
	grafanaTracesUID        = "onlava-victoriatraces-jaeger"
	grafanaOverviewUID      = "onlava-dev-overview"
	grafanaLogsDashboardUID = "onlava-dev-logs"
	grafanaEndpointUID      = "onlava-dev-endpoint"
)

type grafanaMode string

const (
	grafanaModeAuto     grafanaMode = "auto"
	grafanaModeRequired grafanaMode = "required"
	grafanaModeDisabled grafanaMode = "disabled"
)

type grafanaConfig struct {
	Enabled           bool
	Required          bool
	Download          bool
	Version           string
	BinPath           string
	HomePath          string
	RootDir           string
	Port              int
	URL               string
	PublicURL         string
	ConfigPath        string
	DataDir           string
	LogsDir           string
	PluginsDir        string
	ProvisioningDir   string
	DashboardsDir     string
	MetricsURL        string
	LogsURL           string
	TracesURL         string
	PluginPreinstall  string
	MetricsDatasource string
	LogsDatasource    string
	TracesDatasource  string
	OverviewDashboard string
	LogsDashboard     string
	EndpointDashboard string
}

type grafanaComponent struct {
	cfg      grafanaConfig
	cmd      *exec.Cmd
	done     chan error
	external bool
	state    devdash.GrafanaState
}

func startGrafanaForDev(ctx context.Context, appRoot string, victoria *victoriaStack, publicURL string, console *runConsole) (*grafanaComponent, error) {
	cfg := newGrafanaConfig(appRoot, victoria, publicURL)
	if !cfg.Enabled {
		return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "disabled", "Grafana disabled by ONLAVA_DEV_GRAFANA=0")}, nil
	}
	if err := ensureLocalStateDirIgnored(cfg.RootDir); err != nil {
		msg := fmt.Sprintf("could not write Grafana local ignore marker: %v", err)
		if cfg.Required {
			return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "unavailable", msg)}, err
		}
		warnGrafana(console, "%s", msg)
		return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "degraded", msg)}, nil
	}
	if cfg.MetricsURL == "" && cfg.LogsURL == "" && cfg.TracesURL == "" {
		msg := "Victoria sidecars are disabled or unavailable; Grafana provisioning has no local datasources"
		if cfg.Required {
			return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "degraded", msg)}, errors.New(msg)
		}
		warnGrafana(console, "%s", msg)
		return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "degraded", msg)}, nil
	}

	if external, ok := grafanaExternalOnPort(ctx, cfg); ok {
		if explicitGrafanaReuseExternal() {
			if err := verifyGrafanaAssets(ctx, cfg); err != nil {
				msg := fmt.Sprintf("Grafana is running on the configured port, but onlava provisioning is not verified: %v", err)
				if cfg.Required {
					return &grafanaComponent{cfg: cfg, external: true, state: grafanaState(cfg, "degraded", msg)}, errors.New(msg)
				}
				warnGrafana(console, "%s", msg)
				return &grafanaComponent{cfg: cfg, external: true, state: grafanaState(cfg, "degraded", msg)}, nil
			}
			external.state = grafanaState(cfg, "external", "Using verified external Grafana with onlava datasources and dashboards")
			return external, nil
		}
		if explicitGrafanaPort() {
			msg := "Grafana is already running on the configured port; set ONLAVA_GRAFANA_REUSE_EXTERNAL=1 to verify and reuse it"
			if cfg.Required {
				return &grafanaComponent{cfg: cfg, external: true, state: grafanaState(cfg, "unavailable", msg)}, errors.New(msg)
			}
			warnGrafana(console, "%s", msg)
			return &grafanaComponent{cfg: cfg, external: true, state: grafanaState(cfg, "degraded", msg)}, nil
		}
		port, err := freeLoopbackPort()
		if err != nil {
			msg := fmt.Sprintf("Grafana is already running on the default port and no alternate port could be chosen: %v", err)
			if cfg.Required {
				return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "unavailable", msg)}, err
			}
			warnGrafana(console, "%s", msg)
			return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "degraded", msg)}, nil
		}
		setGrafanaPort(&cfg, port)
	}

	if !tcpAddrAvailable(grafanaDefaultHost, cfg.Port) || grafanaPortResponds(ctx, cfg.URL) {
		if explicitGrafanaPort() {
			msg := fmt.Sprintf("port %d is occupied by a non-Grafana process", cfg.Port)
			if cfg.Required {
				return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "unavailable", msg)}, errors.New(msg)
			}
			warnGrafana(console, "%s", msg)
			return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "degraded", msg)}, nil
		}
		port, err := freeLoopbackPort()
		if err != nil {
			msg := fmt.Sprintf("could not choose alternate Grafana port: %v", err)
			if cfg.Required {
				return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "unavailable", msg)}, err
			}
			warnGrafana(console, "%s", msg)
			return &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "degraded", msg)}, nil
		}
		setGrafanaPort(&cfg, port)
	}

	component := &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "starting", "Grafana starting")}
	if err := writeGrafanaProvisioning(ctx, cfg); err != nil {
		component.state = grafanaState(cfg, "degraded", err.Error())
		if cfg.Required {
			return component, err
		}
		warnGrafana(console, "provisioning unavailable: %v", err)
		return component, nil
	}

	binaryPath, homePath, err := resolveGrafanaBinary(ctx, cfg)
	if err != nil {
		component.state = grafanaState(cfg, "unavailable", err.Error())
		if cfg.Required {
			return component, err
		}
		warnGrafana(console, "unavailable: %v", err)
		return component, nil
	}
	component.cfg.BinPath = binaryPath
	component.cfg.HomePath = homePath

	cmd := exec.CommandContext(ctx, binaryPath, grafanaCommandArgs(binaryPath, homePath, cfg.ConfigPath)...)
	configureChildProcess(cmd)
	configureCommandCancellation(cmd, 5*time.Second)
	cmd.Dir = cfg.RootDir
	output := io.Writer(io.Discard)
	if console != nil && console.verbose {
		output = os.Stderr
	}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.Env = grafanaChildEnv(os.Environ(), cfg)
	if err := cmd.Start(); err != nil {
		component.state = grafanaState(cfg, "unavailable", err.Error())
		if cfg.Required {
			return component, err
		}
		warnGrafana(console, "start failed: %v", err)
		return component, nil
	}
	component.cmd = cmd
	component.done = make(chan error, 1)
	go func() {
		component.done <- cmd.Wait()
		close(component.done)
	}()
	if console != nil {
		console.Event("grafana.starting", map[string]any{"url": cfg.URL, "pid": cmd.Process.Pid})
	}

	readyCtx, cancel := context.WithTimeout(ctx, grafanaReadyTimeout)
	defer cancel()
	if err := waitGrafanaReady(readyCtx, cfg); err != nil {
		component.state = grafanaState(cfg, "degraded", err.Error())
		if cfg.Required {
			return component, err
		}
		warnGrafana(console, "health check failed: %v", err)
		return component, nil
	}
	component.state = grafanaState(cfg, "ready", "")
	if console != nil {
		console.Event("grafana.ready", map[string]any{
			"url":        cfg.URL,
			"dashboards": []string{grafanaOverviewUID, grafanaLogsDashboardUID, grafanaEndpointUID},
		})
	}
	return component, nil
}

func newGrafanaConfig(appRoot string, victoria *victoriaStack, publicURL string) grafanaConfig {
	mode := grafanaDevMode()
	versions := devtools.PinnedVersions()
	root := grafanaRootDir(appRoot)
	port := intEnvOrDefault("ONLAVA_GRAFANA_PORT", grafanaDefaultPort)
	directURL := fmt.Sprintf("http://%s:%d", grafanaDefaultHost, port)
	if value := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_PUBLIC_URL")); value != "" {
		publicURL = value
	}
	if strings.TrimSpace(publicURL) == "" {
		publicURL = directURL
	}
	cfg := grafanaConfig{
		Enabled:           mode != grafanaModeDisabled,
		Required:          mode == grafanaModeRequired,
		Download:          grafanaDownloadEnabled(),
		Version:           envOrDefault("ONLAVA_GRAFANA_VERSION", versions.Grafana.Version),
		RootDir:           root,
		Port:              port,
		URL:               directURL,
		PublicURL:         strings.TrimRight(publicURL, "/"),
		ConfigPath:        filepath.Join(root, "conf", "grafana.ini"),
		DataDir:           filepath.Join(root, "data"),
		LogsDir:           filepath.Join(root, "logs"),
		PluginsDir:        filepath.Join(root, "plugins"),
		ProvisioningDir:   filepath.Join(root, "provisioning"),
		DashboardsDir:     filepath.Join(root, "dashboards"),
		MetricsURL:        victoria.BaseURL("metrics"),
		LogsURL:           victoria.BaseURL("logs"),
		TracesURL:         strings.TrimRight(victoria.BaseURL("traces"), "/") + "/select/jaeger",
		PluginPreinstall:  grafanaPluginPreinstall(),
		MetricsDatasource: grafanaMetricsUID,
		LogsDatasource:    grafanaLogsUID,
		TracesDatasource:  grafanaTracesUID,
		OverviewDashboard: grafanaOverviewUID,
		LogsDashboard:     grafanaLogsDashboardUID,
		EndpointDashboard: grafanaEndpointUID,
	}
	if victoria.BaseURL("traces") == "" {
		cfg.TracesURL = ""
	}
	return cfg
}

func setGrafanaPort(cfg *grafanaConfig, port int) {
	if cfg == nil {
		return
	}
	oldURL := cfg.URL
	cfg.Port = port
	cfg.URL = fmt.Sprintf("http://%s:%d", grafanaDefaultHost, cfg.Port)
	if cfg.PublicURL == "" || cfg.PublicURL == oldURL {
		cfg.PublicURL = cfg.URL
	}
}

func grafanaDevMode() grafanaMode {
	value, ok := os.LookupEnv("ONLAVA_DEV_GRAFANA")
	if !ok || strings.TrimSpace(value) == "" {
		return grafanaModeAuto
	}
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "0", "false", "no", "off", "disabled":
		return grafanaModeDisabled
	case "1", "true", "yes", "on", "required":
		return grafanaModeRequired
	default:
		return grafanaModeAuto
	}
}

func grafanaDownloadEnabled() bool {
	value, ok := os.LookupEnv("ONLAVA_DEV_GRAFANA_DOWNLOAD")
	if !ok {
		return true
	}
	return !isFalseEnv(value)
}

func grafanaRootDir(appRoot string) string {
	if value := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_DIR")); value != "" {
		if filepath.IsAbs(value) {
			return value
		}
		return filepath.Join(appRoot, value)
	}
	return filepath.Join(appRoot, ".onlava", "grafana")
}

func grafanaPluginPreinstall() string {
	if value := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC")); value != "" {
		return value
	}
	return devtools.GrafanaPluginPreinstallSync()
}

func explicitGrafanaPort() bool {
	return strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_PORT")) != ""
}

func explicitGrafanaReuseExternal() bool {
	return envEnabled("ONLAVA_GRAFANA_REUSE_EXTERNAL", false)
}

func envEnabled(key string, fallback bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return !isFalseEnv(value)
}

func grafanaExternalOnPort(ctx context.Context, cfg grafanaConfig) (*grafanaComponent, bool) {
	if tcpAddrAvailable(grafanaDefaultHost, cfg.Port) && !grafanaPortResponds(ctx, cfg.URL) {
		return nil, false
	}
	if !grafanaHealthy(ctx, cfg.URL) {
		return nil, false
	}
	return &grafanaComponent{cfg: cfg, external: true}, true
}

func waitGrafanaReady(ctx context.Context, cfg grafanaConfig) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if grafanaHealthy(ctx, cfg.URL) {
			if err := verifyGrafanaAssets(ctx, cfg); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		if ctx.Err() != nil {
			if lastErr != nil {
				return fmt.Errorf("Grafana health check failed: %w", lastErr)
			}
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func verifyGrafanaAssets(ctx context.Context, cfg grafanaConfig) error {
	if cfg.MetricsURL != "" {
		if err := grafanaAPIReady(ctx, cfg.URL, "/api/datasources/uid/"+url.PathEscape(cfg.MetricsDatasource)); err != nil {
			return fmt.Errorf("metrics datasource %s: %w", cfg.MetricsDatasource, err)
		}
	}
	if cfg.LogsURL != "" {
		if err := grafanaAPIReady(ctx, cfg.URL, "/api/datasources/uid/"+url.PathEscape(cfg.LogsDatasource)); err != nil {
			return fmt.Errorf("logs datasource %s: %w", cfg.LogsDatasource, err)
		}
	}
	if cfg.TracesURL != "" {
		if err := grafanaAPIReady(ctx, cfg.URL, "/api/datasources/uid/"+url.PathEscape(cfg.TracesDatasource)); err != nil {
			return fmt.Errorf("traces datasource %s: %w", cfg.TracesDatasource, err)
		}
	}
	for _, uid := range []string{cfg.OverviewDashboard, cfg.LogsDashboard, cfg.EndpointDashboard} {
		if err := grafanaAPIReady(ctx, cfg.URL, "/api/dashboards/uid/"+url.PathEscape(uid)); err != nil {
			return fmt.Errorf("dashboard %s: %w", uid, err)
		}
	}
	return nil
}

func grafanaAPIReady(ctx context.Context, baseURL, path string) error {
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func grafanaHealthy(ctx context.Context, baseURL string) bool {
	var payload struct {
		Database string `json:"database"`
		Version  string `json:"version"`
	}
	return getGrafanaHealth(ctx, baseURL, &payload) && (payload.Database != "" || payload.Version != "")
}

func grafanaPortResponds(ctx context.Context, baseURL string) bool {
	var payload map[string]any
	return getGrafanaHealth(ctx, baseURL, &payload)
}

func getGrafanaHealth(ctx context.Context, baseURL string, target any) bool {
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+grafanaHealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	if target == nil {
		return true
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(target) == nil
}

func freeLoopbackPort() (int, error) {
	ln, err := netListen("tcp", grafanaDefaultHost+":0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %s", ln.Addr())
	}
	return addr.Port, nil
}

func resolveGrafanaBinary(ctx context.Context, cfg grafanaConfig) (binaryPath, homePath string, err error) {
	if path := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_BIN")); path != "" {
		if !isExecutableFile(path) {
			return "", "", fmt.Errorf("ONLAVA_GRAFANA_BIN points to a non-executable file: %s", path)
		}
		return path, grafanaHomeForBinary(path, cfg.RootDir), nil
	}
	if path, home := downloadedGrafanaBinary(cfg); path != "" {
		return path, home, nil
	}
	if cfg.Download {
		path, home, err := downloadGrafanaBinary(ctx, cfg)
		if err == nil {
			return path, home, nil
		}
	}
	if path, err := exec.LookPath("grafana"); err == nil {
		return path, grafanaHomeForBinary(path, cfg.RootDir), nil
	}
	if path, err := exec.LookPath("grafana-server"); err == nil {
		return path, grafanaHomeForBinary(path, cfg.RootDir), nil
	}
	if !cfg.Download {
		return "", "", fmt.Errorf("Grafana binary not found; set ONLAVA_GRAFANA_BIN or enable ONLAVA_DEV_GRAFANA_DOWNLOAD")
	}
	return "", "", err
}

func grafanaHomeForBinary(path, root string) string {
	if home := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_HOME")); home != "" {
		return home
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, filepath.Join(root, "home")+string(os.PathSeparator)) {
		dir := filepath.Dir(filepath.Dir(clean))
		if isGrafanaHome(dir) {
			return dir
		}
	}
	return ""
}

func downloadedGrafanaBinary(cfg grafanaConfig) (string, string) {
	home := filepath.Join(cfg.RootDir, "home", "grafana-"+cfg.Version)
	for _, binary := range []string{"grafana", "grafana-server"} {
		path := filepath.Join(home, "bin", binary)
		if isExecutableFile(path) {
			return path, home
		}
	}
	return "", ""
}

func isGrafanaHome(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "public")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, "conf")); err == nil {
		return true
	}
	return false
}

func grafanaCommandArgs(binaryPath, homePath, configPath string) []string {
	var args []string
	if filepath.Base(binaryPath) == "grafana" {
		args = append(args, "server")
	}
	if homePath != "" {
		args = append(args, "--homepath", homePath)
	}
	args = append(args, "--config", configPath)
	return args
}

func grafanaChildEnv(base []string, cfg grafanaConfig) []string {
	preserveGF := envEnabled("ONLAVA_GRAFANA_PRESERVE_GF_ENV", false)
	out := make([]string, 0, len(base)+8)
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(key, "GF_") && !preserveGF {
			continue
		}
		out = append(out, kv)
	}
	rootURL := strings.TrimRight(firstNonEmpty(cfg.PublicURL, cfg.URL), "/") + "/"
	return append(out,
		"GF_SERVER_HTTP_ADDR="+grafanaDefaultHost,
		"GF_SERVER_HTTP_PORT="+strconv.Itoa(cfg.Port),
		"GF_SERVER_ROOT_URL="+rootURL,
		"GF_PATHS_DATA="+cfg.DataDir,
		"GF_PATHS_LOGS="+cfg.LogsDir,
		"GF_PATHS_PLUGINS="+cfg.PluginsDir,
		"GF_PATHS_PROVISIONING="+cfg.ProvisioningDir,
	)
}

func downloadGrafanaBinary(ctx context.Context, cfg grafanaConfig) (string, string, error) {
	archiveURL, err := grafanaArchiveURL(cfg)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download %s: unexpected status %s", archiveURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 768<<20))
	if err != nil {
		return "", "", err
	}
	if err := verifyGrafanaArchiveChecksum(ctx, archiveURL, data); err != nil {
		return "", "", err
	}
	home, err := extractGrafanaArchive(cfg, data)
	if err != nil {
		return "", "", err
	}
	for _, binary := range []string{"grafana", "grafana-server"} {
		path := filepath.Join(home, "bin", binary)
		if isExecutableFile(path) {
			return path, home, nil
		}
	}
	return "", "", fmt.Errorf("downloaded archive did not contain a Grafana binary")
}

func grafanaArchiveURL(cfg grafanaConfig) (string, error) {
	if value := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_DOWNLOAD_URL")); value != "" {
		return value, nil
	}
	goos := goruntime.GOOS
	goarch := goruntime.GOARCH
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf("automatic Grafana download is unsupported on %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("automatic Grafana download is unsupported on %s/%s", goos, goarch)
	}
	return fmt.Sprintf("https://dl.grafana.com/oss/release/grafana-%s.%s-%s.tar.gz", cfg.Version, goos, goarch), nil
}

func verifyGrafanaArchiveChecksum(ctx context.Context, archiveURL string, data []byte) error {
	if value := strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_DOWNLOAD_SHA256")); value != "" {
		return verifyGrafanaSHA256(data, value)
	}
	if strings.TrimSpace(os.Getenv("ONLAVA_GRAFANA_DOWNLOAD_URL")) != "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL+".sha256", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", archiveURL+".sha256", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	want := grafanaChecksumFromResponse(string(body), filepath.Base(archiveURL))
	if want == "" {
		return fmt.Errorf("Grafana checksum response did not contain a checksum for %s", filepath.Base(archiveURL))
	}
	return verifyGrafanaSHA256(data, want)
}

func grafanaChecksumFromResponse(body, archiveName string) string {
	if checksum := checksumForArchive(body, archiveName); checksum != "" {
		return checksum
	}
	for _, field := range strings.Fields(body) {
		field = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(field), "sha256:"))
		if len(field) == 64 && isLowerHex(field) {
			return field
		}
	}
	return ""
}

func verifyGrafanaSHA256(data []byte, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if len(want) != 64 {
		return fmt.Errorf("invalid Grafana SHA256 checksum")
	}
	sum := sha256.Sum256(data)
	if fmt.Sprintf("%x", sum[:]) != want {
		return fmt.Errorf("checksum mismatch for Grafana archive")
	}
	return nil
}

func extractGrafanaArchive(cfg grafanaConfig, data []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tmpRoot := filepath.Join(cfg.RootDir, "home", ".tmp-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpRoot)

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name := filepath.Clean(header.Name)
		if name == "." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) || filepath.IsAbs(name) {
			continue
		}
		parts := strings.Split(name, string(os.PathSeparator))
		if len(parts) <= 1 {
			continue
		}
		target := filepath.Join(tmpRoot, filepath.Join(parts[1:]...))
		mode := header.FileInfo().Mode()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode.Perm()); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		case tar.TypeSymlink:
			if header.Linkname == "" || filepath.IsAbs(header.Linkname) || strings.Contains(header.Linkname, "..") {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			_ = os.Symlink(header.Linkname, target)
		}
	}
	target := filepath.Join(cfg.RootDir, "home", "grafana-"+cfg.Version)
	_ = os.RemoveAll(target)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmpRoot, target); err != nil {
		return "", err
	}
	return target, nil
}

func grafanaState(cfg grafanaConfig, status, message string) devdash.GrafanaState {
	available := status == "ready" || status == "external"
	baseURL := ""
	if available {
		baseURL = firstNonEmpty(cfg.PublicURL, cfg.URL)
	}
	dashboards := []devdash.GrafanaDashboard{
		{UID: grafanaOverviewUID, Title: "onlava dev overview", URL: grafanaDashboardURL(baseURL, grafanaOverviewUID)},
		{UID: grafanaLogsDashboardUID, Title: "onlava dev logs", URL: grafanaDashboardURL(baseURL, grafanaLogsDashboardUID)},
		{UID: grafanaEndpointUID, Title: "onlava dev endpoint", URL: grafanaDashboardURL(baseURL, grafanaEndpointUID)},
	}
	datasources := map[string]string{}
	datasourceStatus := map[string]string{}
	if cfg.MetricsURL != "" {
		datasources["metrics"] = cfg.MetricsDatasource
		datasourceStatus["metrics"] = statusForGrafanaDatasource(status)
	}
	if cfg.LogsURL != "" {
		datasources["logs"] = cfg.LogsDatasource
		datasourceStatus["logs"] = statusForGrafanaDatasource(status)
	}
	if cfg.TracesURL != "" {
		datasources["traces"] = cfg.TracesDatasource
		datasourceStatus["traces"] = statusForGrafanaDatasource(status)
	}
	state := devdash.GrafanaState{
		Enabled:          cfg.Enabled,
		Available:        available,
		Status:           status,
		URL:              baseURL,
		OverviewURL:      dashboards[0].URL,
		LogsURL:          dashboards[1].URL,
		EndpointURL:      dashboards[2].URL,
		ConfigPath:       cfg.ConfigPath,
		ProvisioningPath: cfg.ProvisioningDir,
		DashboardsPath:   cfg.DashboardsDir,
		Datasources:      datasources,
		DatasourceStatus: datasourceStatus,
		Dashboards:       dashboards,
		Message:          message,
	}
	if !cfg.Enabled {
		state.URL = ""
		state.OverviewURL = ""
		state.LogsURL = ""
		state.EndpointURL = ""
		state.Dashboards = nil
		state.Datasources = nil
		state.DatasourceStatus = nil
	}
	return state
}

func statusForGrafanaDatasource(status string) string {
	switch status {
	case "ready", "external":
		return "ready"
	case "disabled", "unavailable":
		return status
	default:
		return "degraded"
	}
}

func grafanaDashboardURL(baseURL, uid string) string {
	if baseURL == "" || uid == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/d/" + url.PathEscape(uid)
}

func grafanaStateWithBaseURL(state devdash.GrafanaState, baseURL string) devdash.GrafanaState {
	if baseURL == "" || !state.Enabled || !state.Available {
		return state
	}
	state.URL = baseURL
	state.OverviewURL = grafanaDashboardURL(baseURL, grafanaOverviewUID)
	state.LogsURL = grafanaDashboardURL(baseURL, grafanaLogsDashboardUID)
	state.EndpointURL = grafanaDashboardURL(baseURL, grafanaEndpointUID)
	for i := range state.Dashboards {
		state.Dashboards[i].URL = grafanaDashboardURL(baseURL, state.Dashboards[i].UID)
	}
	return state
}

func (g *grafanaComponent) State() devdash.GrafanaState {
	if g == nil {
		return grafanaState(grafanaConfig{}, "disabled", "Grafana not started")
	}
	return g.state
}

func (g *grafanaComponent) URL() string {
	if g == nil || !g.cfg.Enabled {
		return ""
	}
	return g.cfg.URL
}

func (g *grafanaComponent) Interrupt() error {
	if g == nil || g.external || g.cmd == nil {
		return nil
	}
	return interruptProcessTree(g.cmd)
}

func (g *grafanaComponent) WaitOrKill(grace time.Duration) error {
	if g == nil || g.external || g.cmd == nil {
		return nil
	}
	select {
	case err := <-g.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		_ = killProcessTree(g.cmd)
		select {
		case err := <-g.done:
			if err == nil || isExpectedExit(err) {
				return nil
			}
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("Grafana process did not exit after SIGKILL")
		}
	}
}

func encodeGrafanaState(state devdash.GrafanaState) json.RawMessage {
	data, err := json.Marshal(state)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func decodeGrafanaState(raw json.RawMessage) *devdash.GrafanaState {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil
	}
	var state devdash.GrafanaState
	if err := json.Unmarshal(raw, &state); err != nil || state.Status == "" {
		return nil
	}
	return &state
}

func warnGrafana(console *runConsole, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if console != nil {
		console.Event("grafana.degraded", map[string]any{"reason": msg})
		if console.verbose && !console.json {
			fmt.Fprintf(os.Stderr, "onlava: Grafana %s\n", msg)
		}
	}
}
