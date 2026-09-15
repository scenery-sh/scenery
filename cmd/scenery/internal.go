package main

import (
	"fmt"
	"os"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/nativebuilddriver"
)

func internalCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery internal <command> ")
	}
	switch args[0] {
	case "native-build-toolexec":
		if len(args) < 4 || args[1] != "--root" || args[2] == "" {
			return fmt.Errorf("usage: scenery internal native-build-toolexec --root <path> <go-tool> [args...]")
		}
		return nativebuilddriver.RunToolExec(args[2], args[3:], envpolicy.Environ(), os.Stdin, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown internal command %q", args[0])
	}
}
