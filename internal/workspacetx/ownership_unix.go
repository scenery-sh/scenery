//go:build aix || darwin || dragonfly || freebsd || illumos || netbsd || openbsd || solaris

package workspacetx

import (
	"os/exec"
	"strconv"
	"strings"
)

func processOwnerInfo(pid int) ownerProcessInfo {
	info := ownerProcessInfo{Liveness: probeProcessLiveness(pid)}
	if info.Liveness == ownerProcessDead {
		return info
	}
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
	info.Liveness = ownerProcessLive
	return info
}
