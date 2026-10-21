package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/localproxy"
)

func managedFrontendRoot(appRoot string, frontend localproxy.FrontendConfig) string {
	root := strings.TrimSpace(frontend.Root)
	if root == "" && strings.TrimSpace(frontend.Name) != "" {
		root = filepath.Join("apps", strings.TrimSpace(frontend.Name))
	}
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Join(appRoot, root)
}

func managedFrontendCommand(root, port, allowedHost, basePath string) (string, []string, error) {
	script, err := managedFrontendDevScript(root)
	if err != nil {
		return "", nil, err
	}
	baseArg := managedFrontendViteBaseArg(basePath)
	if strings.Contains(script, "astro") {
		if bin := managedFrontendLocalBin(root, "astro"); bin != "" {
			args := []string{"dev", "--host", "127.0.0.1", "--port", port}
			if allowedHost != "" {
				args = append(args, "--allowed-hosts", allowedHost)
			}
			return bin, args, nil
		}
	}
	if strings.Contains(script, "vite") {
		if bin := managedFrontendLocalBin(root, "vite"); bin != "" {
			args := []string{"--host", "127.0.0.1", "--port", port}
			if baseArg != "" {
				args = append(args, "--base", baseArg)
			}
			return bin, args, nil
		}
	}
	manager := managedFrontendPackageManager(root)
	switch manager {
	case "bun":
		args := []string{"run", "dev", "--host", "127.0.0.1", "--port", port}
		if strings.Contains(script, "vite") && baseArg != "" {
			args = append(args, "--base", baseArg)
		}
		return "bun", args, nil
	case "pnpm":
		args := []string{"run", "dev", "--", "--host", "127.0.0.1", "--port", port}
		if strings.Contains(script, "vite") && baseArg != "" {
			args = append(args, "--base", baseArg)
		}
		return "pnpm", args, nil
	case "yarn":
		args := []string{"dev", "--host", "127.0.0.1", "--port", port}
		if strings.Contains(script, "vite") && baseArg != "" {
			args = append(args, "--base", baseArg)
		}
		return "yarn", args, nil
	default:
		args := []string{"run", "dev", "--", "--host", "127.0.0.1", "--port", port}
		if strings.Contains(script, "vite") && baseArg != "" {
			args = append(args, "--base", baseArg)
		}
		return "npm", args, nil
	}
}

func managedFrontendViteBaseArg(basePath string) string {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == "/" {
		return ""
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	return strings.TrimRight(basePath, "/") + "/"
}

func managedFrontendBasePath(session localagent.Session, frontendName string) string {
	if record, ok := managedFrontendRouteRecord(session, frontendName); ok {
		return strings.TrimSpace(record.Path)
	}
	if session.RouteManifest.Mode != localagent.RouteModePath {
		return ""
	}
	frontendName = localagentLabel(frontendName)
	if frontendName == "" {
		return ""
	}
	return "/" + frontendName
}

func managedFrontendRouteRecord(session localagent.Session, frontendName string) (localagent.RouteRecord, bool) {
	frontendName = localagentLabel(frontendName)
	if frontendName == "" {
		return localagent.RouteRecord{}, false
	}
	if record, ok := session.RouteManifest.Routes[frontendName]; ok {
		return record, true
	}
	if record, ok := session.RouteManifest.Routes["root"]; ok &&
		strings.TrimSpace(record.Kind) == "frontend" &&
		localagentLabel(record.Backend) == frontendName {
		return record, true
	}
	return localagent.RouteRecord{}, false
}

func managedFrontendDevScript(root string) (string, error) {
	return managedFrontendScript(root, "dev")
}

func managedFrontendScript(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", err
	}
	var pkg packageJSONForFrontend
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	script := strings.TrimSpace(pkg.Scripts[name])
	if script == "" {
		return "", fmt.Errorf("frontend package has no %s script", name)
	}
	return script, nil
}

