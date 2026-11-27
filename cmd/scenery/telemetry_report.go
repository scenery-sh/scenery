package main

import (
	"context"
	"fmt"
	"io"
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
	Apps  []string
	Since time.Time
	JSON  bool
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
		// Only count live owners that could not be inspected; stopped or legacy
		// incompatible worktree metadata without a live lock holds no active blocks.
		if entry.Status == "unavailable" {
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
	return telemetryreport.Options{
		Apps:             append([]string(nil), opts.Apps...),
		Since:            opts.Since,
		CLITelemetryPath: telemetryPath,
		AgentHome:        agentHome,
	}
}

func parseTelemetryReportArgs(args []string, now time.Time) (telemetryReportOptions, error) {
	var opts telemetryReportOptions
	flags := newCLIFlagSet("telemetry report")
	registerJSONOutput(flags, &opts.JSON)
	since := fmt.Sprintf("%dh", defaultTelemetryReportDays*24)
	flags.StringVar(&since, "since", since, "")
	flags.Func("app", "", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return usageErrorf("--app must not be empty")
		}
		opts.Apps = append(opts.Apps, value)
		return nil
	})
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
	opts.Apps = sortedUniqueStrings(opts.Apps)
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
		return []string{name, fmt.Sprint(t.Count), fmt.Sprintf("failed %d", t.FailureCount), fmt.Sprintf("timed successes n=%d", t.PercentileSampleCount), "p50 " + ms(t.P50MS), "p95 " + ms(t.P95MS)}
	}
	extent := func(value *int64) string {
		if value == nil {
			return "unknown"
		}
		return fmt.Sprint(*value)
	}
	line("Scenery telemetry report, %s to %s", firstNonEmpty(report.Window.Since, "the first record"), report.Window.Until)
	line("Sources: %d CLI records, %d supervisor logs.", report.Sources.CLIRecords, report.Sources.SupervisorLogs)
	for _, source := range report.Sources.CLI {
		line("  CLI %s (%s, archived=%t): read %d / captured %s bytes; retained %d, in window %d, duplicate invocations %d, invalid %d; %s to %s.", source.Path, source.Status, source.Archived, source.ReadBytes, extent(source.SnapshotBytes), source.Records, source.InWindow, source.Duplicates, source.Invalid, firstNonEmpty(source.First, "unknown"), firstNonEmpty(source.Last, "unknown"))
	}
	for _, source := range report.Sources.Supervisor {
		line("  Supervisor %s (%s): read %d / captured %s bytes across %d segments; retained %d, in window %d, invalid %d.", source.Path, source.Status, source.ReadBytes, extent(source.SnapshotBytes), source.Segments, source.Records, source.InWindow, source.Invalid)
	}
	line("  Identified overlaps are counted once (active file wins); legacy rows without invocation identity are retained separately.")
	if incomplete := report.Sources.SupervisorLogsFailed + report.Sources.SupervisorLogsPartial; incomplete > 0 || report.Sources.SupervisorInvalid > 0 {
		line("  %d supervisor logs unreadable or read in part; %d event records with invalid evidence (known outcomes retained).", incomplete, report.Sources.SupervisorInvalid)
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
		accumulated := "unavailable"
		if cache.AccumulatedMS != nil {
			accumulated = fmt.Sprintf("%.1fms", *cache.AccumulatedMS)
		}
		line("  cache %s / %s / %s (%s): %d records, timed n=%d, %s accumulated, %d files hashed, %d reused", cache.Layer, cache.EditClass, cache.Cache, cache.Reason, cache.Samples, cache.TimingSampleCount, accumulated, cache.FilesHashed, cache.FilesReused)
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
	if err := writeDevTelemetryEvents(out, cli.Events); err != nil {
		return err
	}
	line("CLI commands (%d records, %d failed, %d uptime)", cli.Records, cli.Failures, cli.UptimeRecords)
	for _, category := range cli.ErrorCategories {
		line("  %s: %d", category.Name, category.Count)
	}
	for _, diagnostic := range cli.Diagnostics {
		line("  %s: %d", diagnostic.Name, diagnostic.Count)
	}
	for _, rejected := range cli.RejectedTokens {
		line("  rejected %s: %d", rejected.Name, rejected.Count)
	}
	for _, fault := range cli.ProxyFaults {
		line("  proxy fault %s: %d", fault.Name, fault.Count)
	}
	for _, fault := range cli.RuntimeFaults {
		line("  runtime fault %s: %d", fault.Name, fault.Count)
	}
	for _, fault := range cli.WorkerFaults {
		line("  worker fault %s: %d", fault.Name, fault.Count)
	}
	if cli.NoOpRebuilds > 0 {
		line("  no-op rebuild events (0 modules rebuilt): %d", cli.NoOpRebuilds)
	}
	rows = [][]string{timing("scenery up startup", cli.Startup)}
	for index, command := range cli.Commands {
		if index == 12 {
			break
		}
		rows = append(rows, timing(command.Command, command.Timing))
	}
	table(rows)
	if len(cli.Daily) > 0 {
		line("Daily CLI trend (UTC)")
		rows = nil
		for _, day := range cli.Daily {
			rows = append(rows, []string{
				day.Date,
				fmt.Sprint(day.Count),
				fmt.Sprintf("failed %d", day.FailureCount),
				fmt.Sprintf("up %d (failed %d, p50 %s)", day.StartupCount, day.StartupFailureCount, ms(day.StartupP50MS)),
			})
		}
		table(rows)
	}
	if len(cli.ProducerTrends) > 0 {
		line("Producer trends")
		rows = nil
		for index, trend := range cli.ProducerTrends {
			if index == 10 {
				break
			}
			rows = append(rows, []string{
				trend.Producer,
				fmt.Sprint(trend.Count),
				fmt.Sprintf("failed %d", trend.FailureCount),
				fmt.Sprintf("up %d (failed %d, p50 %s)", trend.StartupCount, trend.StartupFailureCount, ms(trend.StartupP50MS)),
			})
		}
		table(rows)
	}
	line("CLI apps, callers and purposes")
	rows = nil
	for _, app := range cli.Apps {
		rows = append(rows, timing(app.ID+" ("+app.Name+")", app.Timing))
	}
	table(rows)
	for _, caller := range cli.Callers {
		line("  caller %s: %d", caller.Name, caller.Count)
	}
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
	_, err := io.WriteString(stdout, out.String())
	return err
}
