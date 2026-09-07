//go:build !unix && !windows

package workspacetx

import (
	"fmt"
	"os"
)

func tryRecoveryLock(*os.File) (bool, error) {
	return false, fmt.Errorf("failed_precondition: workspace transaction recovery locking is unsupported on this platform")
}

func releaseRecoveryLock(*os.File) {}
