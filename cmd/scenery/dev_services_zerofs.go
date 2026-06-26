package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
)

type managedZeroFSService struct {
	StorageCellID    string
	Route            string
	WebUIAddr        string
	Source           string
	AppRoot          string
	SessionID        string
	SessionStateRoot string
	BaseAppID        string
	RuntimeAppID     string
	LogPath          string
	ConfigPath       string
	NinePSocket      string
	RPCSocket        string
	process          *devManagedProcess
	cmd              *exec.Cmd
	done             chan error
}

const (
	managedZeroFSReadinessTimeout  = 2 * time.Minute
	managedZeroFSReadinessInterval = 200 * time.Millisecond
)

var waitForManagedZeroFSFn = waitForManagedZeroFS
var probeManagedZeroFSSubstrateFn = probeManagedZeroFSSubstrate
var resolveManagedZeroFSBinaryFn = resolveManagedZeroFSBinary

func managedZeroFSDeclared(cfg app.Config) (string, app.DevServiceConfig, bool) {
	for name, svc := range cfg.Dev.Services {
		kind := strings.TrimSpace(svc.Kind)
		if kind == "" && name == "storage" {
			kind = "zerofs"
		}
		if kind == "zerofs" {
			return name, svc, true
		}
	}
	return "", app.DevServiceConfig{}, false
}

func resolveManagedZeroFSPlan(cfg app.Config, session *localagent.Session, env []string, agentHome string) (*managedZeroFSPlan, error) {
	name, svc, ok := managedZeroFSDeclared(cfg)
	if !ok && len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	if len(cfg.Storage.Stores) == 0 {
		return nil, fmt.Errorf("dev.services.%s requires top-level storage.stores", name)
	}
	if name == "" {
		name = "storage"
	}
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil, fmt.Errorf("dev.services.%s requires an active agent-backed scenery dev runtime", name)
	}
	route := localagentLabel(firstNonEmpty(strings.TrimSpace(svc.Route), devZeroFSDefaultRoute))
	if route == "" {
		return nil, fmt.Errorf("dev.services.%s route must not be empty", name)
	}
	paths, err := localagent.DefaultPaths()
	if strings.TrimSpace(agentHome) != "" {
		paths = localagent.PathsForHome(agentHome)
		err = nil
	}
	if err != nil {
		return nil, err
	}
	cellID := cfg.StorageCellID()
	cellRoot := filepath.Join(paths.AgentDir, "storage", cellID)
	runDir := filepath.Join(cellRoot, "run")
	socketID := shortIdentityHash(cellRoot)
	if socketID == "" {
		socketID = shortIdentityHash(cellID)
	}
	if socketID == "" {
		socketID = "storage"
	}
	plan := &managedZeroFSPlan{
		ServiceName:   name,
		StorageCellID: cellID,
		Route:         route,
		Image:         strings.TrimSpace(svc.Image),
		ToolchainDir:  zeroFSToolchainStoreDir(paths),
		CellRoot:      cellRoot,
		CacheDir:      filepath.Join(cellRoot, "cache"),
		ObjectsDir:    filepath.Join(cellRoot, "objects"),
		RunDir:        runDir,
		ConfigPath:    filepath.Join(runDir, "zerofs.toml"),
		NinePListen:   "127.0.0.1:0",
		NinePSocket:   filepath.Join(os.TempDir(), "scenery-zerofs-"+socketID+"-9p.sock"),
		RPCSocket:     filepath.Join(os.TempDir(), "scenery-zerofs-"+socketID+"-rpc.sock"),
		WebUIListen:   "127.0.0.1:0",
		WebUIAddrPath: filepath.Join(runDir, "zerofs-webui.addr"),
		LogPath:       filepath.Join(runDir, "zerofs.log"),
		Env:           managedZeroFSEnv(svc.Env, cellRoot),
	}
	return plan, nil
}

func managedZeroFSEnv(serviceEnv map[string]string, cellRoot string) map[string]string {
	env := map[string]string{
		"SCENERY_STORAGE_CELL_ROOT": cellRoot,
	}
	for key, value := range copyManagedEnv(serviceEnv) {
		value = strings.ReplaceAll(value, "${SCENERY_STORAGE_CELL_ROOT}", cellRoot)
		value = strings.ReplaceAll(value, "$SCENERY_STORAGE_CELL_ROOT", cellRoot)
		env[key] = value
	}
	return env
}

