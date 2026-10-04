package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/netprobe"
	"scenery.sh/runtime"
)

func (s *devSupervisor) assistantRuntimeConfigPath() string {
	if s == nil || s.assistants == nil {
		return ""
	}
	return filepath.Join(s.root, ".scenery", "run", "assistant-runtime.json")
}

func (s *devSupervisor) refreshAssistantRuntimeConfig() {
	if s == nil || s.assistants == nil {
		return
	}
	path := s.assistantRuntimeConfigPath()
	if path == "" {
		return
	}
	if err := runtime.WriteAssistantRuntimeConfig(path, s.assistants.RuntimeConfig()); err != nil {
		s.eventSink().Emit(context.Background(), devdash.DevSource{ID: "assistant-runtime", Kind: "assistant", Name: "assistant-runtime", Role: "assistant-bootstrap", Status: "error"}, "error", "assistant runtime config unavailable", map[string]any{"error": err.Error()})
	}
}

func (s *devSupervisor) managedAppEnv(ctx context.Context, baseEnv []string, requirements compiler.SQLRequirements) ([]string, error) {
	env, database, err := managedDatabaseEnv(ctx, s.root, s.cfg, requirements, baseEnv)
	if err != nil {
		return nil, err
	}
	s.rememberWorktreeDatabase(database, s.cfg.AppID())
	emitPostgresReadyEvents(ctx, s.eventSink(), database)
	return env, nil
}

func (s *devSupervisor) processEnvironment() []string {
	if s.invocationEnvironment != nil {
		return s.invocationEnvironment
	}
	return envpolicy.Environ()
}

func (s *devSupervisor) appDatabaseAuthorityEnv(baseEnv []string, requirements compiler.SQLRequirements) []string {
	if s == nil {
		return baseEnv
	}
	if len(requirements) > 0 {
		return envWithoutKeys(baseEnv, databaseEnvKeys(requirements)...)
	}
	return baseEnv
}

func (s *devSupervisor) sessionIdentityEnv() []string {
	session := s.currentAgentSession()
	if session == nil {
		return nil
	}
	baseAppID := strings.TrimSpace(session.BaseAppID)
	if baseAppID == "" {
		baseAppID = s.activeAppID()
	}
	runtimeAppID := strings.TrimSpace(session.RuntimeAppID)
	if runtimeAppID == "" {
		runtimeAppID = baseAppID
	}
	return []string{
		"SCENERY_SESSION_ID=" + strings.TrimSpace(session.SessionID),
		"SCENERY_BASE_APP_ID=" + baseAppID,
		"SCENERY_RUNTIME_APP_ID=" + runtimeAppID,
		"SCENERY_APP_ROOT_HASH=" + appRootHash(session.AppRoot),
		"SCENERY_BRANCH=" + strings.TrimSpace(session.Branch),
		"SCENERY_WORKTREE=" + appWorktreeName(session.AppRoot),
		"SCENERY_ROUTE_MODE=" + string(firstRouteMode(session)),
		"SCENERY_BASE_URL=" + strings.TrimSpace(session.RouteManifest.BaseURL),
		"SCENERY_API_URL=" + strings.TrimSpace(session.RouteManifest.Routes[localagent.RouteAPI].URL),
		"SCENERY_API_BASE_PATH=" + routeBasePath(session, localagent.RouteAPI),
		"SCENERY_PUBLIC_APP_URL=" + publicAppURLForSession(session),
	}
}

func firstRouteMode(session *localagent.Session) localagent.RouteMode {
	if session == nil {
		return localagent.RouteModeHost
	}
	if session.RouteManifest.Mode != "" {
		return session.RouteManifest.Mode
	}
	return localagent.RouteModeHost
}

func routeBasePath(session *localagent.Session, name string) string {
	if session == nil || session.RouteManifest.Routes == nil {
		return ""
	}
	record, ok := session.RouteManifest.Routes[name]
	if !ok {
		return ""
	}
	return strings.TrimSpace(record.Path)
}

