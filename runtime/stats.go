package runtime

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"onlava.com/internal/runtimeapi"
)

var processStartedAt = time.Now()

type ByteStat = runtimeapi.ByteStat
type CPUStats = runtimeapi.CPUStats
type MemoryStats = runtimeapi.MemoryStats
type GCStats = runtimeapi.GCStats
type DiskStats = runtimeapi.DiskStats
type OSProcessStats = runtimeapi.OSProcessStats
type ProfileLinks = runtimeapi.ProfileLinks
type ProcessStats = runtimeapi.ProcessStats
type GoStats = runtimeapi.GoStats
type PlatformStatsResponse = runtimeapi.PlatformStatsResponse

type cpuSample struct {
	at           time.Time
	totalSeconds float64
}

var (
	cpuSampleMu   sync.Mutex
	lastCPUSample cpuSample
)

func collectPlatformStats() PlatformStatsResponse {
	now := time.Now()
	meta := Meta()
	process := collectProcessStats()
	mem := collectGoMemStats()
	cpu, osStats := collectCPUAndOSStats(now)
	disk := collectDiskStats(process.WorkingDir)

	return PlatformStatsResponse{
		Timestamp:           now,
		AppID:               meta.AppID,
		APIBaseURL:          meta.APIBaseURL,
		RegisteredEndpoints: len(listEndpoints()),
		RegisteredCronJobs:  len(listCronJobs()),
		Process:             process,
		Go: GoStats{
			Version:    goruntime.Version(),
			Goroutines: goruntime.NumGoroutine(),
			NumCPU:     goruntime.NumCPU(),
			GOMAXPROCS: goruntime.GOMAXPROCS(0),
			NumCgoCall: goruntime.NumCgoCall(),
		},
		CPU:      cpu,
		Memory:   mem.memory,
		GC:       mem.gc,
		Disk:     disk,
		OS:       osStats,
		Profiles: profileLinks(meta.APIBaseURL),
	}
}

type goMemSnapshot struct {
	memory MemoryStats
	gc     GCStats
}

func collectGoMemStats() goMemSnapshot {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)

	var lastPause uint64
	if ms.NumGC > 0 {
		lastPause = ms.PauseNs[(ms.NumGC-1)%uint32(len(ms.PauseNs))]
	}

	pressure := percentFraction(float64(ms.HeapAlloc), float64(ms.NextGC))
	lastGCAt := ""
	if ms.LastGC != 0 {
		lastGCAt = time.Unix(0, int64(ms.LastGC)).UTC().Format(time.RFC3339Nano)
	}

	return goMemSnapshot{
		memory: MemoryStats{
			CurrentHeap:  humanBytes(ms.HeapAlloc),
			TotalAlloc:   humanBytes(ms.TotalAlloc),
			Sys:          humanBytes(ms.Sys),
			HeapSys:      humanBytes(ms.HeapSys),
			HeapInUse:    humanBytes(ms.HeapInuse),
			HeapIdle:     humanBytes(ms.HeapIdle),
			HeapReleased: humanBytes(ms.HeapReleased),
			StackInUse:   humanBytes(ms.StackInuse),
			StackSys:     humanBytes(ms.StackSys),
			HeapObjects:  ms.HeapObjects,
		},
		gc: GCStats{
			NumGC:             ms.NumGC,
			NextGC:            humanBytes(ms.NextGC),
			Pressure:          pressure,
			PressurePercent:   pressure * 100,
			GCCPUFraction:     ms.GCCPUFraction,
			LastGCUnixNano:    ms.LastGC,
			LastGCAt:          lastGCAt,
			PauseTotalNS:      ms.PauseTotalNs,
			LastPauseNS:       lastPause,
			ForcedGCCount:     ms.NumForcedGC,
			CompletedCyclePct: completedCyclePercent(ms),
		},
	}
}

func completedCyclePercent(ms goruntime.MemStats) float64 {
	if ms.NextGC == 0 {
		return 0
	}
	return float64(ms.HeapAlloc) / float64(ms.NextGC) * 100
}

func collectProcessStats() ProcessStats {
	exe, _ := os.Executable()
	if exe != "" {
		exe = filepath.Clean(exe)
	}
	wd, _ := os.Getwd()
	if wd != "" {
		wd = filepath.Clean(wd)
	}
	return ProcessStats{
		PID:           os.Getpid(),
		PPID:          os.Getppid(),
		Executable:    exe,
		WorkingDir:    wd,
		StartedAt:     processStartedAt.UTC(),
		UptimeSeconds: time.Since(processStartedAt).Seconds(),
	}
}

