package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPrintRuntimeBanner(t *testing.T) {
	SetAppConfig(AppConfig{Name: "testapp", ListenAddr: "127.0.0.1:4000"})

	var out bytes.Buffer
	printRuntimeBanner(&out, "127.0.0.1:4000", StandaloneDevInfo{
		APIURL:     "https://api.test.localhost",
		ConsoleURL: "https://console.test.localhost",
		MCPBaseURL: "https://mcp.test.localhost",
		FrontendURLs: map[string]string{
			"pulse": "https://pulse.test.localhost",
		},
		DBStudioURL: "http://127.0.0.1:4002",
	})

	text := out.String()
	for _, want := range []string{
		"onlava development server running!",
		"Your API is running at:",
		"https://api.test.localhost",
		"Development Dashboard URL:",
		"https://console.test.localhost",
		"MCP SSE URL:",
		"https://mcp.test.localhost/sse?appID=testapp",
		"Frontend pulse URL:",
		"https://pulse.test.localhost",
		"Drizzle Studio URL:",
		"http://127.0.0.1:4002",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("banner %q missing %q", text, want)
		}
	}
}

func TestLaunchedBySupervisor(t *testing.T) {
	t.Setenv("ONLAVA_DEV_SUPERVISOR", "1")
	if !launchedBySupervisor() {
		t.Fatal("expected launchedBySupervisor to be true")
	}
}

func TestSupervisorParentMonitorShouldCancel(t *testing.T) {
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
	t.Setenv("ONLAVA_DEV_SUPERVISOR_PID", "4321")
	if got := supervisorPIDFromEnv(); got != 4321 {
		t.Fatalf("supervisorPIDFromEnv() = %d, want 4321", got)
	}

	t.Setenv("ONLAVA_DEV_SUPERVISOR_PID", "bad")
	if got := supervisorPIDFromEnv(); got != 0 {
		t.Fatalf("supervisorPIDFromEnv() with invalid value = %d, want 0", got)
	}
}

func TestStartLocalPubSubRuntimeNoopWhenUnregistered(t *testing.T) {
	prev := localPubSubStarter
	localPubSubStarter = nil
	defer func() { localPubSubStarter = prev }()

	stop, err := startLocalPubSubRuntime(context.Background(), AppConfig{Name: "testapp"})
	if err != nil {
		t.Fatalf("startLocalPubSubRuntime() error = %v", err)
	}
	if stop == nil {
		t.Fatal("startLocalPubSubRuntime() returned nil stop function")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop() error = %v", err)
	}
}

func TestStartLocalPubSubRuntimeUsesRegisteredStarter(t *testing.T) {
	prev := localPubSubStarter
	defer func() { localPubSubStarter = prev }()

	called := false
	stopped := false
	localPubSubStarter = func(ctx context.Context, cfg AppConfig) (func(context.Context) error, error) {
		called = true
		if cfg.Name != "testapp" {
			t.Fatalf("starter cfg.Name = %q, want testapp", cfg.Name)
		}
		return func(context.Context) error {
			stopped = true
			return nil
		}, nil
	}

	stop, err := startLocalPubSubRuntime(context.Background(), AppConfig{Name: "testapp"})
	if err != nil {
		t.Fatalf("startLocalPubSubRuntime() error = %v", err)
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
