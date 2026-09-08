package devprocess

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type ManagedProcess struct {
	Name      string
	Kind      string
	Role      string
	PID       int
	Cmd       *exec.Cmd
	Tail      *LineTail
	StartedAt time.Time
	Filter    func(pid int, stream string, data []byte) []byte
	Done      <-chan struct{}

	outputDone chan struct{}
	stopOnce   sync.Once

	mu      sync.Mutex
	waitErr error
}

type StartRequest struct {
	Name      string
	Kind      string
	Role      string
	Dir       string
	Command   string
	Args      []string
	Env       []string
	Stdout    io.Writer
	Stderr    io.Writer
	TailLines int
	OnOutput  func(pid int, stream string, data []byte)
	Filter    func(pid int, stream string, data []byte) []byte
	Configure func(*exec.Cmd)
}

type ReadinessProbe func(context.Context) error

type ReadyRequest struct {
	Timeout  time.Duration
	Interval time.Duration
	Probe    ReadinessProbe
}

func Start(ctx context.Context, req StartRequest) (*ManagedProcess, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, fmt.Errorf("missing dev process command")
	}
	cmd := CommandContext(ctx, req.Command, req.Args...)
	cmd.Dir = req.Dir
	if req.Env != nil {
		cmd.Env = append([]string(nil), req.Env...)
	}
	if req.Configure != nil {
		req.Configure(cmd)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tailLines := req.TailLines
	if tailLines <= 0 {
		tailLines = 80
	}
	done := make(chan struct{})
	p := &ManagedProcess{
		Name:       req.Name,
		Kind:       req.Kind,
		Role:       req.Role,
		PID:        cmd.Process.Pid,
		Cmd:        cmd,
		Tail:       &LineTail{limit: tailLines},
		StartedAt:  time.Now().UTC(),
		Filter:     req.Filter,
		Done:       done,
		outputDone: make(chan struct{}),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go p.captureOutput(&wg, "stdout", stdout, req.Stdout, req.OnOutput)
	go p.captureOutput(&wg, "stderr", stderr, req.Stderr, req.OnOutput)
	go func() {
		wg.Wait()
		close(p.outputDone)
		err := cmd.Wait()
		p.mu.Lock()
		p.waitErr = err
		p.mu.Unlock()
		close(done)
	}()
	return p, nil
}

func (p *ManagedProcess) captureOutput(wg *sync.WaitGroup, stream string, src io.Reader, dst io.Writer, onOutput func(int, string, []byte)) {
	defer wg.Done()
	reader := bufio.NewReader(src)
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			plain := stripANSI(chunk)
			output := chunk
			eventOutput := plain
			if reqFilter := p.outputFilter(); reqFilter != nil {
				eventOutput = reqFilter(p.PID, stream, plain)
				output = eventOutput
			}
			if len(eventOutput) > 0 && dst != nil {
				_, _ = dst.Write(output)
			}
			if p.Tail != nil {
				if len(eventOutput) > 0 {
					p.Tail.Add(strings.TrimRight(string(eventOutput), "\n"))
				}
			}
			if onOutput != nil && len(eventOutput) > 0 {
				onOutput(p.PID, stream, eventOutput)
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *ManagedProcess) outputFilter() func(int, string, []byte) []byte {
	if p == nil {
		return nil
	}
	return p.Filter
}

func (p *ManagedProcess) WaitReady(ctx context.Context, req ReadyRequest) error {
	if p == nil {
		return fmt.Errorf("missing dev process")
	}
	if req.Timeout <= 0 {
		req.Timeout = DefaultStartupTimeout
	}
	if req.Interval <= 0 {
		req.Interval = 100 * time.Millisecond
	}
	deadline := time.NewTimer(req.Timeout)
	defer deadline.Stop()
	if req.Probe == nil {
		select {
		case <-ctx.Done():
			_ = p.Stop(DefaultStopTimeout)
			return ctx.Err()
		case <-p.Done:
			return p.ReadinessExitError()
		case <-deadline.C:
			return nil
		}
	}
	ticker := time.NewTicker(req.Interval)
	defer ticker.Stop()
	var lastProbeErr error
	for {
		select {
		case <-ctx.Done():
			_ = p.Stop(DefaultStopTimeout)
			return ctx.Err()
		case <-p.Done:
			return p.ReadinessExitError()
		case <-ticker.C:
			if err := req.Probe(ctx); err != nil {
				lastProbeErr = err
				continue
			}
			return nil
		case <-deadline.C:
			return p.notReadyTimeoutError(req.Timeout, lastProbeErr)
		}
	}
}

func (p *ManagedProcess) Interrupt() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	select {
	case <-p.Done:
		return nil
	default:
	}
	return InterruptTree(p.Cmd)
}

func (p *ManagedProcess) WaitOrKill(grace time.Duration) error {
	if p == nil {
		return nil
	}
	if grace <= 0 {
		grace = DefaultStopTimeout
	}
	select {
	case <-p.Done:
		return p.expectedWaitErr()
	case <-time.After(grace):
		if p.Cmd != nil {
			_ = KillTree(p.Cmd)
		}
		select {
		case <-p.Done:
			return p.expectedWaitErr()
		case <-time.After(time.Second):
			return fmt.Errorf("%s did not exit after SIGKILL", p.label())
		}
	}
}

func (p *ManagedProcess) Stop(grace time.Duration) error {
	var err error
	p.stopOnce.Do(func() {
		if interruptErr := p.Interrupt(); interruptErr != nil {
			err = interruptErr
			return
		}
		err = p.WaitOrKill(grace)
	})
	return err
}

func (p *ManagedProcess) WaitError() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *ManagedProcess) expectedWaitErr() error {
	err := p.WaitError()
	if err == nil || IsExpectedExit(err) {
		return nil
	}
	return err
}

func (p *ManagedProcess) ReadinessExitError() error {
	p.waitBrieflyForOutput()
	err := p.WaitError()
	message := fmt.Sprintf("%s exited before becoming ready", p.label())
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += ": process exited without an error"
	}
	if tail := p.tailString(); tail != "" {
		message += "\n" + tail
	}
	return errors.New(message)
}

func (p *ManagedProcess) notReadyTimeoutError(timeout time.Duration, lastProbeErr error) error {
	message := fmt.Sprintf("%s did not become ready within %s", p.label(), timeout)
	if lastProbeErr != nil {
		message += ": " + lastProbeErr.Error()
	}
	if tail := p.tailString(); tail != "" {
		message += "\n" + tail
	}
	return errors.New(message)
}

func (p *ManagedProcess) waitBrieflyForOutput() {
	if p == nil {
		return
	}
	select {
	case <-p.outputDone:
	case <-time.After(2 * time.Second):
	}
}

func (p *ManagedProcess) tailString() string {
	if p == nil || p.Tail == nil {
		return ""
	}
	return strings.TrimSpace(p.Tail.String())
}

func (p *ManagedProcess) label() string {
	if p == nil {
		return "dev process"
	}
	kind := strings.TrimSpace(p.Kind)
	name := strings.TrimSpace(p.Name)
	switch {
	case kind != "" && name != "":
		return kind + " " + name
	case name != "":
		return name
	case kind != "":
		return kind
	default:
		return "dev process"
	}
}
