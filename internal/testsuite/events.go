package testsuite

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var testResultLine = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (.+?) \(([0-9.]+)s\)$`)

type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test,omitempty"`
	Elapsed float64   `json:"Elapsed,omitempty"`
	Output  string    `json:"Output,omitempty"`
}

func writeJSONOutput(writer io.Writer, runs []packageRun, noTestPackages []string) (int, error) {
	encoder := json.NewEncoder(writer)
	testResults := 0
	for _, run := range runs {
		count, err := writePackageEvents(encoder, run)
		testResults += count
		if err != nil {
			return testResults, err
		}
	}
	sort.Strings(noTestPackages)
	for _, pkg := range noTestPackages {
		now := time.Now()
		if err := encoder.Encode(testEvent{Time: now, Action: "start", Package: pkg}); err != nil {
			return testResults, err
		}
		if err := encoder.Encode(testEvent{Time: now, Action: "output", Package: pkg, Output: "?\t" + pkg + "\t[no test files]\n"}); err != nil {
			return testResults, err
		}
		if err := encoder.Encode(testEvent{Time: now, Action: "skip", Package: pkg}); err != nil {
			return testResults, err
		}
	}
	return testResults, nil
}

func writePackageEvents(encoder *json.Encoder, run packageRun) (int, error) {
	now := time.Now()
	if err := encoder.Encode(testEvent{Time: now, Action: "start", Package: run.Package.ImportPath}); err != nil {
		return 0, err
	}
	results := 0
	for _, line := range bytes.SplitAfter(run.Output, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		text := string(line)
		if err := encoder.Encode(testEvent{Time: now, Action: "output", Package: run.Package.ImportPath, Output: text}); err != nil {
			return results, err
		}
		match := testResultLine.FindStringSubmatch(strings.TrimSpace(text))
		if len(match) != 4 {
			continue
		}
		seconds, _ := strconv.ParseFloat(match[3], 64)
		if err := encoder.Encode(testEvent{
			Time: now, Action: strings.ToLower(match[1]), Package: run.Package.ImportPath,
			Test: match[2], Elapsed: seconds,
		}); err != nil {
			return results, err
		}
		results++
	}
	if err := encoder.Encode(testEvent{
		Time: now, Action: run.Action, Package: run.Package.ImportPath, Elapsed: run.Elapsed.Seconds(),
	}); err != nil {
		return results, err
	}
	return results, nil
}
