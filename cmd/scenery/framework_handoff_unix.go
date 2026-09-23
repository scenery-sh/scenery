//go:build unix

package main

import "syscall"

// processReplacementSupported gates the framework handoff: without exec a
// stopped runtime could not continue as the new producer.
const processReplacementSupported = true

func execProcess(executable string, argv, environment []string) error {
	return syscall.Exec(executable, argv, environment)
}
