package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devreport"
	"testing"
	"testing/synctest"
)

type reportBatchTestController struct{ exportTestController }

func (c reportBatchTestController) dashboardAuthorizeReport(_ *http.Request, report devdash.ReportEnvelope) dashboardReportAuth {
	return dashboardReportAuth{Authorized: report.SessionID != "stale", Reason: "stale session"}
}

func TestReportBatchRejectsMixedAuthorizationBeforeAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var messages []string
		server := newDashboardServerWithControllerHooks(reportBatchTestController{}, t.TempDir(), "127.0.0.1:0", nil, dashboardServerHooks{exportLogEvent: func(event *devdash.LogEvent) { messages = append(messages, event.Message) }})
		defer func() { _ = server.Close() }()
		batch := devreport.ReportBatch{Reports: []devreport.ReportEnvelope{
			{Type: "log", SessionID: "current", LogEvent: &devreport.LogEvent{Message: "first"}},
			{Type: "log", SessionID: "stale", LogEvent: &devreport.LogEvent{Message: "second"}},
		}}
		body, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		server.handleReport(rec, httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewReader(body)))
		synctest.Wait()
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("mixed batch admitted: %d", rec.Code)
		}
		for _, message := range messages {
			if message == "first" || message == "second" {
				t.Fatal("partially admitted unauthorized batch")
			}
		}
		for _, invalid := range []string{`{"type":"log"}`, `{"reports":[]}`, `{"reports":[{"type":"log","app_id":"app"}],"extra":true}`, `{"reports":[{"type":"log","app_id":"app"}]} {}`} {
			rec = httptest.NewRecorder()
			server.handleReport(rec, httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewBufferString(invalid)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid current batch accepted: %s status=%d", invalid, rec.Code)
			}
		}
	})
}

func marshalTestReportBatch(report devdash.ReportEnvelope) ([]byte, error) {
	return json.Marshal(devreport.ReportBatch{Reports: []devreport.ReportEnvelope{report}})
}
