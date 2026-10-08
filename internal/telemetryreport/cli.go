package telemetryreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"scenery.sh/internal/machine"
)

// cliLineLimit bounds one CLI telemetry line; longer lines are no record.
const cliLineLimit = 1 << 20

// burstHourlyFailures is the number of failures of one command and exit code
// within one hour that marks the hour as part of a failure burst: one a
// minute, which no person or agent produces by retrying by hand.
const burstHourlyFailures = 60

// CLIReport aggregates the CLI telemetry records in the window. Records,
// Failures, Unattributed, Unversioned and Apps count every record; Commands
// count command completions and Startup the scenery up startup measurements.
type CLIReport struct {
	Purposes              []Count     `json:"purposes"`
	Cohorts               []CLICohort `json:"cohorts"`
	Producers             []Count     `json:"producers"`
	Diagnostics           []Count     `json:"diagnostics"`
	IdentifiedInvocations int         `json:"identified_invocations"`
	Records               int         `json:"records"`
	Failures              int         `json:"failures"`
	Unattributed          int         `json:"unattributed"`
	// Unversioned counts records whose producer reported no release version
	// ("dev") and no producer commit.
	Unversioned int             `json:"unversioned"`
	Startup     Timing          `json:"startup"`
	Commands    []CommandTiming `json:"commands"`
	Apps        []AppTiming     `json:"apps"`
	Bursts      []FailureBurst  `json:"failure_bursts"`
	invalid     int
	files       []CLIFileCoverage
}

// CLICohort keeps app, execution purpose and producer independent. Unknown
// purpose/dirty state in historical records is never inferred from a dev build.
type CLICohort struct {
	AppID       string `json:"app_id"`
	AppName     string `json:"app_name"`
	Purpose     string `json:"purpose"`
	Producer    string `json:"producer"`
	Dirty       string `json:"dirty"`
	Command     string `json:"command"`
	Measurement string `json:"measurement"`
	Timing
}

type CLIFileCoverage struct {
	Path          string `json:"path"`
	SnapshotBytes *int64 `json:"snapshot_bytes"`
	ReadBytes     int64  `json:"read_bytes"`
	Archived      bool   `json:"archived"`
	Status        string `json:"status"`
	Records       int    `json:"records"`
	InWindow      int    `json:"in_window"`
	Duplicates    int    `json:"duplicates"`
	Invalid       int    `json:"invalid"`
	First         string `json:"first,omitempty"`
	Last          string `json:"last,omitempty"`
}

type CommandTiming struct {
	Command string `json:"command"`
	Timing
}

type AppTiming struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Timing
}

// FailureBurst is a run of consecutive hours in which one command failed with
// one exit code at least once a minute.
type FailureBurst struct {
	Command         string `json:"command"`
	ExitCode        int    `json:"exit_code"`
	Count           int    `json:"count"`
	First           string `json:"first"`
	Last            string `json:"last"`
	PeakPerHour     int    `json:"peak_per_hour"`
	firstAt, lastAt time.Time
}

