//go:build linux

package agent

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
	if exe, err := os.Readlink(filepath.Join(base, "exe")); err == nil {
		info.Exe = exe
	}
	if data, err := os.ReadFile(filepath.Join(base, "cmdline")); err == nil && len(data) > 0 {
		parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		for _, part := range parts {
			if part != "" {
				info.Cmdline = append(info.Cmdline, part)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(base, "stat")); err == nil {
		if startTicks, err := linuxStartTicks(string(data)); err == nil {
			info.StartedAt = startTicks
		}
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
