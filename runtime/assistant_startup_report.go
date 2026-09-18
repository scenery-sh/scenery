package runtime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// A production assistant answers a caller neutrally when it is unavailable, but
// an operator must still be able to see which step of its startup failed
// without rerunning the application. Every assistant records its startup phases
// and a bounded tail of its helper's own output in the private startup report
// of the application's assistant state directory. The report holds no token, no
// environment and no descriptor: only phase names, outcomes, error codes and
// the helper's own output, which the provider writes.
const (
	assistantStartupReportFile = "startup.json"
	// assistantStartupOutputLines bounds the helper output the report keeps.
	assistantStartupOutputLines = 64
	assistantStartupOutputBytes = 2000
)

// Startup phases, in the order a production assistant passes them.
const (
	assistantPhaseAssetsVerified = "assets_verified"
	assistantPhaseProcessStarted = "process_started"
	assistantPhaseGatewayReady   = "mcp_gateway_ready"
	assistantPhaseHelperAccepted = "helper_health_and_info_accepted"
	assistantPhaseClientReady    = "client_installed"
	assistantPhaseProcessExited  = "process_exited"
	// assistantPhaseRequestClient records a public request that found no
	// installed client, which separates a missing client from a failing helper.
	assistantPhaseRequestClient = "client_present_at_request"
	// assistantPhaseRequestServed records the outcome of a public conversation
	// request, so an assistant that became unavailable after startup names its
	// failure instead of only answering neutrally.
	assistantPhaseRequestServed = "conversation_request_served"
)

type assistantStartupPhase struct {
	Phase     string    `json:"phase"`
	OK        bool      `json:"ok"`
	At        time.Time `json:"at"`
	Attempts  int       `json:"attempts"`
	ErrorCode string    `json:"error_code,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

type assistantStartupEntry struct {
	AssistantAddress string                  `json:"assistant_address"`
	Phases           []assistantStartupPhase `json:"phases"`
	Output           []string                `json:"helper_output_tail,omitempty"`
}

type assistantStartupReport struct {
	mu      sync.Mutex
	path    string
	entries map[string]*assistantStartupEntry
}

var activeAssistantStartupReport struct {
	sync.Mutex
	report *assistantStartupReport
}

// openAssistantStartupReport starts a fresh report in the assistant state
// directory of a production runtime.
func openAssistantStartupReport(stateRoot string) *assistantStartupReport {
	report := &assistantStartupReport{path: filepath.Join(stateRoot, assistantStartupReportFile), entries: map[string]*assistantStartupEntry{}}
	activeAssistantStartupReport.Lock()
	activeAssistantStartupReport.report = report
	activeAssistantStartupReport.Unlock()
	return report
}

func currentAssistantStartupReport() *assistantStartupReport {
	activeAssistantStartupReport.Lock()
	defer activeAssistantStartupReport.Unlock()
	return activeAssistantStartupReport.report
}

// recordAssistantStartupPhase records one phase outcome of an assistant; it
// does nothing outside a production runtime.
func recordAssistantStartupPhase(address, phase string, ok bool, attempts int, errorCode, detail string) {
	report := currentAssistantStartupReport()
	if report == nil {
		return
	}
	report.record(address, assistantStartupPhase{Phase: phase, OK: ok, At: time.Now().UTC(), Attempts: attempts, ErrorCode: errorCode, Detail: detail})
}

func (report *assistantStartupReport) record(address string, phase assistantStartupPhase) {
	report.mu.Lock()
	defer report.mu.Unlock()
	entry := report.entry(address)
	// A repeated phase keeps one row with the latest outcome and its attempts.
	for index := range entry.Phases {
		if entry.Phases[index].Phase != phase.Phase {
			continue
		}
		phase.Attempts = max(phase.Attempts, entry.Phases[index].Attempts+1)
		entry.Phases[index] = phase
		report.writeLocked()
		return
	}
	if phase.Attempts == 0 {
		phase.Attempts = 1
	}
	entry.Phases = append(entry.Phases, phase)
	report.writeLocked()
}

// recordOutput keeps the last lines the assistant's helper wrote.
func (report *assistantStartupReport) recordOutput(address string, line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(line) > assistantStartupOutputBytes {
		line = line[:assistantStartupOutputBytes] + "…"
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	entry := report.entry(address)
	entry.Output = append(entry.Output, line)
	if len(entry.Output) > assistantStartupOutputLines {
		entry.Output = entry.Output[len(entry.Output)-assistantStartupOutputLines:]
	}
	report.writeLocked()
}

// entry returns the assistant's record; the caller holds the lock.
func (report *assistantStartupReport) entry(address string) *assistantStartupEntry {
	entry, ok := report.entries[address]
	if !ok {
		entry = &assistantStartupEntry{AssistantAddress: address}
		report.entries[address] = entry
	}
	return entry
}

// writeLocked publishes the report; a report that cannot be written changes no
// behavior. The caller holds the lock.
func (report *assistantStartupReport) writeLocked() {
	if report.path == "" {
		return
	}
	addresses := make([]string, 0, len(report.entries))
	for address := range report.entries {
		addresses = append(addresses, address)
	}
	slices.Sort(addresses)
	entries := make([]*assistantStartupEntry, 0, len(addresses))
	for _, address := range addresses {
		entries = append(entries, report.entries[address])
	}
	data, err := json.MarshalIndent(map[string]any{"kind": "scenery.assistant.startup", "assistants": entries}, "", "  ")
	if err != nil {
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(report.path), ".startup-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return
	}
	if err := temporary.Close(); err != nil {
		return
	}
	_ = os.Chmod(temporary.Name(), 0o600)
	_ = os.Rename(temporary.Name(), report.path)
}

// assistantStartupOutput forwards a helper's output into its report line by
// line, keeping only the bounded tail.
type assistantStartupOutput struct {
	report  *assistantStartupReport
	address string
	pending []byte
}

func (writer *assistantStartupOutput) Write(data []byte) (int, error) {
	if writer == nil || writer.report == nil {
		return len(data), nil
	}
	writer.pending = append(writer.pending, data...)
	for {
		index := bytes.IndexByte(writer.pending, '\n')
		if index < 0 {
			break
		}
		writer.report.recordOutput(writer.address, string(writer.pending[:index]))
		writer.pending = writer.pending[index+1:]
	}
	if len(writer.pending) > assistantStartupOutputBytes {
		writer.report.recordOutput(writer.address, string(writer.pending))
		writer.pending = nil
	}
	return len(data), nil
}
