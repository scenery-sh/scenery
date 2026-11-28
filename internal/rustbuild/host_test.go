package rustbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPruneOldHostDigestsKeepsActiveTargetAndNewest(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	targetDir := filepath.Join(cache, "target")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	digests := []string{
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
		strings.Repeat("d", 64),
		strings.Repeat("e", 64),
	}
	base := time.Now().Add(-5 * time.Hour)
	for i, d := range digests {
		dir := filepath.Join(cache, d)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(dir, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	pruneOldHostDigests(cache, digests[4], 3)

	for _, kept := range []string{"target", digests[4], digests[3], digests[2]} {
		if _, err := os.Stat(filepath.Join(cache, kept)); err != nil {
			t.Errorf("expected %s to be kept: %v", kept, err)
		}
	}
	for _, removed := range []string{digests[0], digests[1]} {
		if _, err := os.Stat(filepath.Join(cache, removed)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be pruned, stat err=%v", removed, err)
		}
	}
}
