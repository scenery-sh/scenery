package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storageconfig"
	publicstorage "scenery.sh/storage"
)

func runtimeStorageJSON(t *testing.T, cfg storageconfig.RuntimeConfig) string {
	t.Helper()
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestRunStorageStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {
			"default": "app",
			"stores": {
				"app": {"kind": "local", "access": "auth"}
			}
		}
	}`)
	var out bytes.Buffer
	if err := runStorageCommand([]string{"status", "-o", "json", "--app-root", root}, &out); err != nil {
		t.Fatalf("runStorageCommand(status) error = %v", err)
	}
	var payload storageStatusResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON(status) error = %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.storage.status" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.storage.status").SchemaRevision || !payload.Storage.Configured {
		t.Fatalf("payload = %+v", payload)
	}
	if len(payload.Stores) != 1 || payload.Stores[0].Name != "app" || payload.Stores[0].Kind != "local" {
		t.Fatalf("stores = %+v", payload.Stores)
	}
}

func TestRunStorageWebUIReportsNoManagedUI(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {"stores": {"app": {"kind": "local"}}}
	}`)
	var out bytes.Buffer
	if err := runStorageCommand([]string{"webui", "-o", "json", "--app-root", root}, &out); err != nil {
		t.Fatalf("runStorageCommand(webui) error = %v", err)
	}
	var payload storageWebUIResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON(webui) error = %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.storage.webui" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.storage.webui").SchemaRevision || !payload.Configured || payload.Ready || payload.Reason == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunStorageObjectCommands(t *testing.T) {
	agentHome := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {
			"default": "app",
			"stores": {
				"app": {"kind": "local", "access": "auth"}
			}
		}
	}`)
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("storage report"), 0o644); err != nil {
		t.Fatal(err)
	}

	var putOut bytes.Buffer
	if err := runStorageCommand([]string{"put", "app", "reports/report.txt", source, "-o", "json", "--app-root", root}, &putOut); err != nil {
		t.Fatalf("storage put error = %v", err)
	}
	var putPayload storageObjectResponse
	if err := decodeCLIJSON(putOut.Bytes(), &putPayload); err != nil {
		t.Fatalf("unmarshal put: %v\n%s", err, putOut.String())
	}
	if putPayload.Kind != "scenery.storage.object" || putPayload.SchemaRevision != newCLIPayloadIdentity("scenery.storage.object").SchemaRevision || putPayload.Object.Key != "reports/report.txt" || putPayload.Object.SizeBytes != int64(len("storage report")) {
		t.Fatalf("put payload = %+v", putPayload)
	}

	var listOut bytes.Buffer
	if err := runStorageCommand([]string{"ls", "app", "--prefix", "reports/", "-o", "json", "--app-root", root}, &listOut); err != nil {
		t.Fatalf("storage ls error = %v", err)
	}
	var listPayload storageListResponse
	if err := decodeCLIJSON(listOut.Bytes(), &listPayload); err != nil {
		t.Fatalf("unmarshal list: %v\n%s", err, listOut.String())
	}
	if len(listPayload.Page.Objects) != 1 || listPayload.Page.Objects[0].Key != "reports/report.txt" {
		t.Fatalf("list payload = %+v", listPayload)
	}

	var statOut bytes.Buffer
	if err := runStorageCommand([]string{"stat", "app", "reports/report.txt", "-o", "json", "--app-root", root}, &statOut); err != nil {
		t.Fatalf("storage stat error = %v", err)
	}
	var statPayload storageObjectResponse
	if err := decodeCLIJSON(statOut.Bytes(), &statPayload); err != nil {
		t.Fatalf("unmarshal stat: %v\n%s", err, statOut.String())
	}
	if statPayload.Object.SHA256 == "" || statPayload.Object.ETag == "" {
		t.Fatalf("stat payload missing hashes = %+v", statPayload)
	}

	target := filepath.Join(t.TempDir(), "download.txt")
	var getOut bytes.Buffer
	if err := runStorageCommand([]string{"get", "app", "reports/report.txt", "--output", target, "-o", "json", "--app-root", root}, &getOut); err != nil {
		t.Fatalf("storage get error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "storage report" {
		t.Fatalf("downloaded data = %q", got)
	}

	var rmOut bytes.Buffer
	if err := runStorageCommand([]string{"rm", "app", "reports/report.txt", "-o", "json", "--app-root", root}, &rmOut); err != nil {
		t.Fatalf("storage rm error = %v", err)
	}
	var rmPayload storageDeleteResponse
	if err := decodeCLIJSON(rmOut.Bytes(), &rmPayload); err != nil {
		t.Fatalf("unmarshal rm: %v\n%s", err, rmOut.String())
	}
	if !rmPayload.Deleted || rmPayload.Key != "reports/report.txt" {
		t.Fatalf("rm payload = %+v", rmPayload)
	}
}

func TestRunStoragePutHonorsMaxObjectBytes(t *testing.T) {
	agentHome := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {
			"default": "app",
			"stores": {
				"app": {"kind": "local", "access": "auth", "max_object_bytes": 4}
			}
		}
	}`)
	source := filepath.Join(t.TempDir(), "too-large.txt")
	if err := os.WriteFile(source, []byte("storage report"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runStorageCommand([]string{"put", "app", "reports/report.txt", source, "-o", "json", "--app-root", root}, &out)
	if err == nil || !strings.Contains(err.Error(), "max_object_bytes 4") {
		t.Fatalf("storage put error = %v, output = %s", err, out.String())
	}
}

func TestRunStorageCleanupDefaultsToDryRun(t *testing.T) {
	agentHome := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {"stores": {"app": {"kind": "local"}}}
	}`)
	cellRoot := filepath.Join(agentHome, "agent", "storage", "storageapp")
	if err := os.MkdirAll(cellRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStorageCommand([]string{"cleanup", "-o", "json", "--app-root", root}, &out); err != nil {
		t.Fatalf("storage cleanup dry-run error = %v", err)
	}
	var payload storageCleanupResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal cleanup: %v\n%s", err, out.String())
	}
	if !payload.DryRun || payload.Deleted || !payload.Exists || payload.CellRoot != cellRoot {
		t.Fatalf("cleanup payload = %+v", payload)
	}
	if _, err := os.Stat(cellRoot); err != nil {
		t.Fatalf("dry-run removed cell root: %v", err)
	}
}

func TestRunStorageCleanupYesRemovesCellRoot(t *testing.T) {
	agentHome := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {"stores": {"app": {"kind": "local"}}}
	}`)
	cellRoot := filepath.Join(agentHome, "agent", "storage", "storageapp")
	if err := os.MkdirAll(filepath.Join(cellRoot, "objects", "app"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStorageCommand([]string{"cleanup", "--yes", "-o", "json", "--app-root", root}, &out); err != nil {
		t.Fatalf("storage cleanup --yes error = %v", err)
	}
	var payload storageCleanupResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal cleanup: %v\n%s", err, out.String())
	}
	if payload.DryRun || !payload.Deleted || payload.Exists {
		t.Fatalf("cleanup payload = %+v", payload)
	}
	if _, err := os.Stat(cellRoot); !os.IsNotExist(err) {
		t.Fatalf("cleanup --yes did not remove cell root: %v", err)
	}
}

