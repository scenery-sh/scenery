//go:build !linux && !windows

package agent

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
	if len(line) >= 24 {
		info.StartedAt = strings.TrimSpace(line[:24])
		command := strings.TrimSpace(line[24:])
		if command != "" {
			info.Cmdline = strings.Fields(command)
			if len(info.Cmdline) > 0 {
				info.Exe = info.Cmdline[0]
			}
		}
	}
	return info
}
