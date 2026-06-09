package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/envpolicy"
	"github.com/pbrazdil/onlava/internal/neonselfhost"
)

const (
	neonSelfhostBranchDriverEnv             = "ONLAVA_DEV_NEON_SELFHOST_DRIVER"
	localPostgresBranchDriverEndpointSource = "local-postgres-branch-driver"
	neonSelfhostBranchDriverEndpointSource  = "neon-selfhost-driver"
)

type executableNeonBranchDriverResult struct {
	Status       string                  `json:"status,omitempty"`
	Message      string                  `json:"message,omitempty"`
	Diff         string                  `json:"diff,omitempty"`
	Endpoint     *neonEndpoint           `json:"endpoint,omitempty"`
	RestorePoint *neonBranchRestorePoint `json:"restore_point,omitempty"`
}

type neonBranchDriver interface {
	EnsureBranch(context.Context, worktreeDBPin) (neonBranchBackendStatus, error)
	ResetBranch(context.Context, worktreeDBPin) error
	RestoreBranch(context.Context, worktreeDBPin, string) (neonBranchRestorePoint, error)
	DiffBranch(context.Context, worktreeDBPin, string) (string, error)
	DeleteBranch(context.Context, worktreeDBPin) error
}

type neonBranchDriverMetadata struct {
	name                  string
	defaultEndpointSource string
}

type executableNeonBranchDriver struct {
	path string
	meta neonBranchDriverMetadata
}

type builtinNeonSelfhostBranchDriver struct {
	meta neonBranchDriverMetadata
}

type neonBranchActionRunner func(context.Context, string, worktreeDBPin, []string) (executableNeonBranchDriverResult, error)

func configuredNeonBranchDriver() (neonBranchDriver, bool, error) {
	if driver, ok, err := configuredNeonSelfhostBranchDriver(); ok || err != nil {
		return driver, ok, err
	}
	if driver, ok, err := configuredManagedNeonSelfhostBranchDriver(); ok || err != nil {
		return driver, ok, err
	}
	return configuredLocalPostgresBranchDriver()
}

func configuredNeonSelfhostBranchDriver() (neonBranchDriver, bool, error) {
	return configuredExecutableNeonBranchDriver(neonSelfhostBranchDriverEnv, "neon-selfhost driver", neonSelfhostBranchDriverEndpointSource)
}

func configuredLocalPostgresBranchDriver() (neonBranchDriver, bool, error) {
	return configuredExecutableNeonBranchDriver(localPostgresBranchDriverEnv, "local-postgres-branch driver", localPostgresBranchDriverEndpointSource)
}

func configuredExecutableNeonBranchDriver(envName, name, defaultEndpointSource string) (neonBranchDriver, bool, error) {
	path := strings.TrimSpace(envpolicy.Get(envName))
	if path == "" {
		return executableNeonBranchDriver{}, false, nil
	}
	driver, err := executableNeonBranchDriverFromPath(path, name, envName, defaultEndpointSource)
	return driver, err == nil, err
}

func newBuiltinNeonSelfhostBranchDriver() builtinNeonSelfhostBranchDriver {
	return builtinNeonSelfhostBranchDriver{
		meta: neonBranchDriverMetadata{
			name:                  "built-in neon-selfhost driver",
			defaultEndpointSource: neonSelfhostBranchDriverEndpointSource,
		},
	}
}

func executableNeonBranchDriverFromPath(path, name, sourceName, defaultEndpointSource string) (executableNeonBranchDriver, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return executableNeonBranchDriver{}, fmt.Errorf("%s is empty", sourceName)
	}
	if !filepath.IsAbs(path) {
		return executableNeonBranchDriver{}, fmt.Errorf("%s must be an absolute path to a %s executable", sourceName, name)
	}
	info, err := os.Stat(path)
	if err != nil {
		return executableNeonBranchDriver{}, fmt.Errorf("stat %s: %w", sourceName, err)
	}
	if info.IsDir() {
		return executableNeonBranchDriver{}, fmt.Errorf("%s points at a directory, want executable file", sourceName)
	}
	if info.Mode()&0o111 == 0 {
		return executableNeonBranchDriver{}, fmt.Errorf("%s is not executable", sourceName)
	}
	return executableNeonBranchDriver{
		path: path,
		meta: neonBranchDriverMetadata{
			name:                  name,
			defaultEndpointSource: defaultEndpointSource,
		},
	}, nil
}

func (d executableNeonBranchDriver) EnsureBranch(ctx context.Context, pin worktreeDBPin) (neonBranchBackendStatus, error) {
	return ensureNeonBranchWithDriver(ctx, pin, d.meta, d.run)
}

func (d executableNeonBranchDriver) ResetBranch(ctx context.Context, pin worktreeDBPin) error {
	return resetNeonBranchWithDriver(ctx, pin, d.meta, d.run)
}

func (d executableNeonBranchDriver) RestoreBranch(ctx context.Context, pin worktreeDBPin, at string) (neonBranchRestorePoint, error) {
	return restoreNeonBranchWithDriver(ctx, pin, at, d.meta, d.run)
}

