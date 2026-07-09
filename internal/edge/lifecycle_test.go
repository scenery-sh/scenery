package edge

import (
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
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

func TestReloadInvokesCaddy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	caddy := filepath.Join(t.TempDir(), "caddy")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n"
	if err := os.WriteFile(caddy, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Reload(caddy, "/tmp/Caddyfile.next", "/tmp/caddy-admin.sock"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(reloadArgs("/tmp/Caddyfile.next", "/tmp/caddy-admin.sock"), "\n") + "\n"
	if string(data) != want {
		t.Fatalf("reload args file = %q, want %q", string(data), want)
	}
}

func TestTrustLocalCAUsesTemporaryAdmin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	caddy := filepath.Join(t.TempDir(), "caddy")
	marker := filepath.Join(t.TempDir(), "marker")
	writeFakeTrustCaddy(t, caddy, marker)

	if err := TrustLocalCA(caddy, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "run\n") || !strings.Contains(got, "trust\n") {
		t.Fatalf("fake Caddy marker = %q, want run and trust", got)
	}
}

func TestStartReportsFastStartupExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	paths := testPaths(t)
	caddy := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(caddy, []byte("#!/bin/sh\necho 'listen tcp 127.0.0.1:443: bind: permission denied' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeLogPath, []byte("old caddy log line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Start(StartConfig{
		Binary: caddy, Paths: paths, PublicAddr: "127.0.0.1:443",
		TargetAddr: "127.0.0.1:19443", HTTPTargetAddr: "127.0.0.1:19080",
		AdminSocket: filepath.Join(paths.RunDir, "caddy-admin.sock"), UpstreamAddr: "127.0.0.1:9440",
		StartupSettle: 15 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "Caddy edge exited during startup") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Start() err = %v, want startup exit with log tail", err)
	}
	if strings.Contains(err.Error(), "old caddy log line") {
		t.Fatalf("Start() included stale log line: %v", err)
	}
	state, stateErr := localagent.LoadEdgeState(paths.EdgeStatePath)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if localagent.EdgeStateRunning(state) {
		t.Fatalf("edge state = %+v, want not running", state)
	}
}

func TestStartWritesRunningStateAndStopTerminatesProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	paths := testPaths(t)
	caddy := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(caddy, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adminSocket := filepath.Join(paths.RunDir, "caddy-admin.sock")
	if err := Start(StartConfig{
		Binary: caddy, Paths: paths, PublicAddr: "127.0.0.1:443",
		TargetAddr: "127.0.0.1:19443", HTTPTargetAddr: "127.0.0.1:19080",
		AdminSocket: adminSocket, UpstreamAddr: "127.0.0.1:9440", StartupSettle: 50 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Stop(paths, 2*time.Second) })
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
	if target.TargetAddr != "127.0.0.1:19443" || target.HTTPTargetAddr != "127.0.0.1:19080" || target.PID != state.PID || target.OwnerUID != os.Getuid() {
		t.Fatalf("edge target state = %+v", target)
	}
	if err := Stop(paths, 2*time.Second); err != nil {
		t.Fatal(err)
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

func writeFakeTrustCaddy(t *testing.T, path, marker string) {
	t.Helper()
	testBin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"SCENERY_FAKE_CADDY_HELPER=1 SCENERY_FAKE_CADDY_MARKER=" + shellQuote(marker) +
		" exec " + shellQuote(testBin) + " -test.run '^TestFakeCaddyHelperProcess$' -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestFakeCaddyHelperProcess(t *testing.T) {
	if os.Getenv("SCENERY_FAKE_CADDY_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	command := args[0]
	config := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			config = args[i+1]
		}
	}
	marker := os.Getenv("SCENERY_FAKE_CADDY_MARKER")
	appendMarker := func(line string) {
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			os.Exit(1)
		}
		_, _ = file.WriteString(line + "\n")
		_ = file.Close()
	}
	switch command {
	case "run":
		appendMarker("run")
		data, err := os.ReadFile(config)
		if err != nil {
			os.Exit(1)
		}
		socket := ""
		for line := range strings.Lines(string(data)) {
			if _, rest, ok := strings.Cut(line, "admin unix//"); ok {
				socket = strings.TrimSpace(rest)
				break
			}
		}
		if socket == "" {
			os.Exit(1)
		}
		_ = os.Remove(socket)
		listener, err := net.Listen("unix", socket)
		if err != nil {
			os.Exit(1)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			for {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				_ = connection.Close()
			}
		}()
		<-signals
		os.Exit(0)
	case "trust":
		appendMarker("trust")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
