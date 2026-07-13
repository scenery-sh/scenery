//go:build windows

package workspacetx

type ownerProcessInfo struct {
	StartedAt string
	Exe       string
	Cmdline   []string
}

func processOwnerInfo(int) ownerProcessInfo { return ownerProcessInfo{} }
