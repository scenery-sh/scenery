package agent

import (
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Identity describes the scenery build a process was compiled from. Agents
// report it through health/state so CLI processes can detect an outdated
// running agent.
type Identity struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"built_at,omitempty"`
}

func (id Identity) IsZero() bool {
	return strings.TrimSpace(id.Version) == "" &&
		strings.TrimSpace(id.Commit) == "" &&
		strings.TrimSpace(id.BuiltAt) == ""
}

func (id Identity) String() string {
	parts := make([]string, 0, 3)
	if version := strings.TrimSpace(id.Version); version != "" {
		parts = append(parts, version)
	}
	if commit := strings.TrimSpace(id.Commit); commit != "" {
		parts = append(parts, "commit "+commit)
	}
	if builtAt := strings.TrimSpace(id.BuiltAt); builtAt != "" {
		parts = append(parts, "built "+builtAt)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ", ")
}

// ShouldReplaceAgent reports whether a running agent with identity running
// should be restarted so the agent runs the build identified by current.
// A running agent that reports no identity predates identity reporting and is
// replaced whenever the current build has an identity. When both identities
// are present the running agent is replaced only when the current build is
// strictly newer: by semver when both versions are valid semver, otherwise by
// built-at timestamp.
func ShouldReplaceAgent(current, running Identity) bool {
	if current.IsZero() {
		return false
	}
	if running.IsZero() {
		return true
	}
	currentVersion := canonicalSemver(current.Version)
	runningVersion := canonicalSemver(running.Version)
	if currentVersion != "" && runningVersion != "" {
		if cmp := semver.Compare(currentVersion, runningVersion); cmp != 0 {
			return cmp > 0
		}
	}
	currentBuilt, currentOK := parseBuildTime(current.BuiltAt)
	runningBuilt, runningOK := parseBuildTime(running.BuiltAt)
	if currentOK && runningOK {
		return currentBuilt.After(runningBuilt)
	}
	return false
}

func canonicalSemver(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return ""
	}
	return version
}

func parseBuildTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
