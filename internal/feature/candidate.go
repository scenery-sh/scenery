package feature

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Prepare captures feature commits and constructs an isolated integration candidate.
func (m *Manager) Prepare(ctx context.Context, names []string, commit string) (Candidate, error) {
	if len(names) == 0 || (commit != "" && len(names) != 1) {
		return Candidate{}, errors.New("prepare requires features; --commit selects one feature checkpoint")
	}
	if len(union(names)) != len(names) {
		return Candidate{}, errors.New("a feature may appear only once in a candidate")
	}
	if _, err := ReadPolicy(m.MainRoot); err != nil {
		return Candidate{}, err
	}
	remote, err := m.originTarget(ctx)
	if err != nil {
		return Candidate{}, err
	}
	if _, err := git(ctx, m.MainRoot, "fetch", "--no-tags", "--", remote, "refs/heads/main:refs/remotes/origin/main"); err != nil {
		return Candidate{}, err
	}
	base, err := git(ctx, m.MainRoot, "rev-parse", "refs/remotes/origin/main")
	if err != nil {
		return Candidate{}, err
	}
	main, err := git(ctx, m.MainRoot, "rev-parse", "main")
	if err != nil {
		return Candidate{}, err
	}
	if !ancestor(ctx, m.MainRoot, main, base) {
		return Candidate{}, errors.New("local main has unpublished/divergent commits; resolve its publication before preparing features")
	}
	overview, err := m.List(ctx)
	if err != nil {
		return Candidate{}, err
	}
	id, err := uniqueID()
	if err != nil {
		return Candidate{}, err
	}
	c := Candidate{Version: 1, ID: id, Path: filepath.Join(m.State, "worktrees", id), Base: base, Remote: digest(remote), Status: "prepared", Checkpoints: []Checkpoint{}, Conflicts: []string{}, Changed: []string{}, Checks: []Check{}, Receipts: []CheckReceipt{}, CreatedAt: time.Now().UTC()}
	selected := []string{}
	for _, name := range names {
		record, err := m.record(name)
		if err != nil {
			return c, err
		}
		if record.Closed || record.Stage == "parked" {
			return c, fmt.Errorf("feature %s is closed or parked", name)
		}
		for _, dependency := range record.Dependencies {
			if slices.Contains(selected, dependency) {
				continue
			}
			complete := false
			for _, row := range overview.Features {
				if row.Name == dependency && row.Landing == "fully_landed" && len(row.Blockers) == 0 {
					complete = true
				}
			}
			if !complete {
				return c, fmt.Errorf("feature %s requires %s to land first (or earlier in this candidate)", name, dependency)
			}
		}
		checkpoint, err := git(ctx, record.Path, "rev-parse", "HEAD")
		if err != nil {
			return c, err
		}
		if commit != "" {
			checkpoint, err = git(ctx, record.Path, "rev-parse", "--verify", commit+"^{commit}")
			if err != nil {
				return c, err
			}
			if !ancestor(ctx, record.Path, checkpoint, "HEAD") {
				return c, errors.New("checkpoint must belong to this feature's current history")
			}
		}
		if ancestor(ctx, m.MainRoot, checkpoint, base) {
			return c, fmt.Errorf("feature %s checkpoint already belongs to main; no new checkpoint to land", name)
		}
		c.Checkpoints = append(c.Checkpoints, Checkpoint{Feature: name, Commit: checkpoint})
		selected = append(selected, name)
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return c, err
	}
	if _, err := git(ctx, m.MainRoot, "worktree", "add", "--detach", c.Path, base); err != nil {
		return c, err
	}
	if err := m.saveCandidate(c); err != nil {
		return c, err
	}
	owner, err := tryLock(filepath.Join(m.State, "candidate-locks", id+".lock"))
	if err != nil {
		return c, err
	}
	defer func() { _ = owner.Close() }()
	return m.finishCandidate(ctx, c)
}

// Inspect resumes a resolved merge and returns the current reviewable revision.
func (m *Manager) Inspect(ctx context.Context, id string) (Candidate, error) {
	if _, err := m.candidatePath(id); err != nil {
		return Candidate{}, err
	}
	lock, err := tryLock(filepath.Join(m.State, "candidate-locks", id+".lock"))
	if err != nil {
		return Candidate{}, fmt.Errorf("candidate is busy: %w", err)
	}
	defer func() { _ = lock.Close() }()
	c, err := m.loadCandidate(id)
	if err != nil {
		return c, err
	}
	if c.Status == "published" {
		return c, nil
	}
	return m.finishCandidate(ctx, c)
}

