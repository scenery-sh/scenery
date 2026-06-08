package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
)

var neonDockerCommandTestMu sync.Mutex

func useFakeNeonDocker(t *testing.T, path string) {
	t.Helper()
	neonDockerCommandTestMu.Lock()
	previousDockerCommand := neonDockerCommand
	neonDockerCommand = path
	t.Cleanup(func() {
		neonDockerCommand = previousDockerCommand
		neonDockerCommandTestMu.Unlock()
	})
}

func TestParseDBNeonArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDBNeonArgs([]string{"start", "--json", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseDBNeonArgs returned error: %v", err)
	}
	if opts.Command != "start" || !opts.JSON || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDBNeonArgs([]string{"status", "--bogus"}); err == nil || err.Error() != `unknown flag "--bogus"` {
		t.Fatalf("unknown flag error = %v", err)
	}
}

func TestDBNeonInstallWritesGeneratedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode install JSON: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != neonStatusSchemaVersion || !payload.OK || payload.Status != "installed" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Ports["compute_postgres"] != 55433 || payload.Cell == nil || payload.Cell.Ports["pageserver_http"] != 55434 {
		t.Fatalf("ports = result:%+v cell:%+v", payload.Ports, payload.Cell)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.neon.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("neon status schema diagnostics = %+v", diagnostics)
	}
	root := filepath.Join(home, "agent", "substrates", "neon")
	for _, rel := range []string{"cell.json", "compose.generated.yml"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("generated %s missing: %v", rel, err)
		}
	}
	if !strings.Contains(payload.Message, "onlava db neon start") {
		t.Fatalf("message = %q", payload.Message)
	}
	compose, err := os.ReadFile(filepath.Join(root, "compose.generated.yml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	if !bytes.Contains(compose, []byte(`127.0.0.1:55433:5432`)) {
		t.Fatalf("compose missing compute port: %s", compose)
	}
}

func TestDBNeonStatusProbesDockerHealth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

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
  case "$3" in
    ghcr.io/neondatabase/neon:latest|ghcr.io/neondatabase/compute-node-v16:latest)
      echo "[]"
      exit 0
      ;;
    *)
      echo "missing image" >&2
      exit 1
      ;;
  esac
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-minio\tUp 2 minutes (health: healthy)\n'
  printf 'onlava-neon-compute\tExited (1) 1 minute ago\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"status", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand status returned error: %v", err)
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, out.String())
	}
	if payload.OK || payload.Status != "exited" {
		t.Fatalf("payload status = ok:%v status:%s checks=%+v components=%+v images=%+v", payload.OK, payload.Status, payload.Checks, payload.Components, payload.Images)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.neon.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("neon status schema diagnostics = %+v", diagnostics)
	}
	var dockerAvailable, computeExited, minioDegraded, minioPortClosed, minioImageMissing bool
	for _, check := range payload.Checks {
		if check.Name == "docker" && check.Status == "available" && check.Message == "29.0.0" {
			dockerAvailable = true
		}
		if check.Name == "port.minio" && check.Status == "closed" {
			minioPortClosed = true
		}
	}
	for _, component := range payload.Components {
		if component.Name == "compute" && component.Status == "exited" {
			computeExited = true
		}
		if component.Name == "minio" && component.Status == "degraded" && component.Health == "healthy" {
			minioDegraded = true
		}
	}
	for _, image := range payload.Images {
		if image.Name == "minio" && image.Status == "missing" {
			minioImageMissing = true
		}
	}
	if !dockerAvailable || !computeExited || !minioDegraded || !minioPortClosed || !minioImageMissing {
		t.Fatalf("probe result checks=%+v components=%+v images=%+v", payload.Checks, payload.Components, payload.Images)
	}
}

