package watchignore

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scenery.sh/internal/filemeta"
)

type ruleTimestamp struct{ Sec, Nsec int64 }
type ruleStat struct {
	Dev       int32
	Ino       uint64
	Ctimespec ruleTimestamp
}
type ruleFileInfo struct {
	sys  any
	mode fs.FileMode
}

func (ruleFileInfo) Name() string           { return ".gitignore" }
func (ruleFileInfo) Size() int64            { return 6 }
func (info ruleFileInfo) Mode() fs.FileMode { return info.mode }
func (ruleFileInfo) ModTime() time.Time     { return time.Unix(1, 0) }
func (info ruleFileInfo) IsDir() bool       { return info.mode.IsDir() }
func (info ruleFileInfo) Sys() any          { return info.sys }

func TestWatchIgnoreRuleCacheRequiresCurrentIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	ignoreRuleCache.Store(path, ignoreRuleCacheEntry{
		identity: filemeta.Identity{Device: 42, Inode: 53, ChangeTimeNano: 3_000_000_004},
		size:     6, modTime: time.Unix(1, 0), base: "nested",
	})
	t.Cleanup(func() { ignoreRuleCache.Delete(path) })
	for _, test := range []struct {
		name string
		info fs.FileInfo
		base string
		want bool
	}{
		{"unchanged", ruleFileInfo{ruleStat{42, 53, ruleTimestamp{3, 4}}, 0o644}, "nested", true},
		{"device", ruleFileInfo{ruleStat{43, 53, ruleTimestamp{3, 4}}, 0o644}, "nested", false},
		{"inode", ruleFileInfo{ruleStat{42, 54, ruleTimestamp{3, 4}}, 0o644}, "nested", false},
		{"change-time", ruleFileInfo{ruleStat{42, 53, ruleTimestamp{3, 5}}, 0o644}, "nested", false},
		{"missing-change-time", ruleFileInfo{struct{ Dev, Ino uint64 }{42, 53}, 0o644}, "nested", false},
		{"zero-change-time", ruleFileInfo{ruleStat{42, 53, ruleTimestamp{}}, 0o644}, "nested", false},
		{"symlink", ruleFileInfo{ruleStat{42, 53, ruleTimestamp{3, 4}}, fs.ModeSymlink}, "nested", false},
		{"unavailable", nil, "nested", false},
		{"base", ruleFileInfo{ruleStat{42, 53, ruleTimestamp{3, 4}}, 0o644}, "other", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, got := cachedIgnoreRules(path, test.base, test.info); got != test.want {
				t.Fatalf("cache hit = %t, want %t", got, test.want)
			}
		})
	}
}

func TestWatchIgnoreRuleCacheRejectsChangedRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := os.WriteFile(path, []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	storeIgnoreRules(path, "", before, nil)
	if _, retained := ignoreRuleCache.Load(path); retained {
		t.Fatal("rules read across a file change must not be retained")
	}
}
