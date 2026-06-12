package scenery_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const integrationPollInterval = 20 * time.Millisecond

var sharedTemporalDevServer temporalDevServerState

var sharedIntegration struct {
	mu       sync.Mutex
	cacheDir string
}

type temporalDevServerState struct {
	once   sync.Once
	addr   string
	err    error
	cancel context.CancelFunc
	cmd    *exec.Cmd
	output bytes.Buffer
}

type missingTemporalCLIError struct {
	err error
}

func (e missingTemporalCLIError) Error() string {
	return e.err.Error()
}

func buildSceneryBinary(t *testing.T, repo string) string {
	t.Helper()
	buildSceneryBinaryOnce.Do(func() {
		buildSceneryBinaryPath, buildSceneryBinaryErr = buildSceneryBinaryForRepo(repo)
	})
	if buildSceneryBinaryErr != nil {
		t.Fatal(buildSceneryBinaryErr)
	}
	return buildSceneryBinaryPath
}

func buildSceneryBinaryForRepo(repo string) (string, error) {
	if path, ok := freshInstalledSceneryBinary(repo); ok {
		return path, nil
	}
	binDir, fingerprint, err := sceneryBinaryCacheDir(repo)
	if err != nil {
		// Fall back to an uncached temp build.
		binDir, err = os.MkdirTemp("", "scenery-test-bin-*")
		if err != nil {
			return "", err
		}
		fingerprint = ""
	}
	binPath := filepath.Join(binDir, "scenery")
	marker := filepath.Join(binDir, "scenery.fingerprint")
	if fingerprint != "" {
		if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == fingerprint {
			if info, err := os.Stat(binPath); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return binPath, nil
			}
		}
		_ = os.Remove(marker)
	}
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/scenery")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build scenery binary: %w\n%s", err, output)
	}
	if fingerprint != "" {
		_ = os.WriteFile(marker, []byte(fingerprint+"\n"), 0o644)
	}
	return binPath, nil
}