func TestDBNeonStatusReportsOpenRunningComponentPort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	root := filepath.Join(home, "agent", "substrates", "neon")
	state, ok, err := readNeonCellState(root)
	if err != nil || !ok {
		t.Fatalf("read state ok=%v err=%v", ok, err)
	}
	state.Ports["minio_api"] = port
	if err := writeNeonCellState(state); err != nil {
		t.Fatalf("write state: %v", err)
	}

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
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"status", "--json"}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, out.String())
	}
	var minioRunning, minioPortOpen bool
	for _, component := range payload.Components {
		if component.Name == "minio" && component.Status == "running" {
			minioRunning = true
		}
	}
	for _, check := range payload.Checks {
		if check.Name == "port.minio" && check.Status == "open" && strings.Contains(check.Message, ":"+portText) {
			minioPortOpen = true
		}
	}
	if !minioRunning || !minioPortOpen {
		t.Fatalf("checks=%+v components=%+v", payload.Checks, payload.Components)
	}
}

func TestDBNeonStartUsesGeneratedComposeProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(bin, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "`+callLog+`"
if [ "$1" = "compose" ]; then
  test "$2" = "-f" || exit 2
  test "$4" = "-p" || exit 2
  test "$5" = "onlava-neon" || exit 2
  test "$6" = "up" || exit 2
  test "$7" = "-d" || exit 2
  exit 0
fi
if [ "$1" = "version" ]; then
  echo "29.0.0"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "[]"
  exit 0
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-compute\tUp 2 minutes\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"start", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand start returned error: %v\n%s", err, out.String())
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode start JSON: %v\n%s", err, out.String())
	}
	if payload.OK || payload.Status != "degraded" || !strings.Contains(payload.Message, "Started generated Neon dev-cell project") {
		t.Fatalf("payload = %+v", payload)
	}
	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if !strings.Contains(string(data), "compose -f ") || !strings.Contains(string(data), " -p onlava-neon up -d") {
		t.Fatalf("docker calls = %s", data)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.neon.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("neon status schema diagnostics = %+v", diagnostics)
	}
}

func TestDBNeonStopUsesGeneratedComposeProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(bin, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "`+callLog+`"
if [ "$1" = "compose" ]; then
  test "$2" = "-f" || exit 2
  test "$4" = "-p" || exit 2
  test "$5" = "onlava-neon" || exit 2
  test "$6" = "stop" || exit 2
  exit 0
fi
if [ "$1" = "version" ]; then
  echo "29.0.0"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "[]"
  exit 0
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-compute\tExited (0) 1 second ago\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"stop", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand stop returned error: %v\n%s", err, out.String())
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode stop JSON: %v\n%s", err, out.String())
	}
	if payload.OK || payload.Status != "exited" || !strings.Contains(payload.Message, "Stopped generated Neon dev-cell project") {
		t.Fatalf("payload = %+v", payload)
	}
	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if !strings.Contains(string(data), "compose -f ") || !strings.Contains(string(data), " -p onlava-neon stop") {
		t.Fatalf("docker calls = %s", data)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.neon.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("neon status schema diagnostics = %+v", diagnostics)
	}
}

