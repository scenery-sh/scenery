package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPulseRunBasicApp(t *testing.T) {
	repo := repoRoot(t)
	appDir := filepath.Join(repo, "testdata", "apps", "basic")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = os.Environ()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer func() {
		cancel()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for pulse process to exit")
		}
	}()

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")

	postJSON(t, "http://"+addr+"/echo/Alice?title=Dr", map[string]string{"body": "body"}, map[string]string{"X-Echo": "hdr"}, http.StatusOK, map[string]any{"message": "hi Alice Dr hdr body"})
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
	getJSON(t, "http://"+addr+"/service.CustomStatus", nil, http.StatusCreated, map[string]any{"message": "created"})
	getJSON(t, "http://"+addr+"/service.AuthEcho", map[string]string{"Authorization": "Bearer token123"}, http.StatusOK, map[string]any{"user": "user-1", "role": "admin"})
	getJSON(t, "http://"+addr+"/raw/alpha/beta", nil, http.StatusOK, map[string]any{"path": "alpha/beta", "method": "GET"})
}

func buildPulseBinary(t *testing.T, repo string) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "pulse")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/pulse")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build pulse binary: %v\n%s", err, output)
	}
	return binPath
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server did not start: %s", url)
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

func assertJSONResponse(t *testing.T, req *http.Request, wantStatus int, want map[string]any) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d, want %d: %s", resp.StatusCode, wantStatus, body)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(got, want) {
		t.Fatalf("unexpected body: got=%v want=%v", got, want)
	}
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

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strings.Split(ln.Addr().String(), ":")[1]
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(wd)
}
