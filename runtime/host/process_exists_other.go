//go:build windows

package host

func processExists(pid int) bool {
	return pid > 0
}