func TestDBNeonRestartRestartsExistingOnlavaContainers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	restartLog := filepath.Join(t.TempDir(), "restart.log")
	fakeDocker := filepath.Join(bin, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
if [ "$1" = "version" ]; then
  echo "29.0.0"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "missing image" >&2
  exit 1
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-minio\tUp 2 minutes (health: healthy)\n'
  printf 'onlava-neon-compute\tUp 1 minute\n'
  exit 0
fi
if [ "$1" = "restart" ]; then
  printf '%s\n' "$2" >> "`+restartLog+`"
  echo "$2"
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"restart", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand restart returned error: %v\n%s", err, out.String())
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode restart JSON: %v\n%s", err, out.String())
	}
	if payload.OK || payload.Status != "degraded" || !strings.Contains(payload.Message, "Restarted 2") {
		t.Fatalf("payload = %+v", payload)
	}
	data, err := os.ReadFile(restartLog)
	if err != nil {
		t.Fatalf("read restart log: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "onlava-neon-minio") || !strings.Contains(got, "onlava-neon-compute") {
		t.Fatalf("restart log = %q", got)
	}
}

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
		"provider": "neon-self-hosted",
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
		Provider:      neonSelfHostedProvider,
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
		Provider:      neonSelfHostedProvider,
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
	if payload.BackendStatus != "pending" || !strings.Contains(payload.BackendMessage, "Neon dev-cell is") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBBranchCheckoutAndDeleteUseConfiguredBranchDriver(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	binDir := t.TempDir()
	driverLog := filepath.Join(binDir, "driver.log")
	driver := filepath.Join(binDir, "neon-branch-driver")
	if err := os.WriteFile(driver, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DRIVER_LOG"
case "$1" in
  ensure|reset|restore)
    printf '{"status":"ready","message":"driver marked branch ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"branchapp","role":"cloud_admin","sslmode":"disable","source":"test-driver"}}\n'
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
	t.Setenv(neonBranchDriverEnv, driver)

	var out bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &out, []string{"checkout", "feature/driver", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	var payload dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode checkout JSON: %v\n%s", err, out.String())
	}
	if payload.BackendStatus != "ready" || payload.Connection == nil || payload.Connection.Source != "test-driver" {
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

func TestDBBranchExpireAndPruneLocalRegistry(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/old", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	var listOut bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &listOut, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	var listed dbBranchListResult
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, listOut.String())
	}
	if len(listed.Branches) != 2 || len(listed.Leases) != 2 || listed.RegistryPath == "" {
		t.Fatalf("listed = %+v", listed)
	}
	for _, lease := range listed.Leases {
		if lease.Status != "missing" || lease.Pin.Branch == "" {
			t.Fatalf("lease = %+v", lease)
		}
	}

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"expire", "feature/old", "--after", "-1h", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("expire returned error: %v", err)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var oldExpired bool
	for _, lease := range registry.Leases {
		if lease.Pin.Branch == "feature/old" && lease.ExpiresAt != "" {
			oldExpired = true
		}
	}
	if !oldExpired {
		t.Fatalf("registry after expire = %+v", registry.Leases)
	}
	foreignPin := worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfHostedProvider,
		Project:       "branchapp",
		ParentBranch:  "main",
		Branch:        "feature/foreign",
		BranchID:      "br-foreign",
		Database:      "branchapp",
		Role:          "cloud_admin",
		CreatedBy:     "external",
	}
	registry.Leases = append(registry.Leases, neonBranchLease{
		Pin:       foreignPin,
		Status:    "expired",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	})
	rootDir, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	if err := writeNeonBranchRegistry(rootDir, registry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	var pruneOut bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &pruneOut, []string{"prune", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("prune returned error: %v", err)
	}
	var pruned dbBranchListResult
	if err := json.Unmarshal(pruneOut.Bytes(), &pruned); err != nil {
		t.Fatalf("decode prune JSON: %v\n%s", err, pruneOut.String())
	}
	if len(pruned.Branches) != 1 || pruned.Branches[0].Branch != "feature/current" || len(pruned.Leases) != 1 || pruned.Leases[0].Status != "missing" {
		t.Fatalf("pruned = %+v", pruned)
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry after prune: %v", err)
	}
	var foreignKept bool
	for _, lease := range registry.Leases {
		if lease.Pin.Branch == "feature/foreign" && lease.Pin.CreatedBy == "external" {
			foreignKept = true
		}
	}
	if !foreignKept {
		t.Fatalf("foreign lease was pruned: %+v", registry.Leases)
	}
}

