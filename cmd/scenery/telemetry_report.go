package main

import (
	"context"
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
	build := telemetryReportBuildOptions(opts, telemetryPath, paths.Home)
	entries, liveErr := inspectWorktreeOwners(context.Background(), "")
	build.LiveStateChecked = liveErr == nil
	if liveErr != nil {
		build.LiveStateUnavailable++
	}
	for _, entry := range entries {
		if entry.Status == "unavailable" || entry.Status == "incompatible-or-invalid" {
			build.LiveStateUnavailable++
		}
		for _, block := range entry.BuildBlocks {
			build.ActiveBlocks = append(build.ActiveBlocks, telemetryreport.BuildBlock{
				AppRoot: entry.AppRoot, Reason: block.Reason, Cause: block.Cause,
				Since: block.Since.UTC().Format(time.RFC3339Nano), PreventedBuilds: block.PreventedBuilds,
			})
		}
	}
	report, err := telemetryreport.Build(build)
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

// telemetryReportCommandFamilies derives public roots and their immediate
// subcommands from the help catalog. Native capture uses this finite vocabulary;
// transcript reports use it to distinguish known commands from unknown attempts.
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
			families[family] = append(subcommands, words[1])
			continue
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
		return []string{name, fmt.Sprint(t.Count), fmt.Sprintf("failed %d", t.FailureCount), fmt.Sprintf("successful n=%d", t.PercentileSampleCount), "p50 " + ms(t.P50MS), "p95 " + ms(t.P95MS)}
	}
	line("Scenery telemetry report, %s to %s", firstNonEmpty(report.Window.Since, "the first record"), report.Window.Until)
	sources := fmt.Sprintf("Sources: %d CLI records, %d supervisor logs", report.Sources.CLIRecords, report.Sources.SupervisorLogs)
	if report.Sources.AgentTranscripts {
		sources += fmt.Sprintf(", %d Claude Code and %d Codex sessions that ran Scenery", report.Sources.ClaudeSessions, report.Sources.CodexSessions)
	} else {
		sources += "; agent transcripts not read (--agent-transcripts)"
	}
	line("%s.", sources)
	for _, source := range report.Sources.CLI {
		line("  CLI %s (%s, archived=%t): retained %d, in window %d, duplicate invocations %d, invalid %d; %s to %s.", source.Path, source.Status, source.Archived, source.Records, source.InWindow, source.Duplicates, source.Invalid, firstNonEmpty(source.First, "unknown"), firstNonEmpty(source.Last, "unknown"))
	}
	line("  Identified overlaps are counted once (active file wins); legacy rows without invocation identity are retained separately.")
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
	if len(builds.StartupPhases) > 0 {
		line("Startup phases (%s)", builds.PhaseBoundary)
		phaseRows := [][]string{}
		for _, phase := range builds.StartupPhases {
			phaseRows = append(phaseRows, timing(phase.Step, phase.Timing))
		}
		table(phaseRows)
	}
	for _, health := range builds.ObservabilityStates {
		line("  observability %s: %d observations", health.Name, health.Count)
	}
	for _, cache := range builds.CacheWork {
		line("  cache %s / %s / %s (%s): %d samples, %.1fms accumulated, %d files hashed, %d reused", cache.Layer, cache.EditClass, cache.Cache, cache.Reason, cache.Samples, cache.AccumulatedMS, cache.FilesHashed, cache.FilesReused)
	}
	line("  Superseded %d; transaction waits %d; deferred candidates %d; blocked rebuilds prevented %d.", builds.Superseded, builds.TransactionWaits, builds.DeferredCandidates, builds.PreventedBuilds)
	for _, stage := range []struct {
		name   string
		causes []telemetryreport.Count
	}{{"initial failure", builds.InitialFailures}, {"rebuild failure", builds.RebuildFailures}, {"unstarted/rejected work", builds.UnstartedErrors}} {
		for _, cause := range stage.causes {
			line("  %s %5d× %s", stage.name, cause.Count, cause.Name)
		}
	}
	if len(builds.Worktrees) > 0 {
		line("Worktrees")
		rows = nil
		for _, worktree := range builds.Worktrees {
			rows = append(rows, timing(worktree.AppRoot+" rebuilds", worktree.Rebuilds), timing("  initial builds", worktree.Initial))
		}
		table(rows)
	}
	if builds.FirstResponse.Count > 0 {
		line("  Passive captured-change to first attested headers: n=%d; includes waiting for traffic and accepted 2xx–4xx responses.", builds.FirstResponse.PercentileSampleCount)
		if builds.FirstResponse.Count == 1 {
			line("  Single observed interval: %s; successful business-request latency requires a controlled request.", ms(builds.FirstResponse.P50MS))
		} else {
			table([][]string{timing("change to first headers", builds.FirstResponse)})
		}
	}
	if len(builds.HeaderObservations) > 0 {
		line("  Attested header events: %d observed, %d retained; initial events are excluded from the rebuild interval aggregate.", builds.HeaderObservationCount, len(builds.HeaderObservations))
		for _, event := range builds.HeaderObservations {
			line("    %s generation %d initial=%t HTTP %d: %dms from captured source; %s", event.At.UTC().Format(time.RFC3339Nano), event.Generation, event.Initial, event.Status, event.DurationMS, event.AppRoot)
		}
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
	line("CLI apps and purposes")
	rows = nil
	for _, app := range cli.Apps {
		rows = append(rows, timing(app.ID+" ("+app.Name+")", app.Timing))
	}
	table(rows)
	for _, purpose := range cli.Purposes {
		line("  purpose %s: %d", purpose.Name, purpose.Count)
	}
	line("CLI cohorts (app / purpose / producer / dirty / command / measurement)")
	rows = nil
	for _, cohort := range cli.Cohorts {
		name := fmt.Sprintf("%s / %s / %s / %s / %s / %s", firstNonEmpty(cohort.AppID, "unattributed"), cohort.Purpose, cohort.Producer, cohort.Dirty, cohort.Command, cohort.Measurement)
		rows = append(rows, timing(name, cohort.Timing))
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
