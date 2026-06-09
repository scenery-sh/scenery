package neonselfhost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/envpolicy"
)

func resetPendingBranch(opts branchActionOptions) (BranchActionResult, error) {
	return replaceBackendBranchTimeline(opts, "reset", "")
}

func restorePendingBranch(opts branchActionOptions) (BranchActionResult, error) {
	if strings.TrimSpace(opts.At) == "" {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver restore requires --at")
	}
	return replaceBackendBranchTimeline(opts, "restore", opts.At)
}

func replaceBackendBranchTimeline(opts branchActionOptions, action string, restoreAt string) (BranchActionResult, error) {
	root, err := substrateRoot(opts.Root)
	if err != nil {
		return BranchActionResult{}, err
	}
	path := filepath.Join(root, "backend.json")
	state, path, err := readOrCreateBackendState(opts)
	if err != nil {
		return BranchActionResult{}, err
	}
	branch := backendBranchFromOptions(state, opts)
	ensureBackendIDs(&state, &branch, opts)
	previous := state.Branches[opts.BranchID]
	if strings.TrimSpace(previous.ComputeContainer) == "" {
		previous = branch
	}
	parentTimelineID := resolveParentTimelineID(state, opts, branch)
	ancestorTimelineID := parentTimelineID
	if action == "restore" && looksLikeHexID(previous.TimelineID) {
		ancestorTimelineID = previous.TimelineID
	}
	if err := removeKnownComputeContainer(previous); err != nil {
		return BranchActionResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	branch.TimelineID = stableHexID(action + ":" + firstNonEmpty(opts.Project, branch.Project, "onlava") + ":" + firstNonEmpty(opts.BranchID, branch.Branch) + ":" + now)
	branch.ParentTimelineID = parentTimelineID
	branch.Status = "pending"
	state.Branches[opts.BranchID] = branch
	if err := WriteBackendState(path, state); err != nil {
		return BranchActionResult{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	baseURL, ok, message := pageserverBaseURL(root)
	if !ok {
		return BranchActionResult{Status: "pending", Message: message}, nil
	}
	if err := ensurePageserverTenant(ctx, baseURL, state.TenantID); err != nil {
		return BranchActionResult{}, err
	}
	if err := ensurePageserverTimeline(ctx, baseURL, state.TenantID, branch.ParentTimelineID, "", "", state.DefaultPGVersion); err != nil {
		return BranchActionResult{}, fmt.Errorf("ensure parent timeline: %w", err)
	}
	if ancestorTimelineID != branch.ParentTimelineID {
		if err := ensurePageserverTimeline(ctx, baseURL, state.TenantID, ancestorTimelineID, branch.ParentTimelineID, "", state.DefaultPGVersion); err != nil {
			return BranchActionResult{}, fmt.Errorf("ensure restore ancestor timeline: %w", err)
		}
	}
	ancestorStartLSN := ""
	if strings.TrimSpace(restoreAt) != "" {
		lsn, err := resolveRestoreLSN(ctx, baseURL, state.TenantID, ancestorTimelineID, restoreAt)
		if err != nil {
			return BranchActionResult{}, err
		}
		ancestorStartLSN = lsn
	}
	if err := ensurePageserverTimeline(ctx, baseURL, state.TenantID, branch.TimelineID, ancestorTimelineID, ancestorStartLSN, state.DefaultPGVersion); err != nil {
		return BranchActionResult{}, fmt.Errorf("%s branch timeline: %w", action, err)
	}
	branch.Status = "starting"
	state.Branches[opts.BranchID] = branch
	if err := WriteBackendState(path, state); err != nil {
		return BranchActionResult{}, err
	}
	if ready, computeMessage, err := ensureBranchCompute(ctx, root, state.TenantID, branch); err != nil {
		return BranchActionResult{}, err
	} else if ready {
		branch.Status = "ready"
		state.Branches[opts.BranchID] = branch
		if err := WriteBackendState(path, state); err != nil {
			return BranchActionResult{}, err
		}
		return BranchActionResult{
			Status:   "ready",
			Message:  computeMessage,
			Endpoint: endpointFromBackendBranch(branch),
		}, nil
	} else if computeMessage != "" {
		message = computeMessage
	}
	state.Branches[opts.BranchID] = branch
	if err := WriteBackendState(path, state); err != nil {
		return BranchActionResult{}, err
	}
	return BranchActionResult{
		Status:  "pending",
		Message: message,
	}, nil
}

func resolveRestoreLSN(ctx context.Context, baseURL, tenantID, timelineID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if looksLikeLSN(ref) {
		return ref, nil
	}
	if _, err := time.Parse(time.RFC3339, ref); err != nil {
		return "", fmt.Errorf("neon-selfhost-driver restore --at must be an LSN or RFC3339 timestamp: %w", err)
	}
	return pageserverLSNByTimestamp(ctx, baseURL, tenantID, timelineID, ref)
}

func looksLikeLSN(value string) bool {
	before, after, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok || before == "" || after == "" {
		return false
	}
	return isHexString(before) && isHexString(after)
}

func isHexString(value string) bool {
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' {
			continue
		}
		return false
	}
	return true
}

func deleteBackendBranch(opts branchActionOptions) (BranchActionResult, error) {
	root, err := substrateRoot(opts.Root)
	if err != nil {
		return BranchActionResult{}, err
	}
	path := filepath.Join(root, "backend.json")
	state, ok, err := ReadBackendState(path)
	if err != nil {
		return BranchActionResult{}, err
	}
	if !ok {
		return BranchActionResult{Status: "deleted", Message: "neon-selfhost-driver backend state was already absent"}, nil
	}
	branch, exists := state.Branches[opts.BranchID]
	if !exists {
		return BranchActionResult{Status: "deleted", Message: fmt.Sprintf("neon-selfhost-driver backend state for %q was already absent", opts.Branch)}, nil
	}
	if err := removeKnownComputeContainer(branch); err != nil {
		return BranchActionResult{}, err
	}
	delete(state.Branches, opts.BranchID)
	if err := WriteBackendState(path, state); err != nil {
		return BranchActionResult{}, err
	}
	return BranchActionResult{
		Status:  "deleted",
		Message: fmt.Sprintf("neon-selfhost-driver deleted backend state for %q", opts.Branch),
	}, nil
}

func diffReadyBranches(opts branchActionOptions) (BranchActionResult, error) {
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver diff requires --target")
	}
	root, err := substrateRoot(opts.Root)
	if err != nil {
		return BranchActionResult{}, err
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		return BranchActionResult{}, err
	}
	if !ok {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver diff requires backend.json")
	}
	current, ok := state.Branches[opts.BranchID]
	if !ok {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver diff could not find current backend branch %q", opts.BranchID)
	}
	targetBranch, ok := findBackendBranch(state, target)
	if !ok {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver diff could not find target backend branch %q", target)
	}
	if current.Status != "ready" {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver diff requires current branch %q to be ready, got %q", current.Branch, firstNonEmpty(current.Status, "unknown"))
	}
	if targetBranch.Status != "ready" {
		return BranchActionResult{}, fmt.Errorf("neon-selfhost-driver diff requires target branch %q to be ready, got %q", targetBranch.Branch, firstNonEmpty(targetBranch.Status, "unknown"))
	}
	currentSchema, err := pgDumpSchema(current)
	if err != nil {
		return BranchActionResult{}, fmt.Errorf("dump current branch %q schema: %w", current.Branch, err)
	}
	targetSchema, err := pgDumpSchema(targetBranch)
	if err != nil {
		return BranchActionResult{}, fmt.Errorf("dump target branch %q schema: %w", targetBranch.Branch, err)
	}
	return BranchActionResult{
		Message: fmt.Sprintf("schema diff complete for %q against %q", current.Branch, targetBranch.Branch),
		Diff:    simpleUnifiedDiff(current.Branch, currentSchema, targetBranch.Branch, targetSchema),
	}, nil
}

