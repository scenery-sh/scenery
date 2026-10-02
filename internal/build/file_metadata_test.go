package build

import (
	"io/fs"
	"testing"
	"time"
)

type metadataFileInfo struct{ sys any }

func (metadataFileInfo) Name() string       { return "input.go" }
func (metadataFileInfo) Size() int64        { return 4 }
func (metadataFileInfo) Mode() fs.FileMode  { return 0o644 }
func (metadataFileInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (metadataFileInfo) IsDir() bool        { return false }
func (info metadataFileInfo) Sys() any      { return info.sys }

func TestBuildInputMetadataRequiresCompletePhysicalIdentity(t *testing.T) {
	type timestamp struct{ Sec, Nsec int64 }
	var nilPointer *struct{ Dev, Ino uint64 }
	for _, test := range []struct {
		name           string
		sys            any
		device, inode  uint64
		changeTimeNano int64
	}{
		{"darwin-shape", struct {
			Dev       int32
			Ino       uint64
			Ctimespec timestamp
		}{42, 53, timestamp{3, 4}}, 42, 53, 3_000_000_004},
		{"linux-shape", struct {
			Dev, Ino uint64
			Ctim     timestamp
		}{42, 53, timestamp{3, 4}}, 42, 53, 3_000_000_004},
		{"other-unix-shape", struct {
			Dev, Ino uint64
			Ctimen   timestamp
		}{42, 53, timestamp{3, 4}}, 42, 53, 3_000_000_004},
		{"missing-change-time", struct{ Dev, Ino uint64 }{42, 53}, 42, 53, 0},
		{"missing-inode", struct {
			Dev  uint64
			Ctim timestamp
		}{42, timestamp{3, 4}}, 42, 0, 0},
		{"missing-device", struct {
			Ino  uint64
			Ctim timestamp
		}{53, timestamp{3, 4}}, 0, 53, 0},
		{"unavailable", nil, 0, 0, 0},
		{"nil-pointer", nilPointer, 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := metadataFileInfo{test.sys}
			stamp := buildInputStamp(info)
			device, inode := buildInputFileIdentity(info)
			if stamp.Device != test.device || stamp.Inode != test.inode || device != test.device || inode != test.inode || stamp.ChangeTimeNano != test.changeTimeNano || buildInputFileChangeTime(info) != test.changeTimeNano {
				t.Fatalf("identity=%d/%d stamp=%+v", device, inode, stamp)
			}
		})
	}
}
