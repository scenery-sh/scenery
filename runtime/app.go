package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

func ListenAddrFromEnv() string {
	if value := os.Getenv("ONLAVA_LISTEN_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:4000"
}

func Main(cfg AppConfig) error {
	role, err := runtimeRoleFromEnv()
	if err != nil {
		return err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ListenAddrFromEnv()
	}
	cfg.Role = string(role)
	SetAppConfig(cfg)
	stopReporting := startDevelopmentReporting(cfg)
	defer stopReporting()
	FlushMissingSecretsWarnings()

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	stopSupervisorMonitor := startSupervisorParentMonitor(cancelRun)
	defer stopSupervisorMonitor()

	stopTemporal, err := StartTemporalRuntime(runCtx, cfg)
	if err != nil {
		return err
	}
	if err := InitializeServices(); err != nil {
		_ = stopTemporal(context.Background())
		return err
	}
	stopTemporalWorkers, err := startTemporalWorkerRuntime(runCtx, cfg)
	if err != nil {
		_ = stopTemporal(context.Background())
		return err
	}
	scheduler, err := startCronScheduler(runCtx, cfg)
	if err != nil {
		_ = stopTemporalWorkers(context.Background())
		_ = stopTemporal(context.Background())
		return err
	}

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-sigCtx.Done()
		stopSignals()
		cancelRun()
	}()

	if role == runtimeRoleWorker {
		logTrace(context.Background(), "worker runtime started")
		<-runCtx.Done()
		cancelRun()
		return shutdownRuntime(nil, stopTemporalWorkers, stopTemporal, scheduler)
	}

	server, err := newServer(cfg.ListenAddr)
	if err != nil {
		cancelRun()
		return shutdownRuntime(nil, stopTemporalWorkers, stopTemporal, scheduler)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	if !launchedBySupervisor() {
		info := StandaloneDevInfo{}
		if standaloneDevStarter != nil {
			session, started, err := standaloneDevStarter(runCtx, cfg)
			if err != nil {
				slog.Warn("standalone dev runtime unavailable", "err", err)
			}
			if session != nil {
				defer func() {
					_ = session.Close()
				}()
			}
			info = started
			if info.APIURL != "" {
				SetPublicBaseURL(info.APIURL)
			}
		}
		printRuntimeBanner(osStdout(), cfg.ListenAddr, info)
	}

	logTrace(context.Background(), fmt.Sprintf("registered %d API endpoints", len(listEndpoints())))
	logTrace(context.Background(), "listening for incoming HTTP requests")

	select {
	case <-runCtx.Done():
		cancelRun()
		return shutdownRuntime(server, stopTemporalWorkers, stopTemporal, scheduler)
	case err := <-errCh:
		cancelRun()
		stopErr := shutdownRuntime(server, stopTemporalWorkers, stopTemporal, scheduler)
		if errors.Is(err, http.ErrServerClosed) {
			return stopErr
		}
		return errorsJoin(err, stopErr)
	}
}

type runtimeRole string

const (
	runtimeRoleAll    runtimeRole = "all"
	runtimeRoleAPI    runtimeRole = "api"
	runtimeRoleWorker runtimeRole = "worker"
)

func runtimeRoleFromEnv() (runtimeRole, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("ONLAVA_ROLE")))
	switch value {
	case "", string(runtimeRoleAll):
		return runtimeRoleAll, nil
	case string(runtimeRoleAPI):
		return runtimeRoleAPI, nil
	case string(runtimeRoleWorker):
		return runtimeRoleWorker, nil
	default:
		return "", fmt.Errorf("runtime: unsupported ONLAVA_ROLE %q", value)
	}
}

func shutdownRuntime(server *http.Server, stopTemporalWorkers func(context.Context) error, stopTemporal func(context.Context) error, scheduler *cronScheduler) error {
	var shutdownErrs []error

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if server != nil {
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			shutdownErrs = append(shutdownErrs, err)
		}
	}

	cronCtx, cronCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cronCancel()
	if scheduler != nil {
		if err := scheduler.Stop(cronCtx); err != nil && !errors.Is(err, context.Canceled) {
			shutdownErrs = append(shutdownErrs, err)
		}
	}

	temporalWorkersCtx, temporalWorkersCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer temporalWorkersCancel()
	if stopTemporalWorkers != nil {
		if err := stopTemporalWorkers(temporalWorkersCtx); err != nil && !errors.Is(err, context.Canceled) {
			shutdownErrs = append(shutdownErrs, err)
		}
	}

	temporalCtx, temporalCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer temporalCancel()
	if stopTemporal != nil {
		if err := stopTemporal(temporalCtx); err != nil && !errors.Is(err, context.Canceled) {
			shutdownErrs = append(shutdownErrs, err)
		}
	}

	serviceCtx, serviceCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer serviceCancel()
	if err := ShutdownServices(serviceCtx); err != nil && !errors.Is(err, context.Canceled) {
		shutdownErrs = append(shutdownErrs, err)
	}

	return errorsJoin(shutdownErrs...)
}

func launchedBySupervisor() bool {
	return os.Getenv("ONLAVA_DEV_SUPERVISOR") == "1"
}

func printRuntimeBanner(out io.Writer, listenAddr string, info StandaloneDevInfo) {
	if out == nil {
		return
	}
	apiURL := "http://" + listenAddr
	if info.APIURL != "" {
		apiURL = info.APIURL
	}

	title := "onlava server running!"
	if info.APIURL != "" || info.ConsoleURL != "" || info.MCPBaseURL != "" || len(info.FrontendURLs) > 0 || info.DBStudioURL != "" {
		title = "onlava development server running!"
	}

	lines := []string{
		"",
		"  " + title,
		"",
		fmt.Sprintf("  %-26s  %s", "Your API is running at:", apiURL),
	}
	if info.ConsoleURL != "" {
		lines = append(lines, fmt.Sprintf("  %-26s  %s", "Development Dashboard URL:", info.ConsoleURL))
	}
	if info.MCPBaseURL != "" {
		lines = append(lines, fmt.Sprintf("  %-26s  %s", "MCP SSE URL:", info.MCPBaseURL+"/sse?appID="+Meta().AppID))
	}
	for _, name := range sortedStandaloneFrontendNames(info.FrontendURLs) {
		lines = append(lines, fmt.Sprintf("  %-26s  %s", standaloneFrontendLabel(name), info.FrontendURLs[name]))
	}
	if info.DBStudioURL != "" {
		lines = append(lines, fmt.Sprintf("  %-26s  %s", "Drizzle Studio URL:", info.DBStudioURL))
	}
	lines = append(lines, "")
	for _, line := range lines {
		_, _ = fmt.Fprintln(out, line)
	}
}

func sortedStandaloneFrontendNames(frontends map[string]string) []string {
	if len(frontends) == 0 {
		return nil
	}
	names := make([]string, 0, len(frontends))
	for name := range frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func standaloneFrontendLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Frontend URL:"
	}
	return "Frontend " + name + " URL:"
}

var osStdout = func() io.Writer { return os.Stdout }
