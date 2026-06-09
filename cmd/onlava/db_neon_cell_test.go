package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
	useMissingNeonDocker(t)
	root := filepath.Join(home, "agent", "substrates", "neon")

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
	if payload.Ports["storage_broker"] != 55432 || payload.Cell == nil || payload.Cell.Ports["pageserver_http"] != 55434 {
		t.Fatalf("ports = result:%+v cell:%+v", payload.Ports, payload.Cell)
	}
	if payload.Storage == nil || payload.Cell.Storage == nil || payload.Storage.Mode != "bind" || payload.Storage.Root != filepath.Join(root, "data") {
		t.Fatalf("storage status missing or wrong: result=%+v cell=%+v", payload.Storage, payload.Cell.Storage)
	}
	if payload.Driver == nil || payload.Cell.Driver == nil || payload.Driver.Tool != neonSelfhostDriverToolchainArtifact {
		t.Fatalf("driver status missing from payload: result=%+v cell=%+v", payload.Driver, payload.Cell.Driver)
	}
	if payload.Backend == nil || !payload.Backend.Present || payload.Backend.BranchCount != 0 {
		t.Fatalf("backend status missing from payload: %+v", payload.Backend)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "onlava.db.neon.status.v1.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("neon status schema diagnostics = %+v", diagnostics)
	}
	for _, rel := range []string{"cell.json", "compose.generated.yml", "pageserver_config/pageserver.toml", "pageserver_config/identity.toml", "compute_templates/config.json", "compute_templates/compute.sh", "backend.json"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("generated %s missing: %v", rel, err)
		}
	}
	for _, rel := range []string{"data/minio", "data/pageserver", "data/safekeeper-1", "data/safekeeper-2", "data/safekeeper-3", "data/storage-broker"} {
		if info, err := os.Stat(filepath.Join(root, rel)); err != nil || !info.IsDir() {
			t.Fatalf("storage dir %s missing or not dir: info=%v err=%v", rel, info, err)
		}
	}
	if !strings.Contains(payload.Message, "onlava db neon start") {
		t.Fatalf("message = %q", payload.Message)
	}
	if strings.Contains(payload.Message, "pending implementation") {
		t.Fatalf("stale implementation message = %q", payload.Message)
	}
	compose, err := os.ReadFile(filepath.Join(root, "compose.generated.yml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	if !bytes.Contains(compose, []byte(`container_name: onlava-neon-bucket-init`)) {
		t.Fatalf("compose missing bucket init: %s", compose)
	}
	for _, mount := range []string{
		"./data/minio:/data",
		"./data/pageserver:/data",
		"./data/safekeeper-1:/data",
		"./data/safekeeper-2:/data",
		"./data/safekeeper-3:/data",
		"./data/storage-broker:/data",
	} {
		if !bytes.Contains(compose, []byte(mount)) {
			t.Fatalf("compose missing bind mount %s:\n%s", mount, compose)
		}
	}
	if bytes.Contains(compose, []byte(`container_name: onlava-neon-compute`)) {
		t.Fatalf("compose should not include static compute: %s", compose)
	}
	pageserver, err := os.ReadFile(filepath.Join(root, "pageserver_config", "pageserver.toml"))
	if err != nil {
		t.Fatalf("read pageserver config: %v", err)
	}
	if !bytes.Contains(pageserver, []byte(`control_plane_emergency_mode = true`)) {
		t.Fatalf("pageserver config missing emergency mode: %s", pageserver)
	}
}

func TestDBNeonUninstallRemovesContainersWhenStateCorrupt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
	root := filepath.Join(home, "agent", "substrates", "neon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, "cell.json", `{`)

	logPath := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-compute\nonlava-neon-pageserver\n'
  exit 0
fi
if [ "$1" = "rm" ]; then
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_LOG", logPath)
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"uninstall", "--destroy-data", "--json"}); err != nil {
		t.Fatalf("uninstall returned error: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root should be removed after container cleanup, stat err=%v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	if !strings.Contains(string(logBytes), "rm -f -v onlava-neon-compute onlava-neon-pageserver") {
		t.Fatalf("docker log = %q", string(logBytes))
	}
}

