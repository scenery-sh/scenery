package main

import (
	"strings"
	"testing"
)

// A request the caller wrote wrongly is an invalid request that says what to
// correct. It must never reach the unclassified fallback, which reports an
// internal failure and withholds the message.
func TestMalformedInvocationsAreInvalidRequestsThatNameTheMistake(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, invocation := range []struct {
		args    string
		message string
	}{
		{"doctor assistant", `unexpected argument "assistant"`},
		{"doctor -o json assistant", `unexpected argument "assistant"`},
		{"ps -o json extra", `unexpected argument "extra"`},
		{"down extra", `unexpected argument "extra"`},
		{"check --app-root -o json", "missing value for --app-root"},
		{"up --port 99999", "--port must be between 0 and 65535"},
		{"doctor -o yaml", `unsupported output "yaml"; use human or json`},
		{"logs query --query x -o yaml", `unsupported output "yaml"; use json or jsonl`},
		{"logs --limit x", `invalid value "x" for --limit`},
		{"assistant", "missing assistant subcommand"},
		{"assistant nope", `unknown assistant subcommand "nope"`},
		{"assistant -o json status", `expected the assistant subcommand before "-o"; flags follow it`},
		{"assistant init", "missing assistant name"},
		{"assistant init -o json", `expected the assistant name before "-o"; flags follow it`},
		{"assistant init Bad -o json", `assistant name "Bad" must be lower_snake_case`},
		{"assistant init copilot -o json", "assistant init requires --mcp-server and --client"},
		{"assistant status copilot", "scenery assistant status currently requires -o json"},
		{"db nope", `unknown db command "nope"`},
		{"db -o json list", `expected the db subcommand before "-o"; flags follow it`},
		{"db list main extra", `unexpected argument "extra"`},
		{"db server nope", `unknown db server command "nope"`},
		{"db migrate --status --adopt-initial", "--status and --adopt-initial are mutually exclusive"},
		{"worktree create", "scenery worktree create requires <name>"},
		{"worktree list extra", `unexpected argument "extra"`},
		{"worktree nope", `unknown worktree command "nope"`},
		{"snapshot nope", `unknown snapshot command "nope"`},
		{"snapshot verify", "snapshot verify requires --input <file.zip>"},
		{"snapshot verify --input absent.zip", "snapshot input absent.zip does not exist"},
		{"snapshot load --input x.zip --db --mode nope", "snapshot load requires --mode overwrite|merge"},
		{"task inspect", "missing task target"},
		{"task inspect a:b --lang nope", "--lang must be go or typescript"},
		{"validate inspect", "missing validation profile"},
		{"validate one two", `unexpected argument "two"`},
		{"worker --log-format nope", `invalid --log-format "nope"`},
		{"worker durable jobs", "scenery worker durable jobs requires list, inspect, cancel, or retry"},
		{"worker durable jobs list", "--service is required"},
		{"traces nope", `unknown traces command "nope"`},
		{"traces -o json list", `expected the traces subcommand before "-o"; flags follow it`},
		{"metrics query", "missing required --promql"},
		{"metrics query --promql up --step x", `invalid step duration "x"`},
		{"metrics query --promql up --start x", `invalid time "x"; use RFC3339`},
		{"telemetry --since x", `invalid since duration "x"`},
		{"inspect -o json", `expected the inspect subject before "-o"; flags follow it`},
		{"inspect app extra -o json", `unexpected argument "extra"`},
		{"inspect harness nope -o json", `unknown inspect harness topic "nope"; use artifact, diagnostics or timing`},
		{"inspect harness diagnostics --severity nope -o json", "--severity must be error or warning"},
		{"system nope", `unknown system command "nope"`},
		{"system toolchain path", "scenery system toolchain path requires --tool <name>"},
		{"harness ui extra -o json", `unexpected argument "extra"`},
	} {
		err := run(strings.Fields(invocation.args))
		if err == nil {
			t.Errorf("scenery %s: accepted", invocation.args)
			continue
		}
		diagnostic := cliErrorDiagnostic(err)
		if code := cliExitCode(err); code != 2 || diagnostic.Code != "SCN8001" || !strings.Contains(diagnostic.Message, invocation.message) {
			t.Errorf("scenery %s: exit %d, %s %q; want exit 2, SCN8001 naming %q", invocation.args, code, diagnostic.Code, diagnostic.Message, invocation.message)
		}
	}
}

func TestFlagValueErrorsUseTheCLIsOwnWords(t *testing.T) {
	t.Parallel()
	var json bool
	var limit int
	flags := newCLIFlagSet("probe")
	registerJSONOutput(flags, &json)
	flags.IntVar(&limit, "limit", 1, "")
	flags.String("app-root", "", "")
	for args, want := range map[string]string{
		"-o yaml":           `unsupported output "yaml"; use human or json`,
		"--limit many":      `invalid value "many" for --limit`,
		"--app-root -o":     "missing value for --app-root",
		"--app-root":        "missing value for --app-root",
		"--app-root=-o x":   "",
		"--nope":            `unknown flag "--nope"`,
		"--app-root - rest": "",
	} {
		_, err := parseCLIFlags(flags, strings.Fields(args))
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: refused: %v", args, err)
		case want != "" && (err == nil || err.Error() != want || cliExitCode(err) != 2):
			t.Errorf("%s: got %v (exit %d), want %q with exit 2", args, err, cliExitCode(err), want)
		}
	}
}
