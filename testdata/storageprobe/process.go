package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// A child is killed only after its public filesystem operation reaches the
// requested boundary. Every started process is reaped, including failed probes.
func killAtPhase(ctx context.Context, root, phase string, whileHeld func() error) (resultErr error) {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, "--root", root, "--child", phase)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdin.Close() }()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make(chan error, 1)
	go func() {
		var message struct {
			Phase string `json:"phase"`
		}
		err := json.NewDecoder(stdout).Decode(&message)
		if err == nil && message.Phase != phase {
			err = fmt.Errorf("unexpected child phase %q", message.Phase)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			return fmt.Errorf("wait for native child %s: %w", phase, err)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if whileHeld != nil {
		if err := whileHeld(); err != nil {
			return err
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		return err
	}
	err = cmd.Wait()
	reaped = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Success() {
		return fmt.Errorf("native %s child was not killed: %v: %s", phase, err, stderr.String())
	}
	return nil
}