// sceneryBinaryCacheDir returns a per-repo cache directory for the shared
// scenery test binary plus the current source fingerprint, so unchanged
// sources skip the relink on every test run.
func sceneryBinaryCacheDir(repo string) (string, string, error) {
	fingerprint, err := integrationSourceFingerprint(repo)
	if err != nil {
		return "", "", err
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(repo))
	dir := filepath.Join(userCache, "scenery", "integration-test", hex.EncodeToString(sum[:8]), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	return dir, fingerprint, nil
}

func freshInstalledSceneryBinary(repo string) (string, bool) {
	candidates := []string{}
	if configured := strings.TrimSpace(os.Getenv("SCENERY_BIN")); configured != "" {
		candidates = append(candidates, configured)
	}
	if found, err := exec.LookPath("scenery"); err == nil {
		candidates = append(candidates, found)
	}
	latest, ok, err := latestIntegrationSourceModTime(repo)
	if err != nil || !ok {
		return "", false
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if !info.ModTime().Before(latest) {
			if installedSceneryBinaryMatchesRepo(candidate, repo) {
				return candidate, true
			}
		}
	}
	return "", false
}

func installedSceneryBinaryMatchesRepo(path, repo string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	needle := filepath.Join(filepath.Clean(repo), "internal", "app", "root.go")
	return bytes.Contains(data, []byte(needle))
}

func latestIntegrationSourceModTime(repo string) (time.Time, bool, error) {
	paths := []string{
		"go.mod",
		"go.sum",
		"auth",
		"cmd",
		"cron",
		"data",
		"errs",
		"internal",
		"middleware",
		"pgxpool",
		"rlog",
		"runtime",
		"runtimeapp",
		"temporal",
	}
	var latest time.Time
	found := false
	for _, rel := range paths {
		modTime, ok, err := latestPathModTime(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			return time.Time{}, false, err
		}
		if ok && (!found || modTime.After(latest)) {
			latest = modTime
			found = true
		}
	}
	return latest, found, nil
}

func latestPathModTime(root string) (time.Time, bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	if !info.IsDir() {
		return info.ModTime(), true, nil
	}
	var latest time.Time
	found := false
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if integrationBinaryInputSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !integrationBinaryInputFile(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !found || info.ModTime().After(latest) {
			latest = info.ModTime()
			found = true
		}
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return latest, found, nil
}

func sceneryServeEnv(repo, dashboardAddr, cacheDir string) []string {
	return append(
		os.Environ(),
		"SCENERY_DEV_CACHE_DIR="+cacheDir,
		"SCENERY_LOCAL_PROXY=0",
	)
}

func sceneryDevAgentEnv(repo, dashboardAddr, cacheDir, agentHome, frontendAddr string) []string {
	env := append(
		os.Environ(),
		"SCENERY_AGENT_HOME="+agentHome,
		"SCENERY_DEV_DASHBOARD_ADDR="+dashboardAddr,
		"SCENERY_DEV_DASHBOARD_UI_DIR="+filepath.Join(repo, "ui", "dist"),
		"SCENERY_DEV_VICTORIA=0",
		"SCENERY_TEST_WATCH_BACKUP_POLL_MS=20",
		"SCENERY_TEST_WATCH_SETTLE_DELAY_MS=20",
	)
	if frontendAddr != "" {
		env = append(env, frontendAddrEnv("web")+"="+frontendAddr)
	}
	return env
}

func frontendAddrEnv(name string) string {
	return "SCENERY_FRONTEND_" + strings.ToUpper(name) + "_ADDR"
}

func sharedIntegrationCache(t *testing.T) string {
	t.Helper()
	sharedIntegration.mu.Lock()
	defer sharedIntegration.mu.Unlock()
	if sharedIntegration.cacheDir != "" {
		return sharedIntegration.cacheDir
	}
	repo := repoRoot(t)
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(repo))
	dir := filepath.Join(userCache, "scenery", "integration-test", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := integrationSourceFingerprint(repo)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "source-fingerprint")
	if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) != fingerprint {
		if err := os.RemoveAll(filepath.Join(dir, "cache")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(marker, []byte(fingerprint+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sharedIntegration.cacheDir = filepath.Join(dir, "cache")
	return sharedIntegration.cacheDir
}

func integrationSourceFingerprint(repo string) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("repo-root"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(filepath.Clean(repo)))
	_, _ = h.Write([]byte{0})
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel == ".":
				return nil
			case integrationBinaryInputSkipDirName(d.Name()):
				return filepath.SkipDir
			case rel == "cmd" || rel == "scripts":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !integrationSourceFingerprintFile(rel, path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func integrationSourceFingerprintFile(rel, path string) bool {
	if rel == "go.mod" || rel == "go.sum" {
		return true
	}
	if strings.HasPrefix(rel, "testdata/apps/") {
		base := filepath.Base(path)
		return base != "" && base != ".DS_Store"
	}
	for _, prefix := range []string{"auth/", "cron/", "errs/", "internal/", "middleware/", "pgxpool/", "rlog/", "runtime/", "runtimeapp/", "temporal/"} {
		if strings.HasPrefix(rel, prefix) {
			return integrationBinaryInputFile(path)
		}
	}
	return false
}

func integrationBinaryInputFile(path string) bool {
	base := filepath.Base(path)
	if base == "" || base == ".DS_Store" || strings.HasPrefix(base, ".env") || strings.HasPrefix(base, ".") {
		return false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	return true
}

func integrationBinaryInputSkipDirName(name string) bool {
	switch name {
	case ".git", ".scenery", "coverage", "dist", "node_modules", "testpostgres":
		return true
	default:
		return false
	}
}

func lockIntegrationFixtureMutation(t *testing.T, appDir string) func() {
	t.Helper()
	lockDir := appDir + ".mutation.lock"
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			return func() { _ = os.Remove(lockDir) }
		}
		if !os.IsExist(err) {
			t.Fatalf("lock mutable fixture %s: %v", appDir, err)
		}
		if info, statErr := os.Stat(lockDir); statErr == nil && time.Since(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(lockDir)
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for mutable fixture lock %s", lockDir)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func startTemporalDevServerForTest(t *testing.T, cacheDir string) string {
	t.Helper()
	_ = cacheDir
	sharedTemporalDevServer.once.Do(func() {
		sharedTemporalDevServer.addr, sharedTemporalDevServer.err = startSharedTemporalDevServer()
	})
	if sharedTemporalDevServer.err != nil {
		if missing, ok := errors.AsType[missingTemporalCLIError](sharedTemporalDevServer.err); ok {
			t.Skipf("temporal CLI not found in PATH: %v", missing.err)
		}
		t.Fatal(sharedTemporalDevServer.err)
	}
	return sharedTemporalDevServer.addr
}

func startSharedTemporalDevServer() (string, error) {
	path, err := exec.LookPath("temporal")
	if err != nil {
		return "", missingTemporalCLIError{err: err}
	}
	port, err := freeTCPPort()
	if err != nil {
		return "", err
	}
	address := "127.0.0.1:" + port
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, path,
		"server",
		"start-dev",
		"--ip", "127.0.0.1",
		"--port", port,
		"--headless",
		"--log-level", "warn",
	)
	cmd.Stdout = &sharedTemporalDevServer.output
	cmd.Stderr = &sharedTemporalDevServer.output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("start temporal dev server: %w", err)
	}
	sharedTemporalDevServer.cancel = cancel
	sharedTemporalDevServer.cmd = cmd
	if err := waitForTemporalAddress(address, &sharedTemporalDevServer.output); err != nil {
		stopSharedTemporalDevServer()
		return "", err
	}
	return address, nil
}

func stopSharedTemporalDevServer() {
	if sharedTemporalDevServer.cancel != nil {
		sharedTemporalDevServer.cancel()
	}
	if sharedTemporalDevServer.cmd != nil && sharedTemporalDevServer.cmd.Process != nil {
		_ = syscall.Kill(-sharedTemporalDevServer.cmd.Process.Pid, syscall.SIGINT)
		_ = sharedTemporalDevServer.cmd.Process.Kill()
		_ = sharedTemporalDevServer.cmd.Wait()
	}
}

func waitForTemporalAddress(address string, output *bytes.Buffer) error {
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(integrationPollInterval)
	}
	return fmt.Errorf("temporal dev server did not become reachable at %s: %v\n%s", address, lastErr, output.String())
}

func killSceneryProcess(t *testing.T, cancel context.CancelFunc, cmd *exec.Cmd) {
	t.Helper()
	cancel()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for scenery process to exit")
	}
}

type sceneryTestProcess struct {
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func killSceneryProcesses(t *testing.T, processes ...sceneryTestProcess) {
	t.Helper()
	for _, process := range processes {
		process.cancel()
		if process.cmd.Process != nil {
			_ = syscall.Kill(-process.cmd.Process.Pid, syscall.SIGKILL)
			_ = process.cmd.Process.Kill()
		}
	}

	done := make(chan error, len(processes))
	for _, process := range processes {
		cmd := process.cmd
		go func() { done <- cmd.Wait() }()
	}
	deadline := time.After(5 * time.Second)
	for range processes {
		select {
		case <-done:
		case <-deadline:
			t.Fatalf("timed out waiting for scenery processes to exit")
		}
	}
}

func wsCallErr(conn *websocket.Conn, id int, method string, params map[string]any) (map[string]any, error) {
	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, fmt.Errorf("write websocket rpc: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set websocket deadline: %w", err)
	}
	for time.Now().Before(deadline) {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			return nil, fmt.Errorf("read websocket rpc: %w", err)
		}
		if int(toFloat(message["id"])) != id {
			continue
		}
		if errPayload, ok := message["error"]; ok && errPayload != nil {
			return nil, fmt.Errorf("websocket rpc %s returned error: %#v", method, message)
		}
		result := toMap(message["result"])
		if method == "version" && toString(result["version"]) == "" {
			return nil, fmt.Errorf("dashboard version response missing version: %#v", result)
		}
		return result, nil
	}
	return nil, fmt.Errorf("timed out waiting for websocket rpc response %s", method)
}

func waitForWSMethodsErr(conn *websocket.Conn, timeout time.Duration, methods ...string) error {
	remaining := make(map[string]bool, len(methods))
	for _, method := range methods {
		remaining[method] = true
	}
	deadline := time.Now().Add(timeout)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("set websocket deadline: %w", err)
	}
	for len(remaining) > 0 && time.Now().Before(deadline) {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			return fmt.Errorf("read websocket notification: %w", err)
		}
		method, _ := message["method"].(string)
		delete(remaining, method)
	}
	if len(remaining) > 0 {
		return fmt.Errorf("timed out waiting for websocket notifications: %v", remaining)
	}
	return nil
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	waitForURL(t, client, url)
}

