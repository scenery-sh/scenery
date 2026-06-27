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

	console.SetupOutput("==> Atlas target: sqlite:///tmp/demo.sqlite", "stdout")
	console.SetupOutput("==> Atlas dry-run: var/atlas/plans/plan.txt", "stdout")
	console.SetupOutput("Schema is synced, no changes to be made", "stdout")
	console.SetupOutput("==> No database changes needed", "stdout")

	got := out.String()
	for _, want := range []string{
		"  • Atlas target: sqlite:///tmp/demo.sqlite\n",
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

	console.SetupOutput("==> Atlas target: sqlite:///tmp/example.sqlite", "stdout")

	var event runEvent
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if event.Type != "setup.output" || event.Data["line"] != "==> Atlas target: sqlite:///tmp/example.sqlite" || event.Data["stream"] != "stdout" {
		t.Fatalf("event = %+v", event)
	}
}

func TestSetupOutputWriterFlushesPartialLines(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", t.TempDir())
	writer := newSetupOutputWriter(console, "stdout", nil)

	if _, err := writer.Write([]byte("==> Atlas target: sqlite:///tmp/example.sqlite")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	writer.Close()

	if got := out.String(); !strings.Contains(got, "  • Atlas target: sqlite:///tmp/example.sqlite\n") {
		t.Fatalf("output = %q", got)
	}
}

func TestRunConsoleSetupOutputSuppressesSQLNoise(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", t.TempDir())

	console.SetupOutput(`NOTICE:  schema "scenery_auth" already exists, skipping`, "stderr")
	console.SetupOutput("NOTICE:  relation \"atlas_apply_cache\" already exists, skipping", "stderr")
	console.SetupOutput("CREATE SCHEMA", "stdout")
	console.SetupOutput("CREATE INDEX", "stdout")
	console.SetupOutput("COMMENT", "stdout")
	console.SetupOutput("==> Bootstrapping standard auth schema for local auth seeds", "stdout")
	console.SetupOutput("ERROR:  relation does not exist", "stderr")
	console.SetupOutput("applied 3 migrations", "stdout")

	got := out.String()
	for _, banned := range []string{"NOTICE", "CREATE SCHEMA", "CREATE INDEX", "COMMENT\n"} {
		if strings.Contains(got, banned) {
			t.Fatalf("setup output kept noise %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{
		"  • Bootstrapping standard auth schema for local auth seeds\n",
		"ERROR:  relation does not exist",
		"applied 3 migrations",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("setup output missing %q:\n%s", want, got)
		}
	}
}

func TestRunConsoleSetupOutputVerboseKeepsSQLNoise(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, true, false, "demo", t.TempDir())

	console.SetupOutput("CREATE SCHEMA", "stdout")
	if !strings.Contains(out.String(), "CREATE SCHEMA") {
		t.Fatalf("verbose setup output dropped SQL line:\n%s", out.String())
	}
}
