package agent

import (
	"strings"
)

// Identity describes the scenery build a process was compiled from. Agents
// report it through health/state for diagnostics; build age does not authorize
// replacing a shared agent.
type Identity struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"built_at,omitempty"`
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
