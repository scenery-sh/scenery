package vnext

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
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

func boundedGoCommand(arguments ...string) *exec.Cmd {
	command := exec.Command("go", arguments...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOMAXPROCS=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOMAXPROCS="+strconv.Itoa(min(runtime.GOMAXPROCS(0), vnextGoCommandMaxProcs)))
	return command
}
