package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	"github.com/pbrazdil/onlava/internal/codegen"
	"github.com/pbrazdil/onlava/internal/dbstudio"
	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/devmeta"
	"github.com/pbrazdil/onlava/internal/envfile"
	"github.com/pbrazdil/onlava/internal/localproxy"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
)

type runningApp struct {
	cmd      *exec.Cmd
	done     chan error
	buildDir string
	pid      string
	output   *safeLineTail
}

type devSupervisor struct {
	ctx    context.Context
	cancel context.CancelFunc
	root   string
	cfg    app.Config
	addr   string

	store       *devdash.Store
	dashboard   *dashboardServer
	dbStudio    *dbstudio.Instance
	dbStudioUI  *dbStudioUIServer
	dbStudioURL string
	proxy       *localproxy.Proxy
	temporal    *temporalDevServer
	victoria    *victoriaStack
	grafana     *grafanaComponent
	reportToken string
	console     *runConsole

	closeOnce sync.Once
	mu        sync.RWMutex
	current   *runningApp
	status    devdash.AppRecord
}

const (
	appStartupTimeout      = 30 * time.Second
	appStartupPollInterval = 50 * time.Millisecond
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func newDevSupervisor(ctx context.Context, root string, cfg app.Config, addr string, verbose, jsonMode bool) (*devSupervisor, error) {
	supervisorCtx, cancel := context.WithCancel(ctx)
	store, err := devdash.OpenStore(os.Getenv("ONLAVA_DEV_CACHE_DIR"))
	if err != nil {
		cancel()
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, err
	}
	appID := cfg.AppID()

	s := &devSupervisor{
		ctx:         supervisorCtx,
		cancel:      cancel,
		root:        root,
		cfg:         cfg,
		addr:        addr,
		store:       store,
		reportToken: token,
		console:     newRunConsole(os.Stdout, os.Stderr, verbose, jsonMode, appID, root),
		status: devdash.AppRecord{
			ID:         appID,
			Name:       cfg.Name,
			Root:       root,
			ListenAddr: addr,
			Offline:    true,
			UpdatedAt:  time.Now().UTC(),
		},
	}
	uiDir, err := prepareDashboardUIDir(supervisorCtx, s.console)
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, err
	}
	s.dashboard = newDashboardServer(s, uiDir)
	return s, nil
}

