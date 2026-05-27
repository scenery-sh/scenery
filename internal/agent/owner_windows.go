//go:build windows

package agent

type ownerProcessInfo struct {
	StartedAt string
	Exe       string
	Cmdline   []string
}

func processOwnerInfo(pid int) ownerProcessInfo {
	return ownerProcessInfo{}
}
