package main

import (
	"slices"
	"strings"
	"testing"
)

func TestCLIGrammarCasesFollowTheAdvertisedUsage(t *testing.T) {
	t.Parallel()
	cases, skipped := harnessCLIGrammarCases([]harnessCLIGrammarCommand{
		{Command: "logs", Subcommands: []string{"query", "tail"}, Usage: []string{"scenery logs query --query <logsql> [--limit <n>] [-o json|-o jsonl]"}},
		{Command: "db", Subcommands: []string{"migrate"}, Usage: []string{"scenery db migrate [service] [--status | --adopt-initial] [--app-root <path>] [-o json]"}},
		{Command: "compile", Usage: []string{"scenery compile [--view source|effective|expanded] [--app-root <path>] [-o human|json]"}},
		{Command: "validate", Subcommands: []string{"list"}, Usage: []string{"scenery validate [<profile>] [-o json]"}},
		{Command: "test", Usage: []string{"scenery test [--app-root <path>] [go test flags/packages...]"}},
		{Command: "system", Usage: []string{"scenery system trust [-o json]"}},
	})
	var got []string
	for _, testCase := range cases {
		got = append(got, testCase.kind+": "+strings.Join(testCase.args, " "))
	}
	for _, want := range []string{
		"refuse: logs zz-unknown-subcommand",
		"refuse: logs query --zz-unknown-flag",
		"refuse: logs query --query",
		"refuse: logs query --limit",
		"accept: logs query --query zzvalue -o json",
		"accept: logs query --query zzvalue -o jsonl",
		"refuse: db migrate --app-root",
		"refuse: compile --view zz-invalid",
		"accept: compile -o json",
		"refuse: zz-unknown-command",
		"help: logs query -h",
		"help: logs query --help",
		"help: db migrate --help -o json",
		// A help request never runs its command, so host families are asked too.
		"help: system trust --help",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing case %q in %q", want, got)
		}
	}
	for _, testCase := range got {
		// A bare alternative is no flag value, a free operand is no unknown
		// subcommand, and a line that forwards arguments refuses nothing.
		if strings.Contains(testCase, "--status") || testCase == "refuse: validate zz-unknown-subcommand" || strings.Contains(testCase, ": test") || (strings.Contains(testCase, "system") && !strings.HasPrefix(testCase, "help: ")) {
			t.Errorf("unexpected case %q", testCase)
		}
	}
	if !slices.Equal(skipped, []string{"system"}) {
		t.Fatalf("not executed = %q", skipped)
	}
}
