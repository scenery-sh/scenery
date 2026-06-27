package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/envpolicy"
)

func ListenAddrFromEnv() string {
	if value := envpolicy.Get("SCENERY_LISTEN_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:4000"
}

func ListenNetworkFromEnv() string {
	switch value := strings.ToLower(strings.TrimSpace(envpolicy.Get("SCENERY_LISTEN_NETWORK"))); value {
	case "", "tcp":
		return "tcp"
	case "unix":
		return "unix"
	default:
		return value
	}
}

func Main(cfg AppConfig) error {
	role, err := runtimeRoleFromEnv()
	if err != nil {
		return err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ListenAddrFromEnv()
	}
	listenNetwork := ListenNetworkFromEnv()
	cfg.Role = string(role)
	SetAppConfig(cfg)
	stopReporting := startDevelopmentReporting(cfg)
	defer stopReporting()
	FlushMissingSecretsWarnings()

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	stopSupervisorMonitor := startSupervisorParentMonitor(cancelRun)
	defer stopSupervisorMonitor()

	stopDurable, err := startDurableRuntime(runCtx, cfg)
	if err != nil {
		return err
	}
	if err := InitializeServices(); err != nil {
		_ = stopDurable(context.Background())
		return err
	}
	scheduler, err := startCronScheduler(runCtx, cfg)
	if err != nil {
		_ = stopDurable(context.Background())
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
		return shutdownRuntime(nil, scheduler, stopDurable)
	}

	server, err := newServer(cfg.ListenAddr)
	if err != nil {
		cancelRun()
		return shutdownRuntime(nil, scheduler, stopDurable)
	}
	ln, err := listenRuntime(listenNetwork, cfg.ListenAddr)
	if err != nil {
		cancelRun()
		return errorsJoin(err, shutdownRuntime(nil, scheduler, stopDurable))
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	if !launchedBySupervisor() {
		printRuntimeBanner(osStdout(), cfg.ListenAddr)
	}

	logTrace(context.Background(), fmt.Sprintf("registered %d API endpoints", len(listEndpoints())))
	logTrace(context.Background(), "listening for incoming HTTP requests")

	select {
	case <-runCtx.Done():
		cancelRun()
		return shutdownRuntime(server, scheduler, stopDurable)
	case err := <-errCh:
		cancelRun()
		stopErr := shutdownRuntime(server, scheduler, stopDurable)
		if errors.Is(err, http.ErrServerClosed) {
			return stopErr
		}
		return errorsJoin(err, stopErr)
	}
}

func listenRuntime(network, addr string) (net.Listener, error) {
	switch network {
	case "", "tcp":
		return net.Listen("tcp", addr)
	case "unix":
		if strings.TrimSpace(addr) == "" {
			return nil, fmt.Errorf("runtime: unix listen address is empty")
		}
		if err := os.MkdirAll(filepath.Dir(addr), 0o755); err != nil {
			return nil, err
		}
		if err := os.Remove(addr); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return net.Listen("unix", addr)
	default:
		return nil, fmt.Errorf("runtime: unsupported SCENERY_LISTEN_NETWORK %q", network)
	}
}

type runtimeRole string

const (
	runtimeRoleAll    runtimeRole = "all"
	runtimeRoleAPI    runtimeRole = "api"
	runtimeRoleWorker runtimeRole = "worker"
)

func runtimeRoleFromEnv() (runtimeRole, error) {
	value := strings.ToLower(strings.TrimSpace(envpolicy.Get("SCENERY_ROLE")))
	switch value {
	case "", string(runtimeRoleAll):
		return runtimeRoleAll, nil
	case string(runtimeRoleAPI):
		return runtimeRoleAPI, nil
	case string(runtimeRoleWorker):
		return runtimeRoleWorker, nil
	default:
		return "", fmt.Errorf("runtime: unsupported SCENERY_ROLE %q", value)
	}
}

func shutdownRuntime(server *http.Server, scheduler *cronScheduler, stopDurable func(context.Context) error) error {
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

	durableCtx, durableCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer durableCancel()
	if stopDurable != nil {
		if err := stopDurable(durableCtx); err != nil && !errors.Is(err, context.Canceled) {
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
	return envpolicy.Get("SCENERY_DEV_SUPERVISOR") == "1"
}

func printRuntimeBanner(out io.Writer, listenAddr string) {
	if out == nil {
		return
	}
	apiURL := "http://" + listenAddr

	lines := []string{
		"",
		"  scenery server running!",
		"",
		fmt.Sprintf("  %-26s  %s", "Your API is running at:", apiURL),
	}
	lines = append(lines, "")
	for _, line := range lines {
		_, _ = fmt.Fprintln(out, line)
	}
}

var osStdout = func() io.Writer { return os.Stdout }