// managedFrontendBuildCommand mirrors managedFrontendCommand for the package
// build script used by serve mode "production": local astro/vite binaries
// first, then the detected package manager. The vite base flag keeps built
// asset URLs under the frontend's path-mode prefix.
func managedFrontendBuildCommand(root, basePath string) (string, []string, error) {
	script, err := managedFrontendScript(root, "build")
	if err != nil {
		return "", nil, err
	}
	baseArg := managedFrontendViteBaseArg(basePath)
	if strings.Contains(script, "astro") {
		if bin := managedFrontendLocalBin(root, "astro"); bin != "" {
			return bin, []string{"build"}, nil
		}
	}
	if strings.Contains(script, "vite") {
		if bin := managedFrontendLocalBin(root, "vite"); bin != "" {
			args := []string{"build"}
			if baseArg != "" {
				args = append(args, "--base", baseArg)
			}
			return bin, args, nil
		}
	}
	viteBaseArgs := func() []string {
		if strings.Contains(script, "vite") && baseArg != "" {
			return []string{"--base", baseArg}
		}
		return nil
	}
	switch managedFrontendPackageManager(root) {
	case "bun":
		return "bun", append([]string{"run", "build"}, viteBaseArgs()...), nil
	case "pnpm":
		args := []string{"run", "build"}
		if extra := viteBaseArgs(); len(extra) > 0 {
			args = append(append(args, "--"), extra...)
		}
		return "pnpm", args, nil
	case "yarn":
		return "yarn", append([]string{"build"}, viteBaseArgs()...), nil
	default:
		args := []string{"run", "build"}
		if extra := viteBaseArgs(); len(extra) > 0 {
			args = append(append(args, "--"), extra...)
		}
		return "npm", args, nil
	}
}

func managedFrontendLocalBin(root, name string) string {
	for dir := filepath.Clean(root); ; dir = filepath.Dir(dir) {
		bin := filepath.Join(dir, "node_modules", ".bin", name)
		if info, err := os.Stat(bin); err == nil && !info.IsDir() {
			return bin
		}
		if parent := filepath.Dir(dir); parent == dir {
			return ""
		}
	}
}

