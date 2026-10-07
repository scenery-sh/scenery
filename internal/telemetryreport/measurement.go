package telemetryreport

import "sort"

type CacheWork struct {
	Layer                    string  `json:"layer"`
	EditClass                string  `json:"edit_class"`
	Cache                    string  `json:"cache"`
	Reason                   string  `json:"reason"`
	Samples                  int     `json:"samples"`
	AccumulatedMS            float64 `json:"accumulated_ms"`
	FilesHashed              int     `json:"files_hashed"`
	BytesHashed              int64   `json:"bytes_hashed"`
	FilesReused              int     `json:"files_reused"`
	BytesReused              int64   `json:"bytes_reused"`
	FilesWritten             int     `json:"files_written"`
	BytesWritten             int64   `json:"bytes_written"`
	CacheHits                int     `json:"cache_hits"`
	CacheMisses              int     `json:"cache_misses"`
	RebuiltPackages          int     `json:"rebuilt_packages"`
	RebuiltPackagesAvailable bool    `json:"rebuilt_packages_available"`
	MaximumExecutableBytes   int64   `json:"maximum_executable_bytes"`
	Boundary                 string  `json:"boundary"`
}

func recordCacheWork(builds *Builds, step buildStepData) {
	if step.Cache == "" || step.Cache == "not_applicable" {
		return
	}
	class := step.EditClass
	if class == "" {
		class = "unknown"
	}
	key := step.Name + "\x00" + class + "\x00" + step.Cache + "\x00" + step.Reason
	value := builds.cacheWork[key]
	if value == nil {
		value = &CacheWork{Layer: step.Name, EditClass: class, Cache: step.Cache, Reason: step.Reason, RebuiltPackagesAvailable: true, Boundary: "accumulated interval work; not elapsed wait or CPU time"}
		builds.cacheWork[key] = value
	}
	value.Samples++
	value.AccumulatedMS += step.DurationMS
	value.FilesHashed += step.FilesHashed
	value.BytesHashed += step.BytesHashed
	value.FilesReused += step.FilesReused
	value.BytesReused += step.BytesReused
	value.FilesWritten += step.FilesWritten
	value.BytesWritten += step.BytesWritten
	value.CacheHits += step.CacheHits
	value.CacheMisses += step.CacheMisses
	value.RebuiltPackages += len(step.PackagesRebuilt)
	value.RebuiltPackagesAvailable = value.RebuiltPackagesAvailable && step.PackagesRebuiltAvailable
	if step.ExecutableBytes > value.MaximumExecutableBytes {
		value.MaximumExecutableBytes = step.ExecutableBytes
	}
}

func finalizePhaseAndCacheWork(builds *Builds) {
	for id, acc := range builds.phaseSamples {
		timing := acc.timing()
		// Two observations are a useful range, not a reliable p95 claim.
		if timing.PercentileSampleCount < 20 {
			timing.P95MS = nil
		}
		builds.StartupPhases = append(builds.StartupPhases, StepTiming{Step: id, Timing: timing})
	}
	sort.Slice(builds.StartupPhases, func(i, j int) bool { return builds.StartupPhases[i].Step < builds.StartupPhases[j].Step })
	for _, value := range builds.cacheWork {
		builds.CacheWork = append(builds.CacheWork, *value)
	}
	sort.Slice(builds.CacheWork, func(i, j int) bool { return builds.CacheWork[i].AccumulatedMS > builds.CacheWork[j].AccumulatedMS })
	builds.ObservabilityStates = sortedCounts(builds.observabilityStates, 0)
}
