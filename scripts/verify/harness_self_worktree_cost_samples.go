package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

type worktreeCostSample struct {
	At                time.Time `json:"at"`
	NativeCPUSeconds  float64   `json:"native_cpu_seconds_total"`
	NativeCPUPercent  float64   `json:"native_cpu_percent_one_core"`
	NativeRSSKiB      int64     `json:"native_rss_kib"`
	DockerCPUPercent  float64   `json:"docker_cpu_percent_one_core"`
	DockerMemoryBytes float64   `json:"docker_stats_memory_bytes"`
	IntervalMS        int64     `json:"native_cpu_interval_ms"`
}

func (p *worktreeRuntimeProbe) measureCostPhase(cohort worktreeCostCohort, load bool) (map[string]any, error) {
	started := time.Now()
	var collectors []func() [2]int
	if load {
		for _, runtime := range cohort.runtimes {
			collectors = append(collectors, p.startSentinel(worktreeProbeAPI(runtime)+"/books"))
		}
	}
	defer func() {
		for _, collect := range collectors {
			collect()
		}
	}()
	previous, err := p.costSample(cohort)
	if err != nil {
		return nil, err
	}
	var samples []worktreeCostSample
	for range 3 {
		select {
		case <-p.ctx.Done():
			return nil, p.ctx.Err()
		case <-time.After(2 * time.Second):
		}
		current, err := p.costSample(cohort)
		if err != nil {
			return nil, err
		}
		seconds := current.At.Sub(previous.At).Seconds()
		if current.NativeCPUSeconds < previous.NativeCPUSeconds || seconds <= 0 {
			return nil, fmt.Errorf("cost process CPU counters changed identity or moved backward")
		}
		current.NativeCPUPercent = 100 * (current.NativeCPUSeconds - previous.NativeCPUSeconds) / seconds
		current.IntervalMS = current.At.Sub(previous.At).Milliseconds()
		samples = append(samples, current)
		previous = current
	}
	requests, failures := 0, 0
	for _, collect := range collectors {
		counts := collect()
		requests, failures = requests+counts[0], failures+counts[1]
	}
	if load && (requests == 0 || failures != 0) {
		return nil, fmt.Errorf("cost load completed %d requests with %d failures", requests, failures)
	}
	for i, root := range cohort.roots {
		record, err := p.record(root)
		if err != nil || !sameWorktreeCluster(cohort.records[i], record) {
			return nil, fmt.Errorf("cost sampling changed worktree SQL ownership: %v", err)
		}
		session, err := p.liveSession(root)
		if err != nil || session.OwnerPID != cohort.runtimes[i].PID {
			return nil, fmt.Errorf("cost sampling replaced a runtime owner: %v", err)
		}
	}
	duration := time.Since(started)
	return map[string]any{"samples": samples, "duration_ms": duration.Milliseconds(), "requests": requests, "failures": failures, "requests_per_second": float64(requests) / duration.Seconds(), "load_schedule": "one GET /books loop per worktree with 20ms delay after each response"}, nil
}

