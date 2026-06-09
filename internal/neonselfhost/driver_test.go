package neonselfhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	if err := WriteBackendState(filepath.Join(root, "backend.json"), NewBackendState("tenant-test", 16)); err != nil {
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
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-test"] = BackendBranch{
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
	if state.Branches["br-local-test"].Status != "ready" {
		t.Fatalf("branch = %+v", state.Branches["br-local-test"])
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
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-test"] = BackendBranch{
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
	branch := state.Branches["br-local-test"]
	if !looksLikeHexID(state.TenantID) || !looksLikeHexID(branch.ParentTimelineID) || !looksLikeHexID(branch.TimelineID) {
		t.Fatalf("ids tenant=%q parent=%q timeline=%q", state.TenantID, branch.ParentTimelineID, branch.TimelineID)
	}
	if branch.Status != "starting" {
		t.Fatalf("branch = %+v", branch)
	}
	requests := strings.Join(seen(), "\n")
	if !strings.Contains(requests, "PUT /v1/tenant/"+state.TenantID+"/location_config") {
		t.Fatalf("requests missing tenant create:\n%s", requests)
	}
	if count := strings.Count(requests, "POST /v1/tenant/"+state.TenantID+"/timeline"); count != 2 {
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
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-main"] = BackendBranch{
		Project:    "onlv",
		Branch:     "onlvnext-o5o2/main",
		TimelineID: parentTimelineID,
		EndpointID: "onlvnext-o5o2-main",
		Status:     "ready",
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
	branch := state.Branches["br-local-feature"]
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
	if !strings.Contains(log, "run -d --name onlava-neon-compute-feature-x") {
		t.Fatalf("docker log missing run: %q", log)
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
	branch := state.Branches["br-local-test"]
	if branch.Status != "pending" || branch.Project != "onlv" || branch.Port == 0 || branch.ComputeContainer != "onlava-neon-compute-onlv-feature-x" {
		t.Fatalf("branch = %+v", branch)
	}
	if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func startFakePageserver(t *testing.T) (*http.Server, int, func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var requests []string
	timelines := map[string]bool{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		requests = append(requests, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		mu.Unlock()
		if r.Method == http.MethodGet {
			if strings.HasSuffix(r.URL.Path, "/get_lsn_by_timestamp") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"lsn":"0/500"}`))
				return
			}
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) >= 5 && timelines[parts[4]] {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"timeline_id":` + strconv.Quote(parts[4]) + `}`))
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/timeline") {
			var payload struct {
				NewTimelineID string `json:"new_timeline_id"`
			}
			if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.NewTimelineID) != "" {
				mu.Lock()
				timelines[payload.NewTimelineID] = true
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
	})
	return server, listener.Addr().(*net.TCPAddr).Port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}
}

func writeCellStateForTest(t *testing.T, root string, pageserverPort int) {
	t.Helper()
	data := []byte(fmt.Sprintf(`{"root":%q,"ports":{"pageserver_http":%d}}`, root, pageserverPort))
	if err := os.WriteFile(filepath.Join(root, "cell.json"), data, 0o644); err != nil {
		t.Fatalf("write cell: %v", err)
	}
}

func writeComputeTemplatesForTest(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "compute_templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compute.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakePSQL(t *testing.T, bin string, logPath string, databaseState string) {
	t.Helper()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	psql := filepath.Join(bin, "psql")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$PSQL_LOG"
case "$*" in
  *"select 1 from pg_database"*)
    if [ "$DATABASE_STATE" = "exists" ]; then
      printf '1\n'
    fi
    exit 0
    ;;
  *"create database"*)
    exit 0
    ;;
  *"select 1"*)
    printf '1\n'
    exit 0
    ;;
