package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
)

const managedFrontendStartupTimeout = 30 * time.Second

type managedFrontendProcess struct {
	Name    string
	Root    string
	Addr    string
	Process *devManagedProcess
	LogFile *os.File
}

type packageJSONForFrontend struct {
	Scripts map[string]string `json:"scripts"`
}

type managedFrontendStartResult struct {
	index   int
	name    string
	backend localagent.Backend
	process *managedFrontendProcess
	err     error
}

func managedFrontendBackendsForSession(ctx context.Context, root string, cfg app.Config, baseEnv []string, session localagent.Session) (map[string]localagent.Backend, []*managedFrontendProcess, error) {
	frontends := localProxyFrontends(cfg.Proxy.Frontends)
	if len(frontends) == 0 || managedFrontendDisabled() {
		return nil, nil, nil
	}
	sort.Slice(frontends, func(i, j int) bool {
		return frontends[i].Name < frontends[j].Name
	})
	backends := map[string]localagent.Backend{}
	var startable []localproxy.FrontendConfig
	for _, frontend := range frontends {
		frontend.Name = localagentLabel(frontend.Name)
		if frontend.Name == "" {
			continue
		}
		if override := localproxy.FrontendOverride(frontend.Name); override != "" {
			backends[frontend.Name] = localagent.Backend{Network: "tcp", Addr: override}
			continue
		}
		startable = append(startable, frontend)
	}
	results := startManagedFrontends(ctx, root, cfg.AppID(), startable, baseEnv, session)
	processes := make([]*managedFrontendProcess, 0, len(results))
	for _, result := range results {
		if result.process != nil {
			processes = append(processes, result.process)
		}
	}
	for _, result := range results {
		if result.err != nil {
			stopManagedFrontendProcesses(processes)
			return nil, nil, result.err
		}
		if result.name == "" {
			continue
		}
		backends[result.name] = result.backend
	}
	if len(backends) == 0 {
		backends = nil
	}
	return backends, processes, nil
}

func startManagedFrontends(ctx context.Context, appRoot, appID string, frontends []localproxy.FrontendConfig, baseEnv []string, session localagent.Session) []managedFrontendStartResult {
	if len(frontends) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultCh := make(chan managedFrontendStartResult, len(frontends))
	var wg sync.WaitGroup
	for i, frontend := range frontends {
		wg.Add(1)
		go func(index int, frontend localproxy.FrontendConfig) {
			defer wg.Done()
			result := startManagedFrontend(ctx, appRoot, appID, index, frontend, baseEnv, session)
			if result.err != nil {
				cancel()
			}
			resultCh <- result
		}(i, frontend)
	}
	wg.Wait()
	close(resultCh)
	results := make([]managedFrontendStartResult, len(frontends))
	for result := range resultCh {
		results[result.index] = result
	}
	return results
}