func managedZeroFSConfigTOML(plan *managedZeroFSPlan) string {
	if plan == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[cache]\ndir = %q\ndisk_size_gb = 10\nmemory_size_gb = 1\n\n", filepath.ToSlash(plan.CacheDir))
	fmt.Fprintf(&b, "[storage]\nurl = %q\nencryption_password = %q\n\n", "file://"+filepath.ToSlash(plan.ObjectsDir), managedZeroFSEncryptionPassword(plan))
	b.WriteString("[servers]\n\n")
	b.WriteString("[servers.ninep]\n")
	fmt.Fprintf(&b, "addresses = [%q]\n", plan.NinePListen)
	fmt.Fprintf(&b, "unix_socket = %q\n\n", filepath.ToSlash(plan.NinePSocket))
	b.WriteString("[servers.rpc]\n")
	fmt.Fprintf(&b, "unix_socket = %q\n\n", filepath.ToSlash(plan.RPCSocket))
	b.WriteString("[servers.webui]\n")
	fmt.Fprintf(&b, "addresses = [%q]\n", plan.WebUIListen)
	fmt.Fprintf(&b, "uid = %d\n", os.Getuid())
	fmt.Fprintf(&b, "gid = %d\n", os.Getgid())
	return b.String()
}

func managedZeroFSEncryptionPassword(plan *managedZeroFSPlan) string {
	if plan == nil || strings.TrimSpace(plan.StorageCellID) == "" {
		return "scenery-local-dev-storage"
	}
	return "scenery-local-dev-" + plan.StorageCellID
}

func managedDevServicePreflightRequired(cfg app.Config) bool {
	_, _, ok := managedZeroFSDeclared(cfg)
	return ok
}

func preflightRequiredManagedDevServices(ctx context.Context, root string, cfg app.Config) error {
	return preflightRequiredManagedZeroFS(ctx, root, cfg)
}

func preflightRequiredManagedZeroFS(ctx context.Context, root string, cfg app.Config) error {
	name, _, ok := managedZeroFSDeclared(cfg)
	if !ok {
		return nil
	}
	if name == "" {
		name = "storage"
	}
	if len(cfg.Storage.Stores) == 0 {
		return fmt.Errorf("dev.services.%s requires top-level storage.stores", name)
	}
	if localagent.DisabledByEnv() {
		return fmt.Errorf("dev.services.%s requires the scenery agent for managed ZeroFS storage; unset SCENERY_AGENT_DISABLE before `scenery up`", name)
	}
	session := &localagent.Session{SessionID: "preflight", BaseAppID: cfg.AppID(), AppRoot: root}
	plan, err := resolveManagedZeroFSPlan(cfg, session, nil, "")
	if err != nil {
		return fmt.Errorf("dev.services.%s managed ZeroFS preflight failed: %w", name, err)
	}
	if plan == nil {
		return nil
	}
	if _, err := resolveManagedZeroFSBinaryFn(ctx, plan); err != nil {
		service := managedZeroFSServiceFromPlan(root, plan, "managed-toolchain")
		service.BaseAppID = cfg.AppID()
		evidencePath, evidenceErr := writeManagedZeroFSFailureEvidence(root, nil, service, "managed-zerofs.preflight", err, "Run `scenery system toolchain sync --tool "+devZeroFSToolchainArtifact+"` with downloads enabled, or set SCENERY_TOOLCHAIN_DIR to a managed store that already contains it.")
		return fmt.Errorf("dev.services.%s managed ZeroFS preflight failed: required managed toolchain artifact %q is unavailable: %w\nFix: run `scenery system toolchain sync --tool %s` with downloads enabled, or set SCENERY_TOOLCHAIN_DIR to a managed store that already contains it%s", name, devZeroFSToolchainArtifact, err, devZeroFSToolchainArtifact, managedZeroFSEvidenceErrorSuffix(evidencePath, evidenceErr))
	}
	return nil
}

