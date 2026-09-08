// Command verify checks the Scenery repository, not a Scenery application.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := runSceneryHarnessSelf(ctx, os.Stdout, os.Args[1:]); err != nil {
		var silent *silentCLIError
		if !errors.As(err, &silent) {
			fmt.Fprintln(os.Stderr, err)
		}
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			os.Exit(coded.ExitCode())
		}
		os.Exit(1)
	}
}

type codedCLIError struct {
	err  error
	code int
}

func (e *codedCLIError) Error() string { return e.err.Error() }
func (e *codedCLIError) Unwrap() error { return e.err }
func (e *codedCLIError) ExitCode() int { return e.code }

type silentCLIError struct {
	err  error
	code int
}

func (e *silentCLIError) Error() string { return e.err.Error() }
func (e *silentCLIError) Unwrap() error { return e.err }
func (e *silentCLIError) ExitCode() int { return e.code }
