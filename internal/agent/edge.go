package agent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	legacyEdgeSchemaVersion       = "scenery.edge.state.v1"
	legacyEdgeTargetSchemaVersion = "scenery.edge.target.v1"
	EdgeKindCaddy                 = "caddy"
	EdgeStatusRunning             = "running"
	EdgeStatusStopped             = "stopped"
)

func LoadEdgeState(path string) (EdgeState, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return EdgeState{}, nil
	}
	if err != nil {
		return EdgeState{}, err
	}
	var state EdgeState
	if err := LoadDurableArtifact(path, &state, &state.ArtifactIdentity, EdgeStateKind, edgeStateSchemaDescriptor, 0o644, func(fields map[string]json.RawMessage) error {
		if err := requireLegacySchemaOrMissing(fields, legacyEdgeSchemaVersion); err != nil {
			return err
		}
		renameLegacyField(fields, "kind", "edge_kind")
		return nil
	}); err != nil {
		return EdgeState{}, err
	}
	state.Kind = sanitizeLabel(state.Kind)
	state.Status = strings.TrimSpace(state.Status)
	state.PublicAddr = strings.TrimSpace(state.PublicAddr)
	state.PublicScheme = strings.TrimSpace(state.PublicScheme)
	state.HTTPSListen = strings.TrimSpace(state.HTTPSListen)
	state.UpstreamAddr = strings.TrimSpace(state.UpstreamAddr)
	return state, nil
}

func WriteEdgeState(path string, state EdgeState) error {
	state.ArtifactIdentity = edgeStateIdentity()
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o644)
}

func LoadEdgeTargetState(path string) (EdgeTargetState, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return EdgeTargetState{}, nil
	}
	if err != nil {
		return EdgeTargetState{}, err
	}
	var state EdgeTargetState
	if err := LoadDurableArtifact(path, &state, &state.ArtifactIdentity, EdgeTargetKind, edgeTargetSchemaDescriptor, 0o600, func(fields map[string]json.RawMessage) error {
		if err := requireLegacySchemaOrMissing(fields, legacyEdgeTargetSchemaVersion); err != nil {
			return err
		}
		renameLegacyField(fields, "kind", "edge_kind")
		return nil
	}); err != nil {
		return EdgeTargetState{}, err
	}
	state.Kind = sanitizeLabel(state.Kind)
	state.TargetAddr = strings.TrimSpace(state.TargetAddr)
	state.HTTPTargetAddr = strings.TrimSpace(state.HTTPTargetAddr)
	state.ProcessStart = strings.TrimSpace(state.ProcessStart)
	state.Executable = strings.TrimSpace(state.Executable)
	return state, nil
}

func WriteEdgeTargetState(path string, state EdgeTargetState) error {
	state.ArtifactIdentity = edgeTargetIdentity()
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o600)
}

func readEdgeToken(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func EdgeStateRunning(state EdgeState) bool {
	if state.Kind != EdgeKindCaddy || state.Status != EdgeStatusRunning || state.PID <= 0 {
		return false
	}
	return processAlive(state.PID)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return !processZombie(pid)
}

func processZombie(pid int) bool {
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