func (d executableNeonBranchDriver) DiffBranch(ctx context.Context, pin worktreeDBPin, target string) (string, error) {
	return diffNeonBranchWithDriver(ctx, pin, target, d.run)
}

func (d executableNeonBranchDriver) DeleteBranch(ctx context.Context, pin worktreeDBPin) error {
	return deleteNeonBranchWithDriver(ctx, pin, d.run)
}

func (d executableNeonBranchDriver) run(ctx context.Context, action string, pin worktreeDBPin, extra []string) (executableNeonBranchDriverResult, error) {
	if strings.TrimSpace(action) == "" {
		return executableNeonBranchDriverResult{}, fmt.Errorf("%s action is required", d.meta.name)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := neonBranchDriverArgs(action, pin, extra)
	cmd := exec.CommandContext(ctx, d.path, args...)
	cmd.Env = envpolicy.Environ()
	if root, err := neonSubstrateRoot(); err == nil {
		cmd.Env = append(cmd.Env, "ONLAVA_NEON_SELFHOST_ROOT="+root)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return executableNeonBranchDriverResult{}, fmt.Errorf("%s %q timed out", d.meta.name, action)
		}
		return executableNeonBranchDriverResult{}, fmt.Errorf("%s %q failed: %w: %s", d.meta.name, action, err, strings.TrimSpace(string(out)))
	}
	return decodeNeonBranchDriverResult(d.meta, action, out)
}

func (d builtinNeonSelfhostBranchDriver) EnsureBranch(ctx context.Context, pin worktreeDBPin) (neonBranchBackendStatus, error) {
	return ensureNeonBranchWithDriver(ctx, pin, d.meta, d.run)
}

func (d builtinNeonSelfhostBranchDriver) ResetBranch(ctx context.Context, pin worktreeDBPin) error {
	return resetNeonBranchWithDriver(ctx, pin, d.meta, d.run)
}

func (d builtinNeonSelfhostBranchDriver) RestoreBranch(ctx context.Context, pin worktreeDBPin, at string) (neonBranchRestorePoint, error) {
	return restoreNeonBranchWithDriver(ctx, pin, at, d.meta, d.run)
}

func (d builtinNeonSelfhostBranchDriver) DiffBranch(ctx context.Context, pin worktreeDBPin, target string) (string, error) {
	return diffNeonBranchWithDriver(ctx, pin, target, d.run)
}

func (d builtinNeonSelfhostBranchDriver) DeleteBranch(ctx context.Context, pin worktreeDBPin) error {
	return deleteNeonBranchWithDriver(ctx, pin, d.run)
}

func (d builtinNeonSelfhostBranchDriver) run(ctx context.Context, action string, pin worktreeDBPin, extra []string) (executableNeonBranchDriverResult, error) {
	if strings.TrimSpace(action) == "" {
		return executableNeonBranchDriverResult{}, fmt.Errorf("%s action is required", d.meta.name)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := neonBranchDriverArgs(action, pin, extra)
	if root, err := neonSubstrateRoot(); err == nil {
		args = append(args[:len(args)-1], "--root", root, args[len(args)-1])
	}
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- neonselfhost.Run(&stdout, &stderr, args)
	}()
	select {
	case err := <-done:
		if err != nil {
			return executableNeonBranchDriverResult{}, fmt.Errorf("%s %q failed: %w: %s", d.meta.name, action, err, strings.TrimSpace(stderr.String()))
		}
	case <-ctx.Done():
		return executableNeonBranchDriverResult{}, fmt.Errorf("%s %q timed out", d.meta.name, action)
	}
	return decodeNeonBranchDriverResult(d.meta, action, stdout.Bytes())
}

func neonBranchDriverArgs(action string, pin worktreeDBPin, extra []string) []string {
	args := []string{
		action,
		"--project", pin.Project,
		"--parent-branch", pin.ParentBranch,
		"--branch", pin.Branch,
		"--branch-id", pin.BranchID,
		"--database", pin.Database,
		"--role", pin.Role,
	}
	if strings.TrimSpace(pin.TTL) != "" {
		args = append(args, "--ttl", strings.TrimSpace(pin.TTL))
	}
	args = append(args, extra...)
	args = append(args, "--json")
	return args
}

func decodeNeonBranchDriverResult(meta neonBranchDriverMetadata, action string, out []byte) (executableNeonBranchDriverResult, error) {
	var result executableNeonBranchDriverResult
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return executableNeonBranchDriverResult{}, fmt.Errorf("parse %s %q JSON: %w", meta.name, action, err)
	}
	return result, nil
}

