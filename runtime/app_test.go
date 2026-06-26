package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintRuntimeBanner(t *testing.T) {
	SetAppConfig(AppConfig{Name: "testapp", ListenAddr: "127.0.0.1:4000"})

	var out bytes.Buffer
	printRuntimeBanner(&out, "127.0.0.1:4000")

	text := out.String()
	for _, want := range []string{
		"scenery server running!",
		"Your API is running at:",
		"http://127.0.0.1:4000",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("banner %q missing %q", text, want)
		}
	}
}

func TestLaunchedBySupervisor(t *testing.T) {
	t.Setenv("SCENERY_DEV_SUPERVISOR", "1")
	if !launchedBySupervisor() {
		t.Fatal("expected launchedBySupervisor to be true")
	}
}

func TestRuntimeRoleFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  runtimeRole
	}{
		{name: "default", want: runtimeRoleAll},
		{name: "all", value: "all", want: runtimeRoleAll},
		{name: "api", value: "api", want: runtimeRoleAPI},
		{name: "worker", value: "worker", want: runtimeRoleWorker},
		{name: "trimmed uppercase", value: " WORKER ", want: runtimeRoleWorker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SCENERY_ROLE", tt.value)
			got, err := runtimeRoleFromEnv()
			if err != nil {
				t.Fatalf("runtimeRoleFromEnv() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("runtimeRoleFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRuntimeRoleFromEnvRejectsUnknown(t *testing.T) {
	t.Setenv("SCENERY_ROLE", "web")
	if _, err := runtimeRoleFromEnv(); err == nil {
		t.Fatal("expected unsupported role error")
	}
}

func TestListenRuntimeUnixSocket(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "runtime.sock")
	ln, err := listenRuntime("unix", socketPath)
	if err != nil {
		t.Fatalf("listenRuntime unix: %v", err)
	}
	defer ln.Close()
	if ln.Addr().Network() != "unix" {
		t.Fatalf("network = %q, want unix", ln.Addr().Network())
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket file missing: %v", err)
	}
}

func TestSupervisorParentMonitorShouldCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		supervisorPID   int
		supervisorAlive bool
		initial         int
		current         int
		want            bool
	}{
		{name: "exact supervisor alive", supervisorPID: 999, supervisorAlive: true, initial: 123, current: 1, want: false},
		{name: "exact supervisor missing", supervisorPID: 999, supervisorAlive: false, initial: 123, current: 123, want: true},
		{name: "same parent fallback", initial: 123, current: 123, want: false},
		{name: "reparented to pid1 fallback", initial: 123, current: 1, want: true},
		{name: "reparented elsewhere fallback", initial: 123, current: 456, want: true},
		{name: "initial pid1 ignored", initial: 1, current: 1, want: false},
		{name: "invalid current ignored", initial: 123, current: 0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supervisorParentMonitorShouldCancel(tt.supervisorPID, tt.supervisorAlive, tt.initial, tt.current); got != tt.want {
				t.Fatalf("supervisorParentMonitorShouldCancel(%d, %v, %d, %d) = %v, want %v", tt.supervisorPID, tt.supervisorAlive, tt.initial, tt.current, got, tt.want)
			}
		})
	}
}

func TestSupervisorPIDFromEnv(t *testing.T) {
	t.Setenv("SCENERY_DEV_SUPERVISOR_PID", "4321")
	if got := supervisorPIDFromEnv(); got != 4321 {
		t.Fatalf("supervisorPIDFromEnv() = %d, want 4321", got)
	}

	t.Setenv("SCENERY_DEV_SUPERVISOR_PID", "bad")
	if got := supervisorPIDFromEnv(); got != 0 {
		t.Fatalf("supervisorPIDFromEnv() with invalid value = %d, want 0", got)
	}
}

func TestStartTemporalWorkerRuntimeUsesRegisteredStarter(t *testing.T) {
	prev := temporalWorkerStarter
	defer func() { temporalWorkerStarter = prev }()

	called := false
	stopped := false
	temporalWorkerStarter = func(ctx context.Context, cfg AppConfig) (func(context.Context) error, error) {
		called = true
		if cfg.Role != "worker" {
			t.Fatalf("starter cfg.Role = %q, want worker", cfg.Role)
		}
		return func(context.Context) error {
			stopped = true
			return nil
		}, nil
	}

	stop, err := startTemporalWorkerRuntime(context.Background(), AppConfig{
		Name:     "testapp",
		Role:     "worker",
		Temporal: TemporalConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("startTemporalWorkerRuntime() error = %v", err)
	}
	if !called {
		t.Fatal("expected registered starter to be called")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop() error = %v", err)
	}
	if !stopped {
		t.Fatal("expected stop callback to be invoked")
	}
}

func TestStartTemporalWorkerRuntimeDisabledNoops(t *testing.T) {
	prev := temporalWorkerStarter
	defer func() { temporalWorkerStarter = prev }()

	called := false
	temporalWorkerStarter = func(ctx context.Context, cfg AppConfig) (func(context.Context) error, error) {
		called = true
		return func(context.Context) error { return nil }, nil
	}

	stop, err := startTemporalWorkerRuntime(context.Background(), AppConfig{Name: "testapp", Role: "worker"})
	if err != nil {
		t.Fatalf("startTemporalWorkerRuntime() error = %v", err)
	}
	if called {
		t.Fatal("expected disabled temporal worker runtime to skip registered starter")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop() error = %v", err)
	}
}