func TestDBNeonUninstallFallsBackWhenComposeMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	logPath := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-minio\n'
  exit 0
fi
if [ "$1" = "rm" ]; then
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_LOG", logPath)
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install returned error: %v", err)
	}
	root := filepath.Join(home, "agent", "substrates", "neon")
	if err := os.Remove(filepath.Join(root, "compose.generated.yml")); err != nil {
		t.Fatalf("remove compose: %v", err)
	}

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"uninstall", "--destroy-data", "--json"}); err != nil {
		t.Fatalf("uninstall returned error: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	if !strings.Contains(string(logBytes), "rm -f -v onlava-neon-minio") {
		t.Fatalf("docker log = %q", string(logBytes))
	}
}

func TestDBNeonUninstallRemovesDriverComputeAfterComposeDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	logPath := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "compose" ] && [ "$6" = "down" ]; then
  exit 0
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-compute-feature-a\n'
  exit 0
fi
if [ "$1" = "rm" ]; then
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_LOG", logPath)
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install returned error: %v", err)
	}

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"uninstall", "--destroy-data", "--json"}); err != nil {
		t.Fatalf("uninstall returned error: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "compose -f ") || !strings.Contains(log, " down -v --remove-orphans") || !strings.Contains(log, "rm -f -v onlava-neon-compute-feature-a") {
		t.Fatalf("docker log = %q", log)
	}
}

func TestDBNeonUninstallPreservesBindMountedDataByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	logPath := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "compose" ] && [ "$6" = "down" ]; then
  exit 0
fi
if [ "$1" = "ps" ]; then
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_LOG", logPath)
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install returned error: %v", err)
	}
	root := filepath.Join(home, "agent", "substrates", "neon")
	sentinel := filepath.Join(root, "data", "minio", "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	var out bytes.Buffer
	if err := runDBNeonCommand(t.Context(), &out, []string{"uninstall", "--json"}); err != nil {
		t.Fatalf("uninstall returned error: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("bind-mounted data should be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cell.json")); !os.IsNotExist(err) {
		t.Fatalf("generated state should be removed while data stays, stat err=%v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "compose -f ") || !strings.Contains(log, " down --remove-orphans") || strings.Contains(log, " down -v ") || strings.Contains(log, "rm -f -v") {
		t.Fatalf("docker log = %q", log)
	}
	var payload dbNeonStatusResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode uninstall JSON: %v\n%s", err, out.String())
	}
	if !strings.Contains(payload.Message, "preserved") || !strings.Contains(payload.RequiredAction, "--destroy-data") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBNeonUninstallKeepsStateWhenFallbackCleanupFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
	root := filepath.Join(home, "agent", "substrates", "neon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, "cell.json", `{`)

	logPath := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_LOG", logPath)
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"uninstall", "--destroy-data", "--json"}); err == nil {
		t.Fatal("uninstall should fail when fallback cleanup fails")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("state should remain when teardown fails, stat err=%v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	if !strings.Contains(string(logBytes), "ps -a --filter label=onlava.substrate=neon") {
		t.Fatalf("docker log = %q", string(logBytes))
	}
}

func TestDBNeonStatusProbesDockerHealth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

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
    ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f|ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249)
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
  printf 'onlava-neon-bucket-init\tExited (0) 1 minute ago\n'
  printf 'onlava-neon-pageserver\tExited (1) 1 minute ago\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}
	root := filepath.Join(home, "agent", "substrates", "neon")
	state, ok, err := readNeonCellState(root)
	if err != nil || !ok {
		t.Fatalf("read state ok=%v err=%v", ok, err)
	}
	state.Ports["minio_api"] = closedTCPPortForTest(t)
	if err := writeNeonCellState(state); err != nil {
		t.Fatalf("write state: %v", err)
	}

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
	var dockerAvailable, bucketInitCompleted, pageserverExited, minioDegraded, minioPortClosed, minioImageMissing bool
	for _, check := range payload.Checks {
		if check.Name == "docker" && check.Status == "available" && check.Message == "29.0.0" {
			dockerAvailable = true
		}
		if check.Name == "port.minio" && check.Status == "closed" {
			minioPortClosed = true
		}
	}
	for _, component := range payload.Components {
		if component.Name == "bucket-init" && component.Status == "completed" {
			bucketInitCompleted = true
		}
		if component.Name == "pageserver" && component.Status == "exited" {
			pageserverExited = true
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
	if !dockerAvailable || !bucketInitCompleted || !pageserverExited || !minioDegraded || !minioPortClosed || !minioImageMissing {
		t.Fatalf("probe result checks=%+v components=%+v images=%+v", payload.Checks, payload.Components, payload.Images)
	}
}

func TestDBNeonStatusReportsOpenRunningComponentPort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)
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

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
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
  printf 'onlava-neon-storage-broker\tUp 2 minutes\n'
  exit 0
