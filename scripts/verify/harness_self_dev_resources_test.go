package main

import "testing"

func TestHarnessDevResourceSettlingUsesFixedBounds(t *testing.T) {
	before := harnessDevResourceSample{ProcessCount: 2, RuntimeChildren: 1, FileDescriptors: 20, OwnerRSSKiB: 100, AggregateRSSKiB: 200}
	after := before
	after.FileDescriptors += harnessDevMaxFDGrowth
	after.OwnerRSSKiB += harnessDevMaxOwnerRSSGrowthKiB
	after.AggregateRSSKiB += harnessDevMaxTreeRSSGrowthKiB
	after.SceneryCache = harnessTreeUsage{Files: harnessDevMaxCacheGrowthFiles, Bytes: harnessDevMaxCacheGrowthBytes}
	after.GoCache = after.SceneryCache
	if evidence, err := harnessDevResourceSettling(before, after); err != nil || evidence["settled"] != true {
		t.Fatalf("fixed-bound sample = %+v, %v", evidence, err)
	}
	after.ProcessCount++
	if evidence, err := harnessDevResourceSettling(before, after); err == nil || evidence["settled"] != false {
		t.Fatalf("process growth passed: %+v, %v", evidence, err)
	}
}
