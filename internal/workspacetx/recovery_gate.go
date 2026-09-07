package workspacetx

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"scenery.sh/internal/scn"
)

// Serialize recovery itself: two readers must not both recover an abandoned
// owner and let the later reader remove a newly acquired publication lock.
// This kernel lock has no journal/owner format and is released on process exit.
func lockRecovery(root string) (func(), error) {
	path := filepath.Join(root, "recovery.lock")
	if err := scn.RejectPathSymlinks(root, path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		locked, err := tryRecoveryLock(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if locked {
			return func() { releaseRecoveryLock(file); _ = file.Close() }, nil
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("failed_precondition: timed out waiting for workspace transaction recovery; retry after the active recovery completes")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
