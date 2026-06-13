package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	sceneryruntime "scenery.sh/runtime"
)

const temporalDevStartupTimeout = 20 * time.Second

type temporalDevServer struct {
	cmd       *exec.Cmd
	done      chan error
	info      sceneryruntime.TemporalRuntimeInfo
	uiURL     string
	dbPath    string
	stdoutLog string
	stderrLog string
	external  bool
	startedAt time.Time
}

func startTemporalDevServer(ctx context.Context, root string, cfg app.Config, console *runConsole) (*temporalDevServer, error) {
	rtCfg := temporalRuntimeConfigFromApp(cfg.Temporal)
	info := sceneryruntime.ResolveTemporalConfig(cfg.Name, rtCfg)
	if !info.Enabled || !info.LocalAutoStart {
		return nil, nil
	}
	if info.Mode != "local" {
		return nil, fmt.Errorf("temporal: local auto_start requires temporal.mode %q, got %q", sceneryruntime.DefaultTemporalMode, info.Mode)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, status := checkTemporalConnection(checkCtx, cfg.Name, rtCfg)
	cancel()
	if status.Reachable {
		if console != nil && console.verbose {
			console.Event("temporal.reuse", map[string]any{
				"address":   info.Address,
				"namespace": info.Namespace,
			})
		}
		return &temporalDevServer{info: info, uiURL: temporalUIURL(info), external: true}, nil
	}

	host, port, err := splitTemporalAddress(info.Address)
	if err != nil {
		return nil, err
	}
	path, err := resolveTemporalCLI(ctx, temporalToolchainStoreDir(root), true)
	if err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}
	dbPath := temporalLocalDBPath(root, info.LocalDBFilename)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	uiPort, err := chooseTemporalUIPort(host, port)
	if err != nil {
		return nil, err
	}
	args := []string{
		"server",
		"start-dev",
		"--ip",
		host,
		"--port",
		strconv.Itoa(port),
		"--ui-ip",
		host,
		"--ui-port",
		strconv.Itoa(uiPort),
		"--db-filename",
		dbPath,
		"--log-level",
		"warn",
	}
	if info.Namespace != sceneryruntime.DefaultTemporalNamespace {
		args = append(args, "--namespace", info.Namespace)
	}

	cmd := commandTreeContext(ctx, path, args...)
	cmd.Dir = root
	logs, err := openSubstrateLogWriters(root, localagent.SubstrateTemporal, "server", console)
	if err != nil {
		return nil, fmt.Errorf("temporal: open substrate logs: %w", err)
	}
	cmd.Stdout = logs.stdout
	cmd.Stderr = logs.stderr
	if err := cmd.Start(); err != nil {
		_ = logs.close()
		return nil, err
	}

	server := &temporalDevServer{
		cmd:       cmd,
		done:      make(chan error, 1),
		info:      info,
		uiURL:     fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(uiPort))),
		dbPath:    dbPath,
		stdoutLog: logs.stdoutPath,
		stderrLog: logs.stderrPath,
		startedAt: time.Now().UTC(),
	}
	go func() {
		server.done <- cmd.Wait()
		_ = logs.close()
		close(server.done)
	}()
	if err := server.waitReady(ctx, cfg.Name, rtCfg); err != nil {
		_ = server.Interrupt()
		_ = server.WaitOrKill(5 * time.Second)
		return nil, err
	}
	if console != nil && console.verbose {
		console.Event("temporal.start", map[string]any{
			"address":   info.Address,
			"namespace": info.Namespace,
			"ui_url":    server.uiURL,
			"db_path":   server.dbPath,
			"pid":       cmd.Process.Pid,
		})
	}
	return server, nil
}

