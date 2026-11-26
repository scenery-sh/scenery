package runtime

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	// Uses a raw net.Listener so replying before reading the body avoids net/http's 500ms rstAvoidanceDelay.
	var (
		sawMu         sync.Mutex
		signalReached func()
	)
	earlyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var earlyWG sync.WaitGroup
	earlyDone := make(chan struct{})
	t.Cleanup(func() {
		_ = earlyListener.Close()
		<-earlyDone
		earlyWG.Wait()
	})
	go func() {
		defer close(earlyDone)
		for {
			conn, acceptErr := earlyListener.Accept()
			if acceptErr != nil {
				return
			}
			earlyWG.Add(1)
			go func(c net.Conn) {
				defer earlyWG.Done()
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				reader := bufio.NewReader(c)
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil || line == "\r\n" || line == "\n" {
						break
					}
				}
				sawMu.Lock()
				fn := signalReached
				sawMu.Unlock()
				if fn != nil {
					fn()
				}
				_, _ = io.WriteString(c, "HTTP/1.1 401 Unauthorized\r\nConnection: close\r\nContent-Length: 0\r\nX-Scenery-Debug: upstream-leak\r\n\r\n")
				if tcpConn, ok := c.(*net.TCPConn); ok {
					_ = tcpConn.CloseWrite()
				}
				_, _ = io.Copy(io.Discard, reader)
			}(conn)
		}
	}()
	targetURL := &url.URL{Scheme: "http", Host: earlyListener.Addr().String()}
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
	_ = earlyListener.Close()
	<-earlyDone
	earlyWG.Wait()

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

func TestWorkerIngressNeutralizesHijackedWebSocketUpgrade(t *testing.T) {
	wsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = wsListener.Close()
		wg.Wait()
	})
	go func() {
		for {
			conn, acceptErr := wsListener.Accept()
			if acceptErr != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				reader := bufio.NewReader(c)
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil || line == "\r\n" || line == "\n" {
						break
					}
				}
				_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: ok\r\n\r\n")
				msg, _ := reader.ReadString('\n')
				if msg == "ping\n" {
					_, _ = io.WriteString(c, "pong\n")
				}
			}(conn)
		}
	}()

	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: wsListener.Addr().String()})
	for _, tc := range []struct {
		name        string
		development bool
		wantGen     string
	}{
		{name: "production", development: false, wantGen: ""},
		{name: "development", development: true, wantGen: "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set(processGenerationHeader, "7")
				w.Header().Set("X-Scenery-Debug", "rw-leak")
				w.Header().Set("Access-Control-Expose-Headers", "X-Exposed, X-Scenery-Debug, X-App-Worker-Pid")
				proxy.ServeHTTP(w, req)
			})
			srv := httptest.NewServer(neutralWorkerIngress(upstream, tc.development))
			defer srv.Close()

			conn, dialErr := net.Dial("tcp", srv.Listener.Addr().String())
			if dialErr != nil {
				t.Fatal(dialErr)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, writeErr := io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); writeErr != nil {
				t.Fatal(writeErr)
			}
			br := bufio.NewReader(conn)
			statusLine, statusErr := br.ReadString('\n')
			if statusErr != nil || !strings.Contains(statusLine, "101 Switching Protocols") {
				t.Fatalf("statusLine = (%q, %v), want 101 Switching Protocols", statusLine, statusErr)
			}
			headers, hdrErr := textproto.NewReader(br).ReadMIMEHeader()
			if hdrErr != nil {
				t.Fatal(hdrErr)
			}
			hdr := http.Header(headers)
			if !tc.development {
				assertNoSceneryOrAppIdentityLeak(t, hdr)
				if got := hdr.Get("Access-Control-Expose-Headers"); got != "X-Exposed" {
					t.Fatalf("prod Access-Control-Expose-Headers = %q, want X-Exposed", got)
				}
			} else {
				if got := hdr.Get("X-App-Generation"); got != tc.wantGen {
					t.Fatalf("dev X-App-Generation = %q, want %q", got, tc.wantGen)
				}
				if got := hdr.Get(processGenerationHeader); got != "" {
					t.Fatalf("dev leaked %s=%q", processGenerationHeader, got)
				}
				if got := hdr.Get("X-Scenery-Debug"); got != "" {
					t.Fatalf("dev leaked X-Scenery-Debug=%q", got)
				}
			}
			if _, writeErr := io.WriteString(conn, "ping\n"); writeErr != nil {
				t.Fatal(writeErr)
			}
			reply, replyErr := br.ReadString('\n')
			if replyErr != nil || reply != "pong\n" {
				t.Fatalf("upgraded stream reply = (%q, %v), want pong\\n", reply, replyErr)
			}
		})
	}
}
