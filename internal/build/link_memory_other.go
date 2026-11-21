//go:build !darwin && !linux

package build

// hostPhysicalMemory reports unknown physical memory, which leaves the Go
// command's default build parallelism in place.
func hostPhysicalMemory() uint64 { return 0 }
