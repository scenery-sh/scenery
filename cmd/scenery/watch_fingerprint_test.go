package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// Content identity is path\0hash\0size:mode:embed\0 in sorted path order.
// File mtimes only control hash reuse and never invalidate a runtime build.
func TestSnapshotFingerprintLayout(t *testing.T) {
	t.Parallel()

	snapshot := fileSnapshot{files: map[string]fileStamp{
		"b/file.go": {
			modTime: time.Date(2026, 7, 22, 12, 0, 1, 500, time.UTC),
			size:    2048,
			mode:    0o755,
			hash:    "deadbeef",
			embed:   true,
		},
		"a/file.go": {
			modTime: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
			size:    1024,
			mode:    0o644,
			hash:    "cafef00d",
		},
	}}

	h := sha256.New()
	for _, path := range []string{"a/file.go", "b/file.go"} {
		stamp := snapshot.files[path]
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write([]byte(stamp.hash))
		h.Write([]byte{0})
		_, _ = fmt.Fprintf(h, "%d:%o:%t", stamp.size, stamp.mode, stamp.embed)
		h.Write([]byte{0})
	}
	want := hex.EncodeToString(h.Sum(nil))

	if got := snapshotFingerprint(snapshot); got != want {
		t.Fatalf("snapshotFingerprint layout changed: got %s, want %s", got, want)
	}
}