fi
if [ "$1" = "inspect" ]; then
  printf '/onlava-neon-storage-broker\t/data=bind=%s;\n' "$PWD/data/storage-broker"
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

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

func TestDBNeonStartBlocksLegacyAnonymousDataVolumes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(bin, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "`+callLog+`"
if [ "$1" = "version" ]; then
  echo "29.0.0"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "[]"
  exit 0
fi
if [ "$1" = "ps" ]; then
  printf 'onlava-neon-minio\tUp 2 minutes\n'
  exit 0
fi
if [ "$1" = "inspect" ]; then
  printf '/onlava-neon-minio\t/data=volume=/var/lib/docker/volumes/legacy/_data;\n'
  exit 0
fi
if [ "$1" = "compose" ]; then
  echo "compose should not run when legacy volumes are present" >&2
  exit 3
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

	var out bytes.Buffer
	err := runDBNeonCommand(t.Context(), &out, []string{"start", "--json"})
	if err == nil {
		t.Fatalf("start should fail with legacy anonymous volumes:\n%s", out.String())
	}
	var payload dbNeonStatusResult
	if decodeErr := json.Unmarshal(out.Bytes(), &payload); decodeErr != nil {
		t.Fatalf("decode start JSON: %v\n%s", decodeErr, out.String())
	}
	if payload.OK || !strings.Contains(payload.Message, "Docker-managed /data volumes") || !strings.Contains(payload.RequiredAction, "--destroy-data") {
		t.Fatalf("payload = %+v", payload)
	}
	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if strings.Contains(string(data), "compose -f") {
		t.Fatalf("compose should not run when legacy volumes are present: %s", data)
	}
}

func TestDBNeonStopUsesGeneratedComposeProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ONLAVA_AGENT_HOME", home)

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
  printf 'onlava-neon-storage-broker\tExited (0) 1 second ago\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	useFakeNeonDocker(t, fakeDocker)

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

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
  printf 'onlava-neon-storage-broker\tUp 1 minute\n'
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

	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("runDBNeonCommand install returned error: %v", err)
	}

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
	if !strings.Contains(got, "onlava-neon-minio") || !strings.Contains(got, "onlava-neon-storage-broker") {
		t.Fatalf("restart log = %q", got)
	}
}

func TestDockerHealthFromStatusParsesSteadyStateTokens(t *testing.T) {
	t.Parallel()

	if got := dockerHealthFromStatus("Up 2 minutes (healthy)"); got != "healthy" {
		t.Fatalf("healthy status = %q", got)
	}
	if got := dockerHealthFromStatus("Up 2 minutes (unhealthy)"); got != "unhealthy" {
		t.Fatalf("unhealthy status = %q", got)
	}
}

func closedTCPPortForTest(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return port
}
