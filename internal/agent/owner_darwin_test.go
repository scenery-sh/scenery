//go:build darwin

package agent

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestParseDarwinProcArgsPreservesLegacyFingerprintShape(t *testing.T) {
	raw := make([]byte, 4)
	binary.NativeEndian.PutUint32(raw, 3)
	raw = append(raw, []byte("/usr/local/bin/scenery")...)
	raw = append(raw, 0, 0, 0)
	raw = append(raw, []byte("/usr/local/bin/scenery")...)
	raw = append(raw, 0)
	raw = append(raw, []byte("--app-root")...)
	raw = append(raw, 0)
	raw = append(raw, []byte("/tmp/app root")...)
	raw = append(raw, 0)
	raw = append(raw, []byte("ENV=value")...)
	raw = append(raw, 0)

	exe, args := parseDarwinProcArgs(raw)
	if exe != "/usr/local/bin/scenery" {
		t.Fatalf("executable = %q", exe)
	}
	wantArgs := []string{"/usr/local/bin/scenery", "--app-root", "/tmp/app", "root"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", args, wantArgs)
	}
}
