package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func runHarnessDesktopProcessProbeCheck(parent context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-desktop-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if resultErr == nil {
			_ = os.RemoveAll(root)
		}
	}()
	runnerProof, err := runHarnessDesktopRunnerBoundary(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("desktop frontend")) }))
	defer frontend.Close()
	home, appRoot := filepath.Join(root, "agent-home"), filepath.Join(root, "app")
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_, err := runHarnessAppCLI(cleanupCtx, repoRoot, appRoot, home, "down", "-o", "json")
		resultErr = errors.Join(resultErr, err)
	}()
	if err := prepareHarnessDesktopApp(repoRoot, appRoot, frontend.Listener.Addr().String()); err != nil {
		return nil, nil, err
	}
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	marker := filepath.Join(appRoot, "tauri-invocation.json")
	script := `#!/bin/sh
set -eu
printf '%s\n%s\n%s\n%s\n%s\n%s\n' "$PWD" "$1" "$2" "$3" "$SCENERY_ENV" "$VITE_API_BASE_URL" > ` + shellQuote(marker) + `
trap 'exit 0' INT TERM
while :; do sleep 1; done
`
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), script, 0o755); err != nil {
		return nil, nil, err
	}
	out, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "up", "--detach", "--desktop", "-o", "json")
	if err != nil {
		return nil, nil, err
	}
	var launched detachedDevResult
	if err := decodeCLIJSON(out, &launched); err != nil {
		return nil, nil, err
	}
	var invocation []string
	var session localagent.Session
	if err := waitForHarnessCondition(ctx, func() bool {
		data, err := os.ReadFile(marker)
		if err != nil {
			return false
		}
		invocation = strings.Split(strings.TrimSpace(string(data)), "\n")
		session, err = harnessLiveSession(ctx, home, appRoot)
		return err == nil && len(invocation) == 6 && session.Processes["desktop-web"].PID > 0
	}); err != nil {
		return nil, nil, err
	}
	resolvedFrontendRoot, err := filepath.EvalSymlinks(frontendRoot)
	if err != nil {
		return nil, nil, err
	}
	apiURL := session.RouteManifest.Routes["api"].URL
	if apiURL == "" || invocation[0] != resolvedFrontendRoot || invocation[1] != "dev" || invocation[2] != "--config" || invocation[4] != "local" || invocation[5] != apiURL {
		return nil, nil, fmt.Errorf("desktop invocation = %#v, expected owned API route %q", invocation, apiURL)
	}
	var overlay struct {
		Build struct {
			DevURL           string `json:"devUrl"`
			BeforeDevCommand string `json:"beforeDevCommand"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(invocation[3]), &overlay); err != nil {
		return nil, nil, err
	}
	if overlay.Build.DevURL != frontend.URL || overlay.Build.BeforeDevCommand != "" {
		return nil, nil, fmt.Errorf("desktop overlay = %+v", overlay)
	}
	pid := session.Processes["desktop-web"].PID
	if _, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "down", "-o", "json"); err != nil {
		return nil, nil, err
	}
	if !waitForPIDExit(ctx, pid, 3*time.Second) {
		return nil, nil, fmt.Errorf("desktop process survived public down")
	}
	exitStarts, err := runHarnessDesktopExitNoRestart(ctx, repoRoot, filepath.Join(root, "exit-app"), home, frontend.Listener.Addr().String())
	if err != nil {
		return nil, nil, err
	}
	restart, err := runHarnessManagedFrontendRestart(ctx, repoRoot, filepath.Join(root, "frontend-app"), home, root)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"proof": "public_up_desktop_and_managed_frontend_process_boundaries_verified", "frontend": "web", "pid": pid, "owner_pid": launched.PID, "dev_url": overlay.Build.DevURL, "api_base_url": invocation[5], "normal_exit_process_starts": exitStarts, "managed_frontend_restart": restart, "desktop_runner": runnerProof}, nil, nil
}

func prepareHarnessDesktopApp(repoRoot, appRoot, upstream string) error {
	if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
		return err
	}
	if err := writeHarnessDesktopFile(filepath.Join(appRoot, "apps/web/src-tauri/tauri.conf.json"), `{}`, 0o644); err != nil {
		return err
	}
	return writeHarnessFixtureConfig(appRoot, app.Config{Name: "desktop-demo", Frontends: map[string]app.FrontendConfig{"web": {Root: "apps/web", Upstream: upstream, AllowSharedUpstream: true, Tauri: &app.FrontendTauriConfig{}}}, Envs: map[string]app.EnvConfig{"local": {Default: true}}})
}

func runHarnessDesktopExitNoRestart(ctx context.Context, repoRoot, appRoot, home, upstream string) (starts int, resultErr error) {
	if err := prepareHarnessDesktopApp(repoRoot, appRoot, upstream); err != nil {
		return 0, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_, err := runHarnessAppCLI(cleanup, repoRoot, appRoot, home, "down", "-o", "json")
		resultErr = errors.Join(resultErr, err)
	}()
	marker := filepath.Join(appRoot, "desktop-starts.log")
	if err := writeHarnessDesktopFile(filepath.Join(appRoot, "apps/web/node_modules/.bin/tauri"), "#!/bin/sh\nset -eu\necho start >> "+shellQuote(marker)+"\n", 0o755); err != nil {
		return 0, err
	}
	if _, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "up", "--detach", "--desktop", "-o", "json"); err != nil {
		return 0, err
	}
	var session localagent.Session
	if err := waitForHarnessCondition(ctx, func() bool {
		if _, err := os.Stat(marker); err != nil {
			return false
		}
		var err error
		session, err = harnessLiveSession(ctx, home, appRoot)
		_, registered := session.Processes["desktop-web"]
		return err == nil && !registered
	}); err != nil {
		return 0, err
	}
	if session.Status != "running" || session.Backends["web"].Addr != upstream {
		return 0, fmt.Errorf("session changed after desktop exit: %+v", session)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return 0, err
	}
	starts = strings.Count(string(data), "start")
	if starts != 1 {
		return starts, fmt.Errorf("desktop starts = %d, want exactly one", starts)
	}
	return starts, nil
}

func runHarnessManagedFrontendRestart(ctx context.Context, repoRoot, appRoot, home, root string) (proof map[string]any, resultErr error) {
	if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_, err := runHarnessAppCLI(cleanup, repoRoot, appRoot, home, "down", "-o", "json")
		resultErr = errors.Join(resultErr, err)
	}()
	serverSource, serverBinary := filepath.Join(root, "frontend-server.go"), filepath.Join(root, "frontend-server")
	marker, readyFile := filepath.Join(root, "frontend-starts.log"), filepath.Join(root, "frontend.ready")
	if err := writeHarnessDesktopFile(serverSource, fmt.Sprintf(`package main
import ("net"; "net/http"; "os"; "time")
func main() {
 for { if _, err := os.Stat(%q); err == nil { break }; time.Sleep(10*time.Millisecond) }
 listener, err := net.Listen("tcp", "127.0.0.1:"+os.Args[1]); if err != nil { panic(err) }
 if err := http.Serve(listener,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Write([]byte("frontend"))})); err != nil { panic(err) }
}
`, readyFile), 0o644); err != nil {
		return nil, err
	}
	build := commandTreeContext(ctx, "go", "build", "-o", serverBinary, serverSource)
	build.Env = harnessAppEnv(home)
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build frontend: %w: %s", err, out)
	}
	frontendRoot := filepath.Join(appRoot, "apps/web")
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "package.json"), `{"scripts":{"dev":"vite"}}`, 0o644); err != nil {
		return nil, err
	}
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "node_modules/.bin/vite"), `#!/bin/sh
set -eu
port=""
previous=""
for argument in "$@"; do
 if [ "$previous" = "--port" ]; then port="$argument"; break; fi
 previous="$argument"