func publicAppURLForSession(session *localagent.Session) string {
	if session == nil {
		return ""
	}
	if root := session.RouteManifest.Routes["root"]; strings.TrimSpace(root.Kind) == "frontend" && strings.TrimSpace(root.URL) != "" {
		return strings.TrimSpace(root.URL)
	}
	names := make([]string, 0, len(session.RouteManifest.Routes))
	for name, record := range session.RouteManifest.Routes {
		if name == localagent.RouteAPI || name == localagent.RouteDashboard || name == "root" {
			continue
		}
		if strings.TrimSpace(record.URL) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return strings.TrimSpace(session.RouteManifest.Routes[names[0]].URL)
	}
	return strings.TrimSpace(firstNonEmpty(session.RouteManifest.BaseURL, session.RouteManifest.Routes[localagent.RouteAPI].URL))
}

func appRootHash(root string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return ""
	}
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:12]
}

func appWorktreeName(root string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return ""
	}
	return filepath.Base(root)
}

func (s *devSupervisor) devReportURL() string {
	if s != nil && s.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if health, err := s.agent.Health(ctx); err == nil {
			if rawURL := reportURLForBackend(health.DashboardBackend); rawURL != "" {
				return rawURL
			}
		}
		if session := s.currentAgentSession(); session != nil {
			if rawURL := reportURLForBackend(session.Backends[localagent.RouteDashboard]); rawURL != "" {
				return rawURL
			}
		}
	}
	return "http://" + devdash.ListenAddr() + devdash.ReportPath
}

func reportURLForBackend(backend localagent.Backend) string {
	network := strings.TrimSpace(backend.Network)
	addr := strings.TrimSpace(backend.Addr)
	if addr == "" || (network != "" && network != "tcp") {
		return ""
	}
	return "http://" + addr + devdash.ReportPath
}

func (s *devSupervisor) sessionAuthEnv() []string {
	if s == nil || !s.cfg.Auth.Enabled {
		return nil
	}
	apiURL := strings.TrimSpace(s.apiURL())
	if apiURL == "" {
		return nil
	}
	publicAppURL := apiURL
	frontends := s.frontendURLs()
	if len(frontends) > 0 {
		names := make([]string, 0, len(frontends))
		for name := range frontends {
			names = append(names, name)
		}
		sort.Strings(names)
		if value := strings.TrimSpace(frontends[names[0]]); value != "" {
			publicAppURL = value
		}
	}
	return []string{
		"SCENERY_API_BASE_URL=" + apiURL,
		"SCENERY_PUBLIC_APP_URL=" + publicAppURL,
	}
}

func backendAcceptsConnections(backend devBackend) bool {
	backend = backend.normalized()
	target := backend.Addr
	if backend.Network == "tcp" {
		if host, port, err := net.SplitHostPort(backend.Addr); err == nil && host == "" {
			target = net.JoinHostPort("127.0.0.1", port)
		}
	}
	conn, err := net.DialTimeout(backend.Network, target, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func tcpAddrAcceptsConnections(addr string) bool {
	return netprobe.DialReachable(addr, 100*time.Millisecond)
}

func (s *devSupervisor) processOutputWriter(dst io.Writer) io.Writer {
	if s != nil && s.console != nil && s.console.json {
		return nil
	}
	if s != nil && s.console != nil && s.console.palette.Enabled() {
		return &devProcessOutputWriter{dst: dst, palette: s.console.palette}
	}
	return dst
}

func (s *devSupervisor) processOutputFilter(pid int, stream string, data []byte) []byte {
	return data
}

func appChildEnv(base []string, forceColor bool, vars ...string) []string {
	env := append(append([]string(nil), base...), vars...)
	if forceColor {
		env = append(env, "CLICOLOR_FORCE=1")
	}
	return env
}

func envWithoutKeys(base []string, keys ...string) []string {
	if len(keys) == 0 {
		return append([]string(nil), base...)
	}
	omit := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		omit[key] = struct{}{}
	}
	env := make([]string, 0, len(base))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, drop := omit[key]; drop {
				continue
			}
		}
		env = append(env, item)
	}
	return env
}
