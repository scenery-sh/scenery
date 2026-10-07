package feature

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (m *Manager) Create(ctx context.Context, name, purpose, path string, dependencies []string) (Record, error) {
	if path == "" {
		path = filepath.Join(filepath.Dir(m.MainRoot), filepath.Base(m.MainRoot)+"-"+name)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Record{}, err
	}
	return m.add(ctx, name, purpose, path, dependencies, true)
}

func (m *Manager) Register(ctx context.Context, name, purpose, path string, dependencies []string) (Record, error) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Record{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Record{}, err
	}
	return m.add(ctx, name, purpose, path, dependencies, false)
}

func (m *Manager) add(ctx context.Context, name, purpose, path string, dependencies []string, create bool) (Record, error) {
	recordPath, err := m.recordPath(name)
	if err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(purpose) == "" {
		return Record{}, errors.New("feature purpose is required")
	}
	lock, err := m.metadataLock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = lock.Close() }()
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		return Record{}, fmt.Errorf("feature %q already exists or cannot be inspected", name)
	}
	records, err := m.records()
	if err != nil {
		return Record{}, err
	}
	for _, record := range records {
		if record.Path == path {
			return Record{}, fmt.Errorf("checkout already belongs to feature %s", record.Name)
		}
	}
	base, err := git(ctx, m.MainRoot, "rev-parse", "main")
	if err != nil {
		return Record{}, err
	}
	record := Record{Version: 1, Name: name, Purpose: purpose, Path: path, Branch: "feat/" + name, Base: base, Stage: "working", Dependencies: union(dependencies)}
	if err := validateDependencies(record, records); err != nil {
		return Record{}, err
	}
	if create {
		if _, err := git(ctx, m.MainRoot, "worktree", "add", "-b", record.Branch, path, base); err != nil {
			return Record{}, err
		}
	} else {
		other, err := Open(ctx, path)
		if err != nil {
			return Record{}, err
		}
		if other.State != m.State || other.Root != path || path == m.MainRoot {
			return Record{}, errors.New("register a feature checkout in this repository, distinct from main")
		}
		record.Branch, err = git(ctx, path, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return Record{}, errors.New("registered feature checkout must own a local branch")
		}
		record.Base, err = git(ctx, path, "merge-base", "HEAD", "main")
		if err != nil {
			return Record{}, err
		}
	}
	if err := writeJSON(recordPath, record, false); err != nil {
		return Record{}, err
	}
	return record, nil
}

func validateDependencies(record Record, records []Record) error {
	graph := map[string][]string{}
	for _, item := range records {
		graph[item.Name] = item.Dependencies
	}
	graph[record.Name] = record.Dependencies
	for _, name := range record.Dependencies {
		if _, ok := graph[name]; !ok {
			return fmt.Errorf("unknown dependency %q", name)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("feature dependency cycle through %s", name)
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		for _, dependency := range graph[name] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[name] = false
		visited[name] = true
		return nil
	}
	return visit(record.Name)
}

func (m *Manager) Update(ctx context.Context, name string, update Update) (Record, error) {
	lock, err := m.metadataLock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = lock.Close() }()
	record, err := m.record(name)
	if err != nil {
		return record, err
	}
	if record.Closed {
		if update.Stage != "working" {
			return record, errors.New("reopen a closed feature with --stage working before changing it")
		}
		record.Closed = false
	}
	if update.Purpose != "" {
		record.Purpose = update.Purpose
	}
	if update.Stage != "" {
		if !slices.Contains([]string{"working", "ready", "parked"}, update.Stage) {
			return record, errors.New("stage must be working, ready or parked")
		}
		record.Stage = update.Stage
	}
	if update.SetDependencies {
		record.Dependencies = union(update.Dependencies)
	}
	records, err := m.records()
	if err != nil {
		return record, err
	}
	if err := validateDependencies(record, records); err != nil {
		return record, err
	}
	path, _ := m.recordPath(name)
	return record, writeJSON(path, record, false)
}

func dirtyPaths(ctx context.Context, root string) ([]string, error) {
	result := []string{}
	for _, args := range [][]string{{"diff", "--no-renames", "--name-only", "-z"}, {"diff", "--cached", "--no-renames", "--name-only", "-z"}, {"ls-files", "--others", "--exclude-standard", "-z"}} {
		value, err := git(ctx, root, args...)
		if err != nil {
			return nil, err
		}
		result = union(result, pathsFromGit(value))
	}
	return result, nil
}

