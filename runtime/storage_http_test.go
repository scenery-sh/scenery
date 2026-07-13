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
		Service: "auth",
		Name:    "Token",
		Authenticate: func(_ context.Context, token string) (AuthInfo, error) {
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
	putReq.Header.Set("X-Scenery-Storage-Meta-Author", "runtime")
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
	if putObj.Store != "app" || putObj.Key != "reports/report.txt" || putObj.SizeBytes != int64(len("storage report")) || putObj.Metadata["Author"] != "runtime" {
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
	if len(page.Objects) != 1 || page.Objects[0].Key != "reports/report.txt" || page.Objects[0].Metadata["Author"] != "runtime" {
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
	if headResp.StatusCode != http.StatusOK || headResp.Header.Get("Content-Length") != "14" || headResp.Header.Get("X-Scenery-Storage-Meta-Author") != "runtime" {
		t.Fatalf("head status = %d content-length = %q metadata = %q", headResp.StatusCode, headResp.Header.Get("Content-Length"), headResp.Header.Get("X-Scenery-Storage-Meta-Author"))
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

func TestStorageHTTPRoutesScopeObjectsByTenant(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	RegisterAuthHandler(&AuthHandler{
		Service: "auth",
		Name:    "Token",
		Authenticate: func(_ context.Context, token string) (AuthInfo, error) {
			switch token {
			case "tenant-a":
				return AuthInfo{UID: "user-a", Data: storageHTTPAuthData{tenant: "tenant-a"}}, nil
			case "tenant-b":
				return AuthInfo{UID: "user-b", Data: storageHTTPAuthData{tenant: "tenant-b"}}, nil
			case "no-tenant":
				return AuthInfo{UID: "user-c"}, nil
			default:
				return AuthInfo{}, nil
			}
		},
	})

	root := t.TempDir()
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfigWithTenantScoped(root, "auth", true))
	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	server := httptest.NewServer(httpServer.Handler)
	defer server.Close()
	client := server.Client()

	putStorageHTTP(t, client, server.URL, "tenant-a", "reports/report.txt", "alpha")
	putStorageHTTP(t, client, server.URL, "tenant-b", "reports/report.txt", "bravo")
	if got := getStorageHTTP(t, client, server.URL, "tenant-a", "reports/report.txt"); got != "alpha" {
		t.Fatalf("tenant A body = %q", got)
	}
	if got := getStorageHTTP(t, client, server.URL, "tenant-b", "reports/report.txt"); got != "bravo" {
		t.Fatalf("tenant B body = %q", got)
	}
	listReq, err := http.NewRequest(http.MethodGet, server.URL+"/__scenery/storage/app?prefix=reports/", nil)
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("Authorization", "Bearer tenant-a")
	listResp, err := client.Do(listReq)
	if err != nil {
		t.Fatalf("list tenant A: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Fatalf("list status = %d body = %s", listResp.StatusCode, body)
	}
	var page storage.ListPage
	if err := json.NewDecoder(listResp.Body).Decode(&page); err != nil {
		t.Fatalf("decode list page: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "reports/report.txt" {
		t.Fatalf("tenant A page = %+v", page)
	}

	noTenantReq, err := http.NewRequest(http.MethodPut, server.URL+"/__scenery/storage/app/reports/missing.txt", strings.NewReader("nope"))
	if err != nil {
		t.Fatal(err)
	}
	noTenantReq.Header.Set("Authorization", "Bearer no-tenant")
	noTenantResp, err := client.Do(noTenantReq)
	if err != nil {
		t.Fatalf("put without tenant: %v", err)
	}
	_ = noTenantResp.Body.Close()
	if noTenantResp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing tenant status = %d, want %d", noTenantResp.StatusCode, http.StatusForbidden)
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

type storageHTTPAuthData struct {
	tenant string
}

func (d storageHTTPAuthData) AuditTenantID() string {
	return d.tenant
}

func putStorageHTTP(t *testing.T, client *http.Client, baseURL, token, key, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/__scenery/storage/app/"+key, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("put %s status = %d body = %s", key, resp.StatusCode, data)
	}
}

func getStorageHTTP(t *testing.T, client *http.Client, baseURL, token, key string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/__scenery/storage/app/"+key, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get %s status = %d body = %s", key, resp.StatusCode, data)
	}
	return string(data)
}

func storageHTTPTestConfig(root, access string) string {
	return storageHTTPTestConfigWithTenantScoped(root, access, false)
}

func storageHTTPTestConfigWithTenantScoped(root, access string, tenantScoped bool) string {
	raw, err := json.Marshal(storageconfig.RuntimeConfig{
		ArtifactIdentity: storageconfig.NewRuntimeIdentity(),
		CellID:           "test-cell",
		Default:          "app",
		Stores: map[string]storageconfig.RuntimeStoreConfig{
			"app": {Kind: "local", Root: filepath.Join(root, "app"), Access: access, TenantScoped: tenantScoped},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(bytes.TrimSpace(raw))
}
