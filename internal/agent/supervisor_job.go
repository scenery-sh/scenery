package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// SupervisorJob is the installed launchd job or systemd unit that supervises
// an agent socket.
type SupervisorJob struct {
	Label string
	Path  string
	// FailureContainment reports whether the job runs the agent with
	// --supervised, which contains a failing start instead of letting the
	// supervisor restart it forever. A job Scenery cannot read has none.
	FailureContainment bool
}

// InstalledSupervisorJob reads, from the job files alone, the installed job
// that supervises socketPath; ok is false when none does.
func InstalledSupervisorJob(socketPath string) (SupervisorJob, bool) {
	socketPath = filepath.Clean(strings.TrimSpace(socketPath))
	if plistPath, err := AgentLaunchdPlistPath(); err == nil {
		if data, err := os.ReadFile(plistPath); err == nil {
			_, paths, opts, parseErr := parseAgentLaunchdPlist(data)
			if parseErr == nil && filepath.Clean(paths.SocketPath) == socketPath {
				return SupervisorJob{Label: AgentLaunchdLabel, Path: plistPath, FailureContainment: opts.Supervised}, true
			}
			if parseErr != nil && strings.Contains(string(data), "<string>"+plistEscape(socketPath)+"</string>") {
				return SupervisorJob{Label: AgentLaunchdLabel, Path: plistPath}, true
			}
		}
	}
	unitPath := AgentSystemdUnitPath()
	if data, err := os.ReadFile(unitPath); err == nil {
		_, paths, opts, parseErr := parseAgentSystemdUnit(string(data))
		if parseErr == nil && filepath.Clean(paths.SocketPath) == socketPath {
			return SupervisorJob{Label: AgentSystemdUnitName, Path: unitPath, FailureContainment: opts.Supervised}, true
		}
		if parseErr != nil && strings.Contains(string(data), "--socket "+socketPath) {
			return SupervisorJob{Label: AgentSystemdUnitName, Path: unitPath}, true
		}
	}
	return SupervisorJob{}, false
}
