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
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"pulse.dev/internal/app"
	"pulse.dev/internal/build"
	"pulse.dev/internal/devdash"
	"pulse.dev/internal/devmeta"
	"pulse.dev/internal/model"
	"pulse.dev/internal/parse"
)

type runningApp struct {
	cmd      *exec.Cmd
	done     chan error
	buildDir string
	pid      string
}

type devSupervisor struct {
	root string
	cfg  app.Config
	addr string

	store       *devdash.Store
	dashboard   *dashboardServer
	reportToken string
	console     *runConsole

	mu      sync.RWMutex
	current *runningApp
	status  devdash.AppRecord
}

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func newDevSupervisor(root string, cfg app.Config, addr string) (*devSupervisor, error) {
	store, err := devdash.OpenStore(os.Getenv("PULSE_DEV_CACHE_DIR"))
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	s := &devSupervisor{
		root:        root,
		cfg:         cfg,
		addr:        addr,
		store:       store,
		reportToken: token,
		console:     newRunConsole(os.Stdout, os.Stderr),
		status: devdash.AppRecord{
			ID:         cfg.Name,
			Name:       cfg.Name,
			Root:       root,
			ListenAddr: addr,
			Offline:    true,
			UpdatedAt:  time.Now().UTC(),
		},
	}
	s.dashboard = newDashboardServer(s)
	return s, nil
}