func resolveTemporalCLI(ctx context.Context, storeDir string, download bool) (string, error) {
	if path := strings.TrimSpace(envpolicy.Get("SCENERY_TEMPORAL_BIN")); path != "" {
		if isExecutableFile(path) {
			return path, nil
		}
		return "", fmt.Errorf("SCENERY_TEMPORAL_BIN points to a non-executable file: %s", path)
	}
	if status, err := managedToolchainArtifactStatusInDir(storeDir, "temporal-cli"); err == nil && status.ManagedPath != "" && isExecutableFile(status.ManagedPath) {
		return status.ManagedPath, nil
	}
	if !download {
		return "", fmt.Errorf("managed Temporal CLI is not installed; system PATH binaries are not used for managed toolchain artifacts; run `scenery system toolchain sync --tool temporal-cli` or set SCENERY_TEMPORAL_BIN explicitly")
	}
	status, err := syncManagedToolchainArtifactInDir(ctx, storeDir, "temporal-cli")
	if err != nil {
		return "", fmt.Errorf("managed Temporal CLI is not installed and could not be synced: %w", err)
	}
	if status.ManagedPath == "" || !isExecutableFile(status.ManagedPath) {
		return "", fmt.Errorf("managed Temporal CLI is not installed; run `scenery system toolchain sync --tool temporal-cli` or set SCENERY_TEMPORAL_BIN explicitly")
	}
	return status.ManagedPath, nil
}

func temporalToolchainStoreDir(root string) string {
	if strings.TrimSpace(envpolicy.Get("SCENERY_TOOLCHAIN_DIR")) != "" {
		return toolchainStoreDirForStateRoot("")
	}
	if filepath.Base(filepath.Clean(root)) == "temporal" {
		return filepath.Join(filepath.Dir(filepath.Clean(root)), "toolchain")
	}
	return filepath.Join(root, ".scenery", "toolchain")
}

func temporalRuntimeConfigFromApp(cfg app.TemporalConfig) sceneryruntime.TemporalConfig {
	return sceneryruntime.TemporalConfig{
		Enabled:         cfg.Enabled,
		Mode:            cfg.Mode,
		Namespace:       cfg.Namespace,
		AddressEnv:      cfg.AddressEnv,
		TaskQueuePrefix: cfg.TaskQueuePrefix,
		PayloadCodec:    cfg.PayloadCodec,
		APIKeyEnv:       cfg.APIKeyEnv,
		TLS: sceneryruntime.TemporalTLSConfig{
			Enabled:           cfg.TLS.Enabled,
			ServerNameEnv:     cfg.TLS.ServerNameEnv,
			CACertFileEnv:     cfg.TLS.CACertFileEnv,
			ClientCertFileEnv: cfg.TLS.ClientCertFileEnv,
			ClientKeyFileEnv:  cfg.TLS.ClientKeyFileEnv,
		},
		Local: sceneryruntime.TemporalLocalConfig{
			AutoStart:  cfg.Local.AutoStart,
			DBFilename: cfg.Local.DBFilename,
		},
	}
}

func (s *temporalDevServer) waitReady(ctx context.Context, appName string, cfg sceneryruntime.TemporalConfig) error {
	timer := time.NewTimer(temporalDevStartupTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var lastErr string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-s.done:
			if err != nil {
				return fmt.Errorf("temporal: dev server exited before becoming ready: %w", err)
			}
			return fmt.Errorf("temporal: dev server exited before becoming ready")
		case <-timer.C:
			if lastErr == "" {
				lastErr = "no connectivity check completed"
			}
			return fmt.Errorf("temporal: dev server at %s did not become ready within %s: %s", s.info.Address, temporalDevStartupTimeout, lastErr)
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			_, status := checkTemporalConnection(checkCtx, appName, cfg)
			cancel()
			if status.Reachable {
				return nil
			}
			lastErr = status.Error
		}
	}
}

func (s *temporalDevServer) Env() []string {
	if s == nil || !s.info.Enabled {
		return nil
	}
	env := []string{
		s.info.AddressEnv + "=" + s.info.Address,
		sceneryruntime.DefaultTemporalNamespaceEnv + "=" + s.info.Namespace,
	}
	return env
}

func (s *temporalDevServer) URL() string {
	if s == nil {
		return ""
	}
	return s.uiURL
}

func temporalDevServerFromSubstrate(substrate localagent.Substrate, appName string, cfg app.TemporalConfig) *temporalDevServer {
	if substrate.Kind != localagent.SubstrateTemporal {
		return nil
	}
	info := sceneryruntime.ResolveTemporalConfig(appName, temporalRuntimeConfigFromApp(cfg))
	address := strings.TrimSpace(substrate.Endpoints["address"])
	if address != "" {
		info.Address = address
	}
	namespace := strings.TrimSpace(substrate.Endpoints["namespace"])
	if namespace != "" {
		info.Namespace = namespace
	}
	uiURL := strings.TrimSpace(substrate.URLs["ui"])
	if uiURL == "" {
		uiURL = temporalUIURL(info)
	}
	return &temporalDevServer{
		info:      info,
		uiURL:     strings.TrimRight(uiURL, "/"),
		dbPath:    strings.TrimSpace(substrate.Endpoints["db_path"]),
		stdoutLog: strings.TrimSpace(substrate.Endpoints["stdout_log"]),
		stderrLog: strings.TrimSpace(substrate.Endpoints["stderr_log"]),
		external:  true,
	}
}