func readOrCreateBackendState(opts branchActionOptions) (BackendState, string, error) {
	root, err := substrateRoot(opts.Root)
	if err != nil {
		return BackendState{}, "", err
	}
	path := filepath.Join(root, "backend.json")
	state, ok, err := ReadBackendState(path)
	if err != nil {
		return BackendState{}, "", err
	}
	if !ok {
		state = NewBackendState("", 16)
	}
	return state, path, nil
}

func backendBranchFromOptions(state BackendState, opts branchActionOptions) BackendBranch {
	port := AllocateBranchPort(state, opts.BranchID)
	branch := state.Branches[opts.BranchID]
	branch.Project = strings.TrimSpace(opts.Project)
	branch.Branch = strings.TrimSpace(opts.Branch)
	branch.ParentTimelineID = firstNonEmpty(branch.ParentTimelineID, "pending-"+safeIdentifier(firstNonEmpty(opts.ParentBranch, "main")))
	branch.EndpointID = firstNonEmpty(branch.EndpointID, safeIdentifier(opts.Branch))
	branch.ComputeContainer = firstNonEmpty(branch.ComputeContainer, "onlava-neon-compute-"+safeIdentifier(opts.Branch))
	branch.Host = "127.0.0.1"
	branch.Port = port
	branch.Database = strings.TrimSpace(opts.Database)
	branch.Role = strings.TrimSpace(opts.Role)
	return branch
}

