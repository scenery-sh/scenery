package compiler

import (
	"testing"
)

const (
	vnextIntegrationParallelism = 3
	vnextGoCommandMaxProcs      = 2
)

var vnextIntegrationSlots = make(chan struct{}, vnextIntegrationParallelism)

func parallelVNextIntegrationTest(t *testing.T) {
	t.Helper()
	t.Parallel()
	vnextIntegrationSlots <- struct{}{}
	t.Cleanup(func() { <-vnextIntegrationSlots })
}
