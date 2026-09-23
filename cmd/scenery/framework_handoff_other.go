//go:build !unix

package main

import "fmt"

// processReplacementSupported gates the framework handoff: without exec a
// stopped runtime could not continue as the new producer.
const processReplacementSupported = false

func execProcess(string, []string, []string) error {
	return fmt.Errorf("replacing the running process is unavailable on this platform")
}
