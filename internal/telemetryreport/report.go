// Package telemetryreport reconstructs how Scenery has been used on this host
// from what earlier runs left behind: the CLI telemetry file, the event logs of
// detached development supervisors and, only when requested, the transcripts
// of coding agents. It reads files and returns aggregates; it never records,
// sends or rewrites anything.
package telemetryreport

import (
	"sort"
	"time"
)

// Options selects the sources and window of a report. Empty paths skip their
// source.
type Options struct {
	Since time.Time
	Until time.Time
	// CLITelemetryPath is the CLI telemetry file (~/.scenery/telemetry.jsonl).
	CLITelemetryPath string
	// AgentHome is the Scenery agent home whose supervisor logs are read.
	AgentHome string
	// ClaudeProjectsDir and CodexSessionsDir are read only when
	// AgentTranscripts is set.
	AgentTranscripts  bool
	ClaudeProjectsDir string
	CodexSessionsDir  string
	// CommandFamilies maps each Scenery command to its subcommands; a word
	// after "scenery" in an agent's shell command counts as an invocation only
	// when it names one of these commands.
	CommandFamilies map[string][]string
}

// Report is the aggregate result. Every list is ordered most significant first.
type Report struct {
	Window   Window    `json:"window"`
	Sources  Sources   `json:"sources"`
	Findings []Finding `json:"findings"`
	CLI      CLIReport `json:"cli"`
	Builds   Builds    `json:"builds"`
	Agents   *Agents   `json:"agents,omitempty"`
}

type Window struct {
	Since string `json:"since,omitempty"`
	Until string `json:"until"`
}

type Sources struct {
	CLIRecords       int  `json:"cli_records"`
	CLIInvalid       int  `json:"cli_invalid_records"`
	SupervisorLogs   int  `json:"supervisor_logs"`
	AgentTranscripts bool `json:"agent_transcripts"`
	ClaudeSessions   int  `json:"claude_sessions"`
	CodexSessions    int  `json:"codex_sessions"`
}

// Finding is one conclusion worth acting on, derived from the aggregates.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Timing summarizes durations in milliseconds: every attempt is counted, and
// the nearest-rank percentiles describe the successful ones.
type Timing struct {
	Count        int   `json:"count"`
	FailureCount int   `json:"failure_count"`
	P50MS        int64 `json:"p50_ms"`
	P95MS        int64 `json:"p95_ms"`
}

type timingAccumulator struct {
	count, failures int
	durations       []int64
}

func (a *timingAccumulator) add(durationMS int64, ok bool) {
	a.count++
	if !ok {
		a.failures++
		return
	}
	a.durations = append(a.durations, durationMS)
}

func (a *timingAccumulator) timing() Timing {
	return Timing{Count: a.count, FailureCount: a.failures, P50MS: percentile(a.durations, 0.50), P95MS: percentile(a.durations, 0.95)}
}

func percentile(values []int64, q float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(q * float64(len(sorted)))
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

// Count names a value and how often it occurred.
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func sortedCounts(counts map[string]int, limit int) []Count {
	result := make([]Count, 0, len(counts))
	for name, count := range counts {
		result = append(result, Count{Name: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (o Options) inWindow(at time.Time) bool {
	if at.IsZero() {
		return false
	}
	return (o.Since.IsZero() || !at.Before(o.Since)) && (o.Until.IsZero() || !at.After(o.Until))
}

// Build reads the selected sources and derives the report.
func Build(opts Options) (Report, error) {
	if opts.Until.IsZero() {
		opts.Until = time.Now().UTC()
	}
	report := Report{Window: Window{Until: opts.Until.UTC().Format(time.RFC3339)}, Findings: []Finding{}}
	if !opts.Since.IsZero() {
		report.Window.Since = opts.Since.UTC().Format(time.RFC3339)
	}
	cli, err := readCLI(opts)
	if err != nil {
		return Report{}, err
	}
	report.CLI = cli
	report.Sources.CLIRecords, report.Sources.CLIInvalid = cli.Records, cli.invalid
	builds, err := readBuilds(opts)
	if err != nil {
		return Report{}, err
	}
	report.Builds = builds
	report.Sources.SupervisorLogs = builds.logs
	if opts.AgentTranscripts {
		agents, err := readAgents(opts)
		if err != nil {
			return Report{}, err
		}
		report.Agents = &agents
		report.Sources.AgentTranscripts = true
		report.Sources.ClaudeSessions, report.Sources.CodexSessions = agents.ClaudeSessions, agents.CodexSessions
	}
	report.Findings = findings(report)
	return report, nil
}
