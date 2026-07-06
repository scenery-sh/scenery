package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	durablestore "scenery.sh/internal/durable/store"
)

func TestDurableWorkerHTTPLeaseHeartbeatAndComplete(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	defer setActiveDurableStores(nil)

	dsn := liveRuntimeDatabaseURL(t)
	db, err := durablestore.Open(context.Background(), "maps", dsn, durablestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ReconcileTasks(context.Background(), []durablestore.TaskDeclaration{{Name: "maps.remote.v1", HandlerRef: "maps.remote.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateWorkerToken(context.Background(), durablestore.WorkerTokenRequest{ID: "tok-1", Name: "remote", Secret: "secret-token"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Start(context.Background(), durablestore.StartRequest{ID: "job-http", TaskName: "maps.remote.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	setActiveDurableStores([]*durablestore.Store{db})

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}

	noAuth := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/__scenery/durable/v1/maps/lease", bytes.NewReader([]byte(`{"worker_id":"w1"}`)))
	server.Handler.ServeHTTP(noAuth, req)
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want %d", noAuth.Code, http.StatusUnauthorized)
	}

	leaseResp := durableLeaseResponse{}
	doDurableRequest(t, server, http.MethodPost, "/__scenery/durable/v1/maps/lease", "secret-token", `{"worker_id":"w1","lease_id":"lease-http"}`, http.StatusOK, &leaseResp)
	if !leaseResp.Leased || leaseResp.LeaseID != "lease-http" || leaseResp.Job == nil || leaseResp.Job.ID != "job-http" {
		t.Fatalf("lease response = %+v", leaseResp)
	}
	if string(leaseResp.Job.Input) != `{"id":"1"}` {
		t.Fatalf("lease input = %s", leaseResp.Job.Input)
	}

	doDurableRequest(t, server, http.MethodPost, "/__scenery/durable/v1/maps/jobs/job-http/heartbeat", "secret-token", `{"worker_id":"w1","lease_id":"bad"}`, http.StatusConflict, nil)
	doDurableRequest(t, server, http.MethodPost, "/__scenery/durable/v1/maps/jobs/job-http/heartbeat", "secret-token", `{"worker_id":"w1","lease_id":"lease-http"}`, http.StatusOK, nil)
	doDurableRequest(t, server, http.MethodPost, "/__scenery/durable/v1/maps/jobs/job-http/complete", "secret-token", `{"worker_id":"w1","lease_id":"lease-http","result":{"ok":true}}`, http.StatusOK, nil)

	var state string
	if err := db.DB().QueryRow(`SELECT state FROM scenery.durable_jobs WHERE service = 'maps' AND id = 'job-http'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("job state = %q, want succeeded", state)
	}
}

func doDurableRequest(t *testing.T, server *http.Server, method, path, token, body string, wantStatus int, dst any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	if dst != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
			t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
		}
	}
}
