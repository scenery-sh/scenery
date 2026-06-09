package neonselfhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/envpolicy"
)

const (
	computeImageRef                 = "ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249"
	computeDockerNet                = "onlava-neon_default"
	computeInternalPort             = 55433
	computeOTLPTracesEndpoint       = "http://host.docker.internal:10428/insert/opentelemetry/v1/traces"
	computeOTLPProtocol             = "http/protobuf"
	computeHostDockerInternalTarget = "host.docker.internal:host-gateway"
)

var (
	computeReadyTimeout  = 30 * time.Second
	computeReadyInterval = 250 * time.Millisecond
)

func ensureBranchCompute(ctx context.Context, root string, tenantID string, branchID string, branch BackendBranch) (bool, string, error) {
	if ready, message := recordedComputeReady(branch); ready {
		return true, message, nil
	}
	configPath := filepath.Join(root, "compute_templates", "config.json")
	scriptPath := filepath.Join(root, "compute_templates", "compute.sh")
	if err := requireComputeTemplate(configPath, scriptPath); err != nil {
		return false, err.Error(), nil
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return false, fmt.Sprintf("neon-selfhost-driver cannot start branch compute because docker is not available: %v", err), nil
	}
	container := strings.TrimSpace(branch.ComputeContainer)
	if container == "" {
		return false, "neon-selfhost-driver cannot start branch compute because backend metadata has no compute container name", nil
	}
	exists, running, err := inspectComputeContainer(ctx, docker, container)
	if err != nil {
		return false, "", err
	}
	switch {
	case running:
		// Already running; readiness loop below will decide whether the endpoint is usable.
	case exists:
		if _, err := runDocker(ctx, docker, 20*time.Second, "start", container); err != nil {
			return false, "", err
		}
	default:
		args := []string{
			"run", "-d",
			"--name", container,
			"--network", computeDockerNet,
			"--add-host", computeHostDockerInternalTarget,
			"--label", "onlava.substrate=neon",
			"--label", "onlava.component=compute",
			"--label", "onlava.project=" + strings.TrimSpace(branch.Project),
			"--label", "onlava.branch_id=" + strings.TrimSpace(branchID),
			"--label", "onlava.branch=" + strings.TrimSpace(branch.Branch),
			"--label", "onlava.tenant_id=" + strings.TrimSpace(tenantID),
			"-e", "PG_VERSION=16",
			"-e", "TENANT_ID=" + strings.TrimSpace(tenantID),
			"-e", "TIMELINE_ID=" + branch.TimelineID,
			"-e", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=" + computeOTLPTracesEndpoint,
			"-e", "OTEL_EXPORTER_OTLP_PROTOCOL=" + computeOTLPProtocol,
			"-v", configPath + ":/var/db/postgres/configs/config.json:ro",
			"-v", scriptPath + ":/shell/compute.sh:ro",
			"-p", fmt.Sprintf("127.0.0.1:%d:%d", branch.Port, computeInternalPort),
			"--entrypoint", "/shell/compute.sh",
			computeImageRef,
		}
		if _, err := runDocker(ctx, docker, 45*time.Second, args...); err != nil {
			return false, "", err
		}
	}
	if waitRecordedComputeReady(branch, computeReadyTimeout, computeReadyInterval) {
		if ok, message, err := ensurePostgresDatabase(ctx, branch); err != nil {
			return false, "", err
		} else if !ok {
			return false, message, nil
		}
		return true, fmt.Sprintf("neon-selfhost-driver branch compute %q is ready at %s:%d", branch.ComputeContainer, branch.Host, branch.Port), nil
	}
	return false, fmt.Sprintf("neon-selfhost-driver started branch compute %q, but endpoint %s:%d is not reachable yet", branch.ComputeContainer, branch.Host, branch.Port), nil
}

func requireComputeTemplate(paths ...string) error {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("neon-selfhost-driver compute template %s is missing; run `onlava db neon install --json`", path)
			}
			return err
		}
	}
	return nil
}

func inspectComputeContainer(ctx context.Context, docker, container string) (bool, bool, error) {
	output, err := runDocker(ctx, docker, 10*time.Second, "ps", "-a", "--filter", "name=^/"+container+"$", "--format", "{{.Names}}\t{{.Status}}")
	if err != nil {
		return false, false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 0 || strings.TrimSpace(fields[0]) != container {
			continue
		}
		status := ""
		if len(fields) == 2 {
			status = fields[1]
		}
		return true, strings.HasPrefix(status, "Up "), nil
	}
	return false, false, nil
}

func runDocker(ctx context.Context, docker string, timeout time.Duration, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, docker, args...)
	cmd.Env = envpolicy.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("docker %s timed out", strings.Join(args, " "))
		}
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func waitRecordedComputeReady(branch BackendBranch, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if ready, _ := recordedComputeReady(branch); ready {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
