package testsuite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test,omitempty"`
	Elapsed float64   `json:"Elapsed,omitempty"`
	Output  string    `json:"Output,omitempty"`
}

// Forward complete native records while binaries run. Concurrent packages
// cannot splice JSON lines or replace test2json's original event timestamps.
type lockedEventOutput struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedEventOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writer == nil {
		return len(data), nil
	}
	return w.writer.Write(data)
}

type testEventOutput struct {
	sink    io.Writer
	output  bytes.Buffer
	pending bytes.Buffer
}

func (w *testEventOutput) Write(data []byte) (int, error) {
	_, _ = w.output.Write(data)
	_, _ = w.pending.Write(data)
	for {
		line, err := w.pending.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			_, _ = w.pending.Write(line)
			return len(data), nil
		}
		if _, err := w.sink.Write(line); err != nil {
			return 0, err
		}
	}
}

func (w *testEventOutput) finish() error {
	if w.pending.Len() > 0 {
		return fmt.Errorf("native test2json stream ended with an incomplete record")
	}
	return nil
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
		for _, event := range []testEvent{
			{Time: now, Action: "start", Package: pkg},
			{Time: now, Action: "output", Package: pkg, Output: "?\t" + pkg + "\t[no test files]\n"},
			{Time: now, Action: "skip", Package: pkg},
		} {
			if err := encoder.Encode(event); err != nil {
				return testResults, err
			}
		}
	}
	return testResults, nil
}

func writePackageEvents(encoder *json.Encoder, run packageRun) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(run.Output))
	results := 0
	packageFinished := false
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return results, fmt.Errorf("decode native events for %s: %w", run.Package.ImportPath, err)
		}
		var event testEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return results, err
		}
		if event.Package != run.Package.ImportPath || event.Time.IsZero() {
			return results, fmt.Errorf("native event lacks package/timestamp identity for %s", run.Package.ImportPath)
		}
		switch event.Action {
		case "pass", "fail", "skip":
			if event.Test != "" {
				results++
			} else {
				packageFinished = true
			}
		}
		if !run.Streamed {
			if err := encoder.Encode(raw); err != nil {
				return results, err
			}
		}
	}
	if !packageFinished {
		return results, fmt.Errorf("native event stream for %s has no package terminal", run.Package.ImportPath)
	}
	return results, nil
}
