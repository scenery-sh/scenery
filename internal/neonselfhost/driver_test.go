package neonselfhost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunCapabilitiesJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run(&stdout, &stderr, []string{"capabilities", "--json"}); err != nil {
		t.Fatalf("Run capabilities returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var payload Capabilities
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode capabilities: %v\n%s", err, stdout.String())
	}
	if payload.SchemaVersion != CapabilitiesSchemaVersion || payload.Provider != "neon-selfhost" || payload.Driver != "neon-selfhost-driver" || payload.Status != "ready" {
		t.Fatalf("unexpected capabilities payload: %+v", payload)
	}
	if len(payload.Actions) != 7 || payload.Actions[0] != "capabilities" || payload.Actions[1] != "status" || payload.Actions[2] != "ensure" {
		t.Fatalf("actions = %+v", payload.Actions)
	}
}

func TestRunStatusJSONReportsBackendSummary(t *testing.T) {
	root := t.TempDir()
	if err := WriteBackendState(filepath.Join(root, "backend.json"), newTestBackendState("onlv", "tenant-test", 16)); err != nil {
		t.Fatalf("write backend: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(&stdout, &stderr, []string{"status", "--root", root, "--json"}); err != nil {
		t.Fatalf("Run status returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var payload Status
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ready" || payload.Root != root || payload.Backend == nil {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunStatusText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run(&stdout, &stderr, []string{"status"}); err != nil {
		t.Fatalf("Run status returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "neon-selfhost-driver ready") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunEnsureReturnsPendingWhenRecordedComputeIsUnreachable(t *testing.T) {
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	writeCellStateForTest(t, root, closedPort)
	var stdout, stderr bytes.Buffer
	err = Run(&stdout, &stderr, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode ensure: %v\n%s", err, stdout.String())
	}
	if payload.Status != "pending" || !strings.Contains(payload.Message, "pageserver HTTP endpoint") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunEnsureReturnsReadyForReachableRecordedCompute(t *testing.T) {
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	state := newTestBackendState("onlv", "tenant-test", 16)
	state.Projects["onlv"].Branches["br-local-test"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-feature-x",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             port,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "pending",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	psqlLog := filepath.Join(bin, "psql.log")
	writeFakePSQL(t, bin, psqlLog, "missing")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	previousTimeout := postgresReadyTimeout
	postgresReadyTimeout = 2 * time.Second
	t.Cleanup(func() { postgresReadyTimeout = previousTimeout })

	var stdout, stderr bytes.Buffer
	err = Run(&stdout, &stderr, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode ensure: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ready" || payload.Endpoint == nil {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Endpoint.Host != "127.0.0.1" || payload.Endpoint.Port != port || payload.Endpoint.Database != "onlv" || payload.Endpoint.Role != "cloud_admin" || payload.Endpoint.SSLMode != "disable" || payload.Endpoint.Source != "neon-selfhost-driver" {
		t.Fatalf("endpoint = %+v", payload.Endpoint)
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		t.Fatalf("read backend ok=%v err=%v", ok, err)
	}
	if state.Projects["onlv"].Branches["br-local-test"].Status != "ready" {
		t.Fatalf("branch = %+v", state.Projects["onlv"].Branches["br-local-test"])
	}
	logBytes, err := os.ReadFile(psqlLog)
	if err != nil {
		t.Fatalf("read psql log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "select 1") || !strings.Contains(log, "create database") {
		t.Fatalf("psql log = %q", log)
	}
}

func TestRunEnsureKeepsReachableComputePendingWithoutPSQL(t *testing.T) {
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	state := newTestBackendState("onlv", "tenant-test", 16)
	state.Projects["onlv"].Branches["br-local-test"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-feature-x",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             port,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "pending",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	err = Run(&stdout, &stderr, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode ensure: %v\n%s", err, stdout.String())
	}
	if payload.Status != "pending" || !strings.Contains(payload.Message, "psql is not available") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunEnsureBootstrapsPageserverTenantAndTimelines(t *testing.T) {
	root := t.TempDir()
	server, port, seen := startFakePageserver(t)
	defer server.Close()
	writeCellStateForTest(t, root, port)
	writeComputeTemplatesForTest(t, root)
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode ensure: %v\n%s", err, stdout.String())
	}
	if payload.Status != "pending" || !strings.Contains(payload.Message, "docker is not available") {
		t.Fatalf("payload = %+v", payload)
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		t.Fatalf("read backend ok=%v err=%v", ok, err)
	}
	branch := state.Projects["onlv"].Branches["br-local-test"]
	if !looksLikeHexID(state.Projects["onlv"].TenantID) || !looksLikeHexID(branch.ParentTimelineID) || !looksLikeHexID(branch.TimelineID) {
		t.Fatalf("ids tenant=%q parent=%q timeline=%q", state.Projects["onlv"].TenantID, branch.ParentTimelineID, branch.TimelineID)
	}
	if branch.Status != "starting" {
		t.Fatalf("branch = %+v", branch)
	}
	requests := strings.Join(seen(), "\n")
	if !strings.Contains(requests, "PUT /v1/tenant/"+state.Projects["onlv"].TenantID+"/location_config") {
		t.Fatalf("requests missing tenant create:\n%s", requests)
	}
	if count := strings.Count(requests, "POST /v1/tenant/"+state.Projects["onlv"].TenantID+"/timeline"); count != 2 {
		t.Fatalf("timeline create count = %d requests:\n%s", count, requests)
	}
}

func TestRunEnsureBranchesFromReadyRecordedParentTimeline(t *testing.T) {
	root := t.TempDir()
	server, port, seen := startFakePageserver(t)
	defer server.Close()
	writeCellStateForTest(t, root, port)
	writeComputeTemplatesForTest(t, root)
	t.Setenv("PATH", t.TempDir())
	parentTimelineID := "11111111111111111111111111111111"
	state := newTestBackendState("onlv", "tenant-test", 16)
	state.Projects["onlv"].Branches["br-local-main"] = BackendBranch{
		Project:    "onlv",
		Branch:     "onlvnext-o5o2/main",
		TimelineID: parentTimelineID,
		EndpointID: "onlvnext-o5o2-main",
		Status:     "ready",
	}
	state.Projects["onlv"].Branches["br-local-feature"] = BackendBranch{
		Project:  "onlv",
		Branch:   "feature/x",
		Host:     "127.0.0.1",
		Port:     closedLoopbackPortForDriverTest(t),
		Database: "onlv",
		Role:     "cloud_admin",
		Status:   "pending",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}

	err := Run(&bytes.Buffer{}, &bytes.Buffer{}, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-feature",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v", err)
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		t.Fatalf("read backend ok=%v err=%v", ok, err)
	}
	branch := state.Projects["onlv"].Branches["br-local-feature"]
	if branch.ParentTimelineID != parentTimelineID {
		t.Fatalf("parent timeline = %q, want %q", branch.ParentTimelineID, parentTimelineID)
	}
	requests := strings.Join(seen(), "\n")
	if !strings.Contains(requests, `"ancestor_timeline_id":"`+parentTimelineID+`"`) {
		t.Fatalf("requests missing parent ancestor timeline:\n%s", requests)
	}
}

func TestRunEnsureStartsBranchComputeContainer(t *testing.T) {
	root := t.TempDir()
	server, port, _ := startFakePageserver(t)
	defer server.Close()
	writeCellStateForTest(t, root, port)
	writeComputeTemplatesForTest(t, root)
	previousTimeout := computeReadyTimeout
	previousInterval := computeReadyInterval
	computeReadyTimeout = 10 * time.Millisecond
	computeReadyInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		computeReadyTimeout = previousTimeout
		computeReadyInterval = previousInterval
	})

	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "ps" ]; then
  exit 0
fi
if [ "$1" = "run" ]; then
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode ensure: %v\n%s", err, stdout.String())
	}
	if payload.Status != "pending" || !strings.Contains(payload.Message, "not reachable yet") {
		t.Fatalf("payload = %+v", payload)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "run -d --name onlava-neon-compute-onlv-test") {
		t.Fatalf("docker log missing run: %q", log)
	}
	if !strings.Contains(log, "--label onlava.project=onlv") ||
		!strings.Contains(log, "--label onlava.branch_id=br-local-test") ||
		!strings.Contains(log, "--label onlava.branch=feature/x") {
		t.Fatalf("docker log missing project/branch labels: %q", log)
	}
	if !strings.Contains(log, "--network onlava-neon_default") || !strings.Contains(log, "-p 127.0.0.1:") || !strings.Contains(log, ":55433") {
		t.Fatalf("docker log missing network/port: %q", log)
	}
	if !strings.Contains(log, "-e TENANT_ID=") || !strings.Contains(log, "-e TIMELINE_ID=") {
		t.Fatalf("docker log missing tenant/timeline env: %q", log)
	}
	if !strings.Contains(log, "--add-host host.docker.internal:host-gateway") ||
		!strings.Contains(log, "-e OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://host.docker.internal:10428/insert/opentelemetry/v1/traces") ||
		!strings.Contains(log, "-e OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf") {
		t.Fatalf("docker log missing Victoria OTLP env: %q", log)
	}
}

func TestRunEnsureWritesPendingBackendBranch(t *testing.T) {
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	writeCellStateForTest(t, root, closedPort)
	var stdout, stderr bytes.Buffer
	err = Run(&stdout, &stderr, []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "onlv/feature x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("Run ensure returned error: %v", err)
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		t.Fatalf("read backend ok=%v err=%v", ok, err)
	}
	branch := state.Projects["onlv"].Branches["br-local-test"]
	if branch.Status != "pending" || branch.Project != "onlv" || branch.Port == 0 || branch.ComputeContainer != "onlava-neon-compute-onlv-test" {
		t.Fatalf("branch = %+v", branch)
	}
	if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunEnsureSerializesBackendStateMutations(t *testing.T) {
	root := t.TempDir()
	const branchCount = 20
	start := make(chan struct{})
	errs := make(chan error, branchCount)
	var wg sync.WaitGroup
	for i := 0; i < branchCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			branch := fmt.Sprintf("feature/%02d", i)
			branchID := fmt.Sprintf("br-local-%02d", i)
			var stdout, stderr bytes.Buffer
			err := Run(&stdout, &stderr, []string{
				"ensure",
				"--project", "onlv",
				"--parent-branch", "main",
				"--branch", branch,
				"--branch-id", branchID,
				"--database", "onlv",
				"--role", "cloud_admin",
				"--root", root,
				"--json",
			})
			if err != nil {
				errs <- fmt.Errorf("ensure %s: %w", branchID, err)
				return
			}
			if stderr.Len() != 0 {
				errs <- fmt.Errorf("ensure %s stderr = %q", branchID, stderr.String())
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		t.Fatalf("read backend ok=%v err=%v", ok, err)
	}
	if len(state.Projects["onlv"].Branches) != branchCount {
		t.Fatalf("backend branches = %d, want %d: %+v", len(state.Projects["onlv"].Branches), branchCount, state.Projects["onlv"].Branches)
	}
	for i := 0; i < branchCount; i++ {
		branchID := fmt.Sprintf("br-local-%02d", i)
		if _, ok := state.Projects["onlv"].Branches[branchID]; !ok {
			t.Fatalf("backend state lost branch %q: %+v", branchID, state.Projects["onlv"].Branches)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "backend.lock")); err != nil {
		t.Fatalf("backend lock file missing: %v", err)
	}
}
