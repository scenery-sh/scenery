package runtime

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessHostForwardsEachRequestToTheOwningProcess(t *testing.T) {
	directory, err := os.MkdirTemp("", "sph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	link := &processLinkConfig{Token: processLinkTestToken, Processes: map[string]processLinkTarget{
		"missing_missing": {Network: "unix", Address: filepath.Join(directory, "missing.sock")},
	}}
	for _, process := range []string{"echo_echo", "greeter_greeter"} {
		socket := filepath.Join(directory, process+".sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"process": process, "method": req.Method, "uri": req.RequestURI, "host": req.Host,
				"forwarded_for": strings.Join(req.Header.Values("X-Forwarded-For"), ","), "accept_encoding": req.Header.Get("Accept-Encoding"),
			})
		}))
		_ = backend.Listener.Close()
		backend.Listener = listener
		backend.Start()
		t.Cleanup(backend.Close)
		link.Processes[process] = processLinkTarget{Network: "unix", Address: socket}
	}
	handler, err := newProcessHostHandler(ProcessHostConfig{Name: "multiservice", Fallback: "echo_echo", Routes: []ProcessHostRoute{
		{Process: "echo_echo", Methods: []string{"POST"}, Path: "/echo"},
		{Process: "echo_echo", Methods: []string{"GET"}, Path: "/items/:id"},
		{Process: "greeter_greeter", Methods: []string{"POST"}, Path: "/greet"},
		{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/items/special"},
		{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/files/*path", PathTail: true},
		{Process: "missing_missing", Methods: []string{"GET"}, Path: "/down"},
	}}, link)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method, target string, headers map[string]string) (*httptest.ResponseRecorder, map[string]string) {
		request := httptest.NewRequest(method, target, nil)
		request.Host = "api.example.test"
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var body map[string]string
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder, body
	}
	for _, check := range []struct {
		method, target, process string
		headers                 map[string]string
	}{
		{method: "POST", target: "/greet?lang=cs", process: "greeter_greeter"},
		{method: "POST", target: "/echo", process: "echo_echo"},
		{method: "GET", target: "/items/special", process: "greeter_greeter"},
		{method: "GET", target: "/items/42", process: "echo_echo"},
		{method: "HEAD", target: "/files/a/b%2Fc", process: "greeter_greeter"},
		{method: "DELETE", target: "/greet", process: "greeter_greeter"},
		{method: "OPTIONS", target: "/items/special", process: "greeter_greeter", headers: map[string]string{"Access-Control-Request-Method": "PUT"}},
		{method: "OPTIONS", target: "/items/42", process: "echo_echo", headers: map[string]string{"Access-Control-Request-Method": "GET"}},
		{method: "OPTIONS", target: "/greet", process: "greeter_greeter", headers: map[string]string{"Access-Control-Request-Method": "POST"}},
		{method: "GET", target: "/__scenery/config", process: "echo_echo"},
		{method: "GET", target: "/unknown", process: "echo_echo"},
	} {
		recorder, body := serve(check.method, check.target, check.headers)
		if check.method == "HEAD" {
			if recorder.Code != http.StatusOK {
				t.Errorf("%s %s status = %d", check.method, check.target, recorder.Code)
			}
			continue
		}
		if recorder.Code != http.StatusOK || body["process"] != check.process || body["method"] != check.method || body["uri"] != check.target {
			t.Errorf("%s %s reached %#v (status %d), want %s", check.method, check.target, body, recorder.Code, check.process)
		}
	}
	_, body := serve("POST", "/greet", map[string]string{"X-Forwarded-For": "203.0.113.7", "Accept-Encoding": "gzip"})
	if body["host"] != "api.example.test" || body["forwarded_for"] != "203.0.113.7" || body["accept_encoding"] != "gzip" {
		t.Fatalf("forwarded request headers = %#v", body)
	}
	recorder, _ := serve("GET", "/down", nil)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"unavailable"`) || !strings.Contains(recorder.Body.String(), "missing_missing") {
		t.Fatalf("unavailable process response = %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := newProcessHostHandler(ProcessHostConfig{Fallback: "echo_echo", Routes: []ProcessHostRoute{{Process: "unknown", Methods: []string{"GET"}, Path: "/x"}}}, link); err == nil {
		t.Fatal("route to a process without a link target was accepted")
	}
}
