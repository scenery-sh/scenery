// Package testlimit caps scheduler parallelism for test binaries.
//
// The test suite is dominated by kernel time: many test binaries run
// concurrently and each spawns subprocesses (go builds, scenery binaries,
// agents). With every process running GOMAXPROCS=NumCPU threads, the kernel
// spends most of the wall clock context-switching instead of doing work —
// empirically `go test ./...` runs ~50% faster on a 24-core machine with the
// cap below than with the defaults.
//
// Import it for side effect from a single _test.go file per package:
//
//	import _ "scenery.sh/internal/testlimit"
//
// The init runs before testing.Init, so the -test.parallel default follows the
// cap as well. Setting GOMAXPROCS explicitly in the environment disables it.
package testlimit

import (
	"flag"
	"runtime"
	"strconv"

	"scenery.sh/internal/envpolicy"
)

const maxTestProcs = 4

func init() {
	if envpolicy.Get("GOMAXPROCS") != "" {
		return
	}
	n := min(runtime.GOMAXPROCS(0), maxTestProcs)
	runtime.GOMAXPROCS(n)
	// Spawned Go subprocesses (go build, scenery binaries, agents) inherit
	// the cap through the environment.
	_ = envpolicy.Set("GOMAXPROCS", strconv.Itoa(n))
}

// RaiseTestParallelism lifts the -test.parallel default (which follows the
// GOMAXPROCS cap above) back up for packages whose parallel tests mostly wait
// on subprocesses rather than running Go code. Call it from TestMain before
// m.Run. An explicit -test.parallel flag on the command line wins.
func RaiseTestParallelism(n int) {
	f := flag.Lookup("test.parallel")
	if f == nil || f.Value.String() != f.DefValue {
		return
	}
	_ = f.Value.Set(strconv.Itoa(n))
}