func TestStorageCapabilityEnvPointsAtSharedCell(t *testing.T) {
	agentHome := t.TempDir()
	cfg := appcfg.Config{
		Name: "storageapp",
		Envs: map[string]appcfg.EnvConfig{"local": {Default: true}, "production": {Deploy: &appcfg.EnvDeployConfig{}}},
		Storage: appcfg.StorageConfig{
			CellID:  "shared-cell",
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local", MaxObjectBytes: 100},
			},
		},
	}
	env, err := storageCapabilityEnv(cfg, &localagent.Session{SessionID: "dev", BaseAppID: "storageapp"}, nil, agentHome)
	if err != nil {
		t.Fatalf("storageCapabilityEnv returned error: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "SCENERY_STORAGE_CELL_ID=shared-cell") {
		t.Fatalf("env missing cell ID: %v", env)
	}
	if !strings.Contains(joined, `"kind":"`+storageconfig.RuntimeKind+`"`) ||
		!strings.Contains(joined, `"default":"app"`) ||
		!strings.Contains(joined, `"kind":"local"`) ||
		!strings.Contains(joined, `"max_object_bytes":100`) {
		t.Fatalf("env missing runtime storage config: %v", env)
	}
	if !strings.Contains(joined, filepath.Join(agentHome, "agent", "storage", "shared-cell", "objects", "app")) {
		t.Fatalf("env does not use shared storage-cell object root: %v", env)
	}
}

