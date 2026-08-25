package edge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestTrustConfigUsesAdminOnlyLocalCA(t *testing.T) {
	t.Parallel()

	config := trustConfig("/tmp/scenery-trust.sock")
	for _, want := range []string{"local_certs", "admin unix///tmp/scenery-trust.sock"} {
		if !strings.Contains(config, want) {
			t.Fatalf("trust config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "https://") || strings.Contains(config, "reverse_proxy") {
		t.Fatalf("trust config should not bind HTTPS routes:\n%s", config)
	}
}

func TestReloadArgsUseAdminSocket(t *testing.T) {
	t.Parallel()

	args := reloadArgs("/tmp/Caddyfile.next", "/tmp/caddy-admin.sock")
	want := strings.Join([]string{"reload", "--config", "/tmp/Caddyfile.next", "--adapter", "caddyfile", "--address", "unix///tmp/caddy-admin.sock"}, "\n")
	if got := strings.Join(args, "\n"); got != want {
		t.Fatalf("reload args = %#v", args)
	}
}

func TestReloadUsesConfiguredRunner(t *testing.T) {
	t.Parallel()

	var gotBinary string
	var gotArgs []string
	err := reloadWithRunner("/managed/caddy", "/tmp/Caddyfile.next", "/tmp/caddy-admin.sock", func(binary string, args []string) ([]byte, error) {
		gotBinary = binary
		gotArgs = append([]string(nil), args...)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBinary != "/managed/caddy" {
		t.Fatalf("reload binary = %q", gotBinary)
	}
	want := strings.Join(reloadArgs("/tmp/Caddyfile.next", "/tmp/caddy-admin.sock"), "\n")
	if got := strings.Join(gotArgs, "\n"); got != want {
		t.Fatalf("reload args = %q, want %q", got, want)
	}

	err = reloadWithRunner("caddy", "config", "admin", func(string, []string) ([]byte, error) {
		return []byte("reload rejected\n"), errors.New("exit status 1")
	})
	if err == nil || !strings.Contains(err.Error(), "exit status 1: reload rejected") {
		t.Fatalf("reload error = %v", err)
	}
}

func TestTrustLocalCAPlansAdminRunAndTrustCommandsInProcess(t *testing.T) {
	t.Parallel()

	const configPath = "/tmp/scenery-caddy-trust/Caddyfile"
	if got, want := strings.Join(trustRunArgs(configPath), " "), "run --config /tmp/scenery-caddy-trust/Caddyfile --adapter caddyfile"; got != want {
		t.Fatalf("run args = %q, want %q", got, want)
	}
	if got, want := strings.Join(trustInstallArgs(configPath), " "), "trust --config /tmp/scenery-caddy-trust/Caddyfile --adapter caddyfile"; got != want {
		t.Fatalf("trust args = %q, want %q", got, want)
	}
}

func TestWaitForStartupReportsOnlyCurrentLogTailInProcess(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "caddy.log")
	if err := os.WriteFile(logPath, []byte("old caddy log line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	offset := fileSize(logPath)
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logFile.WriteString("listen tcp 127.0.0.1:443: bind: permission denied\n"); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	exitCh := make(chan error, 1)
	exitCh <- errors.New("exit status 1")
	err = waitForStartup(exitCh, logPath, offset, 15*time.Second)
	if err == nil || !strings.Contains(err.Error(), "Caddy edge exited during startup") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("waitForStartup() err = %v, want startup exit with log tail", err)
	}
	if strings.Contains(err.Error(), "old caddy log line") {
		t.Fatalf("waitForStartup() included stale log line: %v", err)
	}
}

func TestRunningStateAndStopProcessControlInProcess(t *testing.T) {
	paths := testPaths(t)
	adminSocket := filepath.Join(paths.RunDir, "caddy-admin.sock")
	updatedAt := time.Date(2026, 8, 25, 10, 30, 0, 0, time.UTC)
	config := StartConfig{
		Binary: "/managed/caddy", Paths: paths, PublicAddr: "127.0.0.1:443",
		TargetAddr: "127.0.0.1:19443", HTTPTargetAddr: "127.0.0.1:19080",
		AdminSocket: adminSocket, UpstreamAddr: "127.0.0.1:9440",
	}
	if err := writeRunningEdgeState(config, 4242, "Tue Aug 25 10:30:00 2026", updatedAt, 501, 20); err != nil {
		t.Fatal(err)
	}
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != localagent.EdgeKindCaddy || state.Status != localagent.EdgeStatusRunning || state.PID <= 0 {
		t.Fatalf("edge state = %+v, want running caddy with pid", state)
	}
	if state.PublicAddr != "127.0.0.1:443" || state.UpstreamAddr != "127.0.0.1:9440" || state.AdminSocket != adminSocket {
		t.Fatalf("edge state addresses = %+v", state)
	}
	target, err := localagent.LoadEdgeTargetState(paths.EdgeTargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if target.TargetAddr != "127.0.0.1:19443" || target.HTTPTargetAddr != "127.0.0.1:19080" || target.PID != state.PID || target.OwnerUID != 501 || target.OwnerGID != 20 || target.ProcessStart != "Tue Aug 25 10:30:00 2026" || !target.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("edge target state = %+v", target)
	}
	if !state.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("edge state updated_at = %s, want %s", state.UpdatedAt, updatedAt)
	}
	alive := true
	var signaledPID int
	if err := stopWithProcessControl(paths, 2*time.Second, func(pid int) bool {
		if pid != 4242 {
			t.Fatalf("process liveness PID = %d", pid)
		}
		return alive
	}, func(pid int, signal os.Signal) error {
		signaledPID = pid
		if signal != syscall.SIGTERM {
			t.Fatalf("process signal = %v", signal)
		}
		alive = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if signaledPID != 4242 {
		t.Fatalf("signaled PID = %d", signaledPID)
	}
}

func testPaths(t *testing.T) localagent.Paths {
	t.Helper()
	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	return paths
}
