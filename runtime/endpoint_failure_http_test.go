package runtime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scenery.sh/errs"
	"scenery.sh/internal/devreport"
)

const standardSystemProblem = "{\"code\":\"system.internal\",\"message\":\"contract implementation failure\"}\n"

func TestAuthenticationFailureUsesStandardProblemOutcome(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	logged := captureRequestLogs(t)
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 32)}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	failures := map[string]error{
		"database":    errors.New("query sessions: dial tcp 10.0.0.5:5432: connection refused"),
		"internal":    errs.B().Code(errs.Internal).Msg("session cache shard 7 is corrupt").Err(),
		"data-loss":   errs.B().Code(errs.DataLoss).Msg("session row 42 lost its tenant").Err(),
		"expired":     errs.B().Code(errs.Unauthenticated).Msg("session expired").Err(),
		"unavailable": errs.B().Code(errs.Unavailable).Msg("session store is warming up").Err(),
	}
	RegisterAuthHandler(&AuthHandler{Service: "auth", Name: "AuthHandler", Authenticate: func(_ context.Context, token string) (AuthInfo, error) {
		return AuthInfo{}, failures[token]
	}})
	registerAuthenticatedEndpoints(t)
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/typed", "/raw"} {
		for _, token := range []string{"database", "internal", "data-loss"} {
			t.Run(strings.TrimPrefix(path, "/")+"/"+token, func(t *testing.T) {
				logged.Reset()
				recorder := serveWithToken(server.Handler, path, token)
				assertStandardSystemProblem(t, recorder)
				assertCauseObserved(t, reporter, logged, failures[token].Error())
			})
		}
	}

	rendered := []struct {
		path, token, contentType, body string
		status                         int
	}{
		{"/typed", "expired", "application/problem+json", "{\"code\":\"admission.unauthenticated\",\"message\":\"session expired\"}\n", http.StatusUnauthorized},
		{"/typed", "unavailable", "application/json", "{\"code\":\"unavailable\",\"message\":\"session store is warming up\"}\n", http.StatusServiceUnavailable},
		{"/raw", "expired", "application/json", "{\"code\":\"unauthenticated\",\"message\":\"session expired\"}\n", http.StatusUnauthorized},
		{"/raw", "unavailable", "application/json", "{\"code\":\"unavailable\",\"message\":\"session store is warming up\"}\n", http.StatusServiceUnavailable},
	}
	for _, want := range rendered {
		recorder := serveWithToken(server.Handler, want.path, want.token)
		if recorder.Code != want.status || recorder.Header().Get("Content-Type") != want.contentType || recorder.Body.String() != want.body {
			t.Errorf("%s %s response = %d %q %q, want %d %q %q", want.path, want.token, recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String(), want.status, want.contentType, want.body)
		}
	}
}

func TestAuthEndpointWithoutAuthHandlerUsesStandardProblemOutcome(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	logged := captureRequestLogs(t)
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 16)}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()
	registerAuthenticatedEndpoints(t)
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/typed", "/raw"} {
		logged.Reset()
		recorder := serveWithToken(server.Handler, path, "token")
		assertStandardSystemProblem(t, recorder)
		assertCauseObserved(t, reporter, logged, "auth endpoint configured but no auth handler registered")
	}
}

func TestRawEndpointPanicUsesStandardProblemOutcome(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	logged := captureRequestLogs(t)
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 16)}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()
	for path, handler := range map[string]http.HandlerFunc{
		"/panics": func(http.ResponseWriter, *http.Request) {
			panic("decrypt /private/keys/app.pem: bad padding")
		},
		"/streams": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			panic("stream source closed")
		},
	} {
		if err := RegisterEndpointChecked(&Endpoint{Service: "raw", Name: strings.TrimPrefix(path, "/"), Access: Public, Raw: true, Path: path, Methods: []string{http.MethodGet}, RawHandler: handler}); err != nil {
			t.Fatal(err)
		}
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	panicked := httptest.NewRecorder()
	server.Handler.ServeHTTP(panicked, httptest.NewRequest(http.MethodGet, "/panics", nil))
	assertStandardSystemProblem(t, panicked)
	assertCauseObserved(t, reporter, logged, "panic handling request: decrypt /private/keys/app.pem: bad padding")

	// A status the handler already wrote stays the response.
	streamed := httptest.NewRecorder()
	server.Handler.ServeHTTP(streamed, httptest.NewRequest(http.MethodGet, "/streams", nil))
	if streamed.Code != http.StatusOK || streamed.Body.String() != "partial" {
		t.Fatalf("streamed response = %d %q, want 200 %q", streamed.Code, streamed.Body.String(), "partial")
	}
}

// registerAuthenticatedEndpoints registers a typed and a raw endpoint that
// both require authentication and succeed once it passes.
func registerAuthenticatedEndpoints(t *testing.T) {
	t.Helper()
	endpoints := []*Endpoint{
		{
			Service: "contract", Name: "Typed", Access: Auth, Path: "/typed", Methods: []string{http.MethodGet},
			ContractPolicy: &ContractHTTPPolicy{AuthorizationStrategy: "public"},
			DecodeContractRequest: func(*http.Request, map[string]string) (ContractDecodedRequest, error) {
				return ContractDecodedRequest{}, nil
			},
			Invoke: func(context.Context, []any, any) (any, error) { return nil, nil },
			EncodeContractOutcome: func(*http.Request, any) (ContractHTTPResponse, error) {
				return ContractHTTPResponse{Status: http.StatusNoContent}, nil
			},
		},
		{
			Service: "contract", Name: "Raw", Access: Auth, Raw: true, Path: "/raw", Methods: []string{http.MethodGet},
			RawHandler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		},
	}
	for _, endpoint := range endpoints {
		if err := RegisterEndpointChecked(endpoint); err != nil {
			t.Fatal(err)
		}
	}
}

func serveWithToken(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// captureRequestLogs routes the default logger into a buffer for the test.
func captureRequestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logged
}

func assertStandardSystemProblem(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("Content-Type") != "application/problem+json" || recorder.Body.String() != standardSystemProblem {
		t.Fatalf("response = %d %q %q, want 500 application/problem+json %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String(), standardSystemProblem)
	}
}

// assertCauseObserved checks that the request's failure log and the end of its
// root trace span both carry the cause the caller never receives.
func assertCauseObserved(t *testing.T, reporter *devReporter, logged *bytes.Buffer, cause string) {
	t.Helper()
	var failureLog string
	for line := range strings.Lines(logged.String()) {
		if strings.Contains(line, `msg="request failed"`) {
			failureLog = line
		}
	}
	if !strings.Contains(failureLog, cause) {
		t.Errorf("request failure log = %q, want cause %q", failureLog, cause)
	}
	var spanEnd map[string]any
	for drained := false; !drained; {
		select {
		case envelope := <-reporter.queue:
			if envelope.TraceEvent == nil {
				continue
			}
			if end, ok := envelope.TraceEvent.Event["span_end"].(map[string]any); ok && end["request"] != nil {
				spanEnd = end
			}
		default:
			drained = true
		}
	}
	if spanEnd == nil {
		t.Fatal("no request span end was reported")
	}
	traced, _ := spanEnd["error"].(map[string]any)
	if message, _ := traced["msg"].(string); message != cause {
		t.Errorf("traced error = %#v, want %q", spanEnd["error"], cause)
	}
	if status := spanEnd["request"].(map[string]any)["http_status_code"]; status != http.StatusInternalServerError {
		t.Errorf("traced status = %#v, want 500", status)
	}
}
