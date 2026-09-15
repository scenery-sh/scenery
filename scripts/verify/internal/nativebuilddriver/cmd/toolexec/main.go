package main

import (
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/nativebuilddriver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("missing wrapped Go tool")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(executable)), "bootstrap")
	return nativebuilddriver.RunToolExec(root, os.Args[1:], envpolicy.Environ(), os.Stdin, os.Stdout, os.Stderr)
}