func (s *temporalDevServer) SubstrateRequest(ownerPID int) localagent.UpsertSubstrateRequest {
	if s == nil || !s.info.Enabled || s.info.Address == "" {
		return localagent.UpsertSubstrateRequest{}
	}
	pids := map[string]int{}
	if s.cmd != nil && s.cmd.Process != nil {
		pids["server"] = s.cmd.Process.Pid
		ownerPID = s.cmd.Process.Pid
	}
	endpoints := map[string]string{
		"address":   s.info.Address,
		"namespace": s.info.Namespace,
	}
	if s.dbPath != "" {
		endpoints["db_path"] = s.dbPath
	}
	if s.stdoutLog != "" {
		endpoints["stdout_log"] = s.stdoutLog
	}
	if s.stderrLog != "" {
		endpoints["stderr_log"] = s.stderrLog
	}
	return localagent.UpsertSubstrateRequest{
		Kind:      localagent.SubstrateTemporal,
		Status:    "ready",
		OwnerPID:  ownerPID,
		PIDs:      pids,
		URLs:      map[string]string{"ui": s.uiURL},
		Endpoints: endpoints,
	}
}

func (s *temporalDevServer) ExitRecord(err error) localagent.SubstrateExit {
	if s == nil {
		return localagent.SubstrateExit{}
	}
	pid := 0
	var state *os.ProcessState
	if s.cmd != nil {
		state = s.cmd.ProcessState
		if s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
	}
	return substrateExitRecord("server", pid, s.startedAt, s.stdoutLog, s.stderrLog, err, state)
}

func (s *temporalDevServer) MarkExternal() {
	if s != nil {
		s.external = true
	}
}

func (s *temporalDevServer) Components() []managedSubstrateComponent {
	if s == nil || s.done == nil {
		return nil
	}
	return []managedSubstrateComponent{{
		Name:        "server",
		DisplayName: "Temporal dev server",
		Role:        "workflow-server",
		URL:         s.URL(),
		Done:        s.done,
		ExitRecord:  s.ExitRecord,
	}}
}

func (s *temporalDevServer) Reachable(ctx context.Context, appName string, cfg app.TemporalConfig) bool {
	if s == nil || !s.info.Enabled {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return temporalAddressReachable(checkCtx, s.info.Address) == nil
}

func (s *temporalDevServer) Interrupt() error {
	if s == nil || s.external || s.cmd == nil {
		return nil
	}
	return interruptProcessTree(s.cmd)
}

func (s *temporalDevServer) WaitOrKill(grace time.Duration) error {
	if s == nil || s.external || s.cmd == nil {
		return nil
	}
	select {
	case err := <-s.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		if err := killProcessTree(s.cmd); err != nil {
			return err
		}
		select {
		case err := <-s.done:
			if err == nil || isExpectedExit(err) {
				return nil
			}
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("temporal: dev server did not exit after kill")
		}
	}
}

func splitTemporalAddress(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", 0, fmt.Errorf("temporal: address %q must be host:port for local auto_start: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("temporal: address %q has invalid port %q", address, portText)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port, nil
}

func chooseTemporalUIPort(host string, serverPort int) (int, error) {
	for range 10 {
		ln, err := netListen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return 0, fmt.Errorf("temporal: allocate UI port: %w", err)
		}
		addr, ok := ln.Addr().(*net.TCPAddr)
		closeErr := ln.Close()
		if !ok {
			return 0, fmt.Errorf("temporal: unexpected UI listener address %s", ln.Addr())
		}
		if closeErr != nil {
			return 0, fmt.Errorf("temporal: release UI port probe: %w", closeErr)
		}
		if addr.Port != serverPort {
			return addr.Port, nil
		}
	}
	return 0, fmt.Errorf("temporal: could not allocate UI port distinct from server port %d", serverPort)
}

