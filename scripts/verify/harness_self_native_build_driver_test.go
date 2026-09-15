package main

import "testing"

func TestNativeBuildCompilerSummaryIncludesRetainedStockControl(t *testing.T) {
	samples := []nativeBuildDriverSample{
		{Backend: "stock", AccountableBuildMS: 30, FirstVerifiedResponseMS: 40, AcceptedEditMS: 45, OK: true},
		{Backend: "retained_stock", AccountableBuildMS: 25, FirstVerifiedResponseMS: 35, AcceptedEditMS: 40, OK: true},
		{Backend: "compiler", AccountableBuildMS: 20, FirstVerifiedResponseMS: 30, AcceptedEditMS: 35, OK: true},
	}
	summary := nativeBuildCohortSummary(samples)
	if summary["backend_count"] != 3 || summary["complete_pairs"] != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	matrix := nativeBuildExecutionMatrix(summary, nativeBuildCompilerSpec)
	for _, index := range []int{0, 2, 3} {
		if matrix[index]["status"] != "performed" {
			t.Fatalf("matrix[%d]=%+v", index, matrix[index])
		}
	}
}
