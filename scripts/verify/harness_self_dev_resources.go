package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	harnessDevMaxFDGrowth          = 32
	harnessDevMaxOwnerRSSGrowthKiB = 64 * 1024
	harnessDevMaxTreeRSSGrowthKiB  = 96 * 1024
	harnessDevMaxCacheGrowthBytes  = 512 << 20
	harnessDevMaxCacheGrowthFiles  = 5_000
)

type harnessTreeUsage struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

type harnessDevResourceSample struct {
	ProcessCount    int              `json:"process_count"`
	RuntimeChildren int              `json:"runtime_children"`
	FileDescriptors int              `json:"file_descriptors"`
	OwnerRSSKiB     int64            `json:"owner_rss_kib"`
	AggregateRSSKiB int64            `json:"aggregate_rss_kib"`
	PIDs            []int            `json:"pids"`
	SceneryCache    harnessTreeUsage `json:"scenery_cache"`
	GoCache         harnessTreeUsage `json:"go_cache"`
}

func captureHarnessDevResources(ctx context.Context, ownerPID int, sceneryCache, goCache string) (harnessDevResourceSample, error) {
	var sample harnessDevResourceSample
	command := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,rss=")
	output, err := command.Output()
	if err != nil {
		return sample, err
	}
	type process struct {
		pid, parent int
		rss         int64
	}
	processes := map[int]process{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		rss, rssErr := strconv.ParseInt(fields[2], 10, 64)
		if pidErr == nil && parentErr == nil && rssErr == nil {
			processes[pid] = process{pid: pid, parent: parent, rss: rss}
		}
	}
	if err := scanner.Err(); err != nil {
		return sample, err
	}
	if _, ok := processes[ownerPID]; !ok {
		return sample, fmt.Errorf("development resource owner %d is not running", ownerPID)
	}
	owned := map[int]bool{ownerPID: true}
	for changed := true; changed; {
		changed = false
		for pid, process := range processes {
			if !owned[pid] && owned[process.parent] {
				owned[pid], changed = true, true
			}
		}
	}
	for pid := range owned {
		sample.PIDs = append(sample.PIDs, pid)
		sample.AggregateRSSKiB += processes[pid].rss
		if pid == ownerPID {
			sample.OwnerRSSKiB = processes[pid].rss
		}
		fds, err := harnessProcessFDs(ctx, pid)
		if err != nil {
			return sample, err
		}
		sample.FileDescriptors += fds
	}
	sort.Ints(sample.PIDs)
	sample.ProcessCount = len(sample.PIDs)
	sample.RuntimeChildren = sample.ProcessCount - 1
	if sample.SceneryCache, err = harnessTreeUsageAt(sceneryCache); err != nil {
		return sample, err
	}
	if sample.GoCache, err = harnessTreeUsageAt(goCache); err != nil {
		return sample, err
	}
	return sample, nil
}

func harnessProcessFDs(ctx context.Context, pid int) (int, error) {
	proc := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	if entries, err := os.ReadDir(proc); err == nil {
		return len(entries), nil
	}
	if runtime.GOOS != "darwin" {
		return 0, fmt.Errorf("file descriptor accounting is unavailable for process %d", pid)
	}
	command := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-Fn")
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("account file descriptors for process %d: %w", pid, err)
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "f") {
			count++
		}
	}
	return count, nil
}

func harnessTreeUsageAt(root string) (harnessTreeUsage, error) {
	var usage harnessTreeUsage
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			usage.Files++
			usage.Bytes += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return usage, nil
	}
	return usage, err
}

func harnessDevResourceSettling(before, after harnessDevResourceSample) (map[string]any, error) {
	delta := map[string]int64{
		"process_count":       int64(after.ProcessCount - before.ProcessCount),
		"runtime_children":    int64(after.RuntimeChildren - before.RuntimeChildren),
		"file_descriptors":    int64(after.FileDescriptors - before.FileDescriptors),
		"owner_rss_kib":       after.OwnerRSSKiB - before.OwnerRSSKiB,
		"aggregate_rss_kib":   after.AggregateRSSKiB - before.AggregateRSSKiB,
		"scenery_cache_files": int64(after.SceneryCache.Files - before.SceneryCache.Files),
		"scenery_cache_bytes": after.SceneryCache.Bytes - before.SceneryCache.Bytes,
		"go_cache_files":      int64(after.GoCache.Files - before.GoCache.Files),
		"go_cache_bytes":      after.GoCache.Bytes - before.GoCache.Bytes,
	}
	limits := map[string]int64{
		"process_growth":       0,
		"runtime_child_growth": 0,
		"fd_growth":            harnessDevMaxFDGrowth,
		"owner_rss_growth_kib": harnessDevMaxOwnerRSSGrowthKiB,
		"tree_rss_growth_kib":  harnessDevMaxTreeRSSGrowthKiB,
		"cache_growth_files":   harnessDevMaxCacheGrowthFiles,
		"cache_growth_bytes":   harnessDevMaxCacheGrowthBytes,
	}
	violations := []string{}
	for name, check := range map[string]bool{
		"process count":       delta["process_count"] <= limits["process_growth"],
		"runtime children":    delta["runtime_children"] <= limits["runtime_child_growth"],
		"file descriptors":    delta["file_descriptors"] <= limits["fd_growth"],
		"owner RSS":           delta["owner_rss_kib"] <= limits["owner_rss_growth_kib"],
		"aggregate RSS":       delta["aggregate_rss_kib"] <= limits["tree_rss_growth_kib"],
		"Scenery cache files": delta["scenery_cache_files"] <= limits["cache_growth_files"],
		"Scenery cache bytes": delta["scenery_cache_bytes"] <= limits["cache_growth_bytes"],
		"Go cache files":      delta["go_cache_files"] <= limits["cache_growth_files"],
		"Go cache bytes":      delta["go_cache_bytes"] <= limits["cache_growth_bytes"],
	} {
		if !check {
			violations = append(violations, name)
		}
	}
	sort.Strings(violations)
	evidence := map[string]any{"before": before, "after": after, "delta": delta, "limits": limits, "settled": len(violations) == 0}
	if len(violations) != 0 {
		return evidence, fmt.Errorf("development churn exceeded fixed resource bounds: %s", strings.Join(violations, ", "))
	}
	return evidence, nil
}
