package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunConsoleSetupOutputFormatsAtlasLines(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", t.TempDir())

	console.SetupOutput("==> Atlas target: postgres://scenery:***@127.0.0.1:52463/demo?sslmode=disable", "stdout")
	console.SetupOutput("==> Atlas dry-run: var/atlas/plans/plan.txt", "stdout")
	console.SetupOutput("Schema is synced, no changes to be made", "stdout")
	console.SetupOutput("==> No database changes needed", "stdout")

	got := out.String()
	for _, want := range []string{
		"  • Atlas target: postgres://scenery:***@127.0.0.1:52463/demo?sslmode=disable\n",
		"  • Atlas dry-run: var/atlas/plans/plan.txt\n",
		"  ✔ Atlas schema synced\n",
		"  ✔ No database changes needed\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted setup output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "==>") {
		t.Fatalf("formatted setup output kept raw marker:\n%s", got)
	}
}

func TestRunConsoleSetupOutputJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, true, "demo", t.TempDir())

	console.SetupOutput("==> Atlas target: postgres://example", "stdout")

	var event runEvent
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if event.Type != "setup.output" || event.Data["line"] != "==> Atlas target: postgres://example" || event.Data["stream"] != "stdout" {
		t.Fatalf("event = %+v", event)
	}
}

func TestSetupOutputWriterFlushesPartialLines(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", t.TempDir())
	writer := newSetupOutputWriter(console, "stdout", nil)

	if _, err := writer.Write([]byte("==> Atlas target: postgres://example")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	writer.Close()

	if got := out.String(); !strings.Contains(got, "  • Atlas target: postgres://example\n") {
		t.Fatalf("output = %q", got)
	}
}
