package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestResolveManagedZeroFSPlanUsesSharedStorageCell(t *testing.T) {
	t.Parallel()

	agentHome := t.TempDir()
	cfg := app.Config{
		Name: "ONLV Pulse",
		Storage: app.StorageConfig{
			Default: "app",
			Stores:  map[string]app.StorageStoreConfig{"app": {Kind: "zerofs", Access: "auth"}},
		},
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"storage": {Kind: "zerofs", Mode: "local", Route: "storage", Env: map[string]string{
				"ZEROFS_STORAGE_URL": "file://${SCENERY_STORAGE_CELL_ROOT}/objects",
				"ZEROFS_CACHE_DIR":   "${SCENERY_STORAGE_CELL_ROOT}/cache",
			}},
		}},
	}
	planA, err := resolveManagedZeroFSPlan(cfg, &localagent.Session{SessionID: "session-a", BaseAppID: "onlv"}, nil, agentHome)
	if err != nil {
		t.Fatalf("resolveManagedZeroFSPlan A returned error: %v", err)
	}
	planB, err := resolveManagedZeroFSPlan(cfg, &localagent.Session{SessionID: "session-b", BaseAppID: "onlv"}, nil, agentHome)
	if err != nil {
		t.Fatalf("resolveManagedZeroFSPlan B returned error: %v", err)
	}
	if planA.StorageCellID != "onlv-pulse" || planB.StorageCellID != planA.StorageCellID {
		t.Fatalf("cell IDs = %q %q", planA.StorageCellID, planB.StorageCellID)
	}
	if planA.CellRoot != planB.CellRoot || strings.Contains(planA.CellRoot, "session-a") || strings.Contains(planA.CellRoot, "session-b") {
		t.Fatalf("cell roots must be shared and session-free: A=%q B=%q", planA.CellRoot, planB.CellRoot)
	}
	wantRoot := filepath.Join(agentHome, "agent", "storage", "onlv-pulse")
	if planA.CellRoot != wantRoot || planA.CacheDir != filepath.Join(wantRoot, "cache") || planA.ObjectsDir != filepath.Join(wantRoot, "objects") {
		t.Fatalf("storage paths = %+v, want root %q", planA, wantRoot)
	}
	if planA.Env["ZEROFS_STORAGE_URL"] != "file://"+filepath.Join(wantRoot, "objects") || planA.Env["ZEROFS_CACHE_DIR"] != filepath.Join(wantRoot, "cache") {
		t.Fatalf("expanded env = %+v", planA.Env)
	}
	if planA.NinePSocket != planB.NinePSocket || planA.RPCSocket != planB.RPCSocket {
		t.Fatalf("socket paths must be shared across sessions: A=%q/%q B=%q/%q", planA.NinePSocket, planA.RPCSocket, planB.NinePSocket, planB.RPCSocket)
	}
	for _, socketPath := range []string{planA.NinePSocket, planA.RPCSocket} {
		if !strings.HasPrefix(socketPath, os.TempDir()) || strings.Contains(socketPath, string(filepath.Separator)+"agent"+string(filepath.Separator)+"storage"+string(filepath.Separator)) || len(socketPath) > 100 {
			t.Fatalf("ZeroFS socket path should be short temp path, got %q", socketPath)
		}
	}
}

func TestManagedZeroFSConfigUsesPrivateUnixSocketsAndWebUI(t *testing.T) {
	t.Parallel()

	plan := &managedZeroFSPlan{
		CacheDir:      "/tmp/cell/cache",
		ObjectsDir:    "/tmp/cell/objects",
		StorageCellID: "test-cell",
		NinePListen:   "127.0.0.1:5564",
		NinePSocket:   "/tmp/cell/run/zerofs.9p.sock",
		RPCSocket:     "/tmp/cell/run/zerofs.rpc.sock",
		WebUIListen:   "127.0.0.1:0",
	}
	config := managedZeroFSConfigTOML(plan)
	for _, want := range []string{"[cache]", "dir = \"/tmp/cell/cache\"", "disk_size_gb = 10", "memory_size_gb = 1", "[storage]", "url = \"file:///tmp/cell/objects\"", "encryption_password = \"scenery-local-dev-test-cell\"", "[servers.ninep]", "addresses = [\"127.0.0.1:5564\"]", "unix_socket = \"/tmp/cell/run/zerofs.9p.sock\"", "[servers.rpc]", "unix_socket = \"/tmp/cell/run/zerofs.rpc.sock\"", "[servers.webui]", "addresses = [\"127.0.0.1:0\"]"} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	for _, forbidden := range []string{"path = ", "[servers.nfs]", "[servers.nbd]", "0.0.0.0"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("config contains forbidden %q:\n%s", forbidden, config)
		}
	}
}

