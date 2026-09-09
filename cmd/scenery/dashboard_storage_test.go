package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/storagefs"
	"scenery.sh/storage"
)

func TestDashboardStorageScopePins(t *testing.T) {
	plan := &storageNamespacePlan{Worktree: localagent.WorktreePaths{Key: "worktree-a"}}
	owner := storagefs.Owner{Incarnation: "incarnation-a", Generation: "generation-a"}
	valid := dashboardStorageRequest{WorktreeKey: "worktree-a", Incarnation: "incarnation-a", Generation: "generation-a"}
	if err := checkDashboardStoragePin(plan, owner, valid); err != nil {
		t.Fatal(err)
	}
	for _, req := range []dashboardStorageRequest{
		{WorktreeKey: "worktree-b", Incarnation: valid.Incarnation, Generation: valid.Generation},
		{WorktreeKey: valid.WorktreeKey, Incarnation: "retired", Generation: valid.Generation},
		{WorktreeKey: valid.WorktreeKey, Incarnation: valid.Incarnation, Generation: "restored"},
		{},
	} {
		if err := checkDashboardStoragePin(plan, owner, req); !errors.Is(err, storagefs.ErrPrecondition) {
			t.Fatalf("stale scope accepted: %v", err)
		}
	}
}

func TestDashboardStorageRejectsMalformedRequestsBeforeResolution(t *testing.T) {
	server := &dashboardServer{}
	for _, raw := range []string{`{"app_id":"a","app_id":"b"}`, `{"app_root":"/foreign"}`, `[]`, strings.Repeat(" ", 64<<10+1)} {
		_, err := server.storageRPC(context.Background(), "storage/inspect", json.RawMessage(raw))
		failure, ok := errors.AsType[*dashboardStorageFailure](err)
		if !ok || failure.Diagnostic != "SCN8001" {
			t.Fatalf("request not rejected as invalid: %v", err)
		}
	}
}

func TestDashboardStorageTransferAuthority(t *testing.T) {
	server := &dashboardServer{}
	for _, origin := range []string{"", "http://foreign.example"} {
		req := httptest.NewRequest(http.MethodPut, "http://localhost/__storage", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-Scenery-Storage-Request", "1")
		}
		response := httptest.NewRecorder()
		server.handleStorageTransfer(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("unauthorized transfer: %d", response.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPut, "http://localhost/__storage?key=x&key=y", nil)
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("X-Scenery-Storage-Request", "1")
	response := httptest.NewRecorder()
	server.handleStorageTransfer(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate selector: %d", response.Code)
	}
}

type dashboardVersionStore struct {
	storage.Store
	body *dashboardVersionBody
}
type dashboardVersionBody struct {
	io.Reader
	closed bool
}

func (b *dashboardVersionBody) Close() error { b.closed = true; return nil }
func (s dashboardVersionStore) Head(context.Context, string) (*storage.Object, error) {
	return &storage.Object{ETag: "current"}, nil
}
func (s dashboardVersionStore) Get(context.Context, string, storage.GetOptions) (io.ReadCloser, *storage.Object, error) {
	return s.body, &storage.Object{ETag: "current"}, nil
}

func TestDashboardStorageDownloadPinsResolvedVersion(t *testing.T) {
	body := &dashboardVersionBody{Reader: strings.NewReader("current bytes")}
	store := dashboardDownloadStore{Store: dashboardVersionStore{body: body}, etag: "stale"}
	if _, err := store.Head(context.Background(), "key"); !errors.Is(err, storagefs.ErrPrecondition) {
		t.Fatalf("head: %v", err)
	}
	if stream, _, err := store.Get(context.Background(), "key", storage.GetOptions{}); !errors.Is(err, storagefs.ErrPrecondition) || stream != nil || !body.closed {
		t.Fatalf("get leaked mismatched stream: %v", err)
	}
}
