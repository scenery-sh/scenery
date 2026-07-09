package main

import (
	"os"
	"testing"

	"scenery.sh/internal/testlimit"
)

// TestMain keeps the testlimit GOMAXPROCS cap (set by the import's init) but
// raises -test.parallel: this package's parallel tests mostly wait on
// subprocesses, so more in-flight tests shorten the run without adding
// scheduler threads.
func TestMain(m *testing.M) {
	testlimit.RaiseTestParallelism(8)
	os.Exit(m.Run())
}
