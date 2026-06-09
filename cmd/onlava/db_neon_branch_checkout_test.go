package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBBranchStatusReadsWorktreePin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
		"name": "branchapp",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"isolation": "branch",
					"project": "branchapp",
					"database_url_env": "DatabaseURL"
				}
			}
		}
	}`)
	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-selfhost",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "branchapp/feature",
		"branch_id": "br-local-test",
		"database": "branchapp",
		"role": "cloud_admin",
		"worktree_root": "`+root+`",
		"created_by": "onlava",
		"ttl": "168h"
	}`)

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"status", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBBranchCommand status returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode branch status JSON: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != dbBranchStatusSchemaVersion || payload.Status != "pinned" || payload.BackendStatus != "missing" || payload.Pin == nil {
		t.Fatalf("payload = %+v", payload)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.branch.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("branch status schema diagnostics = %+v", diagnostics)
	}
	if payload.Pin.Branch != "branchapp/feature" || payload.DatabaseURLEnv != "DatabaseURL" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBBranchStatusReportsExpiredLease(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", "feature/old", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"expire", "feature/old", "--after", "-1h", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("expire returned error: %v", err)
	}
	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"status", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "expired" || !strings.Contains(payload.BackendMessage, "expired") {
		t.Fatalf("payload = %+v", payload)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.branch.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("branch status schema diagnostics = %+v", diagnostics)
	}
}

func TestDBBranchListMatchesSchema(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBBranchCommand list returned error: %v", err)
	}
	var payload dbBranchListResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode branch list JSON: %v\n%s", err, out.String())
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.branch.list.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("branch list schema diagnostics = %+v", diagnostics)
	}
}

func TestDBBranchListIgnoresForeignLease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	foreignPin := worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfhostProvider,
		Project:       "branchapp",
		ParentBranch:  "main",
		Branch:        "feature/foreign",
		BranchID:      "br-foreign",
		Database:      "branchapp",
		Role:          "cloud_admin",
		CreatedBy:     "external",
	}
	if err := writeNeonBranchRegistry(filepath.Join(home, "agent", "substrates", "neon"), neonBranchRegistry{
		Leases: []neonBranchLease{{
			Pin:    foreignPin,
			Status: "ready",
			Endpoint: &neonEndpoint{
				Host:     "127.0.0.1",
				Port:     55432,
				Database: "branchapp",
				Role:     "cloud_admin",
			},
		}},
	}); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBBranchCommand list returned error: %v", err)
	}
	var payload dbBranchListResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode branch list JSON: %v\n%s", err, out.String())
	}
	if len(payload.Branches) != 0 || len(payload.Leases) != 0 {
		t.Fatalf("payload exposed foreign lease = %+v", payload)
	}
	if !strings.Contains(payload.Message, "No Onlava-owned") {
		t.Fatalf("message = %q", payload.Message)
	}
}

