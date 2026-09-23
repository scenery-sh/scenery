package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/storage"
)

func TestStorageHTTPInternalAuthenticationFailureIsOpaque(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	logged := captureStorageInternalFailures(t)
	failures := map[string]error{
		"database":  errors.New("query sessions: dial tcp 10.0.0.5:5432: connection refused"),
		"internal":  errs.B().Code(errs.Internal).Msg("session cache shard 7 is corrupt").Err(),
		"data-loss": errs.B().Code(errs.DataLoss).Msg("session row 42 lost its tenant").Err(),
		// Storage answers a canceled context as its own failure quoting the
		// text; the auth handler's cancellation stays internal to the route.
		"canceled":    fmt.Errorf("query sessions of tenant acme: %w", context.Canceled),
		"expired":     errs.B().Code(errs.Unauthenticated).Msg("session expired").Err(),
		"unavailable": errs.B().Code(errs.Unavailable).Msg("session store is warming up").Err(),
	}
	RegisterAuthHandler(&AuthHandler{Service: "auth", Name: "Token", Authenticate: func(_ context.Context, token string) (AuthInfo, error) {
		return AuthInfo{}, failures[token]
	}})
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfig(t.TempDir(), "auth"))
	server, err := newStorageHTTPTestServer()
	if err != nil {
		t.Fatal(err)
	}

	for _, token := range []string{"database", "internal", "data-loss", "canceled"} {
		recorder := serveStorageHTTPWithToken(server.Handler, "/__scenery/storage/app/reports/report.txt", token)
		assertStorageInternalFailure(t, recorder, logged, failures[token].Error())
	}

	logged.Reset()
	for _, want := range []struct {
		path, token, body string
		status            int
	}{
		{"/__scenery/storage/app/reports/report.txt", "expired", `{"code":"unauthenticated","message":"session expired"}`, http.StatusUnauthorized},
		{"/__scenery/storage/app/reports/report.txt", "unavailable", `{"code":"unavailable","message":"session store is warming up"}`, http.StatusServiceUnavailable},
		{"/__scenery/storage/app/reports/report.txt", "", `{"code":"unauthenticated","message":"invalid auth param"}`, http.StatusUnauthorized},
		{"/__scenery/storage/missing", "expired", `{"code":"not_found","message":"storage store \"missing\" is not configured"}`, http.StatusNotFound},
	} {
		recorder := serveStorageHTTPWithToken(server.Handler, want.path, want.token)
		if recorder.Code != want.status || recorder.Header().Get("Content-Type") != "application/json" || recorder.Body.String() != want.body+"\n" {
			t.Errorf("%s %q response = %d %q %q, want %d application/json %q", want.path, want.token, recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String(), want.status, want.body)
		}
	}
	if logged.Len() != 0 {
		t.Fatalf("typed failures logged an internal failure: %q", logged.String())
	}
}

func TestStorageHTTPRuntimeMisconfigurationIsOpaque(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	logged := captureStorageInternalFailures(t)
	server, err := newStorageHTTPTestServer()
	if err != nil {
		t.Fatal(err)
	}

	// An auth store without a registered auth handler.
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfig(t.TempDir(), "auth"))
	recorder := serveStorageHTTPWithToken(server.Handler, "/__scenery/storage/app/reports/report.txt", "token")
	assertStorageInternalFailure(t, recorder, logged, "auth endpoint configured but no auth handler registered")

	// A runtime storage configuration that does not decode.
	const undecodable = `{"stores":`
	_, _, cause := storageconfig.LoadRuntimeConfigValue(undecodable)
	if cause == nil {
		t.Fatalf("configuration %q decoded", undecodable)
	}
	t.Setenv(storageconfig.RuntimeConfigEnv, undecodable)
	recorder = serveStorageHTTPWithToken(server.Handler, "/__scenery/storage/app", "token")
	assertStorageInternalFailure(t, recorder, logged, cause.Error())
}