esac
echo "unexpected psql $*" >&2
exit 1
`
	if err := os.WriteFile(psql, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PSQL_LOG", logPath)
	t.Setenv("DATABASE_STATE", databaseState)
}

func TestResolveRestoreLSNAcceptsRawLSNWithoutPageserverLookup(t *testing.T) {
	lsn, err := resolveRestoreLSN(context.Background(), "http://127.0.0.1:1", "tenant", "timeline", " 0/16F9A70 ")
	if err != nil {
		t.Fatalf("resolve raw LSN: %v", err)
	}
	if lsn != "0/16F9A70" {
		t.Fatalf("lsn = %q", lsn)
	}
}

func TestResolveRestoreLSNRejectsInvalidTimestampRefs(t *testing.T) {
	_, err := resolveRestoreLSN(context.Background(), "http://127.0.0.1:1", "tenant", "timeline", "2026-06-09 00:00:00")
	if err == nil || !strings.Contains(err.Error(), "must be an LSN or RFC3339 timestamp") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveRestoreLSNLooksUpRFC3339Timestamp(t *testing.T) {
	var gotPath string
	server, baseURL := startRestoreLSNServer(t, http.StatusOK, `{"lsn":"0/ABC"}`, &gotPath)
	defer server.Close()

	lsn, err := resolveRestoreLSN(context.Background(), baseURL, "tenant-test", "timeline-test", "2026-06-09T00:00:00Z")
	if err != nil {
		t.Fatalf("resolve timestamp: %v", err)
	}
	if lsn != "0/ABC" {
		t.Fatalf("lsn = %q", lsn)
	}
	if !strings.Contains(gotPath, "/v1/tenant/tenant-test/timeline/timeline-test/get_lsn_by_timestamp?") || !strings.Contains(gotPath, "with_lease=true") {
		t.Fatalf("request path = %q", gotPath)
	}
}

func TestResolveRestoreLSNSurfacesTimestampLookupFailures(t *testing.T) {
	server, baseURL := startRestoreLSNServer(t, http.StatusServiceUnavailable, `pageserver unavailable`, nil)
	defer server.Close()

	_, err := resolveRestoreLSN(context.Background(), baseURL, "tenant-test", "timeline-test", "2026-06-09T00:00:00Z")
	if err == nil || !strings.Contains(err.Error(), "timestamp LSN lookup returned HTTP 503") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveRestoreLSNRejectsTimestampLookupWithoutLSN(t *testing.T) {
	server, baseURL := startRestoreLSNServer(t, http.StatusOK, `{}`, nil)
	defer server.Close()

	_, err := resolveRestoreLSN(context.Background(), baseURL, "tenant-test", "timeline-test", "2026-06-09T00:00:00Z")
	if err == nil || !strings.Contains(err.Error(), "did not include lsn") {
		t.Fatalf("err = %v", err)
	}
}

func startRestoreLSNServer(t *testing.T, status int, body string, gotPath *string) (*http.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotPath != nil {
			*gotPath = r.URL.RequestURI()
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
	})
	return server, "http://" + listener.Addr().String()
}

func TestRunResetAndRestoreRewritePendingBackendTimeline(t *testing.T) {
	root := t.TempDir()
	server, port, seen := startFakePageserver(t)
	defer server.Close()
	writeCellStateForTest(t, root, port)
	writeComputeTemplatesForTest(t, root)
	t.Setenv("PATH", t.TempDir())
	ensureArgs := []string{
		"ensure",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	}
	if err := Run(&bytes.Buffer{}, &bytes.Buffer{}, ensureArgs); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		t.Fatalf("read backend ok=%v err=%v", ok, err)
	}
	initialPort := state.Branches["br-local-test"].Port
	initialTimeline := state.Branches["br-local-test"].TimelineID

	var resetOut bytes.Buffer
	resetArgs := append([]string{"reset"}, ensureArgs[1:]...)
	if err := Run(&resetOut, &bytes.Buffer{}, resetArgs); err != nil {
		t.Fatalf("reset: %v", err)
	}
	state, _, err = ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		t.Fatalf("read reset backend: %v", err)
	}
	resetBranch := state.Branches["br-local-test"]
	if !looksLikeHexID(resetBranch.TimelineID) || resetBranch.TimelineID == initialTimeline || resetBranch.Port != initialPort || resetBranch.Status != "starting" {
		t.Fatalf("reset branch = %+v", resetBranch)
	}

	restoreArgs := append([]string{"restore"}, ensureArgs[1:]...)
	restoreArgs = append(restoreArgs[:len(restoreArgs)-1], "--at", "2026-06-09T00:00:00Z", "--json")
	if err := Run(&bytes.Buffer{}, &bytes.Buffer{}, restoreArgs); err != nil {
		t.Fatalf("restore: %v", err)
	}
	state, _, err = ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		t.Fatalf("read restore backend: %v", err)
	}
	restoreBranch := state.Branches["br-local-test"]
	if !looksLikeHexID(restoreBranch.TimelineID) || restoreBranch.TimelineID == resetBranch.TimelineID || restoreBranch.Port != initialPort || restoreBranch.Status != "starting" {
		t.Fatalf("restore branch = %+v", restoreBranch)
	}
	requests := strings.Join(seen(), "\n")
	if !strings.Contains(requests, `"new_timeline_id":"`+resetBranch.TimelineID+`"`) {
		t.Fatalf("requests missing reset timeline create:\n%s", requests)
	}
	if !strings.Contains(requests, `"new_timeline_id":"`+restoreBranch.TimelineID+`"`) || !strings.Contains(requests, `"ancestor_start_lsn":"0/500"`) {
		t.Fatalf("requests missing restore timeline create with LSN:\n%s", requests)
	}
	if !strings.Contains(requests, "/get_lsn_by_timestamp?") {
		t.Fatalf("requests missing timestamp lookup:\n%s", requests)
	}
	if resetOut.Len() == 0 {
		t.Fatal("reset wrote no output")
	}
}

func TestRunResetBranchesFromReadyRecordedParentTimeline(t *testing.T) {
	root := t.TempDir()
	server, port, seen := startFakePageserver(t)
	defer server.Close()
	writeCellStateForTest(t, root, port)
	writeComputeTemplatesForTest(t, root)
	t.Setenv("PATH", t.TempDir())
	parentTimelineID := "22222222222222222222222222222222"
	oldParentTimelineID := "33333333333333333333333333333333"
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-main"] = BackendBranch{
		Project:    "onlv",
		Branch:     "onlvnext-o5o2/main",
		TimelineID: parentTimelineID,
		EndpointID: "onlvnext-o5o2-main",
		Status:     "ready",
	}
	state.Branches["br-local-feature"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "44444444444444444444444444444444",
		ParentTimelineID: oldParentTimelineID,
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "starting",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}

	err := Run(&bytes.Buffer{}, &bytes.Buffer{}, []string{
		"reset",
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
		t.Fatalf("reset: %v", err)
	}
	state, _, err = ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		t.Fatalf("read reset backend: %v", err)
	}
	branch := state.Branches["br-local-feature"]
	if branch.ParentTimelineID != parentTimelineID {
		t.Fatalf("parent timeline = %q, want %q", branch.ParentTimelineID, parentTimelineID)
	}
	requests := strings.Join(seen(), "\n")
	if !strings.Contains(requests, `"new_timeline_id":"`+branch.TimelineID+`"`) || !strings.Contains(requests, `"ancestor_timeline_id":"`+parentTimelineID+`"`) {
		t.Fatalf("requests missing reset branch from parent timeline:\n%s", requests)
	}
	if strings.Contains(requests, `"ancestor_timeline_id":"`+oldParentTimelineID+`"`) {
		t.Fatalf("reset branched from stale parent timeline:\n%s", requests)
	}
}

func TestRunDeleteRemovesBackendBranchAndComputeContainer(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-test"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"delete",
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
		t.Fatalf("delete: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode delete: %v\n%s", err, stdout.String())
	}
	if payload.Status != "deleted" {
		t.Fatalf("payload = %+v", payload)
	}
	state, _, err = ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		t.Fatalf("read backend: %v", err)
	}
	if _, ok := state.Branches["br-local-test"]; ok {
		t.Fatalf("branch still present: %+v", state.Branches)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	if !strings.Contains(string(logBytes), "rm -f -v onlava-neon-compute-feature-x") {
		t.Fatalf("docker log = %q", string(logBytes))
	}
}

func TestRunDiffUsesPgDumpForReadyBackendBranches(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-current"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-current",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	state.Branches["br-local-main"] = BackendBranch{
		Project:          "onlv",
		Branch:           "main",
		TimelineID:       "timeline-main",
		EndpointID:       "main",
		ComputeContainer: "onlava-neon-compute-main",
		Host:             "127.0.0.1",
		Port:             55442,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "pg_dump.log")
	pgDump := filepath.Join(bin, "pg_dump")
	if err := os.WriteFile(pgDump, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$PG_DUMP_LOG"
case "$*" in
  *" -p 55441 "*)
    printf 'CREATE TABLE current_table(id integer);\n'
    ;;
  *" -p 55442 "*)
    printf 'CREATE TABLE main_table(id integer);\n'
    ;;
  *)
    echo "unexpected pg_dump args $*" >&2
    exit 1
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PG_DUMP_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"diff",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-current",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--target", "main",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("diff: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode diff: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(payload.Diff, "--- feature/x") || !strings.Contains(payload.Diff, "+CREATE TABLE main_table") {
		t.Fatalf("diff payload = %+v", payload)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read pg_dump log: %v", err)
	}
	if !strings.Contains(string(logBytes), "--schema-only --no-owner --no-privileges") {
		t.Fatalf("pg_dump log = %q", string(logBytes))
	}
}

func TestRunDiffFallsBackToComputeContainerPgDumpOnVersionMismatch(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-current"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-current",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	state.Branches["br-local-main"] = BackendBranch{
		Project:          "onlv",
		Branch:           "main",
		TimelineID:       "timeline-main",
		EndpointID:       "main",
		ComputeContainer: "onlava-neon-compute-main",
		Host:             "127.0.0.1",
		Port:             55442,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	pgDump := filepath.Join(bin, "pg_dump")
	if err := os.WriteFile(pgDump, []byte(`#!/bin/sh
