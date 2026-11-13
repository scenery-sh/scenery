package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/doctor"
)

type fakeDoctorResourceProbe struct{}

func (fakeDoctorResourceProbe) Runtime() doctor.RuntimeInfo {
	return doctor.RuntimeInfo{GOOS: "darwin", GOARCH: "arm64", NumCPU: 8}
}

func (fakeDoctorResourceProbe) Memory(ctx context.Context) (doctor.MemoryInfo, error) {
	return doctor.MemoryInfo{TotalBytes: 8 * 1024 * 1024 * 1024}, nil
}

func (fakeDoctorResourceProbe) Disk(ctx context.Context, path string) (doctor.DiskInfo, error) {
	return doctor.DiskInfo{Path: path, FreeBytes: 20 * 1024 * 1024 * 1024, TotalBytes: 40 * 1024 * 1024 * 1024}, nil
}

func doctorHasCheck(checks []doctor.Check, id string) bool {
	for _, check := range checks {
		if check.ID == id {
			return true
		}
	}
	return false
}

func TestDoctorWarnsAboutDuplicateRuntimeOwners(t *testing.T) {
	home := t.TempDir()
	paths := localagent.PathsForHome(home)
	check := doctorProcessOwnershipCheck(context.Background(), doctor.ProbeDeps{
		ResourceProbe: fakeDoctorResourceProbe{},
		AgentHome:     func() (string, error) { return home, nil },
		RunCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ps" {
				return []byte(fmt.Sprintf("10 %d /bin/scenery system agent --socket %s --router-listen 127.0.0.1:9440\n11 %d /bin/scenery system agent --socket /tmp/stale.sock --router-listen 127.0.0.1:9440\n", os.Getuid(), paths.SocketPath, os.Getuid())), nil
			}
			return []byte("10\n11\n"), nil
		},
	})
	if check.Status != doctor.StatusWarn {
		t.Fatalf("process ownership check = %+v", check)
	}
}
