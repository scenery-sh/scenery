// Package devcache is the stdlib leaf for the scenery development cache root.
// Production still honors SCENERY_DEV_CACHE_DIR; tests inject a root with SetRoot.
package devcache

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"scenery.sh/internal/envpolicy"
)

var (
	rootMu       sync.RWMutex
	rootOverride string
)

// SetRoot injects an in-process cache root. Production leaves it empty so
// Root reads SCENERY_DEV_CACHE_DIR, then the user cache directory.
func SetRoot(root string) (restore func()) {
	rootMu.Lock()
	prev := rootOverride
	rootOverride = strings.TrimSpace(root)
	rootMu.Unlock()
	return func() {
		rootMu.Lock()
		rootOverride = prev
		rootMu.Unlock()
	}
}

// EnvOrOverride returns the injected root or SCENERY_DEV_CACHE_DIR.
// Empty means callers should apply their own fallback (agent dashboard, user cache).
func EnvOrOverride() string {
	rootMu.RLock()
	override := rootOverride
	rootMu.RUnlock()
	if strings.TrimSpace(override) != "" {
		return override
	}
	return strings.TrimSpace(envpolicy.Get("SCENERY_DEV_CACHE_DIR"))
}

// Root is the scenery development cache directory.
func Root() (string, error) {
	if root := EnvOrOverride(); root != "" {
		return root, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scenery"), nil
}
