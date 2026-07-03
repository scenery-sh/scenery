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

	"scenery.sh/internal/envpolicy"
)

type Client struct {
	socketPath string
	http       *http.Client
}

type HTTPError struct {
	Method     string
	Path       string
	Status     string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("agent %s %s failed: %s: %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

func IsNotFound(err error) bool {
	httpErr, ok := errors.AsType[*HTTPError](err)
	return ok && httpErr.StatusCode == http.StatusNotFound
}

type StartOptions struct {
	RouterAddr string
	RouterTLS  bool
	RouterHTTP bool
	Trust      bool
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

// Ensure connects to the local agent, starting one when none is running.
// When a running agent reports an older build identity than current (or no
// identity because it predates identity reporting), Ensure restarts it once so
// commands talk to an agent built from the current binary. A zero current
// identity accepts any running agent.
func Ensure(ctx context.Context, current Identity) (*Client, error) {
	if DisabledByEnv() {
		return nil, nil
	}
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	client := NewClient(paths.SocketPath)
	if health, err := client.Health(ctx); err == nil {
		if !ShouldReplaceAgent(current, health.Identity) {
			return client, nil
		}
		if err := replaceRunningAgent(ctx, client, paths, health); err != nil {
			return nil, fmt.Errorf("restart outdated scenery agent (running %s, current %s): %w", health.Identity, current, err)
		}
		return client, nil
	}
	if err := StartProcess(paths, StartOptions{}); err != nil {
		return nil, err
	}
	return waitForAgentReady(ctx, client, paths, 0)
}

// replaceRunningAgent stops the running agent described by health and starts a
// new one from the current executable, preserving the router address and
// internal router scheme so registered route URLs stay valid. It attempts the
// replacement once; it never re-checks identity, so it cannot loop.
func replaceRunningAgent(ctx context.Context, client *Client, paths Paths, health HealthResponse) error {
	if health.PID > 0 {
		if err := terminateProcess(health.PID); err != nil {
			return fmt.Errorf("stop scenery agent pid %d: %w", health.PID, err)
		}
		if err := waitForAgentExit(ctx, client, health.PID); err != nil {
			return err
		}
	}
	client.CloseIdleConnections()
	opts := StartOptions{
		RouterAddr: health.RouterAddr,
		RouterTLS:  internalRouterTLS(health),
	}
	if err := StartProcess(paths, opts); err != nil {
		return err
	}
	_, err := waitForAgentReady(ctx, client, paths, health.PID)
	return err
}

func internalRouterTLS(health HealthResponse) bool {
	if scheme := strings.TrimSpace(health.InternalRouterScheme); scheme != "" {
		return scheme == "https"
	}
	// Old agents do not report the internal router scheme. With an active
	// edge the internal router serves HTTP behind it; without one the public
	// router scheme is the internal scheme.
	return health.Edge == nil && health.RouterScheme == "https"
}

func waitForAgentExit(ctx context.Context, client *Client, pid int) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		health, err := client.Health(ctx)
		if err != nil || health.PID != pid {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for scenery agent pid %d to stop", pid)
}

func waitForAgentReady(ctx context.Context, client *Client, paths Paths, oldPID int) (*Client, error) {
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		health, err := client.Health(ctx)
		if err == nil && (oldPID == 0 || health.PID != oldPID) {
			return client, nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), lastErr)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("timed out waiting for scenery agent at %s: %w", paths.SocketPath, lastErr)
}

func StartProcess(paths Paths, opts StartOptions) error {
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
	routerAddr := strings.TrimSpace(opts.RouterAddr)
	if routerAddr == "" {
		routerAddr = RouterAddrFromEnv()
	}
	routerTLS := opts.RouterTLS
	args := []string{"system", "agent", "--socket", paths.SocketPath, "--router-listen", routerAddr}
	if opts.Trust {
		args = append(args, "--trust")
	} else if routerTLS {
		args = append(args, "--router-tls")
	} else {
		args = append(args, "--router-http")
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = envpolicy.Environ()
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

// CloseIdleConnections drops any keep-alive connections held by the client so
// a server being shut down does not wait on them.
func (c *Client) CloseIdleConnections() {
	if c == nil || c.http == nil {
		return
	}
	c.http.CloseIdleConnections()
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

func (c *Client) DeleteOwned(ctx context.Context, sessionID string, ownerPID int, signal bool) (Session, bool, error) {
	return c.deleteOwned(ctx, sessionID, ownerPID, Owner{}, false, signal)
}

func (c *Client) DeleteOwnedSession(ctx context.Context, session Session, signal bool) (Session, bool, error) {
	ownerPID := firstPositive(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		return c.DeleteUnowned(ctx, session.SessionID)
	}
	owner := session.Owner
	if owner.PID != ownerPID {
		owner = Owner{}
	}
	return c.deleteOwned(ctx, session.SessionID, ownerPID, owner, true, signal)
}

func (c *Client) deleteOwned(ctx context.Context, sessionID string, ownerPID int, owner Owner, strict bool, signal bool) (Session, bool, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID)
	values := url.Values{}
	if signal {
		values.Set("signal", "1")
	}
	if ownerPID > 0 {
		values.Set("owner_pid", fmt.Sprint(ownerPID))
	}
	if strict {
		values.Set("owner_strict", "1")
	}
	if owner.PID == ownerPID && owner.PID > 0 {
		if owner.StartedAt != "" {
			values.Set("owner_started_at", owner.StartedAt)
		}
		if owner.CmdlineHash != "" {
			values.Set("owner_cmdline_hash", owner.CmdlineHash)
		}
		if owner.Exe != "" {
			values.Set("owner_exe", owner.Exe)
		}
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out RegisterResponse
	if err := c.doJSON(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return Session{}, false, err
	}
	return out.Session, out.Deleted, nil
}

func (c *Client) DeleteUnowned(ctx context.Context, sessionID string) (Session, bool, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "?owner_pid=none"
	var out RegisterResponse
	if err := c.doJSON(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return Session{}, false, err
	}
	return out.Session, out.Deleted, nil
}

func (c *Client) UpsertSubstrate(ctx context.Context, req UpsertSubstrateRequest) (Substrate, error) {
	var out SubstrateResponse
	if err := c.postJSON(ctx, "/v1/substrates", req, &out); err != nil {
		return Substrate{}, err
	}
	return out.Substrate, nil
}

func (c *Client) GetSubstrate(ctx context.Context, kind string) (Substrate, error) {
	var out SubstrateResponse
	if err := c.getJSON(ctx, "/v1/substrates/"+url.PathEscape(kind), &out); err != nil {
		return Substrate{}, err
	}
	return out.Substrate, nil
}

func (c *Client) ListSubstrates(ctx context.Context) ([]Substrate, error) {
	var out SubstratesResponse
	if err := c.getJSON(ctx, "/v1/substrates", &out); err != nil {
		return nil, err
	}
	return out.Substrates, nil
}

func (c *Client) DeleteSubstrate(ctx context.Context, kind string) (Substrate, error) {
	var out SubstrateResponse
	if err := c.doJSON(ctx, http.MethodDelete, "/v1/substrates/"+url.PathEscape(kind), nil, &out); err != nil {
		return Substrate{}, err
	}
	return out.Substrate, nil
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
		return &HTTPError{
			Method:     method,
			Path:       path,
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Body:       string(data),
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
