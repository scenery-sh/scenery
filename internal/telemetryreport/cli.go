package telemetryreport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// burstHourlyFailures is the number of failures of one command and exit code
// within one hour that marks the hour as part of a failure burst: one a
// minute, which no person or agent produces by retrying by hand.
const burstHourlyFailures = 60

// CLIReport aggregates the CLI telemetry records in the window.
type CLIReport struct {
	Records      int `json:"records"`
	Failures     int `json:"failures"`
	Unattributed int `json:"unattributed"`
	// Unversioned counts records whose producer reported no release version
	// ("dev"), which cannot be tied to a producer commit.
	Unversioned int             `json:"unversioned"`
	Startup     Timing          `json:"startup"`
	Commands    []CommandTiming `json:"commands"`
	Apps        []AppTiming     `json:"apps"`
	Bursts      []FailureBurst  `json:"failure_bursts"`
	invalid     int
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
	At          time.Time `json:"at"`
	Command     string    `json:"command"`
	DurationMS  int64     `json:"duration_ms"`
	ExitCode    int       `json:"exit_code"`
	Version     string    `json:"version"`
	Measurement string    `json:"measurement"`
	App         *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"app"`
}

func readCLI(opts Options) (CLIReport, error) {
	report := CLIReport{Commands: []CommandTiming{}, Apps: []AppTiming{}, Bursts: []FailureBurst{}}
	if opts.CLITelemetryPath == "" {
		return report, nil
	}
	file, err := os.Open(opts.CLITelemetryPath)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return CLIReport{}, fmt.Errorf("read CLI telemetry: %w", err)
	}
	defer func() { _ = file.Close() }()
	commands := map[string]*timingAccumulator{}
	apps := map[string]*timingAccumulator{}
	appNames := map[string]string{}
	startup := &timingAccumulator{}
	type hourKey struct {
		command, hour string
		exit          int
	}
	hours := map[hourKey][]time.Time{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record cliRecord
		if json.Unmarshal([]byte(line), &record) != nil || record.Command == "" || record.At.IsZero() {
			report.invalid++
			continue
		}
		if !opts.inWindow(record.At) {
			continue
		}
		report.Records++
		ok := record.ExitCode == 0
		if !ok {
			report.Failures++
			key := hourKey{command: record.Command, hour: record.At.UTC().Format("2006-01-02T15"), exit: record.ExitCode}
			hours[key] = append(hours[key], record.At)
		}
		if record.Version == "" || record.Version == "dev" {
			report.Unversioned++
		}
		if record.Measurement == "startup" {
			startup.add(record.DurationMS, ok)
			continue
		}
		accumulate(commands, record.Command, record.DurationMS, ok)
		if record.App == nil || record.App.ID == "" {
			report.Unattributed++
			continue
		}
		accumulate(apps, record.App.ID, record.DurationMS, ok)
		appNames[record.App.ID] = record.App.Name
	}
	if err := scanner.Err(); err != nil {
		return CLIReport{}, fmt.Errorf("read CLI telemetry: %w", err)
	}
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
	for key, times := range hours {
		if len(times) >= burstHourlyFailures {
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
			times := hours[hourKey{command: s.command, hour: hour, exit: s.exit}]
			if current == nil || at.Sub(previous) > time.Hour {
				if current != nil {
					report.Bursts = append(report.Bursts, *current)
				}
				current = &FailureBurst{Command: s.command, ExitCode: s.exit}
			}
			previous = at
			current.Count += len(times)
			current.PeakPerHour = max(current.PeakPerHour, len(times))
			for _, t := range times {
				if current.firstAt.IsZero() || t.Before(current.firstAt) {
					current.firstAt = t
				}
				if t.After(current.lastAt) {
					current.lastAt = t
				}
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
