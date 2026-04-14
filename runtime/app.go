package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func ListenAddrFromEnv() string {
	if value := os.Getenv("PULSE_LISTEN_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:4000"
}

func Main(cfg AppConfig) error {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ListenAddrFromEnv()
	}
	SetAppConfig(cfg)
	stopReporting := startDevelopmentReporting(cfg)
	defer stopReporting()
	FlushMissingSecretsWarnings()

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	server, err := newServer(cfg.ListenAddr)
	if err != nil {
		return err
	}
	scheduler := startCronScheduler(runCtx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	logTrace(context.Background(), fmt.Sprintf("registered %d API endpoints", len(listEndpoints())))
	logTrace(context.Background(), "listening for incoming HTTP requests")

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		cancelRun()
		cronCtx, cronCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cronCancel()
		if err := scheduler.Stop(cronCtx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		cancelRun()
		cronCtx, cronCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cronCancel()
		if stopErr := scheduler.Stop(cronCtx); stopErr != nil && !errors.Is(stopErr, context.Canceled) {
			return stopErr
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
