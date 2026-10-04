package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"scenery.sh/internal/devdash"
)

type traceRPCBackend struct {
	exportTestVictoria
	query  devdash.TraceQuery
	detail *devdash.TraceDetail
}

func (v *traceRPCBackend) GetTraceDetail(_ context.Context, q devdash.TraceQuery) (*devdash.TraceDetail, error) {
	v.query = q
	return v.detail, nil
}
func TestTraceRPCPinsScopeAndBoundsResponse(t *testing.T) {
	backend := &traceRPCBackend{detail: &devdash.TraceDetail{TraceID: strings.Repeat("a", 32), Spans: []devdash.TraceSpanDetail{}}}
	server := newDashboardServerWithController(exportTestController{runtimeRPCTestController: runtimeRPCTestController{status: devdash.AppStatus{AppID: "route", BaseAppID: "app", RuntimeAppID: "runtime", SessionID: "session"}}, victoria: backend}, t.TempDir(), "127.0.0.1:0", nil)
	defer func() { _ = server.Close() }()
	params := json.RawMessage(`{"app_id":"route","trace_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if _, err := server.dispatchRPC(context.Background(), "traces/get", params); err != nil {
		t.Fatal(err)
	}
	if backend.query.AppID != "app" || backend.query.SessionID != "session" {
		t.Fatalf("query scope %+v", backend.query)
	}
	if runtimeCallClassOf("traces/get") != runtimeWorkCall || runtimeCallClassOf("traces/list") != runtimeWorkCall {
		t.Fatal("trace reads consume control allowance")
	}
	server.rpc.limits.resultBytes = 1
	if _, err := server.dispatchRPC(context.Background(), "traces/get", params); err == nil || !strings.Contains(err.Error(), "SCN8013") {
		t.Fatalf("large response %v", err)
	}
	if _, err := server.dispatchRPC(context.Background(), "traces/get", json.RawMessage(`{"app_id":"route","trace_id":"bad"}`)); err == nil {
		t.Fatal("malformed trace accepted")
	}
}