echo "pg_dump: error: server version: 16.9; pg_dump version: 14.20" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$*" in
  *"onlava-neon-compute-feature-x"*)
    printf 'CREATE TABLE current_table(id integer);\n'
    ;;
  *"onlava-neon-compute-main"*)
    printf 'CREATE TABLE main_table(id integer);\n'
    ;;
  *)
    echo "unexpected docker $*" >&2
    exit 1
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"diff",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-current",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--target", "main",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("diff: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode diff: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(payload.Diff, "+CREATE TABLE main_table") {
		t.Fatalf("diff payload = %+v", payload)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "exec -e PGPASSWORD=cloud_admin onlava-neon-compute-feature-x pg_dump") || !strings.Contains(log, "-p 55433") {
		t.Fatalf("docker log = %q", log)
	}
}

func TestRunDiffRequiresReadyBackendBranches(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-current"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-current",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "pending",
	}
	state.Branches["br-local-main"] = BackendBranch{Branch: "main", Status: "ready"}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	err := Run(&bytes.Buffer{}, &bytes.Buffer{}, []string{
		"diff",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-current",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--target", "main",
		"--root", root,
		"--json",
	})
	if err == nil || !strings.Contains(err.Error(), "requires current branch") {
		t.Fatalf("diff error = %v", err)
	}
}
