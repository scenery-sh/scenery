package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"scenery.sh/internal/devprocess"
)

type pluginReloadHostChild struct {
	process *devprocess.ManagedProcess
	input   *os.File
	output  *os.File
	frames  chan pluginReloadHostRead
	cancel  context.CancelFunc
	want    pluginReloadHostIdentity
}

type pluginReloadHostRead struct {
	frame pluginReloadFrame
	err   error
}

func startPluginReloadHost(parent context.Context, dir, binary string, env []string, want pluginReloadHostIdentity, stderr io.Writer) (*pluginReloadHostChild, pluginReloadFrame, error) {
	var empty pluginReloadFrame
	requests, input, err := os.Pipe()
	if err != nil {
		return nil, empty, err
	}
	output, responses, err := os.Pipe()
	if err != nil {
		_ = requests.Close()
		_ = input.Close()
		return nil, empty, err
	}
	ctx, cancel := context.WithCancel(parent)
	child := &pluginReloadHostChild{input: input, output: output, frames: make(chan pluginReloadHostRead, 1), cancel: cancel, want: want}
	child.process, err = devprocess.Start(ctx, devprocess.StartRequest{
		Name: "ONLV plugin reload host", Kind: "experiment", Command: binary, Dir: dir, Env: env, TailLines: 20,
		Stderr:    stderr,
		Configure: func(command *exec.Cmd) { command.ExtraFiles = []*os.File{requests, responses} },
	})
	_ = requests.Close()
	_ = responses.Close()
	if err != nil {
		cancel()
		_ = input.Close()
		_ = output.Close()
		return nil, empty, err
	}
	go func() {
		defer close(child.frames)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), pluginReloadFrameLimit)
		for scanner.Scan() {
			frame, decodeErr := pluginReloadDecodeFrame(scanner.Bytes())
			select {
			case child.frames <- pluginReloadHostRead{frame: frame, err: decodeErr}:
			case <-ctx.Done():
				return
			}
			if decodeErr != nil {
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			select {
			case child.frames <- pluginReloadHostRead{err: scanErr}:
			case <-ctx.Done():
			}
		}
	}()
	readyContext, cancelReady := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReady()
	ready, err := child.receive(readyContext)
	if err == nil && ready.Kind != "host_ready" {
		err = fmt.Errorf("plugin host omitted readiness attestation")
	}
	if err != nil {
		return child, ready, err
	}
	return child, ready, nil
}

func (c *pluginReloadHostChild) receive(ctx context.Context) (pluginReloadFrame, error) {
	select {
	case result, ok := <-c.frames:
		if !ok {
			return pluginReloadFrame{}, fmt.Errorf("plugin host closed its private channel")
		}
		if result.err != nil {
			return result.frame, result.err
		}
		if err := pluginReloadCheckHostIdentity(c.want, result.frame.Host); err != nil {
			return result.frame, err
		}
		if result.frame.PID != c.process.PID {
			return result.frame, fmt.Errorf("plugin response PID differs from supervised host")
		}
		return result.frame, nil
	case <-c.process.Done:
		return pluginReloadFrame{}, fmt.Errorf("plugin host exited before response: %v", c.process.WaitError())
	case <-ctx.Done():
		return pluginReloadFrame{}, ctx.Err()
	}
}

func (c *pluginReloadHostChild) call(ctx context.Context, request pluginReloadFrame) (pluginReloadFrame, error) {
	if request.Nonce == "" {
		request.Nonce = fmt.Sprint(time.Now().UnixNano())
	}
	data, err := json.Marshal(request)
	if err != nil || len(data) >= pluginReloadFrameLimit {
		return pluginReloadFrame{}, fmt.Errorf("encode bounded plugin request: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	if err := c.input.SetWriteDeadline(deadline); err != nil {
		return pluginReloadFrame{}, err
	}
	if _, err := c.input.Write(append(data, '\n')); err != nil {
		return pluginReloadFrame{}, err
	}
	response, err := c.receive(ctx)
	if err == nil && response.Nonce != request.Nonce {
		err = fmt.Errorf("plugin response does not match requested invocation")
	}
	return response, err
}

func (c *pluginReloadHostChild) close() error {
	defer c.cancel()
	defer func() { _ = c.input.Close(); _ = c.output.Close() }()
	select {
	case <-c.process.Done:
		return nil
	default:
	}
	if err := c.process.Stop(500 * time.Millisecond); err != nil {
		return err
	}
	select {
	case <-c.process.Done:
		return nil
	default:
		return fmt.Errorf("plugin host shutdown was not confirmed")
	}
}
