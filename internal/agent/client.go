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
	"syscall"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/machine"
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
// A running agent is shared only when its schema and specification match.
// Build age never authorizes replacing another worktree's control plane.
func Ensure(ctx context.Context, current Identity) (*Client, error) {
	return EnsureWith(ctx, current, Paths{})
}

// EnsureWith is Ensure with an explicit agent home. A zero Paths resolves
// from the process environment (SCENERY_AGENT_HOME / ~/.scenery).
func EnsureWith(ctx context.Context, current Identity, paths Paths) (*Client, error) {
	if DisabledByEnv() {
		return nil, nil
	}
	if paths.Home == "" {
		var err error
		paths, err = DefaultPaths()
		if err != nil {
			return nil, err
		}
	}
	client := NewClient(paths.SocketPath)
	if health, err := client.Health(ctx); err == nil {
		if err := compatibleAgent(health, current); err != nil {
			client.CloseIdleConnections()
			return nil, err
		}
		return client, nil
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
		client.CloseIdleConnections()
		return nil, fmt.Errorf("failed_precondition: inspect existing scenery agent without replacing it: %w", err)
	}
	if err := StartProcess(paths, StartOptions{}); err != nil {
		return nil, err
	}
	return waitForAgentReady(ctx, client, paths)
}

func compatibleAgent(health HealthResponse, current Identity) error {
	if err := machine.ValidateArtifactIdentity(health.ArtifactIdentity, AgentStateKind, agentStateSchemaDescriptor, "connect to shared agent"); err != nil {
		return fmt.Errorf("failed_precondition: incompatible scenery agent (running %s; CLI %s; required spec %s): %w; existing sessions are untouched; use the matching CLI or an isolated SCENERY_AGENT_HOME and router address, or explicitly restart the agent after stopping its sessions", health.Identity, current, agentStateIdentity().SpecRevision, err)
	}
	return nil
}

func waitForAgentReady(ctx context.Context, client *Client, paths Paths) (*Client, error) {
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		health, err := client.Health(ctx)
		if err == nil {
			if err := compatibleAgent(health, Identity{}); err != nil {
				return nil, err
			}
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

// StartProcess starts the local agent. When a launchd supervisor owns the
// requested socket it starts the agent through launchd instead of spawning an
// unsupervised process, so every stop/start path cooperates with supervision.
func StartProcess(paths Paths, opts StartOptions) error {
	if err := EnsureDirs(paths); err != nil {
		return err
	}
	if startSupervisedAgentProcess(paths) {
		return nil
	}
	if startSystemdSupervisedAgent(paths) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, agentProcessArgs(paths, opts)...)
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

// agentProcessArgs is the singular agent invocation shape shared by direct
// spawns and the launchd supervisor plist.
func agentProcessArgs(paths Paths, opts StartOptions) []string {
	routerAddr := strings.TrimSpace(opts.RouterAddr)
	if routerAddr == "" {
		routerAddr = RouterAddrFromEnv()
	}
	args := []string{"system", "agent", "--socket", paths.SocketPath, "--router-listen", routerAddr}
	switch {
	case opts.Trust:
		args = append(args, "--trust")
	case opts.RouterTLS:
		args = append(args, "--router-tls")
	default:
		args = append(args, "--router-http")
	}
	return args
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
	defer func() { _ = resp.Body.Close() }()
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
