package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// A Go handler behind the worker Host reads the same HTTP request as it does
// in process, except for the credentials the Host consumed at ingress.
func TestWorkerHostForwardsIngressRequestToGoWorker(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	const process, operation, binding = "orders", "orders/operation/update", "orders/binding/update_http"
	identity := workerabi.Identity{ABI: workerabi.Version, Service: "orders/service/api", ContractRevision: "contract", ImplementationRevision: "build"}
	plan := WorkerServicePlan{
		Address: identity.Service, Module: "orders", Name: "api", Process: process, Runtime: "go",
		Operations: map[string]WorkerOperationPlan{
			operation: {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "std.type.unit"}},
		},
	}
	observed := make(chan shared.Request, 1)
	if err := RegisterWorkerOperation(operation, func(context.Context, []byte) ([]byte, shared.ByteStream, error) {
		observed <- *CurrentRequest()
		return []byte(`{"kind":"result","name":"ok","value":{}}`), shared.ByteStream{}, nil
	}); err != nil {
		t.Fatal(err)
	}

	directory, err := os.MkdirTemp("/tmp", "abi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "w")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	launch := workerabi.Launch{Identity: identity, Token: "test-token"}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: launch.Handler(invokeGoWorker)}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()
	client := workerabi.NewClient(socket, launch.Token, identity)
	defer client.Close()
	generation := &processHostGeneration{
		number: 1, lifetime: t.Context(),
		instances: map[string]*processHostInstance{
			process: {worker: client, spec: processGenerationInstance{
				Identity: processInstanceIdentity{ContractRevision: identity.ContractRevision},
			}},
		},
	}
	workers := &workerHost{host: &processHost{}, codec: contract.ManifestTypes(nil).Normalize}

	request := httptest.NewRequest(http.MethodPut, "/orders/42", nil)
	request.Header.Set("X-Correlation-Id", "corr-1")
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Cookie", "app_access=secret")
	request.Header.Set("X-Scenery-Cron-Execution", "forged")
	state := newExternalState(&Endpoint{Service: plan.Name, Name: binding}, request, shared.PathParams{{Name: "id", Value: "42"}}, nil, AuthInfo{UID: "user-1"})
	ctx := context.WithValue(withState(t.Context(), state), processHostGenerationKey{}, generation)
	ctx = withRuntimeInvocation(ctx, state)
	if _, err := workers.invoke(ctx, plan, plan.Operations[operation], operation, binding, "direct", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	seen := <-observed
	if seen.Method != http.MethodPut || seen.Path != "/orders/42" || seen.PathParams.Get("id") != "42" ||
		seen.Headers.Get("X-Correlation-Id") != "corr-1" {
		t.Fatalf("request observed by the Go worker: %+v", seen)
	}
	if seen.Headers.Get("Authorization") != "" || seen.Headers.Get("Cookie") != "" || seen.Headers.Get("X-Scenery-Cron-Execution") != "" {
		t.Fatalf("ingress credentials or internal headers reached the worker: %v", seen.Headers)
	}
	if seen.CronIdempotencyKey != "" {
		t.Fatalf("external header spoofed CronIdempotencyKey = %q", seen.CronIdempotencyKey)
	}
}

func assertNoSceneryOrAppIdentityLeak(t *testing.T, sets ...http.Header) {
	t.Helper()
	for _, headerSet := range sets {
		for k, vs := range headerSet {
			lowerKey := strings.ToLower(k)
			if strings.Contains(lowerKey, "scenery") || strings.HasPrefix(lowerKey, "x-app-") {
				t.Fatalf("wire header/trailer key leaked internal identity: %s=%v", k, vs)
			}
			for _, v := range vs {
				lowerVal := strings.ToLower(v)
				if strings.Contains(lowerVal, "scenery") || strings.Contains(lowerVal, "x-app-") {
					t.Fatalf("wire header/trailer value leaked internal identity: %s=%q", k, v)
				}
			}
		}
	}
}

func sendRawChunkedPOST(t *testing.T, addr, rawHTTP string) (*http.Response, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, rawHTTP); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestProductionWorkerIngressNeutralityAndInternalPaths(t *testing.T) {
	type inboundObservation struct {
		trailerHeader string
		trailers      http.Header
	}
	routes := newRouteTable()
	observedInbound := make(chan inboundObservation, 8)
	routes.Handle([]string{http.MethodGet, http.MethodPost}, "/api/ping", func(w http.ResponseWriter, req *http.Request, _ routeParams) {
		if req.Header.Get("X-Scenery-Cron-Execution") != "" || req.Header.Get("X-Scenery-Generation") != "" {
			t.Errorf("external X-Scenery-* header passed ingress: %v", req.Header)
		}
		var trailers http.Header
		if req.Method == http.MethodPost {
			if req.URL.Query().Get("close_unread") == "1" {
				_ = req.Body.Close()
			} else {
				_, _ = io.ReadAll(req.Body)
			}
			trailers = req.Trailer.Clone()
		}
		observedInbound <- inboundObservation{
			trailerHeader: req.Header.Get("Trailer"),
			trailers:      trailers,
		}
		w.Header().Set("X-App-Generation", "1")
		w.Header().Set("X-App-Worker-Pid", "99")
		w.Header().Set("X-App-Worker-Revision", "rev-1")
		w.Header().Set("X-Scenery-Debug", "leak")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id, X-Scenery-Debug, X-App-Worker-Pid")
		w.Header().Set("Trailer", "X-Checksum, X-Scenery-Debug, X-App-Worker-Pid")
		w.Header().Set(http.TrailerPrefix+"X-Scenery-Unannounced", "secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
		w.Header().Set("X-Checksum", "sha256:ok")
		w.Header().Set("X-Scenery-Debug", "leak-trailer")
		w.Header().Set("X-App-Worker-Pid", "99")
		w.Header().Set(http.TrailerPrefix+"X-Scenery-Post", "leak-post")
	})
	routes.Handle([]string{http.MethodGet}, "/api/ping-list-trailers", func(w http.ResponseWriter, req *http.Request, _ routeParams) {
		switch req.URL.Query().Get("mode") {
		case "announced":
			w.Header().Set("Trailer", "Access-Control-Expose-Headers, X-Scenery-Debug, X-App-Worker-Pid")
			w.Header().Set(http.TrailerPrefix+"Trailer", "X-Scenery-Only, X-App-Worker-Pid")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			w.Header().Set("Access-Control-Expose-Headers", "X-Exposed-After, X-Scenery-Post, X-App-Worker-Pid")
		case "prefix":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			_ = http.NewResponseController(w).Flush()
			w.Header().Set(http.TrailerPrefix+"Access-Control-Expose-Headers", "X-Exposed-Prefix, X-Scenery-Post, X-App-Worker-Pid")
			w.Header().Set(http.TrailerPrefix+"Trailer", "X-Allowed-Later, X-Scenery-Post, X-App-Worker-Pid")
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			_ = http.NewResponseController(w).Flush()
			w.Header().Set(http.TrailerPrefix+"Access-Control-Expose-Headers", "X-Scenery-Only, X-App-Worker-Pid")
		}
	})
	routes.Handle([]string{http.MethodGet}, "/api/files/mountain-scenery.png", func(w http.ResponseWriter, _ *http.Request, _ routeParams) {
		w.WriteHeader(http.StatusOK)
	})
	host := &processHost{
		routes: routes, workers: &workerHost{}, developmentCall: false,
		current: &processHostGeneration{number: 1},
	}
	handler := neutralWorkerIngress(http.HandlerFunc(host.serveIngress), false)

	for _, path := range []string{"/__scenery/process/v1/generations", "/__SCENERY/config"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", path, rec.Code)
		}
	}
	userRec := httptest.NewRecorder()
	handler.ServeHTTP(userRec, httptest.NewRequest(http.MethodGet, "/api/files/mountain-scenery.png", nil))
	if userRec.Code != http.StatusOK {
		t.Fatalf("user path containing scenery returned %d, want 200", userRec.Code)
	}
	rec := httptest.NewRecorder()
	pingReq := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	pingReq.Header.Set("X-Scenery-Cron-Execution", "forged")
	pingReq.Header.Set("X-Scenery-Generation", "99")
	pingReq.Header.Set("Trailer", "X-Client-Trailer, X-Scenery-Internal")
	handler.ServeHTTP(rec, pingReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/ping status = %d", rec.Code)
	}
	obs := <-observedInbound
	if obs.trailerHeader != "X-Client-Trailer" {
		t.Fatalf("inbound Trailer header = %q, want X-Client-Trailer", obs.trailerHeader)
	}
	for _, forbidden := range []string{"X-App-Generation", "X-App-Worker-Pid", "X-App-Worker-Revision", "X-Scenery-Debug"} {
		if got := rec.Header().Get(forbidden); got != "" {
			t.Fatalf("production response leaked %s=%q", forbidden, got)
		}
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-Id" {
		t.Fatalf("Access-Control-Expose-Headers = %q, want X-Request-Id", got)
	}
	if got := rec.Header().Get("Trailer"); got != "X-Checksum" {
		t.Fatalf("Trailer header = %q, want X-Checksum", got)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Case 1: announced client trailers with both X-Scenery-* and allowed trailer.
	wireReq, err := http.NewRequest(http.MethodPost, srv.URL+"/api/ping", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	wireReq.ContentLength = -1
	wireReq.Trailer = http.Header{
		"X-Scenery-Internal": {"forged"},
		"X-Client-Trailer":   {"ok"},
	}
	resp, err := srv.Client().Do(wireReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.Trailer["X-Checksum"]; !ok || len(resp.Trailer) != 1 {
		_ = resp.Body.Close()
		t.Fatalf("announced wire trailers = %+v, want only X-Checksum", resp.Trailer)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "pong" {
		t.Fatalf("wire response body = (%q, %v), want pong", body, err)
	}
	obs = <-observedInbound
	if obs.trailers.Get("X-Scenery-Internal") != "" || obs.trailers.Get("X-Client-Trailer") != "ok" {
		t.Fatalf("inbound trailers = %+v, want X-Scenery-Internal stripped and X-Client-Trailer=ok", obs.trailers)
	}
	if got := resp.Trailer.Get("X-Checksum"); got != "sha256:ok" {
		t.Fatalf("wire Trailer X-Checksum = %q, want sha256:ok", got)
	}
	for _, forbidden := range []string{"X-Scenery-Debug", "X-Scenery-Unannounced", "X-Scenery-Post", "X-App-Worker-Pid"} {
		if got := resp.Trailer.Get(forbidden); got != "" {
			t.Fatalf("wire trailer leaked %s=%q", forbidden, got)
		}
	}
	assertNoSceneryOrAppIdentityLeak(t, resp.Header, resp.Trailer)

	// Case 2: announced X-Scenery-* trailer only (request.Trailer becomes empty before Read).
	onlySceneryReq, err := http.NewRequest(http.MethodPost, srv.URL+"/api/ping", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	onlySceneryReq.ContentLength = -1
	onlySceneryReq.Trailer = http.Header{"X-Scenery-Internal": {"forged-only"}}
	resp, err = srv.Client().Do(onlySceneryReq)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	obs = <-observedInbound
	if len(obs.trailers) != 0 {
		t.Fatalf("inbound trailers when only X-Scenery-* announced = %+v, want empty", obs.trailers)
	}
	assertNoSceneryOrAppIdentityLeak(t, resp.Header, resp.Trailer)

	// Case 2b: handler closes body without calling Read when trailers were announced.
	closeAnnouncedReq, err := http.NewRequest(http.MethodPost, srv.URL+"/api/ping?close_unread=1", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	closeAnnouncedReq.ContentLength = -1
	closeAnnouncedReq.Trailer = http.Header{
		"X-Scenery-Close": {"forged"},
		"X-Client-Close":  {"ok"},
	}
	resp, err = srv.Client().Do(closeAnnouncedReq)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	obs = <-observedInbound
	if obs.trailers.Get("X-Scenery-Close") != "" || obs.trailers.Get("X-Client-Close") != "ok" {
		t.Fatalf("close_unread announced inbound trailers = %+v, want X-Scenery-Close stripped and X-Client-Close=ok", obs.trailers)
	}
	assertNoSceneryOrAppIdentityLeak(t, resp.Header, resp.Trailer)

	// Case 3: unannounced chunked trailers without Trailer header through host.serveIngress
	// (where req.WithContext shallow-copies request after ingress initializes Trailer map).
	rawResp, rawBody := sendRawChunkedPOST(t, srv.Listener.Addr().String(),
		"POST /api/ping HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n"+
			"2\r\nhi\r\n0\r\nX-Scenery-Unannounced: forged\r\nX-Client-Unannounced: ok\r\n\r\n")
	if string(rawBody) != "pong" {
		t.Fatalf("unannounced wire response body = %q, want pong", rawBody)
	}
	obs = <-observedInbound
	if obs.trailers.Get("X-Scenery-Unannounced") != "" || obs.trailers.Get("X-Client-Unannounced") != "ok" {
		t.Fatalf("unannounced inbound trailers = %+v, want X-Scenery-Unannounced stripped and X-Client-Unannounced=ok", obs.trailers)
	}
	assertNoSceneryOrAppIdentityLeak(t, rawResp.Header, rawResp.Trailer)

	// Case 4: handler closes chunked body without calling Read when no Trailer header was sent.
	closeResp, closeBody := sendRawChunkedPOST(t, srv.Listener.Addr().String(),
		"POST /api/ping?close_unread=1 HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n"+
			"2\r\nhi\r\n0\r\nX-Scenery-Close: forged\r\nX-Client-Close: ok\r\n\r\n")
	if string(closeBody) != "pong" {
		t.Fatalf("close_unread wire response body = %q, want pong", closeBody)
	}
	obs = <-observedInbound
	if obs.trailers.Get("X-Scenery-Close") != "" || obs.trailers.Get("X-Client-Close") != "ok" {
		t.Fatalf("close_unread inbound trailers = %+v, want X-Scenery-Close stripped and X-Client-Close=ok", obs.trailers)
	}
	assertNoSceneryOrAppIdentityLeak(t, closeResp.Header, closeResp.Trailer)

	// Case 5: concurrent Read in Proxy Transport vs Close in ReverseProxy defer on early upstream response.
	var (
		sawMu         sync.Mutex
		signalReached func()
	)
	earlyUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sawMu.Lock()
		fn := signalReached
		sawMu.Unlock()
		if fn != nil {
			fn()
		}
		w.Header().Set("Connection", "close")
		w.Header().Set("X-Scenery-Debug", "upstream-leak")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer earlyUpstream.Close()
	targetURL, err := url.Parse(earlyUpstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(neutralWorkerIngress(httputil.NewSingleHostReverseProxy(targetURL), false))
	defer proxySrv.Close()
	proxyClient := proxySrv.Client()
	for i := range 20 {
		upstreamReached := make(chan struct{})
		markReached := sync.OnceFunc(func() { close(upstreamReached) })
		sawMu.Lock()
		signalReached = markReached
		sawMu.Unlock()

		pr, pw := io.Pipe()
		reqCtx, cancelReq := context.WithTimeout(t.Context(), 5*time.Second)
		proxyReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, proxySrv.URL+"/proxy", pr)
		if reqErr != nil {
			cancelReq()
			t.Fatal(reqErr)
		}
		proxyReq.ContentLength = -1
		proxyReq.Trailer = http.Header{
			"X-Scenery-Internal": {"forged"},
			"X-Client-Trailer":   {"ok"},
		}
		writeDone := make(chan struct{})
		go func() {
			defer close(writeDone)
			if _, writeErr := io.WriteString(pw, "chunk1-"); writeErr != nil {
				return
			}
			select {
			case <-upstreamReached:
				_, _ = io.WriteString(pw, "chunk2")
				_ = pw.Close()
			case <-reqCtx.Done():
				_ = pw.CloseWithError(reqCtx.Err())
			}
		}()
		proxyResp, doErr := proxyClient.Do(proxyReq)
		if doErr != nil {
			cancelReq()
			_ = pw.CloseWithError(doErr)
			<-writeDone
			t.Fatalf("iteration %d: %v", i, doErr)
		}
		_, _ = io.ReadAll(proxyResp.Body)
		_ = proxyResp.Body.Close()
		cancelReq()
		_ = pw.Close()
		<-writeDone
		if proxyResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("iteration %d: status=%d, want 401", i, proxyResp.StatusCode)
		}
		assertNoSceneryOrAppIdentityLeak(t, proxyResp.Header, proxyResp.Trailer)
	}

	// Outbound list-valued trailers (pre-announced post-body Access-Control-Expose-Headers, TrailerPrefix list values, and empty removal).
	for _, tc := range []struct {
		mode       string
		wantExpose string
		wantTrails string
		wantCount  int
	}{
		{mode: "announced", wantExpose: "X-Exposed-After", wantCount: 1},
		{mode: "prefix", wantExpose: "X-Exposed-Prefix", wantTrails: "X-Allowed-Later", wantCount: 2},
		{mode: "empty-prefix", wantCount: 0},
	} {
		listResp, err := srv.Client().Get(srv.URL + "/api/ping-list-trailers?mode=" + tc.mode)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(listResp.Body)
		_ = listResp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(listResp.Trailer) != tc.wantCount {
			t.Fatalf("mode=%s trailer count=%d (%+v), want %d", tc.mode, len(listResp.Trailer), listResp.Trailer, tc.wantCount)
		}
		if got := listResp.Trailer.Get("Access-Control-Expose-Headers"); got != tc.wantExpose {
			t.Fatalf("mode=%s trailer Access-Control-Expose-Headers=%q, want %q", tc.mode, got, tc.wantExpose)
		}
		if got := listResp.Trailer.Get("Trailer"); got != tc.wantTrails {
			t.Fatalf("mode=%s trailer Trailer=%q, want %q", tc.mode, got, tc.wantTrails)
		}
		assertNoSceneryOrAppIdentityLeak(t, listResp.Header, listResp.Trailer)
	}
}

func TestWorkerHostHTTPDurableMapsTransportStatuses(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	workers := &workerHost{host: &processHost{}, codec: contract.ManifestTypes(nil).Normalize}
	plan := WorkerServicePlan{
		Address: "orders/service/orders", Module: "orders", Name: "orders", Process: "orders", Runtime: "go",
		Operations: map[string]WorkerOperationPlan{
			"orders/operation/run": {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "std.type.unit"}},
		},
		Durable: map[string]WorkerDurablePlan{
			"orders/execution/run": {
				Operation: "orders/operation/run",
				Registration: ContractDurableRegistration{
					Address: "orders/execution/run", ExternalName: "orders.run",
				},
			},
		},
	}
	binding := WorkerBindingPlan{
		Address: "orders/binding/run_http", Operation: "orders/operation/run", Protocol: "http",
		Delivery: "enqueue", Execution: "orders/execution/run",
		Policy: ContractHTTPPolicy{
			ServiceName: "orders", OperationAddress: "orders/operation/run", BindingAddress: "orders/binding/run_http",
			AuthorizationStrategy: "public", TransportStatuses: map[string]int{"dispatch.rejected": http.StatusTooManyRequests},
		},
		HTTP: &WorkerHTTPPlan{
			Method: "POST", Path: "/api/run", Access: Public,
			Responses: map[string]WorkerResponsePlan{
				"dispatch.enqueued": {Status: http.StatusAccepted, Codec: "json", Media: []string{"application/json"}},
			},
		},
	}
	if err := workers.registerBinding(plan, binding); err != nil {
		t.Fatal(err)
	}
	directBinding := WorkerBindingPlan{
		Address: "orders/binding/direct_http", Operation: "orders/operation/run", Protocol: "http", Delivery: "call",
		Policy: ContractHTTPPolicy{ServiceName: "orders", OperationAddress: "orders/operation/run", BindingAddress: "orders/binding/direct_http", AuthorizationStrategy: "public"},
		HTTP:   &WorkerHTTPPlan{Method: "GET", Path: "/api/direct", Access: Public, Responses: map[string]WorkerResponsePlan{"result.ok": {Status: http.StatusOK, Codec: "json", Media: []string{"application/json"}}}},
	}
	if err := workers.registerBinding(plan, directBinding); err != nil {
		t.Fatal(err)
	}
	endpoints := make(map[string]*Endpoint)
	for _, ep := range listEndpoints() {
		endpoints[ep.Name] = ep
	}
	if len(endpoints) != 2 || endpoints[binding.Address] == nil || endpoints[directBinding.Address] == nil {
		t.Fatalf("unexpected endpoints: %+v", endpoints)
	}
	_, err := endpoints[binding.Address].Invoke(t.Context(), nil, json.RawMessage(`{}`))
	transportErr, ok := errors.AsType[*ContractTransportError](err)
	if !ok || transportErr.Outcome != "dispatch.rejected" || transportErr.Status != http.StatusTooManyRequests {
		t.Fatalf("durable error = %#v (%T), want dispatch.rejected HTTP 429", err, err)
	}
	_, directErr := endpoints[directBinding.Address].Invoke(t.Context(), nil, json.RawMessage(`{}`))
	if _, wrapped := errors.AsType[*ContractTransportError](directErr); wrapped || errs.Code(directErr) != errs.Unavailable {
		t.Fatalf("direct error = %#v (%T), want unwrapped errs.Unavailable", directErr, directErr)
	}
}

func TestWorkerHostRegistersAndDispatchesStreamingMCPTool(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	identity := workerabi.Identity{ABI: workerabi.Version, Service: "house/service/house", ContractRevision: "contract", ImplementationRevision: "impl"}
	directory, err := os.MkdirTemp("/tmp", "abi-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "w")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	launch := workerabi.Launch{Identity: identity, Token: "test-token"}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: launch.Handler(func(context.Context, workerabi.Invocation) (workerabi.Result, error) {
		return workerabi.Result{
			Outcome:    []byte(`{"kind":"result","name":"ok","value":{"name":"note.txt","content":""}}`),
			Stream:     io.NopCloser(strings.NewReader("hello")),
			StreamSize: 5,
		}, nil
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	client := workerabi.NewClient(socket, launch.Token, identity)
	defer client.Close()
	codec := contract.ManifestTypes{
		"house/record/Export": {
			Kind:   "record",
			Fields: []contract.ManifestField{{Name: "name", Type: "string"}, {Name: "content", Type: "bytes"}},
		},
	}.Normalize
	plan := WorkerServicePlan{
		Address: identity.Service, Module: "house", Name: "house", Process: "house", Runtime: "rust",
		Operations: map[string]WorkerOperationPlan{
			"house/operation/export": {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "house/record/Export"}},
		},
		MCPTools: []WorkerMCPToolPlan{{
			ID: "app/assistant/support#house/binding/export_mcp", Name: "house__export",
			AssistantAddress: "app/assistant/support", Binding: "house/binding/export_mcp",
			Operation: "house/operation/export", Execution: "house/execution/export_direct", Delivery: "call",
			Policy:       ContractHTTPPolicy{ServiceName: "house", OperationAddress: "house/operation/export", BindingAddress: "house/binding/export_mcp", AuthorizationStrategy: "public"},
			Limits:       MCPToolLimits{MaxInputBytes: 1024, MaxResultBytes: 1024},
			Approval:     "never",
			StreamFields: map[string][]string{"result.ok": {"content"}},
		}},
	}
	host := &processHost{
		contract: "contract", routes: newRouteTable(), closing: make(chan struct{}),
		generations: map[uint64]*processHostGeneration{},
	}
	if err := host.registerWorkerServices([]WorkerServicePlan{plan}, codec, nil, nil); err != nil {
		t.Fatal(err)
	}
	generation := &processHostGeneration{
		number: 1, lifetime: t.Context(),
		instances: map[string]*processHostInstance{"house": {worker: client, spec: processGenerationInstance{Identity: processInstanceIdentity{ContractRevision: "contract"}}}},
	}
	host.current = generation
	host.generations[1] = generation

	outcome, err := (processHostMCPDispatcher{host: host}).CallTool(t.Context(), MCPToolCallContext{
		Principal: "user-1", AssistantAddress: "app/assistant/support",
	}, "house__export", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Outcome != "ok" || string(outcome.Value) != `{"content":"aGVsbG8=","name":"note.txt"}` {
		t.Fatalf("unexpected MCP outcome: %+v (%s)", outcome, outcome.Value)
	}
	_ = errs.OK
}

func TestClassifyWorkerDurableWaitErrorPreservesContextCanceled(t *testing.T) {
	rejected := contractDurableFailure("dispatch.rejected", context.Canceled)
	if got := classifyWorkerDurableWaitError(rejected); !errors.Is(got, rejected) || ContractDurableFailureOutcome(got) != "dispatch.rejected" {
		t.Fatalf("dispatch.rejected = %#v, want original failure", got)
	}
	timeout := contractDurableFailure("dispatch.wait_timeout", context.DeadlineExceeded)
	if got := classifyWorkerDurableWaitError(timeout); !errors.Is(got, timeout) || ContractDurableFailureOutcome(got) != "dispatch.wait_timeout" {
		t.Fatalf("dispatch.wait_timeout = %#v, want original failure", got)
	}
	wrappedCancel := fmt.Errorf("wait: %w", context.Canceled)
	if got := classifyWorkerDurableWaitError(wrappedCancel); !errors.Is(got, context.Canceled) {
		t.Fatalf("context.Canceled = %#v, want context.Canceled preserved", got)
	} else if _, isTransport := errors.AsType[*ContractTransportError](got); isTransport {
		t.Fatalf("context.Canceled must not be wrapped in *ContractTransportError: %#v", got)
	}
	internal := contractDurableFailure("system.internal", errors.New("store failure"))
	gotInternal := classifyWorkerDurableWaitError(internal)
	transportErr, ok := errors.AsType[*ContractTransportError](gotInternal)
	if !ok || transportErr.Outcome != "system.internal" || transportErr.Status != http.StatusInternalServerError {
		t.Fatalf("system.internal = %#v (%T), want system.internal HTTP 500", gotInternal, gotInternal)
	}
}

func TestWorkerHostRegistersAssistantsAndMCPManifest(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	manifest := mcpcontract.Manifest{
		Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision,
		ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "cap-1", Capabilities: []mcpcontract.Capability{},
	}
	encoded, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	host := &processHost{name: "support-app", contract: "contract", routes: newRouteTable(), closing: make(chan struct{}), generations: map[uint64]*processHostGeneration{}}
	plan := WorkerAssistantPlan{
		Address: "app/assistant/support", Name: "support", Path: "/assistants/support",
		Policy: ContractHTTPPolicy{
			ServiceName: "support", BindingAddress: "app/assistant/support",
			GatewayAddress: "app/http_gateway/api", AuthorizationStrategy: "public",
			CORS: "origins", AllowedOrigins: []string{"https://app.example"},
		},
		RuntimeRevision: "runtime-1", CapabilityRevision: "cap-1", MCPManifest: encoded,
	}
	RegisterAuthHandler(&AuthHandler{
		Name: "test-auth",
		Authenticate: func(_ context.Context, token string) (AuthInfo, error) {
			return AuthInfo{UID: token}, nil
		},
	})
	if err := host.registerWorkerAssistants([]WorkerAssistantPlan{plan}, nil); err != nil {
		t.Fatal(err)
	}
	endpoints := listEndpoints()
	local, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host.local, host.localRoutes = local.Handler, newRouteTable()
	for _, ep := range endpoints {
		registerEndpointRoute(host.localRoutes, ep, func(http.ResponseWriter, *http.Request, routeParams) {})
	}
	host.workers = &workerHost{}
	host.current = &processHostGeneration{number: 1}
	if _, ok := assistantMCPManifestFor("app/assistant/support"); !ok {
		t.Fatal("assistant MCP manifest was not registered")
	}
	unauthReq := httptest.NewRequest(http.MethodPost, "/assistants/support/v1/conversations", strings.NewReader(`{"message":{"role":"user","content":"hello"}}`))
	unauthRec := httptest.NewRecorder()
	host.serveIngress(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated assistant route = %d (%s), want 401", unauthRec.Code, unauthRec.Body.String())
	}
	authReq := httptest.NewRequest(http.MethodPost, "/assistants/support/v1/conversations", strings.NewReader(`{"message":{"role":"user","content":"hello"}}`))
	authReq.Header.Set("Authorization", "Bearer user-1")
	authReq.Header.Set("Origin", "https://app.example")
	authRec := httptest.NewRecorder()
	host.serveIngress(authRec, authReq)
	if authRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("authenticated assistant route without helper = %d (%s), want 503", authRec.Code, authRec.Body.String())
	}
	if gotOrigin := authRec.Header().Get("Access-Control-Allow-Origin"); gotOrigin != "https://app.example" {
		t.Fatalf("assistant POST Access-Control-Allow-Origin = %q, want https://app.example", gotOrigin)
	}
	preflightReq := httptest.NewRequest(http.MethodOptions, "/assistants/support/v1/conversations", nil)
	preflightReq.Header.Set("Origin", "https://app.example")
	preflightReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflightRec := httptest.NewRecorder()
	host.serveIngress(preflightRec, preflightReq)
	if preflightRec.Code != http.StatusNoContent {
		t.Fatalf("assistant preflight = %d, want 204", preflightRec.Code)
	}
	if gotOrigin := preflightRec.Header().Get("Access-Control-Allow-Origin"); gotOrigin != "https://app.example" {
		t.Fatalf("assistant preflight Access-Control-Allow-Origin = %q, want https://app.example", gotOrigin)
	}
}

func TestExecuteTypedEndpointMapsContextCanceledTo499WithoutSystemError(t *testing.T) {
	ep := &Endpoint{
		Invoke: func(ctx context.Context, _ []any, _ any) (any, error) {
			return nil, ClassifyContractDurableWaitError(fmt.Errorf("wait: %w", context.Canceled), 0, 0)
		},
	}
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	_, status, _, err := executeTypedEndpoint(ep, canceledCtx, nil, nil)
	if status != 499 || !errors.Is(err, context.Canceled) {
		t.Fatalf("executeTypedEndpoint(canceled) = status %d, err %v; want 499 + context.Canceled", status, err)
	}
	if _, isTransport := errors.AsType[*ContractTransportError](err); isTransport {
		t.Fatalf("canceled wait must not produce *ContractTransportError: %#v", err)
	}
	if typed, ok := errs.As(err); !ok || typed.Code != errs.Canceled {
		t.Fatalf("canceled wait code = %v, want errs.Canceled", err)
	}

	_, activeStatus, _, activeErr := executeTypedEndpoint(ep, t.Context(), nil, nil)
	if activeStatus != http.StatusInternalServerError {
		t.Fatalf("executeTypedEndpoint(active ctx, downstream canceled) = status %d, err %v; want 500", activeStatus, activeErr)
	}
	if typed, ok := errs.As(activeErr); ok && typed.Code == errs.Canceled {
		t.Fatalf("active request context must not classify downstream context.Canceled as errs.Canceled: %v", activeErr)
	}
}

func TestMainWorkerProcessHostValidatesAndAppliesRuntimeRole(t *testing.T) {
	t.Setenv("SCENERY_ROLE", "bogus")
	err := MainWorkerProcessHost(t.Context(), ProcessHostConfig{}, "contract", contract.ManifestTypes(nil).Normalize, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported SCENERY_ROLE") {
		t.Fatalf("invalid role error = %v, want unsupported SCENERY_ROLE", err)
	}

	t.Setenv("SCENERY_ROLE", "api")
	role, err := runtimeRoleFromEnv()
	if err != nil || role != runtimeRoleAPI {
		t.Fatalf("runtimeRoleFromEnv() = %q, %v; want api", role, err)
	}
	stopWorkers := startDurableLocalWorkers(t.Context(), nil, nil, string(role))
	if stopErr := stopWorkers(t.Context()); stopErr != nil {
		t.Fatalf("stopWorkers(api) = %v", stopErr)
	}

	// With SCENERY_ROLE=worker, MainWorkerProcessHost must not bind ListenAddr even
	// when that TCP port is already occupied.
	defer replaceGlobalRegistryForTest()()
	previousLink := processLinkState
	processLinkState = &processLinkRuntime{clients: map[processLinkTarget]*http.Client{}}
	t.Cleanup(func() { processLinkState = previousLink })
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()

	sockDir, err := os.MkdirTemp("/tmp", "wr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	linkPath := filepath.Join(sockDir, "link.json")
	linkData, err := json.Marshal(map[string]any{
		"token":    strings.Repeat("t", 32),
		"dispatch": map[string]string{"network": "unix", "address": filepath.Join(sockDir, "control.sock"), "protocol": "h2c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkPath, linkData, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCENERY_PROCESS_LINK", linkPath)
	t.Setenv("SCENERY_ROLE", "worker")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- MainWorkerProcessHost(ctx, ProcessHostConfig{Name: "role-app", ListenAddr: occupied.Addr().String()}, "contract", contract.ManifestTypes(nil).Normalize, nil)
	}()

	client := &http.Client{Transport: &http.Transport{
		Protocols: workerabi.Protocols(),
		DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(dialCtx, "unix", filepath.Join(sockDir, "control.sock"))
		},
	}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(3 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://host/__scenery/process/v1/generations", nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		resp, reqErr := client.Do(req)
		if reqErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("worker-role host control endpoint did not become ready: %v / %v", reqErr, <-done)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if runErr := <-done; runErr != nil {
		t.Fatalf("MainWorkerProcessHost(worker role) = %v", runErr)
	}

	// Verify serveWorkerDevCall does not panic when HTTP ingress is disabled in worker role,
	// and retiring an already-retired generation is idempotent.
	host := &processHost{
		name:            "role-app",
		contract:        "contract",
		developmentCall: true,
		ingressDisabled: true,
		routes:          newRouteTable(),
		closing:         make(chan struct{}),
		current:         &processHostGeneration{number: 2, instances: map[string]*processHostInstance{}},
		generations:     map[uint64]*processHostGeneration{},
	}
	host.generations[2] = host.current
	devCallReq := httptest.NewRequest(http.MethodPost, "http://host/__scenery/process/v1/dev-call?method=GET&path=%2Fhello", nil)
	devCallRec := httptest.NewRecorder()
	host.serveWorkerDevCall(devCallRec, devCallReq)
	if devCallRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("dev-call in worker role = %d (%s), want 503", devCallRec.Code, devCallRec.Body.String())
	}
	if status, reason := host.retire(1, false); status != http.StatusNoContent {
		t.Fatalf("retire(already retired generation 1) = %d (%q), want 204", status, reason)
	}
}

func TestProcessHostPublishScopesWorkerAndHostConfigPerGeneration(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	prevSnapshot, _ := LoadConfigSnapshot()
	t.Cleanup(func() { UpdateWorkerHostConfig(prevSnapshot) })

	const serviceAddr, processName = "mod/service/svc", "svc"
	host := &processHost{
		name:        "cfg-app",
		contract:    "contract",
		required:    []string{processName},
		routes:      newRouteTable(),
		closing:     make(chan struct{}),
		generations: map[uint64]*processHostGeneration{},
		workers: &workerHost{
			services: map[string]WorkerServicePlan{serviceAddr: {Address: serviceAddr, Process: processName, Module: "mod", Name: "svc"}},
			config:   map[string]*ConfigSnapshot{},
		},
	}
	host.workers.host = host
	instanceSpec := processGenerationInstance{
		Network: "unix", Address: "/tmp/worker-config-test.sock", Protocol: "h2c", PID: 123,
		WorkerService: serviceAddr, WorkerToken: strings.Repeat("w", 32),
		Identity: processInstanceIdentity{
			ContractRevision: "contract", ImplementationRevision: "impl-1", BuildInputDigest: "digest-1", GoTarget: "worker",
		},
	}
	hostSnap1 := &ConfigSnapshot{Kind: ConfigSnapshotKind, AppID: "cfg-app", Environment: "local", Revision: "rev-1", Consumer: "$host", Values: map[string]json.RawMessage{"p": json.RawMessage(`"v1"`)}, Public: []string{"p"}, Secrets: map[string][]byte{}}
	workerSnap1 := &ConfigSnapshot{Kind: ConfigSnapshotKind, AppID: "cfg-app", Environment: "local", Revision: "rev-1", Consumer: serviceAddr, Values: map[string]json.RawMessage{"k": json.RawMessage(`"one"`)}, Secrets: map[string][]byte{}}
	hostSnap2 := &ConfigSnapshot{Kind: ConfigSnapshotKind, AppID: "cfg-app", Environment: "local", Revision: "rev-2", Consumer: "$host", Values: map[string]json.RawMessage{"p": json.RawMessage(`"v2"`)}, Public: []string{"p"}, Secrets: map[string][]byte{}}
	workerSnap2 := &ConfigSnapshot{Kind: ConfigSnapshotKind, AppID: "cfg-app", Environment: "local", Revision: "rev-2", Consumer: serviceAddr, Values: map[string]json.RawMessage{"k": json.RawMessage(`"two"`)}, Secrets: map[string][]byte{}}

	manifestFor := func(gen uint64, procs map[string]processGenerationInstance, hostCfg, workerCfg *ConfigSnapshot) processGenerationManifest {
		return processGenerationManifest{
			Generation:       gen,
			ContractRevision: "contract",
			Identity:         processInstanceIdentity{ContractRevision: "contract", ImplementationRevision: "impl", BuildInputDigest: "digest", GoTarget: "worker"},
			Processes:        procs,
			HostConfig:       hostCfg,
			WorkerConfig:     map[string]*ConfigSnapshot{serviceAddr: workerCfg},
		}
	}

	if err := host.publish(manifestFor(1, map[string]processGenerationInstance{processName: instanceSpec}, hostSnap1, workerSnap1)); err != nil {
		t.Fatalf("publish(gen 1) = %v", err)
	}
	readCapabilityRevision := func(gen *processHostGeneration) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://host"+workerabi.CapabilityPrefix+"config/snapshot", nil)
		req = req.WithContext(context.WithValue(req.Context(), processHostGenerationKey{}, gen))
		rec := httptest.NewRecorder()
		host.workers.serveCapability(serviceAddr, rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("serveCapability(config/snapshot) = %d (%s)", rec.Code, rec.Body.String())
		}
		var snap ConfigSnapshot
		if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
		return snap.Revision
	}

	if err := host.publish(manifestFor(2, map[string]processGenerationInstance{}, hostSnap2, workerSnap2)); err == nil {
		t.Fatal("publish(gen 2 missing required process) unexpectedly succeeded")
	}
	if loaded, err := LoadConfigSnapshot(); err != nil || loaded == nil || loaded.Revision != "rev-1" {
		t.Fatalf("failed publish mutated host snapshot: (%+v, %v)", loaded, err)
	}
	gen1 := host.acquire(1)
	defer gen1.inFlight.Add(-1)
	if rev := readCapabilityRevision(gen1); rev != "rev-1" {
		t.Fatalf("gen1 capability revision after failed publish = %q, want rev-1", rev)
	}

	if err := host.publish(manifestFor(2, map[string]processGenerationInstance{processName: instanceSpec}, hostSnap2, workerSnap2)); err != nil {
		t.Fatalf("publish(gen 2) = %v", err)
	}
	if loaded, err := LoadConfigSnapshot(); err != nil || loaded == nil || loaded.Revision != "rev-2" {
		t.Fatalf("host snapshot after gen 2 publish = (%+v, %v), want rev-2", loaded, err)
	}
	gen2 := host.acquire(2)
	defer gen2.inFlight.Add(-1)
	if rev := readCapabilityRevision(gen1); rev != "rev-1" {
		t.Fatalf("draining gen1 capability revision after gen 2 publish = %q, want rev-1", rev)
	}
	if rev := readCapabilityRevision(gen2); rev != "rev-2" {
		t.Fatalf("gen2 capability revision = %q, want rev-2", rev)
	}
	pub1, err := publicConfiguration(context.WithValue(t.Context(), processHostGenerationKey{}, gen1))
	if err != nil || pub1.Revision != "rev-1" || string(pub1.Values["p"]) != `"v1"` {
		t.Fatalf("publicConfiguration(gen1) = (%+v, %v), want rev-1/v1", pub1, err)
	}
	pub2, err := publicConfiguration(context.WithValue(t.Context(), processHostGenerationKey{}, gen2))
	if err != nil || pub2.Revision != "rev-2" || string(pub2.Values["p"]) != `"v2"` {
		t.Fatalf("publicConfiguration(gen2) = (%+v, %v), want rev-2/v2", pub2, err)
	}
}

func TestProcessHostRetireFencesUnpublishedGenerationAndRejectsCanceledPublish(t *testing.T) {
	host := &processHost{
		name:        "fence-app",
		contract:    "contract",
		required:    []string{"svc"},
		routes:      newRouteTable(),
		closing:     make(chan struct{}),
		generations: map[uint64]*processHostGeneration{},
	}
	manifest := func(gen uint64) processGenerationManifest {
		return processGenerationManifest{
			Generation:       gen,
			ContractRevision: "contract",
			Identity:         processInstanceIdentity{ContractRevision: "contract", ImplementationRevision: "impl", BuildInputDigest: "digest", GoTarget: "worker"},
			Processes: map[string]processGenerationInstance{
				"svc": {
					Network: "unix", Address: "/tmp/fence-svc.sock", Protocol: "h2c", PID: 123,
					Identity: processInstanceIdentity{ContractRevision: "contract", ImplementationRevision: "impl-1", BuildInputDigest: "digest-1", GoTarget: "development"},
				},
			},
		}
	}
	if err := host.publishContext(context.Background(), manifest(1)); err != nil {
		t.Fatalf("publishContext(gen 1) = %v", err)
	}
	if status, _ := host.retire(2, false); status != http.StatusNotFound {
		t.Fatalf("retire(unpublished gen 2) = %d, want 404", status)
	}
	if err := host.publishContext(context.Background(), manifest(2)); err == nil {
		t.Fatal("publishContext(gen 2 after retire fence) unexpectedly succeeded")
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.publishContext(canceledCtx, manifest(3)); err == nil {
		t.Fatal("publishContext(canceled ctx) unexpectedly succeeded")
	}
	if status := host.status(); status.Current != 1 {
		t.Fatalf("host.status().Current = %d after fenced/canceled publishes, want 1", status.Current)
	}
	if err := host.publishContext(context.Background(), manifest(3)); err != nil {
		t.Fatalf("publishContext(gen 3) = %v", err)
	}
	if status := host.status(); status.Current != 3 {
		t.Fatalf("host.status().Current = %d, want 3", status.Current)
	}
}