type cliRecord struct {
	InvocationID   string            `json:"invocation_id"`
	Producer       *machine.Producer `json:"producer"`
	DiagnosticCode string            `json:"diagnostic_code"`
	Purpose        string            `json:"purpose"`
	Dirty          *bool             `json:"dirty"`
	At             time.Time         `json:"at"`
	Command        string            `json:"command"`
	DurationMS     int64             `json:"duration_ms"`
	ExitCode       int               `json:"exit_code"`
	Version        string            `json:"version"`
	Measurement    string            `json:"measurement"`
	App            *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"app"`
}

func readCLI(opts Options) (CLIReport, error) {
	report := CLIReport{Purposes: []Count{}, Cohorts: []CLICohort{}, Producers: []Count{}, Diagnostics: []Count{}, Commands: []CommandTiming{}, Apps: []AppTiming{}, Bursts: []FailureBurst{}, files: []CLIFileCoverage{}}
	if opts.CLITelemetryPath == "" {
		return report, nil
	}
	archives, err := filepath.Glob(filepath.Join(filepath.Dir(opts.CLITelemetryPath), "telemetry-archive", "*", "telemetry.jsonl"))
	if err != nil {
		return CLIReport{}, fmt.Errorf("list CLI telemetry archives: %w", err)
	}
	// Active evidence wins an identified overlap. Legacy lines lack a reliable
	// invocation key and remain separate records, even when their bytes match.
	paths := append([]string{opts.CLITelemetryPath}, archives...)
	identified := map[string]bool{}
	cohorts := map[CLICohort]*timingAccumulator{}
	purposes := map[string]int{}
	producers, diagnostics := map[string]int{}, map[string]int{}
	commands := map[string]*timingAccumulator{}
	apps := map[string]*timingAccumulator{}
	appNames := map[string]string{}
	startup := &timingAccumulator{}
	type hourKey struct {
		command, hour string
		exit          int
	}
	// Each failing hour keeps its count and first and last failure only, so
	// memory grows with the hours, not with the failures of a restart storm.
	type hourStats struct {
		count       int
		first, last time.Time
	}
	hours := map[hourKey]*hourStats{}
	for _, path := range paths {
		coverage := CLIFileCoverage{Path: filepath.ToSlash(path), Archived: path != opts.CLITelemetryPath, Status: "complete"}
		file, openErr := os.Open(path)
		if openErr != nil {
			coverage.Status = "unreadable"
			if errors.Is(openErr, os.ErrNotExist) {
				coverage.Status = "missing"
			}
			report.files = append(report.files, coverage)
			continue
		}
		snapshot, _, captureErr := captureSnapshot(file)
		if captureErr != nil {
			_ = file.Close()
			coverage.Status = "unreadable"
			report.files = append(report.files, coverage)
			continue
		}
		coverage.SnapshotBytes = &snapshot.size
		var first, last time.Time
		oversized, readErr := readLines(snapshot, cliLineLimit, func(raw []byte) {
			line := bytes.TrimSpace(raw)
			if len(line) == 0 {
				return
			}
			var record cliRecord
			if json.Unmarshal(line, &record) != nil || record.Command == "" || record.At.IsZero() || record.DurationMS < 0 {
				coverage.Invalid++
				return
			}
			coverage.Records++
			if first.IsZero() || record.At.Before(first) {
				first = record.At
			}
			if record.At.After(last) {
				last = record.At
			}
			if !opts.inWindow(record.At) {
				return
			}
			coverage.InWindow++
			measurement := "completion"
			if record.Measurement == "startup" {
				measurement = "startup"
			}
			if record.InvocationID != "" {
				key := record.InvocationID + "\x00" + measurement
				if identified[key] {
					coverage.Duplicates++
					return
				}
				identified[key] = true
			}
			report.Records++
			ok := record.ExitCode == 0
			if !ok {
				report.Failures++
				key := hourKey{command: record.Command, hour: record.At.UTC().Format("2006-01-02T15"), exit: record.ExitCode}
				stats := hours[key]
				if stats == nil {
					stats = &hourStats{first: record.At, last: record.At}
					hours[key] = stats
				}
				stats.count++
				if record.At.Before(stats.first) {
					stats.first = record.At
				}
				if record.At.After(stats.last) {
					stats.last = record.At
				}
			}
			producer := record.Version
			if record.Producer != nil && record.Producer.Commit != "" {
				producer = record.Producer.Commit
			}
			if producer == "" || producer == "dev" {
				report.Unversioned++
				producer = "unknown"
			}
			producers[producer]++
			purpose := record.Purpose
			if purpose != "development" && purpose != "verification" && purpose != "release" {
				purpose = "unknown"
			}
			purposes[purpose]++
			cohort := CLICohort{Purpose: purpose, Producer: producer, Dirty: "unknown", Command: record.Command, Measurement: measurement}
			if record.Dirty != nil {
				cohort.Dirty = "clean"
				if *record.Dirty {
					cohort.Dirty = "dirty"
				}
			}
			if record.App != nil {
				cohort.AppID, cohort.AppName = record.App.ID, record.App.Name
			}
			if cohorts[cohort] == nil {
				cohorts[cohort] = &timingAccumulator{}
			}
			cohorts[cohort].add(record.DurationMS, ok)
			if record.InvocationID != "" {
				report.IdentifiedInvocations++
			}
			if record.DiagnosticCode != "" {
				diagnostics[record.DiagnosticCode]++
			}
			// Every record counts toward its app, or as unattributed; startup
			// measurements are timed apart from command completions.
			if record.App == nil || record.App.ID == "" {
				report.Unattributed++
			} else {
				accumulate(apps, record.App.ID, record.DurationMS, ok)
				appNames[record.App.ID] = record.App.Name
			}
			if record.Measurement == "startup" {
				startup.add(record.DurationMS, ok)
				return
			}
			accumulate(commands, record.Command, record.DurationMS, ok)
		})
		_ = file.Close()
		coverage.ReadBytes = snapshot.readBytes
		coverage.Invalid += oversized
		report.invalid += coverage.Invalid
		if readErr != nil {
			coverage.Status = "partial"
		}
		if !first.IsZero() {
			coverage.First, coverage.Last = first.UTC().Format(time.RFC3339Nano), last.UTC().Format(time.RFC3339Nano)
		}
		report.files = append(report.files, coverage)
	}
	report.Purposes = sortedCounts(purposes, 0)
	for cohort, acc := range cohorts {
		cohort.Timing = acc.timing()
		report.Cohorts = append(report.Cohorts, cohort)
	}
	sort.Slice(report.Cohorts, func(i, j int) bool {
		a, b := report.Cohorts[i], report.Cohorts[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return fmt.Sprintf("%s/%s/%s/%s/%s/%s", a.AppID, a.Purpose, a.Producer, a.Dirty, a.Command, a.Measurement) < fmt.Sprintf("%s/%s/%s/%s/%s/%s", b.AppID, b.Purpose, b.Producer, b.Dirty, b.Command, b.Measurement)
	})
	report.Producers, report.Diagnostics = sortedCounts(producers, 0), sortedCounts(diagnostics, 0)
	report.Startup = startup.timing()
	for command, acc := range commands {
		report.Commands = append(report.Commands, CommandTiming{Command: command, Timing: acc.timing()})
	}
	sort.Slice(report.Commands, func(i, j int) bool {
		return report.Commands[i].Count > report.Commands[j].Count || report.Commands[i].Count == report.Commands[j].Count && report.Commands[i].Command < report.Commands[j].Command
	})
	for id, acc := range apps {
		report.Apps = append(report.Apps, AppTiming{ID: id, Name: appNames[id], Timing: acc.timing()})
	}
	sort.Slice(report.Apps, func(i, j int) bool {
		return report.Apps[i].Count > report.Apps[j].Count || report.Apps[i].Count == report.Apps[j].Count && report.Apps[i].ID < report.Apps[j].ID
	})
	// Consecutive burst hours of one command and exit code form one burst.
	type series struct {
		command string
		exit    int
	}
	byHour := map[series][]string{}
	for key, stats := range hours {
		if stats.count >= burstHourlyFailures {
			s := series{key.command, key.exit}
			byHour[s] = append(byHour[s], key.hour)
		}
	}
	for s, hourList := range byHour {
		sort.Strings(hourList)
		var current *FailureBurst
		var previous time.Time
		for _, hour := range hourList {
			at, _ := time.Parse("2006-01-02T15", hour)
			stats := hours[hourKey{command: s.command, hour: hour, exit: s.exit}]
			if current == nil || at.Sub(previous) > time.Hour {
				if current != nil {
					report.Bursts = append(report.Bursts, *current)
				}
				current = &FailureBurst{Command: s.command, ExitCode: s.exit}
			}
			previous = at
			current.Count += stats.count
			current.PeakPerHour = max(current.PeakPerHour, stats.count)
			if current.firstAt.IsZero() || stats.first.Before(current.firstAt) {
				current.firstAt = stats.first
			}
			if stats.last.After(current.lastAt) {
				current.lastAt = stats.last
			}
		}
		if current != nil {
			report.Bursts = append(report.Bursts, *current)
		}
	}
	for i := range report.Bursts {
		report.Bursts[i].First = report.Bursts[i].firstAt.UTC().Format(time.RFC3339)
		report.Bursts[i].Last = report.Bursts[i].lastAt.UTC().Format(time.RFC3339)
	}
	sort.Slice(report.Bursts, func(i, j int) bool { return report.Bursts[i].Count > report.Bursts[j].Count })
	return report, nil
}

func accumulate(groups map[string]*timingAccumulator, key string, durationMS int64, ok bool) {
	acc := groups[key]
	if acc == nil {
		acc = &timingAccumulator{}
		groups[key] = acc
	}
	acc.add(durationMS, ok)
}