func (s *devSupervisor) ensureManagedZeroFS(ctx context.Context) error {
	if s == nil || s.currentZeroFS() != nil {
		return nil
	}
	if _, _, ok := managedZeroFSDeclared(s.cfg); !ok {
		return nil
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return err
	}
	agentSession := s.currentAgentSession()
	if s.agent == nil || agentSession == nil {
		return fmt.Errorf("dev.services.storage requires an active agent-backed scenery dev runtime")
	}
	plan, err := resolveManagedZeroFSPlan(s.cfg, agentSession, baseEnv, "")
	if err != nil || plan == nil {
		return err
	}
	if service, backend, ok, err := attachManagedZeroFSService(ctx, s.agent, plan); err != nil {
		return err
	} else if ok {
		return s.attachManagedZeroFSExisting(ctx, agentSession, plan, service, backend)
	}
	kind := managedZeroFSSubstrateKind(plan.StorageCellID)
	processUnlock := lockManagedSubstrateProcess(plan.CellRoot, kind)
	defer processUnlock()
	unlock, err := lockManagedSubstrateRoot(plan.CellRoot, kind)
	if err != nil {
		return err
	}
	defer unlock()
	if service, backend, ok, err := attachManagedZeroFSService(ctx, s.agent, plan); err != nil {
		return err
	} else if ok {
		return s.attachManagedZeroFSExisting(ctx, agentSession, plan, service, backend)
	}
	service, backend, err := startManagedZeroFSService(ctx, s.root, agentSession, plan, baseEnv)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.zeroFS = service
	s.mu.Unlock()
	session, err := s.registerManagedZeroFSSessionBackend(ctx, agentSession, plan, backend)
	if err != nil {
		return err
	}
	if s.agent != nil {
		_, _ = s.agent.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
			Kind:     managedZeroFSSubstrateKind(plan.StorageCellID),
			Status:   "running",
			OwnerPID: os.Getpid(),
			Owner:    localagent.CaptureOwner(os.Getpid(), "scenery up"),
			PIDs: map[string]int{
				"zerofs": service.PID(),
			},
			Endpoints: map[string]string{
				"cell-id":      plan.StorageCellID,
				"ninep-socket": plan.NinePSocket,
				"rpc-socket":   plan.RPCSocket,
				"webui-addr":   service.WebUIAddr,
			},
			URLs: map[string]string{
				"webui": session.Routes[plan.Route],
			},
			Leases: map[string]localagent.SubstrateLease{
				session.SessionID: managedZeroFSLeaseForSession(session, plan, session.Routes[plan.Route], time.Now().UTC()),
			},
		})
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("zerofs.managed", map[string]any{
			"route":   plan.Route,
			"webui":   service.WebUIAddr,
			"source":  service.Source,
			"cell_id": plan.StorageCellID,
		})
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "zerofs", Kind: "substrate", Name: "zerofs", Role: "storage", Status: "running", URL: session.Routes[plan.Route]}, "info", "managed ZeroFS ready", map[string]any{
		"route":   plan.Route,
		"source":  service.Source,
		"cell_id": plan.StorageCellID,
	})
	return nil
}

func (s *devSupervisor) attachManagedZeroFSExisting(ctx context.Context, agentSession *localagent.Session, plan *managedZeroFSPlan, service *managedZeroFSService, backend localagent.Backend) error {
	populateManagedZeroFSSessionContext(service, s.root, agentSession)
	s.mu.Lock()
	s.zeroFS = service
	s.mu.Unlock()
	session, err := s.registerManagedZeroFSSessionBackend(ctx, agentSession, plan, backend)
	if err != nil {
		return err
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("zerofs.attached", map[string]any{
			"route":   plan.Route,
			"webui":   service.WebUIAddr,
			"source":  service.Source,
			"cell_id": plan.StorageCellID,
		})
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "zerofs", Kind: "substrate", Name: "zerofs", Role: "storage", Status: "running", URL: session.Routes[plan.Route]}, "info", "attached existing ZeroFS storage cell", map[string]any{
		"route":   plan.Route,
		"source":  service.Source,
		"cell_id": plan.StorageCellID,
	})
	if err := upsertManagedZeroFSLease(ctx, s.agent, plan, service, session); err != nil {
		return err
	}
	return nil
}

