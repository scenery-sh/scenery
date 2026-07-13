//go:build linux

package workspacetx

import (
	"errors"
	"os"
	"path/filepath"
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
	base := filepath.Join("/proc", strconv.Itoa(pid))
	info.Exe, _ = os.Readlink(filepath.Join(base, "exe"))
	if data, err := os.ReadFile(filepath.Join(base, "cmdline")); err == nil {
		for _, part := range strings.Split(strings.TrimRight(string(data), "\x00"), "\x00") {
			if part != "" {
				info.Cmdline = append(info.Cmdline, part)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(base, "stat")); err == nil {
		info.StartedAt, _ = linuxStartTicks(string(data))
	}
	return info
}

func linuxStartTicks(stat string) (string, error) {
	end := strings.LastIndex(stat, ")")
	if end < 0 || end+2 >= len(stat) {
		return "", errors.New("invalid proc stat")
	}
	fields := strings.Fields(stat[end+2:])
	if len(fields) < 20 {
		return "", errors.New("short proc stat")
	}
	return fields[19], nil
}
