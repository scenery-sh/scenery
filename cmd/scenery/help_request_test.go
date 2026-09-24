package main

import (
	"slices"
	"strings"
	"testing"
)

// -h and --help on any advertised command path ask for that command's help,
// with operands left out of the path; they are not help requests after "--",
// and an app command keeps its own help.
func TestHelpRequestTopicsResolveTheCommandPath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args   []string
		topics []string
		help   bool
	}{
		{args: []string{"--help"}, topics: []string{}, help: true},
		{args: []string{"-h", "-o", "json"}, topics: []string{"-o", "json"}, help: true},
		{args: []string{"up", "--help"}, topics: []string{"up"}, help: true},
		{args: []string{"db", "migrate", "--app-root", ".", "-h"}, topics: []string{"db", "migrate"}, help: true},
		{args: []string{"config", "set", "KEY", "--help", "-o", "json"}, topics: []string{"config", "set", "KEY", "-o", "json"}, help: true},
		{args: []string{"deploy", "host.example", "--help"}, topics: []string{"deploy"}, help: true},
		{args: []string{"task", "run", "web:build", "--", "-h"}},
		{args: []string{"orders", "create", "--help"}},
		{args: []string{"up"}},
	} {
		topics, help := helpRequestTopics(test.args)
		if help != test.help || (help && !slices.Equal(topics, test.topics)) {
			t.Errorf("helpRequestTopics(%q) = %q, %t; want %q, %t", test.args, topics, help, test.topics, test.help)
		}
		if help {
			if _, ok := findHelpCommand(withoutJSONOutput(topics)); len(withoutJSONOutput(topics)) > 0 && !ok {
				t.Errorf("topics %q name no help entry", topics)
			}
		}
	}
}

func withoutJSONOutput(topics []string) []string {
	if index := slices.Index(topics, "-o"); index >= 0 {
		return topics[:index]
	}
	return topics
}

func TestCommandHelpFlagPrintsHelpWithoutRunningTheCommand(t *testing.T) {
	output := captureStdout(t, func() error {
		return runWithCLITelemetry([]string{"down", "--app-root", t.TempDir(), "-h"}, nil)
	})
	if !strings.Contains(output, "Usage:") || !strings.Contains(output, "scenery down") {
		t.Fatalf("down -h = %q", output)
	}
	if command, mode := telemetryCommand([]string{"up", "--help"}), telemetryMode([]string{"up", "--help"}); command != "help" || mode != "oneshot" {
		t.Fatalf("telemetry of up --help = %s %s", command, mode)
	}
}

// An unknown command or subcommand is refused with the closest known ones,
// which are suggested and never run.
func TestUnknownCommandsSuggestTheClosestKnownOnes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args    []string
		message string
	}{
		{args: []string{"upp"}, message: `unknown command "upp"; did you mean "up"? Run ` + "`scenery help`"},
		{args: []string{"sytem", "agent"}, message: `unknown command "sytem"; did you mean "system"?`},
		{args: []string{"zzz"}, message: `unknown command "zzz"; run ` + "`scenery help`"},
		{args: []string{"inspect", "buld", "-o", "json"}, message: `unknown inspect command "buld"; did you mean "build"? Run ` + "`scenery help inspect`"},
		{args: []string{"db", "mgrate"}, message: `unknown db command "mgrate"; did you mean "migrate"?`},
	} {
		err := runWithCLITelemetry(test.args, nil)
		if err == nil || cliExitCode(err) != 2 || !strings.HasPrefix(err.Error(), test.message) {
			t.Errorf("scenery %s: error = %v (exit %d); want prefix %q", strings.Join(test.args, " "), err, cliExitCode(err), test.message)
		}
	}
	if err := helpCommand([]string{"upp"}); err == nil || !strings.Contains(err.Error(), `did you mean "up"?`) {
		t.Fatalf("help upp = %v", err)
	}
}

// Only a command whose usage lines all start with a listed subcommand has a
// complete list; an operand-taking command parses its own words.
func TestUnknownSubcommandNeedsACompleteList(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"db", "inspect", "snapshot", "system"} {
		entry, _ := findHelpCommand([]string{command})
		if !helpSubcommandsComplete(entry) {
			t.Errorf("%s subcommands are not complete: %q", command, entry.Subcommands)
		}
	}
	for _, args := range [][]string{{"deploy", "host.example"}, {"generate", "--target", "x"}, {"db"}, {"logs", "--follow"}} {
		if err := unknownSubcommandError(args); err != nil {
			t.Errorf("unknownSubcommandError(%q) = %v", args, err)
		}
	}
	if distance := editDistance("statsu", "status"); distance != 1 {
		t.Fatalf("transposition distance = %d", distance)
	}
}
