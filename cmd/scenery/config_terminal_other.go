//go:build !darwin && !linux

package main

import (
	"io"
	"os"
)

func readHiddenLine(*os.File, io.Writer, string) ([]byte, error) {
	return nil, unavailableErrorf("hidden prompts are unavailable on this platform; pass the value with --stdin")
}