func TestAttachManagedZeroFSServiceReusesHealthySubstrate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	plan := &managedZeroFSPlan{
		StorageCellID: "onlv-pulse",
		Route:         "storage",
		ConfigPath:    "/cell/run/zerofs.toml",
		LogPath:       "/cell/run/zerofs.log",
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:   managedZeroFSSubstrateKind(plan.StorageCellID),
		Status: "running",
		Endpoints: map[string]string{
			"cell_id":      plan.StorageCellID,
			"ninep_socket": "/cell/run/zerofs.9p.sock",
			"rpc_socket":   "/cell/run/zerofs.rpc.sock",
			"webui_addr":   "127.0.0.1:49152",
		},
	}); err != nil {
		t.Fatal(err)
	}
	prevProbe := probeManagedZeroFSSubstrateFn
	defer func() { probeManagedZeroFSSubstrateFn = prevProbe }()
	probeManagedZeroFSSubstrateFn = func(_ context.Context, service *managedZeroFSService) error {
		if service.StorageCellID != plan.StorageCellID || service.Source != "substrate" {
			t.Fatalf("probed service = %+v", service)
		}
		return nil
	}

	service, backend, ok, err := attachManagedZeroFSService(ctx, client, plan)
	if err != nil {
		t.Fatalf("attachManagedZeroFSService returned error: %v", err)
	}
	if !ok {
		t.Fatal("attachManagedZeroFSService did not reuse healthy substrate")
	}
	if service.WebUIAddr != "127.0.0.1:49152" || service.NinePSocket != "/cell/run/zerofs.9p.sock" || service.RPCSocket != "/cell/run/zerofs.rpc.sock" {
		t.Fatalf("service = %+v", service)
	}
	if backend.Network != "tcp" || backend.Addr != "127.0.0.1:49152" {
		t.Fatalf("backend = %+v", backend)
	}
}

func TestAttachManagedZeroFSServiceDeletesStaleSubstrate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	plan := &managedZeroFSPlan{StorageCellID: "onlv-pulse", Route: "storage"}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:   managedZeroFSSubstrateKind(plan.StorageCellID),
		Status: "running",
		Endpoints: map[string]string{
			"cell_id":      plan.StorageCellID,
			"ninep_socket": "/cell/run/zerofs.9p.sock",
			"rpc_socket":   "/cell/run/zerofs.rpc.sock",
			"webui_addr":   "127.0.0.1:49152",
		},
	}); err != nil {
		t.Fatal(err)
	}
	prevProbe := probeManagedZeroFSSubstrateFn
	defer func() { probeManagedZeroFSSubstrateFn = prevProbe }()
	probeManagedZeroFSSubstrateFn = func(context.Context, *managedZeroFSService) error {
		return fmt.Errorf("stale")
	}

	_, _, ok, err := attachManagedZeroFSService(ctx, client, plan)
	if err != nil {
		t.Fatalf("attachManagedZeroFSService returned error: %v", err)
	}
	if ok {
		t.Fatal("stale substrate should not be reused")
	}
	if _, err := client.GetSubstrate(ctx, managedZeroFSSubstrateKind(plan.StorageCellID)); !localagent.IsNotFound(err) {
		t.Fatalf("stale substrate err after attach = %v", err)
	}
}