func TestStorageCapabilityEnvUsesProxyForSessionStateRoot(t *testing.T) {
	agentHome := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), ".scenery", "sessions", "dev")
	cfg := appcfg.Config{
		Name: "storageapp",
		Envs: map[string]appcfg.EnvConfig{"local": {Default: true}, "production": {Deploy: &appcfg.EnvDeployConfig{}}},
		Storage: appcfg.StorageConfig{
			CellID:  "shared-cell",
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local"},
			},
		},
	}
	env, err := storageCapabilityEnv(cfg, &localagent.Session{SessionID: "dev", BaseAppID: "storageapp", StateRoot: stateRoot}, nil, agentHome)
	if err != nil {
		t.Fatalf("storageCapabilityEnv returned error: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, `"kind":"proxy"`) || !strings.Contains(joined, `"proxy_socket":"`) {
		t.Fatalf("env missing proxy runtime config: %v", env)
	}
	if strings.Contains(joined, `"root":"`) {
		t.Fatalf("proxy runtime config should not expose object root: %v", env)
	}
}

func TestAppProcessEnvFailsClosedForStorageWithoutExplicitRuntimeConfig(t *testing.T) {
	t.Setenv(storageconfig.RuntimeConfigEnv, "")
	cfg := appcfg.Config{
		Name: "storageapp",
		Envs: map[string]appcfg.EnvConfig{"local": {Default: true}, "production": {Deploy: &appcfg.EnvDeployConfig{}}},
		Storage: appcfg.StorageConfig{
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local"},
			},
		},
	}
	_, err := appProcessEnv(t.TempDir(), cfg, "json", "production")
	if err == nil {
		t.Fatal("appProcessEnv succeeded without explicit storage runtime config")
	}
	if !strings.Contains(err.Error(), "headless runtimes require explicit "+storageconfig.RuntimeConfigEnv) ||
		!strings.Contains(err.Error(), "scenery up") {
		t.Fatalf("error = %v", err)
	}
}

func TestAppProcessEnvAcceptsExplicitProxyStorageRuntimeConfig(t *testing.T) {
	raw := runtimeStorageJSON(t, storageconfig.RuntimeConfig{ArtifactIdentity: storageconfig.NewRuntimeIdentity(), CellID: "prod-cell", Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "proxy", ProxySocket: "/tmp/storage.sock"}}})
	t.Setenv(storageconfig.RuntimeConfigEnv, raw)
	cfg := appcfg.Config{
		Name: "storageapp",
		Envs: map[string]appcfg.EnvConfig{"local": {Default: true}, "production": {Deploy: &appcfg.EnvDeployConfig{}}},
		Storage: appcfg.StorageConfig{
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local"},
			},
		},
	}
	env, err := appProcessEnv(t.TempDir(), cfg, "json", "production")
	if err != nil {
		t.Fatalf("appProcessEnv returned error: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, storageconfig.RuntimeConfigEnv+"="+raw) {
		t.Fatalf("env missing explicit runtime config: %v", env)
	}
}

