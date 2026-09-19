package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/redact"
)

const failureReportKind = "scenery.failure.report"

// maxFailureReports bounds the reports one agent home retains; the oldest are
// removed when a new report is written.
var maxFailureReports = 200

var failureReportTokenPattern = regexp.MustCompile(`^rpt_[a-z0-9]+$`)

// failureReport is the cause behind one report token, kept where only the
// person or agent running this CLI can read it. A public diagnostic carries the
// token and a sanitized message; this record says what actually failed.
type failureReport struct {
	cliPayloadIdentity
	ReportToken      string   `json:"report_token"`
	Code             string   `json:"code"`
	RecordedAt       string   `json:"recorded_at"`
	Command          string   `json:"command"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"working_directory"`
	Cause            string   `json:"cause"`
	ProducerVersion  string   `json:"producer_version"`
}

// failureReportArguments are the arguments of the running invocation.
var failureReportArguments atomic.Pointer[[]string]

func failureReportDir() (string, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.Home, "reports"), nil
}

// installFailureReports records the cause of every report token this process
// mints. Recording never fails the command: a report is evidence about a
// failure that already happened.
func installFailureReports(args []string) {
	failureReportArguments.Store(&args)
	machine.SetInternalFailureSink(func(failure machine.InternalFailure) {
		_ = writeFailureReport(failure, time.Now().UTC())
	})
}

func writeFailureReport(failure machine.InternalFailure, now time.Time) error {
	if !failureReportTokenPattern.MatchString(failure.ReportToken) {
		return errors.New("invalid report token")
	}
	dir, err := failureReportDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var invocation []string
	if stored := failureReportArguments.Load(); stored != nil {
		invocation = *stored
	}
	arguments := make([]string, 0, len(invocation))
	for _, argument := range invocation {
		arguments = append(arguments, redact.String(argument))
	}
	workingDirectory, _ := os.Getwd()
	report := failureReport{
		cliPayloadIdentity: newCLIPayloadIdentity(failureReportKind),
		ReportToken:        failure.ReportToken, Code: failure.Code, RecordedAt: now.Format(time.RFC3339Nano),
		Command: telemetryCommand(invocation), Arguments: arguments, WorkingDirectory: workingDirectory,
		Cause: redact.String(failure.Cause), ProducerVersion: buildVersionResponse().Version,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, failure.ReportToken+".json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return pruneFailureReports(dir)
}

func pruneFailureReports(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type retained struct {
		name     string
		modified time.Time
	}
	var reports []retained
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			reports = append(reports, retained{name: entry.Name(), modified: info.ModTime()})
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].modified.After(reports[j].modified) })
	for index := maxFailureReports; index < len(reports); index++ {
		_ = os.Remove(filepath.Join(dir, reports[index].name))
	}
	return nil
}

func readFailureReport(token string) (failureReport, error) {
	if !failureReportTokenPattern.MatchString(token) {
		return failureReport{}, usageErrorf("report token %q is not a Scenery report token (rpt_...)", token)
	}
	dir, err := failureReportDir()
	if err != nil {
		return failureReport{}, err
	}
	encoded, err := os.ReadFile(filepath.Join(dir, token+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return failureReport{}, usageErrorf("report %s is not recorded here; a report is kept by the agent home of the process that failed, and only the newest %d are retained", token, maxFailureReports)
	}
	if err != nil {
		return failureReport{}, err
	}
	var report failureReport
	if err := json.Unmarshal(encoded, &report); err != nil {
		return failureReport{}, err
	}
	return report, nil
}
