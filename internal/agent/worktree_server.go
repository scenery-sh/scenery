package agent

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"

	"scenery.sh/internal/machine"
)

// ControlPaths is explicit and independent of machine socket/home overrides.
// Persistent object storage must continue using its intentional shared home,
// not this private control-plane directory.
func (p WorktreePaths) ControlPaths() Paths {
	dir := filepath.Join(p.Directory, "control")
	return Paths{
		Home: p.Directory, RunDir: p.SocketDir, AgentDir: dir,
		SocketPath: p.Socket, StatePath: filepath.Join(p.Directory, "owner.json"),
		AgentLockPath: p.LiveLock, RegistryPath: filepath.Join(dir, "sessions.json"),
		LogPath: filepath.Join(dir, "owner.log"),
		EdgeDir: filepath.Join(dir, "route-auth"), EdgeTokenPath: filepath.Join(dir, "route-auth", "token"),
	}
}

func ValidateWorktreeHealth(health HealthResponse, paths WorktreePaths) error {
	return ValidateControlHealth(health, paths.Socket)
}

// ValidateControlHealth checks the one current control protocol without
// starting, replacing, or migrating the selected owner.
func ValidateControlHealth(health HealthResponse, socket string) error {
	if err := machine.ValidateArtifactIdentity(health.ArtifactIdentity, AgentStateKind, agentStateSchemaDescriptor, "use the matching Scenery binary for this control owner"); err != nil {
		return fmt.Errorf("failed_precondition: %w", err)
	}
	if health.PID <= 0 || health.SocketPath != socket {
		return fmt.Errorf("failed_precondition: control endpoint does not match the selected owner")
	}
	return nil
}

// NewWorktreeServer embeds the current control and routing handlers in the
// supervisor. The caller holds the live lock for this server's entire lifetime.
// No machine edge, trust store, deploy registry, launchd or systemd is consulted.
func NewWorktreeServer(paths WorktreePaths, record WorktreeRecord, identity Identity, dashboard Backend, edgeToken string, router net.Listener) (*Server, error) {
	if err := paths.validateRecord(record, record.AppID); err != nil {
		return nil, err
	}
	if router == nil || edgeToken == "" {
		return nil, fmt.Errorf("worktree control requires an owned router listener and route token")
	}
	controlPaths := paths.ControlPaths()
	if err := EnsureDirs(controlPaths); err != nil {
		return nil, err
	}
	registry, err := paths.OpenRegistry(router.Addr().String())
	if err != nil {
		return nil, err
	}
	for _, session := range registry.List() {
		if session.AppRoot != paths.AppRoot || session.BaseAppID != record.AppID {
			return nil, fmt.Errorf("worktree registry contains another application's ownership")
		}
	}
	control, err := listenUnixSocket(paths.Socket)
	if err != nil {
		return nil, err
	}
	server := &Server{
		paths: controlPaths, registry: registry, identity: identity,
		routerAddr: router.Addr().String(), publicRouterAddr: router.Addr().String(),
		routerScheme: "http", internalRouterScheme: "http",
		edgeToken: edgeToken, dashboard: normalizeBackend(dashboard),
		controlLn: control, routerLn: router,
		worktreeRoot: paths.AppRoot, worktreeAppID: record.AppID,
	}
	server.control = &http.Server{Handler: server.controlMux()}
	server.router = &http.Server{Handler: server.routerMux()}
	return server, nil
}

// OpenRegistry reads only the current worktree registry format. Mutating
// callers hold the lifetime lock, or are the server already holding that lock.
func (p WorktreePaths) OpenRegistry(routerAddress string) (*Registry, error) {
	return openRegistry(p.ControlPaths().RegistryPath, routerAddress, "http", true)
}
