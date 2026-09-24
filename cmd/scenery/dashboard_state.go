package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devcache"
)

type dashboardRunState struct {
	SupervisorPID int       `json:"supervisor_pid"`
	StartedAt     time.Time `json:"started_at"`
	AppRoot       string    `json:"app_root"`
	DashboardAddr string    `json:"dashboard_addr"`
	// Owner is the supervisor identity captured when it claimed the address.
	// Only a process that still verifies against it is ever stopped.
	Owner localagent.Owner `json:"owner"`

	// cacheRoot overrides the scenery cache root used for the state file.
	// When empty, sceneryCacheRoot() decides.
	cacheRoot string
}

func newDashboardRunState(root, addr string) dashboardRunState {
	return dashboardRunState{
		SupervisorPID: os.Getpid(),
		StartedAt:     time.Now().UTC(),
		AppRoot:       root,
		DashboardAddr: addr,
		Owner:         localagent.CurrentOwner("scenery up control listener"),
	}
}

func (s dashboardRunState) path() (string, error) {
	root := s.cacheRoot
	if root == "" {
		var err error
		root, err = sceneryCacheRoot()
		if err != nil {
			return "", err
		}
	}
	key := sha256.Sum256([]byte(s.AppRoot + "\x00" + s.DashboardAddr))
	filename := hex.EncodeToString(key[:8]) + ".json"
	return filepath.Join(root, "run", "dashboards", filename), nil
}

func (s dashboardRunState) write() error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s dashboardRunState) remove() error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func loadDashboardRunState(path string) (dashboardRunState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dashboardRunState{}, err
	}
	var state dashboardRunState
	if err := json.Unmarshal(data, &state); err != nil {
		return dashboardRunState{}, err
	}
	return state, nil
}

// ensureDashboardPortAvailable stops the recorded previous owner of addr, if
// it still verifies, and otherwise leaves whatever holds addr alone: the port
// number identifies no owner, so an occupied address is a named failure.
func ensureDashboardPortAvailable(addr string, state dashboardRunState) error {
	statePath, err := state.path()
	if err != nil {
		return err
	}
	if err := reapOwnedDashboard(statePath, state); err != nil {
		return err
	}
	return portAvailable(addr)
}

func reapOwnedDashboard(statePath string, expected dashboardRunState) error {
	state, err := loadDashboardRunState(statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		_ = os.Remove(statePath)
		return nil
	}

	if strings.TrimSpace(state.AppRoot) != strings.TrimSpace(expected.AppRoot) || state.DashboardAddr != expected.DashboardAddr {
		return nil
	}
	if state.SupervisorPID == os.Getpid() {
		return nil
	}
	if state.Owner.PID != state.SupervisorPID || localagent.VerifyOwner(state.Owner) != nil {
		_ = os.Remove(statePath)
		return nil
	}
	if err := stopRecordedOwner(state.Owner, 2*time.Second); err != nil {
		return fmt.Errorf("stop stale control listener owner %d: %w", state.Owner.PID, err)
	}
	if err := waitForPortRelease(expected.DashboardAddr, 3*time.Second); err != nil {
		return err
	}
	_ = os.Remove(statePath)
	return nil
}

func sceneryCacheRoot() (string, error) {
	return devcache.Root()
}

func waitForPortRelease(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := portAvailable(addr); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return portAvailable(addr)
}
