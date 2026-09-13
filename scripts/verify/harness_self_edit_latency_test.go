package main

import "testing"

func TestHarnessEditLatencyStatsUsesNearestRank(t *testing.T) {
	t.Parallel()
	samples := make([]harnessEditLatencySample, 30)
	for i := range samples {
		samples[i].EditToResponseMS = float64(30 - i)
	}
	stats := harnessEditLatencyStats(samples)
	if stats["count"] != 30 || stats["p50_ms"] != 15.0 || stats["p95_ms"] != 29.0 || stats["worst_ms"] != 30.0 || stats["best_ms"] != 1.0 {
		t.Fatalf("unexpected nearest-rank statistics: %+v", stats)
	}
}
