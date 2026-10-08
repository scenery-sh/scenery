package telemetryreport

import (
	"fmt"
	"sort"
	"strings"
)

const (
	severityCritical = "critical"
	severityWarning  = "warning"
	severityInfo     = "info"
)

// findings turns the aggregates into ordered conclusions: critical first,
// then warnings, then gaps in what the sources can tell.
func findings(report Report) []Finding {
	var result []Finding
	add := func(severity, code, format string, args ...any) {
		result = append(result, Finding{Severity: severity, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	for _, block := range report.Builds.ActiveBlocks {
		age := block.DurationMS
		add(severityCritical, "builds.active_block", "Builds in %s are BLOCKED (%s) since %s (%s, %d rebuilds prevented): %s. The last good generation is still serving; newer source is not applied.", block.AppRoot, block.Reason, block.Since, duration(&age), block.PreventedBuilds, block.Cause)
	}
	if report.Builds.PreventedBuilds > 0 {
		add(severityInfo, "builds.prevented", "Observed blocks prevented %d rebuilds that would repeat the same failure; these are not failed build attempts.", report.Builds.PreventedBuilds)
	}
	if report.Sources.LiveStateUnavailable > 0 {
		add(severityWarning, "sources.live_state_unavailable", "%d worktree owners could not be inspected; their current blocks are unknown.", report.Sources.LiveStateUnavailable)
	}
	for _, burst := range report.CLI.Bursts {
		add(severityCritical, "cli.failure_burst", "scenery %s failed %d times with exit %d between %s and %s, at most %d an hour; this is consistent with an automated retry loop, but the caller and cause are not established.",
			burst.Command, burst.Count, burst.ExitCode, burst.First, burst.Last, burst.PeakPerHour)
	}
	for index, streak := range report.Builds.Streaks {
		if index == 3 {
			break
		}
		severity := severityWarning
		if streak.Count >= 20 {
			severity = severityCritical
		}
		add(severity, "builds.failure_streak", "%d consecutive builds failed in %s between %s and %s (%s); edits kept arriving while the runtime accepted none.",
			streak.Count, streak.AppRoot, streak.First, streak.Last, emptyAs(streak.Cause, "cause not recorded"))
	}
	if rebuilds := report.Builds.Rebuilds; rebuilds.Count >= 20 {
		rate := percent(rebuilds.FailureCount, rebuilds.Count)
		switch {
		case rate >= 30:
			add(severityCritical, "builds.failure_rate", "%d%% of rebuilds failed (%d of %d); leading cause: %s.", rate, rebuilds.FailureCount, rebuilds.Count, leadingCause(report.Builds.RebuildFailures))
		case rate >= 10:
			add(severityWarning, "builds.failure_rate", "%d%% of rebuilds failed (%d of %d); leading cause: %s.", rate, rebuilds.FailureCount, rebuilds.Count, leadingCause(report.Builds.RebuildFailures))
		}
	}
	if startup := report.CLI.Startup; startup.Count >= 20 && percent(startup.FailureCount, startup.Count) >= 5 {
		add(severityWarning, "cli.startup_failures", "%d%% of scenery up startups failed (%d of %d); successful startups took p50 %s, p95 %s.",
			percent(startup.FailureCount, startup.Count), startup.FailureCount, startup.Count, duration(startup.P50MS), duration(startup.P95MS))
	}
	var failing []string
	for _, command := range report.CLI.Commands {
		if command.Count >= 20 && percent(command.FailureCount, command.Count) >= 25 {
			failing = append(failing, fmt.Sprintf("%s %d%% of %d", command.Command, percent(command.FailureCount, command.Count), command.Count))
		}
	}
	if len(failing) > 0 {
		add(severityWarning, "cli.failing_commands", "Commands failing at least a quarter of the time: %s. Native diagnostic codes are grouped when recorded; older records carry exit codes only.", strings.Join(failing, "; "))
	}
	if agents := report.Agents; agents != nil && agents.SceneryCommands > 0 {
		invalid := 0
		for _, class := range agents.FailureClasses {
			if class.Name == "invalid invocation" {
				invalid = class.Count
			}
		}
		if invalid > 0 {
			var inputs []string
			for index, input := range agents.RejectedInputs {
				if index == 5 {
					break
				}
				inputs = append(inputs, fmt.Sprintf("%s ×%d", input.Name, input.Count))
			}
			add(severityWarning, "agents.invalid_invocations", "Agents ran %d shell commands Scenery refused as malformed (of %d that ran Scenery); most rejected: %s.", invalid, agents.SceneryCommands, emptyAs(strings.Join(inputs, ", "), "not recorded"))
		}
		// Only an attributable shell command's outcome is Scenery's own.
		if agents.SceneryAttributable >= 20 && percent(agents.SceneryAttributableFailed, agents.SceneryAttributable) >= 10 {
			add(severityWarning, "agents.scenery_failures", "%d%% of agent Scenery commands whose outcome was their own failed (%d of %d).",
				percent(agents.SceneryAttributableFailed, agents.SceneryAttributable), agents.SceneryAttributableFailed, agents.SceneryAttributable)
		}
		if agents.SceneryAttributable*2 < agents.SceneryCommands {
			add(severityInfo, "agents.unattributable", "Only %d of %d agent shell commands that ran Scenery recorded Scenery's own outcome; the others piped or chained it with other commands, whose status the shell reported instead, or recorded no outcome (%d), so transcripts cannot tell their Scenery failures or time.",
				agents.SceneryAttributable, agents.SceneryCommands, agents.SceneryOutcomeUnknown)
		}
	}
	sources := report.Sources
	for _, file := range sources.CLI {
		if file.Status == "partial" || file.Status == "unreadable" {
			add(severityWarning, "sources.cli_incomplete", "CLI source %s is %s; retained aggregates include only its readable records.", file.Path, file.Status)
		}
	}
	for _, purpose := range report.CLI.Purposes {
		if purpose.Name == "unknown" && purpose.Count > 0 {
			add(severityInfo, "cli.purpose_unknown", "%d CLI records have unknown execution purpose. App/build identity cannot classify them as verification; use the independent app, purpose and producer cohorts.", purpose.Count)
		}
	}
	incomplete := sources.SupervisorLogsFailed + sources.SupervisorLogsPartial
	skipped := sources.SupervisorInvalid
	if transcripts := sources.Transcripts; transcripts != nil {
		incomplete += transcripts.Failed + transcripts.Partial
		skipped += transcripts.InvalidRecords + transcripts.OversizedRecords
	}
	if incomplete > 0 {
		add(severityWarning, "sources.incomplete", "%d source files could not be read or were read only in part (see sources); failures they hold are missing from this report.", incomplete)
	}
	if skipped > 0 {
		add(severityInfo, "sources.skipped_records", "%d records of the read sources contained invalid or oversized evidence; that evidence was excluded, while known supervisor outcomes were retained.", skipped)
	}
	if report.Builds.UnmatchedErrors > 0 {
		add(severityInfo, "builds.unmatched_errors", "%d build errors name an operation no build request in the window has; they are charged to no build.", report.Builds.UnmatchedErrors)
	}
	if rebuilds := report.Builds.Rebuilds; rebuilds.PercentileSampleCount > 0 {
		add(severityInfo, "builds.rebuild_latency", "Successful rebuilds took p50 %s, p95 %s from build start to published generation over %d timed successful rebuilds.",
			duration(rebuilds.P50MS), duration(rebuilds.P95MS), rebuilds.PercentileSampleCount)
	}
	if response := report.Builds.FirstResponse; response.Count > 0 {
		add(severityInfo, "builds.first_response_latency", "Captured change to first attested response headers: p50 %s, p95 %s over %d generations. Includes waiting for traffic; filesystem detection delay before capture is excluded.", duration(response.P50MS), duration(response.P95MS), response.Count)
	} else if report.Builds.Sessions > 0 {
		add(severityInfo, "builds.first_response_unavailable", "No captured-change to first-response measurements in retained history; older producers and generations without qualifying traffic provide no sample.")
	}
	if report.Builds.Superseded > 0 {
		add(severityInfo, "builds.superseded", "%d build candidates were superseded by newer source; these are excluded from failed attempts and latency percentiles.", report.Builds.Superseded)
	}
	if report.Sources.SupervisorRotated > 0 {
		add(severityInfo, "sources.retained_history", "%d supervisor sessions have rotated segments. This report covers retained history only; older events may have expired.", report.Sources.SupervisorRotated)
	}
	if report.Builds.Sessions == 0 {
		add(severityInfo, "builds.no_history", "No build history in the window: only scenery up --detach keeps its supervisor events.")
	}
	if cli := report.CLI; cli.Records > 0 {
		if share := percent(cli.Unattributed, cli.Records); share >= 25 {
			add(severityInfo, "cli.unattributed", "%d%% of CLI records name no app, so they cannot be tied to a worktree.", share)
		}
		if share := percent(cli.Unversioned, cli.Records); share >= 50 {
			add(severityInfo, "cli.unversioned", "%d%% of CLI records carry version \"dev\" and cannot be tied to a producer commit.", share)
		}
	}
	rank := map[string]int{severityCritical: 0, severityWarning: 1, severityInfo: 2}
	sort.SliceStable(result, func(i, j int) bool { return rank[result[i].Severity] < rank[result[j].Severity] })
	if result == nil {
		result = []Finding{}
	}
	return result
}

func percent(part, whole int) int {
	if whole == 0 {
		return 0
	}
	return part * 100 / whole
}

func duration(value *int64) string {
	switch {
	case value == nil:
		return "unavailable"
	case *value >= 10_000:
		return fmt.Sprintf("%.1f s", float64(*value)/1000)
	case *value >= 1000:
		return fmt.Sprintf("%.2f s", float64(*value)/1000)
	default:
		return fmt.Sprintf("%d ms", *value)
	}
}

func leadingCause(causes []Count) string {
	if len(causes) == 0 {
		return "not recorded"
	}
	return fmt.Sprintf("%s (%d)", causes[0].Name, causes[0].Count)
}

func emptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
