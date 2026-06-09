package main

import (
	"fmt"
	"os"

	"github.com/pbrazdil/onlava/internal/neonselfhost"
)

func internalCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: onlava internal neon-selfhost-driver ...")
	}
	switch args[0] {
	case "neon-selfhost-driver":
		return neonselfhost.Run(os.Stdout, cliStderr, args[1:])
	default:
		return fmt.Errorf("unknown internal command %q", args[0])
	}
}