func TestAppProcessEnvAcceptsExplicitLocalStorageRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	raw := runtimeStorageJSON(t, storageconfig.RuntimeConfig{ArtifactIdentity: storageconfig.NewRuntimeIdentity(), CellID: "prod-cell", Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "local", Root: dir}}})
	t.Setenv(storageconfig.RuntimeConfigEnv, raw)
	cfg := appcfg.Config{
		Name: "storageapp",
		Envs: map[string]appcfg.EnvConfig{"local": {Default: true}, "production": {Deploy: &appcfg.EnvDeployConfig{}}},
		Storage: appcfg.StorageConfig{
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local"},
			},
		},
	}
	env, err := appProcessEnv(t.TempDir(), cfg, "json", "production")
	if err != nil {
		t.Fatalf("appProcessEnv rejected explicit local storage runtime config: %v", err)
	}
	if !strings.Contains(strings.Join(env, "\n"), storageconfig.RuntimeConfigEnv+"="+raw) {
		t.Fatalf("env missing explicit local runtime config: %v", env)
	}
}

func TestAppProcessEnvRejectsRelativeLocalStorageRoot(t *testing.T) {
	raw := runtimeStorageJSON(t, storageconfig.RuntimeConfig{ArtifactIdentity: storageconfig.NewRuntimeIdentity(), CellID: "prod-cell", Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "local", Root: "relative/path"}}})
	t.Setenv(storageconfig.RuntimeConfigEnv, raw)
	cfg := appcfg.Config{
		Name: "storageapp",
		Envs: map[string]appcfg.EnvConfig{"local": {Default: true}, "production": {Deploy: &appcfg.EnvDeployConfig{}}},
		Storage: appcfg.StorageConfig{
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local"},
			},
		},
	}
	_, err := appProcessEnv(t.TempDir(), cfg, "json", "production")
	if err == nil {
		t.Fatal("appProcessEnv accepted relative local storage root")
	}
	if !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagedStorageProxyRoundTripThroughPublicStoragePackage(t *testing.T) {
	shortRoot, err := os.MkdirTemp("/tmp", "scn-storage-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortRoot) })
	agentHome := filepath.Join(shortRoot, "agent-home")
	stateRoot := filepath.Join(shortRoot, "session")
	cfg := appcfg.Config{
		Name: "storageapp",
		Storage: appcfg.StorageConfig{
			CellID:  "shared-cell",
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "local", MaxObjectBytes: 100},
			},
		},
	}
	session := &localagent.Session{SessionID: "dev", BaseAppID: "storageapp", StateRoot: stateRoot}
	plan, err := resolveStorageCellPlan(cfg, agentHome)
	if err != nil {
		t.Fatalf("resolveStorageCellPlan returned error: %v", err)
	}
	proxy, err := startManagedStorageProxy(context.Background(), cfg, session, plan)
	if err != nil {
		t.Fatalf("startManagedStorageProxy returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := proxy.Close(); err != nil {
			t.Fatalf("close storage proxy: %v", err)
		}
	})
	env, err := storageCapabilityEnv(cfg, session, nil, agentHome)
	if err != nil {
		t.Fatalf("storageCapabilityEnv returned error: %v", err)
	}
	t.Setenv(storageconfig.RuntimeConfigEnv, storageTestEnvValue(t, env, storageconfig.RuntimeConfigEnv))
	store, err := publicstorage.Default(context.Background())
	if err != nil {
		t.Fatalf("storage.Default returned error: %v", err)
	}
	if _, err := store.Put(context.Background(), "reports/report.txt", strings.NewReader("storage report"), publicstorage.PutOptions{
		ContentType: "application/x-report",
		Metadata:    map[string]string{"source": "proxy"},
	}); err != nil {
		t.Fatalf("proxy put returned error: %v", err)
	}
	page, err := store.List(context.Background(), publicstorage.ListOptions{Prefix: "reports/"})
	if err != nil {
		t.Fatalf("proxy list returned error: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "reports/report.txt" || page.Objects[0].Metadata["Source"] != "proxy" {
		t.Fatalf("proxy list page = %+v", page)
	}
	head, err := store.Head(context.Background(), "reports/report.txt")
	if err != nil {
		t.Fatalf("proxy head returned error: %v", err)
	}
	if head.SizeBytes != int64(len("storage report")) || head.ContentType != "application/x-report" || head.Metadata["Source"] != "proxy" {
		t.Fatalf("proxy head = %+v", head)
	}
	body, obj, err := store.Get(context.Background(), "reports/report.txt", publicstorage.GetOptions{})
	if err != nil {
		t.Fatalf("proxy get returned error: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "storage report" || obj.Key != "reports/report.txt" || obj.Metadata["Source"] != "proxy" {
		t.Fatalf("proxy get data=%q object=%+v", data, obj)
	}
	// Objects are plain files under the cell's per-store object root.
	if _, err := os.Stat(filepath.Join(plan.ObjectsDir, "app", "reports", "report.txt")); err != nil {
		t.Fatalf("expected object file under cell object root: %v", err)
	}
	assertStorageProxyConcurrentIfNoneMatch(t, store)
	if err := store.Delete(context.Background(), "reports/report.txt"); err != nil {
		t.Fatalf("proxy delete returned error: %v", err)
	}
	if _, err := store.Head(context.Background(), "reports/report.txt"); err == nil {
		t.Fatal("proxy head succeeded after delete")
	}
}

func assertStorageProxyConcurrentIfNoneMatch(t *testing.T, store publicstorage.Store) {
	t.Helper()
	var success int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Put(context.Background(), "reports/once.txt", strings.NewReader("once"), publicstorage.PutOptions{IfNoneMatch: true})
			if err == nil {
				atomic.AddInt32(&success, 1)
				return
			}
			var exists *publicstorage.AlreadyExistsError
			if !errors.As(err, &exists) {
				t.Errorf("proxy Put IfNoneMatch error = %T %[1]v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if success != 1 {
		t.Fatalf("successful proxy IfNoneMatch writes = %d, want 1", success)
	}
}

func TestStorageProxySocketPathFallsBackToShortTempPath(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(t.TempDir(), strings.Repeat("long-session-component-", 5))
	path := storageProxySocketPath(&localagent.Session{
		SessionID: strings.Repeat("feature-branch-", 8),
		AppRoot:   filepath.Join(t.TempDir(), strings.Repeat("long-app-root-", 4)),
		StateRoot: stateRoot,
	})
	if !strings.HasPrefix(path, filepath.Join(os.TempDir(), "scenery-storage-")) {
		t.Fatalf("fallback storage proxy path = %q, want temp scenery-storage path", path)
	}
	if len(path) > 100 {
		t.Fatalf("fallback storage proxy path length = %d, want <= 100: %q", len(path), path)
	}
}

func storageTestEnvValue(t *testing.T, env []string, key string) string {
	t.Helper()
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	t.Fatalf("env missing %s: %+v", key, env)
	return ""
}

func TestParseStorageArgs(t *testing.T) {
	t.Parallel()
	opts, err := parseStorageArgs([]string{"status", "-o", "json", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseStorageArgs returned error: %v", err)
	}
	if opts.Command != "status" || !opts.JSON || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	opts, err = parseStorageArgs([]string{"ls", "app", "--prefix", "reports/", "--limit", "10", "-o", "json"})
	if err != nil {
		t.Fatalf("parseStorageArgs ls returned error: %v", err)
	}
	if opts.Command != "ls" || opts.Store != "app" || opts.Prefix != "reports/" || opts.Limit != 10 || !opts.JSON {
		t.Fatalf("ls opts = %+v", opts)
	}
	if _, err := parseStorageArgs([]string{"ls"}); err == nil {
		t.Fatal("parseStorageArgs accepted unsupported command")
	}
}