func changedPaths(ctx context.Context, root, base, tip string) ([]string, error) {
	value, err := git(ctx, root, "diff", "--no-renames", "--name-only", "-z", base, tip, "--")
	return pathsFromGit(value), err
}

func (m *Manager) landings() ([]Candidate, error) {
	paths, err := filepath.Glob(filepath.Join(m.State, "landings", "*.json"))
	if err != nil {
		return nil, err
	}
	result := []Candidate{}
	for _, path := range paths {
		var candidate Candidate
		if err := readJSON(path, &candidate); err != nil {
			return nil, err
		}
		if candidate.Status != "published" || candidate.PublishedAt == nil || candidate.Revision == "" || len(candidate.Receipts) == 0 {
			return nil, fmt.Errorf("incomplete landing receipt %s", path)
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (m *Manager) List(ctx context.Context) (Overview, error) {
	lock, err := m.metadataLock(ctx)
	if err != nil {
		return Overview{}, err
	}
	records, err := m.records()
	if err != nil {
		_ = lock.Close()
		return Overview{}, err
	}
	candidates, err := m.candidates()
	if err != nil {
		_ = lock.Close()
		return Overview{}, err
	}
	landings, err := m.landings()
	_ = lock.Close()
	if err != nil {
		return Overview{}, err
	}
	main, err := git(ctx, m.MainRoot, "rev-parse", "main")
	if err != nil {
		return Overview{}, err
	}
	result := Overview{RepoRoot: m.MainRoot, Main: main, Features: []Row{}, Candidates: candidates}
	for _, record := range records {
		row := Row{Record: record, Outstanding: []string{}, Dirty: []string{}, Overlap: []Overlap{}, Blockers: []string{}, Validation: "not_checked", Landing: "not_landed", Runtime: "unknown"}
		base := record.Base
		for _, landing := range landings {
			for _, checkpoint := range landing.Checkpoints {
				if checkpoint.Feature == record.Name {
					base = checkpoint.Commit
					row.LastCheckpoint = checkpoint.Commit
					row.LastLanding = landing.Tip
					row.Validation = "passed"
					if !ancestor(ctx, m.MainRoot, landing.Tip, main) {
						row.Blockers = append(row.Blockers, "recorded landing is absent from current main")
					}
				}
			}
		}
		if record.Closed && row.LastCheckpoint != "" {
			if _, pathErr := os.Stat(record.Path); os.IsNotExist(pathErr) {
				row.Head, err = git(ctx, m.MainRoot, "for-each-ref", "--format=%(objectname)", "--", "refs/heads/"+record.Branch)
				if err != nil {
					return result, err
				}
				row.Landing = "fully_landed"
				if row.Head != "" && row.Head != row.LastCheckpoint {
					row.Landing = "partially_landed"
					row.Blockers = append(row.Blockers, "closed feature branch acquired later commits")
					row.Outstanding, err = changedPaths(ctx, m.MainRoot, row.LastCheckpoint, row.Head)
					if err != nil {
						return result, err
					}
				}
				row.Status = rowStatus(row)
				result.Features = append(result.Features, row)
				continue
			}
		}
		row.Head, err = git(ctx, record.Path, "rev-parse", "HEAD")
		if err != nil {
			row.Blockers = append(row.Blockers, "feature checkout is missing or unavailable")
			row.Status = "blocked"
			result.Features = append(result.Features, row)
			continue
		}
		committed, err := changedPaths(ctx, record.Path, base, row.Head)
		if err != nil {
			return result, err
		}
		row.Dirty, err = dirtyPaths(ctx, record.Path)
		if err != nil {
			return result, err
		}
		row.Outstanding = union(committed, row.Dirty)
		if row.LastCheckpoint != "" {
			row.Landing = "partially_landed"
			if row.Head == row.LastCheckpoint && len(row.Dirty) == 0 {
				row.Landing = "fully_landed"
				if treeErr := verifyCandidateTree(ctx, record.Path); treeErr != nil {
					row.Blockers = append(row.Blockers, treeErr.Error())
				}
			}
			if row.Head != row.LastCheckpoint && !ancestor(ctx, record.Path, row.LastCheckpoint, row.Head) {
				row.Blockers = append(row.Blockers, "feature history changed after its recorded checkpoint")
			}
		}
		if row.Landing != "fully_landed" && row.LastCheckpoint != "" {
			row.Validation = "outstanding_unchecked"
		}
		var latest *Candidate
		for i := range candidates {
			candidate := &candidates[i]
			if candidate.Status == "published" || row.Landing == "fully_landed" {
				continue
			}
			for _, checkpoint := range candidate.Checkpoints {
				if checkpoint.Feature == record.Name && checkpoint.Commit == row.Head && (latest == nil || latest.CreatedAt.Before(candidate.CreatedAt)) {
					latest = candidate
				}
			}
		}
		if latest != nil {
			if latest.Status == "conflicts" {
				row.Blockers = append(row.Blockers, "conflicts in candidate "+latest.ID)
			}
			if latest.Status == "validated" && latest.Base == main && len(row.Dirty) == 0 {
				row.Validation = "passed_candidate"
			}
		}
		if row.Landing != "fully_landed" {
			var development DevelopmentEvidence
			path := filepath.Join(m.State, "development", record.Name+".json")
			if err := readJSON(path, &development); err == nil {
				input, inputErr := workspaceRevision(ctx, record.Path)
				if inputErr != nil {
					return result, inputErr
				}
				if input == development.Revision {
					row.Validation = "development_failed"
					if development.Passed {
						row.Validation = "development_passed"
					}
				}
			} else if !os.IsNotExist(err) {
				return result, err
			}
		}
		if record.Closed && row.Landing != "fully_landed" {
			row.Blockers = append(row.Blockers, "closed feature acquired outstanding work; reopen with --stage working")
		}
		result.Features = append(result.Features, row)
	}
	finishOverview(&result)
	return result, nil
}

func finishOverview(overview *Overview) {
	for i := range overview.Features {
		row := &overview.Features[i]
		if row.Closed && len(row.Blockers) == 0 {
			row.Status = "closed"
			continue
		}
		for _, dependency := range row.Dependencies {
			found := false
			for _, other := range overview.Features {
				if other.Name == dependency {
					found = true
					if other.Landing != "fully_landed" || len(other.Blockers) > 0 {
						row.Blockers = append(row.Blockers, "dependency "+dependency+" is not fully landed")
					}
				}
			}
			if !found {
				row.Blockers = append(row.Blockers, "dependency "+dependency+" is unavailable")
			}
		}
		for j := range overview.Features {
			other := &overview.Features[j]
			if i == j || other.Closed {
				continue
			}
			if paths := intersection(row.Outstanding, other.Outstanding); len(paths) > 0 {
				row.Overlap = append(row.Overlap, Overlap{Feature: other.Name, Paths: paths})
			}
		}
		row.Status = rowStatus(*row)
	}
}

func rowStatus(row Row) string {
	switch {
	case row.Closed && len(row.Blockers) == 0:
		return "closed"
	case row.Stage == "parked":
		return "parked"
	case len(row.Blockers) > 0:
		return "blocked"
	case row.Landing == "fully_landed":
		return "fully_landed"
	case row.Landing == "partially_landed":
		return "partially_landed"
	case row.Stage == "ready":
		return "ready"
	default:
		return "working"
	}
}

// Close records a completed feature. It never removes its checkout or data.
func (m *Manager) Close(ctx context.Context, name string) (Record, error) {
	lock, err := m.metadataLock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = lock.Close() }()
	record, err := m.record(name)
	if err != nil {
		return record, err
	}
	if record.Closed {
		return record, nil
	}
	landings, err := m.landings()
	if err != nil {
		return record, err
	}
	head, err := git(ctx, record.Path, "rev-parse", "HEAD")
	if err != nil {
		return record, err
	}
	dirty, err := dirtyPaths(ctx, record.Path)
	if err != nil {
		return record, err
	}
	main, err := git(ctx, m.MainRoot, "rev-parse", "main")
	if err != nil {
		return record, err
	}
	covered := false
	for _, landing := range landings {
		for _, checkpoint := range landing.Checkpoints {
			if checkpoint.Feature == name && checkpoint.Commit == head && ancestor(ctx, m.MainRoot, landing.Tip, main) {
				covered = true
			}
		}
	}
	if !covered || len(dirty) > 0 {
		return record, errors.New("feature still has unrecorded or outstanding work; close requires an exact published receipt and a clean checkpoint")
	}
	if err := verifyCandidateTree(ctx, record.Path); err != nil {
		return record, err
	}
	record.Closed = true
	path, _ := m.recordPath(name)
	return record, writeJSON(path, record, false)
}
