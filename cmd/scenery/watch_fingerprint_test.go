package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
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

func TestSnapshotFingerprintTracksAbsentCompilerInputAppearance(t *testing.T) {
	t.Parallel()
	before := fileSnapshot{
		compilerAbsent: map[string]bool{"ui/card.tsx": false},
		compilerImpl:   map[string]bool{"ui/card.tsx": false},
		compilerValid:  true,
	}
	after := fileSnapshot{
		compilerFiles: map[string]fileStamp{"ui/card.tsx": {hash: "present", size: 7, mode: 0o644}},
		compilerImpl:  map[string]bool{"ui/card.tsx": false},
		compilerValid: true,
	}
	if snapshotsEqual(before, after) {
		t.Fatal("newly appearing compiler input did not invalidate the runtime snapshot")
	}
	if got := changedPaths(before, after); !slices.Equal(got, []string{"ui/card.tsx"}) {
		t.Fatalf("changed paths = %v", got)
	}
	if snapshotFingerprint(before) == snapshotFingerprint(after) {
		t.Fatal("absent and present compiler inputs produced the same fingerprint")
	}
}

func TestDeclaredCompilerTestFileStillAffectsRuntime(t *testing.T) {
	t.Parallel()
	before := fileSnapshot{
		compilerAbsent: map[string]bool{"ui/card_test.go": false},
		compilerValid:  true,
	}
	after := fileSnapshot{
		compilerFiles: map[string]fileStamp{"ui/card_test.go": {hash: "present", size: 7, mode: 0o644}},
		compilerImpl:  map[string]bool{"ui/card_test.go": false},
		compilerValid: true,
	}
	if snapshotsEqual(before, after) || snapshotFingerprint(before) == snapshotFingerprint(after) {
		t.Fatal("declared graph input was ignored merely because its name ended in _test.go")
	}
}

func TestBuildInputSnapshotsIgnoreOnlyGeneratedPublication(t *testing.T) {
	t.Parallel()
	before := fileSnapshot{files: map[string]fileStamp{"service/api.go": {hash: "one", size: 3}}, generated: map[string]bool{"service/generated.go": false}}
	after := fileSnapshot{files: map[string]fileStamp{"service/api.go": {hash: "one", size: 3}}, generated: map[string]bool{"service/generated.go": true}}
	if !buildInputSnapshotsEqual(before, after) {
		t.Fatal("generated publication invalidated an otherwise exact captured input set")
	}
	after.files["service/api.go"] = fileStamp{hash: "two", size: 3}
	if buildInputSnapshotsEqual(before, after) {
		t.Fatal("changed source bytes were accepted as the captured input set")
	}
}
