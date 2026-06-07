package main

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

type devManagedProcess struct {
	Name      string
	Kind      string
	Role      string
	PID       int
	Cmd       *exec.Cmd
	Tail      *safeLineTail
	StartedAt time.Time

	done       chan struct{}
	outputDone chan struct{}
	stopOnce   sync.Once

	mu      sync.Mutex
	waitErr error
}

type devProcessStartRequest struct {
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
	Configure func(*exec.Cmd)
}

type devReadinessProbe func(context.Context) error

type devProcessReadyRequest struct {
	Timeout  time.Duration
	Interval time.Duration
	Probe    devReadinessProbe
}

func startDevManagedProcess(ctx context.Context, req devProcessStartRequest) (*devManagedProcess, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, fmt.Errorf("missing dev process command")
	}
	cmd := commandTreeContext(ctx, req.Command, req.Args...)
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
	p := &devManagedProcess{
		Name:       req.Name,
		Kind:       req.Kind,
		Role:       req.Role,
		PID:        cmd.Process.Pid,
		Cmd:        cmd,
		Tail:       &safeLineTail{limit: tailLines},
		StartedAt:  time.Now().UTC(),
		done:       make(chan struct{}),
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
		close(p.done)
	}()
	return p, nil
}

func (p *devManagedProcess) captureOutput(wg *sync.WaitGroup, stream string, src io.Reader, dst io.Writer, onOutput func(int, string, []byte)) {
	defer wg.Done()
	reader := bufio.NewReader(src)
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			if dst != nil {
				_, _ = dst.Write(chunk)
			}
			plain := stripANSI(chunk)
			if p.Tail != nil {
				p.Tail.Add(strings.TrimRight(string(plain), "\n"))
			}
			if onOutput != nil {
				onOutput(p.PID, stream, plain)
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *devManagedProcess) WaitReady(ctx context.Context, req devProcessReadyRequest) error {
	if p == nil {
		return fmt.Errorf("missing dev process")
	}
	if req.Timeout <= 0 {
		req.Timeout = managedFrontendStartupTimeout
	}
	if req.Interval <= 0 {
		req.Interval = 100 * time.Millisecond
	}
	deadline := time.NewTimer(req.Timeout)
	defer deadline.Stop()
	if req.Probe == nil {
		select {
		case <-ctx.Done():
			_ = p.Stop(stopTimeout)
			return ctx.Err()
		case <-p.done:
			return p.notReadyExitError()
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
			_ = p.Stop(stopTimeout)
			return ctx.Err()
		case <-p.done:
			return p.notReadyExitError()
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

func (p *devManagedProcess) Interrupt() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	select {
	case <-p.done:
		return nil
	default:
	}
	return interruptProcessTree(p.Cmd)
}

func (p *devManagedProcess) WaitOrKill(grace time.Duration) error {
	if p == nil {
		return nil
	}
	if grace <= 0 {
		grace = stopTimeout
	}
	select {
	case <-p.done:
		return p.expectedWaitErr()
	case <-time.After(grace):
		if p.Cmd != nil {
			_ = killProcessTree(p.Cmd)
		}
		select {
		case <-p.done:
			return p.expectedWaitErr()
		case <-time.After(time.Second):
			return fmt.Errorf("%s did not exit after SIGKILL", p.label())
		}
	}
}

func (p *devManagedProcess) Stop(grace time.Duration) error {
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

func (p *devManagedProcess) waitError() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *devManagedProcess) expectedWaitErr() error {
	err := p.waitError()
	if err == nil || isExpectedExit(err) {
		return nil
	}
	return err
}

func (p *devManagedProcess) notReadyExitError() error {
	p.waitBrieflyForOutput()
	err := p.waitError()
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

func (p *devManagedProcess) notReadyTimeoutError(timeout time.Duration, lastProbeErr error) error {
	message := fmt.Sprintf("%s did not become ready within %s", p.label(), timeout)
	if lastProbeErr != nil {
		message += ": " + lastProbeErr.Error()
	}
	if tail := p.tailString(); tail != "" {
		message += "\n" + tail
	}
	return errors.New(message)
}

func (p *devManagedProcess) waitBrieflyForOutput() {
	if p == nil {
		return
	}
	select {
	case <-p.outputDone:
	case <-time.After(2 * time.Second):
	}
}

func (p *devManagedProcess) tailString() string {
	if p == nil || p.Tail == nil {
		return ""
	}
	return strings.TrimSpace(p.Tail.String())
}

func (p *devManagedProcess) label() string {
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
