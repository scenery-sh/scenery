package main

import (
	"flag"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"
)

// helpFlagNames extracts the flag names a help descriptor documents.
func helpFlagNames(entry helpCommandEntry) []string {
	names := map[string]bool{}
	for _, text := range entry.Flags {
		for token := range strings.FieldsSeq(text) {
			if !strings.HasPrefix(token, "-") || len(token) < 2 {
				continue
			}
			name := strings.TrimLeft(token, "-")
			name, _, _ = strings.Cut(name, "=")
			name, _, _ = strings.Cut(name, "[")
			if name != "" {
				names[name] = true
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// parserFlagNames runs each parser with an unknown flag so it registers its
// flags and stops before acting, and returns every registered flag name.
func parserFlagNames(t *testing.T, parsers ...func([]string) error) []string {
	return parserFlagNamesWithProbe(t, nil, parsers...)
}

func parserFlagNamesWithProbe(t *testing.T, prefix []string, parsers ...func([]string) error) []string {
	t.Helper()
	names := map[string]bool{}
	var flagSets []*flag.FlagSet
	observeCLIFlagSet = func(flags *flag.FlagSet) { flagSets = append(flagSets, flags) }
	defer func() { observeCLIFlagSet = nil }()
	for _, parse := range parsers {
		flagSets = nil
		_ = parse(append(append([]string(nil), prefix...), "--scenery-help-drift-probe"))
		for _, flags := range flagSets {
			flags.VisitAll(func(f *flag.Flag) { names[f.Name] = true })
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func ignoreParse[T any](parse func([]string) (T, error)) func([]string) error {
	return func(args []string) error { _, err := parse(args); return err }
}

var inspectOnlyFlags = []string{"all", "for-path", "frontend", "implementation", "include-text", "repo-root", "review-due", "severity", "stats", "tag", "top", "verify-generation"}

func observabilityList(subject string) func([]string) error {
	return func(args []string) error {
		_, err := parseInspectArgsInternal(append([]string{subject}, args...), true)
		return err
	}
}

func TestHelpDescriptorsDocumentEveryParserFlag(t *testing.T) {
	cases := map[string][]func([]string) error{
		"db": {ignoreParse(parseDBServerArgs), ignoreParse(parseDBTargetArgs), ignoreParse(parseDBResetArgs), ignoreParse(parseDBApplyArgs),
			ignoreParse(parseDBSeedArgs), ignoreParse(parseDBMigrationArgs), ignoreParse(parseDBSetupArgs)},
		"snapshot": {ignoreParse(parseSnapshotSaveArgs), ignoreParse(parseSnapshotLoadArgs), ignoreParse(parseSnapshotVerifyArgs)},
		"traces":   {ignoreParse(parseTracesClearArgs), observabilityList("traces")},
		"metrics": {ignoreParse(parseMetricsQueryArgs), observabilityList("metrics"),
			func(args []string) error { _, err := parseMetricsCatalogArgs(args, true); return err }},
		"system":    {ignoreParse(parseEdgeArgs), ignoreParse(parseToolchainArgs), ignoreParse(parseAgentArgs), ignoreParse(parseAgentCleanupArgs)},
		"up":        {ignoreParse(parseDevArgs)},
		"ps":        {ignoreParse(parseStatusArgs)},
		"down":      {ignoreParse(parseDownArgs)},
		"prune":     {ignoreParse(parsePruneArgs)},
		"doctor":    {ignoreParse(parseDoctorArgs)},
		"logs":      {ignoreParse(parseLogsArgs), ignoreParse(parseLogsQueryArgs), ignoreParse(parseLogsTailArgs)},
		"generate":  {func(args []string) error { return runContractGenerate(io.Discard, args) }, func(args []string) error { _, err := parseGenerateArgs(append([]string{"sqlc"}, args...)); return err }},
		"test":      {ignoreParse(parseTestArgs)},
		"validate":  {ignoreParse(parseValidateArgs)},
		"worktree":  {ignoreParse(parseWorktreeArgs)},
		"feature":   {ignoreParse(parseFeatureArgs)},
		"framework": {ignoreParse(parseFrameworkArgs)},
		"storage":   {ignoreParse(parseStorageArgs)},
		"task":      {ignoreParse(parseTaskArgs)},
		"worker":    {ignoreParse(parseWorkerArgs)},
		"inspect":   {func(args []string) error { _, err := parseInspectArgs(append([]string{"docs"}, args...)); return err }},
		"harness":   {ignoreParse(parseHarnessArgs)},
	}
	entries := map[string]helpCommandEntry{}
	for _, entry := range helpCommandDescriptors(helpCommands) {
		entries[entry.Command] = entry
	}
	for command, parsers := range cases {
		entry, ok := entries[command]
		if !ok {
			t.Errorf("help has no %s descriptor", command)
			continue
		}
		documented := helpFlagNames(entry)
		registered := parserFlagNames(t, parsers...)
		for _, name := range registered {
			// Every parser accepts -o; commands without output modes ignore it.
			// generate keeps --lang/--output only to reject them with guidance;
			// inspect keeps the traces/metrics list filters only to reject them
			// with guidance toward those commands, and the traces/metrics lists
			// share its parser and reject its inspect-only flags.
			if name == "o" || command == "generate" && (name == "lang" || name == "output") ||
				command == "inspect" && slices.Contains([]string{"limit", "since", "service", "endpoint", "trace-id", "min-duration-ms", "slowest"}, name) ||
				(command == "traces" || command == "metrics") && slices.Contains(inspectOnlyFlags, name) {
				continue
			}
			if !slices.Contains(documented, name) {
				t.Errorf("%s: parser flag --%s is missing from help flags %q", command, name, entry.Flags)
			}
		}
		for _, name := range documented {
			if !slices.Contains(registered, name) {
				t.Errorf("%s: help documents --%s, which the parser does not accept", command, name)
			}
		}
	}
}