func (s *devSupervisor) registerManagedZeroFSSessionBackend(ctx context.Context, agentSession *localagent.Session, plan *managedZeroFSPlan, backend localagent.Backend) (localagent.Session, error) {
	backends := copyManagedBackends(agentSession.Backends)
	backends[plan.Route] = backend
	session, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.activeAppID(),
		AppRoot:     s.root,
		SessionID:   agentSession.SessionID,
		Branch:      agentSession.Branch,
		Status:      firstNonEmpty(agentSession.Status, "starting"),
		OwnerPID:    os.Getpid(),
		AppPID:      agentSession.AppPID,
		Processes:   s.sessionProcessesFor(agentSession, agentSession.AppPID),
		Backends:    backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		return localagent.Session{}, err
	}
	s.storeAgentSession(&session)
	return session, nil
}

func upsertManagedZeroFSLease(ctx context.Context, agent *localagent.Client, plan *managedZeroFSPlan, service *managedZeroFSService, session localagent.Session) error {
	if agent == nil || plan == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil
	}
	kind := managedZeroFSSubstrateKind(plan.StorageCellID)
	substrate, err := agent.GetSubstrate(ctx, kind)
	if err != nil {
		if localagent.IsNotFound(err) {
			return nil
		}
		return err
	}
	leases := copyManagedZeroFSLeases(substrate.Leases)
	now := time.Now().UTC()
	lease := managedZeroFSLeaseForSession(session, plan, session.Routes[plan.Route], now)
	if existing, ok := leases[session.SessionID]; ok && !existing.CreatedAt.IsZero() {
		lease.CreatedAt = existing.CreatedAt
	}
	leases[session.SessionID] = lease
	req := managedZeroFSSubstrateUpsertRequest(substrate, leases)
	if service != nil {
		if req.Endpoints == nil {
			req.Endpoints = map[string]string{}
		}
		req.Endpoints["cell-id"] = firstNonEmpty(req.Endpoints["cell-id"], service.StorageCellID, plan.StorageCellID)
		req.Endpoints["ninep-socket"] = firstNonEmpty(req.Endpoints["ninep-socket"], service.NinePSocket)
		req.Endpoints["rpc-socket"] = firstNonEmpty(req.Endpoints["rpc-socket"], service.RPCSocket)
		req.Endpoints["webui-addr"] = firstNonEmpty(req.Endpoints["webui-addr"], service.WebUIAddr)
	}
	if req.URLs == nil {
		req.URLs = map[string]string{}
	}
	req.URLs["webui"] = firstNonEmpty(session.Routes[plan.Route], req.URLs["webui"])
	_, err = agent.UpsertSubstrate(ctx, req)
	return err
}

func releaseManagedZeroFSLeasesForSession(ctx context.Context, agent *localagent.Client, session localagent.Session) ([]string, error) {
	if agent == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil, nil
	}
	substrates, err := agent.ListSubstrates(ctx)
	if err != nil {
		return nil, err
	}
	var cells []string
	for _, substrate := range substrates {
		if !isManagedZeroFSSubstrateKind(substrate.Kind) {
			continue
		}
		leases := copyManagedZeroFSLeases(substrate.Leases)
		if _, ok := leases[session.SessionID]; !ok {
			continue
		}
		delete(leases, session.SessionID)
		if _, err := agent.UpsertSubstrate(ctx, managedZeroFSSubstrateUpsertRequest(substrate, leases)); err != nil {
			return cells, err
		}
		cells = append(cells, firstNonEmpty(substrate.Endpoints["cell-id"], substrate.Endpoints["cell_id"], strings.TrimPrefix(substrate.Kind, localagent.SubstrateZeroFS+"-"), substrate.Kind))
	}
	return cells, nil
}

func managedZeroFSSubstrateUpsertRequest(substrate localagent.Substrate, leases map[string]localagent.SubstrateLease) localagent.UpsertSubstrateRequest {
	return localagent.UpsertSubstrateRequest{
		Kind:           substrate.Kind,
		Status:         substrate.Status,
		OwnerPID:       substrate.OwnerPID,
		Owner:          substrate.Owner,
		PIDs:           copyManagedZeroFSIntMap(substrate.PIDs),
		Owners:         copyManagedZeroFSOwnerMap(substrate.Owners),
		URLs:           copyManagedZeroFSStringMap(substrate.URLs),
		Endpoints:      copyManagedZeroFSStringMap(substrate.Endpoints),
		Leases:         leases,
		LastExit:       substrate.LastExit,
		ComponentExits: substrate.ComponentExits,
	}
}

