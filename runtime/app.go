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
	"sync"
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
	cliRequestPath, err := contractCLIRequestPath(os.Args[1:])
	if err != nil {
		return err
	}
	role, err := runtimeRoleFromEnv()
	if err != nil {
		return err
	}
	if err := requireValidProcessLink(); err != nil {
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

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	stopSupervisorMonitor := startSupervisorParentMonitor(cancelRun)
	defer stopSupervisorMonitor()

	if err := InitializeServices(); err != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		return errorsJoin(err, ShutdownServices(shutdownCtx))
	}
	durable, err := openDurableRuntime(runCtx, cfg)
	if err != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		return errorsJoin(err, ShutdownServices(shutdownCtx))
	}
	background := &runtimeBackground{ctx: runCtx, durable: durable}
	// A service process of a process-model session serves requests before its
	// generation is published and acquires background work only when the
	// supervisor activates it afterwards.
	processLinked := processLinkConfigured()
	if processLinked && role == runtimeRoleWorker {
		// Activation reaches a process-linked runtime through its listener.
		return errorsJoin(fmt.Errorf("runtime: a process-linked runtime cannot use SCENERY_ROLE=worker"), shutdownRuntime(nil, background))
	}
	if !processLinked {
		durable.StartBackground()
	}
	if cliRequestPath != "" {
		invokeErr := ExecuteContractCLIRequest(cliRequestPath, os.Stdout)
		cancelRun()
		return errorsJoin(invokeErr, shutdownRuntime(nil, background))
	}
	if processLinked {
		setProcessBackground(background)
		defer setProcessBackground(nil)
	} else if err := background.activate(); err != nil {
		return errorsJoin(err, shutdownRuntime(nil, background))
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
		return shutdownRuntime(nil, background)
	}

	server, err := newServer(cfg.ListenAddr)
	if err != nil {
		cancelRun()
		return shutdownRuntime(nil, background)
	}
	ln, err := listenRuntime(listenNetwork, cfg.ListenAddr)
	if err != nil {
		cancelRun()
		return errorsJoin(err, shutdownRuntime(nil, background))
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
		return shutdownRuntime(server, background)
	case err := <-errCh:
		cancelRun()
		stopErr := shutdownRuntime(server, background)
		if errors.Is(err, http.ErrServerClosed) {
			return stopErr
		}
		return errorsJoin(err, stopErr)
	}
}

func contractCLIRequestPath(arguments []string) (string, error) {
	for _, argument := range arguments {
		if argument == "--scenery-contract-cli-request" || strings.HasPrefix(argument, "--scenery-contract-cli-request=") {
			if len(arguments) == 2 && arguments[0] == "--scenery-contract-cli-request" && strings.TrimSpace(arguments[1]) != "" {
				return arguments[1], nil
			}
			if len(arguments) == 1 && strings.HasPrefix(arguments[0], "--scenery-contract-cli-request=") {
				path := strings.TrimSpace(strings.TrimPrefix(arguments[0], "--scenery-contract-cli-request="))
				if path != "" {
					return path, nil
				}
			}
			return "", fmt.Errorf("runtime: invalid contract CLI request arguments")
		}
	}
	return "", nil
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

// runtimeBackground owns the background work one runtime process acquires:
// contract event consumers, cron schedules and durable acquisition, schedule
// and retention loops. It starts inactive; activate starts the work once, and
// drain revokes it for the life of the process while durable stores stay open
// for dispatch by requests still served.
type runtimeBackground struct {
	ctx     context.Context
	durable *durableRuntime

	mu        sync.Mutex
	state     runtimeBackgroundState
	events    *ContractEventRuntime
	scheduler *cronScheduler
}

type runtimeBackgroundState int

const (
	runtimeBackgroundInactive runtimeBackgroundState = iota
	runtimeBackgroundActive
	runtimeBackgroundRevoked
)

func (background *runtimeBackground) activate() error {
	background.mu.Lock()
	defer background.mu.Unlock()
	switch background.state {
	case runtimeBackgroundActive:
		return nil
	case runtimeBackgroundRevoked:
		return errors.New("failed_precondition: background work of this runtime process was drained")
	}
	events, err := StartContractEventRuntime(background.ctx)
	if err != nil {
		return err
	}
	scheduler, err := startCronScheduler(background.ctx)
	if err != nil {
		return errorsJoin(err, events.Stop(context.Background()))
	}
	background.durable.StartBackground()
	background.events, background.scheduler, background.state = events, scheduler, runtimeBackgroundActive
	return nil
}

// drain revokes background work at once and waits until ctx ends for work
// already running to stop.
func (background *runtimeBackground) drain(ctx context.Context) error {
	background.mu.Lock()
	background.state = runtimeBackgroundRevoked
	events, scheduler := background.events, background.scheduler
	background.mu.Unlock()
	return errorsJoin(scheduler.Stop(ctx), events.Stop(ctx), background.durable.StopBackground(ctx))
}

func shutdownRuntime(server *http.Server, background *runtimeBackground) error {
	var shutdownErrs []error

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if server != nil {
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			shutdownErrs = append(shutdownErrs, err)
		}
	}

	background.mu.Lock()
	background.state = runtimeBackgroundRevoked
	events, scheduler := background.events, background.scheduler
	background.mu.Unlock()

	cronCtx, cronCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cronCancel()
	if err := scheduler.Stop(cronCtx); err != nil && !errors.Is(err, context.Canceled) {
		shutdownErrs = append(shutdownErrs, err)
	}

	eventCtx, eventCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer eventCancel()
	if err := events.Stop(eventCtx); err != nil && !errors.Is(err, context.Canceled) {
		shutdownErrs = append(shutdownErrs, err)
	}

	durableCtx, durableCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer durableCancel()
	if err := background.durable.Close(durableCtx); err != nil && !errors.Is(err, context.Canceled) {
		shutdownErrs = append(shutdownErrs, err)
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
