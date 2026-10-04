package main

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

type watchedMetadataInfo struct {
	fs.FileInfo
	sys  any
	mode fs.FileMode
}

func (info watchedMetadataInfo) Sys() any          { return info.sys }
func (info watchedMetadataInfo) Mode() fs.FileMode { return info.mode }

func TestWatchHashReuseRequiresTheSameRegularFileIdentity(t *testing.T) {
	info, err := os.Lstat("watch.go")
	if err != nil {
		t.Fatal(err)
	}
	stamp, _, err := stampWatchedFile("watch.go", info, false)
	if err != nil {
		t.Fatal(err)
	}
	if stamp.changeTime == 0 {
		// Keep the synthetic identity and missing-metadata cases active even
		// when the host filesystem cannot supply a native change timestamp.
		stamp.device, stamp.inode, stamp.changeTime = 42, 53, 3_000_000_004
	}
	type timestamp struct{ Sec, Nsec int64 }
	type identity struct {
		Dev, Ino uint64
		Ctim     timestamp
	}
	same := identity{stamp.device, stamp.inode, timestamp{stamp.changeTime / 1_000_000_000, stamp.changeTime % 1_000_000_000}}
	differentDevice, differentInode := same, same
	differentDevice.Dev++
	differentInode.Ino++
	for _, test := range []struct {
		name string
		sys  any
		mode fs.FileMode
		want bool
	}{
		{"same", same, info.Mode(), true},
		{"different-device", differentDevice, info.Mode(), false},
		{"different-inode", differentInode, info.Mode(), false},
		{"missing-identity", struct{ Ctim timestamp }{same.Ctim}, info.Mode(), false},
		{"missing-change-time", struct{ Dev, Ino uint64 }{same.Dev, same.Ino}, info.Mode(), false},
		{"symlink", same, info.Mode() | fs.ModeSymlink, false},
		{"pipe", same, info.Mode() | fs.ModeNamedPipe, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := watchedMetadataInfo{info, test.sys, test.mode}
			if _, got := reusableStamp(map[string]fileStamp{"watch.go": stamp}, "watch.go", current, false); got != test.want {
				t.Fatalf("reuse = %t, want %t", got, test.want)
			}
			if !test.mode.IsRegular() {
				if _, _, err := stampWatchedFile("watch.go", current, false); !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("non-regular capture = %v", err)
				}
			}
		})
	}
}