func waitForHTTPWithProcessOutput(t *testing.T, url string, outputs ...fmt.Stringer) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(integrationPollInterval)
	}
	var detail strings.Builder
	for i, output := range outputs {
		if output == nil {
			continue
		}
		fmt.Fprintf(&detail, "\nprocess %d output:\n%s", i+1, output.String())
	}
	t.Fatalf("server did not start: %s%s", url, detail.String())
}

func waitForURL(t *testing.T, client *http.Client, url string) {
	t.Helper()
	if err := waitForStatusWithClientErr(client, url, 0); err != nil {
		t.Fatal(err)
	}
}

func waitForStatusWithClientErr(client *http.Client, url string, wantStatus int) error {
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			status := resp.StatusCode
			resp.Body.Close()
			if wantStatus == 0 || status == wantStatus {
				return nil
			}
			lastErr = fmt.Errorf("status %d, want %d", status, wantStatus)
		} else {
			lastErr = err
		}
		time.Sleep(integrationPollInterval)
	}
	return fmt.Errorf("server did not start: %s: %w", url, lastErr)
}

func waitForJSONWithClientErr(client *http.Client, url string, wantStatus int, want map[string]any) error {
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if err := assertJSONResponseWithClientErr(client, req, wantStatus, want, nil); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(integrationPollInterval)
	}
	return fmt.Errorf("JSON response did not settle at %s: %w", url, lastErr)
}

