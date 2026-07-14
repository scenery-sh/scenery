package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

// fakeAgentHealthServer serves /v1/health on the agent socket with a
// controllable PID and dashboard backend, so supervisor cooperation can be
// tested without spawning real agent processes.
type fakeAgentHealthServer struct {
	mu        sync.Mutex
	pid       int
	dashboard localagent.Backend
	server    *http.Server
}

func startFakeAgentHealthServer(t *testing.T, socketPath string, pid int) *fakeAgentHealthServer {
	t.Helper()
	fake := &fakeAgentHealthServer{pid: pid}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, req *http.Request) {
		fake.mu.Lock()
		health := localagent.HealthResponse{
			PID:              fake.pid,
			SocketPath:       socketPath,
			RouterAddr:       "127.0.0.1:9440",
			RouterScheme:     "http",
			DashboardBackend: fake.dashboard,
		}
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(health)
	})
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[]}`))
	})
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	fake.server = &http.Server{Handler: mux}
	go func() { _ = fake.server.Serve(ln) }()
	t.Cleanup(func() { _ = fake.server.Close() })
	return fake
}

func (f *fakeAgentHealthServer) setPID(pid int) {
	f.mu.Lock()
	f.pid = pid
	f.mu.Unlock()
}

func (f *fakeAgentHealthServer) setDashboard(backend localagent.Backend) {
	f.mu.Lock()
	f.dashboard = backend
	f.mu.Unlock()
}

func withAgentSupervisorHooks(t *testing.T, status localagent.LaunchdAgentStatus, kickstart func(bool) error, bootstrap func() error) {
	t.Helper()
	oldStatus := agentSupervisorStatusFunc
	oldKickstart := agentSupervisorKickstartFunc
	oldBootstrap := agentSupervisorBootstrapFunc
	t.Cleanup(func() {
		agentSupervisorStatusFunc = oldStatus
		agentSupervisorKickstartFunc = oldKickstart
		agentSupervisorBootstrapFunc = oldBootstrap
	})
	agentSupervisorStatusFunc = func(socketPath string) localagent.LaunchdAgentStatus { return status }
	if kickstart != nil {
		agentSupervisorKickstartFunc = kickstart
	}
	if bootstrap != nil {
		agentSupervisorBootstrapFunc = bootstrap
	}
}

func TestRestartAgentViaSupervisorKickstartsLoadedJob(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)

	kicked := false
	// The supervisor owns pid 111, so restart must not SIGTERM it directly:
	// kickstart -k replaces it atomically.
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: true,
		Loaded:           true,
		Running:          true,
		PID:              111,
	}, func(kill bool) error {
		if !kill {
			t.Fatal("supervised restart must kickstart with -k")
		}
		kicked = true
		fake.setPID(222)
		return nil
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)
	oldHealth, running := currentAgentHealth(ctx, client)
	if !running || oldHealth.PID != 111 {
		t.Fatalf("old health = %+v, running=%v", oldHealth, running)
	}
	health, supervised, err := restartAgentViaSupervisor(ctx, client, paths, oldHealth, running)
	if err != nil || !supervised || !kicked {
		t.Fatalf("supervised restart = %+v, supervised=%v, kicked=%v, err=%v", health, supervised, kicked, err)
	}
	if health.PID != 222 {
		t.Fatalf("restarted pid = %d, want 222", health.PID)
	}
}

func TestRestartAgentViaSupervisorRepairsUnloadedJob(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 0)

	bootstrapped := false
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: true,
		Loaded:           false,
	}, func(bool) error {
		t.Fatal("unloaded job must bootstrap, not kickstart")
		return nil
	}, func() error {
		bootstrapped = true
		fake.setPID(333)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)
	oldHealth, running := currentAgentHealth(ctx, client)
	health, supervised, err := restartAgentViaSupervisor(ctx, client, paths, oldHealth, running)
	if err != nil || !supervised || !bootstrapped {
		t.Fatalf("supervised repair = %+v, supervised=%v, bootstrapped=%v, err=%v", health, supervised, bootstrapped, err)
	}
	if health.PID != 333 {
		t.Fatalf("restarted pid = %d, want 333", health.PID)
	}
}

func TestRestartAgentViaSupervisorSkipsForeignPlist(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: false,
		Loaded:           true,
	}, func(bool) error {
		t.Fatal("foreign plist must not be kickstarted")
		return nil
	}, nil)
	ctx := context.Background()
	client := localagent.NewClient(paths.SocketPath)
	_, supervised, err := restartAgentViaSupervisor(ctx, client, paths, localagent.HealthResponse{}, false)
	if err != nil || supervised {
		t.Fatalf("foreign plist supervised=%v err=%v", supervised, err)
	}
}

func TestDeployStatusRequiresLoadedSupervisor(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	oldLoaded := deployResumeLaunchAgentLoadedFunc
	t.Cleanup(func() { deployResumeLaunchAgentLoadedFunc = oldLoaded })
	deployResumeLaunchAgentLoadedFunc = func() bool { return true }

	// Installed but unloaded supervisor: plist presence must not read as
	// supervision, and status must not be ready.
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: true,
		Loaded:           false,
		PlistPath:        "/Users/example/Library/LaunchAgents/dev.scenery.agent.plist",
		Label:            localagent.AgentLaunchdLabel,
	}, nil, nil)
	status := buildDeployStatus(paths, localagent.EmptyDeployRegistry())
	if status.Ready {
		t.Fatalf("status must not be ready with unloaded supervisor: %+v", status)
	}
	if !status.AgentSupervisor.Installed || status.AgentSupervisor.Loaded {
		t.Fatalf("agent supervisor status = %+v", status.AgentSupervisor)
	}
	found := false
	for _, diag := range status.Diagnostics {
		if strings.Contains(diag, "supervisor LaunchAgent is installed but not loaded") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unloaded-supervisor diagnostic: %v", status.Diagnostics)
	}

	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     false,
		SupervisesSocket: false,
	}, nil, nil)
	status = buildDeployStatus(paths, localagent.EmptyDeployRegistry())
	found = false
	for _, diag := range status.Diagnostics {
		if strings.Contains(diag, "supervisor LaunchAgent is not installed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing uninstalled-supervisor diagnostic: %v", status.Diagnostics)
	}
}

func TestDeployLaunchAgentStatusDistinguishesLoaded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldLaunchctl := deployLaunchctlFunc
	t.Cleanup(func() { deployLaunchctlFunc = oldLaunchctl })

	status := deployLaunchAgentStatusFor()
	if status.Installed || status.Loaded {
		t.Fatalf("missing plist status = %+v", status)
	}

	paths := localagent.PathsForHome(t.TempDir())
	oldExe := deployPrivilegedHelperExecutableFunc
	t.Cleanup(func() { deployPrivilegedHelperExecutableFunc = oldExe })
	deployPrivilegedHelperExecutableFunc = func() (string, error) { return "/usr/local/bin/scenery", nil }
	var calls []string
	deployLaunchctlFunc = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "print" {
			return []byte("Could not find service"), &net.AddrError{Err: "not loaded"}
		}
		return nil, nil
	}
	if err := installDeployResumeLaunchAgent(paths); err != nil {
		t.Fatalf("installDeployResumeLaunchAgent: %v", err)
	}
	if len(calls) != 3 || !strings.HasPrefix(calls[0], "bootout gui/") || !strings.HasPrefix(calls[1], "bootstrap gui/") || !strings.HasPrefix(calls[2], "kickstart gui/") {
		t.Fatalf("install launchctl calls = %v", calls)
	}

	status = deployLaunchAgentStatusFor()
	if !status.Installed || status.Loaded {
		t.Fatalf("unloaded job status = %+v", status)
	}

	deployLaunchctlFunc = func(args ...string) ([]byte, error) { return []byte("state = running"), nil }
	status = deployLaunchAgentStatusFor()
	if !status.Installed || !status.Loaded {
		t.Fatalf("loaded job status = %+v", status)
	}

	calls = nil
	deployLaunchctlFunc = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	removed, err := removeDeployResumeLaunchAgent()
	if err != nil || !removed {
		t.Fatalf("removeDeployResumeLaunchAgent = %v, %v", removed, err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "bootout gui/") {
		t.Fatalf("remove launchctl calls = %v", calls)
	}
}