func (m *Manager) finishCandidate(ctx context.Context, c Candidate) (Candidate, error) {
	if c.Next < len(c.Checkpoints) {
		conflicts, err := git(ctx, c.Path, "diff", "--name-only", "--diff-filter=U", "-z")
		if err != nil {
			return c, err
		}
		c.Conflicts = pathsFromGit(conflicts)
		if len(c.Conflicts) > 0 {
			c.Status = "conflicts"
			c.Diff, _ = git(ctx, c.Path, "diff", c.Base, "--")
			c.Changed, _ = changedPaths(ctx, c.Path, c.Base, "HEAD")
			return c, m.saveCandidate(c)
		}
		mergeHead, mergeErr := git(ctx, c.Path, "rev-parse", "--verify", "MERGE_HEAD")
		if mergeErr == nil {
			if mergeHead != c.Checkpoints[c.Next].Commit {
				return c, errors.New("candidate merge does not match its captured checkpoint")
			}
			if _, err := git(ctx, c.Path, "diff", "--quiet"); err != nil {
				return c, errors.New("stage the resolved candidate files before inspecting")
			}
			if _, err := git(ctx, c.Path, "commit", "--no-gpg-sign", "-m", "Land feature "+c.Checkpoints[c.Next].Feature); err != nil {
				return c, err
			}
			c.Checkpoints[c.Next].IntegratedCommit, _ = git(ctx, c.Path, "rev-parse", "HEAD")
			c.Next++
		} else {
			// Recover a merge committed before its record was saved, including a
			// manually committed conflict resolution. Parents bind the checkpoint.
			previous := c.Base
			if c.Next > 0 {
				previous = c.Checkpoints[c.Next-1].IntegratedCommit
			}
			parents, parentErr := git(ctx, c.Path, "show", "-s", "--format=%P", "HEAD")
			fields := strings.Fields(parents)
			if parentErr == nil && len(fields) == 2 && fields[0] == previous && fields[1] == c.Checkpoints[c.Next].Commit {
				c.Checkpoints[c.Next].IntegratedCommit, _ = git(ctx, c.Path, "rev-parse", "HEAD")
				c.Next++
				if err := m.saveCandidate(c); err != nil {
					return c, err
				}
			}
		}
		for c.Next < len(c.Checkpoints) {
			checkpoint := c.Checkpoints[c.Next]
			if ancestor(ctx, c.Path, checkpoint.Commit, "HEAD") {
				c.Checkpoints[c.Next].IntegratedCommit, _ = git(ctx, c.Path, "rev-parse", "HEAD")
				c.Next++
				if err := m.saveCandidate(c); err != nil {
					return c, err
				}
				continue
			}
			if _, err := git(ctx, c.Path, "merge", "--no-ff", "--no-commit", checkpoint.Commit); err != nil {
				conflicts, conflictErr := git(ctx, c.Path, "diff", "--name-only", "--diff-filter=U", "-z")
				if conflictErr != nil {
					return c, conflictErr
				}
				c.Conflicts = pathsFromGit(conflicts)
				if len(c.Conflicts) == 0 {
					return c, err
				}
				c.Status = "conflicts"
				c.Diff, _ = git(ctx, c.Path, "diff", c.Base, "--")
				c.Changed = union(c.Changed, c.Conflicts)
				return c, m.saveCandidate(c)
			}
			if _, err := git(ctx, c.Path, "commit", "--no-gpg-sign", "-m", "Land feature "+checkpoint.Feature); err != nil {
				return c, err
			}
			c.Checkpoints[c.Next].IntegratedCommit, err = git(ctx, c.Path, "rev-parse", "HEAD")
			if err != nil {
				return c, err
			}
			c.Next++
			if err := m.saveCandidate(c); err != nil {
				return c, err
			}
		}
	}
	dirty, err := dirtyPaths(ctx, c.Path)
	if err != nil {
		return c, err
	}
	if len(dirty) > 0 {
		return c, fmt.Errorf("candidate has outstanding edits: %s; commit reviewed resolution/check corrections before inspection", strings.Join(dirty, ", "))
	}
	c.Tip, err = git(ctx, c.Path, "rev-parse", "HEAD")
	if err != nil {
		return c, err
	}
	c.Tree, err = git(ctx, c.Path, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return c, err
	}
	if !ancestor(ctx, c.Path, c.Base, c.Tip) {
		return c, errors.New("candidate no longer extends its captured main")
	}
	for _, checkpoint := range c.Checkpoints {
		if checkpoint.IntegratedCommit == "" || !ancestor(ctx, c.Path, checkpoint.IntegratedCommit, c.Tip) || !ancestor(ctx, c.Path, checkpoint.Commit, checkpoint.IntegratedCommit) {
			return c, errors.New("candidate lost a recorded feature merge")
		}
	}
	c.Changed, err = changedPaths(ctx, c.Path, c.Base, c.Tip)
	if err != nil {
		return c, err
	}
	c.Diff, err = git(ctx, c.Path, "diff", c.Base, c.Tip, "--")
	if err != nil {
		return c, err
	}
	basePolicyText, err := git(ctx, c.Path, "show", c.Base+":"+PolicyFile)
	if err != nil {
		return c, err
	}
	var basePolicy Policy
	if err := decodeJSON([]byte(basePolicyText), &basePolicy); err != nil {
		return c, err
	}
	policy, err := ReadPolicy(c.Path)
	if err != nil {
		return c, err
	}
	c.Checks = selectChecks(checkUnion(basePolicy, policy), c.Changed)
	if len(c.Checks) == 0 {
		return c, errors.New("combined candidate has no required landing checks")
	}
	oldRevision := c.Revision
	c.Revision = candidateRevision(c)
	if oldRevision != c.Revision {
		c.Status = "prepared"
		c.Receipts = []CheckReceipt{}
	}
	c.Conflicts = []string{}
	if _, err := git(ctx, m.MainRoot, "update-ref", "refs/scenery/features/candidates/"+c.ID, c.Tip); err != nil {
		return c, err
	}
	return c, m.saveCandidate(c)
}

