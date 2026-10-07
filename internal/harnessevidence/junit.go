package harnessevidence

import (
	"encoding/xml"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"scenery.sh/internal/harnessreport"
)

type junitDocument struct {
	XMLName  xml.Name
	Tests    *int         `xml:"tests,attr"`
	Suites   []junitSuite `xml:"testsuite"`
	Cases    []junitCase  `xml:"testcase"`
	Name     string       `xml:"name,attr"`
	File     string       `xml:"file,attr"`
	Comments string       `xml:",comment"`
}

type junitSuite struct {
	Name   string       `xml:"name,attr"`
	File   string       `xml:"file,attr"`
	Tests  *int         `xml:"tests,attr"`
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	Name     string         `xml:"name,attr"`
	Class    string         `xml:"classname,attr"`
	File     string         `xml:"file,attr"`
	Time     string         `xml:"time,attr"`
	Failures []junitFailure `xml:"failure"`
	Errors   []junitFailure `xml:"error"`
	Skipped  []struct {
		Type    string `xml:"type,attr"`
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	} `xml:"skipped"`
}

type junitFailure struct {
	Type string `xml:"type,attr"`
}

// ParseJUnit reconciles the runner's declared count with all terminal cases.
// File identity comes from the report or an unambiguous explicit selection.
func ParseJUnit(data []byte, selectedFiles []string, attempt int) harnessreport.TestResults {
	result := harnessreport.TestResults{Attempt: attempt, SelectedFiles: append([]string{}, selectedFiles...), Cases: []harnessreport.TestCaseResult{}, Artifacts: []harnessreport.EvidenceArtifact{}, UnknownStages: []string{"case timestamps", "module loading", "discovery wall time", "hooks", "cleanup", "report writing"}}
	result.Completeness.ParserErrors = []string{}
	result.Completeness.MissingFiles = []string{}
	fail := func(message string) {
		result.Completeness.ParserErrors = append(result.Completeness.ParserErrors, message)
	}
	var document junitDocument
	if err := xml.Unmarshal(data, &document); err != nil {
		fail("decode JUnit: " + err.Error())
		return result
	}
	if document.XMLName.Local != "testsuites" && document.XMLName.Local != "testsuite" {
		fail("JUnit root is neither testsuites nor testsuite")
		return result
	}
	if attempt < 1 {
		fail("attempt must be positive")
		return result
	}
	if document.Tests == nil {
		// Node's built-in JUnit writes its reconciled root count as an XML
		// comment, including when cases are direct children of testsuites.
		if count := regexp.MustCompile(`(?:^|\s)tests\s+(\d+)`).FindStringSubmatch(document.Comments); len(count) == 2 {
			n, _ := strconv.Atoi(count[1])
			document.Tests = &n
		}
	}
	suites := document.Suites
	if document.XMLName.Local == "testsuites" && len(document.Cases) > 0 {
		n := len(document.Cases)
		suites = append(suites, junitSuite{Name: "root", Cases: document.Cases, Tests: &n})
	}
	if document.XMLName.Local == "testsuite" {
		suites = []junitSuite{{Name: document.Name, File: document.File, Tests: document.Tests, Suites: document.Suites, Cases: document.Cases}}
	}
	expected := 0
	seenFiles := map[string]bool{}
	seenCases := map[string]bool{}
	var visit func(junitSuite, []string)
	visit = func(suite junitSuite, parents []string) {
		names := append(append([]string{}, parents...), suite.Name)
		suiteName := strings.Join(names, " > ")
		before := len(result.Cases)
		for _, entry := range suite.Cases {
			file := entry.File
			if file == "" {
				file = suite.File
			}
			if file == "" {
				for _, selected := range selectedFiles {
					if filepath.ToSlash(entry.Class) == selected || filepath.ToSlash(suite.Name) == selected {
						file = selected
						break
					}
				}
			}
			if file == "" && len(selectedFiles) == 1 {
				file = selectedFiles[0]
			}
			matched := ""
			for _, selected := range selectedFiles {
				if filepath.ToSlash(file) == selected || strings.HasSuffix(filepath.ToSlash(file), "/"+selected) {
					matched = selected
					break
				}
			}
			if matched == "" {
				fail("case file is outside selected files: " + entry.Name)
			} else {
				file = matched
				seenFiles[file] = true
			}
			seconds, err := strconv.ParseFloat(entry.Time, 64)
			if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
				fail("invalid case duration: " + entry.Name)
				seconds = 0
			}
			outcome := "passed"
			if len(entry.Failures)+len(entry.Errors) > 0 {
				outcome = "failed"
				for _, failure := range append(entry.Failures, entry.Errors...) {
					switch strings.ToLower(failure.Type) {
					case "testtimeoutfailure", "timeouterror", "timeout", "timed_out":
						outcome = "timed_out"
					case "cancelledbyparent", "cancelled", "canceled":
						outcome = "canceled"
					}
				}
			} else if len(entry.Skipped) > 0 {
				outcome = "skipped"
				skipped := entry.Skipped[0]
				if strings.EqualFold(skipped.Type, "todo") || (skipped.Type == "" && strings.Contains(strings.ToUpper(skipped.Message+skipped.Text), "TODO")) {
					outcome = "todo"
				}
			}
			if entry.Name == "" {
				fail("case name is missing")
			}
			firstOutcome := outcome
			if attempt > 1 {
				firstOutcome = "incomplete"
			}
			id := file + "::" + suiteName + "::" + entry.Name
			if seenCases[id] {
				fail("duplicate case identity: " + id)
			}
			seenCases[id] = true
			result.Cases = append(result.Cases, harnessreport.TestCaseResult{ID: id, ParentID: file + "::" + suiteName, File: file, Suite: suiteName, Name: entry.Name, Outcome: outcome, Seconds: seconds, Attempt: attempt, FirstAttemptOutcome: firstOutcome, Boundary: "runner-reported case", Replayed: false})
			if outcome != "skipped" && outcome != "todo" {
				result.Completeness.Executed++
			}
		}
		for _, child := range suite.Suites {
			visit(child, names)
		}
		observed := len(result.Cases) - before
		if suite.Tests == nil {
			fail("suite lacks declared case count: " + suite.Name)
		} else if *suite.Tests != observed {
			fail(fmt.Sprintf("suite %s declares %d cases but has %d terminals", suite.Name, *suite.Tests, observed))
		}
	}
	for _, suite := range suites {
		visit(suite, nil)
		if suite.Tests != nil {
			expected += *suite.Tests
		}
	}
	if document.XMLName.Local == "testsuites" && document.Tests != nil {
		if *document.Tests != expected {
			fail("root/suite declared counts disagree")
		}
		expected = *document.Tests
	}
	for _, selected := range selectedFiles {
		if !seenFiles[selected] {
			result.Completeness.MissingFiles = append(result.Completeness.MissingFiles, selected)
		}
	}
	result.Completeness.Expected = expected
	result.Completeness.Discovered = len(result.Cases)
	result.Completeness.Terminal = len(result.Cases)
	result.Completeness.Complete = expected > 0 && expected == len(result.Cases) && len(result.Completeness.ParserErrors) == 0 && len(result.Completeness.MissingFiles) == 0
	return result
}
