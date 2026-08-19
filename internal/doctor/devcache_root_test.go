package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/devcache"
)

func TestRootPrefersOverrideThenEnvThenUserCache(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", "")
	if err := os.Unsetenv("SCENERY_DEV_CACHE_DIR"); err != nil {
		t.Fatal(err)
	}

	override := filepath.Join(t.TempDir(), "override")
	restore := devcache.SetRoot(override)
	defer restore()
	root, err := devcache.Root()
	if err != nil {
		t.Fatal(err)
	}
	if root != override {
		t.Fatalf("Root() = %q, want override %q", root, override)
	}

	restore()
	t.Setenv("SCENERY_DEV_CACHE_DIR", filepath.Join(t.TempDir(), "env"))
	root, err = devcache.Root()
	if err != nil {
		t.Fatal(err)
	}
	if root != os.Getenv("SCENERY_DEV_CACHE_DIR") {
		t.Fatalf("Root() = %q, want env cache", root)
	}

	t.Setenv("SCENERY_DEV_CACHE_DIR", "")
	if err := os.Unsetenv("SCENERY_DEV_CACHE_DIR"); err != nil {
		t.Fatal(err)
	}
	root, err = devcache.Root()
	if err != nil {
		t.Fatal(err)
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(userCache, "scenery") {
		t.Fatalf("Root() = %q, want user cache scenery dir", root)
	}
}