func waitForBodyWithClientErr(client *http.Client, url string, wantStatus int, wantBody string) error {
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(integrationPollInterval)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == wantStatus && string(body) == wantBody {
			return nil
		}
		lastErr = fmt.Errorf("status=%d body=%q, want status=%d body=%q", resp.StatusCode, body, wantStatus, wantBody)
		time.Sleep(integrationPollInterval)
	}
	return fmt.Errorf("body response did not settle at %s: %w", url, lastErr)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("file was not created: %s", path)
}

func waitForCronStatus(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(integrationPollInterval)
			continue
		}
		var got map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if decodeErr == nil && resp.StatusCode == http.StatusOK &&
			toString(got["count"]) != "0" &&
			strings.TrimSpace(toString(got["cron"])) != "" &&
			toString(got["type"]) == "api-call" &&
			toString(got["path"]) == "/service.Run" {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("cron job did not execute at %s", url)
}

func postJSON(t *testing.T, url string, body any, headers map[string]string, wantStatus int, want map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	assertJSONResponse(t, req, wantStatus, want)
}

func postJSONForString(t *testing.T, url string, body any, field string) string {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		got, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s status %d: %s", url, resp.StatusCode, got)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	value, _ := payload[field].(string)
	if strings.TrimSpace(value) == "" {
		t.Fatalf("POST %s response missing string field %q: %#v", url, field, payload)
	}
	return value
}

func getJSON(t *testing.T, url string, headers map[string]string, wantStatus int, want map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	assertJSONResponse(t, req, wantStatus, want)
}

func assertCORSPreflight(t *testing.T, url string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodOptions, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:5178")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected preflight status %d: %s", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get("Access-Control-Allow-Origin"), "http://localhost:5178"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want it to include GET", got)
	}
	if got := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers")); !strings.Contains(got, "authorization") || !strings.Contains(got, "content-type") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want authorization and content-type", got)
	}
	vary := resp.Header.Get("Vary")
	for _, want := range []string{"Origin", "Authorization", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if !strings.Contains(vary, want) {
			t.Fatalf("Vary = %q, want %q", vary, want)
		}
	}
}

func assertCORSActual(t *testing.T, url string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:5178")
	req.Header.Set("Authorization", "Bearer token123")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected actual CORS status %d: %s", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get("Access-Control-Allow-Origin"), "http://localhost:5178"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	vary := resp.Header.Get("Vary")
	for _, want := range []string{"Origin", "Authorization"} {
		if !strings.Contains(vary, want) {
			t.Fatalf("Vary = %q, want %q", vary, want)
		}
	}
}

func assertJSONResponse(t *testing.T, req *http.Request, wantStatus int, want map[string]any) {
	t.Helper()
	assertJSONResponseWithHeaders(t, req, wantStatus, want, nil)
}

func assertJSONResponseWithHeaders(t *testing.T, req *http.Request, wantStatus int, want map[string]any, wantHeaders map[string]string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	assertJSONResponseWithClient(t, client, req, wantStatus, want, wantHeaders)
}

func assertJSONResponseWithClient(t *testing.T, client *http.Client, req *http.Request, wantStatus int, want map[string]any, wantHeaders map[string]string) {
	t.Helper()
	if err := assertJSONResponseWithClientErr(client, req, wantStatus, want, wantHeaders); err != nil {
		t.Fatal(err)
	}
}

