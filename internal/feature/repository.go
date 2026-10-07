package feature

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/redact"
	"slices"
	"strings"
	"time"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var candidatePattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{8}$`)

type Manager struct {
	Root     string
	MainRoot string
	State    string
}

// Open discovers one Git repository and its checked-out main without changing it.
func Open(ctx context.Context, root string) (*Manager, error) {
	if root == "" {
		root = "."
	}
	root, err := git(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	common, err := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return nil, err
	}
	worktrees, err := git(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	mainRoot := ""
	for _, entry := range strings.Split(worktrees, "\n\n") {
		var path, branch string
		for _, line := range strings.Split(entry, "\n") {
			if value, ok := strings.CutPrefix(line, "worktree "); ok {
				path = value
			}
			if value, ok := strings.CutPrefix(line, "branch "); ok {
				branch = value
			}
		}
		if branch == "refs/heads/main" {
			mainRoot = path
		}
	}
	if mainRoot == "" {
		return nil, fmt.Errorf("main must remain checked out in the integration owner")
	}
	mainRoot, err = filepath.EvalSymlinks(mainRoot)
	if err != nil {
		return nil, err
	}
	return &Manager{Root: root, MainRoot: mainRoot, State: filepath.Join(common, "scenery-features")}, nil
}

func git(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(envpolicy.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(output)), fmt.Errorf("git %s: %w: %s", args[0], err, redact.String(bounded(string(output))))
	}
	if strings.ContainsRune(string(output), 0) {
		return string(output), nil
	}
	return strings.TrimSpace(string(output)), nil
}

func bounded(value string) string {
	const limit = 16000
	if len(value) > limit {
		return value[len(value)-limit:]
	}
	return strings.TrimSpace(value)
}

func (m *Manager) recordPath(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid feature name %q: use lowercase letters, digits and hyphens", name)
	}
	return filepath.Join(m.State, "features", name+".json"), nil
}

func (m *Manager) candidatePath(id string) (string, error) {
	if !candidatePattern.MatchString(id) {
		return "", fmt.Errorf("invalid candidate identity %q", id)
	}
	return filepath.Join(m.State, "candidates", id+".json"), nil
}

func (m *Manager) record(name string) (Record, error) {
	path, err := m.recordPath(name)
	if err != nil {
		return Record{}, err
	}
	var record Record
	err = readJSON(path, &record)
	if err == nil && (record.Version != 1 || record.Name != name) {
		err = fmt.Errorf("invalid feature record at %s", path)
	}
	return record, err
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeJSON(data, value)
}

func decodeJSON(data []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func writeJSON(path string, value any, exclusive bool) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".record-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if exclusive {
		return os.Link(file.Name(), path)
	}
	return os.Rename(file.Name(), path)
}

func (m *Manager) records() ([]Record, error) {
	paths, err := filepath.Glob(filepath.Join(m.State, "features", "*.json"))
	if err != nil {
		return nil, err
	}
	result := []Record{}
	for _, path := range paths {
		var record Record
		if err := readJSON(path, &record); err != nil {
			return nil, err
		}
		if record.Version != 1 || !namePattern.MatchString(record.Name) {
			return nil, fmt.Errorf("invalid feature record %s", path)
		}
		result = append(result, record)
	}
	return result, nil
}

func (m *Manager) candidates() ([]Candidate, error) {
	paths, err := filepath.Glob(filepath.Join(m.State, "candidates", "*.json"))
	if err != nil {
		return nil, err
	}
	result := []Candidate{}
	for _, path := range paths {
		var candidate Candidate
		if err := readJSON(path, &candidate); err != nil {
			return nil, err
		}
		if candidate.Version != 1 || !candidatePattern.MatchString(candidate.ID) {
			return nil, fmt.Errorf("invalid candidate record %s", path)
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (m *Manager) saveCandidate(candidate Candidate) error {
	path, err := m.candidatePath(candidate.ID)
	if err != nil {
		return err
	}
	return writeJSON(path, candidate, false)
}

func (m *Manager) loadCandidate(id string) (Candidate, error) {
	path, err := m.candidatePath(id)
	if err != nil {
		return Candidate{}, err
	}
	var c Candidate
	if err := readJSON(path, &c); err != nil {
		return c, err
	}
	if c.ID != id || c.Version != 1 || c.Path != filepath.Join(m.State, "worktrees", id) {
		return c, errors.New("candidate ownership does not match this repository")
	}
	return c, nil
}

func uniqueID() (string, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random[:]), nil
}

func digest(value any) string {
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func pathsFromGit(value string) []string {
	values := strings.Split(value, "\x00")
	result := []string{}
	for _, item := range values {
		if item != "" {
			result = append(result, item)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func union(values ...[]string) []string {
	result := []string{}
	for _, value := range values {
		result = append(result, value...)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func intersection(a, b []string) []string {
	result := []string{}
	for _, value := range a {
		if slices.Contains(b, value) {
			result = append(result, value)
		}
	}
	return result
}

func ancestor(ctx context.Context, root, before, after string) bool {
	_, err := git(ctx, root, "merge-base", "--is-ancestor", before, after)
	return err == nil
}
