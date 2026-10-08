package main

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/machine"
)

type cliTelemetryApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cliTelemetryRecord struct {
	InvocationID   string            `json:"invocation_id,omitempty"`
	Producer       *machine.Producer `json:"producer,omitempty"`
	DiagnosticCode string            `json:"diagnostic_code,omitempty"`
	Purpose        string            `json:"purpose,omitempty"`
	Dirty          *bool             `json:"dirty,omitempty"`
	At             time.Time         `json:"at"`
	Command        string            `json:"command"`
	DurationMS     int64             `json:"duration_ms"`
	ExitCode       int               `json:"exit_code"`
	Version        string            `json:"version"`
	Mode           string            `json:"mode"`
	Measurement    string            `json:"measurement,omitempty"`
	App            *cliTelemetryApp  `json:"app,omitempty"`
}

const (
	cliTelemetryMeasurementCompletion = "completion"
	cliTelemetryMeasurementStartup    = "startup"
)

// cliTelemetryInvocation records ordinary commands on completion and `up`
// when its owner reaches readiness. `up` completion is intentionally omitted:
// an attached supervisor's eventual exit measures process lifetime, not
// startup latency.
type cliTelemetryInvocation struct {
	invocationID    string
	diagnosticCode  string
	started         time.Time
	args            []string
	now             func() time.Time
	recorder        func(cliTelemetryRecord)
	startupRecorded bool
	suppressed      bool
}

func newCLITelemetryInvocation(started time.Time, args []string) *cliTelemetryInvocation {
	return &cliTelemetryInvocation{
		invocationID: rand.Text(),
		started:      started,
		args:         append([]string(nil), args...),
		now:          time.Now,
		recorder:     recordCLITelemetry,
	}
}

func (i *cliTelemetryInvocation) suppress() {
	if i != nil {
		i.suppressed = true
	}
}

func (i *cliTelemetryInvocation) startupReady() {
	if i == nil || i.suppressed || i.startupRecorded || telemetryCommand(i.args) != "up" {
		return
	}
	i.startupRecorded = true
	i.record(cliTelemetryMeasurementStartup, 0)
}

func (i *cliTelemetryInvocation) finish(exitCode int) {
	if i == nil || i.suppressed {
		return
	}
	if telemetryCommand(i.args) == "up" {
		if i.startupRecorded || exitCode == 0 {
			return
		}
		i.startupRecorded = true
		i.record(cliTelemetryMeasurementStartup, exitCode)
		return
	}
	i.record("", exitCode)
}

func (i *cliTelemetryInvocation) record(measurement string, exitCode int) {
	finished := i.now()
	duration := finished.Sub(i.started)
	if duration < 0 {
		duration = 0
	}
	recorder := i.recorder
	if recorder == nil {
		recorder = recordCLITelemetry
	}
	producer := cliProducer()
	dirty := cliBuildDirty()
	recorder(cliTelemetryRecord{
		InvocationID:   i.invocationID,
		Producer:       &producer,
		DiagnosticCode: i.diagnosticCode,
		Purpose:        telemetryExecutionPurpose(),
		Dirty:          &dirty,
		At:             i.started.UTC(),
		Command:        telemetryCommand(i.args),
		DurationMS:     duration.Milliseconds(),
		ExitCode:       exitCode,
		Version:        producer.Version,
		Mode:           telemetryMode(i.args),
		Measurement:    measurement,
		App:            telemetryInvocationApp(i.args),
	})
}

// Purpose is explicit process attribution, independent of app/build identity.
// Unmarked invocations remain unknown rather than inferred from their home.
func telemetryExecutionPurpose() string {
	switch value := envpolicy.Get("SCENERY_EXECUTION_PURPOSE"); value {
	case "development", "verification", "release":
		return value
	default:
		return "unknown"
	}
}

func telemetryRecordMeasurement(record cliTelemetryRecord) string {
	if record.Measurement == cliTelemetryMeasurementStartup {
		return cliTelemetryMeasurementStartup
	}
	return cliTelemetryMeasurementCompletion
}

func recordCLITelemetry(record cliTelemetryRecord) {
	path, err := cliTelemetryPath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = file.Write(append(encoded, '\n'))
}

func cliTelemetryPath() (string, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.Home, "telemetry.jsonl"), nil
}

// telemetryInvocationApp resolves only stable configured identity. App roots
// and raw arguments never enter the telemetry stream.
func telemetryInvocationApp(args []string) *cliTelemetryApp {
	return telemetryInvocationAppFrom(args, ".")
}

func telemetryInvocationAppFrom(args []string, start string) *cliTelemetryApp {
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		if argument == "--app-root" {
			if index+1 >= len(args) {
				return nil
			}
			start = args[index+1]
			break
		}
		if strings.HasPrefix(argument, "--app-root=") {
			start = strings.TrimPrefix(argument, "--app-root=")
			break
		}
	}
	root, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil || root == "" {
		return nil
	}
	return &cliTelemetryApp{ID: cfg.AppID(), Name: cfg.Name}
}

func telemetryCommand(args []string) string {
	if _, help := helpRequestTopics(args); len(args) == 0 || help {
		return "help"
	}
	subcommands, known := telemetryReportCommandFamilies()[args[0]]
	if !known {
		return "unknown"
	}
	if len(args) > 1 && slices.Contains(subcommands, args[1]) {
		return args[0] + " " + args[1]
	}
	return args[0]
}

func telemetryMode(args []string) string {
	if _, help := helpRequestTopics(args); len(args) == 0 || help {
		return "oneshot"
	}
	switch args[0] {
	case "feature":
		if options, err := parseFeatureArgs(args[1:]); err == nil && options.Watch {
			return "long_running"
		}
	case "up", "worker", "console":
		return "long_running"
	case "logs":
		for _, arg := range args[1:] {
			if arg == "--follow" {
				return "long_running"
			}
		}
	}
	return "oneshot"
}