func managedZeroFSLeaseForSession(session localagent.Session, plan *managedZeroFSPlan, routeURL string, now time.Time) localagent.SubstrateLease {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return localagent.SubstrateLease{
		SessionID: session.SessionID,
		AppRoot:   session.AppRoot,
		Route:     plan.Route,
		URL:       strings.TrimSpace(routeURL),
		OwnerPID:  firstPositiveInt(session.OwnerPID, session.Owner.PID),
		Owner:     session.Owner,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func copyManagedZeroFSLeases(values map[string]localagent.SubstrateLease) map[string]localagent.SubstrateLease {
	copied := make(map[string]localagent.SubstrateLease, len(values))
	for key, value := range values {
		sessionID := strings.TrimSpace(firstNonEmpty(value.SessionID, key))
		if sessionID == "" {
			continue
		}
		value.SessionID = sessionID
		copied[sessionID] = value
	}
	return copied
}

func copyManagedZeroFSStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) != "" {
			copied[key] = value
		}
	}
	return copied
}

func copyManagedZeroFSIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]int, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) != "" {
			copied[key] = value
		}
	}
	return copied
}

func copyManagedZeroFSOwnerMap(values map[string]localagent.Owner) map[string]localagent.Owner {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]localagent.Owner, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) != "" {
			copied[key] = value
		}
	}
	return copied
}

func attachManagedZeroFSService(ctx context.Context, agent *localagent.Client, plan *managedZeroFSPlan) (*managedZeroFSService, localagent.Backend, bool, error) {
	if agent == nil || plan == nil {
		return nil, localagent.Backend{}, false, nil
	}
	substrate, err := agent.GetSubstrate(ctx, managedZeroFSSubstrateKind(plan.StorageCellID))
	if err != nil {
		if localagent.IsNotFound(err) {
			return nil, localagent.Backend{}, false, nil
		}
		return nil, localagent.Backend{}, false, err
	}
	service, backend, ok := managedZeroFSServiceFromSubstrate(plan, substrate)
	if !ok {
		return nil, localagent.Backend{}, false, nil
	}
	if err := probeManagedZeroFSSubstrateFn(ctx, service); err != nil {
		if _, deleteErr := agent.DeleteSubstrate(ctx, substrate.Kind); deleteErr != nil && !localagent.IsNotFound(deleteErr) {
			return nil, localagent.Backend{}, false, deleteErr
		}
		return nil, localagent.Backend{}, false, nil
	}
	return service, backend, true, nil
}

func managedZeroFSServiceFromSubstrate(plan *managedZeroFSPlan, substrate localagent.Substrate) (*managedZeroFSService, localagent.Backend, bool) {
	webUIAddr := firstNonEmpty(substrate.Endpoints["webui-addr"], substrate.Endpoints["webui_addr"])
	ninepSocket := firstNonEmpty(substrate.Endpoints["ninep-socket"], substrate.Endpoints["ninep_socket"])
	rpcSocket := firstNonEmpty(substrate.Endpoints["rpc-socket"], substrate.Endpoints["rpc_socket"])
	if webUIAddr == "" || ninepSocket == "" || rpcSocket == "" {
		return nil, localagent.Backend{}, false
	}
	service := &managedZeroFSService{
		StorageCellID: firstNonEmpty(substrate.Endpoints["cell-id"], substrate.Endpoints["cell_id"], plan.StorageCellID),
		Route:         plan.Route,
		WebUIAddr:     webUIAddr,
		Source:        "substrate",
		LogPath:       plan.LogPath,
		ConfigPath:    plan.ConfigPath,
		NinePSocket:   ninepSocket,
		RPCSocket:     rpcSocket,
	}
	return service, localagent.Backend{Network: "tcp", Addr: webUIAddr}, true
}

