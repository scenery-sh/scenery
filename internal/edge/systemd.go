package edge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	localagent "scenery.sh/internal/agent"
)

// SystemdEdgeUnitName is the system-level unit that owns the managed public
// Caddy edge on Linux deploy hosts. Scenery renders the Caddyfile; systemd
// owns the process lifecycle (restart on failure, start after network,
// journald logs). There is exactly one managed edge per host.
const SystemdEdgeUnitName = "scenery-edge.service"

var (
	systemctlEdgeRunFunc  = runSystemctlEdge
	systemdEdgeUnitDirVar = "/etc/systemd/system"
)

func runSystemctlEdge(args ...string) ([]byte, error) {
	return exec.Command("systemctl", args...).CombinedOutput()
}

// SystemdEdgeUnitPath resolves the managed edge unit path.
func SystemdEdgeUnitPath() string {
	return filepath.Join(systemdEdgeUnitDirVar, SystemdEdgeUnitName)
}

// SystemdEdgeUnitInstalled reports whether the managed edge unit file exists.
func SystemdEdgeUnitInstalled() bool {
	info, err := os.Stat(SystemdEdgeUnitPath())
	return err == nil && info.Mode().IsRegular()
}

// SystemdEdgeUnit renders the managed edge unit for the given managed Caddy
// binary and agent paths. The unit runs Scenery's managed Caddy with the
// Scenery-rendered Caddyfile; it never references a distro Caddy or an
// app-owned configuration.
func SystemdEdgeUnit(caddyBin string, paths localagent.Paths, adminSocket string) string {
	home := filepath.Dir(paths.Home)
	return fmt.Sprintf(`[Unit]
Description=Scenery managed public edge (Caddy)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --config %s --adapter caddyfile
ExecReload=%s reload --config %s --adapter caddyfile --address unix//%s
Restart=always
RestartSec=2
LimitNOFILE=1048576
Environment=HOME=%s

[Install]
WantedBy=multi-user.target
`, caddyBin, paths.EdgeConfigPath, caddyBin, paths.EdgeConfigPath, adminSocket, home)
}

// SystemdEdgeServiceStatus is systemd truth for the managed edge unit.
type SystemdEdgeServiceStatus struct {
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	Active    bool   `json:"active"`
	PID       int    `json:"pid,omitempty"`
	UnitPath  string `json:"path"`
}

// SystemdEdgeStatus reports the managed edge unit state.
func SystemdEdgeStatus() SystemdEdgeServiceStatus {
	status := SystemdEdgeServiceStatus{UnitPath: SystemdEdgeUnitPath()}
	if !SystemdEdgeUnitInstalled() {
		return status
	}
	status.Installed = true
	out, err := systemctlEdgeRunFunc("show", SystemdEdgeUnitName, "--property=LoadState,ActiveState,MainPID")
	if err != nil {
		return status
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			status.Loaded = value == "loaded"
		case "ActiveState":
			status.Active = value == "active"
		case "MainPID":
			if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
				status.PID = pid
			}
		}
	}
	return status
}

// InstallSystemdEdgeService writes the managed edge unit, reloads systemd,
// and enables and (re)starts the edge. Re-running converges: a changed unit
// or Caddyfile results in a restart of the same single edge.
func InstallSystemdEdgeService(caddyBin string, paths localagent.Paths, adminSocket string) error {
	unitPath := SystemdEdgeUnitPath()
	if err := os.WriteFile(unitPath, []byte(SystemdEdgeUnit(caddyBin, paths, adminSocket)), 0o644); err != nil {
		return err
	}
	if out, err := systemctlEdgeRunFunc("daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := systemctlEdgeRunFunc("enable", SystemdEdgeUnitName); err != nil {
		return fmt.Errorf("systemctl enable %s: %w: %s", SystemdEdgeUnitName, err, strings.TrimSpace(string(out)))
	}
	if out, err := systemctlEdgeRunFunc("restart", SystemdEdgeUnitName); err != nil {
		return fmt.Errorf("systemctl restart %s: %w: %s", SystemdEdgeUnitName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestartSystemdEdgeService restarts the managed edge unit.
func RestartSystemdEdgeService() error {
	if out, err := systemctlEdgeRunFunc("restart", SystemdEdgeUnitName); err != nil {
		return fmt.Errorf("systemctl restart %s: %w: %s", SystemdEdgeUnitName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveSystemdEdgeService stops and disables the managed edge unit and
// removes its file. It does not delete published deploy artifacts or ACME
// state; those belong to the Scenery agent home.
func RemoveSystemdEdgeService() (bool, error) {
	unitPath := SystemdEdgeUnitPath()
	_, _ = systemctlEdgeRunFunc("disable", "--now", SystemdEdgeUnitName)
	err := os.Remove(unitPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, _ = systemctlEdgeRunFunc("daemon-reload")
	return true, nil
}

// ValidateCaddyConfig runs the managed Caddy binary's own validation against
// a candidate config before it is installed or reloaded.
func ValidateCaddyConfig(caddyBin, configPath string) error {
	out, err := exec.Command(caddyBin, "validate", "--config", configPath, "--adapter", "caddyfile").CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy validate %s: %w: %s", configPath, err, tailOfOutput(string(out), 2048))
	}
	return nil
}

func tailOfOutput(out string, limit int) string {
	out = strings.TrimSpace(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
