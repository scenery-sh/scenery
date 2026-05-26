package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

const temporalDevStartupTimeout = 20 * time.Second

type temporalDevServer struct {
	cmd       *exec.Cmd
	done      chan error
	info      onlavaruntime.TemporalRuntimeInfo
	uiURL     string
	dbPath    string
	external  bool
	startedAt time.Time
}

func startTemporalDevServer(ctx context.Context, root string, cfg app.Config, console *runConsole) (*temporalDevServer, error) {
	rtCfg := temporalRuntimeConfigFromApp(cfg.Temporal)
	info := onlavaruntime.ResolveTemporalConfig(cfg.Name, rtCfg)
	if !info.Enabled || !info.LocalAutoStart {
		return nil, nil
	}
	if info.Mode != "local" {
		return nil, fmt.Errorf("temporal: local auto_start requires temporal.mode %q, got %q", onlavaruntime.DefaultTemporalMode, info.Mode)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, status := onlavaruntime.CheckTemporalConnection(checkCtx, cfg.Name, rtCfg)
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
	path, err := execLookPath("temporal")
	if err != nil {
		return nil, fmt.Errorf("temporal: temporal CLI not found in PATH; install it or disable temporal.local.auto_start: %w", err)
	}
	dbPath := temporalLocalDBPath(root, info.LocalDBFilename)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	uiPort := port + 1000
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
	if info.Namespace != onlavaruntime.DefaultTemporalNamespace {
		args = append(args, "--namespace", info.Namespace)
	}

	cmd := commandTreeContext(ctx, path, args...)
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

	server := &temporalDevServer{
		cmd:       cmd,
		done:      make(chan error, 1),
		info:      info,
		uiURL:     fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(uiPort))),
		dbPath:    dbPath,
		startedAt: time.Now().UTC(),
	}
	go func() {
		server.done <- cmd.Wait()
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

func temporalRuntimeConfigFromApp(cfg app.TemporalConfig) onlavaruntime.TemporalConfig {
	return onlavaruntime.TemporalConfig{
		Enabled:         cfg.Enabled,
		Mode:            cfg.Mode,
		Namespace:       cfg.Namespace,
		AddressEnv:      cfg.AddressEnv,
		TaskQueuePrefix: cfg.TaskQueuePrefix,
		PayloadCodec:    cfg.PayloadCodec,
		APIKeyEnv:       cfg.APIKeyEnv,
		TLS: onlavaruntime.TemporalTLSConfig{
			Enabled:           cfg.TLS.Enabled,
			ServerNameEnv:     cfg.TLS.ServerNameEnv,
			CACertFileEnv:     cfg.TLS.CACertFileEnv,
			ClientCertFileEnv: cfg.TLS.ClientCertFileEnv,
			ClientKeyFileEnv:  cfg.TLS.ClientKeyFileEnv,
		},
		Local: onlavaruntime.TemporalLocalConfig{
			AutoStart:  cfg.Local.AutoStart,
			DBFilename: cfg.Local.DBFilename,
		},
	}
}

func (s *temporalDevServer) waitReady(ctx context.Context, appName string, cfg onlavaruntime.TemporalConfig) error {
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
			_, status := onlavaruntime.CheckTemporalConnection(checkCtx, appName, cfg)
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
		onlavaruntime.DefaultTemporalNamespaceEnv + "=" + s.info.Namespace,
	}
	return env
}

func (s *temporalDevServer) URL() string {
	if s == nil {
		return ""
	}
	return s.uiURL
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

func temporalLocalDBPath(root, filename string) string {
	if filepath.IsAbs(filename) {
		return filename
	}
	return filepath.Join(root, filename)
}

func temporalUIURL(info onlavaruntime.TemporalRuntimeInfo) string {
	host, port, err := splitTemporalAddress(info.Address)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(port+1000)))
}

func temporalUIUpstreamForConfig(cfg app.Config) string {
	info := onlavaruntime.ResolveTemporalConfig(cfg.Name, temporalRuntimeConfigFromApp(cfg.Temporal))
	if info.Mode != onlavaruntime.DefaultTemporalMode {
		return ""
	}
	host, port, err := splitTemporalAddress(info.Address)
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port+1000))
}