func TestDBBranchDeleteRemovesPendingLocalLease(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/kept", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/kept", "--app-root", root}); err != nil {
		t.Fatalf("delete pending non-current lease returned error: %v", err)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var kept, current bool
	for _, lease := range registry.Leases {
		kept = kept || lease.Pin.Branch == "feature/kept"
		current = current || lease.Pin.Branch == "feature/current"
	}
	if kept || !current {
		t.Fatalf("registry after non-current delete = %+v", registry.Leases)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/current", "--app-root", root}); err == nil || !strings.Contains(err.Error(), "without --force") {
		t.Fatalf("delete current without force error = %v", err)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/current", "--app-root", root, "--force"}); err != nil {
		t.Fatalf("delete current pending lease returned error: %v", err)
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry after current delete: %v", err)
	}
	for _, lease := range registry.Leases {
		if lease.Pin.Branch == "feature/current" {
			t.Fatalf("current lease still present: %+v", registry.Leases)
		}
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); !os.IsNotExist(err) {
		t.Fatalf("current delete should remove worktree pin, stat err=%v", err)
	}
}

func TestDBBranchDeleteReadyLeaseRequiresBackend(t *testing.T) {
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
		Source:   "test",
	})

	err = runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/ready", "--app-root", root, "--force"})
	if err == nil || !strings.Contains(err.Error(), "Neon backend delete is not implemented yet") {
		t.Fatalf("ready delete error = %v", err)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if len(registry.Leases) != 1 || registry.Leases[0].Status != "ready" {
		t.Fatalf("ready lease should remain present: %+v", registry.Leases)
	}
}

func TestNeonDownCleanupRemovesCurrentLeaseAndPin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/kept", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	message, err := dropSessionManagedDatabase(t.Context(), root, localagent.Session{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("dropSessionManagedDatabase returned error: %v", err)
	}
	if !strings.Contains(message, "removed local Neon branch lease feature/current") {
		t.Fatalf("message = %q", message)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var kept, current bool
	for _, lease := range registry.Leases {
		kept = kept || lease.Pin.Branch == "feature/kept"
		current = current || lease.Pin.Branch == "feature/current"
	}
	if !kept || current {
		t.Fatalf("registry after down db cleanup = %+v", registry.Leases)
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); err != nil {
		t.Fatalf("db cleanup removed worktree pin: %v", err)
	}

	removed, err := removeNeonWorktreeDBPinIfConfigured(root)
	if err != nil {
		t.Fatalf("removeNeonWorktreeDBPinIfConfigured returned error: %v", err)
	}
	if !removed {
		t.Fatal("expected state cleanup to remove worktree pin")
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); !os.IsNotExist(err) {
		t.Fatalf("worktree pin still exists or stat failed: %v", err)
	}
}

