package build

import (
	"slices"
	"strconv"
	"strings"
	"sync"
)

// developmentLinkMemory is the peak resident memory budgeted for one
// development process link. Linking a 32 MB executable with -w peaked at
// 438 MB (2026-11-20); ONLV's service executables are 16 to 27 MB.
const developmentLinkMemory = 512 << 20

var physicalMemory = sync.OnceValue(hostPhysicalMemory)

// developmentLinkParallelism bounds the actions one process build runs at
// once so that its concurrent links fit in a quarter of physical memory. The
// Go command otherwise runs one action per CPU, and a build that relinks
// every service of a large application links that many executables together.
// It returns zero, which keeps the Go command's default, when that default
// already fits, when no more entrypoints are pending than the bound, or when
// physical memory is unknown.
func developmentLinkParallelism(pending, cpus int, memory uint64) int {
	if memory == 0 || cpus < 1 {
		return 0
	}
	bound := max(int(memory/4/developmentLinkMemory), 1)
	if bound >= cpus || pending <= bound {
		return 0
	}
	return bound
}

// withDevelopmentLinkParallelism adds -p parallelism to the arguments of a go
// build, unless parallelism is zero or the configured build flags choose -p.
func withDevelopmentLinkParallelism(args, buildFlags []string, parallelism int) []string {
	if parallelism < 1 || len(args) == 0 || slices.ContainsFunc(buildFlags, func(flag string) bool {
		return flag == "-p" || strings.HasPrefix(flag, "-p=")
	}) {
		return args
	}
	return slices.Insert(slices.Clone(args), 1, "-p", strconv.Itoa(parallelism))
}
