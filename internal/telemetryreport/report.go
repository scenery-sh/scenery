// Package telemetryreport reconstructs how Scenery has been used on this host
// from what earlier runs left behind: the CLI telemetry file, the event logs of
// detached development supervisors and, only when requested, the transcripts
// of coding agents. It reads files and returns aggregates; it never records,
// sends or rewrites anything.
package telemetryreport

import (
	"slices"
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

// Sources tells what the report read and how completely: a source that could
// not be read, or was read only in part, is counted rather than silently
// missing from the aggregates.
type Sources struct {
	CLIRecords int `json:"cli_records"`
	// CLIInvalid counts CLI telemetry lines that are no record, including
	// lines longer than the reader keeps.
	CLIInvalid     int `json:"cli_invalid_records"`
	SupervisorLogs int `json:"supervisor_logs"`
	// SupervisorLogsPartial counts supervisor logs whose reading stopped at
	// an error, SupervisorLogsFailed those that could not be opened, and
	// SupervisorInvalid their event lines that did not decode or were too
	// long.
	SupervisorLogsPartial int                `json:"supervisor_logs_partial"`
	SupervisorLogsFailed  int                `json:"supervisor_logs_failed"`
	SupervisorInvalid     int                `json:"supervisor_invalid_records"`
	AgentTranscripts      bool               `json:"agent_transcripts"`
	ClaudeSessions        int                `json:"claude_sessions"`
	CodexSessions         int                `json:"codex_sessions"`
	Transcripts           *TranscriptSources `json:"transcripts,omitempty"`
}

// Finding is one conclusion worth acting on, derived from the aggregates.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Timing summarizes durations in milliseconds: every attempt is counted, and
// the nearest-rank percentiles describe the successful ones. A percentile is
// null when no attempt succeeded.
type Timing struct {
	Count        int    `json:"count"`
	FailureCount int    `json:"failure_count"`
	P50MS        *int64 `json:"p50_ms"`
	P95MS        *int64 `json:"p95_ms"`
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
	p := percentiles(a.durations, 50, 95)
	return Timing{Count: a.count, FailureCount: a.failures, P50MS: p[0], P95MS: p[1]}
}

// percentiles returns nearest-rank percentiles of values from one sorted
// copy: for each p, the smallest value with at least p percent of the values
// at or below it, or nil without values.
func percentiles(values []int64, ps ...int) []*int64 {
	result := make([]*int64, len(ps))
	if len(values) == 0 {
		return result
	}
	sorted := append([]int64(nil), values...)
	slices.Sort(sorted)
	for index, p := range ps {
		rank := max((p*len(sorted)+99)/100, 1)
		value := sorted[rank-1]
		result[index] = &value
	}
	return result
}

// ms reads an optional percentile, -1 when it is unavailable.
func ms(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
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
	report.Sources.SupervisorLogsPartial, report.Sources.SupervisorLogsFailed, report.Sources.SupervisorInvalid = builds.partialLogs, builds.failedLogs, builds.invalidRecords
	if opts.AgentTranscripts {
		agents, err := readAgents(opts)
		if err != nil {
			return Report{}, err
		}
		report.Agents = &agents
		report.Sources.AgentTranscripts = true
		report.Sources.ClaudeSessions, report.Sources.CodexSessions = agents.ClaudeSessions, agents.CodexSessions
		report.Sources.Transcripts = &agents.sources
	}
	report.Findings = findings(report)
	return report, nil
}
