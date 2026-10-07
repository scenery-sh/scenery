package main

import (
	"fmt"
	"sort"
	"strings"

	"scenery.sh/internal/harnessreport"
)

type timingDiscovery struct {
	counts      harnessreport.TimingDiscovery
	active      map[string]bool
	replayed    map[string]bool
	missingTime map[string]bool
	errors      []string
}

func newTimingDiscovery() *timingDiscovery {
	return &timingDiscovery{active: map[string]bool{}, replayed: map[string]bool{}, missingTime: map[string]bool{}}
}

func (d *timingDiscovery) nonEvent(line []byte, err error) {
	text := strings.TrimSpace(string(line))
	for _, prefix := range []string{"# ", "go: ", "FAIL\t", "ok\t", "?\t"} {
		if strings.HasPrefix(text, prefix) {
			d.counts.NonEventLines++
			return
		}
	}
	d.addError("invalid Go JSON event: " + err.Error())
}

func (d *timingDiscovery) addError(message string) {
	if len(d.errors) < 20 {
		d.errors = append(d.errors, message)
	}
}

func (d *timingDiscovery) observe(event goTestJSONEvent) {
	if event.Package == "" || event.Action == "" {
		d.addError("event lacks package/action identity")
		return
	}
	if event.Time.IsZero() && !d.missingTime[event.Package] {
		d.addError("event timestamps missing for " + event.Package)
		d.missingTime[event.Package] = true
	}
	key := event.Package + "/" + event.Test
	if strings.Contains(event.Output, "(cached)") {
		d.replayed[event.Package] = true
	}
	switch event.Action {
	case "start":
		if event.Test != "" {
			d.addError("package start names a test")
			return
		}
		d.counts.PackageStarts++
	case "run":
		if event.Test == "" {
			d.addError("test run has no test identity")
			return
		}
		d.counts.TestStarts++
		if isExactTopLevelGoTestRoot(event.Test) {
			d.counts.RootStarts++
		}
	case "pass", "fail", "skip":
		if event.Test == "" {
			d.counts.PackageTerminals++
		} else {
			d.counts.TestTerminals++
			if isExactTopLevelGoTestRoot(event.Test) {
				d.counts.RootTerminals++
			}
		}
		if !d.active[key] {
			d.addError("terminal lacks matching start: " + key)
		}
		delete(d.active, key)
		return
	case "pause", "cont":
		if !d.active[key] {
			d.addError("lifecycle lacks matching run: " + key)
		}
		return
	case "output":
		return
	default:
		d.addError("unrecognized lifecycle action: " + event.Action)
		return
	}
	if d.active[key] {
		d.addError("duplicate start: " + key)
	}
	d.active[key] = true
}

func (d *timingDiscovery) result() harnessreport.TimingDiscovery {
	result := d.counts
	result.ParserErrors = append([]string{}, d.errors...)
	result.MissingTerminals = []string{}
	for key := range d.active {
		result.MissingTerminals = append(result.MissingTerminals, key)
	}
	sort.Strings(result.MissingTerminals)
	result.ReplayedPackages = len(d.replayed)
	result.Complete = result.PackageStarts > 0 && result.PackageStarts == result.PackageTerminals && result.TestStarts == result.TestTerminals && len(result.ParserErrors) == 0 && len(result.MissingTerminals) == 0
	return result
}

func qualifyTimingDiscovery(report *harnessTestTimingReport) {
	d := &report.Discovery
	if d.ExpectedPackages != nil && *d.ExpectedPackages != d.PackageStarts {
		d.Complete = false
	}
	if d.ExpectedRoots != nil && *d.ExpectedRoots != d.RootStarts {
		d.Complete = false
	}
	if d.Complete {
		return
	}
	severity := "warning"
	if report.Budgets.Mode == "enforce-total" {
		severity = "error"
	}
	report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
		Stage: "go tests", Severity: severity,
		Message:         fmt.Sprintf("incomplete Go timing discovery: packages=%d/%d tests=%d/%d roots=%d/%d parser_errors=%d missing_terminals=%d", d.PackageTerminals, d.PackageStarts, d.TestTerminals, d.TestStarts, d.RootTerminals, d.RootStarts, len(d.ParserErrors), len(d.MissingTerminals)),
		SuggestedAction: "Inspect the retained Go JSON stream and rerun the same selected lane; incomplete discovery cannot establish timing proof.",
	})
}