func (p *worktreeRuntimeProbe) costSample(cohort worktreeCostCohort) (worktreeCostSample, error) {
	sample := worktreeCostSample{At: time.Now().UTC()}
	pids := make([]string, len(cohort.pids))
	for i, pid := range cohort.pids {
		pids[i] = strconv.Itoa(pid)
	}
	output, err := p.run(p.repo, "ps", "-o", "pid=,rss=,time=", "-p", strings.Join(pids, ","))
	if err != nil {
		return sample, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	count := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return sample, fmt.Errorf("invalid process resource measurement")
		}
		rss, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return sample, err
		}
		cpu, err := worktreeCPUSeconds(fields[2])
		if err != nil {
			return sample, err
		}
		sample.NativeRSSKiB += rss
		sample.NativeCPUSeconds += cpu
		count++
	}
	if scanner.Err() != nil || count != len(pids) {
		return sample, fmt.Errorf("cost sample lost one of its %d native processes", len(pids))
	}
	args := []string{"--host", cohort.records[0].Postgres.DaemonEndpoint, "stats", "--no-stream", "--format", "{{json .}}"}
	for _, record := range cohort.records {
		args = append(args, record.Postgres.ContainerID)
	}
	output, err = p.run(p.repo, "docker", args...)
	if err != nil {
		return sample, err
	}
	scanner = bufio.NewScanner(bytes.NewReader(output))
	count = 0
	for scanner.Scan() {
		var stats struct{ CPUPerc, MemUsage string }
		if err := json.Unmarshal(scanner.Bytes(), &stats); err != nil {
			return sample, err
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(stats.CPUPerc, "%"), 64)
		if err != nil {
			return sample, err
		}
		used, _, _ := strings.Cut(stats.MemUsage, "/")
		memory, err := worktreeDockerMemoryBytes(strings.TrimSpace(used))
		if err != nil {
			return sample, err
		}
		sample.DockerCPUPercent += cpu
		sample.DockerMemoryBytes += memory
		count++
	}
	if scanner.Err() != nil || count != len(cohort.records) {
		return sample, fmt.Errorf("cost sample lost a PostgreSQL container")
	}
	return sample, nil
}

func worktreeCPUSeconds(value string) (float64, error) {
	days := 0.0
	if day, rest, ok := strings.Cut(value, "-"); ok {
		parsed, err := strconv.ParseFloat(day, 64)
		if err != nil {
			return 0, err
		}
		days, value = parsed, rest
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid process CPU clock")
	}
	seconds := 0.0
	for _, part := range parts {
		parsed, err := strconv.ParseFloat(part, 64)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("invalid process CPU clock component")
		}
		seconds = seconds*60 + parsed
	}
	return days*86400 + seconds, nil
}

func worktreeDockerMemoryBytes(value string) (float64, error) {
	for suffix, scale := range map[string]float64{"GiB": 1 << 30, "MiB": 1 << 20, "KiB": 1 << 10, "GB": 1e9, "MB": 1e6, "kB": 1e3, "B": 1} {
		if !strings.HasSuffix(value, suffix) {
			continue
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, suffix)), 64)
		if err == nil && number >= 0 {
			return number * scale, nil
		}
	}
	return 0, fmt.Errorf("invalid Docker memory measurement")
}

func (p *worktreeRuntimeProbe) costDisk(cohort worktreeCostCohort, cache string) (map[string]any, error) {
	var nativeKiB, retainedKiB, postgresKiB int64
	for i, root := range cohort.roots {
		out, err := p.run(p.repo, "du", "-sk", root)
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(string(out))
		if len(fields) < 2 {
			return nil, fmt.Errorf("checkout disk measurement is missing")
		}
		size, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return nil, err
		}
		nativeKiB += size
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return nil, err
		}
		out, err = p.run(p.repo, "du", "-sk", paths.Directory)
		if err != nil {
			return nil, err
		}
		fields = strings.Fields(string(out))
		if len(fields) < 2 {
			return nil, fmt.Errorf("retained capability disk measurement is missing")
		}
		size, err = strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return nil, err
		}
		retainedKiB += size
		record := cohort.records[i]
		if err := p.verifyRetainedContainer(record); err != nil {
			return nil, err
		}
		out, err = p.run(p.repo, "docker", "--host", record.Postgres.DaemonEndpoint, "exec", record.Postgres.ContainerID, "du", "-sk", "/var/lib/postgresql")
		if err != nil {
			return nil, err
		}
		fields = strings.Fields(string(out))
		if len(fields) < 2 {
			return nil, fmt.Errorf("PostgreSQL disk measurement is missing")
		}
		size, err = strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return nil, err
		}
		postgresKiB += size
	}
	out, err := p.run(p.repo, "du", "-sk", cache)
	if err != nil {
		return nil, err
	}
	return map[string]any{"checkout_allocated_kib": nativeKiB, "retained_capabilities_and_victoria_allocated_kib": retainedKiB, "postgres_allocated_kib": postgresKiB, "cohort_go_cache_du": strings.TrimSpace(string(out)), "shared_toolchain_and_module_cache_excluded": true}, nil
}