func recordedComputeReady(branch BackendBranch) (bool, string) {
	host := strings.TrimSpace(branch.Host)
	if host == "" || branch.Port <= 0 {
		return false, "neon-selfhost-driver recorded backend branch has no reachable endpoint yet"
	}
	address := net.JoinHostPort(host, fmt.Sprintf("%d", branch.Port))
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return false, fmt.Sprintf("neon-selfhost-driver recorded compute endpoint %s is not reachable yet: %v", address, err)
	}
	_ = conn.Close()
	return true, fmt.Sprintf("neon-selfhost-driver found reachable recorded compute endpoint for %q at %s", branch.Branch, address)
}

func endpointFromBackendBranch(branch BackendBranch) *BranchEndpoint {
	host := strings.TrimSpace(branch.Host)
	if host == "" || branch.Port <= 0 {
		return nil
	}
	return &BranchEndpoint{
		Host:     host,
		Port:     branch.Port,
		Database: firstNonEmpty(branch.Database, "postgres"),
		Role:     firstNonEmpty(branch.Role, "cloud_admin"),
		SSLMode:  "disable",
		Source:   "neon-selfhost-driver",
	}
}

func removeKnownComputeContainer(branch BackendBranch) error {
	container := strings.TrimSpace(branch.ComputeContainer)
	if container == "" {
		return nil
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		if branch.Status == "ready" {
			return fmt.Errorf("cannot delete ready Neon compute container %q because docker is not available: %w", container, err)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, docker, "rm", "-f", "-v", container)
	cmd.Env = envpolicy.Environ()
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("remove Neon compute container %q timed out", container)
	}
	if strings.Contains(string(output), "No such container") {
		return nil
	}
	if branch.Status == "ready" {
		return fmt.Errorf("remove Neon compute container %q: %w: %s", container, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func findBackendBranch(state BackendState, target string) (BackendBranch, bool) {
	if branch, ok := state.Branches[target]; ok {
		return branch, true
	}
	for _, branch := range state.Branches {
		if branch.Branch == target || branch.EndpointID == target {
			return branch, true
		}
	}
	return BackendBranch{}, false
}

func pgDumpSchema(branch BackendBranch) (string, error) {
	pgDump, err := exec.LookPath("pg_dump")
	if err != nil {
		return "", fmt.Errorf("pg_dump is not available on PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := pgDumpSchemaWithHostCommand(ctx, pgDump, branch, branch.Host, branch.Port)
	if err == nil {
		return output, nil
	}
	if !strings.Contains(err.Error(), "server version") {
		return "", err
	}
	fallback, fallbackErr := pgDumpSchemaInComputeContainer(ctx, branch)
	if fallbackErr != nil {
		return "", fmt.Errorf("%w; docker compute fallback failed: %v", err, fallbackErr)
	}
	return fallback, nil
}

func pgDumpSchemaWithHostCommand(ctx context.Context, pgDump string, branch BackendBranch, host string, port int) (string, error) {
	args := []string{
		"--schema-only",
		"--no-owner",
		"--no-privileges",
		"-h", host,
		"-p", fmt.Sprintf("%d", port),
		"-U", branch.Role,
		"-d", branch.Database,
	}
	cmd := exec.CommandContext(ctx, pgDump, args...)
	cmd.Env = append(envpolicy.Environ(), "PGPASSWORD=cloud_admin")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("pg_dump timed out")
		}
		return "", fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func pgDumpSchemaInComputeContainer(ctx context.Context, branch BackendBranch) (string, error) {
	container := strings.TrimSpace(branch.ComputeContainer)
	if container == "" {
		return "", fmt.Errorf("backend branch has no compute container")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("docker is not available on PATH: %w", err)
	}
	args := []string{
		"exec",
		"-e", "PGPASSWORD=cloud_admin",
		container,
		"pg_dump",
		"--schema-only",
		"--no-owner",
		"--no-privileges",
		"-h", "127.0.0.1",
		"-p", fmt.Sprintf("%d", computeInternalPort),
		"-U", branch.Role,
		"-d", branch.Database,
	}
	return runDocker(ctx, docker, 30*time.Second, args...)
}

func simpleUnifiedDiff(currentName, currentSchema, targetName, targetSchema string) string {
	if currentSchema == targetSchema {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(currentName)
	b.WriteByte('\n')
	b.WriteString("+++ ")
	b.WriteString(targetName)
	b.WriteByte('\n')
	b.WriteString("@@\n")
	for _, line := range splitDiffLines(currentSchema) {
		b.WriteByte('-')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range splitDiffLines(targetSchema) {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func splitDiffLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
