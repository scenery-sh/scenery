package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/doctor"
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

func withAgentSupervisorHooks(t *testing.T, status localagent.LaunchdAgentStatus, reload func() error, bootstrap func() error) {
	t.Helper()
	oldStatus := agentSupervisorStatusFunc
	oldReload := agentSupervisorReloadFunc
	oldBootstrap := agentSupervisorBootstrapFunc
	oldSpawnFailure := agentSupervisorSpawnFailureFunc
	oldReconcile := agentSupervisorReconcileFunc
	t.Cleanup(func() {
		agentSupervisorStatusFunc = oldStatus
		agentSupervisorReloadFunc = oldReload
		agentSupervisorBootstrapFunc = oldBootstrap
		agentSupervisorSpawnFailureFunc = oldSpawnFailure
		agentSupervisorReconcileFunc = oldReconcile
	})
	agentSupervisorStatusFunc = func(socketPath string) localagent.LaunchdAgentStatus { return status }
	agentSupervisorSpawnFailureFunc = func() string { return "" }
	// The host's real LaunchAgents plist is never rewritten by a test.
	agentSupervisorReconcileFunc = func() (bool, error) { return false, nil }
	if reload != nil {
		agentSupervisorReloadFunc = reload
	}
	if bootstrap != nil {
		agentSupervisorBootstrapFunc = bootstrap
	}
}

func TestRestartAgentViaSupervisorReloadsLoadedJob(t *testing.T) {
	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)

	reloaded := false
	// The supervisor owns pid 111, so restart must not SIGTERM it directly:
	// launchd stops it while registering the job again.
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: true,
		Loaded:           true,
		Running:          true,
		PID:              111,
	}, func() error {
		reloaded = true
		fake.setPID(222)
		return nil
	}, func() error {
		t.Fatal("loaded job must be reloaded, not only bootstrapped")
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)
	oldHealth, running := currentAgentHealth(ctx, client)
	if !running || oldHealth.PID != 111 {
		t.Fatalf("old health = %+v, running=%v", oldHealth, running)
	}
	health, supervised, _, err := restartAgentViaSupervisor(ctx, client, paths, oldHealth, running)
	if err != nil || !supervised || !reloaded {
		t.Fatalf("supervised restart = %+v, supervised=%v, reloaded=%v, err=%v", health, supervised, reloaded, err)
	}
	if health.PID != 222 {
		t.Fatalf("restarted pid = %d, want 222", health.PID)
	}
}

// A restart after an upgrade updates the installed job before launchd reads
// it again, so the reloaded agent runs the current invocation; a job that
// cannot be updated leaves the running agent alone.
func TestRestartAgentViaSupervisorUpdatesTheJobBeforeRegisteringIt(t *testing.T) {
	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)
	var steps []string
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported: true, PlistPresent: true, SupervisesSocket: true, Loaded: true, Running: true, PID: 111,
	}, func() error {
		steps = append(steps, "reload")
		fake.setPID(222)
		return nil
	}, nil)
	agentSupervisorReconcileFunc = func() (bool, error) {
		steps = append(steps, "reconcile")
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)
	oldHealth, running := currentAgentHealth(ctx, client)
	health, supervised, updated, err := restartAgentViaSupervisor(ctx, client, paths, oldHealth, running)
	if err != nil || !supervised || !updated || health.PID != 222 || strings.Join(steps, ",") != "reconcile,reload" {
		t.Fatalf("restart = %+v supervised=%v updated=%v err=%v steps=%v", health, supervised, updated, err, steps)
	}

	steps = nil
	agentSupervisorReconcileFunc = func() (bool, error) {
		return false, errors.New("failed_precondition: the installed supervised agent plist is not one Scenery renders")
	}
	_, supervised, updated, err = restartAgentViaSupervisor(ctx, client, paths, localagent.HealthResponse{PID: 222}, true)
	if err == nil || !supervised || updated || len(steps) != 0 || cliExitCode(err) != 3 {
		t.Fatalf("restart with an unreadable job: supervised=%v updated=%v err=%v steps=%v", supervised, updated, err, steps)
	}
}

