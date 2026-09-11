//go:build !scenery_native_lifecycle_probe

package main

import (
	"os"
	"time"
)

func main() { os.Exit(executeCLI(os.Args[1:])) }

func executeCLI(args []string) int {
	return executeCLIWith(args, os.Stdout, os.Stderr, time.Now(), runWithCLITelemetry, recordCLITelemetry)
}