done
printf '%s %s\n' "$$" "$port" >> `+shellQuote(marker)+`
exec `+shellQuote(serverBinary)+` "$port"
`, 0o755); err != nil {
		return nil, err
	}
	if err := writeHarnessFixtureConfig(appRoot, app.Config{Name: "frontend-restart", Frontends: map[string]app.FrontendConfig{"web": {Root: "apps/web"}}, Envs: map[string]app.EnvConfig{"local": {Default: true}}}); err != nil {
		return nil, err
	}
	launcher := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "up", "--detach", "--app-root", appRoot, "-o", "json")
	launcher.Dir, launcher.Env = appRoot, harnessAppEnv(home)
	var stdout, stderr bytes.Buffer
	launcher.Stdout, launcher.Stderr = &stdout, &stderr
	if err := launcher.Start(); err != nil {
		return nil, err
	}
	launchDone := make(chan error, 1)
	go func() { launchDone <- launcher.Wait() }()
	launchReaped := false
	defer func() {
		if !launchReaped {
			_ = killProcessTree(launcher)
			select {
			case <-launchDone:
			case <-time.After(3 * time.Second):
				resultErr = errors.Join(resultErr, fmt.Errorf("frontend launcher did not reap"))
			}
		}
	}()
	if err := waitForHarnessCondition(ctx, func() bool { data, err := os.ReadFile(marker); return err == nil && len(bytes.TrimSpace(data)) > 0 }); err != nil {
		return nil, err
	}
	select {
	case err := <-launchDone:
		launchReaped = true
		return nil, fmt.Errorf("frontend readiness completed before release: %v: %s", err, stderr.String())
	case <-time.After(75 * time.Millisecond):
	}
	if err := os.WriteFile(readyFile, []byte("ready"), 0o644); err != nil {
		return nil, err
	}
	select {
	case err := <-launchDone:
		launchReaped = true
		if err != nil {
			return nil, fmt.Errorf("frontend startup: %w: %s", err, stderr.String())
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var started detachedDevResult
	if err := decodeCLIJSON(stdout.Bytes(), &started); err != nil {
		return nil, err
	}
	first, err := harnessLiveSession(ctx, home, appRoot)
	if err != nil {
		return nil, err
	}
	oldAddr, oldPID := first.Backends["web"].Addr, first.Processes["frontend-web"].PID
	if oldAddr == "" || oldPID <= 0 {
		return nil, fmt.Errorf("frontend was not registered after readiness")
	}
	owner := localagent.CaptureOwner(oldPID, "owned frontend probe")
	actualExecutable, actualErr := filepath.EvalSymlinks(owner.Exe)
	wantedExecutable, wantedErr := filepath.EvalSymlinks(serverBinary)
	if actualErr != nil || wantedErr != nil || actualExecutable != wantedExecutable || localagent.VerifyOwner(owner) != nil {
		return nil, fmt.Errorf("frontend process identity does not match owned fixture binary")
	}
	if err := killProcessIDTree(oldPID); err != nil {
		return nil, err
	}
	var restarted localagent.Session
	if err := waitForHarnessCondition(ctx, func() bool {
		var err error
		restarted, err = harnessLiveSession(ctx, home, appRoot)
		if err != nil {
			return false
		}
		addr, pid := restarted.Backends["web"].Addr, restarted.Processes["frontend-web"].PID
		if addr == "" || addr == oldAddr || pid <= 0 || pid == oldPID || restarted.Environment != "local" {
			return false
		}
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}); err != nil {
		return nil, fmt.Errorf("wait for frontend restart: %w", err)
	}
	if _, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "down", "-o", "json"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil, err
	}
	starts := len(strings.Fields(strings.TrimSpace(string(data)))) / 2
	if starts != 2 {
		return nil, fmt.Errorf("managed frontend starts=%d, want exactly two", starts)
	}
	return map[string]any{"old_address": oldAddr, "old_pid": oldPID, "new_address": restarted.Backends["web"].Addr, "new_pid": restarted.Processes["frontend-web"].PID, "starts": starts, "readiness_deferred": true, "owner_pid": started.PID}, nil
}