func TestDBBranchCheckoutWritesPinnedBranch(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
		"name": "Branch App",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"isolation": "branch",
					"project": "Branch App",
					"parent_branch": "main",
					"database": "Branch App",
					"role": "cloud_admin"
				}
			}
		}
	}`)

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "Feature/New Thing", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBBranchCommand checkout returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode branch checkout JSON: %v\n%s", err, out.String())
	}
	if payload.Status != "pinned" || payload.Pin == nil {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.BackendStatus != "missing" || !strings.Contains(payload.BackendMessage, "dev-cell is not installed") {
		t.Fatalf("backend status = %q, message = %q", payload.BackendStatus, payload.BackendMessage)
	}
	if payload.Pin.Branch != "feature/new-thing" || payload.Pin.Project != "branch-app" || !strings.HasPrefix(payload.Pin.BranchID, "br-local-") {
		t.Fatalf("pin = %+v", payload.Pin)
	}
	if _, err := os.Stat(filepath.Join(root, ".onlava", ".gitignore")); err != nil {
		t.Fatalf("local state gitignore missing: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".onlava", "worktree-db.json"))
	if err != nil {
		t.Fatalf("read pin: %v", err)
	}
	if !bytes.Contains(data, []byte(`"branch": "feature/new-thing"`)) {
		t.Fatalf("pin file = %s", data)
	}
}

func TestDBBranchCheckoutRefusesForeignLease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	foreignPin := worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfhostProvider,
		Project:       "branchapp",
		ParentBranch:  "main",
		Branch:        "feature/foreign",
		BranchID:      "br-foreign",
		Database:      "branchapp",
		Role:          "cloud_admin",
		CreatedBy:     "external",
	}
	if err := writeNeonBranchRegistry(filepath.Join(home, "agent", "substrates", "neon"), neonBranchRegistry{
		Leases: []neonBranchLease{{Pin: foreignPin, Status: "pending"}},
	}); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	var out bytes.Buffer
	err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "feature/foreign", "--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), "refusing to reuse foreign local Neon branch lease") {
		t.Fatalf("checkout error = %v output=%s", err, out.String())
	}
	if _, statErr := os.Stat(worktreeDBPinPath(root)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("worktree pin stat err = %v", statErr)
	}
}

func TestDBBranchStatusReportsReadyEndpointWithoutURL(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", "feature/ready", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read pin ok=%v err=%v", ok, err)
	}
	markNeonLeaseReadyForTest(t, pin, neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55432,
		Database: "branchapp",
		Role:     "cloud_admin",
		SSLMode:  "disable",
		Source:   "cargo-neon",
	})

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"status", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "ready" || payload.Connection == nil {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Connection.Host != "127.0.0.1" || payload.Connection.Port != 55432 || payload.Connection.Database != "branchapp" {
		t.Fatalf("connection = %+v", payload.Connection)
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatalf("status leaked connection URL: %s", out.String())
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.branch.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("branch status schema diagnostics = %+v", diagnostics)
	}

	out.Reset()
	if err := runDBBranchCommand(t.Context(), &out, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	var listed dbBranchListResult
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, out.String())
	}
	if len(listed.Leases) != 1 || listed.Leases[0].Status != "ready" || listed.Leases[0].Endpoint == nil {
		t.Fatalf("listed = %+v", listed)
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatalf("list leaked connection URL: %s", out.String())
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.branch.list.v1.schema.json"), listed); len(diagnostics) != 0 {
		t.Fatalf("branch list schema diagnostics = %+v", diagnostics)
	}
}

func TestDBBranchStatusProtectsReadyParentBranch(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp","parent_branch":"main"}}}}`)
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read pin ok=%v err=%v", ok, err)
	}
	markNeonLeaseReadyForTest(t, pin, neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55432,
		Database: "branchapp",
		Role:     "cloud_admin",
		SSLMode:  "disable",
		Source:   "test",
	})

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"status", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "protected" || !strings.Contains(payload.BackendMessage, "protected parent branch") {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Connection != nil {
		t.Fatalf("protected parent status exposed connection: %+v", payload.Connection)
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatalf("status leaked connection URL: %s", out.String())
	}

	out.Reset()
	if err := runDBBranchCommand(t.Context(), &out, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	var listed dbBranchListResult
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, out.String())
	}
	if len(listed.Leases) != 1 || listed.Leases[0].Status != "protected" {
		t.Fatalf("listed = %+v", listed)
	}
	if listed.Leases[0].Endpoint != nil {
		t.Fatalf("protected parent list exposed endpoint: %+v", listed.Leases[0].Endpoint)
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatalf("list leaked connection URL: %s", out.String())
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.branch.list.v1.schema.json"), listed); len(diagnostics) != 0 {
		t.Fatalf("branch list schema diagnostics = %+v", diagnostics)
	}
}

