package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRootHelpIsOrienting(t *testing.T) {
	help := rootHelpString()
	if !strings.Contains(help, "Onlava - build, run, and inspect app services.") {
		t.Fatalf("root help missing product line:\n%s", help)
	}
	if !strings.Contains(help, "onlava help <command>") || !strings.Contains(help, "Local session:") || !strings.Contains(help, "App resources:") {
		t.Fatalf("root help missing orientation sections:\n%s", help)
	}
	if strings.Contains(help, "--app-root") || strings.Contains(help, "onlava logs query") {
		t.Fatalf("root help should not include full grammar:\n%s", help)
	}
	for _, line := range strings.Split(help, "\n") {
		if len(line) > 80 {
			t.Fatalf("root help line over 80 columns (%d): %q\n%s", len(line), line, help)
		}
	}
}

func TestHelpAllContainsCanonicalCommands(t *testing.T) {
	var buf bytes.Buffer
	writeHelpAll(&buf)
	usage := buf.String()
	for _, want := range []string{
		"onlava up",
		"onlava ps",
		"onlava logs",
		"onlava console",
		"onlava down",
		"onlava prune",
		"onlava serve",
		"onlava worker",
		"onlava build",
		"onlava check",
		"onlava test",
		"onlava inspect",
		"onlava generate",
		"onlava db",
		"onlava task",
		"onlava traces",
		"onlava metrics",
		"onlava doctor",
		"onlava version",
		"onlava system",
		"onlava harness",
	} {
		if !strings.Contains(usage, want) {
			t.Fatalf("help all missing %q:\n%s", want, usage)
		}
	}
	for _, removed := range []string{
		"onlava dev ",
		"onlava attach ",
		"onlava status ",
		"onlava agent ",
		"onlava edge ",
		"onlava toolchain ",
		"onlava temporal ",
		"onlava admin ",
		"onlava psql ",
		"onlava gen ",
		"onlava run ",
		"onlava script ",
		"onlava db sync ",
		"onlava inspect traces ",
		"onlava inspect metrics ",
	} {
		if strings.Contains(usage, removed) {
			t.Fatalf("help all contains removed spelling %q:\n%s", removed, usage)
		}
	}
}

func TestCommandHelpAndJSONManifest(t *testing.T) {
	logs, ok := findHelpCommand([]string{"logs"})
	if !ok {
		t.Fatal("logs help not found")
	}
	var logsHelp bytes.Buffer
	writeCommandHelp(&logsHelp, logs)
	if got := logsHelp.String(); !strings.Contains(got, "onlava logs query --query <logsql>") || !strings.Contains(got, "--jsonl") {
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
	for _, command := range manifest.Commands {
		if command.Command == "ps" && command.JSON && strings.Contains(strings.Join(command.Usage, "\n"), "onlava ps [--json]") {
			foundPS = true
		}
		if command.Command == "down" && command.JSON && strings.Contains(strings.Join(command.Usage, "\n"), "onlava down") && strings.Contains(strings.Join(command.Flags, "\n"), "--json") {
			foundDown = true
		}
	}
	if !foundPS {
		t.Fatalf("help manifest missing ps json command: %+v", manifest.Commands)
	}
	if !foundDown {
		t.Fatalf("help manifest missing down json command: %+v", manifest.Commands)
	}
}

func TestRunHelpCommands(t *testing.T) {
	out := captureStdout(t, func() error {
		return run(nil)
	})
	if !strings.Contains(out, "Onlava - build, run, and inspect app services.") {
		t.Fatalf("bare run help = %q", out)
	}
	out = captureStdout(t, func() error {
		return run([]string{"help", "all"})
	})
	if !strings.Contains(out, "Onlava command reference") || !strings.Contains(out, "onlava db neon start") {
		t.Fatalf("help all output = %q", out)
	}
	out = captureStdout(t, func() error {
		return run([]string{"help", "logs"})
	})
	if !strings.Contains(out, "onlava logs tail --query <logsql>") {
		t.Fatalf("help logs output = %q", out)
	}
	out = captureStdout(t, func() error {
		return run([]string{"help", "--json"})
	})
	if !strings.Contains(out, `"schema_version": "onlava.help.v1"`) {
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
		if err == nil || err.Error() != `unknown command "`+command+`"; use `+"`onlava help`" {
			t.Fatalf("run(%q) error = %v", command, err)
		}
	}
}

func TestCanonicalCommandParsers(t *testing.T) {
	if _, err := parseDevArgs([]string{"--app-root", "/tmp/app", "--detach"}); err != nil {
		t.Fatalf("parse up args: %v", err)
	}
	if _, err := parseStatusArgs([]string{"--json", "--app-root", "/tmp/app", "--session", "current"}); err != nil {
		t.Fatalf("parse ps args: %v", err)
	}
	if err := workerCommand([]string{"deployment"}); err == nil || !strings.Contains(err.Error(), "onlava worker deployment") {
		t.Fatalf("worker deployment usage error = %v", err)
	}
	if err := dbCommand([]string{"sync"}); err == nil || err.Error() != `unknown db command "sync"` {
		t.Fatalf("db sync error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"traces", "--json"}); err == nil || !strings.Contains(err.Error(), "use `onlava traces list`") {
		t.Fatalf("inspect traces error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"metrics", "--json"}); err == nil || !strings.Contains(err.Error(), "use `onlava metrics list`") {
		t.Fatalf("inspect metrics error = %v", err)
	}
}