func TestStorageHTTPUnencodableResponseIsOpaque(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	logged := captureStorageInternalFailures(t)
	t.Setenv(storageconfig.RuntimeConfigEnv, storageHTTPTestConfig(t.TempDir(), "private"))
	router := newRouteTable()
	storageHTTPRoutes{resolve: func(context.Context, string) (storage.Store, error) {
		return storageHTTPUnencodableStore{}, nil
	}}.register(router, true)
	_, cause := json.Marshal(storageHTTPUnencodableObject)
	if cause == nil {
		t.Fatalf("object %+v encoded", storageHTTPUnencodableObject)
	}

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/__scenery/storage/app", nil))
	assertStorageInternalFailure(t, list, logged, cause.Error())

	// The object is stored, but its answer is not a success with a broken body.
	put := httptest.NewRecorder()
	router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/__scenery/storage/app/reports/report.txt", strings.NewReader("report")))
	assertStorageInternalFailure(t, put, logged, cause.Error())
}

// storageHTTPUnencodableObject has a modification time JSON cannot encode.
var storageHTTPUnencodableObject = storage.Object{Store: "app", Key: "reports/report.txt", ModifiedAt: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}

type storageHTTPUnencodableStore struct{ storage.Store }

func (storageHTTPUnencodableStore) Put(_ context.Context, _ string, body io.Reader, _ storage.PutOptions) (*storage.Object, error) {
	object := storageHTTPUnencodableObject
	_, err := io.Copy(io.Discard, body)
	return &object, err
}

func (storageHTTPUnencodableStore) List(context.Context, storage.ListOptions) (*storage.ListPage, error) {
	return &storage.ListPage{Objects: []storage.Object{storageHTTPUnencodableObject}}, nil
}

func serveStorageHTTPWithToken(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// captureStorageInternalFailures installs the process's internal failure sink
// with the default logger writing JSON records into the returned buffer.
func captureStorageInternalFailures(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, nil)))
	reportInternalFailures()
	t.Cleanup(func() {
		slog.SetDefault(previous)
		machine.SetInternalFailureSink(nil)
	})
	return &logged
}

// assertStorageInternalFailure checks that a response is storage's opaque
// internal failure, in its body and its header, and that the process logged the
// cause the caller never receives beside the same report token.
func assertStorageInternalFailure(t *testing.T, recorder *httptest.ResponseRecorder, logged *bytes.Buffer, cause string) {
	t.Helper()
	body := recorder.Body.String()
	var failure struct {
		Code        string `json:"code"`
		Diagnostic  string `json:"diagnostic"`
		Message     string `json:"message"`
		ReportToken string `json:"report_token"`
	}
	definition, _ := spec.DiagnosticDefinitionFor("SCN9000")
	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("Content-Type") != "application/json" ||
		json.Unmarshal([]byte(body), &failure) != nil || failure.Code != "internal" || failure.Diagnostic != "SCN9000" ||
		failure.Message != definition.Meaning || !strings.HasPrefix(failure.ReportToken, "rpt_") || strings.Contains(body, cause) {
		t.Fatalf("response = %d %q %q, want the opaque internal storage failure without %q", recorder.Code, recorder.Header().Get("Content-Type"), body, cause)
	}
	header, err := base64.RawURLEncoding.DecodeString(recorder.Header().Get("X-Scenery-Storage-Error"))
	if err != nil || string(header)+"\n" != body {
		t.Fatalf("X-Scenery-Storage-Error = %q (%v), want the body %q", header, err, body)
	}
	for line := range strings.Lines(logged.String()) {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["msg"] != "internal failure" || record["report_token"] != failure.ReportToken {
			continue
		}
		if record["code"] != "SCN9000" || record["cause"] != cause {
			t.Fatalf("internal failure log = %v, want code SCN9000 and cause %q", record, cause)
		}
		return
	}
	t.Fatalf("no internal failure was logged for %s: %q", failure.ReportToken, logged.String())
}
