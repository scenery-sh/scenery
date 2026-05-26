package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	socketPath string
	http       *http.Client
}

func NewClient(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{
		socketPath: socketPath,
		http:       &http.Client{Transport: transport, Timeout: 5 * time.Second},
	}
}

func DefaultClient() (*Client, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	return NewClient(paths.SocketPath), nil
}

func Ensure(ctx context.Context) (*Client, error) {
	if DisabledByEnv() {
		return nil, nil
	}
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	client := NewClient(paths.SocketPath)
	if err := client.Ping(ctx); err == nil {
		return client, nil
	}
	if err := startAgentProcess(paths); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return client, nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), lastErr)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("timed out waiting for onlava agent at %s: %w", paths.SocketPath, lastErr)
}

func startAgentProcess(paths Paths) error {
	if err := EnsureDirs(paths); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "agent", "--socket", paths.SocketPath, "--router-listen", RouterAddrFromEnv())
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureAgentProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return cmd.Process.Release()
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Health(ctx)
	return err
}

func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var out HealthResponse
	err := c.getJSON(ctx, "/v1/health", &out)
	return out, err
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (Session, error) {
	var out RegisterResponse
	if err := c.postJSON(ctx, "/v1/sessions", req, &out); err != nil {
		return Session{}, err
	}
	return out.Session, nil
}

func (c *Client) List(ctx context.Context, appRoot string) ([]Session, error) {
	path := "/v1/sessions"
	if strings.TrimSpace(appRoot) != "" {
		path += "?app_root=" + url.QueryEscape(filepath.Clean(appRoot))
	}
	var out StatusResponse
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (c *Client) Delete(ctx context.Context, sessionID string, signal bool) (Session, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID)
	if signal {
		path += "?signal=1"
	}
	var out RegisterResponse
	if err := c.doJSON(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return Session{}, err
	}
	return out.Session, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	return c.doJSON(ctx, http.MethodPost, path, in, out)
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("agent %s %s failed: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
