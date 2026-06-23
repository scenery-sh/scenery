package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/localfs"
	"github.com/hugelgupf/p9/p9"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storageconfig"
	publicstorage "scenery.sh/storage"
)

func TestRunStorageStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {
			"default": "app",
			"stores": {
				"app": {"kind": "zerofs", "access": "auth"}
			}
		},
		"dev": {"services": {"storage": {"kind": "zerofs", "mode": "local"}}}
	}`)
	var out bytes.Buffer
	if err := runStorageCommand([]string{"status", "--json", "--app-root", root}, &out); err != nil {
		t.Fatalf("runStorageCommand(status) error = %v", err)
	}
	var payload storageStatusResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(status) error = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.storage.status.v1" || !payload.Storage.Configured || payload.Storage.Readiness != "configured" {
		t.Fatalf("payload = %+v", payload)
	}
	if len(payload.Stores) != 1 || payload.Stores[0].Name != "app" {
		t.Fatalf("stores = %+v", payload.Stores)
	}
}

func TestRunStorageWebUIReportsMissingRuntimeRoute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {"stores": {"app": {"kind": "zerofs"}}},
		"dev": {"services": {"storage": {"kind": "zerofs"}}}
	}`)
	var out bytes.Buffer
	if err := runStorageCommand([]string{"webui", "--json", "--app-root", root}, &out); err != nil {
		t.Fatalf("runStorageCommand(webui) error = %v", err)
	}
	var payload storageWebUIResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(webui) error = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.storage.webui.v1" || !payload.Configured || payload.Ready || payload.Reason == "" {
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
				"app": {"kind": "zerofs", "access": "auth"}
			}
		},
		"dev": {"services": {"storage": {"kind": "zerofs"}}}
	}`)
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("storage report"), 0o644); err != nil {
		t.Fatal(err)
	}

	var putOut bytes.Buffer
	if err := runStorageCommand([]string{"put", "app", "reports/report.txt", source, "--json", "--app-root", root}, &putOut); err != nil {
		t.Fatalf("storage put error = %v", err)
	}
	var putPayload storageObjectResponse
	if err := json.Unmarshal(putOut.Bytes(), &putPayload); err != nil {
		t.Fatalf("unmarshal put: %v\n%s", err, putOut.String())
	}
	if putPayload.SchemaVersion != "scenery.storage.object.v1" || putPayload.Object.Key != "reports/report.txt" || putPayload.Object.SizeBytes != int64(len("storage report")) {
		t.Fatalf("put payload = %+v", putPayload)
	}

	var listOut bytes.Buffer
	if err := runStorageCommand([]string{"ls", "app", "--prefix", "reports/", "--json", "--app-root", root}, &listOut); err != nil {
		t.Fatalf("storage ls error = %v", err)
	}
	var listPayload storageListResponse
	if err := json.Unmarshal(listOut.Bytes(), &listPayload); err != nil {
		t.Fatalf("unmarshal list: %v\n%s", err, listOut.String())
	}
	if len(listPayload.Page.Objects) != 1 || listPayload.Page.Objects[0].Key != "reports/report.txt" {
		t.Fatalf("list payload = %+v", listPayload)
	}

	var statOut bytes.Buffer
	if err := runStorageCommand([]string{"stat", "app", "reports/report.txt", "--json", "--app-root", root}, &statOut); err != nil {
		t.Fatalf("storage stat error = %v", err)
	}
	var statPayload storageObjectResponse
	if err := json.Unmarshal(statOut.Bytes(), &statPayload); err != nil {
		t.Fatalf("unmarshal stat: %v\n%s", err, statOut.String())
	}
	if statPayload.Object.SHA256 == "" || statPayload.Object.ETag == "" {
		t.Fatalf("stat payload missing hashes = %+v", statPayload)
	}

	target := filepath.Join(t.TempDir(), "download.txt")
	var getOut bytes.Buffer
	if err := runStorageCommand([]string{"get", "app", "reports/report.txt", "--output", target, "--json", "--app-root", root}, &getOut); err != nil {
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
	if err := runStorageCommand([]string{"rm", "app", "reports/report.txt", "--json", "--app-root", root}, &rmOut); err != nil {
		t.Fatalf("storage rm error = %v", err)
	}
	var rmPayload storageDeleteResponse
	if err := json.Unmarshal(rmOut.Bytes(), &rmPayload); err != nil {
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
				"app": {"kind": "zerofs", "access": "auth", "max_object_bytes": 4}
			}
		},
		"dev": {"services": {"storage": {"kind": "zerofs"}}}
	}`)
	source := filepath.Join(t.TempDir(), "too-large.txt")
	if err := os.WriteFile(source, []byte("storage report"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runStorageCommand([]string{"put", "app", "reports/report.txt", source, "--json", "--app-root", root}, &out)
	if err == nil || !strings.Contains(err.Error(), "max_object_bytes 4") {
		t.Fatalf("storage put error = %v, output = %s", err, out.String())
	}
}

func TestStorageCapabilityEnvPointsAtSharedCell(t *testing.T) {
	agentHome := t.TempDir()
	cfg := appcfg.Config{
		Name: "storageapp",
		Storage: appcfg.StorageConfig{
			CellID:  "shared-cell",
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "zerofs", MaxObjectBytes: 100},
			},
		},
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"storage": {Kind: "zerofs"},
		}},
	}
	env, err := storageCapabilityEnv(cfg, &localagent.Session{SessionID: "dev", BaseAppID: "storageapp"}, nil, agentHome)
	if err != nil {
		t.Fatalf("storageCapabilityEnv returned error: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "SCENERY_STORAGE_CELL_ID=shared-cell") {
		t.Fatalf("env missing cell ID: %v", env)
	}
	if !strings.Contains(joined, `"schema_version":"`+storageconfig.RuntimeSchemaVersion+`"`) ||
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
		Storage: appcfg.StorageConfig{
			CellID:  "shared-cell",
			Default: "app",
			Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "zerofs"},
			},
		},
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"storage": {Kind: "zerofs"},
		}},
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
				"app": {Kind: "zerofs", MaxObjectBytes: 100},
			},
		},
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"storage": {Kind: "zerofs"},
		}},
	}
	session := &localagent.Session{SessionID: "dev", BaseAppID: "storageapp", StateRoot: stateRoot}
	plan, err := resolveManagedZeroFSPlan(cfg, session, nil, agentHome)
	if err != nil {
		t.Fatalf("resolveManagedZeroFSPlan returned error: %v", err)
	}
	startStorageP9Server(t, plan.NinePSocket, plan.ObjectsDir)
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
	if _, err := store.Put(context.Background(), "reports/report.txt", strings.NewReader("storage report"), publicstorage.PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatalf("proxy put returned error: %v", err)
	}
	page, err := store.List(context.Background(), publicstorage.ListOptions{Prefix: "reports/"})
	if err != nil {
		t.Fatalf("proxy list returned error: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "reports/report.txt" {
		t.Fatalf("proxy list page = %+v", page)
	}
	head, err := store.Head(context.Background(), "reports/report.txt")
	if err != nil {
		t.Fatalf("proxy head returned error: %v", err)
	}
	if head.SizeBytes != int64(len("storage report")) {
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
	if string(data) != "storage report" || obj.Key != "reports/report.txt" {
		t.Fatalf("proxy get data=%q object=%+v", data, obj)
	}
	if err := store.Delete(context.Background(), "reports/report.txt"); err != nil {
		t.Fatalf("proxy delete returned error: %v", err)
	}
	if _, err := store.Head(context.Background(), "reports/report.txt"); err == nil {
		t.Fatal("proxy head succeeded after delete")
	}
}

func startStorageP9Server(t *testing.T, socketPath, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen p9 socket: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := p9.NewServer(localfs.Attacher(root))
	done := make(chan error, 1)
	go func() {
		done <- server.ServeContext(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("p9 server did not stop")
		}
		_ = os.Remove(socketPath)
	})
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
	opts, err := parseStorageArgs([]string{"status", "--json", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseStorageArgs returned error: %v", err)
	}
	if opts.Command != "status" || !opts.JSON || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	opts, err = parseStorageArgs([]string{"ls", "app", "--prefix", "reports/", "--limit", "10", "--json"})
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
