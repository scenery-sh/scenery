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

	"scenery.sh/internal/contract"
	"scenery.sh/internal/devprocess"
)

type nativeReloadChild struct {
	process                       *devprocess.ManagedProcess
	input                         *os.File
	output                        *os.File
	frames                        chan nativeReloadRead
	cancel                        context.CancelFunc
	want                          nativeReloadIdentity
	startCallMS                   float64
	startRequested, startReturned time.Time
}

type nativeReloadRead struct {
	frame nativeReloadFrame
	err   error
}

func startNativeReloadChild(parent context.Context, dir, binary string, env []string, want nativeReloadIdentity, stderr io.Writer) (*nativeReloadChild, nativeReloadFrame, error) {
	var empty nativeReloadFrame
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
	child := &nativeReloadChild{input: input, output: output, frames: make(chan nativeReloadRead, 1), cancel: cancel, want: want}
	startCall := time.Now()
	child.process, err = devprocess.Start(ctx, devprocess.StartRequest{
		Name: "ONLV AHJ island", Kind: "experiment", Command: binary, Dir: dir, Env: env, TailLines: 20,
		Stderr:    stderr,
		Configure: func(command *exec.Cmd) { command.ExtraFiles = []*os.File{requests, responses} },
	})
	child.startRequested, child.startReturned = startCall, time.Now()
	child.startCallMS = nativeReloadMS(child.startReturned.Sub(startCall))
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
		scanner.Buffer(make([]byte, 4096), nativeReloadFrameLimit)
		for scanner.Scan() {
			frame, decodeErr := nativeReloadDecodeFrame(scanner.Bytes())
			select {
			case child.frames <- nativeReloadRead{frame: frame, err: decodeErr}:
			case <-ctx.Done():
				return
			}
			if decodeErr != nil {
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			select {
			case child.frames <- nativeReloadRead{err: scanErr}:
			case <-ctx.Done():
			}
		}
	}()
	attestationContext, cancelAttestation := context.WithTimeout(ctx, 5*time.Second)
	defer cancelAttestation()
	hello, err := child.receive(attestationContext)
	if err == nil && (hello.Kind != "attested" || hello.Constructed) {
		err = fmt.Errorf("candidate constructed before activation or omitted attestation")
	}
	if err != nil {
		return child, hello, err
	}
	return child, hello, nil
}

func (c *nativeReloadChild) receive(ctx context.Context) (nativeReloadFrame, error) {
	select {
	case result, ok := <-c.frames:
		if !ok {
			return nativeReloadFrame{}, fmt.Errorf("candidate closed its private channel")
		}
		if result.err != nil {
			return result.frame, result.err
		}
		if err := nativeReloadCheckIdentity(c.want, result.frame.Identity); err != nil {
			return result.frame, err
		}
		if result.frame.PID != c.process.PID {
			return result.frame, fmt.Errorf("response PID differs from supervised child")
		}
		return result.frame, nil
	case <-c.process.Done:
		// A terminal error frame may already have been delivered before exit.
		select {
		case result, ok := <-c.frames:
			if ok && result.err == nil && result.frame.PID == c.process.PID {
				return result.frame, nativeReloadCheckIdentity(c.want, result.frame.Identity)
			}
		case <-ctx.Done():
			return nativeReloadFrame{}, ctx.Err()
		}
		return nativeReloadFrame{}, fmt.Errorf("candidate exited before response: %v", c.process.WaitError())
	case <-ctx.Done():
		return nativeReloadFrame{}, ctx.Err()
	}
}

func (c *nativeReloadChild) call(ctx context.Context, request nativeReloadFrame) (nativeReloadFrame, error) {
	if request.Nonce == "" {
		request.Nonce = fmt.Sprint(time.Now().UnixNano())
	}
	data, err := json.Marshal(request)
	if err != nil || len(data) >= nativeReloadFrameLimit {
		return nativeReloadFrame{}, fmt.Errorf("encode bounded request: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	if err := c.input.SetWriteDeadline(deadline); err != nil {
		return nativeReloadFrame{}, err
	}
	if _, err := c.input.Write(append(data, '\n')); err != nil {
		return nativeReloadFrame{}, err
	}
	response, err := c.receive(ctx)
	if err == nil && response.Nonce != request.Nonce {
		err = fmt.Errorf("response does not match the requested invocation")
	}
	return response, err
}

func (c *nativeReloadChild) close() error {
	defer c.cancel()
	defer func() { _ = c.input.Close(); _ = c.output.Close() }()
	select {
	case <-c.process.Done:
		return nil // Negative cases separately assert the expected exit status.
	default:
	}
	if err := c.process.Stop(500 * time.Millisecond); err != nil {
		return err
	}
	select {
	case <-c.process.Done:
		return nil
	default:
		return fmt.Errorf("candidate shutdown was not confirmed")
	}
}

func nativeReloadProveRejections(ctx context.Context, child *nativeReloadChild) ([]string, error) {
	var assertions []string
	for _, mutation := range []struct {
		name    string
		edit    func(*nativeReloadFrame)
		failure string
	}{
		{"invoke_before_activation", func(f *nativeReloadFrame) { f.Kind = "invoke" }, "not_active"},
		{"cross_session", func(f *nativeReloadFrame) { f.Identity.Session += "-foreign" }, "identity_mismatch"},
		{"wrong_worktree", func(f *nativeReloadFrame) { f.Identity.Worktree += "-foreign" }, "identity_mismatch"},
		{"wrong_abi", func(f *nativeReloadFrame) { f.Identity.ABI += "-other" }, "identity_mismatch"},
		{"wrong_producer", func(f *nativeReloadFrame) { f.Identity.FrameworkExecutable = nativeReloadDigest([]byte("other")) }, "identity_mismatch"},
		{"wrong_artifact", func(f *nativeReloadFrame) { f.Identity.ExecutableDigest = nativeReloadDigest([]byte("other")) }, "identity_mismatch"},
		{"wrong_generation", func(f *nativeReloadFrame) { f.Identity.ExecutionGeneration = nativeReloadDigest([]byte("other")) }, "identity_mismatch"},
	} {
		request := nativeReloadFrame{Kind: "activate", Identity: child.want}
		mutation.edit(&request)
		response, err := child.call(ctx, request)
		if err != nil || response.Failure != mutation.failure || response.Constructed || response.Kind != "rejected" {
			return assertions, fmt.Errorf("%s failed: response=%+v: %w", mutation.name, response, err)
		}
		assertions = append(assertions, mutation.name)
	}
	return assertions, nil
}

func nativeReloadCheckBehavior(response nativeReloadFrame, expected string) error {
	if response.Kind != "result" || response.Failure != "" || !response.Constructed ||
		response.Operation != nativeReloadOperation || response.Binding != nativeReloadBinding {
		return fmt.Errorf("new implementation did not return a typed result")
	}
	kind, name, payload, err := contract.DecodeContractOutcomeEnvelope(response.Outcome)
	if err != nil || kind != "error" || name != "invalid_input" {
		return fmt.Errorf("unexpected generated outcome kind=%s name=%s: %w", kind, name, err)
	}
	var problem contract.Problem
	if err := json.Unmarshal(payload, &problem); err != nil {
		return err
	}
	if problem.Message != expected {
		return fmt.Errorf("response executed a different implementation: %q, want %q", problem.Message, expected)
	}
	return nil
}
