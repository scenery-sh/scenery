//go:build darwin

package agent

import (
	"bytes"
	"encoding/binary"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type ownerProcessInfo struct {
	StartedAt string
	Exe       string
	Cmdline   []string
}

func processOwnerInfo(pid int) ownerProcessInfo {
	var info ownerProcessInfo
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || process.Proc.P_pid != int32(pid) {
		return info
	}
	if started := process.Proc.P_starttime; started.Sec > 0 {
		info.StartedAt = time.Unix(started.Sec, int64(started.Usec)*1000).Local().Format("Mon Jan _2 15:04:05 2006")
	}
	info.Exe = darwinProcessName(process.Proc.P_comm[:])
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
		exe, cmdline := parseDarwinProcArgs(raw)
		if exe != "" {
			info.Exe = exe
		}
		info.Cmdline = cmdline
	}
	return info
}

func darwinProcessName(raw []byte) string {
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		raw = raw[:end]
	}
	return strings.TrimSpace(string(raw))
}

func parseDarwinProcArgs(raw []byte) (string, []string) {
	if len(raw) < 4 {
		return "", nil
	}
	argc := int(int32(binary.NativeEndian.Uint32(raw[:4])))
	if argc <= 0 || argc > len(raw) {
		return "", nil
	}
	raw = raw[4:]
	exeEnd := bytes.IndexByte(raw, 0)
	if exeEnd < 0 {
		return "", nil
	}
	exe := strings.TrimSpace(string(raw[:exeEnd]))
	raw = raw[exeEnd+1:]
	for len(raw) > 0 && raw[0] == 0 {
		raw = raw[1:]
	}
	args := make([]string, 0, argc)
	for len(raw) > 0 && len(args) < argc {
		argEnd := bytes.IndexByte(raw, 0)
		if argEnd < 0 {
			args = append(args, string(raw))
			break
		}
		args = append(args, string(raw[:argEnd]))
		raw = raw[argEnd+1:]
	}
	// Preserve the historical macOS fingerprint produced by strings.Fields
	// over `ps -o command=` while avoiding a subprocess on every verification.
	return exe, strings.Fields(strings.Join(args, " "))
}