func managedFrontendPackageManager(root string) string {
	for dir := root; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err == nil {
			var pkg struct {
				PackageManager string `json:"packageManager"`
			}
			if json.Unmarshal(data, &pkg) == nil {
				switch {
				case strings.HasPrefix(pkg.PackageManager, "bun@"):
					return "bun"
				case strings.HasPrefix(pkg.PackageManager, "pnpm@"):
					return "pnpm"
				case strings.HasPrefix(pkg.PackageManager, "yarn@"):
					return "yarn"
				case strings.HasPrefix(pkg.PackageManager, "npm@"):
					return "npm"
				}
			}
		}
		switch {
		case fileExists(filepath.Join(dir, "bun.lock")):
			return "bun"
		case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
			return "pnpm"
		case fileExists(filepath.Join(dir, "yarn.lock")):
			return "yarn"
		case fileExists(filepath.Join(dir, "package-lock.json")):
			return "npm"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "npm"
}

func frontendDevEnv(baseEnv []string, appRoot, addr string, session localagent.Session, frontendName string) []string {
	env := appChildEnv(
		baseEnv,
		false,
		"HOST=127.0.0.1",
		"PORT="+portFromAddr(addr),
		"SCENERY_APP_ROOT="+appRoot,
		"SCENERY_SESSION_ID="+session.SessionID,
		"SCENERY_ENV="+session.Environment,
		"SCENERY_RUNTIME_ENV="+session.Environment,
	)
	if apiURL := strings.TrimSpace(session.RouteManifest.Routes[localagent.RouteAPI].URL); apiURL != "" {
		env = append(env,
			"API_BASE_URL="+apiURL,
			"SCENERY_API_BASE_URL="+apiURL,
			"SCENERY_API_URL="+apiURL,
			"VITE_API_BASE_URL="+apiURL,
		)
	}
	if session.RouteManifest.Mode != "" {
		frontendPath := managedFrontendBasePath(session, frontendName)
		record, _ := managedFrontendRouteRecord(session, frontendName)
		frontendURL := strings.TrimSpace(record.URL)
		env = append(env,
			"SCENERY_ROUTE_MODE="+string(session.RouteManifest.Mode),
			"SCENERY_BASE_URL="+strings.TrimSpace(session.RouteManifest.BaseURL),
			"SCENERY_API_BASE_PATH="+routeBasePath(&session, localagent.RouteAPI),
			"SCENERY_FRONTEND_BASE_PATH="+frontendPath,
			"SCENERY_FRONTEND_PUBLIC_URL="+frontendURL,
			"VITE_SCENERY_ROUTE_MODE="+string(session.RouteManifest.Mode),
			"VITE_SCENERY_BASE_URL="+strings.TrimSpace(session.RouteManifest.BaseURL),
			"VITE_SCENERY_API_BASE_PATH="+routeBasePath(&session, localagent.RouteAPI),
			"VITE_SCENERY_FRONTEND_BASE_PATH="+frontendPath,
			"VITE_SCENERY_FRONTEND_PUBLIC_URL="+frontendURL,
		)
	}
	if allowedHost := managedFrontendAllowedHost(session, frontendName); allowedHost != "" {
		env = append(env, "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS="+allowedHost)
	}
	return env
}

func managedFrontendAllowedHost(session localagent.Session, frontendName string) string {
	frontendName = localagentLabel(frontendName)
	if frontendName == "" {
		return ""
	}
	if record, ok := managedFrontendRouteRecord(session, frontendName); ok {
		route := strings.TrimSpace(record.URL)
		u, err := url.Parse(route)
		if err == nil {
			return strings.TrimSpace(u.Hostname())
		}
	}
	sessionID := localagentLabel(session.SessionID)
	if sessionID == "" {
		return ""
	}
	if host := routeHostName(session.RouteNamespace.Hosts[frontendName]); host != "" {
		return insertSessionIntoRouteHost(host, sessionID)
	}
	baseDomain := routeHostName(session.RouteNamespace.BaseDomain)
	if baseDomain == "" {
		baseDomain = localagent.DefaultRouteBaseDomain
	}
	return frontendName + "." + sessionID + "." + baseDomain
}

func routeHostName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if u, err := url.Parse(value); err == nil && u.Hostname() != "" {
		return strings.TrimSpace(strings.ToLower(u.Hostname()))
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(strings.TrimSuffix(value, "/"), "[]")
}

func insertSessionIntoRouteHost(host, sessionID string) string {
	host = routeHostName(host)
	sessionID = localagentLabel(sessionID)
	if host == "" || sessionID == "" {
		return host
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return host
	}
	labels = append(labels[:1], append([]string{sessionID}, labels[1:]...)...)
	return strings.Join(labels, ".")
}

func portFromAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

func managedFrontendLogFile(session localagent.Session, name string) (*os.File, error) {
	dir := filepath.Join(session.StateRoot, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "frontend-"+localagentLabel(name)+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func waitForManagedFrontend(ctx context.Context, process *managedFrontendProcess) error {
	if process == nil || process.Process == nil {
		return fmt.Errorf("managed frontend did not start")
	}
	started := time.Now()
	listening := false
	if err := process.Process.WaitReady(ctx, devProcessReadyRequest{
		Timeout:  managedFrontendStartupTimeout,
		Interval: 100 * time.Millisecond,
		Probe: func(probeContext context.Context) error {
			if tcpAddrAcceptsConnections(process.Addr) {
				if !listening {
					process.recordReadiness("listening", "ready", "", started)
					listening = true
				}
				count, err := probeFrontendHTTP(probeContext, process.Addr, &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
				if err != nil {
					return err
				}
				process.recordReadiness("representative_route", "ready", "GET /", started)
				outcome := "ready"
				if count == 0 {
					outcome = "not_declared"
				}
				process.recordReadiness("client_modules", outcome, fmt.Sprintf("%d same-origin script modules", count), started)
				process.recordReadiness("render_hmr_interaction", "not_measured", "requires native browser evidence", started)
				return nil
			}
			return fmt.Errorf("frontend %s is not accepting TCP connections on %s", process.Name, process.Addr)
		},
	}); err != nil {
		return err
	}
	// Capture the child after its launcher has exec'd the serving process.
	// Keep this fingerprint immutable for later status and shutdown checks.
	owner := localagent.CaptureOwner(process.Process.PID, "scenery up frontend-"+localagentLabel(process.Name))
	select {
	case <-process.Process.Done:
		return process.Process.ReadinessExitError()
	default:
	}
	process.ownerMu.Lock()
	if process.owner.PID == 0 {
		process.owner = owner
	}
	process.ownerMu.Unlock()
	return nil
}

func stopManagedFrontendProcesses(processes []*managedFrontendProcess) {
	for _, process := range processes {
		_ = process.Stop()
	}
}

func frontendSessionProcesses(processes []*managedFrontendProcess) map[string]localagent.Process {
	if len(processes) == 0 {
		return nil
	}
	out := map[string]localagent.Process{}
	for _, process := range processes {
		if process == nil || process.Process == nil || process.Process.PID <= 0 {
			continue
		}
		name := localagentLabel(process.Name)
		if name == "" {
			continue
		}
		process.ownerMu.RLock()
		owner := process.owner
		process.ownerMu.RUnlock()
		out["frontend-"+name] = localagent.Process{PID: process.Process.PID, Owner: owner}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (p *managedFrontendProcess) Stop() error {
	if p == nil {
		return nil
	}
	if p.Process != nil {
		_ = p.Process.Stop(stopTimeout)
	}
	if p.Static != nil {
		_ = p.Static.Close()
	}
	if p.LogFile != nil {
		return p.LogFile.Close()
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