func TestDBBranchCheckoutReportsPendingWhenDevCellInstalled(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	useMissingNeonDocker(t)
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install returned error: %v", err)
	}

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "feature/pending", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode checkout JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "pending" ||
		!strings.Contains(payload.BackendMessage, "Neon dev-cell is installed") ||
		strings.Contains(payload.BackendMessage, "not implemented") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestNeonSelfhostPendingBranchStatusReportsMissingDriverWhenCellReady(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDocker := filepath.Join(bin, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
if [ "$1" = "version" ]; then
  echo "29.0.0"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "[]"
  exit 0
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-minio\tUp 2 minutes (health: healthy)\n'
  printf 'onlava-neon-bucket-init\tExited (0) 1 minute ago\n'
  printf 'onlava-neon-pageserver\tUp 2 minutes\n'
  printf 'onlava-neon-safekeeper-1\tUp 2 minutes\n'
  printf 'onlava-neon-safekeeper-2\tUp 2 minutes\n'
  printf 'onlava-neon-safekeeper-3\tUp 2 minutes\n'
  printf 'onlava-neon-storage-broker\tUp 2 minutes\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install returned error: %v", err)
	}
	root := filepath.Join(home, "agent", "substrates", "neon")
	state, ok, err := readNeonCellState(root)
	if err != nil || !ok {
		t.Fatalf("read state ok=%v err=%v", ok, err)
	}
	for key := range state.Ports {
		state.Ports[key] = port
	}
	if err := writeNeonCellState(state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	status := neonSelfhostPendingBranchStatus(t.Context())
	if status.Status != "pending" ||
		!strings.Contains(status.Message, "no Neon branch driver is configured") ||
		!strings.Contains(status.Message, neonSelfhostBranchDriverEnv) ||
		strings.Contains(status.Message, "not implemented") {
		t.Fatalf("status = %+v", status)
	}
}

func TestDBBranchCheckoutAndDeleteUseConfiguredBranchDriver(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	binDir := t.TempDir()
	driverLog := filepath.Join(binDir, "driver.log")
	driver := filepath.Join(binDir, "fake-driver")
	if err := os.WriteFile(driver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DRIVER_LOG"
case "$1" in
  ensure|reset|restore)
    printf '{"status":"ready","message":"driver marked branch ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"branchapp","role":"cloud_admin","sslmode":"disable","source":"fake-driver"}}\n'
    exit 0
    ;;
  delete)
    printf '{"status":"deleted","message":"driver deleted branch"}\n'
    exit 0
    ;;
esac
echo "unexpected driver action $1" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRIVER_LOG", driverLog)
	t.Setenv(localPostgresBranchDriverEnv, driver)

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "feature/driver", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode checkout JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "ready" || payload.Connection == nil || payload.Connection.Source != "fake-driver" {
		t.Fatalf("payload = %+v", payload)
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatalf("checkout leaked connection URL: %s", out.String())
	}
	logBytes, err := os.ReadFile(driverLog)
	if err != nil {
		t.Fatalf("read driver log: %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{"ensure", "--project branchapp", "--parent-branch main", "--branch feature/driver", "--database branchapp", "--role cloud_admin", "--ttl 168h", "--json"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("driver log missing %q in %q", want, logText)
		}
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {

		t.Fatalf("read registry: %v", err)
	}
	if len(registry.Leases) != 1 || registry.Leases[0].Status != "ready" || registry.Leases[0].Endpoint == nil {
		t.Fatalf("registry = %+v", registry.Leases)
	}

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/driver", "--app-root", root, "--force"}); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	logBytes, err = os.ReadFile(driverLog)
	if err != nil {
		t.Fatalf("read driver log after delete: %v", err)
	}
	if !strings.Contains(string(logBytes), "delete") || !strings.Contains(string(logBytes), "--branch feature/driver") {
		t.Fatalf("delete did not call driver, log=%q", string(logBytes))
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry after delete: %v", err)
	}
	if len(registry.Leases) != 0 {
		t.Fatalf("registry after delete = %+v", registry.Leases)
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); !os.IsNotExist(err) {
		t.Fatalf("current delete should remove worktree pin, stat err=%v", err)
	}
}

func TestDBBranchCheckoutPrefersNeonSelfhostDriver(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	binDir := t.TempDir()
	selfhostLog := filepath.Join(binDir, "selfhost.log")
	selfhostDriver := filepath.Join(binDir, "fake-neon-selfhost-driver")
	if err := os.WriteFile(selfhostDriver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$SELFHOST_DRIVER_LOG"
case "$1" in
  ensure|reset|restore)
    printf '{"status":"ready","message":"selfhost driver marked branch ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"branchapp","role":"cloud_admin","sslmode":"disable"}}\n'
    exit 0
    ;;
  delete)
    printf '{"status":"deleted","message":"selfhost driver deleted branch"}\n'
    exit 0
    ;;
esac
echo "unexpected neon-selfhost driver action $1" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	localLog := filepath.Join(binDir, "local.log")
	localDriver := filepath.Join(binDir, "fake-local-postgres-branch-driver")
	if err := os.WriteFile(localDriver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$LOCAL_DRIVER_LOG"
printf '{"status":"ready","message":"local driver should not be selected","endpoint":{"host":"127.0.0.1","port":55434,"database":"branchapp","role":"cloud_admin","sslmode":"disable"}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SELFHOST_DRIVER_LOG", selfhostLog)
	t.Setenv("LOCAL_DRIVER_LOG", localLog)
	t.Setenv(neonSelfhostBranchDriverEnv, selfhostDriver)
	t.Setenv(localPostgresBranchDriverEnv, localDriver)

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "feature/selfhost", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode checkout JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "ready" || payload.Connection == nil || payload.Connection.Source != neonSelfhostBranchDriverEndpointSource {
		t.Fatalf("payload = %+v", payload)
	}
	selfhostLogBytes, err := os.ReadFile(selfhostLog)
	if err != nil {
		t.Fatalf("read selfhost driver log: %v", err)
	}
	if !strings.Contains(string(selfhostLogBytes), "ensure") || !strings.Contains(string(selfhostLogBytes), "--branch feature/selfhost") {
		t.Fatalf("selfhost driver log = %q", string(selfhostLogBytes))
	}
	if localLogBytes, err := os.ReadFile(localLog); err == nil && strings.TrimSpace(string(localLogBytes)) != "" {
		t.Fatalf("local fallback driver was called: %q", string(localLogBytes))
	}
}

func TestDBBranchCheckoutUsesCellDriverPathBeforeLocalFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	binDir := t.TempDir()
	cellLog := filepath.Join(binDir, "cell.log")
	cellDriver := filepath.Join(binDir, "fake-cell-neon-selfhost-driver")
	if err := os.WriteFile(cellDriver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CELL_DRIVER_LOG"
printf 'root=%s\n' "$ONLAVA_NEON_SELFHOST_ROOT" >> "$CELL_DRIVER_LOG"
printf '{"status":"ready","message":"cell driver marked branch ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"branchapp","role":"cloud_admin","sslmode":"disable"}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	localLog := filepath.Join(binDir, "local.log")
	localDriver := filepath.Join(binDir, "fake-local-postgres-branch-driver")
	if err := os.WriteFile(localDriver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$LOCAL_DRIVER_LOG"
printf '{"status":"ready","message":"local driver should not be selected","endpoint":{"host":"127.0.0.1","port":55434,"database":"branchapp","role":"cloud_admin","sslmode":"disable"}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CELL_DRIVER_LOG", cellLog)
	t.Setenv("LOCAL_DRIVER_LOG", localLog)
	t.Setenv(localPostgresBranchDriverEnv, localDriver)

	neonRoot := filepath.Join(home, "agent", "substrates", "neon")
	state := defaultNeonCellState(neonRoot, "installed")
	state.Driver = &neonCellDriver{
		Kind:    "toolchain",
		Tool:    neonSelfhostDriverToolchainArtifact,
		Path:    cellDriver,
		Version: "dev",
		Status:  "installed",
	}
	if err := writeNeonCellState(state); err != nil {
		t.Fatalf("write cell state: %v", err)
	}

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "feature/cell", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode checkout JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "ready" || payload.Connection == nil || payload.Connection.Source != neonSelfhostBranchDriverEndpointSource {
		t.Fatalf("payload = %+v", payload)
	}
	cellLogBytes, err := os.ReadFile(cellLog)
	if err != nil {
		t.Fatalf("read cell driver log: %v", err)
	}
	if !strings.Contains(string(cellLogBytes), "ensure") || !strings.Contains(string(cellLogBytes), "--branch feature/cell") || !strings.Contains(string(cellLogBytes), "root="+neonRoot) {
		t.Fatalf("cell driver log = %q", string(cellLogBytes))
	}
	if localLogBytes, err := os.ReadFile(localLog); err == nil && strings.TrimSpace(string(localLogBytes)) != "" {
		t.Fatalf("local fallback driver was called: %q", string(localLogBytes))
	}
}

func TestDBBranchDriverRestoreDiffAndRestorePoints(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	binDir := t.TempDir()
	driverLog := filepath.Join(binDir, "driver.log")
	driver := filepath.Join(binDir, "fake-driver")
	if err := os.WriteFile(driver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DRIVER_LOG"
case "$1" in
  ensure|reset|restore)
    printf '{"status":"ready","message":"driver marked branch ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"branchapp","role":"cloud_admin","sslmode":"disable","source":"fake-driver"}}\n'
    exit 0
    ;;
  diff)
    printf '%s\n' '{"diff":"--- branchapp/feature\\n+++ main\\n"}'
    exit 0
    ;;
esac
echo "unexpected driver action $1" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRIVER_LOG", driverLog)
	t.Setenv(localPostgresBranchDriverEnv, driver)

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", "branchapp/feature", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read worktree pin ok=%v err=%v", ok, err)
	}
	state, _, err := readNeonRestorePointsState()
	if err != nil {
		t.Fatalf("read restore points: %v", err)
	}
	points := state.Points[pin.BranchID]
	if len(points) != 1 || points[0].Source != "branch-created" {
		t.Fatalf("restore points after checkout = %+v", points)
	}
	firstRef := points[0].Ref

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"}); err != nil {
		t.Fatalf("reset returned error: %v", err)
	}
	var restoreOut bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &restoreOut, []string{"restore", "--at", firstRef, "--app-root", root, "--yes", "--json"}); err != nil {
		t.Fatalf("restore returned error: %v", err)
	}
	var restorePayload struct {
		SchemaVersion string                 `json:"schema_version"`
		RestorePoint  neonBranchRestorePoint `json:"restore_point"`
		Status        string                 `json:"status"`
	}
	if err := json.Unmarshal(restoreOut.Bytes(), &restorePayload); err != nil {
		t.Fatalf("decode restore JSON: %v\n%s", err, restoreOut.String())
	}
	if restorePayload.SchemaVersion != "onlava.db.branch.restore.v1" ||
		restorePayload.Status != "restored" ||
		restorePayload.RestorePoint.Source != "branch-restore" ||
		restorePayload.RestorePoint.RestoredFrom != firstRef {
		t.Fatalf("restore payload = %+v", restorePayload)
	}
	state, _, err = readNeonRestorePointsState()
	if err != nil {
		t.Fatalf("read restore points after restore: %v", err)
	}
	points = state.Points[pin.BranchID]
	if len(points) != 3 || points[len(points)-1].Source != "branch-restore" || points[len(points)-1].RestoredFrom != firstRef {
		t.Fatalf("restore points after restore = %+v", points)
	}
	arbitraryRef := "0/16B6C50"
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", arbitraryRef, "--app-root", root, "--yes", "--json"}); err != nil {
		t.Fatalf("restore arbitrary ref returned error: %v", err)
	}
	state, _, err = readNeonRestorePointsState()
	if err != nil {
		t.Fatalf("read restore points after arbitrary restore: %v", err)
	}
	points = state.Points[pin.BranchID]
	if len(points) != 4 || points[len(points)-1].Source != "branch-restore" || points[len(points)-1].RestoredFrom != arbitraryRef {
		t.Fatalf("restore points after arbitrary restore = %+v", points)
	}

	var diffOut bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &diffOut, []string{"diff", "main", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("diff returned error: %v", err)
	}
	var diffPayload struct {
		SchemaVersion string `json:"schema_version"`
		Diff          string `json:"diff"`
	}
	if err := json.Unmarshal(diffOut.Bytes(), &diffPayload); err != nil {
		t.Fatalf("decode diff JSON: %v\n%s", err, diffOut.String())
	}
	if diffPayload.SchemaVersion != "onlava.db.branch.diff.v1" || !strings.Contains(diffPayload.Diff, "+++ main") {
		t.Fatalf("diff payload = %+v", diffPayload)
	}
	if strings.Contains(restoreOut.String(), "postgres://") || strings.Contains(diffOut.String(), "postgres://") {
		t.Fatalf("branch JSON leaked connection URL: restore=%s diff=%s", restoreOut.String(), diffOut.String())
	}
	logBytes, err := os.ReadFile(driverLog)
	if err != nil {
		t.Fatalf("read driver log: %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{"restore", "--at " + firstRef, "diff", "--target main"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("driver log missing %q in %q", want, logText)
		}
	}
}
