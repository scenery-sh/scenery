package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func probeNeonRuntime(ctx context.Context, state neonCellState) ([]neonImageStatus, []neonComponentStatus, []neonHealthCheck) {
	images := cloneNeonImages(state.Images)
	if len(images) == 0 {
		images = defaultNeonImages()
	}
	components := cloneNeonComponents(state.Components)
	if len(components) == 0 {
		components = defaultNeonComponents(state.Root, "not_started")
	}
	checks := []neonHealthCheck{}
	if _, err := exec.LookPath(neonDockerCommand); err != nil {
		checks = append(checks, neonHealthCheck{Name: "docker", Status: "missing", Message: "docker CLI not found on PATH"})
		markImagesUnknown(images, "docker CLI not found")
		markComponentsNotStarted(components, "docker CLI not found")
		return images, components, checks
	}
	if output, err := runDockerProbe(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		checks = append(checks, neonHealthCheck{Name: "docker", Status: "unavailable", Message: err.Error()})
		markImagesUnknown(images, "docker daemon unavailable")
		markComponentsNotStarted(components, "docker daemon unavailable")
		return images, components, checks
	} else {
		checks = append(checks, neonHealthCheck{Name: "docker", Status: "available", Message: strings.TrimSpace(output)})
	}
	for i := range images {
		if _, err := runDockerProbe(ctx, "image", "inspect", images[i].Ref); err != nil {
			images[i].Status = "missing"
			images[i].Message = err.Error()
			continue
		}
		images[i].Status = "present"
	}
	containerStatus, err := dockerContainerStatuses(ctx)
	if err != nil {
		checks = append(checks, neonHealthCheck{Name: "containers", Status: "unavailable", Message: err.Error()})
		markComponentsNotStarted(components, "could not inspect Docker containers")
		return images, components, checks
	}
	checks = append(checks, neonHealthCheck{Name: "containers", Status: "inspected"})
	for i := range components {
		status, ok := containerStatus[components[i].Container]
		if !ok {
			components[i].Status = "not_started"
			continue
		}
		components[i].Message = status
		components[i].Health = dockerHealthFromStatus(status)
		switch {
		case strings.HasPrefix(status, "Up "):
			components[i].Status = "running"
			if components[i].Health == "unhealthy" {
				components[i].Status = "degraded"
			}
		case components[i].Role == "init" && strings.HasPrefix(status, "Exited (0)"):
			components[i].Status = "completed"
		case strings.HasPrefix(status, "Exited ") || strings.HasPrefix(status, "Dead"):
			components[i].Status = "exited"
		default:
			components[i].Status = "unknown"
		}
	}
	portChecks := probeNeonPorts(ctx, firstPorts(state.Ports), components)
	checks = append(checks, portChecks...)
	for _, check := range portChecks {
		if check.Status != "closed" {
			continue
		}
		componentName := strings.TrimPrefix(check.Name, "port.")
		for i := range components {
			if components[i].Name != componentName {
				continue
			}
			components[i].Status = "degraded"
			if components[i].Message == "" {
				components[i].Message = check.Message
			} else if check.Message != "" {
				components[i].Message += "; " + check.Message
			}
			break
		}
	}
	return images, components, checks
}

func firstPorts(ports map[string]int) map[string]int {
	if len(ports) == 0 {
		return defaultNeonPorts()
	}
	return ports
}

