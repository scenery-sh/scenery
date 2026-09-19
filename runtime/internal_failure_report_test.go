package runtime

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"scenery.sh/internal/devreport"
	"scenery.sh/internal/machine"
)

func TestAMintedTokensCauseIsLoggedAndSentToTheSupervisor(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	reporter := &devReporter{appID: "shop", sessionID: "main-1", queue: make(chan devreport.ReportEnvelope, 4)}
	reporterMu.Lock()
	globalReporter = reporter
	reporterMu.Unlock()
	t.Cleanup(func() {
		slog.SetDefault(previous)
		machine.SetInternalFailureSink(nil)
		reporterMu.Lock()
		globalReporter = nil
		reporterMu.Unlock()
	})

	reportInternalFailures()
	machine.ReportInternalFailure("rpt_abc", "SCN9000", "open postgres://shop:hunter2@db/shop: refused")

	if text := logged.String(); !strings.Contains(text, "rpt_abc") || !strings.Contains(text, "refused") || strings.Contains(text, "hunter2") {
		t.Fatalf("log = %q", text)
	}
	select {
	case envelope := <-reporter.queue:
		failure := envelope.InternalFailure
		if envelope.Type != "internal-failure" || envelope.AppID != "shop" || envelope.SessionID != "main-1" || envelope.ReporterPID == 0 ||
			failure == nil || failure.ReportToken != "rpt_abc" || failure.Code != "SCN9000" || strings.Contains(failure.Cause, "hunter2") || failure.Timestamp.IsZero() {
			t.Fatalf("envelope = %+v failure = %+v", envelope, failure)
		}
	default:
		t.Fatal("the cause was not sent to the supervisor")
	}
}
