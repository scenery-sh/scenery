package devtelemetry

import (
	"slices"
	"strings"
	"time"
)

type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Event is a development lifecycle observation in the local telemetry stream.
type Event struct {
	At          time.Time    `json:"at"`
	Kind        string       `json:"kind"`
	App         *App         `json:"app,omitempty"`
	Generation  uint64       `json:"generation"`
	Rebuild     *Rebuild     `json:"rebuild,omitempty"`
	WorkerFault *WorkerFault `json:"worker_fault,omitempty"`
}

type Rebuild struct {
	EditToReadyMS  *float64 `json:"edit_to_ready_ms,omitempty"`
	DetectMS       float64  `json:"detect_ms"`
	CompileMS      float64  `json:"compile_ms"`
	LinkMS         float64  `json:"link_ms"`
	ReadyMS        float64  `json:"ready_ms"`
	RebuiltModules []string `json:"rebuilt_modules"`
	SkippedModules []string `json:"skipped_modules"`
	TriggerKind    string   `json:"trigger_kind"`
	OK             bool     `json:"ok"`
	ErrorCode      string   `json:"error_code,omitempty"`
}

type WorkerFault struct {
	Module   string `json:"module"`
	Runtime  string `json:"runtime"`
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
}

func (e Event) Valid() bool {
	return !e.At.IsZero() && e.Generation != 0 && ((e.Kind == "rebuild" && e.Rebuild != nil && e.WorkerFault == nil) ||
		(e.Kind == "worker_fault" && e.WorkerFault != nil && e.Rebuild == nil))
}

// CommandKey normalizes saved command labels. A parent-only historical record
// cannot be attributed to a particular child because it has no arguments.
func CommandKey(command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return ""
	}
	switch words[0] {
	case "agent":
		return "system agent"
	case "status":
		return "unknown"
	case "--help":
		return "help"
	case "--version":
		return "unknown"
	}
	return strings.Join(words[:min(len(words), 2)], " ")
}

// Uptime identifies completion samples that measured the lifetime of a
// listener or stream. A readiness sample is latency even for a daemon.
func Uptime(command, mode, measurement string, longRunning *bool) bool {
	if measurement == "startup" {
		return false
	}
	if longRunning != nil {
		return *longRunning
	}
	// Older producers recorded foreground system agent completions as
	// oneshot. Their labels cannot distinguish a daemon from management.
	return mode == "long_running" || command == "system agent" ||
		(mode == "" && (command == "up" || command == "worker"))
}

// AppendRecentEvent retains a bounded tail of the telemetry stream.
func AppendRecentEvent(events []Event, event Event, limit int) []Event {
	if limit <= 0 {
		return events
	}
	if len(events) == limit {
		if event.At.Before(events[0].At) {
			return events
		}
		copy(events, events[1:])
		events = events[:limit-1]
	}
	index, _ := slices.BinarySearchFunc(events, event.At, func(entry Event, at time.Time) int {
		return entry.At.Compare(at)
	})
	return slices.Insert(events, index, event)
}