func collectCPUAndOSStats(now time.Time) (CPUStats, OSProcessStats) {
	usage, err := readProcessUsage()
	cpu := CPUStats{
		NumCPU:     goruntime.NumCPU(),
		GOMAXPROCS: goruntime.GOMAXPROCS(0),
		NumCgoCall: goruntime.NumCgoCall(),
	}
	osStats := OSProcessStats{}
	if err != nil {
		osStats.Error = err.Error()
		return cpu, osStats
	}

	totalSeconds := usage.UserSeconds + usage.SystemSeconds
	cpu.UserSeconds = usage.UserSeconds
	cpu.SystemSeconds = usage.SystemSeconds
	cpu.TotalSeconds = totalSeconds
	cpu.ProcessPercent, cpu.SampleWindowSecond = sampleCPUPercent(now, totalSeconds)

	osStats.MaxRSS = humanBytes(usage.MaxRSSBytes)
	osStats.MinorPageFaults = usage.MinorPageFaults
	osStats.MajorPageFaults = usage.MajorPageFaults
	osStats.InputBlocks = usage.InputBlocks
	osStats.OutputBlocks = usage.OutputBlocks
	osStats.VoluntaryContextSwitches = usage.VoluntaryContextSwitches
	osStats.InvoluntaryContextSwitches = usage.InvoluntaryContextSwitches
	return cpu, osStats
}

func sampleCPUPercent(now time.Time, totalSeconds float64) (float64, float64) {
	cpuSampleMu.Lock()
	defer cpuSampleMu.Unlock()
	if now.IsZero() {
		now = time.Now()
	}
	window := 0.0
	percent := 0.0
	if !lastCPUSample.at.IsZero() {
		window = now.Sub(lastCPUSample.at).Seconds()
		if window > 0 {
			percent = ((totalSeconds - lastCPUSample.totalSeconds) / window) * 100
			if percent < 0 {
				percent = 0
			}
		}
	}
	lastCPUSample = cpuSample{at: now, totalSeconds: totalSeconds}
	return percent, window
}

func collectDiskStats(path string) DiskStats {
	if path == "" {
		wd, _ := os.Getwd()
		path = wd
	}
	if path == "" {
		return DiskStats{Error: "working directory unavailable"}
	}
	stats, err := readDiskStats(path)
	if err != nil {
		return DiskStats{
			Path:  path,
			Error: err.Error(),
		}
	}
	return DiskStats{
		Path:             path,
		Total:            humanBytes(stats.TotalBytes),
		Used:             humanBytes(stats.UsedBytes),
		Free:             humanBytes(stats.FreeBytes),
		Available:        humanBytes(stats.AvailableBytes),
		UsedPercent:      percentFraction(float64(stats.UsedBytes), float64(stats.TotalBytes)) * 100,
		FreePercent:      percentFraction(float64(stats.FreeBytes), float64(stats.TotalBytes)) * 100,
		AvailablePercent: percentFraction(float64(stats.AvailableBytes), float64(stats.TotalBytes)) * 100,
	}
}

func percentFraction(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func humanBytes(bytes uint64) ByteStat {
	return ByteStat{
		Bytes: bytes,
		Human: humanizeBytes(bytes),
	}
}

func humanizeBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return trimFloat(float64(bytes)/float64(div), 1) + " " + string("KMGTPE"[exp]) + "iB"
}

func trimFloat(value float64, precision int) string {
	raw := strconv.FormatFloat(value, 'f', precision, 64)
	raw = strings.TrimRight(raw, "0")
	raw = strings.TrimRight(raw, ".")
	if raw == "" {
		return "0"
	}
	return raw
}

func profileLinks(baseURL string) ProfileLinks {
	resolve := func(path string) string {
		if strings.TrimSpace(baseURL) == "" {
			return path
		}
		base, err := url.Parse(baseURL)
		if err != nil {
			return path
		}
		ref, err := url.Parse(path)
		if err != nil {
			return path
		}
		return base.ResolveReference(ref).String()
	}
	return ProfileLinks{
		Index:        resolve("/debug/pprof/"),
		Cmdline:      resolve("/debug/pprof/cmdline"),
		CPU:          resolve("/debug/pprof/profile?seconds=30"),
		Trace:        resolve("/debug/pprof/trace?seconds=5"),
		Symbol:       resolve("/debug/pprof/symbol"),
		Heap:         resolve("/debug/pprof/heap"),
		Goroutine:    resolve("/debug/pprof/goroutine?debug=1"),
		Allocs:       resolve("/debug/pprof/allocs"),
		Block:        resolve("/debug/pprof/block"),
		Mutex:        resolve("/debug/pprof/mutex"),
		ThreadCreate: resolve("/debug/pprof/threadcreate"),
	}
}