func TestDBBranchResetAndDeleteGuards(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-self-hosted",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "main",
		"branch_id": "br-local-parent",
		"database": "branchapp",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)

	err := runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `refusing to reset protected parent branch "main"`) {
		t.Fatalf("reset parent error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "main", "--app-root", root, "--force"})
	if err == nil || !strings.Contains(err.Error(), `refusing to delete protected parent branch "main"`) {
		t.Fatalf("delete parent error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `refusing to restore protected parent branch "main"`) {
		t.Fatalf("restore parent error = %v", err)
	}

	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-self-hosted",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "branchapp/feature",
		"branch_id": "br-local-feature",
		"database": "branchapp",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "branchapp/feature", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `without --force`) {
		t.Fatalf("delete current error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `requires --yes`) {
		t.Fatalf("reset yes error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `requires --at`) {
		t.Fatalf("restore at error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `requires --yes`) {
		t.Fatalf("restore yes error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `requires generated Neon dev-cell readiness`) {
		t.Fatalf("reset preflight error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `requires generated Neon dev-cell readiness`) {
		t.Fatalf("restore preflight error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"diff", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `usage: onlava db branch diff <branch>`) {
		t.Fatalf("diff usage error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"diff", "main", "--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), `requires generated Neon dev-cell readiness`) {
		t.Fatalf("diff preflight error = %v", err)
	}

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install dev-cell returned error: %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `Neon backend reset is not implemented yet`) {
		t.Fatalf("reset backend error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `Neon backend restore is not implemented yet`) {
		t.Fatalf("restore backend error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"diff", "main", "--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), `Neon backend diff is not implemented yet`) {
		t.Fatalf("diff backend error = %v", err)
	}
}

func TestEnsureNeonBranchPinForSessionDerivesWorktreeBranch(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := appcfg.Config{
		Name: "Branch App",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {
				Kind:               "neon",
				Mode:               "self-hosted",
				Isolation:          "branch",
				Project:            "Branch App",
				BranchNameTemplate: "{app}/{git_branch}",
			},
		}},
	}
	resolution, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{
		SessionID: "session-a",
		BaseAppID: "branch-app",
		Branch:    "Feature/API",
	})
	if err != nil {
		t.Fatalf("ensureNeonBranchPinForSession returned error: %v", err)
	}
	if !resolution.Created || resolution.Source != "worktree" {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.Pin.Branch != "branch-app/feature/api" || resolution.Pin.SessionID != "session-a" {
		t.Fatalf("pin = %+v", resolution.Pin)
	}
	if resolution.BackendStatus.Status != "missing" || !strings.Contains(resolution.BackendStatus.Message, "dev-cell is not installed") {
		t.Fatalf("backend status = %+v, want missing dev-cell", resolution.BackendStatus)
	}
	if _, err := os.Stat(filepath.Join(root, ".onlava", "worktree-db.json")); err != nil {
		t.Fatalf("pin not written: %v", err)
	}
}

func TestEnsureNeonBranchPinForSessionReusesExistingPin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-self-hosted",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "branchapp/manual",
		"branch_id": "br-local-manual",
		"database": "branchapp",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)
	cfg := appcfg.Config{
		Name: "branchapp",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", BranchPolicy: "manual"},
		}},
	}
	resolution, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("ensureNeonBranchPinForSession returned error: %v", err)
	}
	if resolution.Created || resolution.Source != "pin" || resolution.Pin.Branch != "branchapp/manual" {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.BackendStatus.Status != "missing" || !strings.Contains(resolution.BackendStatus.Message, "dev-cell is not installed") {
		t.Fatalf("backend status = %+v, want missing dev-cell", resolution.BackendStatus)
	}
}

func TestEnsureNeonBranchPinForSessionManualRequiresPin(t *testing.T) {
	root := t.TempDir()
	cfg := appcfg.Config{
		Name: "branchapp",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", BranchPolicy: "manual"},
		}},
	}
	_, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{SessionID: "session-a"})
	if err == nil || !strings.Contains(err.Error(), "requires `onlava db branch checkout <name>`") {
		t.Fatalf("manual policy error = %v", err)
	}
}

func TestParseDBBranchArgsRequiresKnownCommand(t *testing.T) {
	t.Parallel()

	if _, err := parseDBBranchArgs([]string{"status", "--json"}); err != nil {
		t.Fatalf("parseDBBranchArgs status returned error: %v", err)
	}
	if _, err := parseDBBranchArgs([]string{"unknown"}); err == nil || err.Error() != `unknown db branch command "unknown"` {
		t.Fatalf("unknown command error = %v", err)
	}
}

func markNeonLeaseReadyForTest(t *testing.T, pin worktreeDBPin, endpoint neonEndpoint) {
	t.Helper()
	root, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	for i := range registry.Leases {
		if sameNeonLease(registry.Leases[i].Pin, pin) {
			registry.Leases[i].Status = "ready"
			registry.Leases[i].Endpoint = &endpoint
			registry.Leases[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := writeNeonBranchRegistry(root, registry); err != nil {
				t.Fatalf("write registry: %v", err)
			}
			return
		}
	}
	t.Fatalf("lease not found for pin %+v in %+v", pin, registry.Leases)
}
