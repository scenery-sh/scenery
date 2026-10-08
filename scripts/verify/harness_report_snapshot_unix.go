//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func harnessReportStop(process *os.Process) error   { return process.Signal(syscall.SIGSTOP) }
func harnessReportResume(process *os.Process) error { return process.Signal(syscall.SIGCONT) }

func harnessReportSelectedDescriptor(path string, info os.FileInfo) (harnessReportDescriptor, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return harnessReportDescriptor{}, errors.New("selected file identity unavailable")
	}
	return harnessReportDescriptor{Path: path, Size: info.Size(), Inode: strconv.FormatUint(uint64(stat.Ino), 10), Device: strconv.FormatUint(uint64(stat.Dev), 10)}, nil
}

// Observe the exact child's opened inode and kernel position, rather than
// inferring capture from elapsed time or from a pathname's current contents.
func observeHarnessReportDescriptors(ctx context.Context, pid int, paths []string) ([]harnessReportDescriptor, error) {
	if runtime.GOOS == "linux" {
		return observeHarnessReportProcDescriptors(pid, paths)
	}
	output, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-o", "-FafonsDi").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// lsof exits 1 if the owned child has already closed its descriptors.
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
		return nil, nil
	}
	var rows []harnessReportDescriptor
	for _, block := range strings.Split(string(output), "\nf")[1:] {
		fields := strings.Split(block, "\n")
		row := harnessReportDescriptor{FD: fields[0]}
		var device uint64
		for _, field := range fields[1:] {
			if len(field) < 2 {
				continue
			}
			value := field[1:]
			switch field[0] {
			case 'a':
				if value == "r" {
					row.Mode = "read"
				} else {
					row.Mode = value
				}
			case 'n':
				row.Path = value
			case 'i':
				row.Inode = value
			case 's':
				row.Size, err = strconv.ParseInt(value, 10, 64)
			case 'D':
				device, err = strconv.ParseUint(value, 0, 64)
			case 'o':
				if strings.HasPrefix(value, "0t") {
					row.Offset, err = strconv.ParseInt(value[2:], 10, 64)
				} else {
					row.Offset, err = strconv.ParseInt(value, 0, 64)
				}
			}
			if err != nil {
				return nil, fmt.Errorf("owned descriptor field %q: %w", field, err)
			}
		}
		for _, path := range paths {
			canonical, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if row.Path == canonical && ok && row.Inode == strconv.FormatUint(uint64(stat.Ino), 10) && device == uint64(stat.Dev) {
				row.Path = path
				row.Device = strconv.FormatUint(device, 10)
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func observeHarnessReportProcDescriptors(pid int, paths []string) ([]harnessReportDescriptor, error) {
	base := filepath.Join("/proc", strconv.Itoa(pid))
	entries, err := os.ReadDir(filepath.Join(base, "fd"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []harnessReportDescriptor
	for _, entry := range entries {
		opened, err := os.Stat(filepath.Join(base, "fd", entry.Name()))
		if err != nil {
			continue
		}
		for _, path := range paths {
			selected, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if !os.SameFile(selected, opened) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(base, "fdinfo", entry.Name()))
			if err != nil {
				continue
			}
			row, err := harnessReportSelectedDescriptor(path, opened)
			if err != nil {
				return nil, err
			}
			row.FD = entry.Name()
			for _, field := range strings.Split(string(data), "\n") {
				if value, ok := strings.CutPrefix(field, "pos:"); ok {
					row.Offset, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
				}
				if value, ok := strings.CutPrefix(field, "ino:"); ok {
					row.Inode = strings.TrimSpace(value)
				}
				if value, ok := strings.CutPrefix(field, "flags:"); ok {
					var flags uint64
					flags, err = strconv.ParseUint(strings.TrimSpace(value), 8, 64)
					if flags&uint64(syscall.O_ACCMODE) == uint64(syscall.O_RDONLY) {
						row.Mode = "read"
					} else {
						row.Mode = "write"
					}
				}
				if err != nil {
					return nil, err
				}
			}
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}