func (s *devSupervisor) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}

		app := s.detachCurrentApp()
		dbStudio := s.currentDBStudio()
		victoria := s.victoria
		grafana := s.grafana
		temporal := s.temporal

		var errs []error
		if app != nil {
			if err := app.interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if dbStudio != nil {
			if err := dbStudio.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if victoria != nil {
			if err := victoria.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if grafana != nil {
			if err := grafana.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if temporal != nil {
			if err := temporal.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}

		type closer struct {
			name string
			fn   func() error
		}
		closers := []closer{}
		if s.dashboard != nil {
			closers = append(closers, closer{name: "dashboard", fn: s.dashboard.Close})
		}
		if s.proxy != nil {
			closers = append(closers, closer{name: "proxy", fn: s.proxy.Close})
		}
		if s.dbStudioUI != nil {
			closers = append(closers, closer{name: "dbstudio-ui", fn: s.dbStudioUI.Close})
		}

		if len(closers) > 0 {
			errCh := make(chan error, len(closers))
			var wg sync.WaitGroup
			for _, item := range closers {
				wg.Add(1)
				go func(fn func() error) {
					defer wg.Done()
					if err := fn(); err != nil {
						errCh <- err
					}
				}(item.fn)
			}
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				errs = append(errs, fmt.Errorf("timed out closing in-process dev services"))
			}
			close(errCh)
			for err := range errCh {
				errs = append(errs, err)
			}
		}

		if app != nil {
			if err := app.waitOrKill(stopTimeout); err != nil {
				errs = append(errs, err)
			}
		}
		if dbStudio != nil {
			if err := dbStudio.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}
		if victoria != nil {
			if err := victoria.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}
		if grafana != nil {
			if err := grafana.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}
		if temporal != nil {
			if err := temporal.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}

		if s.store != nil {
			if err := s.store.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		closeErr = errors.Join(errs...)
	})
	return closeErr
}

func (s *devSupervisor) Start(ctx context.Context) error {
	if s.console != nil {
		s.console.Event("run.start", map[string]any{
			"listen_addr": s.addr,
		})
	}
	if err := ensureOnlavaLocalStateIgnored(s.root); err != nil {
		return err
	}
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	if err := s.dashboard.Start(ctx); err != nil {
		return err
	}
	if err := s.ensureTemporalDevServer(ctx); err != nil {
		return err
	}
	s.victoria = startVictoriaStack(s.ctx, s.root, s.console)
	if err := s.startGrafana(s.ctx); err != nil {
		return err
	}
	if err := s.startDBStudio(s.ctx); err != nil {
		slog.Warn("db studio unavailable", "err", err)
	}
	if err := s.startLocalProxy(); err != nil {
		slog.Warn("local HTTPS proxy unavailable", "err", err)
	}
	return nil
}

func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool, snapshot fileSnapshot, changedPaths []string) error {
	if cfg, err := s.reloadConfig(); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	} else {
		s.cfg = cfg
		s.setAppIdentity(cfg)
	}
	s.setCompiling(true, "")
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.dashboard.notify(&devdash.Notification{
		Method: "process/compile-start",
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "compile-start", s.appStatus())
	if s.console != nil {
		s.console.Event("process.compile-start", map[string]any{
			"initial": initial,
		})
	}

	var (
		model       *model.App
		metadata    json.RawMessage
		apiEncoding json.RawMessage
		result      *build.Result
		cached      *build.CachedGraph
		err         error
	)
	graphFingerprint := snapshotFingerprint(snapshot)
	if err := s.console.Phase("Building onlava application graph", func() error {
		cached, _, err = build.LoadCachedGraph(s.root, s.cfg.Name, graphFingerprint)
		if err != nil {
			return err
		}
		if cached != nil {
			metadata = append(json.RawMessage(nil), cached.Metadata...)
			apiEncoding = append(json.RawMessage(nil), cached.APIEncoding...)
			result = cached.Result
		}
		model, err = parse.App(s.root, s.cfg.Name)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if err := s.console.Phase("Analyzing service topology", func() error {
		if cached != nil {
			return nil
		}
		metadata, err = devmeta.BuildMetadataSnapshot(model)
		if err != nil {
			return err
		}
		apiEncoding, err = devmeta.BuildAPIEncoding(model)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if err := validateLocalSecretsFiles(s.root); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	s.cfg = effectiveDevConfigForModel(s.cfg, model)
	if err := s.ensureTemporalDevServer(ctx); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Generating boilerplate code", func() error {
		if cached != nil {
			reused, refreshErr := build.RefreshCachedWorkspace(s.root, result)
			if refreshErr != nil {
				return refreshErr
			}
			if reused {
				return nil
			}
			model, err = parse.App(s.root, s.cfg.Name)
			if err != nil {
				return err
			}
			metadata, err = devmeta.BuildMetadataSnapshot(model)
			if err != nil {
				return err
			}
			apiEncoding, err = devmeta.BuildAPIEncoding(model)
			if err != nil {
				return err
			}
		}
		result, err = build.Prepare(s.root, model, s.cfg, build.PrepareOptions{ChangedPaths: changedPaths})
		if err == nil && result != nil {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return err
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Compiling application source code", func() error {
		if result != nil && result.GraphFingerprint == "" {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return build.CompileContext(ctx, result)
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	s.setMetadata(metadata, apiEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return err
	}

	previous := s.currentApp()
	var current *runningApp
	if err := s.console.Phase("Starting onlava application", func() error {
		if previous != nil {
			if err := previous.stop(); err != nil {
				return err
			}
		}
		current, err = s.startApp(ctx, result, metadata, apiEncoding)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}

	s.mu.Lock()
	s.current = current
	s.mu.Unlock()

	s.setCompiling(false, "")
	s.setRunning(current.pid, metadata, apiEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return err
	}

	method := "process/start"
	if previous != nil {
		method = "process/reload"
	}
	s.dashboard.notify(&devdash.Notification{
		Method: method,
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), method, s.appStatus())
	if s.console != nil {
		s.console.Event(method, map[string]any{
			"pid":         current.pid,
			"listen_addr": s.addr,
			"initial":     initial,
		})
	}
	if initial {
		s.console.Banner(s.runURLs())
	}
	return nil
}

func (s *devSupervisor) reloadConfig() (app.Config, error) {
	root, cfg, err := app.DiscoverRoot(s.root)
	if err != nil {
		return app.Config{}, err
	}
	if filepath.Clean(root) != filepath.Clean(s.root) {
		return app.Config{}, fmt.Errorf(".onlava.json moved from %s to %s", s.root, root)
	}
	return cfg, nil
}

func effectiveDevConfigForModel(cfg app.Config, appModel *model.App) app.Config {
	if !codegen.AppUsesTemporalRuntime(appModel) {
		return cfg
	}
	cfg.Temporal.Enabled = true
	if strings.TrimSpace(cfg.Temporal.Mode) == "" {
		cfg.Temporal.Mode = "local"
	}
	cfg.Temporal.Local.AutoStart = true
	return cfg
}

func (s *devSupervisor) startApp(ctx context.Context, result *build.Result, metadata, apiEncoding json.RawMessage) (*runningApp, error) {
	cmd := exec.Command(result.Binary)
	configureChildProcess(cmd)
	cmd.WaitDelay = stopTimeout + time.Second
	cmd.Dir = s.root
	baseEnv, err := appEnvWithDotEnv(os.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return nil, err
	}
	cmd.Env = appChildEnv(
		baseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"ONLAVA_LISTEN_ADDR="+s.addr,
		"ONLAVA_APP_ID="+s.activeAppID(),
		"ONLAVA_APP_ROOT="+s.root,
		"ONLAVA_DEV_SUPERVISOR=1",
		"ONLAVA_DEV_ENDPOINTS=1",
		fmt.Sprintf("ONLAVA_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"ONLAVA_LOCAL_PROXY=0",
		"ONLAVA_DEV_REPORT_URL=http://"+devdash.ListenAddr()+devdash.ReportPath,
		"ONLAVA_DEV_REPORT_TOKEN="+s.reportToken,
	)
	cmd.Env = append(cmd.Env, s.victoria.Env()...)
	cmd.Env = append(cmd.Env, s.temporal.Env()...)
	if s.proxy != nil {
		cmd.Env = append(cmd.Env, "ONLAVA_PUBLIC_BASE_URL="+s.proxy.Routes().APIURL)
	}
	cmd.Stdin = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := portAvailable(s.addr); err != nil {
		return nil, fmt.Errorf("app listen address %s is unavailable before startup: %w", s.addr, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	app := &runningApp{
		cmd:      cmd,
		done:     make(chan error, 1),
		buildDir: result.Dir,
		pid:      fmt.Sprintf("%d", cmd.Process.Pid),
		output:   &safeLineTail{limit: 80},
	}

	go s.captureOutput(ctx, app, "stdout", stdout, os.Stdout)
	go s.captureOutput(ctx, app, "stderr", stderr, os.Stderr)
	go func() {
		app.done <- cmd.Wait()
		close(app.done)
		s.handleExit(context.Background(), app)
	}()
	if err := s.waitForAppStartup(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

func (s *devSupervisor) waitForAppStartup(ctx context.Context, app *runningApp) error {
	deadline := time.NewTimer(appStartupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(appStartupPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = app.stop()
			return ctx.Err()
		case err, ok := <-app.done:
			if !ok {
				return appStartupExitError(app, nil)
			}
			return appStartupExitError(app, err)
		case <-ticker.C:
			if tcpAddrAcceptsConnections(s.addr) {
				return nil
			}
		case <-deadline.C:
			_ = app.stop()
			return fmt.Errorf("onlava app did not listen on %s within %s", s.addr, appStartupTimeout)
		}
	}
}

func appStartupExitError(app *runningApp, err error) error {
	message := "onlava app exited during startup"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += ": process exited without an error"
	}
	if app != nil && app.output != nil {
		if output := strings.TrimSpace(app.output.String()); output != "" {
			message += "\n" + output
		}
	}
	return errors.New(message)
}

func tcpAddrAcceptsConnections(addr string) bool {
	target := addr
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		target = net.JoinHostPort("127.0.0.1", port)
	}
	conn, err := net.DialTimeout("tcp", target, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *devSupervisor) captureOutput(ctx context.Context, app *runningApp, stream string, src io.Reader, dst io.Writer) {
	reader := bufio.NewReader(src)
	pid := ""
	if app != nil {
		pid = app.pid
	}
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			if s.console == nil || !s.console.json {
				_, _ = dst.Write(chunk)
			}
			plain := stripANSI(chunk)
			if app != nil && app.output != nil {
				app.output.Add(strings.TrimRight(string(plain), "\n"))
			}
			output := devdash.ProcessOutput{
				AppID:     s.activeAppID(),
				PID:       pid,
				Stream:    stream,
				Output:    plain,
				CreatedAt: time.Now().UTC(),
			}
			_ = s.store.WriteProcessOutput(ctx, output)
			s.dashboard.notify(&devdash.Notification{
				Method: "process/output",
				Params: map[string]any{
					"appID":      s.activeAppID(),
					"pid":        pid,
					"stream":     stream,
					"output":     output.Output,
					"created_at": output.CreatedAt.Format(time.RFC3339Nano),
				},
			})
			if s.console != nil {
				s.console.Event("process.output", map[string]any{
					"pid":        pid,
					"stream":     stream,
					"output":     string(output.Output),
					"created_at": output.CreatedAt.Format(time.RFC3339Nano),
				})
			}
		}
		if err != nil {
			if !isExpectedOutputReadError(err) {
				fmt.Fprintf(os.Stderr, "onlava: failed reading %s: %v\n", stream, err)
			}
			return
		}
	}
}

func isExpectedOutputReadError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed)
}

func appChildEnv(base []string, forceColor bool, vars ...string) []string {
	env := append(append([]string(nil), base...), vars...)
	if forceColor {
		env = append(env, "CLICOLOR_FORCE=1")
	}
	return env
}

func appEnvWithDotEnv(base []string, root string, names ...string) ([]string, error) {
	if len(names) == 0 {
		names = []string{".env"}
	}
	values, err := envfile.MergeFiles(root, names...)
	if err != nil {
		return nil, err
	}
	return envfile.AppendMissing(base, values), nil
}

func appEnvWithRequiredDotEnv(base []string, root string, names ...string) ([]string, error) {
	if err := requireDotEnv(root); err != nil {
		return nil, err
	}
	return appEnvWithDotEnv(base, root, names...)
}

func stripANSI(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	clean := ansiEscapeRE.ReplaceAll(data, nil)
	return append([]byte(nil), clean...)
}

func (s *devSupervisor) handleExit(ctx context.Context, app *runningApp) {
	if app == nil {
		return
	}
	s.mu.Lock()
	if s.current == nil || s.current.pid != app.pid {
		s.mu.Unlock()
		return
	}
	s.current = nil
	s.status.Running = false
	s.status.Offline = true
	s.status.PID = ""
	s.status.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()

	_ = s.persistStatus(ctx)
	s.dashboard.notify(&devdash.Notification{
		Method: "process/stop",
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "process-stop", s.appStatus())
	if s.console != nil {
		s.console.Event("process.stop", map[string]any{
			"pid": app.pid,
		})
	}
}

func (a *runningApp) interrupt() error {
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	return interruptProcessTree(a.cmd)
}

func (a *runningApp) kill() error {
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	return killProcessTree(a.cmd)
}

func (a *runningApp) waitOrKill(grace time.Duration) error {
	if a == nil {
		return nil
	}
	select {
	case err := <-a.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		_ = a.kill()
		select {
		case err := <-a.done:
			if err == nil || isExpectedExit(err) {
				return nil
			}
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("app did not exit after SIGKILL")
		}
	}
}

func (a *runningApp) stop() error {
	if err := a.interrupt(); err != nil {
		return err
	}
	return a.waitOrKill(stopTimeout)
}

func isExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func validateLocalSecretsFiles(root string) error {
	if err := requireDotEnv(root); err != nil {
		return err
	}
	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := envfile.ParseFile(path); err != nil {
			return err
		}
	}
	return nil
}

func requireDotEnv(root string) error {
	path := filepath.Join(root, ".env")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("missing required local env file: %s\ncreate .env in the app root before starting onlava locally; process environment values may still override values from the file", path)
	}
	if err != nil {
		return fmt.Errorf("check required local env file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("required local env file is a directory: %s", path)
	}
	return nil
}

func (s *devSupervisor) persistStatus(ctx context.Context) error {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	return s.store.UpsertApp(ctx, status)
}

func (s *devSupervisor) setCompiling(compiling bool, compileErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Compiling = compiling
	s.status.CompileError = compileErr
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) setMetadata(metadata, apiEncoding json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Metadata = metadata
	s.status.APIEncoding = apiEncoding
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) setRunning(pid string, metadata, apiEncoding json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = true
	s.status.Offline = false
	s.status.PID = pid
	s.status.ListenAddr = s.addr
	s.status.Metadata = metadata
	s.status.APIEncoding = apiEncoding
	s.status.CompileError = ""
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) setGrafanaState(state devdash.GrafanaState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Grafana = encodeGrafanaState(state)
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) currentApp() *runningApp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *devSupervisor) detachCurrentApp() *runningApp {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current
	s.current = nil
	return current
}

func (s *devSupervisor) currentDBStudio() *dbstudio.Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbStudio
}

func (s *devSupervisor) announceRebuild(paths []string) {
	if s.console != nil {
		s.console.RebuildDetected(paths)
	}
}

func (s *devSupervisor) apiURL() string {
	if s.proxy != nil && s.proxy.Routes().APIURL != "" {
		return s.proxy.Routes().APIURL
	}
	return "http://" + s.addr
}

func (s *devSupervisor) dashboardURL() string {
	if s.proxy != nil && s.proxy.Routes().ConsoleURL != "" {
		return localproxy.ConsoleAppURL(s.proxy.Routes(), s.activeAppID())
	}
	return "http://" + devdash.ListenAddr() + "/" + url.PathEscape(s.activeAppID())
}

func (s *devSupervisor) mcpURL() string {
	if s.proxy != nil && s.proxy.Routes().MCPBaseURL != "" {
		return localproxy.MCPSSEURL(s.proxy.Routes(), s.activeAppID())
	}
	return "http://" + devdash.ListenAddr() + "/sse?appID=" + url.QueryEscape(s.activeAppID())
}

func (s *devSupervisor) temporalURL() string {
	if s.proxy != nil && s.proxy.Routes().TemporalURL != "" {
		return s.proxy.Routes().TemporalURL
	}
	return s.temporal.URL()
}

func (s *devSupervisor) grafanaUpstream() string {
	if s == nil || s.grafana == nil {
		return ""
	}
	return s.grafana.URL()
}

func (s *devSupervisor) frontendURLs() map[string]string {
	if s.proxy != nil {
		return frontendURLs(s.proxy.Routes())
	}
	return nil
}

func (s *devSupervisor) startDBStudio(ctx context.Context) error {
	cfg, ok, err := dbstudio.Discover(s.root)
	if err != nil || !ok {
		return err
	}
	s.dbStudioURL = dbStudioDirectURL(dbstudio.DefaultPort)
	if uiDir, uiErr := prepareDBStudioUIDir(ctx, s.console); uiErr != nil {
		slog.Warn("db studio UI unavailable", "err", uiErr)
	} else if dbStudioUIAssetsAvailable(uiDir) {
		uiServer := newDBStudioUIServer(uiDir)
		if startErr := uiServer.Start(ctx); startErr != nil {
			slog.Warn("db studio UI unavailable", "err", startErr)
		} else {
			s.dbStudioUI = uiServer
			s.dbStudioURL = dbStudioUIURL(dbstudio.DefaultPort)
		}
	}
	go func() {
		inst, startErr := dbstudio.Start(ctx, dbstudio.Options{
			AppRoot: s.root,
			AppID:   s.activeAppID(),
			Config:  cfg,
			Port:    dbstudio.DefaultPort,
			Verbose: s.console != nil && s.console.verbose,
			Stdout:  os.Stdout,
			Stderr:  os.Stderr,
		})
		if startErr != nil {
			slog.Warn("db studio unavailable", "err", startErr)
			return
		}
		if inst == nil {
			return
		}
		s.mu.Lock()
		s.dbStudio = inst
		s.mu.Unlock()
	}()
	return nil
}

func (s *devSupervisor) ensureTemporalDevServer(ctx context.Context) error {
	if s == nil || s.temporal != nil {
		return nil
	}
	temporal, err := startTemporalDevServer(ctx, s.root, s.cfg, s.console)
	if err != nil {
		return err
	}
	s.temporal = temporal
	return nil
}

func (s *devSupervisor) startGrafana(ctx context.Context) error {
	if s == nil || s.grafana != nil {
		return nil
	}
	grafana, err := startGrafanaForDev(ctx, s.root, s.victoria, s.plannedGrafanaPublicURL(), s.console)
	if grafana != nil {
		s.grafana = grafana
		s.setGrafanaState(grafana.State())
		_ = s.persistStatus(ctx)
		if s.dashboard != nil {
			s.dashboard.notify(&devdash.Notification{
				Method: "grafana/status",
				Params: grafana.State(),
			})
		}
	}
	return err
}

func (s *devSupervisor) plannedGrafanaPublicURL() string {
	if s == nil || !localproxy.Enabled() {
		return ""
	}
	workspace := s.cfg.Proxy.Workspace
	if workspace == "" {
		workspace = localproxy.DiscoverWorkspace(s.root, s.activeAppID())
	}
	proxyCfg := localproxy.BuildConfig(localproxy.Config{
		Workspace:         workspace,
		APIHost:           s.cfg.Proxy.APIHost,
		ConsoleHost:       s.cfg.Proxy.ConsoleHost,
		MCPHost:           s.cfg.Proxy.MCPHost,
		TemporalHost:      s.cfg.Proxy.TemporalHost,
		GrafanaHost:       s.cfg.Proxy.GrafanaHost,
		APIUpstream:       s.addr,
		DashboardUpstream: devdash.ListenAddr(),
		TemporalUpstream:  temporalUIUpstreamForConfig(s.cfg),
		GrafanaUpstream:   fmt.Sprintf("%s:%d", grafanaDefaultHost, grafanaDefaultPort),
	})
	if proxyCfg.Workspace == "" && proxyCfg.APIHost == "" {
		return ""
	}
	return localproxy.PreviewRoutes(proxyCfg).GrafanaURL
}

func (s *devSupervisor) activeAppID() string {
	return s.cfg.AppID()
}

func (s *devSupervisor) setAppIdentity(cfg app.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.ID = cfg.AppID()
	s.status.Name = cfg.Name
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) startLocalProxy() error {
	if !localproxy.Enabled() {
		return nil
	}
	workspace := s.cfg.Proxy.Workspace
	if workspace == "" {
		workspace = localproxy.DiscoverWorkspace(s.root, s.activeAppID())
	}
	proxyCfg := localproxy.BuildConfig(localproxy.Config{
		Workspace:         workspace,
		APIHost:           s.cfg.Proxy.APIHost,
		ConsoleHost:       s.cfg.Proxy.ConsoleHost,
		MCPHost:           s.cfg.Proxy.MCPHost,
		TemporalHost:      s.cfg.Proxy.TemporalHost,
		GrafanaHost:       s.cfg.Proxy.GrafanaHost,
		APIUpstream:       s.addr,
		DashboardUpstream: devdash.ListenAddr(),
		TemporalUpstream:  temporalUIUpstreamForConfig(s.cfg),
		GrafanaUpstream:   s.grafanaUpstream(),
		Frontends:         localproxy.ResolveFrontends(s.root, localProxyFrontends(s.cfg.Proxy.Frontends)),
		Verbose:           s.console != nil && s.console.verbose,
	})
	if proxyCfg.Workspace == "" && proxyCfg.APIHost == "" {
		return nil
	}
	proxy, err := localproxy.Start(proxyCfg)
	if err != nil {
		return err
	}
	s.proxy = proxy
	if s.grafana != nil && proxy.Routes().GrafanaURL != "" {
		state := grafanaStateWithBaseURL(s.grafana.State(), proxy.Routes().GrafanaURL)
		s.setGrafanaState(state)
		s.dashboard.notify(&devdash.Notification{
			Method: "grafana/status",
			Params: state,
		})
	}
	return nil
}

func localProxyFrontends(frontends map[string]app.FrontendConfig) []localproxy.FrontendConfig {
	names := make([]string, 0, len(frontends))
	for name := range frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := make([]localproxy.FrontendConfig, 0, len(names))
	for _, name := range names {
		frontend := frontends[name]
		resolved = append(resolved, localproxy.FrontendConfig{
			Name:     name,
			Host:     frontend.Host,
			Root:     frontend.Root,
			Upstream: frontend.Upstream,
		})
	}
	return resolved
}

func frontendURLs(routes localproxy.Routes) map[string]string {
	if len(routes.Frontends) == 0 {
		return nil
	}
	names := make([]string, 0, len(routes.Frontends))
	for name := range routes.Frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	urls := make(map[string]string, len(names))
	for _, name := range names {
		urls[name] = routes.Frontends[name].URL
	}
	return urls
}

func (s *devSupervisor) runURLs() runURLs {
	return runURLs{
		API:       s.apiURL(),
		Dashboard: s.dashboardURL(),
		MCP:       s.mcpURL(),
		Frontends: s.frontendURLs(),
		DBStudio:  s.dbStudioURL,
		Temporal:  s.temporalURL(),
		Victoria:  s.victoria.URLs(),
		Grafana:   s.appStatus().Grafana,
	}
}

func (s *devSupervisor) handleCompileError(ctx context.Context, metadata, apiEncoding json.RawMessage, err error) error {
	s.setCompiling(false, err.Error())
	if len(metadata) > 0 || len(apiEncoding) > 0 {
		s.setMetadata(metadata, apiEncoding)
	}
	_ = s.persistStatus(ctx)
	s.dashboard.notify(&devdash.Notification{
		Method: "process/compile-error",
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "compile-error", map[string]any{"error": err.Error()})
	if s.console != nil {
		s.console.Event("process.compile-error", map[string]any{
			"error": err.Error(),
		})
	}
	return err
}

func (s *devSupervisor) appStatus() devdash.AppStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return devdash.AppStatus{
		Running:      s.status.Running,
		AppID:        s.status.ID,
		AppRoot:      s.status.Root,
		PID:          s.status.PID,
		Meta:         s.status.Metadata,
		Addr:         s.status.ListenAddr,
		APIEncoding:  s.status.APIEncoding,
		Grafana:      decodeGrafanaState(s.status.Grafana),
		Compiling:    s.status.Compiling,
		CompileError: s.status.CompileError,
	}
}

func (s *devSupervisor) listApps(ctx context.Context) ([]map[string]any, error) {
	appID := s.activeAppID()
	if appID == "" {
		return []map[string]any{}, nil
	}
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		status := s.appStatus()
		return []map[string]any{{
			"id":           status.AppID,
			"name":         status.AppID,
			"app_root":     status.AppRoot,
			"offline":      !status.Running,
			"compileError": status.CompileError,
		}}, nil
	}
	return []map[string]any{{
		"id":           app.ID,
		"name":         app.Name,
		"app_root":     app.Root,
		"offline":      !app.Running,
		"compileError": app.CompileError,
	}}, nil
}

func (s *devSupervisor) statusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	if appID == "" {
		appID = s.activeAppID()
	}
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		return devdash.AppStatus{}, err
	}
	return devdash.AppStatus{
		Running:      app.Running,
		AppID:        app.ID,
		AppRoot:      app.Root,
		PID:          app.PID,
		Meta:         app.Metadata,
		Addr:         app.ListenAddr,
		APIEncoding:  app.APIEncoding,
		Grafana:      decodeGrafanaState(app.Grafana),
		Compiling:    app.Compiling,
		CompileError: app.CompileError,
	}, nil
}

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func portAvailable(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

func localPath(root, target string) (string, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("file path must be within the app root")
	}
	return target, nil
}
