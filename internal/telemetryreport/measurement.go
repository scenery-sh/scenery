package telemetryreport

import (
	"math"
	"sort"
)

type CacheWork struct {
	Layer                    string   `json:"layer"`
	EditClass                string   `json:"edit_class"`
	Cache                    string   `json:"cache"`
	Reason                   string   `json:"reason"`
	Samples                  int      `json:"samples"`
	TimingSampleCount        int      `json:"timing_sample_count"`
	AccumulatedMS            *float64 `json:"accumulated_ms"`
	FilesHashed              int      `json:"files_hashed"`
	BytesHashed              int64    `json:"bytes_hashed"`
	FilesReused              int      `json:"files_reused"`
	BytesReused              int64    `json:"bytes_reused"`
	FilesWritten             int      `json:"files_written"`
	BytesWritten             int64    `json:"bytes_written"`
	CacheHits                int      `json:"cache_hits"`
	CacheMisses              int      `json:"cache_misses"`
	RebuiltPackages          int      `json:"rebuilt_packages"`
	RebuiltPackagesAvailable bool     `json:"rebuilt_packages_available"`
	MaximumExecutableBytes   int64    `json:"maximum_executable_bytes"`
	Boundary                 string   `json:"boundary"`
}

// Cache records retain non-timing work independently. The timing sample count
// identifies the admitted subtotal; nil distinguishes no timing from valid zero.
func recordCacheWork(builds *Builds, step buildStepData, timing supervisorTiming) bool {
	if step.Cache == "" || step.Cache == "not_applicable" {
		return true
	}
	class := step.EditClass
	if class == "" {
		class = "unknown"
	}
	key := step.Name + "\x00" + class + "\x00" + step.Cache + "\x00" + step.Reason
	value := builds.cacheWork[key]
	if value == nil {
		value = &CacheWork{Layer: step.Name, EditClass: class, Cache: step.Cache, Reason: step.Reason, RebuiltPackagesAvailable: true, Boundary: "accumulated valid-timing interval work; compare timing_sample_count with samples for incomplete timing; not elapsed wait or CPU time"}
		builds.cacheWork[key] = value
	}
	valid := !timing.invalid
	if valid {
		sum := timing.ms
		if value.AccumulatedMS != nil {
			sum += *value.AccumulatedMS
		}
		valid = !math.IsInf(sum, 0)
		if valid {
			if value.AccumulatedMS == nil {
				value.AccumulatedMS = new(float64)
			}
			*value.AccumulatedMS = sum
			value.TimingSampleCount++
		}
	}
	value.Samples++
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
	return timing.invalid || valid
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
	sort.Slice(builds.CacheWork, func(i, j int) bool {
		a, b := builds.CacheWork[i].AccumulatedMS, builds.CacheWork[j].AccumulatedMS
		return a != nil && (b == nil || *a > *b)
	})
	builds.ObservabilityStates = sortedCounts(builds.observabilityStates, 0)
}
