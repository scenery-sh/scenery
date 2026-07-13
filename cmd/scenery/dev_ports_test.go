package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/machine"
)

func TestPreferredDevPortStableForAppRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	first, err := preferredDevPort(root, 4001, 4999)
	if err != nil {
		t.Fatal(err)
	}
	second, err := preferredDevPort(root, 4001, 4999)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("preferred port not stable: %d then %d", first, second)
	}
	if first < 4001 || first > 4999 {
		t.Fatalf("preferred port %d outside range", first)
	}
}

func TestAllocateDevPortLeaseSkipsOccupiedPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev-ports.json")
	occupied := map[int]bool{4001: true}
	lease, err := allocateDevPortLease(path, devPortLeaseRequest{
		AppRoot:   filepath.Join(t.TempDir(), "app"),
		SessionID: "main",
		Start:     4001,
		End:       4003,
		Port:      4001,
		PortFree:  func(port int) bool { return !occupied[port] },
		Now:       time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Port != 4002 {
		t.Fatalf("port = %d, want 4002", lease.Port)
	}
}

func TestAllocateDevPortLeaseReusesExistingLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev-ports.json")
	root := filepath.Join(t.TempDir(), "app")
	first, err := allocateDevPortLease(path, devPortLeaseRequest{
		AppRoot:   root,
		SessionID: "main",
		Start:     4001,
		End:       4003,
		Port:      4002,
		PortFree:  func(int) bool { return true },
		Now:       time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocateDevPortLease(path, devPortLeaseRequest{
		AppRoot:   root,
		SessionID: "main",
		Start:     4001,
		End:       4003,
		PortFree:  func(int) bool { return true },
		Now:       time.Unix(2, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Port != first.Port {
		t.Fatalf("port = %d, want reused %d", second.Port, first.Port)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("created_at changed on reuse")
	}
}

func TestAllocateDevPortLeaseReclaimsStaleFreeLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev-ports.json")
	rootA := filepath.Join(t.TempDir(), "app-a")
	rootB := filepath.Join(t.TempDir(), "app-b")
	stale := devPortLeaseFile{
		ArtifactIdentity: machine.NewArtifactIdentity(devPortLeaseFileKind, devPortLeaseFileDescriptor),
		Leases: []localagent.PortLease{{
			ArtifactIdentity: localagent.NewPortLeaseIdentity(),
			AppRoot:          rootA,
			SessionID:        "old",
			Port:             4001,
			Owner:            localagent.Owner{PID: 999999},
		}},
	}
	if err := saveDevPortLeases(path, stale); err != nil {
		t.Fatal(err)
	}
	lease, err := allocateDevPortLease(path, devPortLeaseRequest{
		AppRoot:   rootB,
		SessionID: "new",
		Start:     4001,
		End:       4001,
		Port:      4001,
		PortFree:  func(int) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Port != 4001 || lease.AppRoot != filepath.Clean(rootB) {
		t.Fatalf("lease = %+v, want reclaimed port for rootB", lease)
	}
}

func TestAllocateDevPortLeaseFailsWhenRangeExhausted(t *testing.T) {
	_, err := allocateDevPortLease(filepath.Join(t.TempDir(), "dev-ports.json"), devPortLeaseRequest{
		AppRoot:   filepath.Join(t.TempDir(), "app"),
		SessionID: "main",
		Start:     4001,
		End:       4001,
		Port:      4001,
		PortFree:  func(int) bool { return false },
	})
	if err == nil {
		t.Fatal("expected exhausted range error")
	}
}

func TestLoadDevPortLeasesMigratesOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev-ports.json")
	legacy := []byte(`{"schema_version":"scenery.dev.port_lease.v1","leases":[{"schema_version":"scenery.dev.port_lease.v1","app_root":"/repo/onlv","session_id":"main","port":4001,"url":"http://localhost:4001","owner_pid":42,"owner":{"pid":42,"recorded_at":"2026-07-13T00:00:00Z"},"created_at":"2026-07-13T00:00:00Z","updated_at":"2026-07-13T00:00:00Z"}]}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := loadDevPortLeases(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != devPortLeaseFileKind || len(file.Leases) != 1 || file.Leases[0].OwnerPID != 42 || file.Leases[0].Kind != localagent.PortLeaseKind {
		t.Fatalf("migrated leases = %+v", file)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}

func TestDevPortFreeDetectsOccupiedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if devPortFree(port) {
		t.Fatalf("port %d reported free while listener is active", port)
	}
}
