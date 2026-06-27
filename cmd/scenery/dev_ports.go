package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const (
	devPortLeaseSchemaVersion = "scenery.dev.port_lease.v1"
	defaultDevPortStart       = 4001
	defaultDevPortEnd         = 4999
)

type devPortLeaseFile struct {
	SchemaVersion string                 `json:"schema_version"`
	Leases        []localagent.PortLease `json:"leases"`
}

type devPortLeaseRequest struct {
	AppRoot       string
	SessionID     string
	BaseAppID     string
	Branch        string
	WorktreeLabel string
	Start         int
	End           int
	OwnerPID      int
	Owner         localagent.Owner
	Port          int
	PortFree      func(int) bool
	Now           time.Time
}

func defaultDevPortLeasePath(paths localagent.Paths) string {
	return filepath.Join(paths.RunDir, "dev-ports.json")
}

func preferredDevPort(appRoot string, start, end int) (int, error) {
	start, end, err := normalizeDevPortRange(start, end)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(appRoot)))
	span := uint32(end - start + 1)
	offset := binary.BigEndian.Uint32(sum[:4]) % span
	return start + int(offset), nil
}

func allocateDevPortLease(path string, req devPortLeaseRequest) (localagent.PortLease, error) {
	start, end, err := normalizeDevPortRange(req.Start, req.End)
	if err != nil {
		return localagent.PortLease{}, err
	}
	appRoot := filepath.Clean(strings.TrimSpace(req.AppRoot))
	sessionID := strings.TrimSpace(req.SessionID)
	if appRoot == "" || sessionID == "" {
		return localagent.PortLease{}, fmt.Errorf("app root and session id are required for dev port lease")
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	portFree := req.PortFree
	if portFree == nil {
		portFree = devPortFree
	}
	file, err := loadDevPortLeases(path)
	if err != nil {
		return localagent.PortLease{}, err
	}
	for _, lease := range file.Leases {
		if filepath.Clean(lease.AppRoot) == appRoot && strings.TrimSpace(lease.SessionID) == sessionID && lease.Port >= start && lease.Port <= end {
			if sameDevPortLeaseOwner(lease, req) || portFree(lease.Port) {
				lease.UpdatedAt = now
				file.Leases = upsertDevPortLease(file.Leases, lease)
				if err := saveDevPortLeases(path, file); err != nil {
					return localagent.PortLease{}, err
				}
				return lease, nil
			}
		}
	}
	file.Leases = pruneStaleDevPortLeases(file.Leases, portFree)
	preferred := req.Port
	if preferred == 0 {
		preferred, err = preferredDevPort(appRoot, start, end)
		if err != nil {
			return localagent.PortLease{}, err
		}
	}
	for i := 0; i <= end-start; i++ {
		port := start + ((preferred - start + i + (end - start + 1)) % (end - start + 1))
		if portClaimedByOther(file.Leases, port, appRoot, sessionID) || !portFree(port) {
			continue
		}
		lease := localagent.PortLease{
			SchemaVersion: devPortLeaseSchemaVersion,
			AppRoot:       appRoot,
			SessionID:     sessionID,
			BaseAppID:     strings.TrimSpace(req.BaseAppID),
			Branch:        strings.TrimSpace(req.Branch),
			WorktreeLabel: strings.TrimSpace(req.WorktreeLabel),
			Port:          port,
			URL:           fmt.Sprintf("http://localhost:%d", port),
			OwnerPID:      req.OwnerPID,
			Owner:         localagent.OwnerFromRequest(req.OwnerPID, req.Owner, "scenery local path caddy"),
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		file.Leases = upsertDevPortLease(file.Leases, lease)
		if err := saveDevPortLeases(path, file); err != nil {
			return localagent.PortLease{}, err
		}
		return lease, nil
	}
	return localagent.PortLease{}, fmt.Errorf("no free localhost ports in range %d-%d", start, end)
}

func normalizeDevPortRange(start, end int) (int, int, error) {
	if start == 0 {
		start = defaultDevPortStart
	}
	if end == 0 {
		end = defaultDevPortEnd
	}
	if start < 1024 || end < start || end > 65535 {
		return 0, 0, fmt.Errorf("invalid dev port range %d-%d", start, end)
	}
	return start, end, nil
}

func loadDevPortLeases(path string) (devPortLeaseFile, error) {
	file := devPortLeaseFile{SchemaVersion: devPortLeaseSchemaVersion}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, err
	}
	if file.SchemaVersion == "" {
		file.SchemaVersion = devPortLeaseSchemaVersion
	}
	return file, nil
}

func saveDevPortLeases(path string, file devPortLeaseFile) error {
	file.SchemaVersion = devPortLeaseSchemaVersion
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o600)
}

func pruneStaleDevPortLeases(leases []localagent.PortLease, portFree func(int) bool) []localagent.PortLease {
	var kept []localagent.PortLease
	for _, lease := range leases {
		if lease.Port <= 0 {
			continue
		}
		if lease.Owner.PID > 0 && localagent.VerifyOwner(lease.Owner) == nil {
			kept = append(kept, lease)
			continue
		}
		if !portFree(lease.Port) {
			kept = append(kept, lease)
		}
	}
	return kept
}

func sameDevPortLeaseOwner(lease localagent.PortLease, req devPortLeaseRequest) bool {
	if lease.Owner.PID <= 0 || req.OwnerPID <= 0 || lease.Owner.PID != req.OwnerPID {
		return false
	}
	return localagent.VerifyOwner(lease.Owner) == nil
}

func portClaimedByOther(leases []localagent.PortLease, port int, appRoot, sessionID string) bool {
	for _, lease := range leases {
		if lease.Port != port {
			continue
		}
		if filepath.Clean(lease.AppRoot) == appRoot && strings.TrimSpace(lease.SessionID) == sessionID {
			continue
		}
		return true
	}
	return false
}

func upsertDevPortLease(leases []localagent.PortLease, next localagent.PortLease) []localagent.PortLease {
	for i, lease := range leases {
		if filepath.Clean(lease.AppRoot) == filepath.Clean(next.AppRoot) && strings.TrimSpace(lease.SessionID) == strings.TrimSpace(next.SessionID) {
			if !lease.CreatedAt.IsZero() && next.CreatedAt.IsZero() {
				next.CreatedAt = lease.CreatedAt
			}
			leases[i] = next
			return leases
		}
	}
	return append(leases, next)
}

func devPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