func TestManagedZeroFSLeasesPersistAndReleaseBySession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	plan := &managedZeroFSPlan{StorageCellID: "shared-cell", Route: "storage"}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:   managedZeroFSSubstrateKind(plan.StorageCellID),
		Status: "running",
		Endpoints: map[string]string{
			"cell-id":      plan.StorageCellID,
			"ninep-socket": "/cell/run/zerofs.9p.sock",
			"rpc-socket":   "/cell/run/zerofs.rpc.sock",
			"webui-addr":   "127.0.0.1:49152",
		},
		URLs: map[string]string{"webui": "http://old.local"},
	}); err != nil {
		t.Fatal(err)
	}
	sessionA := localagent.Session{
		SessionID: "session-a",
		AppRoot:   "/tmp/app-a",
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CaptureOwner(os.Getpid(), "test"),
		Routes:    map[string]string{"storage": "http://storage.session-a.local.dev"},
	}
	if err := upsertManagedZeroFSLease(ctx, client, plan, &managedZeroFSService{
		StorageCellID: plan.StorageCellID,
		NinePSocket:   "/cell/run/zerofs.9p.sock",
		RPCSocket:     "/cell/run/zerofs.rpc.sock",
		WebUIAddr:     "127.0.0.1:49152",
	}, sessionA); err != nil {
		t.Fatal(err)
	}
	sessionB := localagent.Session{
		SessionID: "session-b",
		AppRoot:   "/tmp/app-b",
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CaptureOwner(os.Getpid(), "test"),
		Routes:    map[string]string{"storage": "http://storage.session-b.local.dev"},
	}
	if err := upsertManagedZeroFSLease(ctx, client, plan, nil, sessionB); err != nil {
		t.Fatal(err)
	}
	substrate, err := client.GetSubstrate(ctx, managedZeroFSSubstrateKind(plan.StorageCellID))
	if err != nil {
		t.Fatal(err)
	}
	if len(substrate.Leases) != 2 || substrate.Leases["session-a"].URL != "http://storage.session-a.local.dev" || substrate.Leases["session-b"].URL != "http://storage.session-b.local.dev" {
		t.Fatalf("substrate leases = %+v", substrate.Leases)
	}

	cells, err := releaseManagedZeroFSLeasesForSession(ctx, client, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0] != plan.StorageCellID {
		t.Fatalf("released cells = %+v", cells)
	}
	substrate, err = client.GetSubstrate(ctx, managedZeroFSSubstrateKind(plan.StorageCellID))
	if err != nil {
		t.Fatal(err)
	}
	if len(substrate.Leases) != 1 || substrate.Leases["session-b"].SessionID != "session-b" {
		t.Fatalf("substrate leases after release = %+v", substrate.Leases)
	}
}

func TestManagedZeroFSWaitOrKillNoopsForAttachedService(t *testing.T) {
	t.Parallel()

	service := &managedZeroFSService{Source: "substrate"}
	if err := service.WaitOrKill(24 * time.Hour); err != nil {
		t.Fatalf("WaitOrKill returned error: %v", err)
	}
}

func TestManagedZeroFSHasOtherLiveSession(t *testing.T) {
	t.Parallel()

	owner := localagent.CaptureOwner(os.Getpid(), "test")
	sessions := []localagent.Session{
		{
			SessionID: "current",
			OwnerPID:  os.Getpid(),
			Owner:     owner,
			Backends: map[string]localagent.Backend{
				"storage": {Network: "tcp", Addr: "127.0.0.1:49152"},
			},
		},
		{
			SessionID: "other",
			OwnerPID:  os.Getpid(),
			Owner:     owner,
			Backends: map[string]localagent.Backend{
				"storage": {Network: "tcp", Addr: "127.0.0.1:49152"},
			},
		},
	}
	if !managedZeroFSHasOtherLiveSession(sessions, "current", "storage", "127.0.0.1:49152") {
		t.Fatal("expected other live session to keep shared ZeroFS cell alive")
	}
}

func TestManagedZeroFSHasOtherLiveSessionRejectsCurrentStaleAndDifferentBackends(t *testing.T) {
	t.Parallel()

	owner := localagent.CaptureOwner(os.Getpid(), "test")
	sessions := []localagent.Session{
		{
			SessionID: "current",
			OwnerPID:  os.Getpid(),
			Owner:     owner,
			Backends: map[string]localagent.Backend{
				"storage": {Network: "tcp", Addr: "127.0.0.1:49152"},
			},
		},
		{
			SessionID: "stale",
			OwnerPID:  999999,
			Owner:     localagent.Owner{PID: 999999},
			Backends: map[string]localagent.Backend{
				"storage": {Network: "tcp", Addr: "127.0.0.1:49152"},
			},
		},
		{
			SessionID: "different-route",
			OwnerPID:  os.Getpid(),
			Owner:     owner,
			Backends: map[string]localagent.Backend{
				"files": {Network: "tcp", Addr: "127.0.0.1:49152"},
			},
		},
		{
			SessionID: "different-addr",
			OwnerPID:  os.Getpid(),
			Owner:     owner,
			Backends: map[string]localagent.Backend{
				"storage": {Network: "tcp", Addr: "127.0.0.1:49153"},
			},
		},
	}
	if managedZeroFSHasOtherLiveSession(sessions, "current", "storage", "127.0.0.1:49152") {
		t.Fatal("did not expect current, stale, or different backend sessions to keep shared ZeroFS cell alive")
	}
}

