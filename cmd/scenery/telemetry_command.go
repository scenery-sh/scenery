package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	cliTelemetryPayloadKind = "scenery.telemetry"
	defaultTelemetryLimit   = 100
	maximumTelemetryLimit   = 10000
)

type telemetryQueryOptions struct {
	Apps     []string
	Commands []string
	Since    time.Time
	Limit    int
	JSON     bool
}

type telemetryQuery struct {
	Apps     []string `json:"apps"`
	Commands []string `json:"commands"`
	Since    string   `json:"since,omitempty"`
	Limit    int      `json:"limit"`
}

type telemetryTimingStats struct {
	Count           int     `json:"count"`
	SuccessCount    int     `json:"success_count"`
	FailureCount    int     `json:"failure_count"`
	TotalDurationMS int64   `json:"total_duration_ms"`
	AverageMS       float64 `json:"avg_duration_ms"`
	MinDurationMS   int64   `json:"min_duration_ms"`
	MaxDurationMS   int64   `json:"max_duration_ms"`
}

type telemetrySummary struct {
	telemetryTimingStats
	ReturnedCount     int `json:"returned_count"`
	InvalidCount      int `json:"invalid_record_count"`
	UnattributedCount int `json:"unattributed_count"`
}

type telemetryAppStats struct {
	App cliTelemetryApp `json:"app"`
	telemetryTimingStats
}

type telemetryCommandStats struct {
	Command string `json:"command"`
	telemetryTimingStats
}

type telemetryResponse struct {
	cliPayloadIdentity
	Query    telemetryQuery          `json:"query"`
	Summary  telemetrySummary        `json:"summary"`
	Apps     []telemetryAppStats     `json:"apps"`
	Commands []telemetryCommandStats `json:"commands"`
	Records  []cliTelemetryRecord    `json:"records"`
	Warnings []string                `json:"warnings"`
}

func runTelemetryCommand(stdout io.Writer, args []string) error {
	opts, err := parseTelemetryArgs(args, time.Now().UTC())
	if err != nil {
		return err
	}
	path, err := cliTelemetryPath()
	if err != nil {
		return err
	}
	response, err := loadCLITelemetry(path, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeCLIJSON(stdout, response)
	}
	return writeTelemetryHuman(stdout, response)
}

func parseTelemetryArgs(args []string, now time.Time) (telemetryQueryOptions, error) {
	opts := telemetryQueryOptions{Limit: defaultTelemetryLimit}
	flags := newCLIFlagSet("telemetry")
	registerJSONOutput(flags, &opts.JSON)
	var since string
	flags.IntVar(&opts.Limit, "limit", defaultTelemetryLimit, "")
	flags.StringVar(&since, "since", "", "")
	flags.Func("app", "", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return errors.New("app filter must not be empty")
		}
		opts.Apps = append(opts.Apps, value)
		return nil
	})
	flags.Func("command", "", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return errors.New("command filter must not be empty")
		}
		opts.Commands = append(opts.Commands, value)
		return nil
	})
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return telemetryQueryOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return telemetryQueryOptions{}, err
	}
	if opts.Limit < 1 || opts.Limit > maximumTelemetryLimit {
		return telemetryQueryOptions{}, fmt.Errorf("--limit must be between 1 and %d", maximumTelemetryLimit)
	}
	if since != "" {
		duration, err := parsePositiveDuration(since, "since")
		if err != nil {
			return telemetryQueryOptions{}, err
		}
		opts.Since = now.Add(-duration)
	}
	opts.Apps = sortedUniqueStrings(opts.Apps)
	opts.Commands = sortedUniqueStrings(opts.Commands)
	return opts, nil
}

