package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
)

// Sample the owned cohort during startup. Temporary compiler processes cannot
// be fingerprinted after exit; select their live ancestry and unique fixture
// paths in memory, and retain only aggregate numbers, never unrelated argv.
func (p *worktreeRuntimeProbe) startCostPeakSampler(roots []string) func() map[string]any {
	result := map[string]any{
		"interval_ms": 1000,
		"boundary":    "sampled native process trees identified by owned cohort paths; processes shorter than the interval and Docker startup memory are not measured",
	}
	scopes := append([]string(nil), roots...)
	for _, root := range roots {
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			result["error"] = err.Error()
			return func() map[string]any { return result }
		}
		scopes = append(scopes, paths.AppRoot, paths.Directory, paths.SocketDir)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	started := time.Now()
	go func() {
		defer close(done)
		samples, maxProcesses := 0, 0
		var maximumRSS int64
		sample := func() {
			output, err := exec.CommandContext(p.ctx, "ps", "-ww", "-axo", "pid=,ppid=,rss=,command=").Output()
			if err != nil {
				result["error"] = fmt.Sprintf("read native process sample: %v", err)
				return
			}
			rss, processes, err := worktreeCostPeakSample(output, scopes)
			if err != nil {
				result["error"] = err.Error()
				return
			}
			samples++
			maximumRSS = max(maximumRSS, rss)
			maxProcesses = max(maxProcesses, processes)
		}
		sample()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				sample()
				result["samples"], result["elapsed_ms"] = samples, time.Since(started).Milliseconds()
				result["maximum_native_rss_kib"], result["maximum_native_processes"] = maximumRSS, maxProcesses
				return
			case <-p.ctx.Done():
				result["error"] = p.ctx.Err().Error()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	var once sync.Once
	return func() map[string]any {
		once.Do(func() { close(stop) })
		<-done
		return result
	}
}

func worktreeCostPeakSample(output []byte, scopes []string) (int64, int, error) {
	type process struct {
		parent int
		rss    int64
	}
	processes, owned := map[int]process{}, map[int]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			return 0, 0, fmt.Errorf("incomplete native process sample")
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		rss, rssErr := strconv.ParseInt(fields[2], 10, 64)
		if pidErr != nil || parentErr != nil || rssErr != nil || pid <= 0 || rss < 0 {
			return 0, 0, fmt.Errorf("invalid native process sample")
		}
		processes[pid] = process{parent: parent, rss: rss}
		command := strings.Join(fields[3:], " ")
		for _, scope := range scopes {
			if scope != "" && strings.Contains(command, scope) {
				owned[pid] = true
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	for changed := true; changed; {
		changed = false
		for pid, process := range processes {
			if !owned[pid] && owned[process.parent] {
				owned[pid], changed = true, true
			}
		}
	}
	var rss int64
	for pid := range owned {
		rss += processes[pid].rss
	}
	return rss, len(owned), nil
}
