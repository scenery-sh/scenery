package main

import (
	"encoding/json"
	"fmt"
	"io"
	"scenery.sh/internal/evolution"
)

func runProviderLock(stdout io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "lock" {
		return fmt.Errorf("invalid_request: usage: scenery provider lock [--check] [--app-root <path>] [-o human|json]")
	}
	var root, output string
	var check bool
	flags := newCLIFlagSet("provider lock")
	flags.StringVar(&root, "app-root", "", "")
	flags.StringVar(&output, "o", "human", "")
	flags.BoolVar(&check, "check", false, "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if len(positionals) != 0 {
		return fmt.Errorf("invalid_request: provider lock takes no positional arguments")
	}
	if err := validateContractOutput(output); err != nil {
		return &codedCLIError{err: err, code: 2}
	}
	root, err = findContractRoot(root)
	if err != nil {
		return fmt.Errorf("failed_precondition: %w", err)
	}
	result, err := evolution.SyncBuiltinProviderLocks(root, check)
	if err != nil {
		return err
	}
	if output == "json" {
		if err := json.NewEncoder(stdout).Encode(newCLIEnvelope(!check || !result.Changed, result, nil)); err != nil {
			return err
		}
	} else {
		status := "current"
		if result.Changed {
			status = "updated"
			if check {
				status = "needs update"
			}
		}
		_, _ = fmt.Fprintf(stdout, "scenery: %s %s (%d declared builtin providers; offline)\n", result.Path, status, len(result.Providers))
	}
	if check && result.Changed {
		return &silentCLIError{err: fmt.Errorf("provider locks need updating; run scenery provider lock"), code: 1}
	}
	return nil
}
