package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"scenery.sh/internal/telemetryreport"
)

const (
	telemetryReportPayloadKind = "scenery.telemetry.report"
	defaultTelemetryReportDays = 30
)

type telemetryReportOptions struct {
	Since            time.Time
	AgentTranscripts bool
	JSON             bool
}

type telemetryReportResponse struct {
	cliPayloadIdentity
	telemetryreport.Report
}

func runTelemetryReportCommand(stdout io.Writer, args []string) error {
	opts, err := parseTelemetryReportArgs(args, time.Now().UTC())
	if err != nil {
		return err
	}
	telemetryPath, err := cliTelemetryPath()
	if err != nil {
		return err
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	report, err := telemetryreport.Build(telemetryReportBuildOptions(opts, telemetryPath, paths.Home))
	if err != nil {
		return err
	}
	response := telemetryReportResponse{cliPayloadIdentity: newCLIPayloadIdentity(telemetryReportPayloadKind), Report: report}
	if opts.JSON {
		return writeCLIJSON(stdout, response)
	}
	return writeTelemetryReportHuman(stdout, report)
}

func telemetryReportBuildOptions(opts telemetryReportOptions, telemetryPath, agentHome string) telemetryreport.Options {
	build := telemetryreport.Options{
		Since: opts.Since, CLITelemetryPath: telemetryPath, AgentHome: agentHome,
		AgentTranscripts: opts.AgentTranscripts, CommandFamilies: telemetryReportCommandFamilies(),
	}
	if opts.AgentTranscripts {
		if home, err := os.UserHomeDir(); err == nil {
			build.ClaudeProjectsDir = filepath.Join(home, ".claude", "projects")
			build.CodexSessionsDir = filepath.Join(home, ".codex", "sessions")
		}
	}
	return build
}

// telemetryReportCommandFamilies names every command `scenery help` advertises
// with its subcommands, so an agent's shell command counts as a Scenery
// invocation only when it names a real command.
func telemetryReportCommandFamilies() map[string][]string {
	// help and version answer without a command entry of their own.
	families := map[string][]string{"help": nil, "version": nil}
	for _, command := range helpCommands {
		words := strings.Fields(command.Command)
		if len(words) == 0 {
			continue
		}
		family := words[0]
		subcommands := families[family]
		if len(words) > 1 {
			subcommands = append(subcommands, words[1])
		}
		families[family] = append(subcommands, command.Subcommands...)
	}
	return families
}

func parseTelemetryReportArgs(args []string, now time.Time) (telemetryReportOptions, error) {
	var opts telemetryReportOptions
	flags := newCLIFlagSet("telemetry report")
	registerJSONOutput(flags, &opts.JSON)
	since := fmt.Sprintf("%dh", defaultTelemetryReportDays*24)
	flags.StringVar(&since, "since", since, "")
	flags.BoolVar(&opts.AgentTranscripts, "agent-transcripts", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return telemetryReportOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return telemetryReportOptions{}, err
	}
	window, err := parsePositiveDuration(since, "since")
	if err != nil {
		return telemetryReportOptions{}, err
	}
	opts.Since = now.Add(-window)
	return opts, nil
}

func writeTelemetryReportHuman(stdout io.Writer, report telemetryreport.Report) error {
	out := &strings.Builder{}
	line := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format+"\n", args...) }
	table := func(rows [][]string) {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, row := range rows {
			_, _ = fmt.Fprintln(w, "  "+strings.Join(row, "\t"))
		}
		_ = w.Flush()
	}
	ms := func(value *int64) string {
		switch {
		case value == nil:
			return "-"
		case *value >= 10_000:
			return fmt.Sprintf("%.1fs", float64(*value)/1000)
		case *value >= 1000:
			return fmt.Sprintf("%.2fs", float64(*value)/1000)
		}
		return fmt.Sprintf("%dms", *value)
	}
	timing := func(name string, t telemetryreport.Timing) []string {
		return []string{name, fmt.Sprint(t.Count), fmt.Sprintf("failed %d", t.FailureCount), "p50 " + ms(t.P50MS), "p95 " + ms(t.P95MS)}
	}
	line("Scenery telemetry report, %s to %s", firstNonEmpty(report.Window.Since, "the first record"), report.Window.Until)
	sources := fmt.Sprintf("Sources: %d CLI records, %d supervisor logs", report.Sources.CLIRecords, report.Sources.SupervisorLogs)
	if report.Sources.AgentTranscripts {
		sources += fmt.Sprintf(", %d Claude Code and %d Codex sessions that ran Scenery", report.Sources.ClaudeSessions, report.Sources.CodexSessions)
	} else {
		sources += "; agent transcripts not read (--agent-transcripts)"
	}
	line("%s.", sources)
	if incomplete := report.Sources.SupervisorLogsFailed + report.Sources.SupervisorLogsPartial; incomplete > 0 {
		line("  %d supervisor logs unreadable or read in part; %d invalid event records skipped.", incomplete, report.Sources.SupervisorInvalid)
	}
	if transcripts := report.Sources.Transcripts; transcripts != nil {
		line("  Transcripts: %d read, %d read in part, %d unreadable; %d invalid and %d oversized records skipped, %d results without a call, %d calls without a result.",
			transcripts.Read, transcripts.Partial, transcripts.Failed, transcripts.InvalidRecords, transcripts.OversizedRecords, transcripts.UnmatchedResults, transcripts.UnansweredCalls)
	}
	line("")
	line("Findings")
	if len(report.Findings) == 0 {
		line("  none")
	}
	for _, finding := range report.Findings {
		line("  [%s] %s", finding.Severity, finding.Message)
	}
	builds := report.Builds
	line("")
	line("Builds of detached scenery up sessions (%d sessions)", builds.Sessions)
	rows := [][]string{timing("rebuilds", builds.Rebuilds), timing("initial builds", builds.Initial)}
	for _, step := range builds.Steps {
		if step.Count*10 >= builds.Rebuilds.Count-builds.Rebuilds.FailureCount && len(rows) < 12 {
			rows = append(rows, timing("  step "+step.Step, step.Timing))
		}
	}
	table(rows)
	for index, cause := range builds.Failures {
		if index == 5 {
			break
		}
		line("  %5d× %s", cause.Count, cause.Name)
	}
	cli := report.CLI
	line("")
	line("CLI commands (%d records, %d failed)", cli.Records, cli.Failures)
	rows = [][]string{timing("scenery up startup", cli.Startup)}
	for index, command := range cli.Commands {
		if index == 12 {
			break
		}
		rows = append(rows, timing(command.Command, command.Timing))
	}
	table(rows)
	if agents := report.Agents; agents != nil {
		line("")
		line("Agents: %d tool calls, %d errors; %d shell commands ran Scenery (%d invocations): %d recorded Scenery's own outcome (%d failed); as shell commands %d failed and %d recorded no outcome",
			agents.ToolCalls, agents.ToolErrors, agents.SceneryCommands, agents.SceneryInvocations, agents.SceneryAttributable, agents.SceneryAttributableFailed, agents.SceneryFailed, agents.SceneryOutcomeUnknown)
		rows = nil
		for index, command := range agents.Commands {
			if index == 10 {
				break
			}
			wall := command.WallTimeMS
			rows = append(rows, []string{command.Command, fmt.Sprint(command.Count), fmt.Sprintf("attributable %d", command.Attributable), fmt.Sprintf("failed %d", command.FailureCount), "waited " + ms(&wall), "p50 " + ms(command.P50MS)})
		}
		table(rows)
		for _, class := range agents.FailureClasses {
			line("  %5d× failed: %s", class.Count, class.Name)
		}
		for index, input := range agents.RejectedInputs {
			if index == 5 {
				break
			}
			line("  %5d× rejected %s", input.Count, input.Name)
		}
	}
	_, err := io.WriteString(stdout, out.String())
	return err
}
