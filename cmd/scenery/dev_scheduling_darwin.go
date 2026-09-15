//go:build darwin

package main

import (
	"fmt"
	"syscall"
)

// prioDarwinProcess selects the process-level Darwin background policy from
// <sys/resource.h>. It is inherited by the compiler, linker and application
// children, and makes a full-application link three to four times slower.
const prioDarwinProcess = 4

// clearInheritedBackgroundPolicy removes a background policy that the
// supervisor inherited or set for itself. A launcher's QoS clamp is not
// visible through this call and cannot be lifted by the clamped process.
func clearInheritedBackgroundPolicy() (string, error) {
	state, err := syscall.Getpriority(prioDarwinProcess, 0)
	if err != nil {
		return "darwin_background_clear_failed", fmt.Errorf("read Darwin background policy: %w", err)
	}
	if state == 0 {
		return "darwin_background_absent", nil
	}
	if err := syscall.Setpriority(prioDarwinProcess, 0, 0); err != nil {
		return "darwin_background_clear_failed", fmt.Errorf("clear Darwin background policy: %w", err)
	}
	if state, err = syscall.Getpriority(prioDarwinProcess, 0); err != nil {
		return "darwin_background_clear_failed", fmt.Errorf("confirm Darwin background policy: %w", err)
	}
	if state != 0 {
		return "darwin_background_clear_failed", fmt.Errorf("background policy remains %d after clearing", state)
	}
	return "darwin_background_cleared", nil
}
