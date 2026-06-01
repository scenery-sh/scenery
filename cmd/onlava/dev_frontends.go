package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/envpolicy"
	"github.com/pbrazdil/onlava/internal/localproxy"
)

const managedFrontendStartupTimeout = 30 * time.Second

type managedFrontendProcess struct {
	Name     string
	Root     string
	Addr     string
	Command  *exec.Cmd
	LogFile  *os.File
	Store    *devdash.Store
	Victoria *victoriaStack
}

type packageJSONForFrontend struct {
	Scripts map[string]string `json:"scripts"`
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
	var processes []*managedFrontendProcess
	for _, frontend := range frontends {
		frontend.Name = localagentLabel(frontend.Name)
		if frontend.Name == "" {
			continue
		}
		if override := localproxy.FrontendOverride(frontend.Name); override != "" {
			backends[frontend.Name] = localagent.Backend{Network: "tcp", Addr: override}
			continue
		}
		process, err := startManagedFrontendProcess(ctx, root, cfg.AppID(), frontend, baseEnv, session)
		if err == nil && process != nil {
			processes = append(processes, process)
			backends[frontend.Name] = localagent.Backend{Network: "tcp", Addr: process.Addr}
			continue
		}
		if frontend.AllowSharedUpstream {
			upstream := normalizeManagedTCPUpstream(frontend.Upstream)
			if upstream != "" {
				backends[frontend.Name] = localagent.Backend{Network: "tcp", Addr: upstream}
				continue
			}
			return nil, nil, fmt.Errorf("start managed frontend %q: allow_shared_upstream is true but no upstream is configured", frontend.Name)
		}
		if err == nil {
			err = fmt.Errorf("managed frontend did not start")
		}
		if strings.TrimSpace(frontend.Upstream) != "" {
			return nil, nil, fmt.Errorf("start managed frontend %q: %w; configured upstream is ignored unless allow_shared_upstream is true or %s is set", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
		}
		return nil, nil, fmt.Errorf("start managed frontend %q: %w; set %s for a manual upstream", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
	}
	if len(backends) == 0 {
		backends = nil
	}
	return backends, processes, nil
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
	return "ONLAVA_FRONTEND_" + strings.Trim(b.String(), "_") + "_ADDR"
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
	cmdName, args, err := managedFrontendCommand(root, port)
	if err != nil {
		return nil, err
	}
	cmd := commandTreeContext(ctx, cmdName, args...)
	cmd.Dir = root
	cmd.Env = frontendDevEnv(baseEnv, appRoot, addr, session)
	logFile, err := managedFrontendLogFile(session, frontend.Name)
	if err != nil {
		return nil, err
	}
	store, err := openDevdashStore()
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = store.Close()
		_ = logFile.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = store.Close()
		_ = logFile.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = store.Close()
		_ = logFile.Close()
		return nil, err
	}
	process := &managedFrontendProcess{
		Name:     frontend.Name,
		Root:     root,
		Addr:     addr,
		Command:  cmd,
		LogFile:  logFile,
		Store:    store,
		Victoria: resolveLogsVictoriaStackFunc(ctx, false),
	}
	go captureManagedFrontendOutput(ctx, store, appID, session.SessionID, process, "stdout", stdout, logFile)
	go captureManagedFrontendOutput(ctx, store, appID, session.SessionID, process, "stderr", stderr, logFile)
	if err := waitForManagedFrontend(ctx, process); err != nil {
		_ = process.Stop()
		return nil, err
	}
	return process, nil
}

func captureManagedFrontendOutput(ctx context.Context, store *devdash.Store, appID, sessionID string, process *managedFrontendProcess, stream string, src io.Reader, dst io.Writer) {
	if process == nil || store == nil || src == nil {
		return
	}
	reader := bufio.NewReader(src)
	pid := ""
	if process.Command != nil && process.Command.Process != nil {
		pid = fmt.Sprintf("%d", process.Command.Process.Pid)
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
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			_, _ = dst.Write(chunk)
			plain := stripANSI(chunk)
			now := time.Now().UTC()
			event := assignDevEventID(devdash.DevEventFromOutput(appID, sessionID, source, plain, now))
			victoria := process.Victoria
			if victoria == nil {
				victoria = resolveLogsVictoriaStackFunc(ctx, false)
			}
			if victoria != nil {
				go func(event devdash.DevEvent) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					_ = victoria.ExportDevEvent(ctx, event)
				}(event)
			}
		}
		if err != nil {
			return
		}
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

func managedFrontendCommand(root, port string) (string, []string, error) {
	script, err := managedFrontendDevScript(root)
	if err != nil {
		return "", nil, err
	}
	if strings.Contains(script, "astro") {
		if bin := managedFrontendLocalBin(root, "astro"); bin != "" {
			return bin, []string{"dev", "--host", "127.0.0.1", "--port", port}, nil
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

func frontendDevEnv(baseEnv []string, appRoot, addr string, session localagent.Session) []string {
	env := appChildEnv(
		baseEnv,
		false,
		"HOST=127.0.0.1",
		"PORT="+portFromAddr(addr),
		"ONLAVA_APP_ROOT="+appRoot,
		"ONLAVA_SESSION_ID="+session.SessionID,
	)
	if apiURL := strings.TrimSpace(session.Routes[localagent.RouteAPI]); apiURL != "" {
		env = append(env,
			"API_BASE_URL="+apiURL,
			"ONLAVA_API_BASE_URL="+apiURL,
			"VITE_API_BASE_URL="+apiURL,
		)
	}
	if electricURL := strings.TrimSpace(session.Routes["electric"]); electricURL != "" {
		env = append(env,
			"ELECTRIC_URL="+electricURL,
			"ONLAVA_ELECTRIC_URL="+electricURL,
			"VITE_ELECTRIC_URL="+electricURL,
		)
	}
	return env
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
	deadline := time.NewTimer(managedFrontendStartupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if tcpAddrAcceptsConnections(process.Addr) {
				return nil
			}
			if process.Command.ProcessState != nil && process.Command.ProcessState.Exited() {
				return fmt.Errorf("frontend %s exited before becoming ready", process.Name)
			}
		case <-deadline.C:
			return fmt.Errorf("frontend %s did not listen on %s within %s", process.Name, process.Addr, managedFrontendStartupTimeout)
		}
	}
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
		if process == nil || process.Command == nil || process.Command.Process == nil {
			continue
		}
		name := localagentLabel(process.Name)
		if name == "" {
			continue
		}
		out["frontend-"+name] = localagent.Process{PID: process.Command.Process.Pid}
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
	if p.Command != nil && p.Command.Process != nil && p.Command.ProcessState == nil {
		_ = interruptProcessTree(p.Command)
		done := make(chan error, 1)
		go func() { done <- p.Command.Wait() }()
		select {
		case <-done:
		case <-time.After(stopTimeout):
			_ = killProcessTree(p.Command)
			<-done
		}
	}
	if p.LogFile != nil {
		err := p.LogFile.Close()
		if p.Store != nil {
			_ = p.Store.Close()
		}
		return err
	}
	if p.Store != nil {
		return p.Store.Close()
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func managedFrontendDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(envpolicy.Get("ONLAVA_DISABLE_FRONTEND_PROXY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