func startManagedFrontend(ctx context.Context, appRoot, appID string, index int, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) managedFrontendStartResult {
	result := managedFrontendStartResult{index: index, name: frontend.Name}
	process, err := startManagedFrontendProcess(ctx, appRoot, appID, frontend, baseEnv, session)
	if err == nil && process != nil {
		result.process = process
		result.backend = localagent.Backend{Network: "tcp", Addr: process.Addr}
		return result
	}
	if frontend.AllowSharedUpstream {
		upstream := normalizeManagedTCPUpstream(frontend.Upstream)
		if upstream != "" {
			result.backend = localagent.Backend{Network: "tcp", Addr: upstream}
			return result
		}
		result.err = fmt.Errorf("start managed frontend %q: allow_shared_upstream is true but no upstream is configured", frontend.Name)
		return result
	}
	if err == nil {
		err = fmt.Errorf("managed frontend did not start")
	}
	if strings.TrimSpace(frontend.Upstream) != "" {
		result.err = fmt.Errorf("start managed frontend %q: %w; configured upstream is ignored unless allow_shared_upstream is true or %s is set", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
		return result
	}
	result.err = fmt.Errorf("start managed frontend %q: %w; set %s for a manual upstream", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
	return result
}

func frontendOverrideEnvName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return "SCENERY_FRONTEND_" + strings.Trim(b.String(), "_") + "_ADDR"
}

func startManagedFrontendProcess(ctx context.Context, appRoot, appID string, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) (*managedFrontendProcess, error) {
	root := managedFrontendRoot(appRoot, frontend)
	if root == "" {
		return nil, fmt.Errorf("frontend %s has no root", frontend.Name)
	}
	addr, err := freeLoopbackAddr()
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	allowedHost := managedFrontendAllowedHost(session, frontend.Name)
	cmdName, args, err := managedFrontendCommand(root, port, allowedHost)
	if err != nil {
		return nil, err
	}
	logFile, err := managedFrontendLogFile(session, frontend.Name)
	if err != nil {
		return nil, err
	}
	process := &managedFrontendProcess{
		Name:    frontend.Name,
		Root:    root,
		Addr:    addr,
		LogFile: logFile,
	}
	processCtx := context.Background()
	runner, err := startDevManagedProcess(processCtx, devProcessStartRequest{
		Name:    frontend.Name,
		Kind:    "frontend",
		Role:    "web-frontend",
		Dir:     root,
		Command: cmdName,
		Args:    args,
		Env:     frontendDevEnv(baseEnv, appRoot, addr, session, frontend.Name),
		Stdout:  logFile,
		Stderr:  logFile,
		OnOutput: func(pid int, stream string, data []byte) {
			plain := append([]byte(nil), data...)
			go captureManagedFrontendOutput(processCtx, appID, session.SessionID, process, pid, stream, plain)
		},
	})
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	process.Process = runner
	if err := waitForManagedFrontend(ctx, process); err != nil {
		_ = process.Stop()
		return nil, err
	}
	return process, nil
}

func captureManagedFrontendOutput(ctx context.Context, appID, sessionID string, process *managedFrontendProcess, pidValue int, stream string, plain []byte) {
	if process == nil || len(plain) == 0 {
		return
	}
	pid := ""
	if pidValue > 0 {
		pid = fmt.Sprintf("%d", pidValue)
	}
	source := devdash.DevSource{
		ID:     "frontend:" + process.Name,
		Kind:   "frontend",
		Name:   process.Name,
		Role:   "web-frontend",
		PID:    pid,
		Stream: stream,
		Status: "running",
		URL:    "http://" + process.Addr,
	}
	now := time.Now().UTC()
	event := assignDevEventID(devdash.DevEventFromOutput(appID, sessionID, source, plain, now))
	if victoria := resolveLogsVictoriaStackFunc(ctx, false); victoria != nil {
		go func(event devdash.DevEvent) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = victoria.ExportDevEvent(ctx, event)
		}(event)
	}
}

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

func managedFrontendCommand(root, port, allowedHost string) (string, []string, error) {
	script, err := managedFrontendDevScript(root)
	if err != nil {
		return "", nil, err
	}
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
			return bin, []string{"--host", "127.0.0.1", "--port", port}, nil
		}
	}
	manager := managedFrontendPackageManager(root)
	switch manager {
	case "bun":
		return "bun", []string{"run", "dev", "--host", "127.0.0.1", "--port", port}, nil
	case "pnpm":
		return "pnpm", []string{"run", "dev", "--", "--host", "127.0.0.1", "--port", port}, nil
	case "yarn":
		return "yarn", []string{"dev", "--host", "127.0.0.1", "--port", port}, nil
	default:
		return "npm", []string{"run", "dev", "--", "--host", "127.0.0.1", "--port", port}, nil
	}
}

func managedFrontendDevScript(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", err
	}
	var pkg packageJSONForFrontend
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	script := strings.TrimSpace(pkg.Scripts["dev"])
	if script == "" {
		return "", fmt.Errorf("frontend package has no dev script")
	}
	return script, nil
}

func managedFrontendLocalBin(root, name string) string {
	bin := filepath.Join(root, "node_modules", ".bin", name)
	if info, err := os.Stat(bin); err == nil && !info.IsDir() {
		return bin
	}
	return ""
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
	)
	if apiURL := strings.TrimSpace(session.Routes[localagent.RouteAPI]); apiURL != "" {
		env = append(env,
			"API_BASE_URL="+apiURL,
			"SCENERY_API_BASE_URL="+apiURL,
			"SCENERY_API_URL="+apiURL,
			"VITE_API_BASE_URL="+apiURL,
		)
	}
	if session.RouteManifest.Mode != "" {
		frontendPath := routeBasePath(&session, frontendName)
		frontendURL := strings.TrimSpace(session.Routes[localagentLabel(frontendName)])
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
	if route := strings.TrimSpace(session.Routes[frontendName]); route != "" {
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
	return process.Process.WaitReady(ctx, devProcessReadyRequest{
		Timeout:  managedFrontendStartupTimeout,
		Interval: 100 * time.Millisecond,
		Probe: func(context.Context) error {
			if tcpAddrAcceptsConnections(process.Addr) {
				return nil
			}
			return fmt.Errorf("frontend %s is not accepting TCP connections on %s", process.Name, process.Addr)
		},
	})
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
		out["frontend-"+name] = localagent.Process{PID: process.Process.PID}
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
	if p.LogFile != nil {
		return p.LogFile.Close()
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func managedFrontendDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(envpolicy.Get("SCENERY_DISABLE_FRONTEND_PROXY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