func candidateRevision(c Candidate) string {
	return digest(struct {
		Base, Tip, Tree, Remote string
		Checkpoints             []Checkpoint
		Checks                  []Check
	}{c.Base, c.Tip, c.Tree, c.Remote, c.Checkpoints, c.Checks})
}

func (m *Manager) originTarget(ctx context.Context) (string, error) {
	remote, err := git(ctx, m.MainRoot, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return "", err
	}
	if remote == "" || strings.ContainsAny(remote, "\r\n") {
		return "", errors.New("origin must have exactly one publication URL")
	}
	return remote, nil
}

func (m *Manager) remoteMain(ctx context.Context, c Candidate) (string, error) {
	remote, err := m.originTarget(ctx)
	if err != nil {
		return "", err
	}
	if digest(remote) != c.Remote {
		return "", errors.New("origin publication destination changed since candidate preparation")
	}
	output, err := git(ctx, m.MainRoot, "ls-remote", "--", remote, "refs/heads/main")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[1] != "refs/heads/main" {
		return "", errors.New("origin must expose exactly one main ref")
	}
	return fields[0], nil
}

// Land validates an approved candidate and publishes only a main fast-forward.
func (m *Manager) Land(ctx context.Context, id, expected string) (Candidate, error) {
	if _, err := m.candidatePath(id); err != nil {
		return Candidate{}, err
	}
	publication, err := m.publicationLock()
	if err != nil {
		return Candidate{}, err
	}
	defer func() { _ = publication.Close() }()
	owner, err := tryLock(filepath.Join(m.State, "candidate-locks", id+".lock"))
	if err != nil {
		return Candidate{}, err
	}
	defer func() { _ = owner.Close() }()
	c, err := m.loadCandidate(id)
	if err != nil {
		return c, err
	}
	if c.Status == "published" {
		if c.Revision != expected {
			return c, errors.New("candidate approval revision does not match")
		}
		return c, nil
	}
	c, err = m.finishCandidate(ctx, c)
	if err != nil {
		return c, err
	}
	if c.Revision == "" || c.Revision != expected || len(c.Conflicts) > 0 {
		return c, errors.New("candidate approval is stale or has unresolved conflicts; inspect and review its current revision")
	}
	remote, err := m.remoteMain(ctx, c)
	if err != nil {
		return c, err
	}
	main, err := git(ctx, m.MainRoot, "rev-parse", "main")
	if err != nil {
		return c, err
	}
	if remote == c.Tip && c.Status == "publishing" {
		if !ancestor(ctx, m.MainRoot, c.Tip, main) {
			return c, errors.New("published candidate is absent from local main; inspect its ownership before recovery")
		}
		return m.completeLanding(ctx, c)
	}
	if c.Status == "publishing" && remote != c.Base {
		target, targetErr := m.originTarget(ctx)
		if targetErr != nil {
			return c, targetErr
		}
		if _, err := git(ctx, m.MainRoot, "fetch", "--no-tags", "--", target, "refs/heads/main:refs/remotes/origin/main"); err != nil {
			return c, err
		}
		if ancestor(ctx, m.MainRoot, c.Tip, remote) && ancestor(ctx, m.MainRoot, c.Tip, main) {
			return m.completeLanding(ctx, c)
		}
	}
	if remote != c.Base {
		return c, errors.New("origin/main advanced; prepare a new candidate and validate its combined inputs")
	}
	if main != c.Tip && !ancestor(ctx, m.MainRoot, main, c.Base) {
		return c, errors.New("local main diverged; publication cannot discard its work")
	}
	c, err = m.validateCandidate(ctx, c)
	if err != nil {
		return c, err
	}
	remote, err = m.remoteMain(ctx, c)
	if err != nil {
		return c, err
	}
	if remote != c.Base {
		return c, errors.New("origin/main changed during validation; candidate must be prepared again")
	}
	c.Status = "publishing"
	if err := m.saveCandidate(c); err != nil {
		return c, err
	}
	if main != c.Tip {
		branch, err := git(ctx, m.MainRoot, "symbolic-ref", "HEAD")
		if err != nil || branch != "refs/heads/main" {
			return c, errors.New("the integration checkout no longer owns main")
		}
		changed, err := changedPaths(ctx, m.MainRoot, main, c.Tip)
		if err != nil {
			return c, err
		}
		dirty, err := dirtyPaths(ctx, m.MainRoot)
		if err != nil {
			return c, err
		}
		if paths := intersection(changed, dirty); len(paths) > 0 {
			return c, fmt.Errorf("main has overlapping outstanding work: %s", strings.Join(paths, ", "))
		}
		if _, err := git(ctx, m.MainRoot, "merge", "--ff-only", c.Tip); err != nil {
			return c, err
		}
	}
	target, err := m.originTarget(ctx)
	if err != nil || digest(target) != c.Remote {
		return c, errors.New("origin publication destination changed before push")
	}
	if _, err := git(ctx, m.MainRoot, "push", "--porcelain", "--", target, c.Tip+":refs/heads/main"); err != nil {
		return c, fmt.Errorf("validated main remains local; retry this candidate after resolving the push failure: %w", err)
	}
	remote, err = m.remoteMain(ctx, c)
	if err != nil {
		return c, err
	}
	if remote != c.Tip {
		return c, errors.New("remote main did not confirm the candidate; retain it and inspect publication before retrying")
	}
	if _, err := git(ctx, m.MainRoot, "update-ref", "refs/remotes/origin/main", c.Tip); err != nil {
		return c, err
	}
	return m.completeLanding(ctx, c)
}

func (m *Manager) completeLanding(ctx context.Context, c Candidate) (Candidate, error) {
	lock, err := m.metadataLock(ctx)
	if err != nil {
		return c, err
	}
	defer func() { _ = lock.Close() }()
	now := time.Now().UTC()
	c.Status = "published"
	c.PublishedAt = &now
	path := filepath.Join(m.State, "landings", c.ID+".json")
	if err := writeJSON(path, c, true); err != nil {
		if !os.IsExist(err) {
			return c, err
		}
		var existing Candidate
		if err := readJSON(path, &existing); err != nil {
			return c, err
		}
		if existing.Revision != c.Revision || existing.Tip != c.Tip {
			return c, errors.New("landing receipt already exists for different inputs")
		}
		c = existing
	}
	return c, m.saveCandidate(c)
}
