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
	At           time.Time     `json:"at"`
	Kind         string        `json:"kind"`
	App          *App          `json:"app,omitempty"`
	Generation   uint64        `json:"generation,omitempty"`
	Rebuild      *Rebuild      `json:"rebuild,omitempty"`
	WorkerFault  *WorkerFault  `json:"worker_fault,omitempty"`
	ProxyFault   *ProxyFault   `json:"proxy_fault,omitempty"`
	RuntimeFault *RuntimeFault `json:"runtime_fault,omitempty"`
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
	TriggerBuckets []string `json:"trigger_buckets,omitempty"`
	OK             bool     `json:"ok"`
	ErrorCode      string   `json:"error_code,omitempty"`
}

type WorkerFault struct {
	Module   string `json:"module"`
	Runtime  string `json:"runtime"`
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
}

type ProxyFault struct {
	Backend    string `json:"backend"`
	Status     int    `json:"status"`
	ErrorClass string `json:"error_class"`
}

type RuntimeFault struct {
	Component  string `json:"component"`
	Target     string `json:"target,omitempty"`
	ErrorClass string `json:"error_class"`
}

func (e Event) Valid() bool {
	if e.At.IsZero() {
		return false
	}
	switch e.Kind {
	case "rebuild":
		return e.Generation != 0 && e.Rebuild != nil && e.WorkerFault == nil && e.ProxyFault == nil && e.RuntimeFault == nil
	case "worker_fault":
		return e.Generation != 0 && e.WorkerFault != nil && e.Rebuild == nil && e.ProxyFault == nil && e.RuntimeFault == nil
	case "proxy_fault":
		return e.ProxyFault != nil && e.ProxyFault.Backend != "" && e.ProxyFault.ErrorClass != "" && e.Rebuild == nil && e.WorkerFault == nil && e.RuntimeFault == nil
	case "runtime_fault":
		return e.RuntimeFault != nil && e.RuntimeFault.Component != "" && e.RuntimeFault.ErrorClass != "" && e.Rebuild == nil && e.WorkerFault == nil && e.ProxyFault == nil
	default:
		return false
	}
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
