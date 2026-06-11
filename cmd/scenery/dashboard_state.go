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

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
)

type dashboardRunState struct {
	SupervisorPID int       `json:"supervisor_pid"`
	StartedAt     time.Time `json:"started_at"`
	AppRoot       string    `json:"app_root"`
	DashboardAddr string    `json:"dashboard_addr"`

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

func ensureDashboardPortAvailable(addr string, state dashboardRunState) error {
	statePath, err := state.path()
	if err != nil {
		return err
	}

	if err := reapOwnedDashboard(statePath, state); err != nil {
		return err
	}
	if err := portAvailable(addr); err == nil {
		return nil
	}
	if addr != devdash.DashboardAddr {
		return portAvailable(addr)
	}

	pid, ok := findListeningPID(addr)
	if !ok {
		return portAvailable(addr)
	}
	info, ok := inspectProcess(pid)
	if !ok {
		return portAvailable(addr)
	}
	if !looksLikeSceneryDashboardProcess(info) {
		return portAvailable(addr)
	}
	if err := stopProcess(pid); err != nil {
		return err
	}
	return waitForPortRelease(addr, 3*time.Second)
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

	info, ok := inspectProcess(state.SupervisorPID)
	if !ok {
		_ = os.Remove(statePath)
		return nil
	}
	if !looksLikeSceneryDashboardProcess(info) {
		_ = os.Remove(statePath)
		return nil
	}
	if info.pid == os.Getpid() {
		return nil
	}
	if err := stopProcess(info.pid); err != nil {
		return fmt.Errorf("stop stale dashboard owner %d: %w", info.pid, err)
	}
	if err := waitForPortRelease(expected.DashboardAddr, 3*time.Second); err != nil {
		return err
	}
	_ = os.Remove(statePath)
	return nil
}

func sceneryCacheRoot() (string, error) {
	if root := strings.TrimSpace(envpolicy.Get("SCENERY_DEV_CACHE_DIR")); root != "" {
		return root, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scenery"), nil
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
