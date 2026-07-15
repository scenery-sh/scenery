package symphony

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrRunStalled reports that the codex app-server stopped emitting activity
// for longer than the run's stall timeout while a turn was in flight.
var ErrRunStalled = errors.New("codex app-server stalled")

// CodexAppServerClient is a JSON-RPC client for one `codex app-server` child
// process speaking newline-delimited JSON over stdio. It tracks pending
// request/response pairs, forwards server notifications to the configured
// handler, and records notification activity for stall detection.
type CodexAppServerClient struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	pendingMu      sync.Mutex
	pending        map[int]chan codexRPCMessage
	nextID         int
	done           chan error
	turnDone       chan struct{}
	onNotification func(string, json.RawMessage)
	activityMu     sync.Mutex
	lastActivity   time.Time
}

type codexRPCMessage struct {
	ID     any             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewCodexAppServerClient starts `codex app-server --listen stdio://` and
// returns a client wired to its stdio. Server notifications are delivered to
// onNotification from the read loop goroutine.
func NewCodexAppServerClient(ctx context.Context, onNotification func(string, json.RawMessage)) (*CodexAppServerClient, error) {
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	client := &CodexAppServerClient{
		cmd:            cmd,
		stdin:          stdin,
		pending:        map[int]chan codexRPCMessage{},
		done:           make(chan error, 1),
		turnDone:       make(chan struct{}),
		onNotification: onNotification,
		lastActivity:   time.Now(),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go io.Copy(io.Discard, stderr)
	go client.readLoop(stdout)
	go func() { client.done <- cmd.Wait() }()
	return client, nil
}

// PID reports the app-server process id, or zero when no process is running.
func (c *CodexAppServerClient) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Close interrupts the app-server process and waits briefly for it to exit,
// escalating to a kill when it does not stop in time.
func (c *CodexAppServerClient) Close() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.stdin.Close()
	if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	return nil
}

// Call sends one JSON-RPC request and waits for its response, the process
// exiting, or ctx cancellation.
func (c *CodexAppServerClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.pendingMu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan codexRPCMessage, 1)
	c.pending[id] = ch
	c.pendingMu.Unlock()

	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	case err := <-c.done:
		return nil, fmt.Errorf("codex app-server exited: %w", err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *CodexAppServerClient) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var msg codexRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if id, ok := numericID(msg.ID); ok {
			c.pendingMu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.pendingMu.Unlock()
			if ch != nil {
				ch <- msg
				continue
			}
		}
		if msg.Method != "" {
			c.markActivity()
			if c.onNotification != nil {
				c.onNotification(msg.Method, msg.Params)
			}
			if msg.Method == "turn/completed" {
				select {
				case <-c.turnDone:
				default:
					close(c.turnDone)
				}
			}
		}
	}
}

// WaitForTurnCompleted blocks until the server emits `turn/completed`,
// returning ErrRunStalled when no notification activity arrives within
// stallTimeout, the process exit error when the server dies first, or the
// context error on cancellation. A non-positive stallTimeout defaults to
// five minutes.
func (c *CodexAppServerClient) WaitForTurnCompleted(ctx context.Context, stallTimeout time.Duration) error {
	if stallTimeout <= 0 {
		stallTimeout = 5 * time.Minute
	}
	tick := stallTimeout / 4
	if tick <= 0 || tick > time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	select {
	case <-c.turnDone:
		return nil
	default:
	}
	for {
		select {
		case <-c.turnDone:
			return nil
		case err := <-c.done:
			return fmt.Errorf("codex app-server exited before turn completed: %w", err)
		case <-ticker.C:
			if c.idleFor() >= stallTimeout {
				return ErrRunStalled
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *CodexAppServerClient) markActivity() {
	c.activityMu.Lock()
	c.lastActivity = time.Now()
	c.activityMu.Unlock()
}

func (c *CodexAppServerClient) idleFor() time.Duration {
	c.activityMu.Lock()
	last := c.lastActivity
	c.activityMu.Unlock()
	if last.IsZero() {
		return 0
	}
	return time.Since(last)
}

func numericID(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}