func (s *devSupervisor) Close() error {
	var errs []error
	if s.dashboard != nil {
		if err := s.dashboard.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.stopCurrent(); err != nil {
		errs = append(errs, err)
	}
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *devSupervisor) Start(ctx context.Context) error {
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	return s.dashboard.Start(ctx)
}

func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool) error {
	s.setCompiling(true, "")
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.dashboard.notify(&devdash.Notification{
		Method: "process/compile-start",
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.cfg.Name, "compile-start", s.appStatus())

	var (
		model       *model.App
		metadata    json.RawMessage
		apiEncoding json.RawMessage
		result      *build.Result
		err         error
	)
	if err := s.console.Phase("Building Pulse application graph", func() error {
		model, err = parse.App(s.root, s.cfg.Name)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if err := s.console.Phase("Analyzing service topology", func() error {
		metadata, err = devmeta.BuildMetadataSnapshot(model)
		if err != nil {
			return err
		}
		apiEncoding, err = devmeta.BuildAPIEncoding(model)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if err := s.console.Phase("Reading local secrets", func() error {
		return validateLocalSecretsFiles(s.root)
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Generating boilerplate code", func() error {
		result, err = build.Prepare(s.root, model)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Compiling application source code", func() error {
		return build.Compile(result)
	}); err != nil {
		if result != nil {
			_ = os.RemoveAll(result.Dir)
		}
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}

	previous := s.currentApp()
	var current *runningApp
	if err := s.console.Phase("Starting Pulse application", func() error {
		current, err = s.startApp(ctx, result, metadata, apiEncoding)
		return err
	}); err != nil {
		if result != nil {
			_ = os.RemoveAll(result.Dir)
		}
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if previous != nil {
		if err := previous.stop(); err != nil {
			return err
		}
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
	_ = s.store.WriteProcessEvent(ctx, s.cfg.Name, method, s.appStatus())
	if initial {
		s.console.Banner(s.apiURL(), s.dashboardURL(), s.mcpURL())
	}
	return nil
}

func (s *devSupervisor) startApp(ctx context.Context, result *build.Result, metadata, apiEncoding json.RawMessage) (*runningApp, error) {
	cmd := exec.Command(result.Binary)
	cmd.Dir = s.root
	cmd.Env = appChildEnv(
		os.Environ(),
		s.console != nil && s.console.palette.Enabled(),
		"PULSE_LISTEN_ADDR="+s.addr,
		"PULSE_APP_ID="+s.cfg.Name,
		"PULSE_APP_ROOT="+s.root,
		"PULSE_DEV_REPORT_URL=http://"+devdash.ListenAddr()+devdash.ReportPath,
		"PULSE_DEV_REPORT_TOKEN="+s.reportToken,
	)
	cmd.Stdin = os.Stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	app := &runningApp{
		cmd:      cmd,
		done:     make(chan error, 1),
		buildDir: result.Dir,
		pid:      fmt.Sprintf("%d", cmd.Process.Pid),
	}

	go s.captureOutput(ctx, app.pid, "stdout", stdout, os.Stdout)
	go s.captureOutput(ctx, app.pid, "stderr", stderr, os.Stderr)
	go func() {
		app.done <- cmd.Wait()
		close(app.done)
		s.handleExit(context.Background(), app)
	}()
	return app, nil
}

func (s *devSupervisor) captureOutput(ctx context.Context, pid, stream string, src io.Reader, dst io.Writer) {
	reader := bufio.NewReader(src)
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			_, _ = dst.Write(chunk)
			plain := stripANSI(chunk)
			output := devdash.ProcessOutput{
				AppID:     s.cfg.Name,
				PID:       pid,
				Stream:    stream,
				Output:    plain,
				CreatedAt: time.Now().UTC(),
			}
			_ = s.store.WriteProcessOutput(ctx, output)
			s.dashboard.notify(&devdash.Notification{
				Method: "process/output",
				Params: map[string]any{
					"appID":  s.cfg.Name,
					"pid":    pid,
					"output": output.Output,
				},
			})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintf(os.Stderr, "pulse: failed reading %s: %v\n", stream, err)
			}
			return
		}
	}
}

func appChildEnv(base []string, forceColor bool, vars ...string) []string {
	env := append(append([]string(nil), base...), vars...)
	if forceColor {
		env = append(env, "CLICOLOR_FORCE=1")
	}
	return env
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
	_ = s.store.WriteProcessEvent(ctx, s.cfg.Name, "process-stop", s.appStatus())
}

func (s *devSupervisor) stopCurrent() error {
	s.mu.Lock()
	current := s.current
	s.current = nil
	s.mu.Unlock()
	if current == nil {
		return nil
	}
	return current.stop()
}

func (s *runningApp) stop() error {
	if s == nil {
		return nil
	}
	defer func() {
		if s.buildDir != "" {
			_ = os.RemoveAll(s.buildDir)
		}
	}()
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-s.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(stopTimeout):
		_ = s.cmd.Process.Signal(syscall.SIGKILL)
		err := <-s.done
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	}
}

func isExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func validateLocalSecretsFiles(root string) error {
	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
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

func (s *devSupervisor) currentApp() *runningApp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *devSupervisor) announceRebuild() {
	if s.console != nil {
		s.console.RebuildDetected()
	}
}

func (s *devSupervisor) apiURL() string {
	return "http://" + s.addr
}

func (s *devSupervisor) dashboardURL() string {
	return "http://" + devdash.ListenAddr() + "/" + url.PathEscape(s.cfg.Name)
}

func (s *devSupervisor) mcpURL() string {
	return "http://" + devdash.ListenAddr() + "/sse?appID=" + url.QueryEscape(s.cfg.Name)
}

func (s *devSupervisor) activeAppID() string {
	return s.cfg.Name
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
	_ = s.store.WriteProcessEvent(ctx, s.cfg.Name, "compile-error", map[string]any{"error": err.Error()})
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
		Compiling:    s.status.Compiling,
		CompileError: s.status.CompileError,
	}
}

func (s *devSupervisor) listApps(ctx context.Context) ([]map[string]any, error) {
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		items = append(items, map[string]any{
			"id":       app.ID,
			"name":     app.Name,
			"app_root": app.Root,
			"offline":  !app.Running,
		})
	}
	return items, nil
}

func (s *devSupervisor) statusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	if appID == "" {
		appID = s.cfg.Name
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
