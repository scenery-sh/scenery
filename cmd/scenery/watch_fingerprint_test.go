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

func TestSnapshotFingerprintNamespaceOrderAndMembership(t *testing.T) {
	t.Parallel()
	snapshot := fileSnapshot{
		files: map[string]fileStamp{
			"a.go":         {hash: "source", size: 6, mode: 0o644},
			"used_test.go": {hash: "embedded", size: 8, mode: 0o600, embed: true},
		},
		compilerFiles: map[string]fileStamp{
			"a.go":         {hash: "compiler", size: 8, mode: 0o755},
			"used_test.go": {hash: "embedded", size: 8, mode: 0o600, embed: true},
			"skip_test.go": {hash: "unused", size: 6},
		},
		compilerImpl: map[string]bool{"skip_test.go": true, "used_test.go": true},
		compilerAbsent: map[string]bool{
			"z.json":           false,
			"declared_test.go": false,
			"unused_test.go":   true,
		},
	}
	// Namespace ordering is absent, compiler, source. A path present in two
	// namespaces contributes twice; test-only implementation files contribute neither.
	wire := "declared_test.go\x00absent\x00z.json\x00absent\x00" +
		"a.go\x00compiler\x008:755:false\x00used_test.go\x00embedded\x008:600:true\x00" +
		"a.go\x00source\x006:644:false\x00used_test.go\x00embedded\x008:600:true\x00"
	sum := sha256.Sum256([]byte(wire))
	if got, want := snapshotFingerprint(snapshot), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("mixed fingerprint = %s, want %s", got, want)
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