func startManagedZeroFSService(ctx context.Context, root string, session *localagent.Session, plan *managedZeroFSPlan, baseEnv []string) (*managedZeroFSService, localagent.Backend, error) {
	if plan == nil {
		return nil, localagent.Backend{}, nil
	}
	if err := os.MkdirAll(plan.RunDir, 0o700); err != nil {
		return nil, localagent.Backend{}, err
	}
	if err := os.Chmod(plan.RunDir, 0o700); err != nil {
		return nil, localagent.Backend{}, err
	}
	for _, dir := range []string{plan.CacheDir, plan.ObjectsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, localagent.Backend{}, err
		}
	}
	ninepPort, err := freeLoopbackPort()
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	webUIPort, err := freeLoopbackPort()
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	startPlan := *plan
	startPlan.NinePListen = net.JoinHostPort("127.0.0.1", strconv.Itoa(ninepPort))
	startPlan.WebUIListen = net.JoinHostPort("127.0.0.1", strconv.Itoa(webUIPort))
	_ = os.Remove(startPlan.NinePSocket)
	_ = os.Remove(startPlan.RPCSocket)
	if err := os.WriteFile(startPlan.ConfigPath, []byte(managedZeroFSConfigTOML(&startPlan)), 0o600); err != nil {
		return nil, localagent.Backend{}, err
	}
	if err := os.Chmod(startPlan.ConfigPath, 0o600); err != nil {
		return nil, localagent.Backend{}, err
	}
	if err := os.WriteFile(startPlan.WebUIAddrPath, []byte(startPlan.WebUIListen+"\n"), 0o644); err != nil {
		return nil, localagent.Backend{}, err
	}
	binaryPath, err := resolveManagedZeroFSBinaryFn(ctx, &startPlan)
	if err != nil {
		return nil, localagent.Backend{}, managedZeroFSErrorWithEvidence(root, session, &startPlan, "managed-toolchain", "managed-zerofs.toolchain", err, "Run `scenery system toolchain sync --tool "+devZeroFSToolchainArtifact+"` with downloads enabled, or set SCENERY_TOOLCHAIN_DIR to a managed store that already contains it.")
	}
	service, err := startManagedZeroFSBinary(ctx, root, session, &startPlan, binaryPath, baseEnv)
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	return service, localagent.Backend{Network: "tcp", Addr: startPlan.WebUIListen}, nil
}

func resolveManagedZeroFSBinary(ctx context.Context, plan *managedZeroFSPlan) (string, error) {
	storeDir := ""
	if plan != nil {
		storeDir = strings.TrimSpace(plan.ToolchainDir)
	}
	if storeDir == "" {
		storeDir = toolchainStoreDirForStateRoot("")
	}
	if status, err := managedToolchainArtifactStatusInDir(storeDir, devZeroFSToolchainArtifact); err == nil && status.ManagedPath != "" && isExecutableFile(status.ManagedPath) {
		return status.ManagedPath, nil
	}
	status, err := syncManagedToolchainArtifactInDir(ctx, storeDir, devZeroFSToolchainArtifact)
	if err != nil {
		return "", fmt.Errorf("managed ZeroFS is not installed and could not be synced: %w", err)
	}
	if status.ManagedPath == "" || !isExecutableFile(status.ManagedPath) {
		return "", fmt.Errorf("managed ZeroFS is not installed in %s; run `scenery system toolchain sync --tool %s` with downloads enabled", storeDir, devZeroFSToolchainArtifact)
	}
	return status.ManagedPath, nil
}

func zeroFSToolchainStoreDir(paths localagent.Paths) string {
	if strings.TrimSpace(envpolicy.Get("SCENERY_TOOLCHAIN_DIR")) != "" {
		return toolchainStoreDirForStateRoot("")
	}
	if strings.TrimSpace(paths.Home) == "" {
		return toolchainStoreDirForStateRoot("")
	}
	return filepath.Join(paths.Home, "toolchain")
}

func startManagedZeroFSBinary(ctx context.Context, root string, session *localagent.Session, plan *managedZeroFSPlan, binaryPath string, baseEnv []string) (*managedZeroFSService, error) {
	if strings.TrimSpace(binaryPath) == "" {
		err := fmt.Errorf("managed ZeroFS binary path is empty")
		return nil, managedZeroFSErrorWithEvidence(root, session, plan, "managed-toolchain", "managed-zerofs.toolchain", err, "Run `scenery system toolchain sync --tool "+devZeroFSToolchainArtifact+"` with downloads enabled.")
	}
	if !isExecutableFile(binaryPath) {
		err := fmt.Errorf("managed ZeroFS binary is not executable: %s", binaryPath)
		return nil, managedZeroFSErrorWithEvidence(root, session, plan, "managed-toolchain", "managed-zerofs.toolchain", err, "Run `scenery system toolchain sync --tool "+devZeroFSToolchainArtifact+"` with downloads enabled.")
	}
	env := managedZeroFSProcessEnv(plan, baseEnv, managedZeroFSSessionEnv(root, session)...)
	service, err := startManagedZeroFSProcess(ctx, root, session, "managed-toolchain", plan.LogPath, binaryPath, []string{"run", "-c", plan.ConfigPath}, env, plan)
	if err != nil {
		return nil, managedZeroFSErrorWithEvidence(root, session, plan, "managed-toolchain", "managed-zerofs.start", err, "Inspect the evidence artifact and ZeroFS log path, then rerun `scenery up` after fixing the substrate.")
	}
	if err := waitForManagedZeroFSFn(ctx, service); err != nil {
		_ = service.Interrupt()
		return nil, err
	}
	return service, nil
}

