package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pbrazdil/onlava/internal/neonselfhost"
)

const neonSelfhostDriverTool = "neon-selfhost-driver"

func ensureBuiltinNeonSelfhostDriver(_ context.Context) neonCellDriver {
	return inspectBuiltinNeonSelfhostDriver()
}

func inspectBuiltinNeonSelfhostDriver() neonCellDriver {
	driver := neonCellDriver{
		Kind:    "builtin",
		Tool:    neonSelfhostDriverTool,
		Version: neonselfhost.DriverVersion,
		Status:  "installed",
		Message: "Neon self-host driver is built into the onlava CLI as `onlava internal neon-selfhost-driver`.",
	}
	if exe, err := os.Executable(); err == nil {
		driver.Path = exe
	} else {
		driver.Message = fmt.Sprintf("%s Current executable path was unavailable: %v", driver.Message, err)
	}
	return driver
}

func configuredManagedNeonSelfhostBranchDriver() (neonBranchDriver, bool, error) {
	root, err := neonSubstrateRoot()
	if err != nil {
		return nil, false, err
	}
	state, ok, err := readNeonCellState(root)
	if err != nil {
		return nil, false, err
	}
	if !ok || state.Driver == nil {
		return nil, false, nil
	}
	if state.Driver.Kind == "builtin" {
		return newBuiltinNeonSelfhostBranchDriver(), true, nil
	}
	if strings.TrimSpace(state.Driver.Path) != "" {
		driver, err := executableNeonBranchDriverFromPath(state.Driver.Path, "neon-selfhost driver", "cell.json driver.path", neonSelfhostBranchDriverEndpointSource)
		return driver, err == nil, err
	}
	return nil, false, nil
}
