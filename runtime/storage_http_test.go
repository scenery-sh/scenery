package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/storageconfig"
	"scenery.sh/storage"
)

func TestStorageHTTPRoutesRequireAuthAndServeObjects(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	RegisterAuthHandler(&AuthHandler{
		Service:   "auth",
		Name:      "Token",
		ParamType: TypeOf[string](),
		Authenticate: func(_ context.Context, token any) (AuthInfo, error) {
			if token == "storage-token" {
				return AuthInfo{UID: "user-1"}, nil
			}
			return AuthInfo{}, nil
		},
	})

	root := t.TempDir()
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfig(root, "auth"))
	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	server := httptest.NewServer(httpServer.Handler)
	defer server.Close()
	client := server.Client()

	putReq, err := http.NewRequest(http.MethodPut, server.URL+"/__scenery/storage/app/reports/report.txt", strings.NewReader("storage report"))
	if err != nil {
		t.Fatal(err)
	}
	putReq.Header.Set("Content-Type", "text/plain")
	noAuthResp, err := client.Do(putReq)
	if err != nil {
		t.Fatalf("put without auth: %v", err)
	}
	_ = noAuthResp.Body.Close()
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("put without auth status = %d, want %d", noAuthResp.StatusCode, http.StatusUnauthorized)
	}

	putReq, err = http.NewRequest(http.MethodPut, server.URL+"/__scenery/storage/app/reports/report.txt", strings.NewReader("storage report"))
	if err != nil {
		t.Fatal(err)
	}
	putReq.Header.Set("Authorization", "Bearer storage-token")
	putReq.Header.Set("Content-Type", "text/plain")
	putResp, err := client.Do(putReq)
	if err != nil {
		t.Fatalf("put with auth: %v", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("put status = %d, body = %s", putResp.StatusCode, body)
	}
	var putObj storage.Object
	if err := json.NewDecoder(putResp.Body).Decode(&putObj); err != nil {
		t.Fatalf("decode put object: %v", err)
	}
	if putObj.Store != "app" || putObj.Key != "reports/report.txt" || putObj.SizeBytes != int64(len("storage report")) {
		t.Fatalf("put object = %+v", putObj)
	}

	listReq, err := http.NewRequest(http.MethodGet, server.URL+"/__scenery/storage/app?prefix=reports/&delimiter=/", nil)
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("Authorization", "Bearer storage-token")
	listResp, err := client.Do(listReq)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Fatalf("list status = %d, body = %s", listResp.StatusCode, body)
	}
	var page storage.ListPage
	if err := json.NewDecoder(listResp.Body).Decode(&page); err != nil {
		t.Fatalf("decode list page: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "reports/report.txt" {
		t.Fatalf("list page = %+v", page)
	}

	headReq, err := http.NewRequest(http.MethodHead, server.URL+"/__scenery/storage/app/reports/report.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	headReq.Header.Set("Authorization", "Bearer storage-token")
	headResp, err := client.Do(headReq)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	_ = headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK || headResp.Header.Get("Content-Length") != "14" {
		t.Fatalf("head status = %d content-length = %q", headResp.StatusCode, headResp.Header.Get("Content-Length"))
	}

	getReq, err := http.NewRequest(http.MethodGet, server.URL+"/__scenery/storage/app/reports/report.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	getReq.Header.Set("Authorization", "Bearer storage-token")
	getReq.Header.Set("Range", "bytes=0-6")
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer getResp.Body.Close()
	got, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if getResp.StatusCode != http.StatusPartialContent || string(got) != "storage" {
		t.Fatalf("get status = %d body = %q", getResp.StatusCode, got)
	}

	deleteReq, err := http.NewRequest(http.MethodDelete, server.URL+"/__scenery/storage/app/reports/report.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteReq.Header.Set("Authorization", "Bearer storage-token")
	deleteResp, err := client.Do(deleteReq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}

	missingReq, err := http.NewRequest(http.MethodGet, server.URL+"/__scenery/storage/app/reports/report.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	missingReq.Header.Set("Authorization", "Bearer storage-token")
	missingResp, err := client.Do(missingReq)
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	_ = missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", missingResp.StatusCode, http.StatusNotFound)
	}
}

func TestStorageHTTPRoutesDenyPrivateStores(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	root := t.TempDir()
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfig(root, "private"))
	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/__scenery/storage/app/reports/report.txt", nil)
	httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("private store status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStorageHTTPPrivateStoreServedOnInternalRouter(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	root := t.TempDir()
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfig(root, "private"))
	s := &server{
		public:  newRouteTable(),
		private: newRouteTable(),
	}
	s.registerStorageRoutes()

	putRec := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/__scenery/storage/app/reports/report.txt", strings.NewReader("internal report"))
	putReq.Header.Set("Content-Type", "text/plain")
	s.private.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusCreated {
		t.Fatalf("internal put status = %d, body = %s", putRec.Code, putRec.Body.String())
	}

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/__scenery/storage/app/reports/report.txt", nil)
	s.private.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("internal get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	if got := getRec.Body.String(); got != "internal report" {
		t.Fatalf("internal get body = %q, want %q", got, "internal report")
	}

	publicRec := httptest.NewRecorder()
	publicReq := httptest.NewRequest(http.MethodGet, "/__scenery/storage/app/reports/report.txt", nil)
	s.public.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusForbidden {
		t.Fatalf("public get status = %d, want %d", publicRec.Code, http.StatusForbidden)
	}
}

func storageHTTPTestConfig(root, access string) string {
	raw, err := json.Marshal(map[string]any{
		"schema_version": storageconfig.RuntimeSchemaVersion,
		"cell_id":        "test-cell",
		"default":        "app",
		"stores": map[string]any{
			"app": map[string]any{
				"kind":   "local",
				"root":   filepath.Join(root, "app"),
				"access": access,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(bytes.TrimSpace(raw))
}
