package build

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// GoCommandError is a rejected application build or dependency resolution.
// Launch failures and canceled commands keep their original classification.
type GoCommandError struct{ Err error }

func (e *GoCommandError) Error() string { return e.Err.Error() }
func (e *GoCommandError) Unwrap() error { return e.Err }
func (*GoCommandError) ExitCode() int   { return 3 }

func goCommandFailure(ctx context.Context, args []string, output []byte, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reported := fmt.Errorf("go %s failed: %w\n%s", strings.Join(args, " "), err, output)
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return &GoCommandError{Err: reported}
	}
	return reported
}
