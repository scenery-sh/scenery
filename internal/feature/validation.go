package feature

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func workspaceRevision(ctx context.Context, root string) (string, error) {
	files, err := git(ctx, root, "ls-files", "-c", "-o", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, name := range pathsFromGit(files) {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "%s\x00deleted\x00", name)
			continue
		}
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", name, info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(hash, target)
		} else if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("unsupported authored input %s", name)
		}
		_, _ = hash.Write([]byte{0})
	}
	head, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	index, err := git(ctx, root, "diff", "--cached", "--binary")
	if err != nil {
		return "", err
	}
	_, _ = io.WriteString(hash, head+"\x00"+index)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// CheckDevelopment runs only the declared focused feedback for the current feature.
func (m *Manager) CheckDevelopment(ctx context.Context, name string) (receipts []CheckReceipt, resultErr error) {
	record, err := m.record(name)
	if err != nil {
		return nil, err
	}
	policy, err := ReadPolicy(record.Path)
	if err != nil {
		return nil, err
	}
	head, err := git(ctx, record.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	paths, err := changedPaths(ctx, record.Path, record.Base, head)
	if err != nil {
		return nil, err
	}
	dirty, err := dirtyPaths(ctx, record.Path)
	if err != nil {
		return nil, err
	}
	checks := selectChecks(policy.Development, union(paths, dirty))
	if len(checks) == 0 {
		return nil, errors.New("no development checks apply; select focused owner checks before readiness")
	}
	revision, err := workspaceRevision(ctx, record.Path)
	if err != nil {
		return nil, err
	}
	receipts = []CheckReceipt{}
	defer func() {
		path := filepath.Join(m.State, "development", name+".json")
		resultErr = errors.Join(resultErr, writeJSON(path, DevelopmentEvidence{Revision: revision, Passed: resultErr == nil, Receipts: receipts}, false))
	}()
	for _, check := range checks {
		receipt, err := m.executeCheck(ctx, record.Path, record.Base, revision, check, policy.ProbeLimit, nil)
		if receipt.ID != "" {
			receipts = append(receipts, receipt)
		}
		if err != nil {
			return receipts, err
		}
	}
	return receipts, nil
}

func (m *Manager) CheckCandidate(ctx context.Context, id, expected string) (Candidate, error) {
	if _, err := m.candidatePath(id); err != nil {
		return Candidate{}, err
	}
	owner, err := tryLock(filepath.Join(m.State, "candidate-locks", id+".lock"))
	if err != nil {
		return Candidate{}, err
	}
	defer func() { _ = owner.Close() }()
	c, err := m.loadCandidate(id)
	if err != nil {
		return c, err
	}
	c, err = m.finishCandidate(ctx, c)
	if err != nil {
		return c, err
	}
	if c.Revision == "" || c.Revision != expected || len(c.Conflicts) > 0 {
		return c, errors.New("validation requires the reviewed current candidate revision")
	}
	return m.validateCandidate(ctx, c)
}

func (m *Manager) validateCandidate(ctx context.Context, c Candidate) (Candidate, error) {
	if err := verifyApprovedCandidate(ctx, c); err != nil {
		return c, err
	}
	policy, err := ReadPolicy(c.Path)
	if err != nil {
		return c, err
	}
	input, err := workspaceRevision(ctx, c.Path)
	if err != nil {
		return c, err
	}
	previous := c.Receipts
	c.Receipts = []CheckReceipt{}
	for _, check := range c.Checks {
		receipt, err := m.executeCheck(ctx, c.Path, c.Base, input, check, policy.ProbeLimit, previous)
		if receipt.ID != "" {
			c.Receipts = append(c.Receipts, receipt)
		}
		if err != nil {
			c.Status = "validation_failed"
			if saveErr := m.saveCandidate(c); saveErr != nil {
				return c, errors.Join(err, saveErr)
			}
			return c, err
		}
		if err := m.saveCandidate(c); err != nil {
			return c, err
		}
	}
	if err := verifyApprovedCandidate(ctx, c); err != nil {
		return c, err
	}
	c.Status = "validated"
	return c, m.saveCandidate(c)
}

func checkEnvironment(root string, args []string) (string, error) {
	command := args[0]
	if strings.ContainsAny(command, "/\\") {
		if !filepath.IsAbs(command) {
			command = filepath.Join(root, command)
		}
	} else {
		resolved, err := exec.LookPath(command)
		if err != nil {
			return "", err
		}
		command = resolved
	}
	file, err := os.Open(command)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return digest(struct{ Environment, Executable, Bytes string }{commandEnvironment(), command, hex.EncodeToString(hash.Sum(nil))}), nil
}

func reusableReceipt(previous []CheckReceipt, check Check, revision, environment, root string, args []string) *CheckReceipt {
	if !check.Reuse || check.Expensive {
		return nil
	}
	for _, receipt := range previous {
		if receipt.ID == check.ID && receipt.Revision == revision && receipt.Environment == environment && receipt.CWD == root && receipt.Outcome == "passed" && slices.Equal(receipt.Command, args) {
			var archived CheckReceipt
			if readJSON(filepath.Join(receipt.Archive, "receipt.json"), &archived) == nil && archived.Outcome == "passed" && archived.ExitCode == 0 && archived.ID == check.ID && archived.CWD == root && archived.Archive == receipt.Archive && archived.Revision == revision && archived.Environment == environment && slices.Equal(archived.Command, args) {
				receipt.Reused = true
				return &receipt
			}
		}
	}
	return nil
}

func (m *Manager) executeCheck(ctx context.Context, root, base, revision string, check Check, limit int, previous []CheckReceipt) (CheckReceipt, error) {
	args := checkArgs(check, base, revision)
	environment, err := checkEnvironment(root, args)
	if err != nil {
		return CheckReceipt{}, err
	}
	if receipt := reusableReceipt(previous, check, revision, environment, root, args); receipt != nil {
		return *receipt, nil
	}
	id, err := uniqueID()
	if err != nil {
		return CheckReceipt{}, err
	}
	archive := filepath.Join(m.State, "checks", id)
	if err := os.MkdirAll(archive, 0o700); err != nil {
		return CheckReceipt{}, err
	}
	receipt := CheckReceipt{ID: check.ID, Revision: revision, Environment: environment, Command: args, CWD: root, StartedAt: time.Now().UTC(), Outcome: "failed", ExitCode: -1, Archive: archive}
	var lease *os.File
	if check.Expensive {
		lease, err = m.probeLock(ctx, limit)
		if err != nil {
			return receipt, err
		}
		defer func() { _ = lease.Close() }()
	}
	stdout, err := os.OpenFile(filepath.Join(archive, "stdout.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := os.OpenFile(filepath.Join(archive, "stderr.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = stderr.Close() }()
	command := args[0]
	if strings.ContainsAny(command, "/\\") && !filepath.IsAbs(command) {
		command = filepath.Join(root, command)
	}
	cmd := exec.CommandContext(ctx, command, args[1:]...)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	if err := protectCommand(cmd, lease); err != nil {
		return receipt, err
	}
	started := time.Now()
	runErr := cmd.Run()
	receipt.DurationMS = time.Since(started).Milliseconds()
	if cmd.ProcessState != nil {
		receipt.ExitCode = cmd.ProcessState.ExitCode()
	}
	finalInput, inputErr := workspaceRevision(ctx, root)
	finalEnvironment, environmentErr := checkEnvironment(root, args)
	if runErr == nil && inputErr == nil && environmentErr == nil && revision == finalInput && environment == finalEnvironment {
		receipt.Outcome = "passed"
	}
	if err := writeJSON(filepath.Join(archive, "receipt.json"), receipt, true); err != nil {
		return receipt, err
	}
	if receipt.Outcome != "passed" {
		return receipt, fmt.Errorf("check %s did not pass with stable inputs (exit %d); inspect %s: %w", check.ID, receipt.ExitCode, archive, errors.Join(runErr, inputErr, environmentErr, errors.New("required check or input stability failed")))
	}
	return receipt, nil
}

// Bind validation to the exact commit and raw bytes shown for approval.
func verifyApprovedCandidate(ctx context.Context, c Candidate) error {
	head, err := git(ctx, c.Path, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != c.Tip {
		return errors.New("candidate HEAD changed after approval")
	}
	return verifyCandidateTree(ctx, c.Path)
}