func loadCLITelemetry(path string, opts telemetryQueryOptions) (telemetryResponse, error) {
	response := telemetryResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(cliTelemetryPayloadKind),
		Query: telemetryQuery{
			Apps: append([]string{}, opts.Apps...), Commands: append([]string{}, opts.Commands...), Limit: opts.Limit,
		},
		Apps: []telemetryAppStats{}, Commands: []telemetryCommandStats{}, Records: []cliTelemetryRecord{}, Warnings: []string{},
	}
	if !opts.Since.IsZero() {
		response.Query.Since = opts.Since.Format(time.RFC3339Nano)
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return response, nil
	}
	if err != nil {
		return telemetryResponse{}, fmt.Errorf("read telemetry: %w", err)
	}
	defer file.Close()

	appFilter := stringSet(opts.Apps)
	commandFilter := stringSet(opts.Commands)
	appGroups := map[string]*telemetryAppStats{}
	commandGroups := map[string]*telemetryCommandStats{}
	nextRecord := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record cliTelemetryRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil || !validCLITelemetryRecord(record) {
			response.Summary.InvalidCount++
			continue
		}
		if !opts.Since.IsZero() && record.At.Before(opts.Since) {
			continue
		}
		if len(commandFilter) > 0 {
			if _, ok := commandFilter[record.Command]; !ok {
				continue
			}
		}
		if len(appFilter) > 0 {
			if record.App == nil || (!telemetrySetContains(appFilter, record.App.ID) && !telemetrySetContains(appFilter, record.App.Name)) {
				continue
			}
		}

		addTelemetryTiming(&response.Summary.telemetryTimingStats, record)
		if record.App == nil {
			response.Summary.UnattributedCount++
		} else {
			key := record.App.ID + "\x00" + record.App.Name
			group := appGroups[key]
			if group == nil {
				group = &telemetryAppStats{App: *record.App}
				appGroups[key] = group
			}
			addTelemetryTiming(&group.telemetryTimingStats, record)
		}
		commandGroup := commandGroups[record.Command]
		if commandGroup == nil {
			commandGroup = &telemetryCommandStats{Command: record.Command}
			commandGroups[record.Command] = commandGroup
		}
		addTelemetryTiming(&commandGroup.telemetryTimingStats, record)

		if len(response.Records) < opts.Limit {
			response.Records = append(response.Records, record)
		} else {
			response.Records[nextRecord] = record
			nextRecord = (nextRecord + 1) % opts.Limit
		}
	}
	if err := scanner.Err(); err != nil {
		return telemetryResponse{}, fmt.Errorf("read telemetry: %w", err)
	}
	if response.Summary.InvalidCount > 0 {
		response.Warnings = append(response.Warnings, fmt.Sprintf("skipped %d invalid telemetry records", response.Summary.InvalidCount))
	}
	if len(response.Records) == opts.Limit && nextRecord > 0 {
		ordered := append([]cliTelemetryRecord{}, response.Records[nextRecord:]...)
		ordered = append(ordered, response.Records[:nextRecord]...)
		response.Records = ordered
	}
	for left, right := 0, len(response.Records)-1; left < right; left, right = left+1, right-1 {
		response.Records[left], response.Records[right] = response.Records[right], response.Records[left]
	}
	response.Summary.ReturnedCount = len(response.Records)
	finalizeTelemetryTiming(&response.Summary.telemetryTimingStats)
	for _, group := range appGroups {
		finalizeTelemetryTiming(&group.telemetryTimingStats)
		response.Apps = append(response.Apps, *group)
	}
	sort.Slice(response.Apps, func(i, j int) bool {
		if response.Apps[i].App.ID == response.Apps[j].App.ID {
			return response.Apps[i].App.Name < response.Apps[j].App.Name
		}
		return response.Apps[i].App.ID < response.Apps[j].App.ID
	})
	for _, group := range commandGroups {
		finalizeTelemetryTiming(&group.telemetryTimingStats)
		response.Commands = append(response.Commands, *group)
	}
	sort.Slice(response.Commands, func(i, j int) bool { return response.Commands[i].Command < response.Commands[j].Command })
	return response, nil
}

func validCLITelemetryRecord(record cliTelemetryRecord) bool {
	if record.At.IsZero() || strings.TrimSpace(record.Command) == "" || record.DurationMS < 0 {
		return false
	}
	if record.Mode != "oneshot" && record.Mode != "long_running" {
		return false
	}
	return record.App == nil || (strings.TrimSpace(record.App.ID) != "" && strings.TrimSpace(record.App.Name) != "")
}

func addTelemetryTiming(stats *telemetryTimingStats, record cliTelemetryRecord) {
	stats.Count++
	stats.TotalDurationMS += record.DurationMS
	if record.ExitCode == 0 {
		stats.SuccessCount++
	} else {
		stats.FailureCount++
	}
	if stats.Count == 1 || record.DurationMS < stats.MinDurationMS {
		stats.MinDurationMS = record.DurationMS
	}
	if stats.Count == 1 || record.DurationMS > stats.MaxDurationMS {
		stats.MaxDurationMS = record.DurationMS
	}
}

func finalizeTelemetryTiming(stats *telemetryTimingStats) {
	if stats.Count > 0 {
		stats.AverageMS = float64(stats.TotalDurationMS) / float64(stats.Count)
	}
}

func writeTelemetryHuman(w io.Writer, response telemetryResponse) error {
	if _, err := fmt.Fprintf(w, "Scenery CLI telemetry: %d matching, %d shown, avg %.1fms, max %dms, failures %d\n", response.Summary.Count, response.Summary.ReturnedCount, response.Summary.AverageMS, response.Summary.MaxDurationMS, response.Summary.FailureCount); err != nil {
		return err
	}
	if response.Summary.UnattributedCount > 0 {
		if _, err := fmt.Fprintf(w, "Unattributed historical records: %d\n", response.Summary.UnattributedCount); err != nil {
			return err
		}
	}
	if len(response.Apps) > 0 {
		if _, err := fmt.Fprintln(w, "\nApps:"); err != nil {
			return err
		}
		table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "APP\tCALLS\tAVG\tMAX\tFAILURES")
		for _, app := range response.Apps {
			label := app.App.ID
			if app.App.Name != app.App.ID {
				label = app.App.Name + " (" + app.App.ID + ")"
			}
			fmt.Fprintf(table, "%s\t%d\t%.1fms\t%dms\t%d\n", label, app.Count, app.AverageMS, app.MaxDurationMS, app.FailureCount)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	if len(response.Commands) > 0 {
		if _, err := fmt.Fprintln(w, "\nCommands:"); err != nil {
			return err
		}
		table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "COMMAND\tCALLS\tAVG\tMAX\tFAILURES")
		for _, command := range response.Commands {
			fmt.Fprintf(table, "%s\t%d\t%.1fms\t%dms\t%d\n", command.Command, command.Count, command.AverageMS, command.MaxDurationMS, command.FailureCount)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	if len(response.Records) > 0 {
		if _, err := fmt.Fprintln(w, "\nRecent:"); err != nil {
			return err
		}
		table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "AT\tAPP\tCOMMAND\tDURATION\tEXIT\tMODE\tVERSION")
		for _, record := range response.Records {
			appID := "-"
			if record.App != nil {
				appID = record.App.ID
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%dms\t%d\t%s\t%s\n", record.At.Local().Format("2006-01-02 15:04:05"), appID, record.Command, record.DurationMS, record.ExitCode, record.Mode, record.Version)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	for _, warning := range response.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func telemetrySetContains(set map[string]struct{}, value string) bool {
	_, ok := set[value]
	return ok
}

func sortedUniqueStrings(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
