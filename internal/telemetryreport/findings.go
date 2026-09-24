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
	for _, burst := range report.CLI.Bursts {
		add(severityCritical, "cli.failure_burst", "scenery %s failed %d times with exit %d between %s and %s, at most %d an hour; a supervised or scripted caller is retrying without a person noticing.",
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
			add(severityCritical, "builds.failure_rate", "%d%% of rebuilds failed (%d of %d); leading cause: %s.", rate, rebuilds.FailureCount, rebuilds.Count, leadingCause(report.Builds.Failures))
		case rate >= 10:
			add(severityWarning, "builds.failure_rate", "%d%% of rebuilds failed (%d of %d); leading cause: %s.", rate, rebuilds.FailureCount, rebuilds.Count, leadingCause(report.Builds.Failures))
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
		add(severityWarning, "cli.failing_commands", "Commands failing at least a quarter of the time: %s. Records carry exit codes only, so misuse and real failures cannot be told apart.", strings.Join(failing, "; "))
	}
	if agents := report.Agents; agents != nil && agents.SceneryCalls > 0 {
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
			add(severityWarning, "agents.invalid_invocations", "Agents wrote %d Scenery calls Scenery refused as malformed (of %d calls); most rejected: %s.", invalid, agents.SceneryCalls, emptyAs(strings.Join(inputs, ", "), "not recorded"))
		}
		if percent(agents.SceneryFailed, agents.SceneryCalls) >= 10 {
			add(severityWarning, "agents.scenery_failures", "%d%% of agent Scenery calls failed (%d of %d).", percent(agents.SceneryFailed, agents.SceneryCalls), agents.SceneryFailed, agents.SceneryCalls)
		}
	}
	if rebuilds := report.Builds.Rebuilds; rebuilds.Count > rebuilds.FailureCount {
		add(severityInfo, "builds.rebuild_latency", "Successful rebuilds took p50 %s, p95 %s from build start to published generation over %d rebuilds; the time from saving a file to the first answer of the new generation is not recorded.",
			duration(rebuilds.P50MS), duration(rebuilds.P95MS), rebuilds.Count-rebuilds.FailureCount)
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

func duration(ms int64) string {
	switch {
	case ms >= 10_000:
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	case ms >= 1000:
		return fmt.Sprintf("%.2f s", float64(ms)/1000)
	default:
		return fmt.Sprintf("%d ms", ms)
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
