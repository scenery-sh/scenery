//go:build !linux && !windows

package workspacetx

import (
	"os/exec"
	"strconv"
	"strings"
)

type ownerProcessInfo struct {
	StartedAt string
	Exe       string
	Cmdline   []string
}

func processOwnerInfo(pid int) ownerProcessInfo {
	var info ownerProcessInfo
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "command=").Output()
	if err != nil {
		return info
	}
	line := strings.TrimSpace(string(out))
	if len(line) < 24 {
		return info
	}
	info.StartedAt = strings.TrimSpace(line[:24])
	info.Cmdline = strings.Fields(strings.TrimSpace(line[24:]))
	if len(info.Cmdline) > 0 {
		info.Exe = info.Cmdline[0]
	}
	return info
}