func startManagedZeroFSProcess(ctx context.Context, root string, session *localagent.Session, source, logPath, command string, args, env []string, plan *managedZeroFSPlan) (*managedZeroFSService, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	process, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name:    "managed ZeroFS",
		Kind:    "substrate",
		Role:    "storage",
		Dir:     root,
		Command: command,
		Args:    args,
		Env:     env,
		Stdout:  logFile,
		Stderr:  logFile,
	})
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	service := &managedZeroFSService{
		StorageCellID: plan.StorageCellID,
		Route:         plan.Route,
		WebUIAddr:     plan.WebUIListen,
		Source:        source,
		AppRoot:       root,
		LogPath:       logPath,
		ConfigPath:    plan.ConfigPath,
		NinePSocket:   plan.NinePSocket,
		RPCSocket:     plan.RPCSocket,
		process:       process,
		cmd:           process.Cmd,
		done:          make(chan error, 1),
	}
	populateManagedZeroFSSessionContext(service, root, session)
	go func() {
		<-process.done
		process.mu.Lock()
		waitErr := process.waitErr
		process.mu.Unlock()
		service.done <- waitErr
		close(service.done)
		<-process.outputDone
		_ = logFile.Close()
	}()
	return service, nil
}

func managedZeroFSProcessEnv(plan *managedZeroFSPlan, baseEnv []string, extra ...string) []string {
	overrides := map[string]string{
		"SCENERY_ROLE":                  "zerofs",
		"SCENERY_STORAGE_CELL_ID":       plan.StorageCellID,
		"SCENERY_STORAGE_CELL_ROOT":     plan.CellRoot,
		"SCENERY_STORAGE_ZEROFS_CONFIG": plan.ConfigPath,
		"SCENERY_ZEROFS_WEBUI_ADDR":     plan.WebUIListen,
	}
	for _, item := range extra {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.TrimSpace(key) != "" {
			overrides[strings.TrimSpace(key)] = value
		}
	}
	env := envWithManagedOverrides(baseEnv, overrides)
	values := envListMap(env)
	for key, value := range plan.Env {
		env = envWithManagedOverrides(env, map[string]string{key: os.Expand(strings.TrimSpace(value), func(name string) string {
			return values[name]
		})})
		values = envListMap(env)
	}
	return env
}

func managedZeroFSSessionEnv(root string, session *localagent.Session) []string {
	if session == nil {
		return nil
	}
	return []string{
		"SCENERY_APP_ROOT=" + root,
		"SCENERY_SESSION_ID=" + strings.TrimSpace(session.SessionID),
		"SCENERY_BASE_APP_ID=" + strings.TrimSpace(session.BaseAppID),
		"SCENERY_RUNTIME_APP_ID=" + strings.TrimSpace(session.RuntimeAppID),
	}
}

func waitForManagedZeroFS(ctx context.Context, service *managedZeroFSService) error {
	if service == nil || service.process == nil {
		return fmt.Errorf("missing managed ZeroFS process")
	}
	err := service.process.WaitReady(ctx, devProcessReadyRequest{
		Timeout:  managedZeroFSReadinessTimeout,
		Interval: managedZeroFSReadinessInterval,
		Probe:    func(ctx context.Context) error { return probeManagedZeroFSSubstrate(ctx, service) },
	})
	if err != nil {
		evidencePath, evidenceErr := writeManagedZeroFSFailureEvidence(service.AppRoot, nil, service, "managed-zerofs.readiness", err, "Inspect the evidence artifact and ZeroFS log path, then rerun `scenery up` after fixing the substrate.")
		contextText := managedZeroFSReadinessContext(service) + managedZeroFSEvidenceErrorSuffix(evidencePath, evidenceErr)
		return fmt.Errorf("managed ZeroFS readiness failed for storage cell %q: %w\n%s", firstNonEmpty(service.StorageCellID, "unknown"), err, contextText)
	}
	return nil
}

