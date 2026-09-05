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
	Apps         []string
	Commands     []string
	Measurements []string
	Since        time.Time
	Limit        int
	JSON         bool
}

type telemetryQuery struct {
	Apps         []string `json:"apps"`
	Commands     []string `json:"commands"`
	Measurements []string `json:"measurements"`
	Since        string   `json:"since,omitempty"`
	Limit        int      `json:"limit"`
}

type telemetryTimingStats struct {
	Count                 int     `json:"count"`
	SuccessCount          int     `json:"success_count"`
	FailureCount          int     `json:"failure_count"`
	TotalDurationMS       int64   `json:"total_duration_ms"`
	AverageMS             float64 `json:"avg_duration_ms"`
	MinDurationMS         int64   `json:"min_duration_ms"`
	MaxDurationMS         int64   `json:"max_duration_ms"`
	PercentileSampleCount int     `json:"percentile_sample_count"`
	P50DurationMS         int64   `json:"p50_duration_ms"`
	P95DurationMS         int64   `json:"p95_duration_ms"`
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

type telemetryMeasurementStats struct {
	Measurement string `json:"measurement"`
	telemetryTimingStats
}

type telemetryResponse struct {
	cliPayloadIdentity
	Query        telemetryQuery              `json:"query"`
	Summary      telemetrySummary            `json:"summary"`
	Apps         []telemetryAppStats         `json:"apps"`
	Commands     []telemetryCommandStats     `json:"commands"`
	Measurements []telemetryMeasurementStats `json:"measurements"`
	Records      []cliTelemetryRecord        `json:"records"`
	Warnings     []string                    `json:"warnings"`
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
	flags.Func("measurement", "", func(value string) error {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case cliTelemetryMeasurementCompletion, cliTelemetryMeasurementStartup:
			opts.Measurements = append(opts.Measurements, value)
			return nil
		default:
			return fmt.Errorf("measurement must be %q or %q", cliTelemetryMeasurementCompletion, cliTelemetryMeasurementStartup)
		}
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
	opts.Measurements = sortedUniqueStrings(opts.Measurements)
	return opts, nil
}

func loadCLITelemetry(path string, opts telemetryQueryOptions) (telemetryResponse, error) {
	response := telemetryResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(cliTelemetryPayloadKind),
		Query: telemetryQuery{
			Apps: append([]string{}, opts.Apps...), Commands: append([]string{}, opts.Commands...), Measurements: append([]string{}, opts.Measurements...), Limit: opts.Limit,
		},
		Apps: []telemetryAppStats{}, Commands: []telemetryCommandStats{}, Measurements: []telemetryMeasurementStats{}, Records: []cliTelemetryRecord{}, Warnings: []string{},
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
	defer func() { _ = file.Close() }()

	appFilter := stringSet(opts.Apps)
	commandFilter := stringSet(opts.Commands)
	measurementFilter := stringSet(opts.Measurements)
	appGroups := map[string]*telemetryAppStats{}
	commandGroups := map[string]*telemetryCommandStats{}
	measurementGroups := map[string]*telemetryMeasurementStats{}
	nextRecord := 0
	percentileSamples := make([]cliTelemetryRecord, 0, maximumTelemetryLimit)
	nextPercentileSample := 0
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
		measurement := telemetryRecordMeasurement(record)
		if len(measurementFilter) > 0 {
			if _, ok := measurementFilter[measurement]; !ok {
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
		measurementGroup := measurementGroups[measurement]
		if measurementGroup == nil {
			measurementGroup = &telemetryMeasurementStats{Measurement: measurement}
			measurementGroups[measurement] = measurementGroup
		}
		addTelemetryTiming(&measurementGroup.telemetryTimingStats, record)

		if len(percentileSamples) < maximumTelemetryLimit {
			percentileSamples = append(percentileSamples, record)
		} else {
			percentileSamples[nextPercentileSample] = record
			nextPercentileSample = (nextPercentileSample + 1) % maximumTelemetryLimit
		}

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
	for _, group := range measurementGroups {
		finalizeTelemetryTiming(&group.telemetryTimingStats)
		response.Measurements = append(response.Measurements, *group)
	}
	sort.Slice(response.Measurements, func(i, j int) bool {
		return response.Measurements[i].Measurement < response.Measurements[j].Measurement
	})
	applyTelemetryPercentiles(&response, percentileSamples, appGroups, commandGroups, measurementGroups)
	return response, nil
}

func validCLITelemetryRecord(record cliTelemetryRecord) bool {
	if record.At.IsZero() || strings.TrimSpace(record.Command) == "" || record.DurationMS < 0 {
		return false
	}
	if record.Mode != "oneshot" && record.Mode != "long_running" {
		return false
	}
	if record.Measurement != "" && record.Measurement != cliTelemetryMeasurementStartup {
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

func applyTelemetryPercentiles(response *telemetryResponse, samples []cliTelemetryRecord, appGroups map[string]*telemetryAppStats, commandGroups map[string]*telemetryCommandStats, measurementGroups map[string]*telemetryMeasurementStats) {
	all := make([]int64, 0, len(samples))
	apps := make(map[string][]int64)
	commands := make(map[string][]int64)
	measurements := make(map[string][]int64)
	for _, record := range samples {
		all = append(all, record.DurationMS)
		if record.App != nil {
			key := record.App.ID + "\x00" + record.App.Name
			apps[key] = append(apps[key], record.DurationMS)
		}
		commands[record.Command] = append(commands[record.Command], record.DurationMS)
		measurement := telemetryRecordMeasurement(record)
		measurements[measurement] = append(measurements[measurement], record.DurationMS)
	}
	setTelemetryPercentiles(&response.Summary.telemetryTimingStats, all)
	for key, durations := range apps {
		setTelemetryPercentiles(&appGroups[key].telemetryTimingStats, durations)
	}
	for key, durations := range commands {
		setTelemetryPercentiles(&commandGroups[key].telemetryTimingStats, durations)
	}
	for key, durations := range measurements {
		setTelemetryPercentiles(&measurementGroups[key].telemetryTimingStats, durations)
	}
	for index := range response.Apps {
		key := response.Apps[index].App.ID + "\x00" + response.Apps[index].App.Name
		response.Apps[index].telemetryTimingStats = appGroups[key].telemetryTimingStats
	}
	for index := range response.Commands {
		response.Commands[index].telemetryTimingStats = commandGroups[response.Commands[index].Command].telemetryTimingStats
	}
	for index := range response.Measurements {
		response.Measurements[index].telemetryTimingStats = measurementGroups[response.Measurements[index].Measurement].telemetryTimingStats
	}
}

func setTelemetryPercentiles(stats *telemetryTimingStats, durations []int64) {
	stats.PercentileSampleCount = len(durations)
	if len(durations) == 0 {
		return
	}
	ordered := append([]int64(nil), durations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	stats.P50DurationMS = nearestRankPercentile(ordered, 50)
	stats.P95DurationMS = nearestRankPercentile(ordered, 95)
}

func nearestRankPercentile(ordered []int64, percentile int) int64 {
	if len(ordered) == 0 {
		return 0
	}
	index := (percentile*len(ordered) + 99) / 100
	if index < 1 {
		index = 1
	}
	return ordered[index-1]
}

func writeTelemetryHuman(w io.Writer, response telemetryResponse) error {
	if _, err := fmt.Fprintf(w, "Scenery CLI telemetry: %d matching, %d shown, avg %.1fms, p50 %dms, p95 %dms, max %dms, failures %d\n", response.Summary.Count, response.Summary.ReturnedCount, response.Summary.AverageMS, response.Summary.P50DurationMS, response.Summary.P95DurationMS, response.Summary.MaxDurationMS, response.Summary.FailureCount); err != nil {
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
		_, _ = fmt.Fprintln(table, "APP\tCALLS\tAVG\tP50\tP95\tMAX\tFAILURES")
		for _, app := range response.Apps {
			label := app.App.ID
			if app.App.Name != app.App.ID {
				label = app.App.Name + " (" + app.App.ID + ")"
			}
			_, _ = fmt.Fprintf(table, "%s\t%d\t%.1fms\t%dms\t%dms\t%dms\t%d\n", label, app.Count, app.AverageMS, app.P50DurationMS, app.P95DurationMS, app.MaxDurationMS, app.FailureCount)
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
		_, _ = fmt.Fprintln(table, "COMMAND\tCALLS\tAVG\tP50\tP95\tMAX\tFAILURES")
		for _, command := range response.Commands {
			_, _ = fmt.Fprintf(table, "%s\t%d\t%.1fms\t%dms\t%dms\t%dms\t%d\n", command.Command, command.Count, command.AverageMS, command.P50DurationMS, command.P95DurationMS, command.MaxDurationMS, command.FailureCount)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	if len(response.Measurements) > 0 {
		if _, err := fmt.Fprintln(w, "\nMeasurements:"); err != nil {
			return err
		}
		table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "MEASUREMENT\tCALLS\tSAMPLE\tAVG\tP50\tP95\tMAX\tFAILURES")
		for _, measurement := range response.Measurements {
			_, _ = fmt.Fprintf(table, "%s\t%d\t%d\t%.1fms\t%dms\t%dms\t%dms\t%d\n", measurement.Measurement, measurement.Count, measurement.PercentileSampleCount, measurement.AverageMS, measurement.P50DurationMS, measurement.P95DurationMS, measurement.MaxDurationMS, measurement.FailureCount)
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
		_, _ = fmt.Fprintln(table, "AT\tAPP\tCOMMAND\tMEASUREMENT\tDURATION\tEXIT\tMODE\tVERSION")
		for _, record := range response.Records {
			appID := "-"
			if record.App != nil {
				appID = record.App.ID
			}
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%dms\t%d\t%s\t%s\n", record.At.Local().Format("2006-01-02 15:04:05"), appID, record.Command, telemetryRecordMeasurement(record), record.DurationMS, record.ExitCode, record.Mode, record.Version)
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