func assertJSONResponseWithClientErr(client *http.Client, req *http.Request, wantStatus int, want map[string]any, wantHeaders map[string]string) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d, want %d: %s", resp.StatusCode, wantStatus, body)
	}
	for key, wantValue := range wantHeaders {
		if got := resp.Header.Get(key); got != wantValue {
			return fmt.Errorf("unexpected header %s=%q, want %q", key, got, wantValue)
		}
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return err
	}
	if !mapsEqual(got, want) {
		return fmt.Errorf("unexpected body: got=%v want=%v", got, want)
	}
	return nil
}

func insecureHTTPSClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func mustRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func mapsEqual(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			return false
		}
		if strings.TrimSpace(toString(gotValue)) != strings.TrimSpace(toString(wantValue)) {
			return false
		}
	}
	return true
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func toMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func toFloat(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func freeTCPPort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return strings.Split(ln.Addr().String(), ":")[1], nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(wd)
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	}); err != nil {
		t.Fatal(err)
	}
}

func rewriteFixtureReplace(t *testing.T, goModPath, repo string) {
	t.Helper()
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "replace scenery.sh => ../../..", "replace scenery.sh => "+repo, 1)
	if updated == string(data) {
		t.Fatalf("expected fixture go.mod replace in %s", goModPath)
	}
	if err := os.WriteFile(goModPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cachedFixtureApp(t *testing.T, repo, name string) string {
	t.Helper()
	return cachedFixtureAppVariant(t, repo, name, "", nil, nil)
}

func cachedFixtureAppVariant(t *testing.T, repo, name, variant string, overrides map[string]string, removes []string) string {
	t.Helper()
	cacheDir := sharedIntegrationCache(t)
	appsDir := filepath.Join(cacheDir, "apps")
	cacheName := sanitizeIntegrationCacheName(t.Name() + "-" + name + "-" + variant)
	dst := filepath.Join(appsDir, cacheName)
	ready := filepath.Join(appsDir, cacheName+".ready")
	fingerprint := fixtureAppVariantFingerprint(name, overrides, removes)

	sharedIntegration.mu.Lock()
	defer sharedIntegration.mu.Unlock()
	if data, err := os.ReadFile(ready); err == nil && strings.TrimSpace(string(data)) == fingerprint {
		return dst
	}
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	copyDir(t, filepath.Join(repo, "testdata", "apps", name), dst)
	rewriteFixtureReplace(t, filepath.Join(dst, "go.mod"), repo)
	for _, rel := range removes {
		if err := os.Remove(filepath.Join(dst, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	overridePaths := make([]string, 0, len(overrides))
	for rel := range overrides {
		overridePaths = append(overridePaths, rel)
	}
	sort.Strings(overridePaths)
	for _, rel := range overridePaths {
		writeFile(t, filepath.Join(dst, filepath.FromSlash(rel)), overrides[rel])
	}
	if err := os.WriteFile(ready, []byte(fingerprint+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func fixtureAppVariantFingerprint(name string, overrides map[string]string, removes []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte{0})
	removePaths := append([]string(nil), removes...)
	sort.Strings(removePaths)
	for _, rel := range removePaths {
		_, _ = h.Write([]byte("remove"))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
	}
	overridePaths := make([]string, 0, len(overrides))
	for rel := range overrides {
		overridePaths = append(overridePaths, filepath.ToSlash(rel))
	}
	sort.Strings(overridePaths)
	for _, rel := range overridePaths {
		_, _ = h.Write([]byte("override"))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(overrides[rel]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cachedSyntheticApp(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	cacheDir := sharedIntegrationCache(t)
	appsDir := filepath.Join(cacheDir, "apps")
	cacheName := sanitizeIntegrationCacheName(t.Name() + "-" + name)
	dst := filepath.Join(appsDir, cacheName)
	ready := filepath.Join(appsDir, cacheName+".ready")
	fingerprint := syntheticAppFingerprint(files)

	sharedIntegration.mu.Lock()
	defer sharedIntegration.mu.Unlock()
	if data, err := os.ReadFile(ready); err == nil && strings.TrimSpace(string(data)) == fingerprint {
		return dst
	}
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		writeFile(t, filepath.Join(dst, filepath.FromSlash(rel)), files[rel])
	}
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ready, []byte(fingerprint+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func syntheticAppFingerprint(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(files[rel]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sanitizeIntegrationCacheName(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFileIfChanged(t *testing.T, path, contents string) {
	t.Helper()
	if current, err := os.ReadFile(path); err == nil && string(current) == contents {
		return
	}
	writeFile(t, path, contents)
}