func probeNeonPorts(ctx context.Context, ports map[string]int, components []neonComponentStatus) []neonHealthCheck {
	portKeys := map[string]string{
		"minio":          "minio_api",
		"pageserver":     "pageserver_http",
		"safekeeper-1":   "safekeeper_1",
		"safekeeper-2":   "safekeeper_2",
		"safekeeper-3":   "safekeeper_3",
		"storage-broker": "storage_broker",
	}
	checks := make([]neonHealthCheck, 0, len(portKeys))
	for _, component := range components {
		key, ok := portKeys[component.Name]
		if !ok {
			continue
		}
		port := ports[key]
		if port == 0 || component.Status != "running" {
			continue
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if err := probeTCPPort(ctx, addr); err != nil {
			checks = append(checks, neonHealthCheck{Name: "port." + component.Name, Status: "closed", Message: addr + ": " + err.Error()})
			continue
		}
		checks = append(checks, neonHealthCheck{Name: "port." + component.Name, Status: "open", Message: addr})
	}
	return checks
}

func probeTCPPort(ctx context.Context, addr string) error {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

func cloneNeonImages(images []neonImageStatus) []neonImageStatus {
	out := make([]neonImageStatus, len(images))
	copy(out, images)
	return out
}

func cloneNeonComponents(components []neonComponentStatus) []neonComponentStatus {
	out := make([]neonComponentStatus, len(components))
	copy(out, components)
	return out
}

func cloneNeonPorts(ports map[string]int) map[string]int {
	if len(ports) == 0 {
		return nil
	}
	out := make(map[string]int, len(ports))
	for key, value := range ports {
		out[key] = value
	}
	return out
}

func cloneNeonEndpoint(endpoint *neonEndpoint) *neonEndpoint {
	if endpoint == nil {
		return nil
	}
	out := *endpoint
	return &out
}

func runDockerProbe(ctx context.Context, args ...string) (string, error) {
	return runDockerCommand(ctx, 3*time.Second, args...)
}

func runDockerCommand(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, neonDockerCommand, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runDockerCompose(ctx context.Context, timeout time.Duration, state neonCellState, args ...string) (string, error) {
	composePath := strings.TrimSpace(state.ComposePath)
	if composePath == "" {
		return "", errors.New("missing Neon compose path in cell state")
	}
	dockerArgs := []string{"compose", "-f", composePath, "-p", "onlava-neon"}
	dockerArgs = append(dockerArgs, args...)
	return runDockerCommand(ctx, timeout, dockerArgs...)
}

func dockerContainerStatuses(ctx context.Context) (map[string]string, error) {
	output, err := runDockerProbe(ctx, "ps", "-a", "--filter", "label=onlava.substrate=neon", "--format", "{{.Names}}\t{{.Status}}")
	if err != nil {
		return nil, err
	}
	statuses := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, status, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		statuses[strings.TrimSpace(name)] = strings.TrimSpace(status)
	}
	return statuses, nil
}

func onlavaNeonContainerNames(ctx context.Context) ([]string, error) {
	output, err := runDockerCommand(ctx, 15*time.Second, "ps", "-a", "--filter", "label=onlava.substrate=neon", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func legacyAnonymousNeonDataVolumes(ctx context.Context) ([]string, error) {
	names, err := onlavaNeonContainerNames(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	args := append([]string{"inspect", "--format", "{{.Name}}\t{{range .Mounts}}{{.Destination}}={{.Type}}={{.Source}};{{end}}"}, names...)
	output, err := runDockerCommand(ctx, 15*time.Second, args...)
	if err != nil {
		return nil, err
	}
	var legacy []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		container, mounts, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		container = strings.TrimPrefix(strings.TrimSpace(container), "/")
		for _, mount := range strings.Split(mounts, ";") {
			if !legacyNeonDataMount(mount) {
				continue
			}
			legacy = append(legacy, container)
			break
		}
	}
	return legacy, nil
}

func legacyNeonDataMount(mount string) bool {
	parts := strings.Split(strings.TrimSpace(mount), "=")
	if len(parts) < 2 {
		return false
	}
	return parts[0] == "/data" && parts[1] == "volume"
}

func removeOnlavaNeonContainers(ctx context.Context, destroyData bool) error {
	names, err := onlavaNeonContainerNames(ctx)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	args := []string{"rm", "-f"}
	if destroyData {
		args = append(args, "-v")
	}
	args = append(args, names...)
	_, err = runDockerCommand(ctx, 90*time.Second, args...)
	return err
}

func markImagesUnknown(images []neonImageStatus, message string) {
	for i := range images {
		images[i].Status = "unknown"
		images[i].Message = message
	}
}

func markComponentsNotStarted(components []neonComponentStatus, message string) {
	for i := range components {
		components[i].Status = "not_started"
		components[i].Message = message
	}
}

func generatedFilesMissing(files []neonGeneratedFile) bool {
	for _, file := range files {
		if file.Status != "present" {
			return true
		}
	}
	return false
}

func componentsAllRunning(components []neonComponentStatus) bool {
	if len(components) == 0 {
		return false
	}
	for _, component := range components {
		if component.Status != "running" && component.Status != "completed" {
			return false
		}
	}
	return true
}

func componentsPartiallyRunning(components []neonComponentStatus) bool {
	for _, component := range components {
		if component.Status == "running" {
			return true
		}
	}
	return false
}

func componentStatusesInclude(components []neonComponentStatus, status string) bool {
	for _, component := range components {
		if component.Status == status {
			return true
		}
	}
	return false
}
