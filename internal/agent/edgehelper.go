package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EdgeHelperContractRevision identifies the privileged edge helper handoff
// contract: the frozen EdgeHelperTarget field set plus the helper's
// per-connection validation semantics. Installation stamps this revision into
// the helper LaunchDaemon arguments so drift detection can compare the
// installed helper against the current binary. Bump it only when a frozen
// field is renamed, removed, or validated with different semantics; additive
// target-metadata fields never require a bump because helpers decode
// tolerantly.
//
// Revision history:
//   - (unstamped): helpers decoded target metadata with the strict durable
//     artifact loader and broke whenever a newer scenery rewrote the file
//     under a new schema/spec revision.
//   - "2": helpers decode via LoadEdgeHelperTarget, ignoring artifact
//     identity and unknown fields, and never rewrite the file.
const EdgeHelperContractRevision = "2"

// EdgeHelperTarget is the frozen handoff payload the privileged helper reads
// from the edge target metadata file on every accepted connection. The JSON
// field names and meanings are a cross-version contract between any installed
// helper build and any newer scenery writing the file; see
// EdgeHelperContractRevision before changing them.
type EdgeHelperTarget struct {
	Kind           string `json:"edge_kind"`
	TargetAddr     string `json:"target_addr"`
	HTTPTargetAddr string `json:"http_target_addr"`
	PID            int    `json:"pid"`
	OwnerUID       int    `json:"owner_uid"`
	OwnerGID       int    `json:"owner_gid"`
	ProcessStart   string `json:"process_start"`
	Executable     string `json:"executable"`
}

// LoadEdgeHelperTarget reads target metadata on behalf of the privileged
// helper. It is deliberately tolerant — unknown fields and artifact identity
// revisions from newer or older writers are ignored — and never rewrites the
// file: the helper runs as root and must not take ownership of user state.
func LoadEdgeHelperTarget(path string) (EdgeHelperTarget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EdgeHelperTarget{}, err
	}
	var target EdgeHelperTarget
	if err := json.Unmarshal(data, &target); err != nil {
		return EdgeHelperTarget{}, fmt.Errorf("edge helper target metadata %s is not valid JSON: %w", path, err)
	}
	target.Kind = strings.TrimSpace(target.Kind)
	target.TargetAddr = strings.TrimSpace(target.TargetAddr)
	target.HTTPTargetAddr = strings.TrimSpace(target.HTTPTargetAddr)
	target.ProcessStart = strings.TrimSpace(target.ProcessStart)
	target.Executable = strings.TrimSpace(target.Executable)
	return target, nil
}