func temporalLocalDBPath(root, filename string) string {
	if filepath.IsAbs(filename) {
		return filename
	}
	return filepath.Join(root, filename)
}

func temporalUIURL(info sceneryruntime.TemporalRuntimeInfo) string {
	host, port, err := splitTemporalAddress(info.Address)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(port+1000)))
}

func checkTemporalConnection(ctx context.Context, appName string, cfg sceneryruntime.TemporalConfig) (sceneryruntime.TemporalRuntimeInfo, sceneryruntime.TemporalConnectionStatus) {
	info := sceneryruntime.ResolveTemporalConfig(appName, cfg)
	if !info.Enabled {
		return info, sceneryruntime.TemporalConnectionStatus{}
	}
	if err := temporalAddressReachable(ctx, info.Address); err != nil {
		return info, sceneryruntime.TemporalConnectionStatus{
			Checked: true,
			Error:   err.Error(),
		}
	}
	return info, sceneryruntime.TemporalConnectionStatus{
		Checked:   true,
		Reachable: true,
	}
}

func temporalAddressReachable(ctx context.Context, address string) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", strings.TrimSpace(address))
	if err != nil {
		return err
	}
	return conn.Close()
}

func temporalUIUpstreamForConfig(cfg app.Config) string {
	info := sceneryruntime.ResolveTemporalConfig(cfg.Name, temporalRuntimeConfigFromApp(cfg.Temporal))
	if info.Mode != sceneryruntime.DefaultTemporalMode {
		return ""
	}
	host, port, err := splitTemporalAddress(info.Address)
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port+1000))
}

func warnTemporal(console *runConsole, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if console != nil && console.verbose {
		console.Event("temporal.warn", map[string]any{"message": msg})
		if !console.json {
			fmt.Fprintf(os.Stderr, "scenery: Temporal %s\n", msg)
		}
	}
}

type temporalSubstrateAdapter struct {
	cfg     app.Config
	console *runConsole
}

func (a temporalSubstrateAdapter) Kind() string       { return localagent.SubstrateTemporal }
func (a temporalSubstrateAdapter) SourceID() string   { return "temporal" }
func (a temporalSubstrateAdapter) SourceName() string { return "Temporal" }
func (a temporalSubstrateAdapter) Role() string       { return "workflow-server" }

func (a temporalSubstrateAdapter) Start(_ context.Context, root string) (managedSubstrateHandle, error) {
	return startTemporalDevServer(context.Background(), root, a.cfg, a.console)
}

func (a temporalSubstrateAdapter) FromSubstrate(ctx context.Context, substrate localagent.Substrate) (managedSubstrateHandle, bool) {
	temporal := temporalDevServerFromSubstrate(substrate, a.cfg.Name, a.cfg.Temporal)
	if temporal == nil || !temporal.Reachable(ctx, a.cfg.Name, a.cfg.Temporal) {
		return nil, false
	}
	return temporal, true
}

func (a temporalSubstrateAdapter) ReadyFields(handle managedSubstrateHandle) map[string]any {
	temporal, _ := handle.(*temporalDevServer)
	if temporal == nil {
		return nil
	}
	return map[string]any{
		"owner":     "agent",
		"address":   temporal.info.Address,
		"namespace": temporal.info.Namespace,
		"ui_url":    temporal.URL(),
	}
}

func (a temporalSubstrateAdapter) ReuseFields(handle managedSubstrateHandle, _ localagent.Substrate) map[string]any {
	return a.ReadyFields(handle)
}

func (a temporalSubstrateAdapter) ExitStatus(managedSubstrateComponent) string {
	return "exited"
}

func (a temporalSubstrateAdapter) ExitMessage(managedSubstrateComponent) string {
	return "Temporal dev server exited"
}

func (a temporalSubstrateAdapter) EventSource(handle managedSubstrateHandle, component managedSubstrateComponent, status string) devdash.DevSource {
	url := component.URL
	if temporal, _ := handle.(*temporalDevServer); temporal != nil {
		url = temporal.URL()
	}
	return devdash.DevSource{
		ID:     "temporal",
		Kind:   "substrate",
		Name:   "temporal",
		Role:   "workflow-server",
		Status: status,
		URL:    url,
	}
}