func managedZeroFSReadinessContext(service *managedZeroFSService) string {
	if service == nil {
		return "ZeroFS context: unavailable"
	}
	lines := []string{
		"ZeroFS context:",
		"  cell_id: " + firstNonEmpty(service.StorageCellID, "unknown"),
		"  source: " + firstNonEmpty(service.Source, "unknown"),
		"  pid: " + strconv.Itoa(firstPositiveInt(service.PID(), 0)),
		"  config: " + firstNonEmpty(service.ConfigPath, "unknown"),
		"  log: " + firstNonEmpty(service.LogPath, "unknown"),
		"  ninep_socket: " + firstNonEmpty(service.NinePSocket, "unknown"),
		"  rpc_socket: " + firstNonEmpty(service.RPCSocket, "unknown"),
	}
	if strings.TrimSpace(service.WebUIAddr) != "" {
		lines = append(lines, "  webui: http://"+strings.TrimSpace(service.WebUIAddr))
	} else {
		lines = append(lines, "  webui: unknown")
	}
	return strings.Join(lines, "\n")
}

func probeManagedZeroFSSubstrate(ctx context.Context, service *managedZeroFSService) error {
	if _, err := os.Stat(service.NinePSocket); err != nil {
		return err
	}
	if _, err := os.Stat(service.RPCSocket); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+service.WebUIAddr+"/", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 200 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("ZeroFS Web UI returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *devSupervisor) shouldDetachManagedZeroFS(ctx context.Context, service *managedZeroFSService) bool {
	if s == nil || s.agent == nil || service == nil || service.process == nil {
		return false
	}
	route := strings.TrimSpace(service.Route)
	addr := strings.TrimSpace(service.WebUIAddr)
	if route == "" || addr == "" {
		return false
	}
	session := s.currentAgentSession()
	currentSessionID := ""
	if session != nil {
		currentSessionID = session.SessionID
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	sessions, err := s.agent.List(ctx, "")
	if err != nil {
		return false
	}
	if !managedZeroFSHasOtherLiveSession(sessions, currentSessionID, route, addr) {
		return false
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("zerofs.detached", map[string]any{
			"route":   route,
			"webui":   addr,
			"cell_id": service.StorageCellID,
		})
	}
	return true
}

func managedZeroFSHasOtherLiveSession(sessions []localagent.Session, currentSessionID, route, addr string) bool {
	currentSessionID = strings.TrimSpace(currentSessionID)
	route = strings.TrimSpace(route)
	addr = strings.TrimSpace(addr)
	if route == "" || addr == "" {
		return false
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.SessionID) == currentSessionID {
			continue
		}
		backend, ok := session.Backends[route]
		if !ok || backend.Network != "tcp" || strings.TrimSpace(backend.Addr) != addr {
			continue
		}
		if _, live := sessionOwnerProcessLive(session); !live {
			continue
		}
		return true
	}
	return false
}

func (s *managedZeroFSService) Interrupt() error {
	if s == nil || s.cmd == nil {
		return nil
	}
	if s.process != nil {
		return s.process.Interrupt()
	}
	return interruptProcessTree(s.cmd)
}

func (s *managedZeroFSService) WaitOrKill(grace time.Duration) error {
	if s == nil {
		return nil
	}
	if s.process != nil {
		return s.process.WaitOrKill(grace)
	}
	if s.cmd == nil && s.done == nil {
		return nil
	}
	select {
	case err := <-s.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		if s.cmd != nil {
			_ = killProcessTree(s.cmd)
		}
		return nil
	}
}

func (s *managedZeroFSService) PID() int {
	if s == nil {
		return 0
	}
	if s.process != nil {
		return s.process.PID
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

func managedZeroFSSubstrateKind(cellID string) string {
	cellID = localagentLabel(cellID)
	if cellID == "" {
		return localagent.SubstrateZeroFS
	}
	return localagent.SubstrateZeroFS + "-" + cellID
}

func isManagedZeroFSSubstrateKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	return kind == localagent.SubstrateZeroFS || strings.HasPrefix(kind, localagent.SubstrateZeroFS+"-")
}