func TestDoctorAgentSupervisorCheckNamesMissingContainment(t *testing.T) {
	old := doctorAgentSupervisorJobFunc
	t.Cleanup(func() { doctorAgentSupervisorJobFunc = old })
	deps := doctor.ProbeDeps{AgentHome: func() (string, error) { return "/home/example/.scenery", nil }}
	for _, tc := range []struct {
		job    localagent.SupervisorJob
		ok     bool
		status string
	}{
		{ok: false, status: doctor.StatusOK},
		{job: localagent.SupervisorJob{Label: localagent.AgentLaunchdLabel, FailureContainment: true}, ok: true, status: doctor.StatusOK},
		{job: localagent.SupervisorJob{Label: localagent.AgentLaunchdLabel}, ok: true, status: doctor.StatusWarn},
	} {
		doctorAgentSupervisorJobFunc = func(string) (localagent.SupervisorJob, bool) { return tc.job, tc.ok }
		check := doctorAgentSupervisorCheck(deps)
		if check.ID != "runtime.agent_supervisor" || check.Status != tc.status {
			t.Fatalf("job %+v (%v): check = %+v", tc.job, tc.ok, check)
		}
		if tc.status == doctor.StatusWarn && (!strings.Contains(check.Message, "without start failure containment") || !strings.Contains(check.SuggestedAction, "scenery system agent restart")) {
			t.Fatalf("check = %+v", check)
		}
	}
}

func TestRestartAgentViaSupervisorRepairsUnloadedJob(t *testing.T) {
	paths := localagent.PathsForHome(t.TempDir())
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
	}, func() error {
		t.Fatal("unloaded job must bootstrap, not reload")
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
	health, supervised, _, err := restartAgentViaSupervisor(ctx, client, paths, oldHealth, running)
	if err != nil || !supervised || !bootstrapped {
		t.Fatalf("supervised repair = %+v, supervised=%v, bootstrapped=%v, err=%v", health, supervised, bootstrapped, err)
	}
	if health.PID != 333 {
		t.Fatalf("restarted pid = %d, want 333", health.PID)
	}
}

func TestRestartAgentViaSupervisorSkipsForeignPlist(t *testing.T) {
	paths := localagent.PathsForHome(t.TempDir())
	withAgentSupervisorHooks(t, localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: false,
		Loaded:           true,
	}, func() error {
		t.Fatal("foreign plist must not be reloaded")
		return nil
	}, nil)
	ctx := context.Background()
	client := localagent.NewClient(paths.SocketPath)
	_, supervised, _, err := restartAgentViaSupervisor(ctx, client, paths, localagent.HealthResponse{}, false)
	if err != nil || supervised {
		t.Fatalf("foreign plist supervised=%v err=%v", supervised, err)
	}
}

func TestRestartAgentViaSupervisorReportsLaunchdSpawnFailure(t *testing.T) {
	paths := localagent.PathsForHome(t.TempDir())
	status := localagent.LaunchdAgentStatus{
		Supported:        true,
		PlistPresent:     true,
		SupervisesSocket: true,
		Loaded:           true,
		PlistPath:        "/Users/example/Library/LaunchAgents/dev.scenery.agent.plist",
	}
	withAgentSupervisorHooks(t, status, func() error {
		return errors.New("launchctl kickstart gui/501/dev.scenery.agent: exit status 5")
	}, nil)
	agentSupervisorSpawnFailureFunc = func() string {
		return "job state spawn failed; last exit code 78: EX_CONFIG"
	}
	client := localagent.NewClient(paths.SocketPath)
	_, supervised, _, err := restartAgentViaSupervisor(context.Background(), client, paths, localagent.HealthResponse{}, false)
	if !supervised || err == nil {
		t.Fatalf("spawn failure supervised=%v err=%v", supervised, err)
	}
	if code := cliExitCode(err); code != 3 {
		t.Fatalf("exit code = %d, want failed precondition 3: %v", code, err)
	}
	if diagnostic := cliErrorDiagnostic(err); diagnostic.Code != "SCN8003" {
		t.Fatalf("diagnostic = %+v, want SCN8003", diagnostic)
	}
	for _, want := range []string{"job state spawn failed", "EX_CONFIG", "launchctl bootstrap gui/", status.PlistPath, "exit status 5"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}

	// Without a launchd spawn failure the start error stays as reported.
	agentSupervisorSpawnFailureFunc = func() string { return "" }
	_, _, _, err = restartAgentViaSupervisor(context.Background(), client, paths, localagent.HealthResponse{}, false)
	if err == nil || strings.HasPrefix(err.Error(), "failed_precondition:") {
		t.Fatalf("plain start error = %v", err)
	}
}
