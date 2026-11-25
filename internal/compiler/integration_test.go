package compiler

import (
	"testing"
)

const (
	compilerIntegrationParallelism = 3
	compilerGoCommandMaxProcs      = 2
)

var compilerIntegrationSlots = make(chan struct{}, compilerIntegrationParallelism)

func parallelCompilerIntegrationTest(t *testing.T) {
	t.Helper()
	t.Parallel()
	compilerIntegrationSlots <- struct{}{}
	t.Cleanup(func() { <-compilerIntegrationSlots })
}