func TestStartManagedZeroFSServiceUsesExplicitBinaryAndSharedCellPaths(t *testing.T) {
	prevWait := waitForManagedZeroFSFn
	waitForManagedZeroFSFn = func(context.Context, *managedZeroFSService) error { return nil }
	defer func() { waitForManagedZeroFSFn = prevWait }()

	root := t.TempDir()
	agentHome := t.TempDir()
	bin := filepath.Join(t.TempDir(), "fake-zerofs")
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	envPath := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + testShellQuote(argsPath) + "\nenv > " + testShellQuote(envPath) + "\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{
		Name: "storageapp",
		Storage: app.StorageConfig{
			CellID: "shared-cell",
			Stores: map[string]app.StorageStoreConfig{
				"app": {Kind: "zerofs"},
			},
		},
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"storage": {Kind: "zerofs", Env: map[string]string{"ZEROFS_DATA": "${SCENERY_STORAGE_CELL_ROOT}/data"}},
		}},
	}
	session := &localagent.Session{SessionID: "dev", BaseAppID: "storageapp", StateRoot: t.TempDir()}
	plan, err := resolveManagedZeroFSPlan(cfg, session, []string{devZeroFSBinEnv + "=" + bin}, agentHome)
	if err != nil {
		t.Fatalf("resolveManagedZeroFSPlan returned error: %v", err)
	}
	service, backend, err := startManagedZeroFSService(t.Context(), root, session, plan, []string{"PATH=" + os.Getenv("PATH"), devZeroFSBinEnv + "=" + bin})
	if err != nil {
		t.Fatalf("startManagedZeroFSService returned error: %v", err)
	}
	defer func() { _ = service.WaitOrKill(100 * time.Millisecond) }()
	defer func() { _ = service.Interrupt() }()

	if backend.Network != "tcp" || !strings.HasPrefix(backend.Addr, "127.0.0.1:") {
		t.Fatalf("backend = %+v", backend)
	}
	if !strings.HasPrefix(service.ConfigPath, filepath.Join(agentHome, "agent", "storage", "shared-cell")) {
		t.Fatalf("config path = %q", service.ConfigPath)
	}
	config, err := os.ReadFile(service.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(config); !strings.Contains(got, "[servers.ninep]") || strings.Contains(got, "[servers.nfs]") || strings.Contains(got, "[servers.nbd]") {
		t.Fatalf("ZeroFS config drifted:\n%s", got)
	}
	args := readFileEventually(t, argsPath)
	if got := strings.Fields(string(args)); len(got) != 3 || got[0] != "run" || got[1] != "-c" || got[2] != service.ConfigPath {
		t.Fatalf("fake zerofs args = %q", args)
	}
	envData := readFileEventuallyContaining(t, envPath, "SCENERY_ROLE=zerofs")
	for _, want := range []string{
		"SCENERY_ROLE=zerofs",
		"SCENERY_STORAGE_CELL_ID=shared-cell",
		"SCENERY_STORAGE_CELL_ROOT=" + filepath.Join(agentHome, "agent", "storage", "shared-cell"),
		"ZEROFS_DATA=" + filepath.Join(agentHome, "agent", "storage", "shared-cell", "data"),
	} {
		if !strings.Contains(string(envData), want) {
			t.Fatalf("fake zerofs env missing %q:\n%s", want, envData)
		}
	}
}

func TestStorageCapabilityEnvIncludesProtectedZeroFSWebUIRoute(t *testing.T) {
	agentHome := t.TempDir()
	cfg := app.Config{
		Name: "storageapp",
		Storage: app.StorageConfig{
			CellID: "shared-cell",
			Stores: map[string]app.StorageStoreConfig{
				"app": {Kind: "zerofs"},
			},
		},
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"storage": {Kind: "zerofs", Route: "storage"},
		}},
	}
	env, err := storageCapabilityEnv(cfg, &localagent.Session{
		SessionID: "dev",
		BaseAppID: "storageapp",
		Routes: map[string]string{
			"storage": "https://storage.dev.local",
		},
	}, nil, agentHome)
	if err != nil {
		t.Fatalf("storageCapabilityEnv returned error: %v", err)
	}
	if !containsString(env, "SCENERY_ZEROFS_WEBUI_URL=https://storage.dev.local") {
		t.Fatalf("env missing protected Web UI URL: %+v", env)
	}
}

func testShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func readFileEventually(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("read %s: %v", path, lastErr)
	return nil
}

func readFileEventuallyContaining(t *testing.T, path, needle string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var data []byte
	var lastErr error
	for time.Now().Before(deadline) {
		var err error
		data, err = os.ReadFile(path)
		if err == nil && strings.Contains(string(data), needle) {
			return data
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("read %s: %v", path, lastErr)
	}
	t.Fatalf("read %s: missing %q in %q", path, needle, data)
	return nil
}
