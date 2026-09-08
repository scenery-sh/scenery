package devprocess

import (
	"errors"
	"os/exec"
	"regexp"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	// ReplaceAll always builds a fresh buffer and never aliases data, so the
	// result is already safe to retain; copying it again doubled the allocation
	// for every process output line.
	return ansiEscapeRE.ReplaceAll(data, nil)
}

func IsExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	_, ok := errors.AsType[*exec.ExitError](err)
	return ok
}
