package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRootHelpIsOrienting(t *testing.T) {
	t.Parallel()

	help := rootHelpString()
	if !strings.Contains(help, "Scenery - build, run, and inspect app services.") {
		t.Fatalf("root help missing product line:\n%s", help)
	}
	if !strings.Contains(help, "scenery help <command>") || !strings.Contains(help, "Local dev:") || !strings.Contains(help, "App resources:") {
		t.Fatalf("root help missing orientation sections:\n%s", help)
	}
	if strings.Contains(help, "--app-root") || strings.Contains(help, "scenery logs query") {
		t.Fatalf("root help should not include full grammar:\n%s", help)
	}
	for _, line := range strings.Split(help, "\n") {
		if len(line) > 80 {
			t.Fatalf("root help line over 80 columns (%d): %q\n%s", len(line), line, help)
		}
	}
}

func TestHelpAllContainsCanonicalCommands(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeHelpAll(&buf)
	usage := buf.String()
	for _, want := range []string{
		"scenery up",
		"scenery ps",
		"scenery logs",
		"scenery console",
		"scenery down",
		"scenery prune",
		"scenery serve",
		"scenery worker",
		"scenery build",
		"scenery check",
		"scenery test",
		"scenery inspect",
		"scenery generate",
		"scenery db",
		"scenery task",
		"scenery traces",
		"scenery metrics",
		"scenery doctor",
		"scenery version",
		"scenery upgrade",
		"scenery system",
		"scenery harness",
	} {
		if !strings.Contains(usage, want) {
			t.Fatalf("help all missing %q:\n%s", want, usage)
		}
	}
	for _, removed := range []string{
		"scenery dev ",
		"scenery attach ",
		"scenery status ",
		"scenery agent ",
		"scenery edge ",
		"scenery toolchain ",
		"scenery temporal ",
		"scenery admin ",
		"scenery psql ",
		"scenery gen ",
		"scenery run ",
		"scenery script ",
		"scenery db sync ",
		"scenery inspect traces ",
		"scenery inspect metrics ",
	} {
		if strings.Contains(usage, removed) {
			t.Fatalf("help all contains removed spelling %q:\n%s", removed, usage)
		}
	}
}

func TestCommandHelpAndJSONManifest(t *testing.T) {
	t.Parallel()

	logs, ok := findHelpCommand([]string{"logs"})
	if !ok {
		t.Fatal("logs help not found")
	}
	var logsHelp bytes.Buffer
	writeCommandHelp(&logsHelp, logs)
	if got := logsHelp.String(); !strings.Contains(got, "scenery logs query --query <logsql>") || !strings.Contains(got, "--jsonl") {
		t.Fatalf("logs help missing usage or flags:\n%s", got)
	}
	dbBranch, ok := findHelpCommand([]string{"db", "branch", "status"})
	if !ok || dbBranch.Command != "db branch" {
		t.Fatalf("db branch topic resolved to %+v, %v", dbBranch, ok)
	}
	var manifest helpManifest
	var jsonOut bytes.Buffer
	if err := writeHelpJSON(&jsonOut); err != nil {
		t.Fatalf("writeHelpJSON: %v", err)
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &manifest); err != nil {
		t.Fatalf("help json: %v\n%s", err, jsonOut.String())
	}
	if manifest.SchemaVersion != helpManifestSchemaVersion {
		t.Fatalf("schema version = %q", manifest.SchemaVersion)
	}
	foundPS := false
	foundDown := false
	foundUpgrade := false
	for _, command := range manifest.Commands {
		if command.Command == "ps" && command.JSON && strings.Contains(strings.Join(command.Usage, "\n"), "scenery ps [--json]") {
			foundPS = true
		}
		if command.Command == "down" && command.JSON && strings.Contains(strings.Join(command.Usage, "\n"), "scenery down") && strings.Contains(strings.Join(command.Flags, "\n"), "--json") {
			foundDown = true
		}
		if command.Command == "upgrade" && command.JSON && strings.Contains(strings.Join(command.Usage, "\n"), "scenery upgrade") && strings.Contains(strings.Join(command.Flags, "\n"), "--toolchain installed|all|none") {
			foundUpgrade = true
		}
	}
	if !foundPS {
		t.Fatalf("help manifest missing ps json command: %+v", manifest.Commands)
	}
	if !foundDown {
		t.Fatalf("help manifest missing down json command: %+v", manifest.Commands)
	}
	if !foundUpgrade {
		t.Fatalf("help manifest missing upgrade json command: %+v", manifest.Commands)
	}
}

func TestRunHelpCommands(t *testing.T) {
	out := captureStdout(t, func() error {
		return run(nil)
	})
	if !strings.Contains(out, "Scenery - build, run, and inspect app services.") {
		t.Fatalf("bare run help = %q", out)
	}
	out = captureStdout(t, func() error {
		return run([]string{"help", "all"})
	})
	if !strings.Contains(out, "Scenery command reference") || !strings.Contains(out, "scenery db postgres start") {
		t.Fatalf("help all output = %q", out)
	}
	out = captureStdout(t, func() error {
		return run([]string{"help", "logs"})
	})
	if !strings.Contains(out, "scenery logs tail --query <logsql>") {
		t.Fatalf("help logs output = %q", out)
	}
	out = captureStdout(t, func() error {
		return run([]string{"help", "--json"})
	})
	if !strings.Contains(out, `"schema_version": "scenery.help.v1"`) {
		t.Fatalf("help json output = %q", out)
	}
}

func TestRemovedTopLevelCommandsFail(t *testing.T) {
	for _, command := range []string{
		"dev",
		"attach",
		"status",
		"agent",
		"edge",
		"toolchain",
		"temporal",
		"admin",
		"psql",
		"gen",
		"run",
		"script",
	} {
		err := run([]string{command})
		if err == nil || err.Error() != `unknown command "`+command+`"; use `+"`scenery help`" {
			t.Fatalf("run(%q) error = %v", command, err)
		}
	}
}

func TestCanonicalCommandParsers(t *testing.T) {
	t.Parallel()

	if _, err := parseDevArgs([]string{"--app-root", "/tmp/app", "--detach"}); err != nil {
		t.Fatalf("parse up args: %v", err)
	}
	if _, err := parseStatusArgs([]string{"--json", "--app-root", "/tmp/app"}); err != nil {
		t.Fatalf("parse ps args: %v", err)
	}
	if _, err := parseStatusArgs([]string{"--session", "current"}); err == nil || !strings.Contains(err.Error(), "use --app-root") {
		t.Fatalf("parse ps --session error = %v", err)
	}
	if err := workerCommand([]string{"deployment"}); err == nil || !strings.Contains(err.Error(), "scenery worker deployment") {
		t.Fatalf("worker deployment usage error = %v", err)
	}
	if err := dbCommand([]string{"sync"}); err == nil || err.Error() != `unknown db command "sync"` {
		t.Fatalf("db sync error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"traces", "--json"}); err == nil || !strings.Contains(err.Error(), "use `scenery traces list`") {
		t.Fatalf("inspect traces error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"metrics", "--json"}); err == nil || !strings.Contains(err.Error(), "use `scenery metrics list`") {
		t.Fatalf("inspect metrics error = %v", err)
	}
}
