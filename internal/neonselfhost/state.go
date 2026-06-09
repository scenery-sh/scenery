package neonselfhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultBranchPortBase  = 55440
	DefaultBranchPortRange = 1000
)

type BackendState struct {
	SchemaVersion string                    `json:"schema_version"`
	Provider      string                    `json:"provider"`
	Projects      map[string]BackendProject `json:"projects"`
	UpdatedAt     string                    `json:"updated_at,omitempty"`
}

type BackendProject struct {
	TenantID         string                   `json:"tenant_id"`
	DefaultPGVersion int                      `json:"default_pg_version"`
	Branches         map[string]BackendBranch `json:"branches"`
	UpdatedAt        string                   `json:"updated_at,omitempty"`
}

type BackendBranch struct {
	Project          string `json:"project"`
	Branch           string `json:"branch"`
	TimelineID       string `json:"timeline_id"`
	ParentTimelineID string `json:"parent_timeline_id,omitempty"`
	EndpointID       string `json:"endpoint_id"`
	ComputeContainer string `json:"compute_container"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Database         string `json:"database"`
	Role             string `json:"role"`
	Status           string `json:"status"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type legacyBackendStateV1 struct {
	SchemaVersion    string                   `json:"schema_version"`
	Provider         string                   `json:"provider"`
	TenantID         string                   `json:"tenant_id"`
	DefaultPGVersion int                      `json:"default_pg_version"`
	Branches         map[string]BackendBranch `json:"branches"`
	UpdatedAt        string                   `json:"updated_at,omitempty"`
}

func NewBackendState() BackendState {
	return BackendState{
		SchemaVersion: BackendSchemaVersion,
		Provider:      "neon-selfhost",
		Projects:      map[string]BackendProject{},
	}
}

func NewBackendProject(project string, pgVersion int) BackendProject {
	if pgVersion == 0 {
		pgVersion = defaultPGVersion
	}
	return BackendProject{
		TenantID:         stableHexID("tenant:" + projectKey(project)),
		DefaultPGVersion: pgVersion,
		Branches:         map[string]BackendBranch{},
	}
}

func ReadBackendState(path string) (BackendState, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return BackendState{}, false, nil
	}
	if err != nil {
		return BackendState{}, false, err
	}
	var header struct {
		SchemaVersion string `json:"schema_version"`
		Provider      string `json:"provider"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return BackendState{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if header.Provider != "neon-selfhost" {
		return BackendState{}, false, fmt.Errorf("%s has unsupported provider %q", path, header.Provider)
	}
	if header.SchemaVersion == LegacyBackendSchemaVersion {
		var legacy legacyBackendStateV1
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&legacy); err != nil {
			return BackendState{}, false, fmt.Errorf("parse %s: %w", path, err)
		}
		return migrateLegacyBackendStateV1(legacy), true, nil
	}
	if header.SchemaVersion != BackendSchemaVersion {
		return BackendState{}, false, fmt.Errorf("%s has unsupported schema_version %q", path, header.SchemaVersion)
	}
	var state BackendState
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return BackendState{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	normalizeBackendState(&state)
	return state, true, nil
}

func WriteBackendState(path string, state BackendState) error {
	if state.SchemaVersion == "" {
		state.SchemaVersion = BackendSchemaVersion
	}
	if state.Provider == "" {
		state.Provider = "neon-selfhost"
	}
	normalizeBackendState(&state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o644)
}

func WithBackendStateLock(root string, fn func() error) error {
	unlock, err := lockBackendState(root)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func projectKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func ensureBackendProject(state *BackendState, project string, pgVersion int) (BackendProject, string, error) {
	key := projectKey(project)
	if key == "" {
		return BackendProject{}, "", fmt.Errorf("neon-selfhost-driver requires --project")
	}
	normalizeBackendState(state)
	current := state.Projects[key]
	if current.Branches == nil {
		current = NewBackendProject(key, pgVersion)
	}
	if strings.TrimSpace(current.TenantID) == "" {
		current.TenantID = stableHexID("tenant:" + key)
	}
	if current.DefaultPGVersion == 0 {
		current.DefaultPGVersion = firstNonZero(pgVersion, defaultPGVersion)
	}
	state.Projects[key] = current
	return current, key, nil
}

func backendProjectForOptions(state *BackendState, opts branchActionOptions) (BackendProject, string, error) {
	return ensureBackendProject(state, opts.Project, defaultPGVersion)
}

func branchCount(state BackendState) int {
	count := 0
	for _, project := range state.Projects {
		count += len(project.Branches)
	}
	return count
}

func AllocateBranchPort(state BackendState, project string, branchID string) (int, error) {
	key := projectKey(project)
	if key == "" {
		return 0, fmt.Errorf("neon-selfhost-driver requires project for branch port allocation")
	}
	if branch, ok := state.Projects[key].Branches[branchID]; ok && branch.Port > 0 {
		return branch.Port, nil
	}
	used := map[int]bool{}
	for _, project := range state.Projects {
		for _, branch := range project.Branches {
			if branch.Port > 0 {
				used[branch.Port] = true
			}
		}
	}
	start := DefaultBranchPortBase + int(hashString(key+"\x00"+branchID)%DefaultBranchPortRange)
	for offset := 0; offset < DefaultBranchPortRange; offset++ {
		port := DefaultBranchPortBase + ((start - DefaultBranchPortBase + offset) % DefaultBranchPortRange)
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("neon-selfhost-driver could not allocate branch port for project %q branch %q: range %d-%d is exhausted", key, branchID, DefaultBranchPortBase, DefaultBranchPortBase+DefaultBranchPortRange-1)
}

func migrateLegacyBackendStateV1(legacy legacyBackendStateV1) BackendState {
	state := NewBackendState()
	state.UpdatedAt = legacy.UpdatedAt
	keys := make([]string, 0, len(legacy.Branches))
	for branchID := range legacy.Branches {
		keys = append(keys, branchID)
	}
	sort.Strings(keys)
	preservedTenantProject := ""
	for _, branchID := range keys {
		branch := legacy.Branches[branchID]
		key := projectKey(branch.Project)
		if key == "" {
			key = legacyProjectKey()
			branch.Project = key
		}
		project := state.Projects[key]
		if project.Branches == nil {
			project = NewBackendProject(key, legacy.DefaultPGVersion)
		}
		if preservedTenantProject == "" && strings.TrimSpace(legacy.TenantID) != "" {
			project.TenantID = strings.TrimSpace(legacy.TenantID)
			preservedTenantProject = key
		}
		project.Branches[branchID] = branch
		state.Projects[key] = project
	}
	if len(keys) == 0 && strings.TrimSpace(legacy.TenantID) != "" {
		project := NewBackendProject(legacyProjectKey(), legacy.DefaultPGVersion)
		project.TenantID = strings.TrimSpace(legacy.TenantID)
		state.Projects[legacyProjectKey()] = project
	}
	normalizeBackendState(&state)
	return state
}

func legacyProjectKey() string {
	return "legacy"
}

func normalizeBackendState(state *BackendState) {
	if state.SchemaVersion == "" {
		state.SchemaVersion = BackendSchemaVersion
	}
	if state.Provider == "" {
		state.Provider = "neon-selfhost"
	}
	if state.Projects == nil {
		state.Projects = map[string]BackendProject{}
	}
	for key, project := range state.Projects {
		normalizedKey := projectKey(key)
		if normalizedKey == "" {
			normalizedKey = legacyProjectKey()
		}
		if project.Branches == nil {
			project.Branches = map[string]BackendBranch{}
		}
		if project.DefaultPGVersion == 0 {
			project.DefaultPGVersion = defaultPGVersion
		}
		if strings.TrimSpace(project.TenantID) == "" {
			project.TenantID = stableHexID("tenant:" + normalizedKey)
		}
		for branchID, branch := range project.Branches {
			if strings.TrimSpace(branch.Project) == "" {
				branch.Project = normalizedKey
				project.Branches[branchID] = branch
			}
		}
		if normalizedKey != key {
			delete(state.Projects, key)
		}
		state.Projects[normalizedKey] = project
	}
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func hashString(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
