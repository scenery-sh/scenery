package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onlava.com/errs"
	"onlava.com/internal/wire"
)

func TestWireCapabilitiesAndBinaryCall(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	type request struct {
		Name string `json:"name"`
	}
	type response struct {
		Message string `json:"message"`
	}
	RegisterEndpoint(&Endpoint{
		Service:        "svc",
		Name:           "Hello",
		Access:         Public,
		Path:           "/hello/:id",
		Methods:        []string{http.MethodPost},
		PathParams:     []ParamSpec{{Name: "id", Kind: ParamInt}},
		PayloadType:    TypeOf[*request](),
		ResponseType:   TypeOf[*response](),
		WireID:         "svc.Hello",
		WireSchemaHash: "hash-hello",
		WireAvailable:  true,
		Invoke: func(_ context.Context, pathArgs []any, payload any) (any, error) {
			return &response{Message: payload.(*request).Name + ":" + fmt.Sprint(pathArgs[0])}, nil
		},
	})

	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	server := httptest.NewServer(httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + wire.CapabilitiesPath)
	if err != nil {
		t.Fatalf("GET capabilities: %v", err)
	}
	defer resp.Body.Close()
	var caps wire.Capabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if got := caps.Endpoints["svc.Hello"]; !got.Available || got.SchemaHash != "hash-hello" {
		t.Fatalf("capabilities endpoint = %+v", got)
	}

	body, err := wire.Encode(map[string]any{
		"schema_hash": "hash-hello",
		"method":      http.MethodPost,
		"path_params": map[string]any{"id": 42},
		"payload":     map[string]any{"name": "onlava"},
	})
	if err != nil {
		t.Fatalf("wire.Encode() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/_wire/svc.Hello", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", wire.ContentType)
	req.Header.Set(wire.CallIDHeader, "call-1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("wire call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wire status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, wire.ContentType) {
		t.Fatalf("wire content type = %q", got)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := wire.Decode(raw)
	if err != nil {
		t.Fatalf("wire.Decode() error = %v", err)
	}
	envelope := decoded.(map[string]any)
	result := envelope["result"].(map[string]any)
	if result["message"] != "onlava:42" {
		t.Fatalf("wire result = %#v", result)
	}

	recoverResp, err := http.Get(server.URL + wire.RecoverPathPrefix + "call-1")
	if err != nil {
		t.Fatalf("recover call: %v", err)
	}
	defer recoverResp.Body.Close()
	var recovered wireRecoveryRecord
	if err := json.NewDecoder(recoverResp.Body).Decode(&recovered); err != nil {
		t.Fatalf("decode recovery: %v", err)
	}
	recoveredResult := recovered.Result.(map[string]any)
	if recoveredResult["message"] != "onlava:42" {
		t.Fatalf("recovered result = %#v", recovered.Result)
	}
}

func TestWireApplicationErrorsDoNotAskClientToFallback(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	RegisterEndpoint(&Endpoint{
		Service:        "svc",
		Name:           "Fail",
		Access:         Public,
		Path:           "/svc.Fail",
		Methods:        []string{http.MethodPost},
		WireID:         "svc.Fail",
		WireSchemaHash: "hash-fail",
		WireAvailable:  true,
		Invoke: func(context.Context, []any, any) (any, error) {
			return nil, errs.B().Code(errs.InvalidArgument).Msg("bad app input").Err()
		},
	})
	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	server := httptest.NewServer(httpServer.Handler)
	defer server.Close()

	body, err := wire.Encode(map[string]any{"schema_hash": "hash-fail", "method": http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/_wire/svc.Fail", wire.ContentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("wire call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if got := resp.Header.Get(wire.FallbackHeader); got != "" {
		t.Fatalf("fallback header = %q, want empty", got)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := wire.Decode(raw)
	if err != nil {
		t.Fatalf("wire.Decode() error = %v", err)
	}
	envelope := decoded.(map[string]any)
	if envelope["ok"] != false {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func TestWireSchemaMismatchRequestsJSONFallback(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	RegisterEndpoint(&Endpoint{
		Service:        "svc",
		Name:           "Hello",
		Access:         Public,
		Path:           "/svc.Hello",
		Methods:        []string{http.MethodPost},
		WireID:         "svc.Hello",
		WireSchemaHash: "server-hash",
		WireAvailable:  true,
		Invoke: func(context.Context, []any, any) (any, error) {
			return nil, nil
		},
	})
	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	server := httptest.NewServer(httpServer.Handler)
	defer server.Close()

	body, err := wire.Encode(map[string]any{"schema_hash": "client-hash", "method": http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/_wire/svc.Hello", wire.ContentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("wire call: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get(wire.FallbackHeader); got != "json" {
		t.Fatalf("fallback header = %q, want json", got)
	}
}
