package main

import (
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
)

type neonBranchDriverResult struct {
	Status   string        `json:"status,omitempty"`
	Message  string        `json:"message,omitempty"`
	Endpoint *neonEndpoint `json:"endpoint,omitempty"`
}

type selfHostedNeonBranchDriver struct {
	path string
}

func configuredSelfHostedNeonBranchDriver() (selfHostedNeonBranchDriver, bool, error) {
	path := strings.TrimSpace(envpolicy.Get(neonBranchDriverEnv))
	if path == "" {
		return selfHostedNeonBranchDriver{}, false, nil
	}
	if !filepath.IsAbs(path) {
		return selfHostedNeonBranchDriver{}, false, fmt.Errorf("%s must be an absolute path to a local Neon branch driver executable", neonBranchDriverEnv)
	}
	info, err := os.Stat(path)
	if err != nil {
		return selfHostedNeonBranchDriver{}, false, fmt.Errorf("stat %s: %w", neonBranchDriverEnv, err)
	}
	if info.IsDir() {
		return selfHostedNeonBranchDriver{}, false, fmt.Errorf("%s points at a directory, want executable file", neonBranchDriverEnv)
	}
	if info.Mode()&0o111 == 0 {
		return selfHostedNeonBranchDriver{}, false, fmt.Errorf("%s is not executable", neonBranchDriverEnv)
	}
	return selfHostedNeonBranchDriver{path: path}, true, nil
}

func (d selfHostedNeonBranchDriver) EnsureBranch(ctx context.Context, pin worktreeDBPin) (neonBranchBackendStatus, error) {
	result, err := d.run(ctx, "ensure", pin, nil)
	if err != nil {
		return neonBranchBackendStatus{Status: "unknown", Message: err.Error()}, err
	}
	return updateNeonBranchLeaseFromDriver(pin, result)
}

func (d selfHostedNeonBranchDriver) ResetBranch(ctx context.Context, pin worktreeDBPin) error {
	result, err := d.run(ctx, "reset", pin, nil)
	if err != nil {
		return err
	}
	_, err = updateNeonBranchLeaseFromDriver(pin, result)
	return err
}

func (d selfHostedNeonBranchDriver) RestoreBranch(ctx context.Context, pin worktreeDBPin, at string) error {
	result, err := d.run(ctx, "restore", pin, []string{"--at", strings.TrimSpace(at)})
	if err != nil {
		return err
	}
	_, err = updateNeonBranchLeaseFromDriver(pin, result)
	return err
}

func (d selfHostedNeonBranchDriver) DeleteBranch(ctx context.Context, pin worktreeDBPin) error {
	_, err := d.run(ctx, "delete", pin, nil)
	return err
}

func (d selfHostedNeonBranchDriver) run(ctx context.Context, action string, pin worktreeDBPin, extra []string) (neonBranchDriverResult, error) {
	if strings.TrimSpace(action) == "" {
		return neonBranchDriverResult{}, fmt.Errorf("Neon branch driver action is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
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
	cmd := exec.CommandContext(ctx, d.path, args...)
	cmd.Env = envpolicy.Environ()
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return neonBranchDriverResult{}, fmt.Errorf("Neon branch driver %q timed out", action)
		}
		return neonBranchDriverResult{}, fmt.Errorf("Neon branch driver %q failed: %w", action, err)
	}
	var result neonBranchDriverResult
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return neonBranchDriverResult{}, fmt.Errorf("parse Neon branch driver %q JSON: %w", action, err)
	}
	return result, nil
}

func updateNeonBranchLeaseFromDriver(pin worktreeDBPin, result neonBranchDriverResult) (neonBranchBackendStatus, error) {
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
			return neonBranchBackendStatus{}, fmt.Errorf("Neon branch driver marked %q ready without endpoint metadata", pin.Branch)
		}
	case "pending", "missing", "expired":
	default:
		return neonBranchBackendStatus{}, fmt.Errorf("Neon branch driver returned unsupported status %q for %q", status, pin.Branch)
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
			normalized.Source = "branch-driver"
		}
		endpoint = &normalized
	}
	for i := range registry.Leases {
		if !sameNeonLease(registry.Leases[i].Pin, pin) && !sameNeonBranch(registry.Leases[i].Pin, pin) {
			continue
		}
		if !isOnlavaOwnedNeonLease(registry.Leases[i]) {
			return neonBranchBackendStatus{}, fmt.Errorf("refusing to update foreign local Neon branch lease %q from branch driver", pin.Branch)
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
			Message:  firstNonEmpty(strings.TrimSpace(result.Message), "Neon branch driver updated the local branch lease."),
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
		Message:  firstNonEmpty(strings.TrimSpace(result.Message), "Neon branch driver created the local branch lease."),
		Endpoint: endpoint,
	}, nil
}
