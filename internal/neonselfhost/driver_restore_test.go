package neonselfhost

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

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