func ensureNeonBranchWithDriver(ctx context.Context, pin worktreeDBPin, meta neonBranchDriverMetadata, run neonBranchActionRunner) (neonBranchBackendStatus, error) {
	result, err := run(ctx, "ensure", pin, nil)
	if err != nil {
		return neonBranchBackendStatus{Status: "unknown", Message: err.Error()}, err
	}
	status, err := updateNeonBranchLeaseFromDriver(pin, result, meta)
	if err != nil {
		return status, err
	}
	if status.Status == "ready" {
		if err := ensureInitialNeonRestorePoint(pin); err != nil {
			return neonBranchBackendStatus{Status: "unknown", Message: err.Error()}, err
		}
	}
	return status, nil
}

func resetNeonBranchWithDriver(ctx context.Context, pin worktreeDBPin, meta neonBranchDriverMetadata, run neonBranchActionRunner) error {
	result, err := run(ctx, "reset", pin, nil)
	if err != nil {
		return err
	}
	if _, err := updateNeonBranchLeaseFromDriver(pin, result, meta); err != nil {
		return err
	}
	_, err = recordNeonRestorePoint(pin, "branch-reset", "")
	return err
}

func restoreNeonBranchWithDriver(ctx context.Context, pin worktreeDBPin, at string, meta neonBranchDriverMetadata, run neonBranchActionRunner) (neonBranchRestorePoint, error) {
	result, err := run(ctx, "restore", pin, []string{"--at", strings.TrimSpace(at)})
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	if _, err := updateNeonBranchLeaseFromDriver(pin, result, meta); err != nil {
		return neonBranchRestorePoint{}, err
	}
	if result.RestorePoint != nil {
		return *result.RestorePoint, nil
	}
	return recordNeonRestorePoint(pin, "branch-restore", at)
}

func diffNeonBranchWithDriver(ctx context.Context, pin worktreeDBPin, target string, run neonBranchActionRunner) (string, error) {
	result, err := run(ctx, "diff", pin, []string{"--target", strings.TrimSpace(target)})
	if err != nil {
		return "", err
	}
	return result.Diff, nil
}

func deleteNeonBranchWithDriver(ctx context.Context, pin worktreeDBPin, run neonBranchActionRunner) error {
	_, err := run(ctx, "delete", pin, nil)
	return err
}

func updateNeonBranchLeaseFromDriver(pin worktreeDBPin, result executableNeonBranchDriverResult, driver neonBranchDriverMetadata) (neonBranchBackendStatus, error) {
	status := strings.ToLower(strings.TrimSpace(result.Status))
	if status == "" {
		if result.Endpoint != nil {
			status = "ready"
		} else {
			status = "pending"
		}
	}
	switch status {
	case "ready":
		if result.Endpoint == nil {
			return neonBranchBackendStatus{}, fmt.Errorf("%s marked %q ready without endpoint metadata", driver.name, pin.Branch)
		}
	case "pending", "missing", "expired":
	default:
		return neonBranchBackendStatus{}, fmt.Errorf("%s returned unsupported status %q for %q", driver.name, status, pin.Branch)
	}

	root, err := neonSubstrateRoot()
	if err != nil {
		return neonBranchBackendStatus{}, err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return neonBranchBackendStatus{}, err
	}
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	var endpoint *neonEndpoint
	if result.Endpoint != nil && status == "ready" {
		normalized := normalizedNeonEndpoint(*result.Endpoint, pin)
		if normalized.Source == "" {
			normalized.Source = driver.defaultEndpointSource
		}
		endpoint = &normalized
	}
	for i := range registry.Leases {
		if !sameNeonLease(registry.Leases[i].Pin, pin) && !sameNeonBranch(registry.Leases[i].Pin, pin) {
			continue
		}
		if !isOnlavaOwnedNeonLease(registry.Leases[i]) {
			return neonBranchBackendStatus{}, fmt.Errorf("refusing to update foreign local Neon branch lease %q from %s", pin.Branch, driver.name)
		}
		if registry.Leases[i].CreatedAt == "" {
			registry.Leases[i].CreatedAt = nowText
		}
		registry.Leases[i].Pin = pin
		registry.Leases[i].Status = status
		registry.Leases[i].Endpoint = endpoint
		registry.Leases[i].UpdatedAt = nowText
		registry.UpdatedAt = nowText
		if err := writeNeonBranchRegistry(root, registry); err != nil {
			return neonBranchBackendStatus{}, err
		}
		return neonBranchBackendStatus{
			Status:   status,
			Message:  firstNonEmpty(strings.TrimSpace(result.Message), driver.name+" updated the local branch lease."),
			Endpoint: endpoint,
		}, nil
	}
	registry.Leases = append(registry.Leases, neonBranchLease{
		Pin:       pin,
		Status:    status,
		Endpoint:  endpoint,
		CreatedAt: nowText,
		UpdatedAt: nowText,
		ExpiresAt: neonLeaseExpiresAt(now, pin.TTL),
	})
	registry.UpdatedAt = nowText
	if err := writeNeonBranchRegistry(root, registry); err != nil {
		return neonBranchBackendStatus{}, err
	}
	return neonBranchBackendStatus{
		Status:   status,
		Message:  firstNonEmpty(strings.TrimSpace(result.Message), driver.name+" created the local branch lease."),
		Endpoint: endpoint,
	}, nil
}
